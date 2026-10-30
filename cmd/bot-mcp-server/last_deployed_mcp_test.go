package main

import (
	"context"
	"strings"
	"testing"
)

func TestLastDeployedCatalogList(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	fake.setMode("catalog-last-deployed")
	res := mustCall(t, cs, "list_catalog", map[string]any{"project_id": projectID})
	if res.IsError {
		t.Fatalf("catalog %s", textOf(t, res))
	}
	blob := mustJSON(t, res.StructuredContent)
	if strings.Contains(blob, "last-deployed-do-not-keep") {
		t.Fatalf("invalid last_deployed_at kept %s", blob)
	}
	items, _ := structuredMap(t, res)["items"].([]any)
	if len(items) != 5 {
		t.Fatalf("items %#v", items)
	}
	want := map[string]any{
		"tra-backend": "2026-10-29T08:15:00Z",
		"redis":       "2026-10-30T01:02:03.456Z",
		"nginx":       nil,
		"old":         nil,
		"bad":         nil,
	}
	for _, item := range items {
		row, _ := item.(map[string]any)
		name, _ := row["name"].(string)
		if _, present := row["last_deployed_at"]; !present || row["last_deployed_at"] != want[name] {
			t.Fatalf("%s last_deployed_at %#v", name, row["last_deployed_at"])
		}
	}
}

func TestLastDeployedToolDescription(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == "list_catalog" {
			if !strings.Contains(tool.Description, "last_deployed_at") {
				t.Fatalf("list_catalog description %q", tool.Description)
			}
			return
		}
	}
	t.Fatal("list_catalog not listed")
}
