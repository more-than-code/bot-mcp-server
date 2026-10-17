package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// healthChecksPlan is the 202 sentence the fake API returns for a delegated PUT.
const healthChecksPlan = "will set 2 health checks on swag (pi3): tcp 443, http 8080 /health every 60 s"

const healthChecksKey = "hc-key.1"

func healthChecksConfirmPayload() map[string]any {
	return map[string]any{
		"checks": []any{
			map[string]any{
				"id": checkID, "service_id": serviceID,
				"protocol": "tcp", "port": 443, "path": nil,
				"interval_sec": 60, "enabled": true,
			},
			map[string]any{
				"id": "55555555-5555-4555-8555-555555555555", "service_id": serviceID,
				"protocol": "http", "port": 8080, "path": "/health?password=hunter2",
				"interval_sec": 60, "expect_status_min": 200, "expect_status_max": 399,
				"enabled": true, "note": "hunter2plain",
			},
		},
		"token": userToken,
	}
}

func TestSetHealthChecksRejectsInput(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	eleven := make([]any, 11)
	for i := range eleven {
		eleven[i] = map[string]any{"protocol": "tcp", "port": i + 1}
	}
	cases := []struct {
		name string
		args any
	}{
		{name: "bad uuid", args: map[string]any{"service_id": "not-a-uuid", "checks": []any{}, "idempotency_key": healthChecksKey}},
		{name: "checks omitted", args: map[string]any{"service_id": serviceID, "idempotency_key": healthChecksKey}},
		{name: "11 checks", args: map[string]any{"service_id": serviceID, "idempotency_key": healthChecksKey, "checks": eleven}},
		{name: "tcp with path", args: map[string]any{"service_id": serviceID, "idempotency_key": healthChecksKey, "checks": []any{
			map[string]any{"protocol": "tcp", "port": 22, "path": "/"},
		}}},
		{name: "http without path", args: map[string]any{"service_id": serviceID, "idempotency_key": healthChecksKey, "checks": []any{
			map[string]any{"protocol": "http", "port": 80},
		}}},
		{name: "interval 20", args: map[string]any{"service_id": serviceID, "idempotency_key": healthChecksKey, "checks": []any{
			map[string]any{"protocol": "http", "port": 80, "path": "/health", "interval_sec": 20},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := fake.count()
			res := mustCall(t, cs, "set_health_checks", tc.args)
			if !res.IsError || !strings.HasPrefix(textOf(t, res), "bad_request:") {
				t.Fatalf("result error=%v text %q", res.IsError, textOf(t, res))
			}
			if fake.count() != before {
				t.Fatal("api was called")
			}
		})
	}
}

func TestSetHealthChecksPutBody(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	args := map[string]any{
		"service_id":      serviceID,
		"idempotency_key": healthChecksKey,
		"checks": []any{
			map[string]any{"protocol": "tcp", "port": 443},
			map[string]any{
				"protocol": "http", "port": 8080, "path": "/health",
				"interval_sec": 60, "expect_status_min": 200, "expect_status_max": 399,
				"enabled": false,
			},
		},
	}
	body := `{"checks":[{"protocol":"tcp","port":443,"path":null},{"protocol":"http","port":8080,"path":"/health","interval_sec":60,"expect_status_min":200,"expect_status_max":399,"enabled":false}],"idempotency_key":"hc-key.1"}`
	res := mustCall(t, cs, "set_health_checks", args)
	if res.IsError {
		t.Fatalf("tool error %s", textOf(t, res))
	}
	assertLast(t, fake, http.MethodPut, "/v1/services/"+serviceID+"/health-checks", "", body)

	args["idempotency_key"] = "hc-key.2"
	res = mustCall(t, cs, "set_health_checks", args)
	if res.IsError {
		t.Fatalf("tool error %s", textOf(t, res))
	}
	assertLast(t, fake, http.MethodPut, "/v1/services/"+serviceID+"/health-checks", "", strings.ReplaceAll(body, "hc-key.1", "hc-key.2"))

	empty := mustCall(t, cs, "set_health_checks", map[string]any{
		"service_id": serviceID, "idempotency_key": "hc-empty", "checks": []any{},
	})
	if empty.IsError || !strings.Contains(textOf(t, empty), "nothing ran yet") {
		t.Fatalf("empty set error=%v text %q", empty.IsError, textOf(t, empty))
	}
	assertLast(t, fake, http.MethodPut, "/v1/services/"+serviceID+"/health-checks", "", `{"checks":[],"idempotency_key":"hc-empty"}`)
}

func TestSetHealthChecksPlan(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "set_health_checks", map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey,
		"checks": []any{map[string]any{"protocol": "tcp", "port": 443}},
	})
	if res.IsError {
		t.Fatalf("tool error %s", textOf(t, res))
	}
	want := "nothing ran yet. confirmation id " + confirmationID + ". Expires 2026-10-15T00:05:00Z.\n" + healthChecksPlan
	if textOf(t, res) != want {
		t.Fatalf("text %q", textOf(t, res))
	}
	got := structuredMap(t, res)
	if got["confirmation_id"] != confirmationID || got["plan"] != healthChecksPlan {
		t.Fatalf("payload %#v", got)
	}
	if _, ok := got["note"]; ok {
		t.Fatal("202 kept an unknown field")
	}
	if _, ok := got["checks"]; ok {
		t.Fatal("202 was summarized as health checks")
	}
	assertNoHealthSecrets(t, res)
	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestSetHealthChecksAPIError(t *testing.T) {
	fake := &fakeAPI{token: userToken, mode: "forbidden"}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "set_health_checks", map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey,
		"checks": []any{map[string]any{"protocol": "tcp", "port": 443}},
	})
	if !res.IsError || textOf(t, res) != "forbidden: not a member" {
		t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
	}
	call := fake.last()
	if call.Method != http.MethodPut || call.Path != "/v1/services/"+serviceID+"/health-checks" {
		t.Fatalf("api call %s %s", call.Method, call.Path)
	}
	assertDelegatedV1(t, call)

	fake.setMode("health-error")
	res = mustCall(t, cs, "set_health_checks", map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey,
		"checks": []any{map[string]any{"protocol": "http", "port": 80, "path": "/health"}},
	})
	if !res.IsError || textOf(t, res) != "bad_request: token=[REDACTED]" {
		t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
	}
	if strings.Contains(textOf(t, res), "s3cret") {
		t.Fatalf("secret in error %q", textOf(t, res))
	}
	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestSetHealthChecksRejectsHTTP200(t *testing.T) {
	fake := &fakeAPI{token: userToken, mode: "acted"}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "set_health_checks", map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey,
		"checks": []any{map[string]any{"protocol": "tcp", "port": 443}},
	})
	if !res.IsError || textOf(t, res) != "error: expected a confirmation" {
		t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
	}
	if strings.Contains(textOf(t, res), "health checks set") || strings.Contains(textOf(t, res), "nothing ran yet") {
		t.Fatalf("200 summarized %q", textOf(t, res))
	}
}

