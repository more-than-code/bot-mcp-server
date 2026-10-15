package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

func registerRestartEntry(server *mcp.Server, api *apiclient.Client) {
	registerEntryAction(server, api, "restart_entry", "restart",
		"Restart a catalog program entry on a host. The action is real and is audited. Nothing runs until a second confirm call with the returned confirmation id, after the user agrees. This call does not contact the host.")
}
