package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const deployEntrySchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["project_id", "entry", "host_id", "idempotency_key"],
  "properties": {
    "project_id": {"type": "string", "format": "uuid"},
    "entry": {"type": "string", "pattern": "^[a-z0-9][a-z0-9-]{0,47}$"},
    "host_id": {"type": "string", "format": "uuid"},
    "idempotency_key": {"type": "string", "pattern": "^[A-Za-z0-9._:-]{1,128}$"},
    "force": {"type": "boolean"}
  }
}`

func registerDeployEntry(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "deploy_entry",
		Description: "Deploy a catalog image entry on a host. Requires the deploy permission. The host must run containers. The action is real and is audited. Nothing runs until a second confirm call with the returned confirmation id, after the user agrees. This call does not contact the host. The result lists the last 3 deploys so a rollback digest can be chosen.",
		Annotations: mutatingAnnotations(false),
		InputSchema: json.RawMessage(deployEntrySchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct {
			ProjectID      string `json:"project_id"`
			Entry          string `json:"entry"`
			HostID         string `json:"host_id"`
			IdempotencyKey string `json:"idempotency_key"`
			Force          *bool  `json:"force"`
		}
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := validateEntryAction(in.ProjectID, in.Entry, in.HostID, in.IdempotencyKey); err != nil {
			return badRequest(err)
		}
		// This POST only reserves a confirmation. The long wait is confirm.
		path := idPath("v1", "projects", in.ProjectID, "catalog", in.Entry, "deploy")
		body := struct {
			HostID         string `json:"host_id"`
			Force          *bool  `json:"force,omitempty"`
			IdempotencyKey string `json:"idempotency_key"`
		}{HostID: in.HostID, Force: in.Force, IdempotencyKey: in.IdempotencyKey}
		status, payload, fail := postStatus(ctx, api, path, body)
		if fail != nil {
			return fail
		}
		if status != http.StatusAccepted {
			return confirmationOnly(status, payload)
		}
		if hist, ok := fetchDeploys(ctx, api, in.ProjectID, in.Entry, in.HostID); ok {
			payload = attachHistory(payload, hist)
		}
		if in.Force != nil && *in.Force {
			payload = markForce(payload)
		}
		return pendingConfirmation(payload)
	})
}

// markForce records that this confirmation was reserved with force.
// projectConfirmation copies the flag onto the structured result and the plan.
func markForce(raw json.RawMessage) json.RawMessage {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return raw
	}
	body["force"] = true
	out, err := json.Marshal(body)
	if err != nil {
		return raw
	}
	return out
}

func fetchDeploys(ctx context.Context, api *apiclient.Client, projectID, entry, hostID string) (json.RawMessage, bool) {
	path := idPath("v1", "projects", projectID, "catalog", entry, "deploys")
	q := url.Values{}
	q.Set("host_id", hostID)
	payload, fail := get(ctx, api, path, q)
	if fail != nil {
		return nil, false
	}
	return payload, true
}

// attachHistory keeps digest, outcome, and time for the newest 3 rows.
// The digest stays full so rollback_entry can use it. Other fields, including
// anything that could carry an env value, are dropped.
func attachHistory(raw, deploys json.RawMessage) json.RawMessage {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return raw
	}
	body["history"] = projectHistory(deploys)
	out, err := json.Marshal(body)
	if err != nil {
		return raw
	}
	return out
}

func projectHistory(raw json.RawMessage) []any {
	var body struct {
		Items []struct {
			Digest     string `json:"digest"`
			Outcome    string `json:"outcome"`
			StartedAt  string `json:"started_at"`
			FinishedAt string `json:"finished_at"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return []any{}
	}
	out := make([]any, 0, 3)
	for _, item := range body.Items {
		if len(out) == 3 {
			break
		}
		if item.Digest == "" && item.Outcome == "" {
			continue
		}
		when := item.FinishedAt
		if when == "" {
			when = item.StartedAt
		}
		out = append(out, map[string]any{
			"digest":  item.Digest,
			"outcome": item.Outcome,
			"time":    when,
		})
	}
	return out
}
