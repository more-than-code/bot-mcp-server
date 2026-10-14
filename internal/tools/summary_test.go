package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServiceLabel(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		discovered  string
		owner       string
		want        string
	}{
		{name: "display name", displayName: "Cache", discovered: "port-8080", owner: "ada", want: "Cache"},
		{name: "display name that looks like a port", displayName: "port-1", discovered: "redis", want: "port-1"},
		{name: "discovered name", discovered: "api", owner: "ada", want: "api"},
		{name: "port with owner", discovered: "port-8080", owner: "ada", want: "port 8080 · ada"},
		{name: "port without owner", discovered: "port-22", want: "port 22"},
		{name: "leading zeros", discovered: "port-080", owner: "root", want: "port 080 · root"},
		{name: "port zero", discovered: "port-0", want: "port 0"},
		{name: "not a whole port name", discovered: "port-80-extra", owner: "ada", want: "port-80-extra"},
		{name: "port word", discovered: "port-http", want: "port-http"},
		{name: "prefix only", discovered: "port-", want: "port-"},
		{name: "empty", want: ""},
		{name: "display name newline", displayName: "a\nb", discovered: "api", want: "a\uFFFDb"},
		{name: "owner newline", discovered: "port-80", owner: "ada\rroot", want: "port 80 · ada\uFFFDroot"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := serviceLabel(tc.displayName, tc.discovered, tc.owner)
			if got != tc.want {
				t.Fatalf("serviceLabel(%q, %q, %q) = %q, want %q", tc.displayName, tc.discovered, tc.owner, got, tc.want)
			}
		})
	}
}

func TestSummarizeServices(t *testing.T) {
	payload := []byte(`{
		"items": [
			{"name": "port-8080", "display_name": null, "owner_user": "ada", "host_name": "pi1", "health": {"status": "up"}},
			{"name": "redis", "display_name": "Cache", "owner_user": null, "host_name": "pi2", "health": {"status": ""}}
		],
		"next_cursor": "n",
		"prev_cursor": null,
		"total": 5
	}`)
	wantProject := "5 services, more pages\nport 8080 · ada up pi1\nCache unknown pi2"
	if got := summarizeServices(payload, true); got != wantProject {
		t.Fatalf("project summary\n got %q\nwant %q", got, wantProject)
	}
	wantHost := "2 services\nport 8080 · ada up\nCache unknown"
	if got := summarizeServices(payload, false); got != wantHost {
		t.Fatalf("host summary\n got %q\nwant %q", got, wantHost)
	}

	last := []byte(`{"items":[{"name":"api","health":{"status":"up"}}],"next_cursor":null,"prev_cursor":"p","total":1}`)
	if got := summarizeServices(last, true); got != "1 services, no more pages\napi up" {
		t.Fatalf("last page %q", got)
	}
	blankCursor := []byte(`{"items":[],"next_cursor":"","total":0}`)
	if got := summarizeServices(blankCursor, true); got != "0 services, no more pages" {
		t.Fatalf("blank cursor %q", got)
	}
	noTotal := []byte(`{"items":[{"name":"api","health":{"status":"up"}}],"next_cursor":"n"}`)
	if got := summarizeServices(noTotal, true); got != "1 services, more pages\napi up" {
		t.Fatalf("missing total %q", got)
	}
	if got := summarizeServices([]byte(`{`), true); got != "services" {
		t.Fatalf("bad json %q", got)
	}
	dirty := []byte(`{"items":[{"name":"api","display_name":"ca\nche","host_name":"pi\r1","health":{"status":"up\n"}}]}`)
	if got := summarizeServices(dirty, true); got != "1 services, no more pages\nca\uFFFDche up\uFFFD pi\uFFFD1" {
		t.Fatalf("dirty %q", got)
	}
}

