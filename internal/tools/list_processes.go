package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const listProcessesSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["host_id"],
  "properties": {
    "host_id": {"type": "string", "format": "uuid"},
    "sort": {"type": "string", "enum": ["cpu", "memory"]},
    "limit": {"type": "integer", "minimum": 1, "maximum": 200}
  }
}`

func registerListProcesses(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "list_processes",
		Description: "Read the latest stored process sample for a host (at most 90 seconds old). Optional sort is cpu (default) or memory. Optional limit is 1–200. Does not contact the host.",
		Annotations: readOnlyAnnotations(false),
		InputSchema: json.RawMessage(listProcessesSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct {
			HostID string  `json:"host_id"`
			Sort   *string `json:"sort"`
			Limit  *int    `json:"limit"`
		}
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := requireUUID("host_id", in.HostID); err != nil {
			return badRequest(err)
		}
		if in.Sort != nil && *in.Sort != "cpu" && *in.Sort != "memory" {
			return badRequest(errors.New("sort must be cpu or memory"))
		}
		if in.Limit != nil && (*in.Limit < 1 || *in.Limit > 200) {
			return badRequest(errors.New("limit must be between 1 and 200"))
		}
		query := url.Values{}
		if in.Sort != nil {
			query.Set("sort", *in.Sort)
		}
		if in.Limit != nil {
			query.Set("limit", strconv.Itoa(*in.Limit))
		}
		path := idPath("v1", "hosts", in.HostID, "processes")
		payload, fail := get(ctx, api, path, query)
		if fail != nil {
			return fail
		}
		return present(payload, summarizeProcesses(payload))
	})
}
