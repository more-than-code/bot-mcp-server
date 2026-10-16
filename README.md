# bot-mcp-server

Stateless MCP gateway (Streamable HTTP at `/mcp`) for external AI agents. Every tool call is forwarded to bot-api-server with a short-lived token exchanged from the caller's user token, so permissions are exactly the console's. It also contains `deploybot login`, a device-code login CLI.

- Design: `docs/design.md` and the umbrella `docs/design-bot-agent-deploy.md` §7.
- Contract (frozen): `docs/contracts/api-v1.md`.

The server stores nothing and writes no files. The CLI writes only `~/.config/deploybot/token` (directory `0700`, file `0600`, atomic replace). The file is JSON, `{"api":"<base URL>","token":"<token>"}`.

## Run

```bash
export BOT_MCP_LISTEN=127.0.0.1:8091
export BOT_MCP_API_URL=https://<api>
export BOT_MCP_PUBLIC_ORIGIN=https://<mcp>
export BOT_MCP_CLIENT_SECRET_FILE=/path/to/client-secret
go run ./cmd/bot-mcp-server
```

`BOT_MCP_CLIENT_SECRET_FILE` is mode `0600`. The server reads the first line and refuses to start if the file is missing, empty, shorter than 32 characters, or not mode `0600`. That line is the secret for client id `bot-mcp-server`. It only allows exchanging a user token. It is never logged and never sent to `/v1/*`.