func TestSummaryField(t *testing.T) {
	long := strings.Repeat("é", summaryFieldRunes)
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", want: ""},
		{name: "plain", in: "api", want: "api"},
		{name: "newline and return", in: "a\nb\rc", want: "a\uFFFDb\uFFFDc"},
		{name: "other controls", in: "\t\x00\x7f", want: "\uFFFD\uFFFD\uFFFD"},
		{name: "at cap", in: long, want: long},
		{name: "over cap", in: long + "x", want: strings.Repeat("é", summaryFieldRunes-1) + "…"},
		{name: "ascii over cap", in: strings.Repeat("a", summaryFieldRunes+1), want: strings.Repeat("a", summaryFieldRunes-1) + "…"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := summaryField(tc.in)
			if got != tc.want {
				t.Fatalf("summaryField(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if n := utf8.RuneCountInString(got); n > summaryFieldRunes {
				t.Fatalf("rune count %d", n)
			}
		})
	}
}

func TestSummaryFieldClasses(t *testing.T) {
	tests := []struct {
		name string
		in   rune
	}{
		{name: "Cc", in: '\n'},
		{name: "Cf", in: '\u200B'},
		{name: "Cf bidi 202A", in: '\u202A'},
		{name: "Cf bidi 202E", in: '\u202E'},
		{name: "Cf bidi 2066", in: '\u2066'},
		{name: "Cf bidi 2069", in: '\u2069'},
		{name: "Zl", in: '\u2028'},
		{name: "Zp", in: '\u2029'},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := summaryField("a" + string(tc.in) + "b")
			if got != "a\uFFFDb" {
				t.Fatalf("summaryField class %s (%U) = %q", tc.name, tc.in, got)
			}
		})
	}
}

