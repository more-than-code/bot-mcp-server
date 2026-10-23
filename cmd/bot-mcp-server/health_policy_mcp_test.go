package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const healthPolicySentence = "spec.health_policy is optional and for services only: on_deploy_failure rollback (default), stop or keep; on_unhealthy none (default), restart or rollback, which needs spec.healthcheck and cannot be combined with on_deploy_failure keep; unhealthy_after_sec (15–600, default 45) and max_actions_per_hour (1–10, default 3)."

func TestHealthPolicyCatalogWrite(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	const sentinel = "policy-sentinel-do-not-echo"
	hc := map[string]any{"test": []any{"true"}}
	rejected := []struct {
		name string
		spec map[string]any
		want string
	}{
		{"job", map[string]any{"kind": "job", "image": "nginx", "health_policy": map[string]any{}}, "bad_request: health_policy applies to services"},
		{"no healthcheck", map[string]any{"kind": "service", "image": "nginx", "health_policy": map[string]any{"on_unhealthy": "restart"}}, "bad_request: health_policy.on_unhealthy needs a healthcheck"},
		{"keep", map[string]any{"kind": "service", "image": "nginx", "healthcheck": hc, "health_policy": map[string]any{"on_deploy_failure": "keep", "on_unhealthy": "rollback"}}, "bad_request: health_policy.on_unhealthy cannot be combined with keep"},
		{"enum", map[string]any{"kind": "service", "image": "nginx", "health_policy": map[string]any{"on_deploy_failure": sentinel}}, "bad_request: health_policy.on_deploy_failure must be one of rollback, stop, keep"},
		{"after low", map[string]any{"kind": "service", "image": "nginx", "health_policy": map[string]any{"unhealthy_after_sec": 14}}, "bad_request: health_policy.unhealthy_after_sec must be an integer from 15 to 600"},
		{"actions high", map[string]any{"kind": "service", "image": "nginx", "health_policy": map[string]any{"max_actions_per_hour": 11}}, "bad_request: health_policy.max_actions_per_hour must be an integer from 1 to 10"},
		{"unknown key", map[string]any{"kind": "service", "image": "nginx", "health_policy": map[string]any{"mode": 1}}, "bad_request: unknown field health_policy.mode"},
		{"odd key", map[string]any{"kind": "service", "image": "nginx", "health_policy": map[string]any{sentinel: 1}}, "bad_request: invalid health_policy"},
		{"not object", map[string]any{"kind": "service", "image": "nginx", "health_policy": sentinel}, "bad_request: health_policy must be an object"},
	}
	before := fake.v1Count()
	for _, tool := range []string{"create_catalog_entry", "update_catalog_entry"} {
		for _, tc := range rejected {
			res := mustCall(t, cs, tool, map[string]any{
				"project_id": projectID, "name": imageEntry, "spec": tc.spec, "idempotency_key": "hp-bad",
			})
			if !res.IsError || textOf(t, res) != tc.want || strings.Contains(textOf(t, res), sentinel) {
				t.Fatalf("%s %s error=%v text %q", tool, tc.name, res.IsError, textOf(t, res))
			}
		}
	}
	if fake.v1Count() != before {
		t.Fatal("rejected health_policy called the api")
	}

	spec := map[string]any{
		"kind":        "service",
		"image":       "nginx",
		"healthcheck": hc,
		"health_policy": map[string]any{
			"on_deploy_failure":    "stop",
			"on_unhealthy":         "restart",
			"unhealthy_after_sec":  30,
			"max_actions_per_hour": 4,
		},
	}
	policyJSON, _ := json.Marshal(spec["health_policy"])
	res := mustCall(t, cs, "create_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": spec, "idempotency_key": "hp-create",
	})
	assertCatalogCall(t, fake, "POST", "/v1/projects/"+projectID+"/catalog", map[string]any{
		"name": imageEntry, "kind": "image", "spec": spec, "idempotency_key": "hp-create",
	})
	if res.IsError || !strings.Contains(fake.last().Body, `"health_policy":`+string(policyJSON)) {
		t.Fatalf("create error=%v body %s", res.IsError, fake.last().Body)
	}

	cleared := map[string]any{"kind": "service", "image": "nginx", "health_policy": nil}
	res = mustCall(t, cs, "update_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": cleared, "idempotency_key": "hp-null",
	})
	assertCatalogCall(t, fake, "PUT", "/v1/projects/"+projectID+"/catalog/"+imageEntry, map[string]any{
		"kind": "image", "spec": cleared, "idempotency_key": "hp-null",
	})
	if res.IsError || !strings.Contains(fake.last().Body, `"health_policy":null`) {
		t.Fatalf("null error=%v body %s", res.IsError, fake.last().Body)
	}

	res = mustCall(t, cs, "update_catalog_entry", map[string]any{
		"project_id": projectID, "name": imageEntry, "spec": spec, "idempotency_key": "hp-update",
	})
	assertCatalogCall(t, fake, "PUT", "/v1/projects/"+projectID+"/catalog/"+imageEntry, map[string]any{
		"kind": "image", "spec": spec, "idempotency_key": "hp-update",
	})
	if res.IsError || !strings.Contains(fake.last().Body, `"health_policy":`+string(policyJSON)) {
		t.Fatalf("update error=%v body %s", res.IsError, fake.last().Body)
	}
}

func TestHealthPolicyCatalogList(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	fake.setMode("catalog-health-policy")
	res := mustCall(t, cs, "list_catalog", map[string]any{"project_id": projectID})
	if res.IsError {
		t.Fatalf("catalog %s", textOf(t, res))
	}
	if strings.Contains(mustJSON(t, res.StructuredContent), "policy-extra-do-not-keep") {
		t.Fatal("health_policy extra key leaked")
	}
	items, _ := structuredMap(t, res)["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items %#v", items)
	}
	want := []string{
		`{"max_actions_per_hour":3,"on_deploy_failure":"rollback","on_unhealthy":"none","unhealthy_after_sec":45}`,
		`{"max_actions_per_hour":5,"on_deploy_failure":"rollback","on_unhealthy":"restart","unhealthy_after_sec":45}`,
		"",
	}
	for i, raw := range items {
		item, _ := raw.(map[string]any)
		if want[i] == "" {
			if !nullField(item, "health_policy") {
				t.Fatalf("job %#v", item)
			}
			continue
		}
		got, _ := json.Marshal(item["health_policy"])
		if string(got) != want[i] {
			t.Fatalf("item %d: %s want %s", i, got, want[i])
		}
	}
}

func TestHealthPolicyToolDescriptions(t *testing.T) {
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
		if !strings.Contains(byName[name].Description, healthPolicySentence) {
			t.Fatalf("%s description %q", name, byName[name].Description)
		}
		props, _ := byName[name].InputSchema.(map[string]any)["properties"].(map[string]any)
		spec, _ := props["spec"].(map[string]any)["properties"].(map[string]any)
		policy, _ := spec["health_policy"].(map[string]any)
		if policy["additionalProperties"] != false {
			t.Fatalf("%s additionalProperties %#v", name, policy["additionalProperties"])
		}
	}
}