func TestConfirmHealthChecksSummary(t *testing.T) {
	fake := &fakeAPI{token: userToken, mode: "health-checks"}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "confirm-hc",
	})
	if res.IsError {
		t.Fatalf("tool error %s", textOf(t, res))
	}
	if textOf(t, res) != "2 health checks set" {
		t.Fatalf("text %q", textOf(t, res))
	}
	assertLast(t, fake, http.MethodPost, "/v1/confirmations/"+confirmationID, "", `{"idempotency_key":"confirm-hc"}`)
	checks, ok := structuredMap(t, res)["checks"].([]any)
	if !ok || len(checks) != 2 {
		t.Fatalf("checks %#v", structuredMap(t, res)["checks"])
	}
	httpCheck, ok := checks[1].(map[string]any)
	if !ok {
		t.Fatalf("check %#v", checks[1])
	}
	path, _ := httpCheck["path"].(string)
	if path != "/health?password=[REDACTED]" {
		t.Fatalf("path %q", path)
	}
	if _, exists := httpCheck["note"]; exists {
		t.Fatal("check kept note")
	}
	if _, exists := structuredMap(t, res)["token"]; exists {
		t.Fatal("result kept token")
	}
	tcpCheck, ok := checks[0].(map[string]any)
	if !ok || tcpCheck["protocol"] != "tcp" || tcpCheck["path"] != nil {
		t.Fatalf("tcp check %#v", checks[0])
	}
	assertNoHealthSecrets(t, res)
	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestSetHealthChecksRejectsKeyAndExpect(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	httpCheck := func(extra ...map[string]any) map[string]any {
		check := map[string]any{"protocol": "http", "port": 80, "path": "/health"}
		for _, more := range extra {
			for k, v := range more {
				check[k] = v
			}
		}
		return check
	}
	cases := []struct {
		name string
		args map[string]any
		text string
	}{
		{
			name: "missing key",
			args: map[string]any{"service_id": serviceID, "checks": []any{httpCheck()}},
			text: "bad_request: idempotency_key is required",
		},
		{
			name: "malformed key",
			args: map[string]any{
				"service_id": serviceID, "idempotency_key": "bad key",
				"checks": []any{httpCheck()},
			},
			text: "bad_request: idempotency_key must be 1 to 128 characters from [A-Za-z0-9._:-]",
		},
		{
			name: "expect 199",
			args: map[string]any{
				"service_id": serviceID, "idempotency_key": healthChecksKey,
				"checks": []any{httpCheck(map[string]any{"expect_status_min": 199})},
			},
			text: "bad_request: expect_status_min must be between 200 and 399",
		},
		{
			name: "expect 400",
			args: map[string]any{
				"service_id": serviceID, "idempotency_key": healthChecksKey,
				"checks": []any{httpCheck(map[string]any{"expect_status_max": 400})},
			},
			text: "bad_request: expect_status_max must be between 200 and 399",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := fake.count()
			res := mustCall(t, cs, "set_health_checks", tc.args)
			if !res.IsError || textOf(t, res) != tc.text {
				t.Fatalf("result error=%v text %q", res.IsError, textOf(t, res))
			}
			if fake.count() != before {
				t.Fatal("api was called")
			}
		})
	}
}

