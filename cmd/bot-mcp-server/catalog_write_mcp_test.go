package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const fileContentLeak = "img-file-content-do-not-leak"

func catalogPlanOr(action, override string) string {
	if override != "" {
		return override
	}
	return catalogWritePlan(action)
}

func catalogWritePlan(action string) string {
	switch action {
	case "update":
		return "will replace image entry webadmin: ports"
	case "delete":
		return "will delete image entry webadmin. 0 host approvals. running containers are not stopped"
	default:
		return "will create image entry webadmin in lab: docker.io/library/nginx:latest, service"
	}
}

func catalogWriteEntry() map[string]any {
	return map[string]any{
		"id":              "99999999-9999-4999-8999-999999999999",
		"project_id":      projectID,
		"name":            imageEntry,
		"kind":            "image",
		"spec_hash":       "abc123",
		"command_display": "docker.io/library/nginx:latest",
		"spec": map[string]any{
			"kind":       "service",
			"image":      "docker.io/library/nginx:latest",
			"command":    []any{cmdLeak},
			"entrypoint": []any{entryLeak},
			"user":       userLeak,
			"ports":      []any{map[string]any{"host": 8080, "container": 80, "public": false}},
			"env":        map[string]any{"NODE_ENV": envLeak},
			"secret_env": map[string]any{"DB_URL": secretLeak},
			"env_sets":   []any{"base"},
			"volumes":    []any{map[string]any{"name": "webdata", "target": targetLeak}},
			"files": []any{map[string]any{
				"file": "nginx.conf", "target": fileTargetSentinel, "content": fileContentLeak,
			}},
			"restart":   "unless-stopped",
			"memory_mb": 64,
		},
		"my_actions": []any{"deploy", "logs", "status"},
	}
}

const fileTargetSentinel = "img-file-target-do-not-leak"

func catalogSpec() map[string]any {
	return map[string]any{
		"image": "docker.io/library/nginx:latest",
		"kind":  "service",
		"ports": []any{map[string]any{"container": 80, "host": 8080, "public": false}},
	}
}

func TestCatalogWriteTools(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	spec := catalogSpec()

	before := fake.v1Count()
	missing := mustCall(t, cs, "create_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": spec,
	})
	if !missing.IsError || textOf(t, missing) != "bad_request: idempotency_key is required" {
		t.Fatalf("missing key error=%v text %q", missing.IsError, textOf(t, missing))
	}
	unknown := mustCall(t, cs, "create_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "idempotency_key": "create-1",
		"spec": map[string]any{"image": "docker.io/library/nginx:latest", "kind": "service", "nope": true},
	})
	if !unknown.IsError || !strings.Contains(textOf(t, unknown), "unknown spec field nope") {
		t.Fatalf("unknown spec error=%v text %q", unknown.IsError, textOf(t, unknown))
	}
	badKind := mustCall(t, cs, "update_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "idempotency_key": "update-1",
		"spec": map[string]any{"image": "docker.io/library/nginx:latest", "kind": "container"},
	})
	if !badKind.IsError || textOf(t, badKind) != "bad_request: spec.kind must be service or job" {
		t.Fatalf("kind error=%v text %q", badKind.IsError, textOf(t, badKind))
	}
	if fake.v1Count() != before {
		t.Fatal("rejected input called the api")
	}

	res := mustCall(t, cs, "create_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": spec, "idempotency_key": "create-1",
	})
	assertCatalogCall(t, fake, http.MethodPost, "/v1/projects/"+projectID+"/catalog", map[string]any{
		"name": imageEntry, "kind": "image", "spec": spec, "idempotency_key": "create-1",
	})
	assertPlan(t, res, catalogWritePlan("create"))

	res = mustCall(t, cs, "update_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": spec, "idempotency_key": "update-1",
	})
	assertCatalogCall(t, fake, http.MethodPut, "/v1/projects/"+projectID+"/catalog/"+imageEntry, map[string]any{
		"kind": "image", "spec": spec, "idempotency_key": "update-1",
	})
	assertPlan(t, res, catalogWritePlan("update"))

	res = mustCall(t, cs, "delete_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "idempotency_key": "delete-1",
	})
	assertCatalogCall(t, fake, http.MethodDelete, "/v1/projects/"+projectID+"/catalog/"+imageEntry, map[string]any{
		"idempotency_key": "delete-1",
	})
	assertPlan(t, res, catalogWritePlan("delete"))

	fake.setMode("acted")
	acted := mustCall(t, cs, "create_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": spec, "idempotency_key": "create-2",
	})
	if !acted.IsError || textOf(t, acted) != "error: expected a confirmation" {
		t.Fatalf("acted error=%v text %q", acted.IsError, textOf(t, acted))
	}
	assertNoCatalogLeaks(t, acted)

	fake.setMode("catalog-created")
	created := mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "create-1",
	})
	assertCatalogConfirm(t, created, "created catalog entry "+imageEntry+" (docker.io/library/nginx:latest).")

	fake.setMode("catalog-updated")
	updated := mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "update-1",
	})
	assertCatalogConfirm(t, updated, "updated catalog entry "+imageEntry+" (docker.io/library/nginx:latest).")

	fake.setMode("catalog-deleted")
	deleted := mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "delete-1",
	})
	if deleted.IsError || textOf(t, deleted) != "deleted the catalog entry." {
		t.Fatalf("delete confirm error=%v text %q", deleted.IsError, textOf(t, deleted))
	}
	if structuredMap(t, deleted)["deleted"] != true {
		t.Fatalf("deleted structured %#v", structuredMap(t, deleted))
	}
	assertNoCatalogLeaks(t, deleted)

	fake.setMode("catalog-bad")
	bad := mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "create-1",
	})
	if !bad.IsError || textOf(t, bad) != "error: catalog entry response was not usable" {
		t.Fatalf("bad confirm error=%v text %q", bad.IsError, textOf(t, bad))
	}
	assertNoCatalogLeaks(t, bad)
}

