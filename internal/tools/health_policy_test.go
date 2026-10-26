package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHealthPolicySchema(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(catalogWriteSchema), &schema); err != nil {
		t.Fatal(err)
	}
	props, _ := schema["properties"].(map[string]any)
	spec, _ := props["spec"].(map[string]any)
	specProps, _ := spec["properties"].(map[string]any)
	policy, ok := specProps["health_policy"].(map[string]any)
	if !ok {
		t.Fatal("health_policy missing from spec schema")
	}
	if policy["additionalProperties"] != false {
		t.Fatalf("additionalProperties %#v", policy["additionalProperties"])
	}
	fields, _ := policy["properties"].(map[string]any)
	if len(fields) != 4 {
		t.Fatalf("properties %#v", fields)
	}
	after, _ := fields["unhealthy_after_sec"].(map[string]any)
	most, _ := fields["max_actions_per_hour"].(map[string]any)
	if after["minimum"] != float64(15) || after["maximum"] != float64(600) {
		t.Fatalf("unhealthy_after_sec %#v", after)
	}
	if most["minimum"] != float64(1) || most["maximum"] != float64(10) {
		t.Fatalf("max_actions_per_hour %#v", most)
	}
	onUnhealthy, _ := fields["on_unhealthy"].(map[string]any)
	enum, _ := onUnhealthy["enum"].([]any)
	if len(enum) != 3 {
		t.Fatalf("on_unhealthy enum %#v", enum)
	}
}