func TestSetHealthChecksAdvertisesKeyAndExpect(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var health, confirm *mcp.Tool
	for _, tool := range listed.Tools {
		switch tool.Name {
		case "set_health_checks":
			health = tool
		case "confirm":
			confirm = tool
		}
	}
	if health == nil || confirm == nil {
		t.Fatal("missing tool")
	}
	if !strings.Contains(health.Description, "same idempotency_key") {
		t.Fatalf("description %q", health.Description)
	}
	if !strings.Contains(confirm.Description, "set_health_checks") {
		t.Fatalf("confirm description %q", confirm.Description)
	}
	props, _ := health.InputSchema.(map[string]any)["properties"].(map[string]any)
	key, _ := props["idempotency_key"].(map[string]any)
	if key["pattern"] != "^[A-Za-z0-9._:-]{1,128}$" {
		t.Fatalf("idempotency schema %#v", key)
	}
	required, _ := health.InputSchema.(map[string]any)["required"].([]any)
	if !schemaLists(required, "idempotency_key") {
		t.Fatalf("required %#v", required)
	}
	items, _ := props["checks"].(map[string]any)["items"].(map[string]any)
	itemProps, _ := items["properties"].(map[string]any)
	for _, name := range []string{"expect_status_min", "expect_status_max"} {
		field, _ := itemProps[name].(map[string]any)
		if field["minimum"] != float64(200) || field["maximum"] != float64(399) {
			t.Fatalf("%s schema %#v", name, field)
		}
	}
}

func schemaLists(items []any, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func healthChecksServicePayload() map[string]any {
	return map[string]any{
		"id": serviceID, "host_id": hostID, "name": "swag",
		"source": "discovered", "kind": "process", "systemd_unit": "swag.service",
		"token": userToken, "ports": []any{80, 443},
		"health": map[string]any{
			"status": "up", "last_checked_at": "2026-10-15T00:00:00Z",
			"latency_ms": 12, "note": "hunter2plain",
		},
		"health_checks": []any{
			map[string]any{
				"id": checkID, "service_id": serviceID, "protocol": "tcp", "port": 443, "path": nil,
				"interval_sec": 60, "expect_status_min": 200, "expect_status_max": 399,
				"enabled": true, "note": "hunter2plain",
			},
			map[string]any{
				"id": "55555555-5555-4555-8555-555555555555", "protocol": "http", "port": 8080,
				"path": "/health?password=hunter2", "interval_sec": 60, "enabled": false,
				"token": delegatedToken,
			},
		},
	}
}

func TestSetHealthChecksPathRules(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	long := "/" + strings.Repeat("a", 512)
	cases := []struct {
		name string
		path string
	}{
		{name: "double slash", path: "//x"},
		{name: "513 bytes", path: long},
		{name: "question", path: "/a?b"},
		{name: "hash", path: "/a#b"},
		{name: "at", path: "/a@b"},
		{name: "backslash", path: "/a\\b"},
		{name: "space", path: "/a b"},
		{name: "control", path: "/a\x01b"},
		{name: "nbsp", path: "/a\u00a0b"},
		{name: "line separator", path: "/a\u2028b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := fake.count()
			res := mustCall(t, cs, "set_health_checks", map[string]any{
				"service_id": serviceID, "idempotency_key": healthChecksKey,
				"checks": []any{map[string]any{"protocol": "http", "port": 80, "path": tc.path}},
			})
			if !res.IsError || textOf(t, res) != "bad_request: path is required for http and must start with /" {
				t.Fatalf("result error=%v text %q", res.IsError, textOf(t, res))
			}
			if strings.Contains(textOf(t, res), tc.path) {
				t.Fatal("error echoed the path")
			}
			if fake.count() != before {
				t.Fatal("api was called")
			}
		})
	}
}