func TestCatalogWritePlanKept(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	plan := "will update image entry web: secret_env: OTHER←svc.OTHER, TOKEN←svc.TOKEN → none; env MODE=plain-mode-value; " + strings.Repeat("p", 1100)
	fake.setPlan(plan)
	res := mustCall(t, cs, "create_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": catalogSpec(), "idempotency_key": "plan-1",
	})
	assertPlan(t, res, plan)
	if strings.Contains(textOf(t, res), "secret_env=[REDACTED]") || strings.Contains(textOf(t, res), "…") {
		t.Fatalf("plan was rewritten %q", textOf(t, res))
	}
}

func assertPlan(t *testing.T, res *mcp.CallToolResult, plan string) {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool error %s", textOf(t, res))
	}
	want := "nothing ran yet. confirmation id " + confirmationID + ". Expires 2026-10-15T00:05:00Z.\n" + plan
	if textOf(t, res) != want {
		t.Fatalf("plan text %q", textOf(t, res))
	}
	got := structuredMap(t, res)
	if got["confirmation_id"] != confirmationID || got["plan"] != plan {
		t.Fatalf("plan structured %#v", got)
	}
	assertNoCatalogLeaks(t, res)
}

func assertCatalogCall(t *testing.T, fake *fakeAPI, method, path string, want map[string]any) {
	t.Helper()
	call := fake.last()
	if call.Method != method || call.Path != path || call.RawQuery != "" {
		t.Fatalf("api call method=%s path=%s query=%s", call.Method, call.Path, call.RawQuery)
	}
	assertDelegatedV1(t, call)
	if call.tokenInOther {
		t.Fatal("token appeared outside Authorization")
	}
	if len(call.Unexpected) != 0 {
		t.Fatalf("unexpected headers %v", call.Unexpected)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(call.Body), &got); err != nil {
		t.Fatalf("body %s: %v", call.Body, err)
	}
	gotRaw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantRaw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotRaw) != string(wantRaw) {
		t.Fatalf("body %s want %s", gotRaw, wantRaw)
	}
}

func assertCatalogConfirm(t *testing.T, res *mcp.CallToolResult, text string) {
	t.Helper()
	if res.IsError || textOf(t, res) != text {
		t.Fatalf("confirm error=%v text %q", res.IsError, textOf(t, res))
	}
	assertNoCatalogLeaks(t, res)
	got := structuredMap(t, res)
	if got["name"] != imageEntry || got["kind"] != "image" || got["image"] != "docker.io/library/nginx:latest" {
		t.Fatalf("entry %#v", got)
	}
	if _, ok := got["spec"]; ok {
		t.Fatal("spec was returned")
	}
	keys, _ := got["env_keys"].([]any)
	if len(keys) != 2 || keys[0] != "DB_URL" || keys[1] != "NODE_ENV" {
		t.Fatalf("env_keys %#v", got["env_keys"])
	}
	files, _ := got["files"].([]any)
	if len(files) != 1 || files[0] != "nginx.conf" {
		t.Fatalf("files %#v", got["files"])
	}
	ports, _ := got["ports"].([]any)
	if len(ports) != 1 {
		t.Fatalf("ports %#v", got["ports"])
	}
}

func assertNoCatalogLeaks(t *testing.T, res *mcp.CallToolResult) {
	t.Helper()
	blob := textOf(t, res)
	if res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		blob += string(raw)
	}
	for _, leak := range []string{envLeak, cmdLeak, entryLeak, targetLeak, secretLeak, userLeak, fileContentLeak, fileTargetSentinel, "unless-stopped"} {
		if strings.Contains(blob, leak) {
			t.Fatalf("kept %s in %s", leak, blob)
		}
	}
}
