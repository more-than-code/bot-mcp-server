# bot-mcp-server

Stateless MCP gateway (Streamable HTTP at `/mcp`) for external AI agents. Every tool call is forwarded to bot-api-server with the caller's own user token, so permissions are exactly the console's. It also contains `deploybot login`, a device-code login CLI.

- Design: `docs/design.md` and the umbrella `docs/design-bot-agent-deploy.md` §7.
- Contract (frozen): `docs/contracts/api-v1.md`.