func TestSetHealthChecksBounds(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	before := fake.count()
	res := mustCall(t, cs, "set_health_checks", map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey,
		"checks": []any{map[string]any{
			"protocol": "http", "port": 80, "path": "/health",
			"expect_status_min": 300, "expect_status_max": 200,
		}},
	})
	if !res.IsError || textOf(t, res) != "bad_request: expect_status_min must not be greater than expect_status_max" {
		t.Fatalf("min>max error=%v text %q", res.IsError, textOf(t, res))
	}
	if fake.count() != before {
		t.Fatal("api was called")
	}

	before = fake.count()
	res = mustCall(t, cs, "set_health_checks", map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey,
		"checks": []any{map[string]any{
			"protocol": "http", "port": 80, "path": "/health", "interval_sec": 301,
		}},
	})
	if !res.IsError || textOf(t, res) != "bad_request: interval_sec must be between 30 and 300" {
		t.Fatalf("interval 301 error=%v text %q", res.IsError, textOf(t, res))
	}
	if fake.count() != before {
		t.Fatal("api was called")
	}

	res = mustCall(t, cs, "set_health_checks", map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey,
		"checks": []any{map[string]any{
			"protocol": "http", "port": 80, "path": "/health", "interval_sec": 300,
		}},
	})
	if res.IsError {
		t.Fatalf("interval 300 %s", textOf(t, res))
	}
	assertLast(t, fake, http.MethodPut, "/v1/services/"+serviceID+"/health-checks", "", `{"checks":[{"protocol":"http","port":80,"path":"/health","interval_sec":300}],"idempotency_key":"hc-key.1"}`)

	checks := make([]any, 10)
	for i := range checks {
		checks[i] = map[string]any{"protocol": "tcp", "port": i + 1}
	}
	res = mustCall(t, cs, "set_health_checks", map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey, "checks": checks,
	})
	if res.IsError {
		t.Fatalf("10 checks %s", textOf(t, res))
	}
	var body struct {
		Checks []struct {
			Protocol string  `json:"protocol"`
			Port     int     `json:"port"`
			Path     *string `json:"path"`
		} `json:"checks"`
		Key string `json:"idempotency_key"`
	}
	if err := json.Unmarshal([]byte(fake.last().Body), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Checks) != 10 || body.Key != healthChecksKey {
		t.Fatalf("10 checks body %s", fake.last().Body)
	}
	for i, check := range body.Checks {
		if check.Protocol != "tcp" || check.Port != i+1 || check.Path != nil {
			t.Fatalf("check %d %#v", i, check)
		}
	}
}

func TestSetHealthChecksSamePort(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "set_health_checks", map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey,
		"checks": []any{
			map[string]any{"protocol": "tcp", "port": 443},
			map[string]any{"protocol": "http", "port": 443, "path": "/health"},
		},
	})
	if res.IsError {
		t.Fatalf("tool error %s", textOf(t, res))
	}
	assertLast(t, fake, http.MethodPut, "/v1/services/"+serviceID+"/health-checks", "", `{"checks":[{"protocol":"tcp","port":443,"path":null},{"protocol":"http","port":443,"path":"/health"}],"idempotency_key":"hc-key.1"}`)
}

