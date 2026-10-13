package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const hostSnapshotSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["host_id"],
  "properties": {
    "host_id": {"type": "string", "format": "uuid"}
  }
}`

func registerHostSnapshot(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "host_snapshot",
		Description: "Take a live snapshot of a host. This contacts the host now and waits for the agent (up to 30 seconds). Returns current CPU, memory, disks, load, temperature, uptime, and the top processes. Fails if the host is offline or asleep.",
		Annotations: readOnlyAnnotations(true),
		InputSchema: json.RawMessage(hostSnapshotSchema),
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
		path := idPath("v1", "hosts", in.HostID, "snapshot")
		payload, fail := post(ctx, api, path, nil)
		if fail != nil {
			return fail
		}
		return present(payload, summarizeSnapshot(payload))
	})
}
