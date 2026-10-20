package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

func registerUpdateCatalogEntry(server *mcp.Server, api *apiclient.Client) {
	addTool(server, catalogWriteTool(
		"update_catalog_entry",
		"Replace an image catalog entry. Nothing changes until confirm after the user agrees. Read the entry with list_catalog first. Program entries are console-only. Secret env values go in env sets entered in the console, not in spec.env. Does not contact the host.",
		catalogWriteSchema,
	), func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in catalogWriteArgs
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := validateCatalogWrite(in.ProjectID, in.Name, in.IdempotencyKey, in.Spec); err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "projects", in.ProjectID, "catalog", in.Name)
		body := catalogReplaceBody{Kind: "image", Spec: in.Spec, IdempotencyKey: in.IdempotencyKey}
		status, payload, fail := putStatus(ctx, api, path, body)
		if fail != nil {
			return fail
		}
		return confirmationOnly(status, payload)
	})
}
