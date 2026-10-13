package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const listProjectsSchema = `{"type":"object","additionalProperties":false}`

func registerListProjects(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "list_projects",
		Description: "List the projects the signed-in user belongs to. Reads the caller's profile and project memberships (id, name, slug, role) from stored API data. Does not contact any host.",
		Annotations: readOnlyAnnotations(false),
		InputSchema: json.RawMessage(listProjectsSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct{}
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		payload, fail := get(ctx, api, "/v1/me", nil)
		if fail != nil {
			return fail
		}
		return present(payload, summarizeProjects(payload))
	})
}
