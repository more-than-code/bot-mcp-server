# bot-mcp-server: per-repo design

**Date:** 2026-10-13. **Status:** Approved design (D1–D31), wave 1 = phase 1.
Cross-repo design: umbrella `docs/design-bot-agent-deploy.md` §7. Contract: `docs/contracts/api-v1.md`.

## Layout

```
cmd/bot-mcp-server/   MCP server: Streamable HTTP at /mcp, GET /health
cmd/deploybot/        CLI: `deploybot login` (device flow), `deploybot logout`
internal/apiclient/   typed client for api-v1 (Bearer passthrough)
internal/tools/       one file per tool; schemas and handlers
```

## Auth

- Every MCP HTTP request must carry `Authorization: Bearer <token>`. Missing → HTTP 401 with `WWW-Authenticate: Bearer resource_metadata="<origin>/.well-known/oauth-protected-resource"`.
- `/.well-known/oauth-protected-resource` names bot-api-server as the authorization server. The browser authorization-code flow for MCP clients is wave 2. In wave 1, clients use `deploybot login`.
- The token is forwarded unchanged to bot-api-server, which decides everything. A 401 from the API during a tool call becomes a tool error `unauthorized: <msg>` (HTTP 200): the MCP Go SDK v1.8.0 cannot return HTTP 401 from inside a tool result. Any other API error becomes a tool error with the API's `code` and `msg`.

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
