package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const loggingSentence = "spec.logging is optional; Deploybot defaults to 10 MB × 3 files, and an override sets both max_size_mb (1–100) and max_files (1–10)."

func TestLoggingCatalogWrite(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	const sentinel = "log-sentinel-do-not-echo"
	before := fake.v1Count()
	rejected := []struct {
		name string
		spec map[string]any
		want string
	}{
		{"missing files", map[string]any{"kind": "service", "image": "nginx", "logging": map[string]any{"max_size_mb": 10}}, "bad_request: logging.max_files is required"},
		{"size low", map[string]any{"kind": "service", "image": "nginx", "logging": map[string]any{"max_size_mb": 0, "max_files": 3}}, "bad_request: logging.max_size_mb must be an integer from 1 to 100"},
		{"size high", map[string]any{"kind": "service", "image": "nginx", "logging": map[string]any{"max_size_mb": 101, "max_files": 3}}, "bad_request: logging.max_size_mb must be an integer from 1 to 100"},
		{"files high", map[string]any{"kind": "service", "image": "nginx", "logging": map[string]any{"max_size_mb": 10, "max_files": 11}}, "bad_request: logging.max_files must be an integer from 1 to 10"},
		{"unknown", map[string]any{"kind": "service", "image": "nginx", "logging": map[string]any{"max_size_mb": 10, "max_files": 3, "driver": sentinel}}, "bad_request: unknown logging field driver"},
		{"not object", map[string]any{"kind": "service", "image": "nginx", "logging": sentinel}, "bad_request: logging must be an object"},
	}
	for _, tc := range rejected {
		res := mustCall(t, cs, "update_catalog_entry", map[string]any{
			"project_id": projectID, "name": imageEntry, "spec": tc.spec, "idempotency_key": "log-bad",
		})
		if !res.IsError || textOf(t, res) != tc.want || strings.Contains(textOf(t, res), sentinel) {
			t.Fatalf("%s error=%v text %q", tc.name, res.IsError, textOf(t, res))
		}
	}
	if fake.v1Count() != before {
		t.Fatal("rejected logging called the api")
	}

	spec := map[string]any{
		"kind":  "service",
		"image": "nginx",
		"logging": map[string]any{
			"max_size_mb": 5,
			"max_files":   2,
		},
	}
	res := mustCall(t, cs, "create_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": spec, "idempotency_key": "log-create",
	})
	assertCatalogCall(t, fake, "POST", "/v1/projects/"+projectID+"/catalog", map[string]any{
		"name": imageEntry, "kind": "image", "spec": spec, "idempotency_key": "log-create",
	})
	if res.IsError {
		t.Fatalf("create %s", textOf(t, res))
	}

	cleared := map[string]any{
		"kind":    "service",
		"image":   "nginx",
		"logging": nil,
	}
	res = mustCall(t, cs, "update_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": cleared, "idempotency_key": "log-null",
	})
	assertCatalogCall(t, fake, "PUT", "/v1/projects/"+projectID+"/catalog/"+imageEntry, map[string]any{
		"kind": "image", "spec": cleared, "idempotency_key": "log-null",
	})
	if res.IsError {
		t.Fatalf("null %s", textOf(t, res))
	}

	job := map[string]any{
		"kind":  "job",
		"image": "nginx",
		"logging": map[string]any{
			"max_size_mb": 100,
			"max_files":   1,
		},
	}
	res = mustCall(t, cs, "create_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": job, "idempotency_key": "log-job",
	})
	assertCatalogCall(t, fake, "POST", "/v1/projects/"+projectID+"/catalog", map[string]any{
		"name": imageEntry, "kind": "image", "spec": job, "idempotency_key": "log-job",
	})
	if res.IsError {
		t.Fatalf("job %s", textOf(t, res))
	}
}

func TestLoggingCatalogList(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	fake.setMode("catalog-logging")
	res := mustCall(t, cs, "list_catalog", map[string]any{"project_id": projectID})
	if res.IsError || textOf(t, res) != "3 catalog entries: tra-backend (program), redis (image service), nginx (image service)" {
		t.Fatalf("catalog error=%v text %q", res.IsError, textOf(t, res))
	}
	blob := mustJSON(t, res.StructuredContent)
	if strings.Contains(blob, "program-log-do-not-keep") || strings.Contains(blob, "image-log-extra-do-not-keep") {
		t.Fatalf("logging leaked %s", blob)
	}
	items, _ := structuredMap(t, res)["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items %#v", items)
	}
	prog, _ := items[0].(map[string]any)
	if _, ok := prog["logging"]; ok || len(prog) != 5 || prog["kind"] != "program" {
		t.Fatalf("program %#v", prog)
	}
	redis, _ := items[1].(map[string]any)
	log, _ := redis["logging"].(map[string]any)
	if len(log) != 2 || !numberIs(log["max_size_mb"], 5) || !numberIs(log["max_files"], 2) {
		t.Fatalf("redis logging %#v", redis["logging"])
	}
	nginx, _ := items[2].(map[string]any)
	if !nullField(nginx, "logging") || nginx["healthcheck"] != false {
		t.Fatalf("nginx %#v", nginx)
	}
}

func TestLoggingToolSchema(t *testing.T) {
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
	for _, name := range []string{"create_catalog_entry", "update_catalog_entry"} {
		if !strings.Contains(byName[name].Description, loggingSentence) {
			t.Fatalf("%s description %q", name, byName[name].Description)
		}
		props, _ := byName[name].InputSchema.(map[string]any)["properties"].(map[string]any)
		spec, _ := props["spec"].(map[string]any)["properties"].(map[string]any)
		logging, _ := spec["logging"].(map[string]any)
		if logging["additionalProperties"] != false {
			t.Fatalf("%s additionalProperties %#v", name, logging["additionalProperties"])
		}
		types, _ := logging["type"].([]any)
		if len(types) != 2 || types[0] != "object" || types[1] != "null" {
			t.Fatalf("%s type %#v", name, logging["type"])
		}
		required, _ := logging["required"].([]any)
		if !schemaLists(required, "max_size_mb") || !schemaLists(required, "max_files") || len(required) != 2 {
			t.Fatalf("%s required %#v", name, required)
		}
		fields, _ := logging["properties"].(map[string]any)
		size, _ := fields["max_size_mb"].(map[string]any)
		files, _ := fields["max_files"].(map[string]any)
		if size["type"] != "integer" || size["minimum"] != float64(1) || size["maximum"] != float64(100) {
			t.Fatalf("%s max_size_mb %#v", name, size)
		}
		if files["type"] != "integer" || files["minimum"] != float64(1) || files["maximum"] != float64(10) {
			t.Fatalf("%s max_files %#v", name, files)
		}
	}
	if !strings.Contains(byName["list_catalog"].Description, "logging as max_size_mb and max_files or null") {
		t.Fatalf("list_catalog description %q", byName["list_catalog"].Description)
	}
}
