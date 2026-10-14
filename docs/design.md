# bot-mcp-server: per-repo design

**Date:** 2026-10-13. **Status:** Approved design (D1–D31), wave 1 = phase 1.
Cross-repo design: umbrella `docs/design-bot-agent-deploy.md` §7. Contracts: `docs/contracts/api-v1.md`, `docs/contracts/oauth-v1.md`.

## Layout

```
cmd/bot-mcp-server/   MCP server: Streamable HTTP at /mcp, GET /health
cmd/deploybot/        CLI: `deploybot login` (device flow), `deploybot logout`
internal/apiclient/   typed client for api-v1 (delegated bearer on /v1)
internal/tools/       one file per tool; schemas and handlers
```

## Auth

- Every MCP HTTP request must carry `Authorization: Bearer <token>`. Missing → HTTP 401 with `WWW-Authenticate: Bearer resource_metadata="<origin>/.well-known/oauth-protected-resource"`.
- `/.well-known/oauth-protected-resource` names the bot-api-server origin (scheme, host, port) as the authorization server and advertises `scopes_supported: ["deploybot"]`. `BOT_MCP_API_URL` must be that origin: a path other than `/`, a query, or a fragment is refused at startup. Browser clients use the authorization-code flow (`docs/spec-mcp-oauth.md`, contract `docs/contracts/oauth-v1.md`). `deploybot login` stays the headless fallback.
- **Token scope (umbrella D35, confirmed by Joe):** a token is a session of the user's account (`app.sessions`, no project column). It reaches exactly the projects the user belongs to, with the user's role in each, checked live by bot-api-server's `authz.Authorize` on every call (non-member → `not_found`, role too low → `forbidden`). This is intended; there are no per-project tokens.
- **Token exchange (umbrella D36 ended).** The server holds one client secret (`BOT_MCP_CLIENT_SECRET_FILE`, mode 0600, at least 32 characters, client id `bot-mcp-server`) and never acts without a user token. For every `/mcp` request it exchanges the caller token at the API origin (`POST /oauth/token`, `client_secret_basic`, token-exchange grant, `resource` = API origin) unless a cached delegated token exists. The cache is in memory, keyed by SHA-256 of the caller token, expires 30 seconds before `expires_in`, and drops the oldest entry past 10 000. At most 256 exchanges run at once; one more distinct caller token is HTTP 503 and is not sent to the authorization server. A subject refused with `invalid_grant` or `invalid_request` is remembered for 60 seconds (same SHA-256 key, at most 10 000, oldest dropped) and answered 401 with no further exchange. A transport error or a 5xx is not remembered. `/v1/*` is called with only the delegated token. The caller token is not sent upstream and is not passed to tool code. Exchange `invalid_grant` or `invalid_request` → HTTP 401 with `WWW-Authenticate: Bearer error="invalid_token", resource_metadata=…` before any MCP handling. An unreachable authorization server or a 5xx → HTTP 503, so the client does not discard its token. A 401 from the API during a tool call drops that cache entry and is still a tool error `unauthorized: <msg>` (HTTP 200): the MCP Go SDK v1.8.0 cannot return HTTP 401 from inside a tool result. Any other API error becomes a tool error with the API's `code` and `msg`. Neither token nor the client secret is logged.

## Phase 1 tools

| Tool | Args | API |
|---|---|---|
| `list_projects` | — | GET `/v1/me` (projects) |
| `list_hosts` | `project_id` | GET `/v1/projects/{id}/hosts` |
| `list_services` | `host_id` | GET `/v1/hosts/{id}/services` |
| `query_metrics` | `host_id`, `metric`, `from?`, `to?`, `step?` | GET `/v1/hosts/{id}/metrics` |
| `list_processes` | `host_id`, `sort?`, `limit?` | GET `/v1/hosts/{id}/processes` |
| `service_health_history` | `service_id`, `from?`, `to?` | GET `/v1/services/{id}/health` |
| `host_snapshot` | `host_id` | POST `/v1/hosts/{id}/snapshot` |
| `probe_service` | `service_id`, `check_id?` or `protocol`+`port`+`path?` | POST `/v1/services/{id}/probe` |

Phase 2 (later wave): `service_logs`, `deploy_service`, `redeploy_service`, `run_service`, `stop_service`, `restart_service`, and `confirm` (plan → `confirmation_id` → confirm, D25).

## `deploybot login`

Device flow against `--api <base-url>` (or `DEPLOYBOT_API_URL`): request a code, print the URL and user code, poll at `interval` (honour `slow_down`), and save the token to `~/.config/deploybot/token` (mode 0600, directory 0700). `--print` prints the token instead of saving it. `deploybot logout` calls `/v1/auth/signout` and deletes the file.
