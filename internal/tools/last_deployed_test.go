package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectCatalogLastDeployedAt(t *testing.T) {
	const leak = "last-deployed-do-not-keep"
	raw := []byte(`{"items":[
		{"name":"prog","kind":"program","command_display":"pnpm start","my_actions":["run"],"last_deployed_at":"2026-10-29T08:15:00Z"},
		{"name":"present","kind":"image","command_display":"a:1","spec":{"kind":"service","image":"a:1"},"my_actions":["deploy"],"last_deployed_at":"2026-10-30T01:02:03.456+08:00"},
		{"name":"null","kind":"image","command_display":"b:1","spec":{"kind":"service","image":"b:1"},"my_actions":["deploy"],"last_deployed_at":null},
		{"name":"absent","kind":"image","command_display":"c:1","spec":{"kind":"service","image":"c:1"},"my_actions":["deploy"]},
		{"name":"invalid","kind":"image","command_display":"d:1","spec":{"kind":"service","image":"d:1"},"my_actions":["deploy"],"last_deployed_at":"` + leak + `"},
		{"name":"number","kind":"image","command_display":"e:1","spec":{"kind":"job","image":"e:1"},"my_actions":["deploy"],"last_deployed_at":1730000000},
		{"name":"oldprog","kind":"program","command_display":"pnpm dev","my_actions":["run"]}
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
	want := map[string]any{
		"prog":    "2026-10-29T08:15:00Z",
		"present": "2026-10-30T01:02:03.456+08:00",
		"null":    nil,
		"absent":  nil,
		"invalid": nil,
		"number":  nil,
		"oldprog": nil,
	}
	if len(body.Items) != len(want) {
		t.Fatalf("items %#v", body.Items)
	}
	for _, item := range body.Items {
		name, _ := item["name"].(string)
		got, present := item["last_deployed_at"]
		if !present || got != want[name] {
			t.Fatalf("%s last_deployed_at %#v present=%v", name, got, present)
		}
	}
}

// The catalog-write confirm result is not list_catalog; v1.21 changes only the list.
func TestCatalogWriteConfirmOmitsLastDeployedAt(t *testing.T) {
	out, err := projectOneCatalogEntry(json.RawMessage(`{"name":"web","kind":"image","command_display":"a:1","spec":{"kind":"service","image":"a:1"},"my_actions":["deploy"],"last_deployed_at":"2026-10-29T08:15:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "last_deployed_at") {
		t.Fatalf("confirm projection %s", out)
	}
}

func TestCatalogWriteRefusesLastDeployedAtInSpec(t *testing.T) {
	if err := validateImageSpec(json.RawMessage(`{"kind":"service","image":"a:1","last_deployed_at":"2026-10-29T08:15:00Z"}`)); err == nil {
		t.Fatal("spec with last_deployed_at accepted")
	}
}
