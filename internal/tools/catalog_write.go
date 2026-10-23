package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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
	"healthcheck": {}, "logging": {}, "health_policy": {}, "restart": {}, "user": {}, "healthy_after_sec": {},
	"memory_mb": {}, "timeout_sec": {},
}

// Healthcheck bounds match api-v1 v1.11 item 7. Defaults, timeout_sec <
// interval_sec, the job rule, and NUL checks stay with the API.
const (
	healthTestMin  = 1
	healthTestMax  = 32
	healthArgMin   = 1
	healthArgMax   = 1024
	healthEveryMin = 5
	healthEveryMax = 300
	healthWaitMin  = 1
	healthWaitMax  = 60
	healthTriesMin = 1
	healthTriesMax = 10
	healthStartMin = 0
	healthStartMax = 600

	// Log bounds match api-v1 v1.14. A null or missing logging field means
	// Deploybot's default of 10 MB × 3 files.
	logSizeMin  = 1
	logSizeMax  = 100
	logFilesMin = 1
	logFilesMax = 10

	// Health policy bounds match api-v1 v1.15 item 1.
	policyAfterMin   = 15
	policyAfterMax   = 600
	policyActionsMin = 1
	policyActionsMax = 10
)

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
        "healthcheck": {
          "type": ["object", "null"],
          "additionalProperties": false,
          "required": ["test"],
          "properties": {
            "test": {
              "type": "array",
              "minItems": 1,
              "maxItems": 32,
              "items": {"type": "string", "minLength": 1, "maxLength": 1024}
            },
            "interval_sec": {"type": "integer", "minimum": 5, "maximum": 300},
            "timeout_sec": {"type": "integer", "minimum": 1, "maximum": 60},
            "retries": {"type": "integer", "minimum": 1, "maximum": 10},
            "start_period_sec": {"type": "integer", "minimum": 0, "maximum": 600}
          }
        },
        "logging": {
          "type": ["object", "null"],
          "additionalProperties": false,
          "required": ["max_size_mb", "max_files"],
          "properties": {
            "max_size_mb": {"type": "integer", "minimum": 1, "maximum": 100},
            "max_files": {"type": "integer", "minimum": 1, "maximum": 10}
          }
        },
        "health_policy": {
          "type": ["object", "null"],
          "additionalProperties": false,
          "properties": {
            "on_deploy_failure": {"type": "string", "enum": ["rollback", "stop", "keep"]},
            "on_unhealthy": {"type": "string", "enum": ["none", "restart", "rollback"]},
            "unhealthy_after_sec": {"type": "integer", "minimum": 15, "maximum": 600},
            "max_actions_per_hour": {"type": "integer", "minimum": 1, "maximum": 10}
          }
        },
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
// It checks the healthcheck and logging shapes. It does not apply image rules 1–13, fill
// healthcheck defaults, or compare timeout_sec with interval_sec. The API does,
// and names the field. A rejected healthcheck does not include the argv.
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
	if hc, ok := fields["healthcheck"]; ok {
		if err := validateHealthcheck(hc); err != nil {
			return err
		}
	}
	if logging, ok := fields["logging"]; ok {
		if err := validateLogging(logging); err != nil {
			return err
		}
	}
	if policy, ok := fields["health_policy"]; ok {
		if err := validateHealthPolicy(policy, fields); err != nil {
			return err
		}
	}
	return nil
}

// validateHealthPolicy checks the local shape of spec.health_policy (api-v1
// v1.15 item 1). Null means all defaults. All four keys are optional. A job
// is refused, and on_unhealthy other than none needs a healthcheck object.
// The spec bytes are still forwarded unchanged when this returns nil.
func validateHealthPolicy(raw json.RawMessage, spec map[string]json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if kind, ok := spec["kind"]; ok {
		var value string
		if json.Unmarshal(kind, &value) == nil && value == "job" {
			return errors.New("health_policy applies to services")
		}
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return errors.New("health_policy must be an object")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("health_policy must be an object")
	}
	allowed := map[string]struct{}{
		"on_deploy_failure": {}, "on_unhealthy": {}, "unhealthy_after_sec": {}, "max_actions_per_hour": {},
	}
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			if safePolicyKey(key) {
				return fmt.Errorf("unknown field health_policy.%s", key)
			}
			return errors.New("invalid health_policy")
		}
	}
	keep := false
	if value, ok := fields["on_deploy_failure"]; ok {
		if err := validateEnum(value, "health_policy.on_deploy_failure", "rollback", "stop", "keep"); err != nil {
			return err
		}
		var chosen string
		_ = json.Unmarshal(value, &chosen)
		keep = chosen == "keep"
	}
	unhealthy := "none"
	if value, ok := fields["on_unhealthy"]; ok {
		if err := validateEnum(value, "health_policy.on_unhealthy", "none", "restart", "rollback"); err != nil {
			return err
		}
		_ = json.Unmarshal(value, &unhealthy)
	}
	if value, ok := fields["unhealthy_after_sec"]; ok {
		if err := validateBoundedInt(value, "health_policy.unhealthy_after_sec", policyAfterMin, policyAfterMax); err != nil {
			return err
		}
	}
	if value, ok := fields["max_actions_per_hour"]; ok {
		if err := validateBoundedInt(value, "health_policy.max_actions_per_hour", policyActionsMin, policyActionsMax); err != nil {
			return err
		}
	}
	if unhealthy != "none" && !specHasHealthcheck(spec) {
		return errors.New("health_policy.on_unhealthy needs a healthcheck")
	}
	if keep && unhealthy != "none" {
		return errors.New("health_policy.on_unhealthy cannot be combined with keep")
	}
	return nil
}

