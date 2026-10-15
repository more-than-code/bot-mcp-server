package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const rollbackEntrySchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["project_id", "entry", "host_id", "digest", "idempotency_key"],
  "properties": {
    "project_id": {"type": "string", "format": "uuid"},
    "entry": {"type": "string", "pattern": "^[a-z0-9][a-z0-9-]{0,47}$"},
    "host_id": {"type": "string", "format": "uuid"},
    "digest": {"type": "string", "pattern": "^sha256:[0-9a-f]{64}$"},
    "idempotency_key": {"type": "string", "pattern": "^[A-Za-z0-9._:-]{1,128}$"}
  }
}`

func registerRollbackEntry(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "rollback_entry",
		Description: "Roll a catalog image entry back to a digest on a host. Requires the deploy permission. The host must run containers. digest is sha256: followed by 64 lowercase hex digits. The action is real and is audited. Nothing runs until a second confirm call with the returned confirmation id, after the user agrees. This call does not contact the host.",
		Annotations: mutatingAnnotations(false),
		InputSchema: json.RawMessage(rollbackEntrySchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct {
			ProjectID      string `json:"project_id"`
			Entry          string `json:"entry"`
			HostID         string `json:"host_id"`
			Digest         string `json:"digest"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := validateEntryAction(in.ProjectID, in.Entry, in.HostID, in.IdempotencyKey); err != nil {
			return badRequest(err)
		}
		if err := requireDigest(in.Digest); err != nil {
			return badRequest(err)
		}
		// This POST only reserves a confirmation. The long wait is confirm.
		path := idPath("v1", "projects", in.ProjectID, "catalog", in.Entry, "rollback")
		body := struct {
			HostID         string `json:"host_id"`
			Digest         string `json:"digest"`
			IdempotencyKey string `json:"idempotency_key"`
		}{HostID: in.HostID, Digest: in.Digest, IdempotencyKey: in.IdempotencyKey}
		status, payload, fail := postStatus(ctx, api, path, body)
		if fail != nil {
			return fail
		}
		return confirmationOnly(status, payload)
	})
}
