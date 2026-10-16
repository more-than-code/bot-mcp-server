package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const confirmSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["confirmation_id", "idempotency_key"],
  "properties": {
    "confirmation_id": {"type": "string", "pattern": "^[A-Za-z0-9_-]{43}$"},
    "idempotency_key": {"type": "string", "pattern": "^[A-Za-z0-9._:-]{1,128}$"},
    "project_id": {"type": "string", "format": "uuid"},
    "entry": {"type": "string", "pattern": "^[a-z0-9][a-z0-9-]{0,47}$"}
  }
}`

func registerConfirm(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "confirm",
		Description: "Perform a pending catalog run, stop, restart, deploy, or rollback after the user agrees. The action is real and is audited. This contacts the host now. Pass the same idempotency_key as the run, stop, restart, deploy_entry, or rollback_entry call that returned this confirmation; a different key is refused and nothing runs. A program confirm waits up to 30 seconds. For a deploy or rollback, pass project_id and entry; the confirm waits up to 50 seconds. Returns the program status, or for an image the outcome, short digest, exit code, and last log lines. A deploy or rollback still going after 50 seconds keeps running on the host: the result is \"still running on the host\"; check entry_status for the outcome.",
		Annotations: mutatingAnnotations(true),
		InputSchema: json.RawMessage(confirmSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct {
			ConfirmationID string `json:"confirmation_id"`
			IdempotencyKey string `json:"idempotency_key"`
			ProjectID      string `json:"project_id"`
			Entry          string `json:"entry"`
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
		if (in.ProjectID == "") != (in.Entry == "") {
			return badRequest(errors.New("project_id and entry are both required to confirm a deploy or rollback"))
		}
		if in.ProjectID != "" {
			if err := requireUUID("project_id", in.ProjectID); err != nil {
				return badRequest(err)
			}
			if err := requireEntryName(in.Entry); err != nil {
				return badRequest(err)
			}
		}
		path := idPath("v1", "confirmations", in.ConfirmationID)
		body := struct {
			IdempotencyKey string `json:"idempotency_key"`
		}{IdempotencyKey: in.IdempotencyKey}
		// project_id and entry are not sent. They only choose the wait.
		// The API body stays {"idempotency_key"}.
		var (
			status  int
			payload json.RawMessage
			fail    *mcp.CallToolResult
		)
		if in.ProjectID == "" {
			status, payload, fail = postStatus(ctx, api, path, body)
		} else {
			wait := confirmWaitFor(ctx, api, in.ProjectID, in.Entry)
			if wait.deploy {
				extendWriteBudget(ctx, wait.limit)
				status, payload, fail = postWithin(ctx, api, path, body, wait.limit)
			} else {
				status, payload, fail = postStatus(ctx, api, path, body)
			}
		}
		if fail != nil {
			return fail
		}
		return actedOrPending(status, payload)
	})
}