func TestHealthPolicyShape(t *testing.T) {
	const sentinel = "policy-secret-do-not-echo"
	hc := `,"healthcheck":{"test":["true"]}`
	cases := []struct{ spec, want string }{
		{`{"kind":"service","image":"nginx"}`, ""},
		{`{"kind":"service","image":"nginx","health_policy":null}`, ""},
		{`{"kind":"service","image":"nginx","health_policy":{}}`, ""},
		{`{"kind":"service","image":"nginx","health_policy":{"on_deploy_failure":"stop","on_unhealthy":"none"}}`, ""},
		{`{"image":"nginx","health_policy":{"on_unhealthy":"restart","unhealthy_after_sec":15,"max_actions_per_hour":10}` + hc + `}`, ""},
		{`{"kind":"service","image":"nginx","health_policy":{"on_unhealthy":"rollback","unhealthy_after_sec":600,"max_actions_per_hour":1}` + hc + `}`, ""},
		{`{"kind":"service","image":"nginx","health_policy":{"on_unhealthy":"restart"}}`, "health_policy.on_unhealthy needs a healthcheck"},
		{`{"kind":"service","image":"nginx","health_policy":{"on_unhealthy":"restart"},"healthcheck":null}`, "health_policy.on_unhealthy needs a healthcheck"},
		{`{"kind":"job","image":"nginx","health_policy":{"on_deploy_failure":"keep"}}`, "health_policy applies to services"},
		// The job rule comes before every other check, as in the API's
		// decodeHealthPolicy (internal/catalog/health_policy.go:90-92).
		{`{"kind":"job","image":"nginx","health_policy":{"mode":1}}`, "health_policy applies to services"},
		{`{"kind":"job","image":"nginx","health_policy":"` + sentinel + `"}`, "health_policy applies to services"},
		{`{"kind":"job","image":"nginx","health_policy":{"` + sentinel + `":1}}`, "health_policy applies to services"},
		// keep + on_unhealthy without a healthcheck: the healthcheck rule wins, as in the API (lines 130-135).
		{`{"kind":"service","image":"nginx","health_policy":{"on_deploy_failure":"keep","on_unhealthy":"restart"}}`, "health_policy.on_unhealthy needs a healthcheck"},
		{`{"kind":"service","image":"nginx","health_policy":{"on_deploy_failure":"` + sentinel + `"}}`, "health_policy.on_deploy_failure must be one of rollback, stop, keep"},
		{`{"kind":"service","image":"nginx","health_policy":{"on_unhealthy":"reboot"}}`, "health_policy.on_unhealthy must be one of none, restart, rollback"},
		{`{"kind":"service","image":"nginx","health_policy":{"on_unhealthy":null}}`, "health_policy.on_unhealthy must be one of none, restart, rollback"},
		{`{"kind":"service","image":"nginx","health_policy":{"unhealthy_after_sec":14}}`, "health_policy.unhealthy_after_sec must be an integer from 15 to 600"},
		{`{"kind":"service","image":"nginx","health_policy":{"unhealthy_after_sec":601}}`, "health_policy.unhealthy_after_sec must be an integer from 15 to 600"},
		{`{"kind":"service","image":"nginx","health_policy":{"unhealthy_after_sec":45.0}}`, "health_policy.unhealthy_after_sec must be an integer from 15 to 600"},
		{`{"kind":"service","image":"nginx","health_policy":{"max_actions_per_hour":0}}`, "health_policy.max_actions_per_hour must be an integer from 1 to 10"},
		{`{"kind":"service","image":"nginx","health_policy":{"max_actions_per_hour":11}}`, "health_policy.max_actions_per_hour must be an integer from 1 to 10"},
		{`{"kind":"service","image":"nginx","health_policy":{"max_actions_per_hour":"` + sentinel + `"}}`, "health_policy.max_actions_per_hour must be an integer from 1 to 10"},
		{`{"kind":"service","image":"nginx","health_policy":{"mode":"` + sentinel + `"}}`, "unknown field health_policy.mode"},
		{`{"kind":"service","image":"nginx","health_policy":{"` + sentinel + `":1}}`, "invalid health_policy"},
		{`{"kind":"service","image":"nginx","health_policy":{"` + strings.Repeat("a", 65) + `":1}}`, "invalid health_policy"},
		{`{"kind":"service","image":"nginx","health_policy":{"` + strings.Repeat("a_", 32) + `":1}}`, "unknown field health_policy." + strings.Repeat("a_", 32)},
		{`{"kind":"service","image":"nginx","health_policy":{"x":1}}`, "unknown field health_policy.x"},
		{`{"kind":"service","image":"nginx","health_policy":{"":1}}`, "invalid health_policy"},
		{`{"kind":"service","image":"nginx","health_policy":{"Mode":1}}`, "invalid health_policy"},
		{`{"kind":"service","image":"nginx","health_policy":{"mode1":1}}`, "invalid health_policy"},
		{`{"kind":"service","image":"nginx","health_policy":{"mo-de":1}}`, "invalid health_policy"},
		{`{"kind":"service","image":"nginx","health_policy":{"on_deploy_failure":"keep","on_unhealthy":"restart"}` + hc + `}`, "health_policy.on_unhealthy cannot be combined with keep"},
		{`{"kind":"service","image":"nginx","health_policy":{"on_deploy_failure":"keep","on_unhealthy":"none"}}`, ""},
		{`{"kind":"service","image":"nginx","health_policy":"` + sentinel + `"}`, "health_policy must be an object"},
		{`{"kind":"service","image":"nginx","health_policy":[1]}`, "health_policy must be an object"},
	}
	for _, tc := range cases {
		err := validateImageSpec(json.RawMessage(tc.spec))
		if tc.want == "" {
			if err != nil {
				t.Fatalf("%s: %v", tc.spec, err)
			}
			continue
		}
		if err == nil || err.Error() != tc.want || strings.Contains(err.Error(), sentinel) {
			t.Fatalf("spec %s err %v want %s", tc.spec, err, tc.want)
		}
	}
}

