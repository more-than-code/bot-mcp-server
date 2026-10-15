package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

func registerStopEntry(server *mcp.Server, api *apiclient.Client) {
	registerEntryAction(server, api, "stop_entry", "stop",
		"Stop a catalog program entry on a host. The action is real and is audited. Nothing runs until a second confirm call with the returned confirmation id, after the user agrees. This call does not contact the host.")
}
