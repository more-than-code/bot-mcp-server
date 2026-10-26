package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// api-v1 v1.18 item 1: the catalog write tools tell callers that test is the
// bare command, without Docker's CMD prefix.
func TestHealthcheckTestToolSchema(t *testing.T) {
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
		tool := byName[name]
		if tool == nil {
			t.Fatalf("missing tool %s", name)
		}
		if !strings.Contains(tool.Description, "Deploybot adds Docker's CMD") || !strings.Contains(tool.Description, `["sh","-c"`) {
			t.Fatalf("%s description %q", name, tool.Description)
		}
		props, _ := tool.InputSchema.(map[string]any)["properties"].(map[string]any)
		spec, _ := props["spec"].(map[string]any)["properties"].(map[string]any)
		healthcheck, _ := spec["healthcheck"].(map[string]any)["properties"].(map[string]any)
		test, _ := healthcheck["test"].(map[string]any)
		description, _ := test["description"].(string)
		if description == "" || !strings.Contains(description, "without CMD") {
			t.Fatalf("%s healthcheck.test description %q", name, description)
		}
	}
}
