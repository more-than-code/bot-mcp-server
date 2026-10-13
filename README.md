# bot-mcp-server

Stateless MCP gateway (Streamable HTTP at `/mcp`) for external AI agents. Every tool call is forwarded to bot-api-server with the caller's own user token, so permissions are exactly the console's. It also contains `deploybot login`, a device-code login CLI.

- Design: `docs/design.md` and the umbrella `docs/design-bot-agent-deploy.md` §7.
- Contract (frozen): `docs/contracts/api-v1.md`.

The server stores nothing and writes no files. The CLI writes only `~/.config/deploybot/token` (directory `0700`, file `0600`, atomic replace). The file is JSON, `{"api":"<base URL>","token":"<token>"}`.

## Run

```bash
export BOT_MCP_LISTEN=127.0.0.1:8091
export BOT_MCP_API_URL=https://<api>
export BOT_MCP_PUBLIC_ORIGIN=https://<mcp>
go run ./cmd/bot-mcp-server
```

`GET /health` is unauthenticated. `GET /.well-known/oauth-protected-resource` names `BOT_MCP_API_URL` as the authorization server. `POST /mcp` requires `Authorization: Bearer <token>`.

`BOT_MCP_API_URL` and `BOT_MCP_PUBLIC_ORIGIN` must be `https`. Cleartext `http` is allowed only for `localhost`, `127.0.0.1`, and `::1`, or when `BOT_MCP_ALLOW_INSECURE_HTTP=1` (tailnet dev). The API client does not follow redirects.

Phase 1 tools are read-only: `list_projects`, `list_hosts`, `list_services`, `query_metrics`, `list_processes`, `service_health_history`, `host_snapshot`, `probe_service`. `host_snapshot` and `probe_service` contact the host when they are called.

## deploybot

```bash
go run ./cmd/deploybot login --api https://<api>
go run ./cmd/deploybot logout
```

`--api` overrides `DEPLOYBOT_API_URL`. `--client-name` defaults to `deploybot`. `--print` writes the token to stdout instead of the token file. `--insecure-http` allows an `http` API URL that is not loopback. Exit code 2 means the device code expired. Exit code 3 means the user denied it.

`logout` sends the saved token only to the `api` stored in the token file. It deletes the file when signout succeeds or returns unauthorized. Any other signout failure keeps the file. `--force` deletes the local file anyway. An `--api` value or `DEPLOYBOT_API_URL` that differs from the saved issuer (trailing slashes ignored) is refused and the token is not sent.

## Use with Claude Code

```bash
deploybot login --api https://<api>
claude mcp add --transport http deploybot https://<mcp>/mcp --header "Authorization: Bearer $(python3 -c 'import json,pathlib; print(json.loads(pathlib.Path.home().joinpath(".config/deploybot/token").read_text())["token"])')"
```

The header is the `token` field. The file also stores the API base URL, so the raw file is not a bearer token.