`GET /health` is unauthenticated. `GET /.well-known/oauth-protected-resource` gives the resource `<BOT_MCP_PUBLIC_ORIGIN>/mcp` (the URL clients connect to; the API's `BOT_API_MCP_RESOURCE` must equal it), names the API origin as the authorization server, and lists scope `deploybot`. `POST /mcp` requires `Authorization: Bearer <token>`. The server exchanges that token and calls the API with the delegated token only.

`BOT_MCP_API_URL` is an origin (`https://host[:port]`, optional trailing slash). A path, query, or fragment is refused at startup. `BOT_MCP_API_URL` and `BOT_MCP_PUBLIC_ORIGIN` must be `https`. Cleartext `http` is allowed only for `localhost`, `127.0.0.1`, and `::1`, or when `BOT_MCP_ALLOW_INSECURE_HTTP=1` (tailnet dev). The API client does not follow redirects.

Phase 1 tools are read-only: `list_projects`, `list_hosts`, `list_services`, `query_metrics`, `list_processes`, `service_health_history`, `host_snapshot`, `probe_service`. `host_snapshot` and `probe_service` contact the host when they are called.

Catalog tools call api-v1 v1.6. `list_catalog` reads a project's catalog and does not contact a host. It returns each entry's name, kind, `command_display`, and `my_actions`, and does not return the program spec or its environment. `entry_logs` and `entry_status` contact the host when they are called; logs and status do not need confirmation. Log lines, including any `lines` on a confirm or status result, are redacted (URL userinfo, then keywords), each line capped at 2048 bytes, and the lines together capped at 64 KiB. A `lines` value that is not an array of strings is not returned raw. They are also cut at 200 items. `run_entry`, `stop_entry`, and `restart_entry` are real, audited actions. With the delegated token the API does not act on those calls: it answers 202 with a plan and a `confirmation_id`. Any other status, including 200, is a tool error and is not shown as a program status. The tool shows that plan and the id, and says nothing ran yet. In the structured result, every confirmation string has control characters replaced, and `plan` is capped at 1024 runes. After the user agrees, `confirm` posts `{"idempotency_key"}` to `/v1/confirmations/{confirmation_id}` and returns the program status. That second call is what runs, stops, or restarts the program. Arguments are checked before any API call (catalog name, UUIDs, idempotency key, `lines` 1–1000).

Catalog image entries (api-v1 v1.8) use the same confirmation. `list_catalog` adds, for an image, the image reference, service or job, ports, volume short names, env key names, and env set names. It does not return env values, the command, the entrypoint, or volume targets. `deploy_entry` (`project_id`, `entry`, `host_id`, `idempotency_key`, optional `force`) and `rollback_entry` (those, plus `digest`) need the deploy permission, and the host must run containers. `digest` is `sha256:` and 64 lowercase hex digits. The first call does not act. `confirm` does, and for an image it returns the outcome, a short digest, the exit code, and the last log lines. Pass `project_id` and `entry` on that `confirm` (they are not sent to the API). An image confirm waits at most 50s. If the deploy or rollback is still going, the result is `still running on the host`, the action keeps running on the host, and `entry_status` shows the outcome. `confirm` must use the same `idempotency_key` as the `deploy_entry`, `rollback_entry`, run, stop or restart call that returned the confirmation; another key is `conflict` (`idempotency_key does not match the request that issued this confirmation`) and nothing runs. A program confirm, and `deploy_entry` / `rollback_entry` themselves, keep the 35s deadline. `list_catalog` returns only allowlisted fields; a catalog body that is not an object with an `items` array is an error and is not shown. `deploy_entry`'s plan includes the last 3 deploys (full digest in the structured result, short digest in the text, outcome, and time). With `force: true` the structured result has `force: true` and the plan says so. The 202 result keeps only the confirmation id, plan, expiry, history, and that flag. An image deploy result keeps the outcome, digest, exit code, status, and redacted lines. A timed-out deploy or rollback has `status.state` `unknown`, and the confirm text says unknown. `entry_status` shows unknown the same way. A state other than `running`, `exited`, `created`, `paused`, `restarting`, `dead`, `missing`, or `unknown` is not shown. `entry_status`, and a confirm body that is only a status, then return `error: status response was not usable` and do not return the body. On a deploy result the outcome stays and `status` is omitted. `command_display` and the image reference are redacted when they contain URL userinfo. There is no remove tool. No tool returns env values. `entry_logs` and `entry_status` also render an image container.

`list_hosts` includes hosts shared with the project (`relation` and `share_level` pass through; a shared host is summarized as `pi1 online (shared: metrics)`). `list_services` takes `host_id` or `project_id`; with `project_id` the query (`q`, `sort`, `dir`, `limit`, `cursor`, `view`) is passed through and the result is paged and limited to what that project may see. `view` is `all` (default when omitted), `deploybot`, or `deploybot_and_discovered`. A service's `gone_at`, when set, means the host no longer reports it. `list_processes` shows the whole host.

## deploybot

```bash
go run ./cmd/deploybot login --api https://<api>
go run ./cmd/deploybot logout
```

`--api` overrides `DEPLOYBOT_API_URL`. `--client-name` defaults to `deploybot`. `--print` writes the token to stdout instead of the token file. `--insecure-http` allows an `http` API URL that is not loopback. Exit code 2 means the device code expired. Exit code 3 means the user denied it.

`logout` sends the saved token only to the `api` stored in the token file. It deletes the file when signout succeeds or returns unauthorized. Any other signout failure keeps the file. `--force` deletes the local file anyway. An `--api` value or `DEPLOYBOT_API_URL` that differs from the saved issuer (trailing slashes ignored) is refused and the token is not sent.

## Use with Claude Code

Once OAuth is live, add the server with no token header. Claude Code discovers the protected-resource metadata and signs in in the browser:

```bash
claude mcp add --transport http deploybot https://<mcp>/mcp
```

`deploybot login` stays the headless fallback for clients without a browser:

```bash
deploybot login --api https://<api>
claude mcp add --transport http deploybot https://<mcp>/mcp --header "Authorization: Bearer $(python3 -c 'import json,pathlib; print(json.loads(pathlib.Path.home().joinpath(".config/deploybot/token").read_text())["token"])')"
```

The header is the `token` field. The file also stores the API base URL, so the raw file is not a bearer token. bot-mcp-server exchanges that token; it does not forward it to the API.
