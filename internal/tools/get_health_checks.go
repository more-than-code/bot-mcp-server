package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const getHealthChecksSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["service_id"],
  "properties": {
    "service_id": {"type": "string", "format": "uuid"}
  }
}`

// errServiceShape is fixed. It must not include the service body.
var errServiceShape = errors.New("service response was not usable")

func registerGetHealthChecks(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "get_health_checks",
		Description: "Read the current health checks for a service, plus its name, ports, and health. Does not contact the host.",
		Annotations: readOnlyAnnotations(false),
		InputSchema: json.RawMessage(getHealthChecksSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct {
			ServiceID string `json:"service_id"`
		}
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := requireUUID("service_id", in.ServiceID); err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "services", in.ServiceID)
		payload, fail := get(ctx, api, path, nil)
		if fail != nil {
			return fail
		}
		projected, err := projectServiceHealthChecks(payload)
		if err != nil {
			return toolError("error", err.Error())
		}
		return present(projected, summarizeGetHealthChecks(projected))
	})
}

func summarizeGetHealthChecks(payload json.RawMessage) string {
	var body struct {
		Name   string            `json:"name"`
		Checks []json.RawMessage `json:"health_checks"`
	}
	_ = json.Unmarshal(payload, &body)
	if body.Name == "" {
		return fmt.Sprintf("%d health checks", len(body.Checks))
	}
	return fmt.Sprintf("%d health checks on %s", len(body.Checks), summaryField(body.Name))
}

// projectServiceHealthChecks keeps health_checks, name, ports, and health.
// Check strings use the same redaction as a health-check confirm. service_id
// on a check is dropped. Anything else on the service is dropped.
func projectServiceHealthChecks(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errServiceShape
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return nil, errServiceShape
	}
	items, err := serviceHealthCheckItems(body["health_checks"])
	if err != nil {
		return nil, err
	}
	outItems := make([]any, len(items))
	for i, item := range items {
		outItems[i] = projectGetHealthCheck(item)
	}
	out := map[string]any{"health_checks": outItems}
	if name, ok := body["name"].(string); ok {
		out["name"] = publicString(name)
	}
	if ports := numberList(body["ports"]); ports != nil {
		out["ports"] = ports
	}
	if health, ok := projectServiceHealth(body["health"]); ok {
		out["health"] = health
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, errServiceShape
	}
	return encoded, nil
}

func serviceHealthCheckItems(v any) ([]any, error) {
	if v == nil {
		return []any{}, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, errServiceShape
	}
	return items, nil
}

func projectServiceHealth(v any) (map[string]any, bool) {
	row, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	out := map[string]any{}
	for _, key := range []string{"status", "last_checked_at"} {
		if s, ok := row[key].(string); ok {
			out[key] = publicString(s)
		}
	}
	if n, ok := row["latency_ms"].(json.Number); ok {
		out["latency_ms"] = n
	}
	return out, true
}