func TestSetHealthChecksPlanCap(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	args := map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey,
		"checks": []any{map[string]any{"protocol": "tcp", "port": 443}},
	}
	long := strings.Repeat("a", 5000)
	fake.setPlan(long)
	res := mustCall(t, cs, "set_health_checks", args)
	if res.IsError {
		t.Fatalf("5000 %s", textOf(t, res))
	}
	if structuredMap(t, res)["plan"] != long || !strings.Contains(textOf(t, res), long) {
		t.Fatalf("plan len %d", len([]rune(structuredMap(t, res)["plan"].(string))))
	}
	if strings.Contains(textOf(t, res), "…") {
		t.Fatal("5000 rune plan was truncated")
	}

	huge := strings.Repeat("b", len([]rune(worstHealthPlan()))+1)
	fake.setPlan(huge)
	res = mustCall(t, cs, "set_health_checks", args)
	if !res.IsError || textOf(t, res) != "error: plan too long to show; send fewer checks" {
		t.Fatalf("over cap error=%v text %q", res.IsError, textOf(t, res))
	}
	if strings.Contains(textOf(t, res), strings.Repeat("b", 20)) {
		t.Fatal("long plan was returned")
	}
}

func TestConfirmHealthChecksFields(t *testing.T) {
	fake := &fakeAPI{token: userToken, mode: "health-checks"}
	fake.setConfirmBody(map[string]any{
		"checks": []any{
			map[string]any{
				"id": checkID, "service_id": serviceID, "protocol": "tcp", "port": 443, "path": nil,
				"interval_sec": 60, "expect_status_min": 200, "expect_status_max": 399,
				"enabled": false, "note": "hunter2plain",
			},
			map[string]any{
				"id": "55555555-5555-4555-8555-555555555555", "service_id": serviceID,
				"protocol": "http", "port": 8080, "interval_sec": 30,
				"expect_status_min": 201, "expect_status_max": 398, "enabled": true,
			},
		},
		"token": userToken, "unit": "deploybot-lab.service", "active_state": "active",
		"note": "hunter2plain",
	})
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "confirm-hc",
	})
	if res.IsError || textOf(t, res) != "2 health checks set" {
		t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
	}
	got := structuredMap(t, res)
	for _, extra := range []string{"token", "unit", "active_state", "note"} {
		if _, ok := got[extra]; ok {
			t.Fatalf("kept %s", extra)
		}
	}
	checks, _ := got["checks"].([]any)
	if len(checks) != 2 {
		t.Fatalf("checks %#v", got["checks"])
	}
	tcp, _ := checks[0].(map[string]any)
	httpCheck, _ := checks[1].(map[string]any)
	assertHealthCheckFields(t, tcp, map[string]any{
		"id": checkID, "service_id": serviceID, "protocol": "tcp",
		"port": 443, "interval_sec": 60, "expect_status_min": 200, "expect_status_max": 399,
		"enabled": false,
	})
	if _, ok := tcp["path"]; !ok || tcp["path"] != nil {
		t.Fatalf("tcp path %#v present=%v", tcp["path"], ok)
	}
	assertHealthCheckFields(t, httpCheck, map[string]any{
		"id": "55555555-5555-4555-8555-555555555555", "service_id": serviceID, "protocol": "http",
		"port": 8080, "interval_sec": 30, "expect_status_min": 201, "expect_status_max": 398,
		"enabled": true,
	})
	if _, ok := httpCheck["path"]; ok {
		t.Fatalf("missing path was returned %#v", httpCheck["path"])
	}
	assertNoHealthSecrets(t, res)
}

