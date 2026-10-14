package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const listServicesSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["host_id"],
  "properties": {
    "host_id": {"type": "string", "format": "uuid"}
  }
}`

func registerListServices(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "list_services",
		Description: "List services on a host. Reads stored service records, including the last known health status. Results are limited to what the caller's projects may see. Does not contact the host.",
		Annotations: readOnlyAnnotations(false),
		InputSchema: json.RawMessage(listServicesSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct {
			HostID string `json:"host_id"`
		}
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := requireUUID("host_id", in.HostID); err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "hosts", in.HostID, "services")
		payload, fail := get(ctx, api, path, nil)
		if fail != nil {
			return fail
		}
		return present(payload, summarizeServices(payload))
	})
}
