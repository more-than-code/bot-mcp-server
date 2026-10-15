package tools

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const listCatalogSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["project_id"],
  "properties": {
    "project_id": {"type": "string", "format": "uuid"}
  }
}`

func registerListCatalog(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "list_catalog",
		Description: "List catalog entries in a project. Returns name, kind, command_display, and the actions the caller may take. Does not return the program spec or its environment. Does not contact the host.",
		Annotations: readOnlyAnnotations(false),
		InputSchema: json.RawMessage(listCatalogSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in struct {
			ProjectID string `json:"project_id"`
		}
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := requireUUID("project_id", in.ProjectID); err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "projects", in.ProjectID, "catalog")
		payload, fail := get(ctx, api, path, nil)
		if fail != nil {
			return fail
		}
		payload = projectCatalog(payload)
		return present(payload, summarizeCatalog(payload))
	})
}

// projectCatalog keeps name, kind, command_display, and my_actions.
// spec (env and args) is dropped. Kept strings have controls replaced.
func projectCatalog(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return raw
	}
	items, ok := body["items"].([]any)
	if !ok {
		return raw
	}
	projected := make([]any, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		projected = append(projected, map[string]any{
			"name":            replaceControls(stringField(row, "name")),
			"kind":            replaceControls(stringField(row, "kind")),
			"command_display": replaceControls(stringField(row, "command_display")),
			"my_actions":      row["my_actions"],
		})
	}
	body["items"] = projected
	out, err := json.Marshal(body)
	if err != nil {
		return raw
	}
	return out
}

func stringField(row map[string]any, key string) string {
	s, _ := row[key].(string)
	return s
}