// safePolicyKey is true for a key of at most 64 bytes of [a-z_], the only
// kind of key that may be named in an error.
func safePolicyKey(key string) bool {
	if len(key) == 0 || len(key) > 64 {
		return false
	}
	for i := 0; i < len(key); i++ {
		if c := key[i]; c != '_' && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

func specHasHealthcheck(spec map[string]json.RawMessage) bool {
	hc, ok := spec["healthcheck"]
	if !ok {
		return false
	}
	trimmed := bytes.TrimSpace(hc)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// validateEnum accepts a JSON string equal to one of the allowed values. The
// error lists the allowed values and does not echo the input.
func validateEnum(raw json.RawMessage, name string, allowed ...string) error {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		for _, a := range allowed {
			if value == a {
				return nil
			}
		}
	}
	return fmt.Errorf("%s must be one of %s", name, strings.Join(allowed, ", "))
}

// validateHealthcheck checks the local shape. Null means the field is omitted.
// The spec bytes are still forwarded unchanged when this returns nil.
func validateHealthcheck(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return errors.New("healthcheck must be an object")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("healthcheck must be an object")
	}
	allowed := map[string]struct{}{
		"test": {}, "interval_sec": {}, "timeout_sec": {}, "retries": {}, "start_period_sec": {},
	}
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unknown healthcheck field %s", key)
		}
	}
	test, ok := fields["test"]
	if !ok || bytes.Equal(bytes.TrimSpace(test), []byte("null")) {
		return errors.New("healthcheck.test is required")
	}
	if err := validateHealthTest(test); err != nil {
		return err
	}
	bounds := []struct {
		key      string
		min, max int
	}{
		{"interval_sec", healthEveryMin, healthEveryMax},
		{"timeout_sec", healthWaitMin, healthWaitMax},
		{"retries", healthTriesMin, healthTriesMax},
		{"start_period_sec", healthStartMin, healthStartMax},
	}
	for _, bound := range bounds {
		value, ok := fields[bound.key]
		if !ok {
			continue
		}
		if err := validateBoundedInt(value, "healthcheck."+bound.key, bound.min, bound.max); err != nil {
			return err
		}
	}
	return nil
}

// validateLogging checks the local shape. Null means the field is omitted
// and Deploybot keeps the default of 10 MB × 3 files. Both keys are required
// when the object is present. The spec bytes are still forwarded unchanged
// when this returns nil.
func validateLogging(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return errors.New("logging must be an object")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("logging must be an object")
	}
	allowed := map[string]struct{}{
		"max_size_mb": {}, "max_files": {},
	}
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unknown logging field %s", key)
		}
	}
	size, ok := fields["max_size_mb"]
	if !ok {
		return errors.New("logging.max_size_mb is required")
	}
	if err := validateBoundedInt(size, "logging.max_size_mb", logSizeMin, logSizeMax); err != nil {
		return err
	}
	files, ok := fields["max_files"]
	if !ok {
		return errors.New("logging.max_files is required")
	}
	return validateBoundedInt(files, "logging.max_files", logFilesMin, logFilesMax)
}

func validateHealthTest(raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var items []json.RawMessage
	if err := dec.Decode(&items); err != nil {
		return errors.New("healthcheck.test must be 1 to 32 strings")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("healthcheck.test must be 1 to 32 strings")
	}
	if len(items) < healthTestMin || len(items) > healthTestMax {
		return errors.New("healthcheck.test must be 1 to 32 strings")
	}
	for _, item := range items {
		var text string
		if err := json.Unmarshal(item, &text); err != nil {
			return errors.New("healthcheck.test must be 1 to 32 strings")
		}
		if len(text) < healthArgMin || len(text) > healthArgMax {
			return errors.New("healthcheck.test items must be 1 to 1024 bytes")
		}
	}
	return nil
}

func validateBoundedInt(raw json.RawMessage, name string, min, max int) error {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	dec.UseNumber()
	var n json.Number
	if err := dec.Decode(&n); err != nil {
		return fmt.Errorf("%s must be an integer from %d to %d", name, min, max)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s must be an integer from %d to %d", name, min, max)
	}
	v, err := n.Int64()
	if err != nil || v < int64(min) || v > int64(max) {
		return fmt.Errorf("%s must be an integer from %d to %d", name, min, max)
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
