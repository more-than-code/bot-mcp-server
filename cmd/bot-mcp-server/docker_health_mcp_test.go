package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDockerHealthCatalogAndLogs(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	spec := map[string]any{
		"kind":  "service",
		"image": "redis:7",
		"healthcheck": map[string]any{
			"test":             []any{"redis-cli", "ping"},
			"interval_sec":     30,
			"timeout_sec":      5,
			"retries":          3,
			"start_period_sec": 0,
		},
	}
	res := mustCall(t, cs, "create_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": spec, "idempotency_key": "hc-create",
	})
	assertCatalogCall(t, fake, "POST", "/v1/projects/"+projectID+"/catalog", map[string]any{
		"name": imageEntry, "kind": "image", "spec": spec, "idempotency_key": "hc-create",
	})
	if res.IsError {
		t.Fatalf("create %s", textOf(t, res))
	}

	bare := map[string]any{
		"kind":        "service",
		"image":       "redis:7",
		"healthcheck": map[string]any{"test": []any{"redis-cli", "ping"}},
	}
	res = mustCall(t, cs, "update_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": bare, "idempotency_key": "hc-update",
	})
	assertCatalogCall(t, fake, "PUT", "/v1/projects/"+projectID+"/catalog/"+imageEntry, map[string]any{
		"kind": "image", "spec": bare, "idempotency_key": "hc-update",
	})
	if res.IsError {
		t.Fatalf("update %s", textOf(t, res))
	}

	// The API owns the job rule and timeout_sec < interval_sec. Both are forwarded.
	job := map[string]any{
		"kind":  "job",
		"image": "nginx",
		"healthcheck": map[string]any{
			"test":         []any{"true"},
			"interval_sec": 5,
			"timeout_sec":  60,
		},
	}
	_ = mustCall(t, cs, "create_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": job, "idempotency_key": "hc-job",
	})
	assertCatalogCall(t, fake, "POST", "/v1/projects/"+projectID+"/catalog", map[string]any{
		"name": imageEntry, "kind": "image", "spec": job, "idempotency_key": "hc-job",
	})

	const sentinel = "argv-sentinel-do-not-echo"
	before := fake.v1Count()
	rejected := []struct {
		name string
		spec map[string]any
		want string
	}{
		{"missing test", map[string]any{"kind": "service", "image": "nginx", "healthcheck": map[string]any{}}, "bad_request: healthcheck.test is required"},
		{"empty item", map[string]any{"kind": "service", "image": "nginx", "healthcheck": map[string]any{"test": []any{sentinel, ""}}}, "bad_request: healthcheck.test items must be 1 to 1024 bytes"},
		{"interval", map[string]any{"kind": "service", "image": "nginx", "healthcheck": map[string]any{"test": []any{"ok"}, "interval_sec": 4}}, "bad_request: healthcheck.interval_sec must be an integer from 5 to 300"},
		{"unknown", map[string]any{"kind": "service", "image": "nginx", "healthcheck": map[string]any{"test": []any{"ok"}, "shell": sentinel}}, "bad_request: unknown healthcheck field shell"},
	}
	for _, tc := range rejected {
		res = mustCall(t, cs, "update_catalog_entry", map[string]any{
			"project_id": projectID, "name": imageEntry, "spec": tc.spec, "idempotency_key": "hc-bad",
		})
		if !res.IsError || textOf(t, res) != tc.want || strings.Contains(textOf(t, res), sentinel) {
			t.Fatalf("%s error=%v text %q", tc.name, res.IsError, textOf(t, res))
		}
	}
	if fake.v1Count() != before {
		t.Fatal("rejected healthcheck called the api")
	}

	plan := `will create image entry web in lab: nginx, service, healthcheck CMD "hello world" interval 30 timeout 5 retries 3 start 0`
	fake.setPlan(plan)
	res = mustCall(t, cs, "create_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": spec, "idempotency_key": "hc-plan",
	})
	assertPlan(t, res, plan)

	fake.setMode("catalog-healthcheck")
	res = mustCall(t, cs, "list_catalog", map[string]any{"project_id": projectID})
	if res.IsError || textOf(t, res) != "3 catalog entries: tra-backend (program), redis (image service), nginx (image service)" {
		t.Fatalf("catalog error=%v text %q", res.IsError, textOf(t, res))
	}
	blob := mustJSON(t, res.StructuredContent)
	if strings.Contains(blob, sentinel) || strings.Contains(blob, "redis-cli") || strings.Contains(textOf(t, res), sentinel) {
		t.Fatalf("argv leaked %s", blob)
	}
	items, _ := structuredMap(t, res)["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items %#v", items)
	}
	prog, _ := items[0].(map[string]any)
	if _, ok := prog["healthcheck"]; ok || len(prog) != 4 {
		t.Fatalf("program %#v", prog)
	}
	redis, _ := items[1].(map[string]any)
	nginx, _ := items[2].(map[string]any)
	if redis["healthcheck"] != true || nginx["healthcheck"] != false {
		t.Fatalf("flags redis %#v nginx %#v", redis["healthcheck"], nginx["healthcheck"])
	}

	fake.setMode("log-preview")
	res = mustCall(t, cs, "entry_logs", map[string]any{
		"project_id": projectID, "entry": imageEntry, "host_id": hostID,
	})
	if res.IsError {
		t.Fatalf("logs %s", textOf(t, res))
	}
	lines, _ := structuredMap(t, res)["lines"].([]any)
	if len(lines) != 4 || lines[0] != "abcd…" || lines[1] != "••••" || lines[2] != "ready abcd… ••••" || lines[3] != "password=[REDACTED]" {
		t.Fatalf("lines %#v", lines)
	}
}

