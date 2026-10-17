package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const (
	maxHealthChecks    = 10
	minHealthInterval  = 30
	maxHealthInterval  = 300
	minExpectStatus    = 200
	maxExpectStatus    = 399
	maxHealthPathBytes = 512
)

const setHealthChecksSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["service_id", "checks", "idempotency_key"],
  "properties": {
    "service_id": {"type": "string", "format": "uuid"},
    "idempotency_key": {"type": "string", "pattern": "^[A-Za-z0-9._:-]{1,128}$"},
    "checks": {
      "type": "array",
      "maxItems": 10,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["protocol", "port"],
        "properties": {
          "protocol": {"type": "string", "enum": ["tcp", "http"]},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "path": {"type": "string", "maxLength": 512},
          "interval_sec": {"type": "integer", "minimum": 30, "maximum": 300},
          "expect_status_min": {"type": "integer", "minimum": 200, "maximum": 399},
          "expect_status_max": {"type": "integer", "minimum": 200, "maximum": 399},
          "enabled": {"type": "boolean"},
          "id": {"type": "string"},
          "service_id": {"type": "string"}
        }
      }
    }
  }
}`

// healthCheckIn is one check from the tool arguments.
// ID and ServiceID are accepted so a get_health_checks row can be sent back.
// They are not copied onto the PUT body.
// Path is always sent: a string for http, null for tcp, because api-v1 stores null.
// Omitted optional fields stay omitted so the API applies its defaults.
type healthCheckIn struct {
	ID              string  `json:"id,omitempty"`
	ServiceID       string  `json:"service_id,omitempty"`
	Protocol        string  `json:"protocol"`
	Port            int     `json:"port"`
	Path            *string `json:"path"`
	IntervalSec     *int    `json:"interval_sec,omitempty"`
	ExpectStatusMin *int    `json:"expect_status_min,omitempty"`
	ExpectStatusMax *int    `json:"expect_status_max,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
}

