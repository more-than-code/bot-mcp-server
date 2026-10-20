package tools

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const serviceHealthSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["service_id"],
  "properties": {
    "service_id": {"type": "string", "format": "uuid"},
    "from": {"type": "string", "format": "date-time"},
    "to": {"type": "string", "format": "date-time"}
  }
}`

func registerServiceHealthHistory(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "service_health_history",
		Description: "Read stored health-check results for a service. A row from Docker health has protocol docker (result success or unhealthy; check_id, port, http_status, and latency_ms are null). Other rows are Deploybot checks. Optional from and to are RFC3339 UTC timestamps. Does not contact the host.",
		Annotations: readOnlyAnnotations(false),
		InputSchema: json.RawMessage(serviceHealthSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct {
			ServiceID string  `json:"service_id"`
			From      *string `json:"from"`
			To        *string `json:"to"`
		}
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := requireUUID("service_id", in.ServiceID); err != nil {
			return badRequest(err)
		}
		from, err := optionalTime("from", in.From)
		if err != nil {
			return badRequest(err)
		}
		to, err := optionalTime("to", in.To)
		if err != nil {
			return badRequest(err)
		}
		if err := requireSpan(from, to, 0); err != nil {
			return badRequest(err)
		}
		query := url.Values{}
		if from != "" {
			query.Set("from", from)
		}
		if to != "" {
			query.Set("to", to)
		}
		path := idPath("v1", "services", in.ServiceID, "health")
		payload, fail := get(ctx, api, path, query)
		if fail != nil {
			return fail
		}
		return present(payload, summarizeHealth(payload))
	})
}
