package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

// errCatalogShape is fixed. It must not include the catalog body.
var errCatalogShape = errors.New("catalog response was not usable")

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
		Description: "List catalog entries in a project. Returns name, kind, command_display, and the actions the caller may take. An image entry also returns the image reference, service or job, ports, volume short names, managed file names, env key names, env set names, healthcheck true or false (not the healthcheck command), and logging as max_size_mb and max_files or null. It does not return env values, file contents, or file targets. Does not return the program spec or its environment. Does not contact the host.",
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
		projected, err := projectCatalog(payload)
		if err != nil {
			return toolError("error", errCatalogShape.Error())
		}
		return present(projected, summarizeCatalog(projected))
	})
}

// projectCatalog returns only an items array of allowlisted fields.
// A program keeps name, kind, command_display, and my_actions.
// An image also keeps the image reference, service or job, ports, volume
// short names, file names, env key names, env set names, healthcheck
// true or false, and logging as max_size_mb and max_files or null.
// The healthcheck command is not copied. Values are not copied.
// Anything that is not that shape is an error. The raw body is never returned.
func projectCatalog(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errCatalogShape
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return nil, errCatalogShape
	}
	items, ok := body["items"].([]any)
	if !ok {
		return nil, errCatalogShape
	}
	projected := make([]any, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if stringField(row, "kind") == "image" {
			projected = append(projected, projectImageEntry(row))
			continue
		}
		projected = append(projected, map[string]any{
			"name":            replaceControls(stringField(row, "name")),
			"kind":            replaceControls(stringField(row, "kind")),
			"command_display": publicString(stringField(row, "command_display")),
			"my_actions":      stringList(row["my_actions"]),
		})
	}
	out, err := json.Marshal(map[string]any{"items": projected})
	if err != nil {
		return nil, errCatalogShape
	}
	return out, nil
}

// publicString redacts URL userinfo and token-shaped text, then replaces controls.
func publicString(s string) string {
	return replaceControls(redactText(s))
}

func stringField(row map[string]any, key string) string {
	if row == nil {
		return ""
	}
	s, _ := row[key].(string)
	return s
}

func projectImageEntry(row map[string]any) map[string]any {
	spec, _ := row["spec"].(map[string]any)
	image := stringField(spec, "image")
	if image == "" {
		image = stringField(row, "command_display")
	}
	return map[string]any{
		"name":            replaceControls(stringField(row, "name")),
		"kind":            replaceControls(stringField(row, "kind")),
		"command_display": publicString(stringField(row, "command_display")),
		"image":           publicString(image),
		"image_kind":      replaceControls(stringField(spec, "kind")),
		"ports":           projectPorts(spec["ports"]),
		"volumes":         namesOf(spec["volumes"], "name"),
		"files":           namesOf(spec["files"], "file"),
		"env_keys":        envKeyNames(spec),
		"env_sets":        stringList(spec["env_sets"]),
		"healthcheck":     imageHasHealthcheck(spec),
		"logging":         projectLogging(spec),
		"my_actions":      stringList(row["my_actions"]),
	}
}

// projectLogging keeps max_size_mb and max_files when spec.logging is an
// object of those integers. Otherwise it is null. Other fields are not copied.
func projectLogging(spec map[string]any) any {
	if spec == nil {
		return nil
	}
	obj, ok := spec["logging"].(map[string]any)
	if !ok {
		return nil
	}
	size, sizeOK := integerField(obj["max_size_mb"])
	files, filesOK := integerField(obj["max_files"])
	if !sizeOK || !filesOK {
		return nil
	}
	return map[string]any{
		"max_size_mb": size,
		"max_files":   files,
	}
}

func integerField(v any) (json.Number, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return "", false
	}
	if _, err := n.Int64(); err != nil {
		return "", false
	}
	return n, true
}

// imageHasHealthcheck is true only when spec.healthcheck is an object.
// The argv and the numbers stay on the API.
func imageHasHealthcheck(spec map[string]any) bool {
	if spec == nil {
		return false
	}
	_, ok := spec["healthcheck"].(map[string]any)
	return ok
}

func projectPorts(v any) []any {
	arr, ok := v.([]any)
	if !ok {
		return []any{}
	}
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		port, ok := item.(map[string]any)
		if !ok {
			continue
		}
		kept := map[string]any{}
		if n, ok := port["host"].(json.Number); ok {
			kept["host"] = n
		}
		if n, ok := port["container"].(json.Number); ok {
			kept["container"] = n
		}
		if b, ok := port["public"].(bool); ok {
			kept["public"] = b
		}
		if len(kept) == 0 {
			continue
		}
		out = append(out, kept)
	}
	return out
}

// namesOf keeps one string field of each row (volume names, file names); targets
// and every other field are dropped.
func namesOf(v any, key string) []any {
	arr, ok := v.([]any)
	if !ok {
		return []any{}
	}
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, ok := row[key].(string)
		if !ok {
			continue
		}
		out = append(out, replaceControls(name))
	}
	return out
}

func envKeyNames(spec map[string]any) []any {
	if spec == nil {
		return []any{}
	}
	seen := map[string]struct{}{}
	add := func(v any) {
		keys, ok := v.(map[string]any)
		if !ok {
			return
		}
		for key := range keys {
			seen[replaceControls(key)] = struct{}{}
		}
	}
	add(spec["env"])
	add(spec["secret_env"])
	names := make([]string, 0, len(seen))
	for key := range seen {
		names = append(names, key)
	}
	sort.Strings(names)
	out := make([]any, len(names))
	for i, name := range names {
		out[i] = name
	}
	return out
}

func stringList(v any) []any {
	arr, ok := v.([]any)
	if !ok {
		return []any{}
	}
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			continue
		}
		out = append(out, replaceControls(s))
	}
	return out
}