// healthCheckPut is one check in the PUT body. id and service_id are absent.
type healthCheckPut struct {
	Protocol        string  `json:"protocol"`
	Port            int     `json:"port"`
	Path            *string `json:"path"`
	IntervalSec     *int    `json:"interval_sec,omitempty"`
	ExpectStatusMin *int    `json:"expect_status_min,omitempty"`
	ExpectStatusMax *int    `json:"expect_status_max,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
}

type setHealthChecksArgs struct {
	ServiceID      string          `json:"service_id"`
	Checks         []healthCheckIn `json:"checks"`
	IdempotencyKey string          `json:"idempotency_key"`
}

type healthChecksPut struct {
	Checks         []healthCheckPut `json:"checks"`
	IdempotencyKey string           `json:"idempotency_key"`
}

// errHealthChecksShape is fixed. It must not include the confirm body.
var errHealthChecksShape = errors.New("health check response was not usable")

func registerSetHealthChecks(server *mcp.Server, api *apiclient.Client) {
	addTool(server, &mcp.Tool{
		Name:        "set_health_checks",
		Description: "Replace all health checks on a service. Nothing changes until confirm after the user agrees. Pass the same idempotency_key to confirm. Checks connect only to 127.0.0.1 on the host. Read the current checks first with get_health_checks. The set replaces all checks.",
		Annotations: mutatingAnnotations(false),
		InputSchema: json.RawMessage(setHealthChecksSchema),
	}, func(ctx context.Context, raw json.RawMessage) *mcp.CallToolResult {
		var in setHealthChecksArgs
		if err := decodeArgs(raw, &in); err != nil {
			return badRequest(err)
		}
		if err := validateHealthChecks(in.ServiceID, in.IdempotencyKey, in.Checks); err != nil {
			return badRequest(err)
		}
		path := idPath("v1", "services", in.ServiceID, "health-checks")
		body := healthChecksPut{Checks: healthCheckPuts(in.Checks), IdempotencyKey: in.IdempotencyKey}
		status, payload, fail := putStatus(ctx, api, path, body)
		if fail != nil {
			return fail
		}
		return confirmationHealth(status, payload)
	})
}

func validateHealthChecks(serviceID, key string, checks []healthCheckIn) error {
	if err := requireUUID("service_id", serviceID); err != nil {
		return err
	}
	if err := requireIdempotencyKey(key); err != nil {
		return err
	}
	if checks == nil {
		return errors.New("checks is required")
	}
	if len(checks) > maxHealthChecks {
		return fmt.Errorf("at most %d health checks", maxHealthChecks)
	}
	for _, check := range checks {
		if err := validateHealthCheck(check); err != nil {
			return err
		}
	}
	return nil
}

func validateHealthCheck(in healthCheckIn) error {
	switch in.Protocol {
	case "tcp", "http":
	default:
		return errors.New("protocol must be tcp or http")
	}
	if in.Port < 1 || in.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if err := validateHealthPath(in.Protocol, in.Path); err != nil {
		return err
	}
	if err := optionalInterval(in.IntervalSec); err != nil {
		return err
	}
	if err := optionalExpect("expect_status_min", in.ExpectStatusMin); err != nil {
		return err
	}
	if err := optionalExpect("expect_status_max", in.ExpectStatusMax); err != nil {
		return err
	}
	if in.ExpectStatusMin != nil && in.ExpectStatusMax != nil && *in.ExpectStatusMin > *in.ExpectStatusMax {
		return errors.New("expect_status_min must not be greater than expect_status_max")
	}
	return nil
}

func validateHealthPath(protocol string, path *string) error {
	switch protocol {
	case "tcp":
		if path != nil {
			return errors.New("path is not valid for tcp")
		}
		return nil
	case "http":
		if path == nil || !validHealthPath(*path) {
			return errors.New("path is required for http and must start with /")
		}
		return nil
	default:
		return errors.New("protocol must be tcp or http")
	}
}

// validHealthPath is the v1.0.1 health-check path rule: starts with /, not //,
// at most 512 bytes, and no @ \ ? #, spaces, or control characters.
// unicode.IsSpace matches the API's validHTTPPath, including U+00A0 and U+2028.
func validHealthPath(path string) bool {
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return false
	}
	if len(path) > maxHealthPathBytes {
		return false
	}
	if strings.ContainsAny(path, "@\\?# ") {
		return false
	}
	for _, r := range path {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func healthCheckPuts(checks []healthCheckIn) []healthCheckPut {
	out := make([]healthCheckPut, len(checks))
	for i, check := range checks {
		out[i] = healthCheckPut{
			Protocol:        check.Protocol,
			Port:            check.Port,
			Path:            check.Path,
			IntervalSec:     check.IntervalSec,
			ExpectStatusMin: check.ExpectStatusMin,
			ExpectStatusMax: check.ExpectStatusMax,
			Enabled:         check.Enabled,
		}
	}
	return out
}

func optionalInterval(value *int) error {
	if value == nil {
		return nil
	}
	if *value < minHealthInterval || *value > maxHealthInterval {
		return fmt.Errorf("interval_sec must be between %d and %d", minHealthInterval, maxHealthInterval)
	}
	return nil
}

func optionalExpect(name string, value *int) error {
	if value == nil {
		return nil
	}
	if *value < minExpectStatus || *value > maxExpectStatus {
		return fmt.Errorf("%s must be between %d and %d", name, minExpectStatus, maxExpectStatus)
	}
	return nil
}

// healthCheckItems reports a body whose checks field is an array.
// Other keys do not matter: the caller projects the allowlist and drops the rest.
func healthCheckItems(raw json.RawMessage) ([]any, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return nil, false
	}
	items, ok := body["checks"].([]any)
	if !ok {
		return nil, false
	}
	return items, true
}

func summarizeHealthChecks(payload json.RawMessage) (string, bool) {
	items, ok := healthCheckItems(payload)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d health checks set", len(items)), true
}

// projectHealthChecks keeps HealthCheck fields and drops everything else.
// Strings are redacted. The bool is false when raw is not a checks result.
func projectHealthChecks(raw json.RawMessage) (json.RawMessage, bool, error) {
	items, ok := healthCheckItems(raw)
	if !ok {
		return nil, false, nil
	}
	outItems := make([]any, len(items))
	for i, item := range items {
		outItems[i] = projectHealthCheck(item)
	}
	encoded, err := json.Marshal(map[string]any{"checks": outItems})
	if err != nil {
		return nil, true, errHealthChecksShape
	}
	return encoded, true, nil
}

func projectHealthCheck(v any) map[string]any {
	return projectHealthCheckRow(v, []string{"id", "service_id", "protocol", "path"})
}

func projectGetHealthCheck(v any) map[string]any {
	return projectHealthCheckRow(v, []string{"id", "protocol", "path"})
}

func projectHealthCheckRow(v any, stringKeys []string) map[string]any {
	row, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := map[string]any{}
	for _, key := range stringKeys {
		switch s := row[key].(type) {
		case string:
			out[key] = publicString(s)
		case nil:
			if _, exists := row[key]; exists {
				out[key] = nil
			}
		}
	}
	for _, key := range []string{"port", "interval_sec", "expect_status_min", "expect_status_max"} {
		if n, ok := row[key].(json.Number); ok {
			out[key] = n
		}
	}
	if enabled, ok := row["enabled"].(bool); ok {
		out["enabled"] = enabled
	}
	return out
}