func TestDockerHealthRows(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	fake.setMode("docker-list")
	res := mustCall(t, cs, "list_services", map[string]any{"host_id": hostID})
	if res.IsError || textOf(t, res) != "3 services\nredis down (Docker health)\napi up\nold unknown" {
		t.Fatalf("services error=%v text %q", res.IsError, textOf(t, res))
	}
	items, _ := structuredMap(t, res)["items"].([]any)
	redisHealth, _ := items[0].(map[string]any)["health"].(map[string]any)
	if redisHealth["source"] != "docker" || !nullField(redisHealth, "latency_ms") {
		t.Fatalf("redis health %#v", redisHealth)
	}
	apiHealth, _ := items[1].(map[string]any)["health"].(map[string]any)
	if apiHealth["source"] != "check" || !numberIs(apiHealth["latency_ms"], 4) {
		t.Fatalf("api health %#v", apiHealth)
	}
	oldHealth, _ := items[2].(map[string]any)["health"].(map[string]any)
	if !nullField(oldHealth, "source") || !nullField(oldHealth, "latency_ms") {
		t.Fatalf("old health %#v", oldHealth)
	}

	fake.setServiceBody(map[string]any{
		"id": serviceID, "name": "redis", "ports": []any{6379}, "token": userToken,
		"health": map[string]any{
			"status": "down", "last_checked_at": "2026-10-20T00:00:00Z",
			"source": "docker", "latency_ms": nil, "note": "do-not-keep",
		},
		"health_checks": []any{},
	})
	res = mustCall(t, cs, "get_health_checks", map[string]any{"service_id": serviceID})
	if res.IsError || textOf(t, res) != "0 health checks on redis (Docker health)" {
		t.Fatalf("get error=%v text %q", res.IsError, textOf(t, res))
	}
	got := structuredMap(t, res)
	health, _ := got["health"].(map[string]any)
	if health["source"] != "docker" || !nullField(health, "latency_ms") {
		t.Fatalf("health %#v", health)
	}
	if _, ok := health["note"]; ok || strings.Contains(mustJSON(t, got), "do-not-keep") || strings.Contains(mustJSON(t, got), userToken) {
		t.Fatalf("kept extra %s", mustJSON(t, got))
	}

	fake.setServiceBody(map[string]any{
		"id": serviceID, "name": "api", "ports": []any{80},
		"health": map[string]any{
			"status": "unknown", "source": nil, "latency_ms": nil,
		},
		"health_checks": []any{},
	})
	res = mustCall(t, cs, "get_health_checks", map[string]any{"service_id": serviceID})
	if res.IsError || textOf(t, res) != "0 health checks on api" {
		t.Fatalf("null source error=%v text %q", res.IsError, textOf(t, res))
	}
	health, _ = structuredMap(t, res)["health"].(map[string]any)
	if !nullField(health, "source") || !nullField(health, "latency_ms") {
		t.Fatalf("null health %#v", health)
	}

	fake.setHealthBody(map[string]any{
		"items": []any{
			map[string]any{
				"t": "2026-10-20T00:00:00Z", "check_id": nil, "protocol": "docker",
				"port": nil, "result": "unhealthy", "http_status": nil, "latency_ms": nil,
			},
			map[string]any{
				"t": "2026-10-20T00:01:00Z", "check_id": checkID, "protocol": "http",
				"port": 8080, "result": "success", "http_status": 200, "latency_ms": 4,
			},
		},
	})
	res = mustCall(t, cs, "service_health_history", map[string]any{"service_id": serviceID})
	if res.IsError || textOf(t, res) != "2 health results, 1 from Docker health" {
		t.Fatalf("history error=%v text %q", res.IsError, textOf(t, res))
	}
	rows, _ := structuredMap(t, res)["items"].([]any)
	row, _ := rows[0].(map[string]any)
	if row["protocol"] != "docker" || row["result"] != "unhealthy" || !nullField(row, "check_id") || !nullField(row, "port") || !nullField(row, "http_status") || !nullField(row, "latency_ms") {
		t.Fatalf("docker row %#v", row)
	}
}