func TestGetHealthChecks(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "get_health_checks", map[string]any{"service_id": serviceID})
	if res.IsError {
		t.Fatalf("tool error %s", textOf(t, res))
	}
	assertLast(t, fake, http.MethodGet, "/v1/services/"+serviceID, "", "")
	if textOf(t, res) != "2 health checks on swag" {
		t.Fatalf("text %q", textOf(t, res))
	}
	got := structuredMap(t, res)
	for key := range got {
		switch key {
		case "name", "ports", "health", "health_checks":
		default:
			t.Fatalf("extra field %s", key)
		}
	}
	if got["name"] != "swag" {
		t.Fatalf("name %#v", got["name"])
	}
	ports, _ := got["ports"].([]any)
	if len(ports) != 2 || !numberIs(ports[0], 80) || !numberIs(ports[1], 443) {
		t.Fatalf("ports %#v", ports)
	}
	health, _ := got["health"].(map[string]any)
	if health["status"] != "up" || health["last_checked_at"] != "2026-10-15T00:00:00Z" || !numberIs(health["latency_ms"], 12) {
		t.Fatalf("health %#v", health)
	}
	if _, ok := health["note"]; ok {
		t.Fatal("health kept note")
	}
	checks, _ := got["health_checks"].([]any)
	if len(checks) != 2 {
		t.Fatalf("checks %#v", got["health_checks"])
	}
	tcp, _ := checks[0].(map[string]any)
	if _, ok := tcp["service_id"]; ok || tcp["protocol"] != "tcp" || !numberIs(tcp["port"], 443) || tcp["enabled"] != true {
		t.Fatalf("tcp %#v", tcp)
	}
	if _, ok := tcp["path"]; !ok || tcp["path"] != nil {
		t.Fatalf("tcp path %#v", tcp["path"])
	}
	if _, ok := tcp["note"]; ok {
		t.Fatal("check kept note")
	}
	httpCheck, _ := checks[1].(map[string]any)
	if httpCheck["path"] != "/health?password=[REDACTED]" || httpCheck["enabled"] != false {
		t.Fatalf("http %#v", httpCheck)
	}
	if _, ok := httpCheck["token"]; ok {
		t.Fatal("check kept token")
	}
	assertNoHealthSecrets(t, res)
	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}

	before := fake.count()
	bad := mustCall(t, cs, "get_health_checks", map[string]any{"service_id": "not-a-uuid"})
	if !bad.IsError || !strings.HasPrefix(textOf(t, bad), "bad_request:") {
		t.Fatalf("bad uuid error=%v text %q", bad.IsError, textOf(t, bad))
	}
	if fake.count() != before {
		t.Fatal("api was called")
	}

	fake.setMode("missing-service")
	missing := mustCall(t, cs, "get_health_checks", map[string]any{"service_id": serviceID})
	if !missing.IsError || textOf(t, missing) != "not_found: no such service" {
		t.Fatalf("404 error=%v text %q", missing.IsError, textOf(t, missing))
	}
	assertLast(t, fake, http.MethodGet, "/v1/services/"+serviceID, "", "")
}

func TestSetHealthChecksRoundTrip(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	fake.setServiceBody(roundTripService())
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var health *mcp.Tool
	for _, tool := range listed.Tools {
		if tool.Name == "set_health_checks" {
			health = tool
		}
	}
	if health == nil {
		t.Fatal("missing tool")
	}
	props, _ := health.InputSchema.(map[string]any)["properties"].(map[string]any)
	items, _ := props["checks"].(map[string]any)["items"].(map[string]any)
	itemProps, _ := items["properties"].(map[string]any)
	for _, name := range []string{"id", "service_id"} {
		if _, ok := itemProps[name].(map[string]any); !ok {
			t.Fatalf("schema missing %s", name)
		}
	}
	itemRequired, _ := items["required"].([]any)
	if schemaLists(itemRequired, "id") || schemaLists(itemRequired, "service_id") {
		t.Fatalf("id is required %#v", itemRequired)
	}

	gotRes := mustCall(t, cs, "get_health_checks", map[string]any{"service_id": serviceID})
	if gotRes.IsError {
		t.Fatalf("get %s", textOf(t, gotRes))
	}
	checks, _ := structuredMap(t, gotRes)["health_checks"].([]any)
	if len(checks) != 2 {
		t.Fatalf("checks %#v", checks)
	}
	httpRow, _ := checks[1].(map[string]any)
	if httpRow["id"] != "55555555-5555-4555-8555-555555555555" || httpRow["protocol"] != "http" || httpRow["path"] != "/health" {
		t.Fatalf("get shape %#v", httpRow)
	}
	if _, ok := httpRow["service_id"]; ok {
		t.Fatal("get returned service_id")
	}
	httpRow["interval_sec"] = 120

	want := `{"checks":[{"protocol":"tcp","port":443,"path":null,"interval_sec":60,"expect_status_min":200,"expect_status_max":399,"enabled":true},{"protocol":"http","port":8080,"path":"/health","interval_sec":120,"expect_status_min":200,"expect_status_max":399,"enabled":false}],"idempotency_key":"hc-key.1"}`
	res := mustCall(t, cs, "set_health_checks", map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey, "checks": checks,
	})
	if res.IsError {
		t.Fatalf("round trip %s", textOf(t, res))
	}
	assertLast(t, fake, http.MethodPut, "/v1/services/"+serviceID+"/health-checks", "", want)

	for _, item := range checks {
		row, _ := item.(map[string]any)
		row["service_id"] = serviceID
	}
	res = mustCall(t, cs, "set_health_checks", map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey, "checks": checks,
	})
	if res.IsError {
		t.Fatalf("service_id %s", textOf(t, res))
	}
	assertLast(t, fake, http.MethodPut, "/v1/services/"+serviceID+"/health-checks", "", want)
}

