package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// listToolDescriptions registers every tool and reads tools/list over an in-memory session.
func listToolDescriptions(t *testing.T) map[string]string {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(server, nil)
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, tool := range res.Tools {
		out[tool.Name] = tool.Description
	}
	return out
}

// Brief program-entries-over-mcp item A: agents read the first words first.
func TestCatalogWriteToolsSayImageEntriesOnly(t *testing.T) {
	desc := listToolDescriptions(t)
	for _, name := range []string{"create_catalog_entry", "update_catalog_entry", "delete_catalog_entry"} {
		d, ok := desc[name]
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if !strings.HasPrefix(d, "Image entries only. ") {
			t.Errorf("%s description starts %q", name, d[:min(len(d), 40)])
		}
		if !strings.Contains(d, "in the console") {
			t.Errorf("%s description does not send program entries to the console", name)
		}
	}
}

// Brief item E: a program confirm can answer before the host reports ports.
func TestConfirmDescriptionSaysProgramPortsComeLater(t *testing.T) {
	d := listToolDescriptions(t)["confirm"]
	if !strings.Contains(d, "ports and started_at can still be empty") || !strings.Contains(d, "entry_status") {
		t.Errorf("confirm description lacks the ports note: %q", d)
	}
}
