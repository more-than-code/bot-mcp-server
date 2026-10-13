# bot-mcp-server

Go service and CLI. Umbrella: `/Users/joe/workspace/deploybot` (cross-repo design `docs/design-bot-agent-deploy.md`).

No database. No credentials of its own. Never call bot-api-server with anything but the caller's token. The contracts in `docs/contracts/` are frozen.

```bash
go build ./... && go vet ./... && go test ./...
```