func TestSummariesReplaceSeparators(t *testing.T) {
	const dirty = "a\nb\u2028c\u202Ed"
	const clean = "a\uFFFDb\uFFFDc\uFFFDd"
	mustSummary := func(t *testing.T, name, got, want string, lines int) {
		t.Helper()
		if got != want {
			t.Fatalf("%s\n got %q\nwant %q", name, got, want)
		}
		if n := len(strings.Split(got, "\n")); n != lines {
			t.Fatalf("%s lines %d", name, n)
		}
		for _, r := range []rune{'\u2028', '\u2029', '\u202A', '\u202E', '\u2066', '\u2069'} {
			if strings.ContainsRune(got, r) {
				t.Fatalf("%s contains %U", name, r)
			}
		}
	}

	projects, err := json.Marshal(map[string]any{
		"projects": []any{map[string]any{"name": dirty, "role": dirty}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustSummary(t, "projects", summarizeProjects(projects), "1 projects: "+clean+" ("+clean+")", 1)

	hosts, err := json.Marshal(map[string]any{
		"items": []any{map[string]any{
			"name": dirty, "status": dirty, "relation": "shared", "share_level": dirty,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustSummary(t, "hosts", summarizeHosts(hosts), "1 hosts: "+clean+" "+clean+" (shared: "+clean+")", 1)

	processes, err := json.Marshal(map[string]any{
		"sampled_at": dirty,
		"items":      []any{map[string]any{"process_name": dirty}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustSummary(t, "processes", summarizeProcesses(processes), "1 processes at "+clean+": "+clean, 1)

	mustSummary(t, "metrics", summarizeMetrics(dirty, []byte(`{"series":[{"points":[]}]}`)), clean+": 1 series, 0 points", 1)

	snapshot, err := json.Marshal(map[string]any{"sampled_at": dirty, "cpu_percent": 1.5})
	if err != nil {
		t.Fatal(err)
	}
	mustSummary(t, "snapshot", summarizeSnapshot(snapshot), "snapshot at "+clean+", cpu 1.5%", 1)

	probe, err := json.Marshal(map[string]any{
		"result": dirty, "protocol": dirty, "port": 80, "service": dirty, "latency_ms": 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	mustSummary(t, "probe", summarizeProbe(probe), "probe "+clean+" "+clean+":80 ("+clean+") 3ms", 1)

	services, err := json.Marshal(map[string]any{
		"items": []any{
			map[string]any{"display_name": dirty, "name": "api", "host_name": dirty, "health": map[string]any{"status": dirty}},
			map[string]any{"name": "port-22", "owner_user": dirty, "host_name": dirty, "health": map[string]any{"status": dirty}},
			map[string]any{"name": dirty, "host_name": dirty, "health": map[string]any{"status": dirty}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	serviceWant := "3 services, no more pages\n" +
		clean + " " + clean + " " + clean + "\n" +
		"port 22 · " + clean + " " + clean + " " + clean + "\n" +
		clean + " " + clean + " " + clean
	mustSummary(t, "services", summarizeServices(services, true), serviceWant, 4)

	// summarizeHealth counts items and has no summaryField call site.
	health := []byte(`{"items":[{"note":"a\nb\u2028c\u202Ed"}]}`)
	mustSummary(t, "health", summarizeHealth(health), "1 health results", 1)
}

func TestPresentServicesTruncation(t *testing.T) {
	host := servicePayload(t, 500, false)
	res := presentServices(host, false)
	lines := strings.Split(toolText(t, res), "\n")
	if len(lines) != maxListItems+2 {
		t.Fatalf("host lines %d", len(lines))
	}
	if lines[0] != "500 services" {
		t.Fatalf("head %q", lines[0])
	}
	if lines[len(lines)-1] != "truncated to 200 items." {
		t.Fatalf("note %q", lines[len(lines)-1])
	}
	if strings.Contains(lines[len(lines)-2], "truncated") {
		t.Fatalf("note glued to last item %q", lines[len(lines)-2])
	}
	names := structuredNames(t, res)
	if len(names) != maxListItems {
		t.Fatalf("structured %d", len(names))
	}
	for i := 0; i < maxListItems; i++ {
		wantLine := fmt.Sprintf("s%03d up", i)
		wantName := fmt.Sprintf("s%03d", i)
		if lines[i+1] != wantLine {
			t.Fatalf("line %d %q", i+1, lines[i+1])
		}
		if names[i] != wantName {
			t.Fatalf("item %d %q", i, names[i])
		}
	}
	if strings.Contains(toolText(t, res), "s200") {
		t.Fatal("dropped service was listed")
	}

	project := servicePayload(t, 200, true)
	res = presentServices(project, true)
	text := toolText(t, res)
	lines = strings.Split(text, "\n")
	if len(lines) != 201 {
		t.Fatalf("project lines %d", len(lines))
	}
	if lines[0] != "640 services, more pages" {
		t.Fatalf("project head %q", lines[0])
	}
	if strings.Contains(text, "truncated") {
		t.Fatal("project page was truncated")
	}
	if lines[1] != "s000 up pi1" || lines[200] != "s199 up pi1" {
		t.Fatalf("project edges %q %q", lines[1], lines[200])
	}
}

func servicePayload(t *testing.T, n int, project bool) json.RawMessage {
	t.Helper()
	items := make([]any, n)
	for i := range items {
		item := map[string]any{
			"name":   fmt.Sprintf("s%03d", i),
			"health": map[string]any{"status": "up"},
		}
		if project {
			item["host_name"] = "pi1"
		}
		items[i] = item
	}
	body := map[string]any{"items": items}
	if project {
		body["total"] = 640
		body["next_cursor"] = "next"
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func toolText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("content %#v", res)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content %T", res.Content[0])
	}
	return text.Text
}

func structuredNames(t *testing.T, res *mcp.CallToolResult) []string {
	t.Helper()
	raw, ok := res.StructuredContent.(json.RawMessage)
	if !ok {
		t.Fatalf("structured %T", res.StructuredContent)
	}
	var body struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(body.Items))
	for i, item := range body.Items {
		names[i] = item.Name
	}
	return names
}
