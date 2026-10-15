package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const confirmSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["confirmation_id", "idempotency_key"],
  "properties": {
    "confirmation_id": {"type": "string", "pattern": "^[A-Za-z0-9_-]{43}$"},
    "idempotency_key": {"type": "string", "pattern": "^[A-Za-z0-9._:-]{1,128}$"}
  }
}`

func registerConfirm(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "confirm",
		Description: "Perform a pending catalog run, stop, or restart after the user agrees. The action is real and is audited. This contacts the host now and waits for the agent (up to 30 seconds), then returns the program status.",
		Annotations: mutatingAnnotations(true),
		InputSchema: json.RawMessage(confirmSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct {
			ConfirmationID string `json:"confirmation_id"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := requireConfirmationID(in.ConfirmationID); err != nil {
			return badRequest(err)
		}
		if err := requireIdempotencyKey(in.IdempotencyKey); err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "confirmations", in.ConfirmationID)
		body := struct {
			IdempotencyKey string `json:"idempotency_key"`
		}{IdempotencyKey: in.IdempotencyKey}
		status, payload, fail := postStatus(ctx, api, path, body)
		if fail != nil {
			return fail
		}
		return actedOrPending(status, payload)
	})
}
