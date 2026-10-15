package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const entryStatusSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["project_id", "entry", "host_id"],
  "properties": {
    "project_id": {"type": "string", "format": "uuid"},
    "entry": {"type": "string", "pattern": "^[a-z0-9][a-z0-9-]{0,47}$"},
    "host_id": {"type": "string", "format": "uuid"}
  }
}`

func registerEntryStatus(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "entry_status",
		Description: "Read the live status of a catalog program or image container on a host. This contacts the host now and waits for the agent (up to 30 seconds). For a program, returns the unit state, pid, start time, and ports. For an image, returns the container state, digest, and exit code. Fails if the host is offline or asleep.",
		Annotations: readOnlyAnnotations(true),
		InputSchema: json.RawMessage(entryStatusSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct {
			ProjectID string `json:"project_id"`
			Entry     string `json:"entry"`
			HostID    string `json:"host_id"`
		}
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := validateEntryLocation(in.ProjectID, in.Entry, in.HostID); err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "projects", in.ProjectID, "catalog", in.Entry, "status")
		body := struct {
			HostID string `json:"host_id"`
		}{HostID: in.HostID}
		payload, fail := post(ctx, api, path, body)
		if fail != nil {
			return fail
		}
		if imageStateRejected(payload) {
			return toolError("error", errStatusShape.Error())
		}
		return present(payload, summarizeEntryStatus(payload))
	})
}