func TestDockerHealthDescriptions(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		byName[tool.Name] = tool
	}
	if !strings.Contains(byName["update_catalog_entry"].Description, "owner or admin") {
		t.Fatalf("update description %q", byName["update_catalog_entry"].Description)
	}
	if strings.Contains(byName["create_catalog_entry"].Description, "owner or admin") {
		t.Fatalf("create description %q", byName["create_catalog_entry"].Description)
	}
	for _, name := range []string{"list_services", "get_health_checks", "service_health_history"} {
		if !strings.Contains(byName[name].Description, "Docker health") {
			t.Fatalf("%s description %q", name, byName[name].Description)
		}
	}
	if !strings.Contains(byName["list_catalog"].Description, "healthcheck true or false") {
		t.Fatalf("list_catalog description %q", byName["list_catalog"].Description)
	}
	for _, name := range []string{"create_catalog_entry", "update_catalog_entry"} {
		props, _ := byName[name].InputSchema.(map[string]any)["properties"].(map[string]any)
		spec, _ := props["spec"].(map[string]any)["properties"].(map[string]any)
		hc, _ := spec["healthcheck"].(map[string]any)
		required, _ := hc["required"].([]any)
		if !schemaLists(required, "test") {
			t.Fatalf("%s healthcheck %#v", name, hc)
		}
		fields, _ := hc["properties"].(map[string]any)
		test, _ := fields["test"].(map[string]any)
		if test["minItems"] != float64(1) || test["maxItems"] != float64(32) {
			t.Fatalf("test schema %#v", test)
		}
		item, _ := test["items"].(map[string]any)
		if item["minLength"] != float64(1) || item["maxLength"] != float64(1024) {
			t.Fatalf("item schema %#v", item)
		}
		bounds := map[string][2]float64{
			"interval_sec":     {5, 300},
			"timeout_sec":      {1, 60},
			"retries":          {1, 10},
			"start_period_sec": {0, 600},
		}
		for key, span := range bounds {
			field, _ := fields[key].(map[string]any)
			if field["minimum"] != span[0] || field["maximum"] != span[1] {
				t.Fatalf("%s %#v", key, field)
			}
		}
	}
}

func nullField(row map[string]any, key string) bool {
	v, ok := row[key]
	return ok && v == nil
}
