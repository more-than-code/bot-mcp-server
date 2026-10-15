package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const entryActionSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["project_id", "entry", "host_id", "idempotency_key"],
  "properties": {
    "project_id": {"type": "string", "format": "uuid"},
    "entry": {"type": "string", "pattern": "^[a-z0-9][a-z0-9-]{0,47}$"},
    "host_id": {"type": "string", "format": "uuid"},
    "idempotency_key": {"type": "string", "pattern": "^[A-Za-z0-9._:-]{1,128}$"}
  }
}`

type entryActionArgs struct {
	ProjectID      string `json:"project_id"`
	Entry          string `json:"entry"`
	HostID         string `json:"host_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

type entryActionBody struct {
	HostID         string `json:"host_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

func registerEntryAction(server *mcp.Server, api *apiclient.Client, name, verb, description string) {
	addTool(server, &mcp.Tool{
		Name:        name,
		Description: description,
		Annotations: mutatingAnnotations(false),
		InputSchema: json.RawMessage(entryActionSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in entryActionArgs
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := validateEntryAction(in.ProjectID, in.Entry, in.HostID, in.IdempotencyKey); err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "projects", in.ProjectID, "catalog", in.Entry, verb)
		body := entryActionBody{HostID: in.HostID, IdempotencyKey: in.IdempotencyKey}
		status, payload, fail := postStatus(ctx, api, path, body)
		if fail != nil {
			return fail
		}
		return confirmationOnly(status, payload)
	})
}