func TestSetHealthChecksWorstPlan(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	args := map[string]any{
		"service_id": serviceID, "idempotency_key": healthChecksKey,
		"checks": []any{map[string]any{"protocol": "tcp", "port": 443}},
	}
	plan := worstHealthPlan()
	fake.setPlan(plan)
	res := mustCall(t, cs, "set_health_checks", args)
	if res.IsError {
		t.Fatalf("worst %s", textOf(t, res))
	}
	if structuredMap(t, res)["plan"] != plan || !strings.Contains(textOf(t, res), plan) {
		got, _ := structuredMap(t, res)["plan"].(string)
		t.Fatalf("plan len %d", len([]rune(got)))
	}
	if strings.Contains(textOf(t, res), "…") {
		t.Fatal("worst plan was truncated")
	}

	fake.setPlan(plan + "x")
	res = mustCall(t, cs, "set_health_checks", args)
	if !res.IsError || textOf(t, res) != "error: plan too long to show; send fewer checks" {
		t.Fatalf("over worst error=%v text %q", res.IsError, textOf(t, res))
	}
	if strings.Contains(textOf(t, res), "will set 10") {
		t.Fatal("long plan was returned")
	}
}

func roundTripService() map[string]any {
	return map[string]any{
		"name": "swag", "token": userToken, "ports": []any{443, 8080},
		"health": map[string]any{
			"status": "up", "last_checked_at": "2026-10-15T00:00:00Z", "latency_ms": 12,
		},
		"health_checks": []any{
			map[string]any{
				"id": checkID, "service_id": serviceID, "protocol": "tcp", "port": 443, "path": nil,
				"interval_sec": 60, "expect_status_min": 200, "expect_status_max": 399, "enabled": true,
				"note": "hunter2plain",
			},
			map[string]any{
				"id": "55555555-5555-4555-8555-555555555555", "service_id": serviceID,
				"protocol": "http", "port": 8080, "path": "/health",
				"interval_sec": 60, "expect_status_min": 200, "expect_status_max": 399, "enabled": false,
				"token": delegatedToken,
			},
		},
	}
}

// worstHealthPlan is the v1.10 sentence maxHealthPlanRunes is sized to.
func worstHealthPlan() string {
	label := strconv.Quote(strings.Repeat("\"", 64))
	host := strings.Repeat("h", 63)
	path := strconv.Quote(strings.Repeat("\"", 512))
	item := "http 65535 " + path + " every 300 s (disabled)"
	parts := make([]string, 10)
	for i := range parts {
		parts[i] = item
	}
	return "will set 10 health checks on " + label + " (" + host + "): " + strings.Join(parts, ", ") +
		" (tcp 65535 is redundant with http 65535)"
}

func assertHealthCheckFields(t *testing.T, got, want map[string]any) {
	t.Helper()
	for key, expect := range want {
		value, ok := got[key]
		if !ok {
			t.Fatalf("%s missing in %#v", key, got)
		}
		switch n := expect.(type) {
		case int:
			if !numberIs(value, float64(n)) {
				t.Fatalf("%s = %#v", key, value)
			}
		default:
			if value != expect {
				t.Fatalf("%s = %#v", key, value)
			}
		}
	}
}

func assertNoHealthSecrets(t *testing.T, res *mcp.CallToolResult) {
	t.Helper()
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	blob := string(raw)
	for _, secret := range []string{userToken, delegatedToken, clientSecret, "hunter2plain", "s3cret", "hunter2"} {
		if strings.Contains(blob, secret) {
			t.Fatalf("result contains %s", secret)
		}
	}
}