func TestProjectCatalogHealthPolicy(t *testing.T) {
	const leak = "policy-extra-do-not-keep"
	raw := []byte(`{"items":[
		{"name":"plain","kind":"image","command_display":"a:1","spec":{"kind":"service","image":"a:1"},"my_actions":["deploy"]},
		{"name":"set","kind":"image","command_display":"b:1","spec":{"kind":"service","image":"b:1",
		 "health_policy":{"on_deploy_failure":"stop","on_unhealthy":"restart","unhealthy_after_sec":30,"max_actions_per_hour":5,"note":"` + leak + `"}},"my_actions":["deploy"]},
		{"name":"partial","kind":"image","command_display":"c:1","spec":{"kind":"service","image":"c:1",
		 "health_policy":{"on_unhealthy":"rollback"}},"my_actions":["deploy"]},
		{"name":"bad","kind":"image","command_display":"d:1","spec":{"kind":"service","image":"d:1",
		 "health_policy":{"on_unhealthy":"` + leak + `","unhealthy_after_sec":"` + leak + `"}},"my_actions":["deploy"]},
		{"name":"jobby","kind":"image","command_display":"e:1","spec":{"kind":"job","image":"e:1"},"my_actions":["deploy"]},
		{"name":"prog","kind":"program","command_display":"pnpm start","my_actions":["run"]}
	]}`)
	out, err := projectCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), leak) {
		t.Fatalf("leak in %s", out)
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"plain":   `{"max_actions_per_hour":3,"on_deploy_failure":"rollback","on_unhealthy":"none","unhealthy_after_sec":45}`,
		"set":     `{"max_actions_per_hour":5,"on_deploy_failure":"stop","on_unhealthy":"restart","unhealthy_after_sec":30}`,
		"partial": `{"max_actions_per_hour":3,"on_deploy_failure":"rollback","on_unhealthy":"rollback","unhealthy_after_sec":45}`,
		"bad":     `{"max_actions_per_hour":3,"on_deploy_failure":"rollback","on_unhealthy":"none","unhealthy_after_sec":45}`,
	}
	for _, item := range body.Items {
		name, _ := item["name"].(string)
		policy, present := item["health_policy"]
		if name == "jobby" {
			if v, present := item["health_policy"]; !present || v != nil {
				t.Fatalf("job health_policy %#v", v)
			}
			continue
		}
		if name == "prog" {
			if present {
				t.Fatalf("program has health_policy")
			}
			continue
		}
		got, _ := json.Marshal(policy)
		if string(got) != want[name] {
			t.Fatalf("%s: %s want %s", name, got, want[name])
		}
	}
}

func TestProjectHistorySkipsAutomaticRows(t *testing.T) {
	raw := json.RawMessage(`{"items":[
		{"digest":"sha256:` + strings.Repeat("a", 64) + `","outcome":"auto_rolled_back","via":"agent","by":null,"started_at":"2026-10-23T10:00:00Z"},
		{"outcome":"auto_failed","via":"agent","by":null,"started_at":"2026-10-23T09:30:00Z"},
		{"digest":"sha256:` + strings.Repeat("b", 64) + `","outcome":"auto_gave_up","via":"agent","by":null,"started_at":"2026-10-23T09:00:00Z"},
		{"digest":"sha256:` + strings.Repeat("c", 64) + `","outcome":"auto_restarted","via":"agent","by":null,"started_at":"2026-10-23T08:00:00Z"},
		{"digest":"sha256:` + strings.Repeat("d", 64) + `","outcome":"succeeded","via":"console","by":"u1","started_at":"2026-10-23T07:00:00Z","finished_at":"2026-10-23T07:01:00Z"},
		{"digest":"sha256:` + strings.Repeat("e", 64) + `","outcome":"rolled_back","via":"mcp","by":"u1","started_at":"2026-10-23T06:00:00Z"}
	]}`)
	rows := projectHistory(raw)
	if len(rows) != 2 {
		t.Fatalf("rows %#v", rows)
	}
	first, _ := rows[0].(map[string]any)
	if first["outcome"] != "succeeded" || first["time"] != "2026-10-23T07:01:00Z" {
		t.Fatalf("first %#v", first)
	}
	if _, has := first["by"]; has {
		t.Fatalf("by kept: %#v", first)
	}
	blob, err := json.Marshal(map[string]any{"history": rows})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(historyLines(blob), "auto_") {
		t.Fatalf("automatic row listed: %q", historyLines(blob))
	}
}
