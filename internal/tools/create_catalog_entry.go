package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

func registerCreateCatalogEntry(server *mcp.Server, api *apiclient.Client) {
	addTool(server, catalogWriteTool(
		"create_catalog_entry",
		"Create an image catalog entry. Nothing changes until confirm after the user agrees. Program entries are console-only. Secret env values go in env sets entered in the console, not in spec.env. spec.healthcheck is optional; when set, test is required and the API checks the remaining rules and fills defaults. healthcheck.test is the command and its arguments, e.g. [\"redis-cli\",\"ping\"]; Deploybot adds Docker's CMD, so do not include CMD or CMD-SHELL; for a shell use [\"sh\",\"-c\",\"…\"]. spec.logging is optional; Deploybot defaults to 10 MB × 3 files, and an override sets both max_size_mb (1–100) and max_files (1–10). spec.health_policy is optional and for services only: on_deploy_failure rollback (default), stop or keep; on_unhealthy none (default), restart or rollback, which needs spec.healthcheck and cannot be combined with on_deploy_failure keep; unhealthy_after_sec (15–600, default 45) and max_actions_per_hour (1–10, default 3). A host whose agent lacks health policy support refuses a non-default policy at deploy time. Does not contact the host.",
		catalogWriteSchema,
	), func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in catalogWriteArgs
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := validateCatalogWrite(in.ProjectID, in.Name, in.IdempotencyKey, in.Spec); err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "projects", in.ProjectID, "catalog")
		body := catalogCreateBody{Name: in.Name, Kind: "image", Spec: in.Spec, IdempotencyKey: in.IdempotencyKey}
		status, payload, fail := postStatus(ctx, api, path, body)
		if fail != nil {
			return fail
		}
		return confirmationCatalog(status, payload)
	})
}
