package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

// errCatalogWriteShape is fixed. It must not include the entry body.
var errCatalogWriteShape = errors.New("catalog entry response was not usable")

// imageSpecKeys is the ImageSpec object from api-v1. Anything else is rejected
// before the API is called. The values are forwarded unchanged.
var imageSpecKeys = map[string]struct{}{
	"kind": {}, "image": {}, "command": {}, "entrypoint": {}, "ports": {},
	"env": {}, "env_sets": {}, "secret_env": {}, "volumes": {}, "files": {},
	"restart": {}, "user": {}, "healthy_after_sec": {}, "memory_mb": {}, "timeout_sec": {},
}

const catalogWriteSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["project_id", "name", "spec", "idempotency_key"],
  "properties": {
    "project_id": {"type": "string", "format": "uuid"},
    "name": {"type": "string", "pattern": "^[a-z0-9][a-z0-9-]{0,47}$"},
    "idempotency_key": {"type": "string", "pattern": "^[A-Za-z0-9._:-]{1,128}$"},
    "spec": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "kind": {"type": "string", "enum": ["service", "job"]},
        "image": {"type": "string"},
        "command": {"type": ["array", "null"], "items": {"type": "string"}},
        "entrypoint": {"type": ["array", "null"], "items": {"type": "string"}},
        "ports": {"type": "array"},
        "env": {"type": "object"},
        "env_sets": {"type": "array", "items": {"type": "string"}},
        "secret_env": {"type": "object"},
        "volumes": {"type": "array"},
        "files": {"type": "array"},
        "restart": {"type": "string"},
        "user": {"type": "string"},
        "healthy_after_sec": {"type": "integer"},
        "memory_mb": {"type": ["integer", "null"]},
        "timeout_sec": {"type": "integer"}
      }
    }
  }
}`

const catalogDeleteSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["project_id", "name", "idempotency_key"],
  "properties": {
    "project_id": {"type": "string", "format": "uuid"},
    "name": {"type": "string", "pattern": "^[a-z0-9][a-z0-9-]{0,47}$"},
    "idempotency_key": {"type": "string", "pattern": "^[A-Za-z0-9._:-]{1,128}$"}
  }
}`

type catalogWriteArgs struct {
	ProjectID      string          `json:"project_id"`
	Name           string          `json:"name"`
	Spec           json.RawMessage `json:"spec"`
	IdempotencyKey string          `json:"idempotency_key"`
}

type catalogDeleteArgs struct {
	ProjectID      string `json:"project_id"`
	Name           string `json:"name"`
	IdempotencyKey string `json:"idempotency_key"`
}

type catalogCreateBody struct {
	Name           string          `json:"name"`
	Kind           string          `json:"kind"`
	Spec           json.RawMessage `json:"spec"`
	IdempotencyKey string          `json:"idempotency_key"`
}

type catalogReplaceBody struct {
	Kind           string          `json:"kind"`
	Spec           json.RawMessage `json:"spec"`
	IdempotencyKey string          `json:"idempotency_key"`
}

type catalogDeleteBody struct {
	IdempotencyKey string `json:"idempotency_key"`
}

func catalogWriteTool(name, description, schema string) *mcp.Tool {
	return &mcp.Tool{
		Name:        name,
		Description: description,
		Annotations: mutatingAnnotations(false),
		InputSchema: json.RawMessage(schema),
	}
}

func validateCatalogWrite(projectID, name, key string, spec json.RawMessage) error {
	if err := requireUUID("project_id", projectID); err != nil {
		return err
	}
	if err := requireCatalogName(name); err != nil {
		return err
	}
	if err := requireIdempotencyKey(key); err != nil {
		return err
	}
	return validateImageSpec(spec)
}

func validateCatalogDelete(projectID, name, key string) error {
	if err := requireUUID("project_id", projectID); err != nil {
		return err
	}
	if err := requireCatalogName(name); err != nil {
		return err
	}
	return requireIdempotencyKey(key)
}

func requireCatalogName(value string) error {
	if value == "" {
		return errors.New("name is required")
	}
	if !entryNamePattern.MatchString(value) {
		return errors.New("name must be a lowercase catalog name of at most 48 characters")
	}
	return nil
}

// validateImageSpec accepts an ImageSpec object and rejects unknown keys.
// It does not apply image rules 1–13; the API does, and names the field.
func validateImageSpec(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("spec is required")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil {
		return errors.New("spec must be an object")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("spec must be an object")
	}
	for key := range fields {
		if _, ok := imageSpecKeys[key]; !ok {
			return fmt.Errorf("unknown spec field %s", key)
		}
	}
	if kind, ok := fields["kind"]; ok {
		var value string
		if err := json.Unmarshal(kind, &value); err != nil || (value != "service" && value != "job") {
			return errors.New("spec.kind must be service or job")
		}
	}
	return nil
}

func deleteStatus(ctx context.Context, api *apiclient.Client, path string, body any) (int, json.RawMessage, *mcp.CallToolResult) {
	return callStatus(ctx, api, http.MethodDelete, path, nil, body)
}

// catalogWriteResult presents one catalog entry from a confirm, or reports
// a delete. Env values, file contents, and targets are not copied.
func catalogWriteResult(payload json.RawMessage, verb string) *mcp.CallToolResult {
	if isNullJSON(payload) {
		return present(json.RawMessage(`{"deleted":true}`), "deleted the catalog entry.")
	}
	projected, err := projectOneCatalogEntry(payload)
	if err != nil {
		return toolError("error", errCatalogWriteShape.Error())
	}
	var row struct {
		Name  string `json:"name"`
		Image string `json:"image"`
		Kind  string `json:"kind"`
	}
	_ = json.Unmarshal(projected, &row)
	text := verb + " catalog entry " + summaryField(row.Name)
	if row.Image != "" {
		text += " (" + summaryField(row.Image) + ")"
	}
	text += "."
	return present(projected, text)
}

func isNullJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func catalogEntryPayload(raw json.RawMessage) bool {
	if isNullJSON(raw) {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return false
	}
	kind := stringField(body, "kind")
	if kind != "image" && kind != "program" {
		return false
	}
	return stringField(body, "name") != ""
}

func projectOneCatalogEntry(raw json.RawMessage) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	dec.UseNumber()
	var row map[string]any
	if err := dec.Decode(&row); err != nil {
		return nil, errCatalogWriteShape
	}
	var projected map[string]any
	if stringField(row, "kind") == "image" {
		projected = projectImageEntry(row)
	} else if stringField(row, "kind") == "program" {
		projected = map[string]any{
			"name":            replaceControls(stringField(row, "name")),
			"kind":            replaceControls(stringField(row, "kind")),
			"command_display": publicString(stringField(row, "command_display")),
			"my_actions":      stringList(row["my_actions"]),
		}
	} else {
		return nil, errCatalogWriteShape
	}
	out, err := json.Marshal(projected)
	if err != nil {
		return nil, errCatalogWriteShape
	}
	return out, nil
}
