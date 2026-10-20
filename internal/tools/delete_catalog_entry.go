package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

func registerDeleteCatalogEntry(server *mcp.Server, api *apiclient.Client) {
	addTool(server, catalogWriteTool(
		"delete_catalog_entry",
		"Delete a catalog entry. Nothing changes until confirm after the user agrees. A program entry is console-only. Running containers are not stopped. Does not contact the host.",
		catalogDeleteSchema,
	), func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in catalogDeleteArgs
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := validateCatalogDelete(in.ProjectID, in.Name, in.IdempotencyKey); err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "projects", in.ProjectID, "catalog", in.Name)
		status, payload, fail := deleteStatus(ctx, api, path, catalogDeleteBody{IdempotencyKey: in.IdempotencyKey})
		if fail != nil {
			return fail
		}
		return confirmationOnly(status, payload)
	})
}
