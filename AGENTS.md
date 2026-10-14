# bot-mcp-server

Go service and CLI. Umbrella: `/Users/joe/workspace/deploybot` (cross-repo design `docs/design-bot-agent-deploy.md`).

No database. One client secret that only allows exchanging user tokens; never act without a user token. Calls to bot-api-server `/v1` use only the delegated token from that exchange. The contracts in `docs/contracts/` are frozen.

```bash
go build ./... && go vet ./... && go test ./...
```
