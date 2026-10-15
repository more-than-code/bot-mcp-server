package tools

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const entryLogsSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["project_id", "entry", "host_id"],
  "properties": {
    "project_id": {"type": "string", "format": "uuid"},
    "entry": {"type": "string", "pattern": "^[a-z0-9][a-z0-9-]{0,47}$"},
    "host_id": {"type": "string", "format": "uuid"},
    "lines": {"type": "integer", "minimum": 1, "maximum": 1000}
  }
}`

func registerEntryLogs(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "entry_logs",
		Description: "Read recent log lines for a catalog program or image container on a host. Optional lines is 1 to 1000 (default 200 when omitted). This contacts the host now and waits for the agent (up to 30 seconds). Fails if the host is offline or asleep.",
		Annotations: readOnlyAnnotations(true),
		InputSchema: json.RawMessage(entryLogsSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct {
			ProjectID string `json:"project_id"`
			Entry     string `json:"entry"`
			HostID    string `json:"host_id"`
			Lines     *int   `json:"lines"`
		}
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := validateEntryLocation(in.ProjectID, in.Entry, in.HostID); err != nil {
			return badRequest(err)
		}
		if err := optionalLines(in.Lines); err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "projects", in.ProjectID, "catalog", in.Entry, "logs")
		body := struct {
			HostID string `json:"host_id"`
			Lines  *int   `json:"lines,omitempty"`
		}{HostID: in.HostID, Lines: in.Lines}
		payload, fail := post(ctx, api, path, body)
		if fail != nil {
			return fail
		}
		payload = redactLines(payload)
		return present(payload, summarizeProgramLogs(payload))
	})
}
