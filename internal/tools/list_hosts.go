package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const listHostsSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["project_id"],
  "properties": {
    "project_id": {"type": "string", "format": "uuid"}
  }
}`

func registerListHosts(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "list_hosts",
		Description: "List hosts in a project. Reads stored host records (id, name, status, last seen, and related fields). Does not contact the hosts.",
		Annotations: readOnlyAnnotations(false),
		InputSchema: json.RawMessage(listHostsSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct {
			ProjectID string `json:"project_id"`
		}
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := requireUUID("project_id", in.ProjectID); err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "projects", in.ProjectID, "hosts")
		payload, fail := get(ctx, api, path, nil)
		if fail != nil {
			return fail
		}
		return present(payload, summarizeHosts(payload))
	})
}
