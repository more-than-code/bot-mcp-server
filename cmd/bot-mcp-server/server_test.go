package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const (
	projectID      = "11111111-1111-4111-8111-111111111111"
	manyProjectID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	hostID         = "22222222-2222-4222-8222-222222222222"
	sharedHostID   = "66666666-6666-4666-8666-666666666666"
	serviceID      = "33333333-3333-4333-8333-333333333333"
	checkID        = "44444444-4444-4444-8444-444444444444"
	userToken      = "session-token.abc~def"
	delegatedToken = "delegated-token.xyz~123"
	clientSecret   = "mcp-client-secret"
	entryName      = "tra-backend"
	// confirmationID is 43 base64url characters, the v1.6 confirmation id length.
	confirmationID = "Abcdefghijklmnopqrstuvwxyz0123456789_-ABCDE"
)

func TestMissingAndInvalidToken(t *testing.T) {
	fake := &fakeAPI{}
	mcpSrv, _ := newTestServer(t, fake)
	wantAuth := `Bearer resource_metadata="https://mcp.example/.well-known/oauth-protected-resource"`
	cases := []struct {
		name   string
		header string
	}{
		{name: "absent"},
		{name: "empty scheme", header: "Bearer"},
		{name: "blank token", header: "Bearer "},
		{name: "basic", header: "Basic abc"},
		{name: "embedded newline", header: "Bearer tok\nen"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := fake.count()
			// A newline cannot be sent with http.Client; call the handler directly.
			if strings.Contains(tc.header, "\n") {
				req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
				// A path-only NewRequest has an empty Host, which /mcp now refuses
				// before auth. Set a loopback Host so this still reaches the token check.
				req.Host = "127.0.0.1"
				req.Header["Authorization"] = []string{tc.header}
				rec := httptest.NewRecorder()
				mcpSrv.Config.Handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
				}
				if got := rec.Header().Get("WWW-Authenticate"); got != wantAuth {
					t.Fatalf("WWW-Authenticate = %q", got)
				}
				if fake.count() != before {
					t.Fatalf("api calls %d, want %d", fake.count(), before)
				}
				return
			}
			req, err := http.NewRequest(http.MethodPost, mcpSrv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status %d body %s", resp.StatusCode, body)
			}
			if got := resp.Header.Get("WWW-Authenticate"); got != wantAuth {
				t.Fatalf("WWW-Authenticate = %q", got)
			}
			if fake.count() != before {
				t.Fatalf("api calls %d, want %d", fake.count(), before)
			}
		})
	}
}

func TestHealthAndProtectedResource(t *testing.T) {
	fake := &fakeAPI{}
	mcpSrv, _ := newTestServer(t, fake)

	health, err := http.Get(mcpSrv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer health.Body.Close()
	healthBody, _ := io.ReadAll(health.Body)
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health status %d", health.StatusCode)
	}
	if strings.TrimSpace(string(healthBody)) != `{"status":"ok"}` {
		t.Fatalf("health body %s", healthBody)
	}

	meta, err := http.Get(mcpSrv.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	defer meta.Body.Close()
	var doc struct {
		Resource               string   `json:"resource"`
		AuthorizationServers   []string `json:"authorization_servers"`
		BearerMethodsSupported []string `json:"bearer_methods_supported"`
		ScopesSupported        []string `json:"scopes_supported"`
	}
	if err := json.NewDecoder(meta.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.Resource != "https://mcp.example/mcp" {
		t.Fatalf("resource %q", doc.Resource)
	}
	if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != fake.base {
		t.Fatalf("authorization_servers %#v", doc.AuthorizationServers)
	}
	if len(doc.BearerMethodsSupported) != 1 || doc.BearerMethodsSupported[0] != "header" {
		t.Fatalf("bearer_methods %#v", doc.BearerMethodsSupported)
	}
	if len(doc.ScopesSupported) != 1 || doc.ScopesSupported[0] != "deploybot" {
		t.Fatalf("scopes_supported %#v", doc.ScopesSupported)
	}
	if fake.count() != 0 {
		t.Fatalf("unauthenticated routes called the api %d times", fake.count())
	}
}

func TestTools(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	if cs.InitializeResult() == nil {
		t.Fatal("missing initialize result")
	}
	t.Logf("negotiated protocol %s", cs.InitializeResult().ProtocolVersion)

	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	closed := map[string]bool{
		"list_projects": true, "list_hosts": true, "list_services": true,
		"query_metrics": true, "list_processes": true, "service_health_history": true,
		"get_health_checks": true, "list_catalog": true,
	}
	live := map[string]bool{
		"host_snapshot": true, "probe_service": true,
		"entry_logs": true, "entry_status": true, "confirm": true,
	}
	pending := map[string]bool{
		"run_entry": true, "stop_entry": true, "restart_entry": true,
		"deploy_entry": true, "rollback_entry": true,
	}
	seen := map[string]bool{}
	for _, tool := range listed.Tools {
		seen[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
		open := tool.Annotations != nil && tool.Annotations.OpenWorldHint != nil && *tool.Annotations.OpenWorldHint
		switch {
		case pending[tool.Name]:
			if readOnly || open {
				t.Errorf("%s readOnly=%v openWorld=%v", tool.Name, readOnly, open)
			}
			for _, phrase := range []string{"real", "audited", "second confirm", "user agrees", "Nothing runs"} {
				if !strings.Contains(tool.Description, phrase) {
					t.Errorf("%s description %q missing %q", tool.Name, tool.Description, phrase)
				}
			}
		case tool.Name == "set_health_checks":
			if readOnly || open {
				t.Errorf("%s readOnly=%v openWorld=%v", tool.Name, readOnly, open)
			}
			for _, phrase := range []string{
				"all health checks",
				"Nothing changes until confirm",
				"user agrees",
				"127.0.0.1",
				"get_health_checks",
				"replaces all checks",
			} {
				if !strings.Contains(tool.Description, phrase) {
					t.Errorf("%s description %q missing %q", tool.Name, tool.Description, phrase)
				}
			}
		case tool.Name == "create_catalog_entry" || tool.Name == "update_catalog_entry" || tool.Name == "delete_catalog_entry":
			if readOnly || open {
				t.Errorf("%s readOnly=%v openWorld=%v", tool.Name, readOnly, open)
			}
			for _, phrase := range []string{
				"Nothing changes until confirm",
				"user agrees",
				"console",
				"Does not contact",
			} {
				if !strings.Contains(tool.Description, phrase) {
					t.Errorf("%s description %q missing %q", tool.Name, tool.Description, phrase)
				}
			}
			if tool.Name != "delete_catalog_entry" && !strings.Contains(tool.Description, "env sets") {
				t.Errorf("%s description %q missing env sets", tool.Name, tool.Description)
			}
			if tool.Name == "delete_catalog_entry" && !strings.Contains(tool.Description, "not stopped") {
				t.Errorf("%s description %q missing not stopped", tool.Name, tool.Description)
			}
		case live[tool.Name]:
			if tool.Name == "confirm" {
				if readOnly {
					t.Errorf("confirm is read-only")
				}
				for _, phrase := range []string{"real", "audited", "program status"} {
					if !strings.Contains(tool.Description, phrase) {
						t.Errorf("%s description %q missing %q", tool.Name, tool.Description, phrase)
					}
				}
			} else if !readOnly {
				t.Errorf("%s readOnlyHint missing", tool.Name)
			}
			if !open || !strings.Contains(tool.Description, "contacts the host now") {
				t.Errorf("%s description %q openWorld=%v", tool.Name, tool.Description, open)
			}
		case closed[tool.Name]:
			if !readOnly {
				t.Errorf("%s readOnlyHint missing", tool.Name)
			}
			if open || !strings.Contains(tool.Description, "Does not contact") {
				t.Errorf("%s description %q openWorld=%v", tool.Name, tool.Description, open)
			}
		default:
			t.Errorf("unclassified tool %s", tool.Name)
		}
		if tool.Name == "list_services" {
			for _, phrase := range []string{
				"host_id",
				"project_id",
				"paged",
				"next_cursor",
				"prev_cursor",
				"total",
				"limited to what that project may see",
				"Results are limited to what the caller's projects may see.",
				"cursor, and view apply only with project_id",
				"view is all (every row; omit view to leave the API default all)",
				"deploybot (source owned or declared, gone or not, except a gone owned row whose catalog entry was deleted, which only all lists)",
				"deploybot_and_discovered (those rows plus discovered rows the host still reports)",
				"gone_at set means the host no longer reports the service (a stopped program or a listener that went away)",
			} {
				if !strings.Contains(tool.Description, phrase) {
					t.Errorf("%s description %q missing %q", tool.Name, tool.Description, phrase)
				}
			}
		}
		if tool.Name == "list_processes" &&
			!strings.Contains(tool.Description, "Shows every process on the host to any member who can see the host.") {
			t.Errorf("%s description %q", tool.Name, tool.Description)
		}
		if tool.Name == "deploy_entry" || tool.Name == "rollback_entry" {
			for _, phrase := range []string{"deploy permission", "run containers"} {
				if !strings.Contains(tool.Description, phrase) {
					t.Errorf("%s description %q missing %q", tool.Name, tool.Description, phrase)
				}
			}
		}
		if tool.Name == "list_catalog" {
			for _, phrase := range []string{"env values", "volume short names"} {
				if !strings.Contains(tool.Description, phrase) {
					t.Errorf("%s description %q missing %q", tool.Name, tool.Description, phrase)
				}
			}
		}
	}
	for _, name := range []string{
		"list_projects", "list_hosts", "list_services", "query_metrics",
		"list_processes", "service_health_history", "get_health_checks", "host_snapshot", "probe_service",
		"list_catalog", "run_entry", "stop_entry", "restart_entry",
		"deploy_entry", "rollback_entry", "set_health_checks",
		"create_catalog_entry", "update_catalog_entry", "delete_catalog_entry",
		"confirm", "entry_logs", "entry_status",
	} {
		if !seen[name] {
			t.Errorf("missing tool %s", name)
		}
	}
	if len(seen) != 22 {
		t.Fatalf("tool count %d", len(seen))
	}

	res := mustCall(t, cs, "list_projects", map[string]any{})
	assertTool(t, fake, res, http.MethodGet, "/v1/me", "", "")
	if !strings.Contains(textOf(t, res), "1 projects: lab (owner)") {
		t.Fatalf("list_projects text %q", textOf(t, res))
	}

	res = mustCall(t, cs, "list_hosts", map[string]any{"project_id": projectID})
	assertTool(t, fake, res, http.MethodGet, "/v1/projects/"+projectID+"/hosts", "", "")
	if textOf(t, res) != "2 hosts: pi1 online, pi2 online (shared: metrics)" {
		t.Fatalf("list_hosts text %q", textOf(t, res))
	}
	hostItems, ok := structuredMap(t, res)["items"].([]any)
	if !ok || len(hostItems) != 2 {
		t.Fatalf("list_hosts items %#v", structuredMap(t, res)["items"])
	}
	owned, _ := hostItems[0].(map[string]any)
	shared, _ := hostItems[1].(map[string]any)
	if owned["id"] != hostID || owned["relation"] != "owner" || owned["share_level"] != nil {
		t.Fatalf("owned host %#v", owned)
	}
	if shared["id"] != sharedHostID || shared["relation"] != "shared" || shared["share_level"] != "metrics" {
		t.Fatalf("shared host %#v", shared)
	}

	res = mustCall(t, cs, "list_services", map[string]any{"host_id": hostID})
	assertTool(t, fake, res, http.MethodGet, "/v1/hosts/"+hostID+"/services", "", "")
	if textOf(t, res) != "1 services\napi up" {
		t.Fatalf("list_services text %q", textOf(t, res))
	}

	from := "2026-10-13T00:00:00Z"
	to := "2026-10-13T01:00:00Z"
	res = mustCall(t, cs, "query_metrics", map[string]any{
		"host_id": hostID, "metric": "cpu", "from": from, "to": to, "step": 60,
	})
	assertTool(t, fake, res, http.MethodGet, "/v1/hosts/"+hostID+"/metrics", "from="+url.QueryEscape(from)+"&m=cpu&step=60&to="+url.QueryEscape(to), "")
	if !strings.Contains(textOf(t, res), "cpu: 1 series, 1 points") {
		t.Fatalf("query_metrics text %q", textOf(t, res))
	}

	res = mustCall(t, cs, "list_processes", map[string]any{"host_id": hostID, "sort": "memory", "limit": 2})
	assertTool(t, fake, res, http.MethodGet, "/v1/hosts/"+hostID+"/processes", "limit=2&sort=memory", "")
	if !strings.Contains(textOf(t, res), "1 processes at 2026-10-13T00:00:00Z: bot") {
		t.Fatalf("list_processes text %q", textOf(t, res))
	}

	res = mustCall(t, cs, "service_health_history", map[string]any{"service_id": serviceID, "from": from, "to": to})
	assertTool(t, fake, res, http.MethodGet, "/v1/services/"+serviceID+"/health", "from="+url.QueryEscape(from)+"&to="+url.QueryEscape(to), "")
	if textOf(t, res) != "1 health results" {
		t.Fatalf("health text %q", textOf(t, res))
	}

	res = mustCall(t, cs, "host_snapshot", map[string]any{"host_id": hostID})
	assertTool(t, fake, res, http.MethodPost, "/v1/hosts/"+hostID+"/snapshot", "", "")
	if fake.last().ContentType != "" {
		t.Fatalf("snapshot content-type %q", fake.last().ContentType)
	}
	if !strings.Contains(textOf(t, res), "snapshot at 2026-10-13T00:00:00Z, cpu 12.5%") {
		t.Fatalf("snapshot text %q", textOf(t, res))
	}

	res = mustCall(t, cs, "probe_service", map[string]any{"service_id": serviceID, "check_id": checkID})
	assertTool(t, fake, res, http.MethodPost, "/v1/services/"+serviceID+"/probe", "", `{"check_id":"`+checkID+`"}`)
	if !strings.Contains(textOf(t, res), "probe up http:8080 (api) 7ms") {
		t.Fatalf("probe text %q", textOf(t, res))
	}

	res = mustCall(t, cs, "probe_service", map[string]any{
		"service_id": serviceID, "protocol": "http", "port": 8080, "path": "/healthz",
	})
	assertTool(t, fake, res, http.MethodPost, "/v1/services/"+serviceID+"/probe", "", `{"protocol":"http","port":8080,"path":"/healthz"}`)
	if res.IsError {
		t.Fatalf("adhoc probe error %s", textOf(t, res))
	}

	res = mustCall(t, cs, "probe_service", map[string]any{
		"service_id": serviceID, "protocol": "tcp", "port": 22,
	})
	assertTool(t, fake, res, http.MethodPost, "/v1/services/"+serviceID+"/probe", "", `{"protocol":"tcp","port":22,"path":null}`)

	before := fake.count()
	res = mustCall(t, cs, "list_hosts", map[string]any{"project_id": manyProjectID})
	assertLast(t, fake, http.MethodGet, "/v1/projects/"+manyProjectID+"/hosts", "", "")
	if fake.count() != before+1 {
		t.Fatalf("truncation called api %d times", fake.count()-before)
	}
	text := textOf(t, res)
	if !strings.Contains(text, "201 hosts") || !strings.Contains(text, "truncated to 200 items.") {
		t.Fatalf("truncation text %q", text)
	}
	body := structuredMap(t, res)
	assertKeptPrefix(t, body, "items", "item-")
	assertKeptPrefix(t, body, "projects", "proj-")

	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestListServicesProject(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	res := mustCall(t, cs, "list_services", map[string]any{"project_id": projectID})
	assertTool(t, fake, res, http.MethodGet, "/v1/projects/"+projectID+"/services", "", "")
	if textOf(t, res) != "5 services, more pages\nport 8080 · ada up pi1\nCache down pi2 (gone_at 2026-10-15T14:13:00Z)" {
		t.Fatalf("text %q", textOf(t, res))
	}
	assertServiceGoneAt(t, res, 0, nil)
	assertServiceGoneAt(t, res, 1, "2026-10-15T14:13:00Z")

	q := "a b&100%"
	cursor := "x/y"
	res = mustCall(t, cs, "list_services", map[string]any{
		"project_id": projectID,
		"q":          q,
		"sort":       "last_seen",
		"dir":        "desc",
		"limit":      200,
		"cursor":     cursor,
	})
	wantQuery := "cursor=" + url.QueryEscape(cursor) + "&dir=desc&limit=200&q=" + url.QueryEscape(q) + "&sort=last_seen"
	assertTool(t, fake, res, http.MethodGet, "/v1/projects/"+projectID+"/services", wantQuery, "")

	q = strings.Repeat("é", 100)
	res = mustCall(t, cs, "list_services", map[string]any{"project_id": projectID, "q": q, "limit": 1})
	wantQuery = "limit=1&q=" + url.QueryEscape(q)
	assertTool(t, fake, res, http.MethodGet, "/v1/projects/"+projectID+"/services", wantQuery, "")

	res = mustCall(t, cs, "list_services", map[string]any{"project_id": projectID, "q": ""})
	assertTool(t, fake, res, http.MethodGet, "/v1/projects/"+projectID+"/services", "q=", "")

	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestListServicesView(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	listed, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	for _, tool := range listed.Tools {
		if tool.Name != "list_services" {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
	}
	if schema == nil {
		t.Fatal("list_services schema missing")
	}
	props, _ := schema["properties"].(map[string]any)
	view, _ := props["view"].(map[string]any)
	enum, _ := view["enum"].([]any)
	wantEnum := []any{"all", "deploybot", "deploybot_and_discovered"}
	if !reflect.DeepEqual(enum, wantEnum) {
		t.Fatalf("view enum %#v", enum)
	}
	then, _ := schema["then"].(map[string]any)
	not, _ := then["not"].(map[string]any)
	anyOf, _ := not["anyOf"].([]any)
	forbidsView := false
	for _, item := range anyOf {
		clause, _ := item.(map[string]any)
		req, _ := clause["required"].([]any)
		if len(req) == 1 && req[0] == "view" {
			forbidsView = true
		}
	}
	if !forbidsView {
		t.Fatalf("schema does not reject view with host_id: %#v", schema["then"])
	}

	res := mustCall(t, cs, "list_services", map[string]any{"project_id": projectID})
	assertTool(t, fake, res, http.MethodGet, "/v1/projects/"+projectID+"/services", "", "")
	if strings.Contains(fake.last().RawQuery, "view") {
		t.Fatalf("omitted view was sent %q", fake.last().RawQuery)
	}
	assertServiceGoneAt(t, res, 0, nil)
	assertServiceGoneAt(t, res, 1, "2026-10-15T14:13:00Z")

	res = mustCall(t, cs, "list_services", map[string]any{"host_id": hostID})
	assertTool(t, fake, res, http.MethodGet, "/v1/hosts/"+hostID+"/services", "", "")
	assertServiceGoneAt(t, res, 0, nil)
	if textOf(t, res) != "1 services\napi up" {
		t.Fatalf("null gone_at changed the summary %q", textOf(t, res))
	}

	for _, view := range []string{"all", "deploybot", "deploybot_and_discovered"} {
		t.Run("view "+view, func(t *testing.T) {
			res := mustCall(t, cs, "list_services", map[string]any{"project_id": projectID, "view": view})
			assertTool(t, fake, res, http.MethodGet, "/v1/projects/"+projectID+"/services", "view="+view, "")
		})
	}

	res = mustCall(t, cs, "list_services", map[string]any{
		"project_id": projectID, "q": "api", "sort": "name", "view": "deploybot",
	})
	assertTool(t, fake, res, http.MethodGet, "/v1/projects/"+projectID+"/services", "q=api&sort=name&view=deploybot", "")

	res = mustCall(t, cs, "list_services", map[string]any{"project_id": projectID, "sort": "name"})
	assertTool(t, fake, res, http.MethodGet, "/v1/projects/"+projectID+"/services", "sort=name", "")
	if strings.Contains(fake.last().RawQuery, "view") {
		t.Fatalf("view sent without the argument %q", fake.last().RawQuery)
	}

	t.Run("host view", func(t *testing.T) {
		before := fake.count()
		res := mustCall(t, cs, "list_services", map[string]any{"host_id": hostID, "view": "deploybot"})
		if !res.IsError || textOf(t, res) != "bad_request: q, sort, dir, limit, cursor, and view are only allowed with project_id" {
			t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
		}
		if fake.count() != before {
			t.Fatal("api was called")
		}
	})
	for _, bad := range []string{"bogus", "ALL", "all ", ""} {
		t.Run("bad view "+bad, func(t *testing.T) {
			before := fake.count()
			res := mustCall(t, cs, "list_services", map[string]any{"project_id": projectID, "view": bad})
			if !res.IsError || textOf(t, res) != "bad_request: view must be all, deploybot, or deploybot_and_discovered" {
				t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
			}
			if fake.count() != before {
				t.Fatal("api was called")
			}
		})
	}
}

func TestListServicesSortDir(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	for _, sort := range []string{"name", "host", "kind", "status", "last_seen"} {
		t.Run("sort "+sort, func(t *testing.T) {
			res := mustCall(t, cs, "list_services", map[string]any{"project_id": projectID, "sort": sort})
			assertTool(t, fake, res, http.MethodGet, "/v1/projects/"+projectID+"/services", "sort="+sort, "")
		})
	}
	for _, dir := range []string{"asc", "desc"} {
		t.Run("dir "+dir, func(t *testing.T) {
			res := mustCall(t, cs, "list_services", map[string]any{"project_id": projectID, "dir": dir})
			assertTool(t, fake, res, http.MethodGet, "/v1/projects/"+projectID+"/services", "dir="+dir, "")
		})
	}
	t.Run("bad sort", func(t *testing.T) {
		before := fake.count()
		res := mustCall(t, cs, "list_services", map[string]any{"project_id": projectID, "sort": "cpu"})
		if !res.IsError || textOf(t, res) != "bad_request: sort must be name, host, kind, status, or last_seen" {
			t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
		}
		if fake.count() != before {
			t.Fatal("api was called")
		}
	})
	t.Run("bad dir", func(t *testing.T) {
		before := fake.count()
		res := mustCall(t, cs, "list_services", map[string]any{"project_id": projectID, "dir": "up"})
		if !res.IsError || textOf(t, res) != "bad_request: dir must be asc or desc" {
			t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
		}
		if fake.count() != before {
			t.Fatal("api was called")
		}
	})
}

func TestListServicesAPIError(t *testing.T) {
	t.Run("forbidden", func(t *testing.T) {
		fake := &fakeAPI{token: userToken, mode: "forbidden"}
		mcpSrv, logs := newTestServer(t, fake)
		cs, _ := connectMCP(t, mcpSrv, userToken)
		res := mustCall(t, cs, "list_services", map[string]any{"project_id": projectID})
		if !res.IsError || textOf(t, res) != "forbidden: not a member" {
			t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
		}
		assertLast(t, fake, http.MethodGet, "/v1/projects/"+projectID+"/services", "", "")
		if logsContainSecret(logs.String()) {
			t.Fatal("secret material appeared in logs")
		}
	})
	t.Run("not found", func(t *testing.T) {
		fake := &fakeAPI{token: userToken}
		mcpSrv, logs := newTestServer(t, fake)
		cs, _ := connectMCP(t, mcpSrv, userToken)
		missing := "99999999-9999-4999-8999-999999999999"
		res := mustCall(t, cs, "list_services", map[string]any{"project_id": missing})
		want := "not_found: no such route /v1/projects/" + missing + "/services"
		if !res.IsError || textOf(t, res) != want {
			t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
		}
		assertLast(t, fake, http.MethodGet, "/v1/projects/"+missing+"/services", "", "")
		if logsContainSecret(logs.String()) {
			t.Fatal("secret material appeared in logs")
		}
	})
}

func TestMetricsShareProcessesForbidden(t *testing.T) {
	fake := &fakeAPI{token: userToken, mode: "metrics-share-processes"}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "list_processes", map[string]any{"host_id": hostID})
	if !res.IsError {
		t.Fatal("expected tool error")
	}
	if textOf(t, res) != "forbidden: metrics share cannot list processes" {
		t.Fatalf("text %q", textOf(t, res))
	}
	assertLast(t, fake, http.MethodGet, "/v1/hosts/"+hostID+"/processes", "", "")
	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestAPIForbidden(t *testing.T) {
	fake := &fakeAPI{token: userToken, mode: "forbidden"}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "list_projects", map[string]any{})
	if !res.IsError {
		t.Fatal("expected tool error")
	}
	if textOf(t, res) != "forbidden: not a member" {
		t.Fatalf("text %q", textOf(t, res))
	}
	if fake.v1Count() != 1 {
		t.Fatalf("v1 calls %d", fake.v1Count())
	}
	if len(fake.last().Unexpected) != 0 {
		t.Fatalf("unexpected headers %v", fake.last().Unexpected)
	}
	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestAPIUnauthorizedIsToolError(t *testing.T) {
	fake := &fakeAPI{token: userToken, mode: "unauthorized"}
	mcpSrv, logs := newTestServer(t, fake)
	cs, trips := connectMCP(t, mcpSrv, userToken)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_projects",
		Arguments: map[string]any{},
	})
	var toolStatus int
	for _, trip := range trips.snapshot() {
		if strings.Contains(trip.body, "tools/call") {
			toolStatus = trip.status
		}
	}
	t.Logf("tools/call http status %d call error %v", toolStatus, err)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.HasPrefix(textOf(t, res), "unauthorized:") {
		t.Fatalf("result error=%v text %q", res.IsError, textOf(t, res))
	}
	if textOf(t, res) != "unauthorized: session expired" {
		t.Fatalf("text %q", textOf(t, res))
	}
	if len(fake.last().Unexpected) != 0 {
		t.Fatalf("unexpected headers %v", fake.last().Unexpected)
	}
	if toolStatus != http.StatusOK {
		t.Fatalf("tools/call status %d", toolStatus)
	}
	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestInputValidationSkipsAPI(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	// initialize must not have called /v1. The exchange is not a v1 call.
	if fake.v1Count() != 0 {
		t.Fatalf("v1 calls before tools %d", fake.v1Count())
	}
	cases := []struct {
		name string
		tool string
		args any
	}{
		{name: "bad uuid", tool: "list_hosts", args: map[string]any{"project_id": "not-a-uuid"}},
		{name: "unknown field", tool: "list_hosts", args: map[string]any{"project_id": projectID, "extra": true}},
		{name: "bad metric", tool: "query_metrics", args: map[string]any{"host_id": hostID, "metric": "load"}},
		{name: "step low", tool: "query_metrics", args: map[string]any{"host_id": hostID, "metric": "cpu", "step": 9}},
		{name: "step high", tool: "query_metrics", args: map[string]any{"host_id": hostID, "metric": "cpu", "step": 3601}},
		{name: "from after to", tool: "query_metrics", args: map[string]any{
			"host_id": hostID, "metric": "cpu", "from": "2026-10-02T00:00:00Z", "to": "2026-10-01T00:00:00Z",
		}},
		{name: "span over 31 days", tool: "query_metrics", args: map[string]any{
			"host_id": hostID, "metric": "cpu", "from": "2026-01-01T00:00:00Z", "to": "2026-02-02T00:00:00Z",
		}},
		{name: "non utc", tool: "query_metrics", args: map[string]any{
			"host_id": hostID, "metric": "cpu", "from": "2026-10-13T00:00:00+01:00",
		}},
		{name: "probe both", tool: "probe_service", args: map[string]any{
			"service_id": serviceID, "check_id": checkID, "protocol": "http", "port": 80, "path": "/",
		}},
		{name: "http path missing", tool: "probe_service", args: map[string]any{
			"service_id": serviceID, "protocol": "http", "port": 80,
		}},
		{name: "tcp path", tool: "probe_service", args: map[string]any{
			"service_id": serviceID, "protocol": "tcp", "port": 22, "path": "/",
		}},
		{name: "process sort", tool: "list_processes", args: map[string]any{"host_id": hostID, "sort": "disk"}},
		{name: "process limit low", tool: "list_processes", args: map[string]any{"host_id": hostID, "limit": 0}},
		{name: "process limit high", tool: "list_processes", args: map[string]any{"host_id": hostID, "limit": 201}},
		{name: "probe port low", tool: "probe_service", args: map[string]any{"service_id": serviceID, "protocol": "tcp", "port": 0}},
		{name: "probe port high", tool: "probe_service", args: map[string]any{"service_id": serviceID, "protocol": "tcp", "port": 65536}},
		{name: "probe protocol", tool: "probe_service", args: map[string]any{"service_id": serviceID, "protocol": "udp", "port": 22}},
		{name: "health span", tool: "service_health_history", args: map[string]any{
			"service_id": serviceID, "from": "2026-10-02T00:00:00Z", "to": "2026-10-01T00:00:00Z",
		}},
		{name: "services neither", tool: "list_services", args: map[string]any{}},
		{name: "services both", tool: "list_services", args: map[string]any{"host_id": hostID, "project_id": projectID}},
		{name: "services bad host", tool: "list_services", args: map[string]any{"host_id": "not-a-uuid"}},
		{name: "services bad project", tool: "list_services", args: map[string]any{"project_id": "not-a-uuid"}},
		{name: "services empty project", tool: "list_services", args: map[string]any{"project_id": ""}},
		{name: "services host q", tool: "list_services", args: map[string]any{"host_id": hostID, "q": "api"}},
		{name: "services host sort", tool: "list_services", args: map[string]any{"host_id": hostID, "sort": "name"}},
		{name: "services host dir", tool: "list_services", args: map[string]any{"host_id": hostID, "dir": "asc"}},
		{name: "services host limit", tool: "list_services", args: map[string]any{"host_id": hostID, "limit": 10}},
		{name: "services host cursor", tool: "list_services", args: map[string]any{"host_id": hostID, "cursor": "x"}},
		{name: "services host view", tool: "list_services", args: map[string]any{"host_id": hostID, "view": "all"}},
		{name: "services sort", tool: "list_services", args: map[string]any{"project_id": projectID, "sort": "cpu"}},
		{name: "services view", tool: "list_services", args: map[string]any{"project_id": projectID, "view": "bogus"}},
		{name: "services dir", tool: "list_services", args: map[string]any{"project_id": projectID, "dir": "up"}},
		{name: "services limit low", tool: "list_services", args: map[string]any{"project_id": projectID, "limit": 0}},
		{name: "services limit high", tool: "list_services", args: map[string]any{"project_id": projectID, "limit": 201}},
		{name: "services q long", tool: "list_services", args: map[string]any{"project_id": projectID, "q": strings.Repeat("a", 101)}},
		{name: "services q long runes", tool: "list_services", args: map[string]any{"project_id": projectID, "q": strings.Repeat("é", 101)}},
		{name: "catalog bad project", tool: "list_catalog", args: map[string]any{"project_id": "not-a-uuid"}},
		{name: "catalog unknown", tool: "list_catalog", args: map[string]any{"project_id": projectID, "extra": true}},
		{name: "entry empty", tool: "run_entry", args: map[string]any{"project_id": projectID, "entry": "", "host_id": hostID, "idempotency_key": "k"}},
		{name: "entry upper", tool: "run_entry", args: map[string]any{"project_id": projectID, "entry": "Tra", "host_id": hostID, "idempotency_key": "k"}},
		{name: "entry hyphen", tool: "stop_entry", args: map[string]any{"project_id": projectID, "entry": "-a", "host_id": hostID, "idempotency_key": "k"}},
		{name: "entry long", tool: "restart_entry", args: map[string]any{"project_id": projectID, "entry": strings.Repeat("a", 49), "host_id": hostID, "idempotency_key": "k"}},
		{name: "entry underscore", tool: "entry_status", args: map[string]any{"project_id": projectID, "entry": "has_under", "host_id": hostID}},
		{name: "action bad host", tool: "run_entry", args: map[string]any{"project_id": projectID, "entry": entryName, "host_id": "nope", "idempotency_key": "k"}},
		{name: "key empty", tool: "run_entry", args: map[string]any{"project_id": projectID, "entry": entryName, "host_id": hostID, "idempotency_key": ""}},
		{name: "key space", tool: "run_entry", args: map[string]any{"project_id": projectID, "entry": entryName, "host_id": hostID, "idempotency_key": "a b"}},
		{name: "key slash", tool: "confirm", args: map[string]any{"confirmation_id": confirmationID, "idempotency_key": "a/b"}},
		{name: "key long", tool: "stop_entry", args: map[string]any{"project_id": projectID, "entry": entryName, "host_id": hostID, "idempotency_key": strings.Repeat("a", 129)}},
		{name: "confirm id short", tool: "confirm", args: map[string]any{"confirmation_id": "abc", "idempotency_key": "k"}},
		{name: "confirm id plus", tool: "confirm", args: map[string]any{"confirmation_id": strings.Repeat("a", 42) + "+", "idempotency_key": "k"}},
		{name: "confirm id pad", tool: "confirm", args: map[string]any{"confirmation_id": strings.Repeat("a", 42) + "=", "idempotency_key": "k"}},
		{name: "lines zero", tool: "entry_logs", args: map[string]any{"project_id": projectID, "entry": entryName, "host_id": hostID, "lines": 0}},
		{name: "lines high", tool: "entry_logs", args: map[string]any{"project_id": projectID, "entry": entryName, "host_id": hostID, "lines": 1001}},
		{name: "lines negative", tool: "entry_logs", args: map[string]any{"project_id": projectID, "entry": entryName, "host_id": hostID, "lines": -1}},
		{name: "logs bad entry", tool: "entry_logs", args: map[string]any{"project_id": projectID, "entry": "Bad", "host_id": hostID, "lines": 10}},
		{name: "confirm entry only", tool: "confirm", args: map[string]any{"confirmation_id": confirmationID, "idempotency_key": "k", "entry": imageEntry}},
		{name: "confirm project only", tool: "confirm", args: map[string]any{"confirmation_id": confirmationID, "idempotency_key": "k", "project_id": projectID}},
		{name: "confirm bad entry", tool: "confirm", args: map[string]any{"confirmation_id": confirmationID, "idempotency_key": "k", "project_id": projectID, "entry": "Bad"}},
		{name: "deploy missing key", tool: "deploy_entry", args: map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID}},
		{name: "deploy missing project", tool: "deploy_entry", args: map[string]any{"entry": imageEntry, "host_id": hostID, "idempotency_key": "k"}},
		{name: "deploy bad entry", tool: "deploy_entry", args: map[string]any{"project_id": projectID, "entry": "Bad", "host_id": hostID, "idempotency_key": "k"}},
		{name: "deploy unknown", tool: "deploy_entry", args: map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "k", "note": true}},
		{name: "deploy digest field", tool: "deploy_entry", args: map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "k", "digest": digestAB}},
		{name: "deploy force type", tool: "deploy_entry", args: map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "k", "force": "yes"}},
		{name: "rollback missing digest", tool: "rollback_entry", args: map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "k"}},
		{name: "rollback short digest", tool: "rollback_entry", args: map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "k", "digest": "sha256:abcd"}},
		{name: "rollback upper digest", tool: "rollback_entry", args: map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "k", "digest": "sha256:" + strings.Repeat("AB", 32)}},
		{name: "rollback no prefix", tool: "rollback_entry", args: map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "k", "digest": strings.Repeat("ab", 32)}},
		{name: "rollback extra", tool: "rollback_entry", args: map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "k", "digest": digestAB, "force": true}},
		{name: "rollback long digest", tool: "rollback_entry", args: map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "k", "digest": digestAB + "ab"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := fake.count()
			res := mustCall(t, cs, tc.tool, tc.args)
			if !res.IsError || !strings.HasPrefix(textOf(t, res), "bad_request:") {
				t.Fatalf("result error=%v text %q", res.IsError, textOf(t, res))
			}
			if fake.count() != before {
				t.Fatalf("api was called")
			}
		})
	}
}

func TestCatalogTools(t *testing.T) {
	if len(confirmationID) != 43 {
		t.Fatalf("confirmation id length %d", len(confirmationID))
	}
	fake := &fakeAPI{token: userToken}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	res := mustCall(t, cs, "list_catalog", map[string]any{"project_id": projectID})
	assertLast(t, fake, http.MethodGet, "/v1/projects/"+projectID+"/catalog", "", "")
	if textOf(t, res) != "1 catalog entries: tra-backend (program)" {
		t.Fatalf("list_catalog text %q", textOf(t, res))
	}
	items, ok := structuredMap(t, res)["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items %#v", structuredMap(t, res)["items"])
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("item %#v", items[0])
	}
	if item["name"] != entryName || item["kind"] != "program" {
		t.Fatalf("item %#v", item)
	}
	display, _ := item["command_display"].(string)
	if display != "pnpm run dev\uFFFDhidden" || strings.ContainsRune(display, '\u202e') {
		t.Fatalf("command_display %q", display)
	}
	allowed, ok := item["my_actions"].([]any)
	if !ok || len(allowed) != 5 || allowed[0] != "run" {
		t.Fatalf("my_actions %#v", item["my_actions"])
	}
	for _, key := range []string{"spec", "id", "project_id", "spec_hash", "env", "args"} {
		if _, exists := item[key]; exists {
			t.Fatalf("catalog item kept %s", key)
		}
	}
	projected, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"s3cret", "raw-token", "DATABASE_URL"} {
		if strings.Contains(string(projected), secret) {
			t.Fatalf("catalog structured content contains %s: %s", secret, projected)
		}
	}

	actions := []struct {
		tool string
		verb string
		key  string
	}{
		{tool: "run_entry", verb: "run", key: "run-1"},
		{tool: "stop_entry", verb: "stop", key: "stop.key:1"},
		{tool: "restart_entry", verb: "restart", key: "restart_1"},
	}
	for _, action := range actions {
		t.Run(action.tool, func(t *testing.T) {
			res := mustCall(t, cs, action.tool, map[string]any{
				"project_id": projectID, "entry": entryName, "host_id": hostID, "idempotency_key": action.key,
			})
			body := `{"host_id":"` + hostID + `","idempotency_key":"` + action.key + `"}`
			path := "/v1/projects/" + projectID + "/catalog/" + entryName + "/" + action.verb
			assertTool(t, fake, res, http.MethodPost, path, "", body)
			plan := "will " + action.verb + " program tra-backend (pnpm run dev, as joe) on pi1"
			want := "nothing ran yet. confirmation id " + confirmationID + ". Expires 2026-10-15T00:05:00Z.\n" + plan
			if textOf(t, res) != want {
				t.Fatalf("text %q", textOf(t, res))
			}
			if strings.Contains(textOf(t, res), "pid ") || strings.Contains(textOf(t, res), "active") {
				t.Fatal("202 treated as program status")
			}
			got := structuredMap(t, res)
			if got["confirmation_id"] != confirmationID || got["plan"] != plan {
				t.Fatalf("payload %#v", got)
			}
			if _, ok := got["active_state"]; ok {
				t.Fatal("202 payload is a program status")
			}
		})
	}

	res = mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "confirm-1",
	})
	// api-v1 v1.6.1 item 1: the confirm body is exactly this object.
	assertTool(t, fake, res, http.MethodPost, "/v1/confirmations/"+confirmationID, "", `{"idempotency_key":"confirm-1"}`)
	if textOf(t, res) != "deploybot-lab-tra-backend.service active (running) pid 4242" {
		t.Fatalf("confirm text %q", textOf(t, res))
	}

	res = mustCall(t, cs, "entry_logs", map[string]any{
		"project_id": projectID, "entry": entryName, "host_id": hostID,
	})
	assertTool(t, fake, res, http.MethodPost, "/v1/projects/"+projectID+"/catalog/"+entryName+"/logs", "", `{"host_id":"`+hostID+`"}`)
	if textOf(t, res) != "2 log lines for deploybot-lab-tra-backend.service" {
		t.Fatalf("logs text %q", textOf(t, res))
	}

	res = mustCall(t, cs, "entry_logs", map[string]any{
		"project_id": projectID, "entry": entryName, "host_id": hostID, "lines": 50,
	})
	assertTool(t, fake, res, http.MethodPost, "/v1/projects/"+projectID+"/catalog/"+entryName+"/logs", "", `{"host_id":"`+hostID+`","lines":50}`)

	res = mustCall(t, cs, "entry_status", map[string]any{
		"project_id": projectID, "entry": entryName, "host_id": hostID,
	})
	assertTool(t, fake, res, http.MethodPost, "/v1/projects/"+projectID+"/catalog/"+entryName+"/status", "", `{"host_id":"`+hostID+`"}`)
	if textOf(t, res) != "deploybot-lab-tra-backend.service active (running) pid 4242" {
		t.Fatalf("status text %q", textOf(t, res))
	}

	entry48 := strings.Repeat("b", 48)
	key128 := strings.Repeat("a", 128)
	res = mustCall(t, cs, "run_entry", map[string]any{
		"project_id": projectID, "entry": entry48, "host_id": hostID, "idempotency_key": key128,
	})
	assertLast(t, fake, http.MethodPost, "/v1/projects/"+projectID+"/catalog/"+entry48+"/run", "", `{"host_id":"`+hostID+`","idempotency_key":"`+key128+`"}`)
	if res.IsError || !strings.Contains(textOf(t, res), "nothing ran yet") {
		t.Fatalf("boundary result error=%v text %q", res.IsError, textOf(t, res))
	}

	res = mustCall(t, cs, "entry_logs", map[string]any{
		"project_id": projectID, "entry": "long-log", "host_id": hostID, "lines": 1000,
	})
	assertLast(t, fake, http.MethodPost, "/v1/projects/"+projectID+"/catalog/long-log/logs", "", `{"host_id":"`+hostID+`","lines":1000}`)
	if res.IsError {
		t.Fatalf("long logs %s", textOf(t, res))
	}
	text := textOf(t, res)
	if !strings.Contains(text, "250 log lines") || !strings.Contains(text, "truncated to 200 items.") {
		t.Fatalf("long logs text %q", text)
	}
	lines, ok := structuredMap(t, res)["lines"].([]any)
	if !ok || len(lines) != 200 || lines[0] != "line-000" {
		t.Fatalf("lines %#v", structuredMap(t, res)["lines"])
	}

	for _, call := range fake.snapshot() {
		if strings.HasPrefix(call.Path, "/v1/") {
			assertDelegatedV1(t, call)
		}
	}
	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestCatalogAPIError(t *testing.T) {
	fake := &fakeAPI{token: userToken, mode: "forbidden"}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "run_entry", map[string]any{
		"project_id": projectID, "entry": entryName, "host_id": hostID, "idempotency_key": "run-1",
	})
	if !res.IsError || textOf(t, res) != "forbidden: not a member" {
		t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
	}
	assertLast(t, fake, http.MethodPost, "/v1/projects/"+projectID+"/catalog/"+entryName+"/run", "", `{"host_id":"`+hostID+`","idempotency_key":"run-1"}`)
	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestEntryActionRejectsHTTP200(t *testing.T) {
	fake := &fakeAPI{token: userToken, mode: "acted"}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "run_entry", map[string]any{
		"project_id": projectID, "entry": entryName, "host_id": hostID, "idempotency_key": "run-1",
	})
	if !res.IsError || textOf(t, res) != "error: expected a confirmation" {
		t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
	}
	text := textOf(t, res)
	if strings.Contains(text, "pid ") || strings.Contains(text, "active (running)") || strings.Contains(text, "nothing ran yet") {
		t.Fatalf("200 summarized %q", text)
	}
	assertLast(t, fake, http.MethodPost, "/v1/projects/"+projectID+"/catalog/"+entryName+"/run", "", `{"host_id":"`+hostID+`","idempotency_key":"run-1"}`)
}

func TestConfigFrom(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "client-secret")
	const secretLine = "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(secretPath, []byte(secretLine+"\nnope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secretPath, 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"BOT_MCP_LISTEN":             "127.0.0.1:8091",
		"BOT_MCP_API_URL":            "https://api.example",
		"BOT_MCP_PUBLIC_ORIGIN":      "https://mcp.example",
		"BOT_MCP_CLIENT_SECRET_FILE": secretPath,
	}
	cfg, err := configFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIURL != "https://api.example" || cfg.Listen != "127.0.0.1:8091" || cfg.ClientSecret != secretLine {
		t.Fatalf("listen %s api %s secret-ok %v", cfg.Listen, cfg.APIURL, cfg.ClientSecret == secretLine)
	}
	env["BOT_MCP_API_URL"] = "https://api.example/base"
	if _, err := configFrom(func(k string) string { return env[k] }); err == nil {
		t.Fatal("api path was accepted")
	}
	env["BOT_MCP_API_URL"] = "https://api.example/"
	if _, err := configFrom(func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	env["BOT_MCP_API_URL"] = "https://api.example?q=1"
	if _, err := configFrom(func(k string) string { return env[k] }); err == nil {
		t.Fatal("api query was accepted")
	}
	env["BOT_MCP_API_URL"] = "https://api.example#frag"
	if _, err := configFrom(func(k string) string { return env[k] }); err == nil {
		t.Fatal("api fragment was accepted")
	}
	env["BOT_MCP_API_URL"] = "https://user:pass@api.example"
	if _, err := configFrom(func(k string) string { return env[k] }); err == nil {
		t.Fatal("userinfo was accepted")
	}
	env["BOT_MCP_API_URL"] = "https://api.example"
	env["BOT_MCP_PUBLIC_ORIGIN"] = "https://mcp.example/mcp"
	if _, err := configFrom(func(k string) string { return env[k] }); err == nil {
		t.Fatal("origin path was accepted")
	}
	env["BOT_MCP_PUBLIC_ORIGIN"] = "https://mcp.example"
	env["BOT_MCP_API_URL"] = "http://example.com"
	if _, err := configFrom(func(k string) string { return env[k] }); err == nil {
		t.Fatal("cleartext api was accepted")
	}
	env["BOT_MCP_API_URL"] = "https://api.example"
	env["BOT_MCP_PUBLIC_ORIGIN"] = "http://mcp.example"
	if _, err := configFrom(func(k string) string { return env[k] }); err == nil {
		t.Fatal("cleartext origin was accepted")
	}
	env["BOT_MCP_API_URL"] = "http://127.0.0.1:8080"
	env["BOT_MCP_PUBLIC_ORIGIN"] = "http://localhost:8091"
	if _, err := configFrom(func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	env["BOT_MCP_PUBLIC_ORIGIN"] = "http://[::1]:8091"
	if _, err := configFrom(func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	env["BOT_MCP_API_URL"] = "http://example.com"
	env["BOT_MCP_PUBLIC_ORIGIN"] = "http://mcp.example"
	env["BOT_MCP_ALLOW_INSECURE_HTTP"] = "1"
	if _, err := configFrom(func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
}

func TestClientSecretFile(t *testing.T) {
	dir := t.TempDir()
	base := func(path string) map[string]string {
		return map[string]string{
			"BOT_MCP_LISTEN":             "127.0.0.1:8091",
			"BOT_MCP_API_URL":            "https://api.example",
			"BOT_MCP_PUBLIC_ORIGIN":      "https://mcp.example",
			"BOT_MCP_CLIENT_SECRET_FILE": path,
		}
	}
	load := func(path string) error {
		env := base(path)
		_, err := configFrom(func(k string) string { return env[k] })
		return err
	}
	t.Run("missing", func(t *testing.T) {
		err := load(filepath.Join(dir, "absent"))
		if err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("unset", func(t *testing.T) {
		err := load("")
		if err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		path := filepath.Join(dir, "empty")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		err := load(path)
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("newline only", func(t *testing.T) {
		path := filepath.Join(dir, "newline")
		if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := load(path)
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("mode 0644", func(t *testing.T) {
		path := filepath.Join(dir, "loose")
		const contents = "mode-secret-value"
		if err := os.WriteFile(path, []byte(contents+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		err := load(path)
		if err == nil || !strings.Contains(err.Error(), "0600") {
			t.Fatalf("err %v", err)
		}
		if strings.Contains(err.Error(), contents) {
			t.Fatal("secret appeared in the error")
		}
	})
	t.Run("short", func(t *testing.T) {
		path := filepath.Join(dir, "short")
		const contents = "0123456789012345678901234567890"
		if len(contents) != 31 {
			t.Fatalf("fixture length %d", len(contents))
		}
		if err := os.WriteFile(path, []byte(contents+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := load(path)
		if err == nil || !strings.Contains(err.Error(), "32") {
			t.Fatalf("err %v", err)
		}
		if strings.Contains(err.Error(), contents) {
			t.Fatal("secret appeared in the error")
		}
	})
	t.Run("long enough", func(t *testing.T) {
		path := filepath.Join(dir, "long")
		const contents = "01234567890123456789012345678901"
		if len(contents) != 32 {
			t.Fatalf("fixture length %d", len(contents))
		}
		if err := os.WriteFile(path, []byte(contents+"\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		env := base(path)
		cfg, err := configFrom(func(k string) string { return env[k] })
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ClientSecret != contents {
			t.Fatal("secret-ok", cfg.ClientSecret == contents)
		}
	})
}

func TestNoPassthrough(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	mustCall(t, cs, "list_projects", map[string]any{})
	var exchanges, v1s int
	for _, call := range fake.snapshot() {
		switch {
		case call.Path == "/oauth/token":
			exchanges++
			assertExchange(t, call, fake.base)
		case strings.HasPrefix(call.Path, "/v1/"):
			v1s++
			assertDelegatedV1(t, call)
		default:
			t.Fatalf("unexpected path %s", call.Path)
		}
	}
	if exchanges == 0 || v1s == 0 {
		t.Fatalf("exchanges %d v1 %d", exchanges, v1s)
	}
	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestExchangeCacheHit(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	cs, trips := connectMCP(t, mcpSrv, userToken)
	mustCall(t, cs, "list_projects", map[string]any{})
	mustCall(t, cs, "list_projects", map[string]any{})
	if tripsN := len(trips.snapshot()); tripsN < 2 {
		t.Fatalf("mcp requests %d", tripsN)
	}
	if got := fake.exchangeCount(); got != 1 {
		t.Fatalf("exchanges %d", got)
	}
}

func TestShortDelegatedTokenIsNotCached(t *testing.T) {
	fake := &fakeAPI{token: userToken, expiresIn: 30}
	mcpSrv, _ := newTestServer(t, fake)
	cs, trips := connectMCP(t, mcpSrv, userToken)
	mustCall(t, cs, "list_projects", map[string]any{})
	if got, want := fake.exchangeCount(), len(trips.snapshot()); got != want {
		t.Fatalf("exchanges %d mcp requests %d", got, want)
	}
	if fake.exchangeCount() < 2 {
		t.Fatalf("expected a re-exchange, exchanges %d", fake.exchangeCount())
	}
}

func TestExchangeInvalidGrant(t *testing.T) {
	want := `Bearer error="invalid_token", resource_metadata="https://mcp.example/.well-known/oauth-protected-resource"`
	for _, mode := range []string{"invalid_grant", "invalid_request"} {
		t.Run(mode, func(t *testing.T) {
			fake := &fakeAPI{mode: mode}
			mcpSrv, logs := newTestServer(t, fake)
			resp, body := postMCP(t, mcpSrv.URL, userToken)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status %d body %s", resp.StatusCode, body)
			}
			if got := resp.Header.Get("WWW-Authenticate"); got != want {
				t.Fatalf("WWW-Authenticate = %q", got)
			}
			if strings.Contains(body, "jsonrpc") {
				t.Fatal("MCP handled the request")
			}
			if fake.v1Count() != 0 {
				t.Fatal("v1 was called")
			}
			if logsContainSecret(logs.String()) || strings.Contains(body, userToken) || strings.Contains(body, clientSecret) {
				t.Fatal("secret material appeared in the response or logs")
			}
		})
	}
}

func TestExchangeUnavailable(t *testing.T) {
	t.Run("unreachable", func(t *testing.T) {
		dead := httptest.NewServer(http.NotFoundHandler())
		u := dead.URL
		dead.Close()
		api, err := apiclient.New(u, false)
		if err != nil {
			t.Fatal(err)
		}
		logs := &safeBuf{}
		logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		handler, err := newHandler(config{
			Listen:       "127.0.0.1:0",
			APIURL:       u,
			PublicOrigin: "https://mcp.example",
			ClientSecret: clientSecret,
		}, api, logger)
		if err != nil {
			t.Fatal(err)
		}
		mcpSrv := httptest.NewServer(handler)
		t.Cleanup(mcpSrv.Close)
		resp, body := postMCP(t, mcpSrv.URL, userToken)
		defer resp.Body.Close()
		assertUnavailable(t, resp, body, logs.String())
	})
	t.Run("as 500", func(t *testing.T) {
		fake := &fakeAPI{mode: "as-500"}
		mcpSrv, logs := newTestServer(t, fake)
		resp, body := postMCP(t, mcpSrv.URL, userToken)
		defer resp.Body.Close()
		assertUnavailable(t, resp, body, logs.String())
		if fake.v1Count() != 0 {
			t.Fatal("v1 was called")
		}
	})
}

func TestDelegated401EvictsCache(t *testing.T) {
	fake := &fakeAPI{token: userToken, mode: "unauthorized"}
	mcpSrv, logs := newTestServer(t, fake)
	cs, trips := connectMCP(t, mcpSrv, userToken)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_projects",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || textOf(t, res) != "unauthorized: session expired" {
		t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
	}
	if fake.exchangeCount() != 1 {
		t.Fatalf("exchanges before retry %d", fake.exchangeCount())
	}
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_projects",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || textOf(t, res) != "unauthorized: session expired" {
		t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
	}
	if fake.exchangeCount() != 2 {
		t.Fatalf("exchanges %d", fake.exchangeCount())
	}
	var toolOK int
	for _, trip := range trips.snapshot() {
		if strings.Contains(trip.body, "tools/call") && trip.status == http.StatusOK {
			toolOK++
		}
	}
	if toolOK != 2 {
		t.Fatalf("tools/call http 200 count %d", toolOK)
	}
	for _, call := range fake.snapshot() {
		if strings.HasPrefix(call.Path, "/v1/") {
			assertDelegatedV1(t, call)
		}
	}
	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func postMCP(t *testing.T, base, token string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func assertUnavailable(t *testing.T, resp *http.Response, body, logs string) {
	t.Helper()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != "" {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
	if strings.Contains(body, "jsonrpc") {
		t.Fatal("MCP handled the request")
	}
	if logsContainSecret(logs) || strings.Contains(body, userToken) || strings.Contains(body, clientSecret) {
		t.Fatal("secret material appeared in the response or logs")
	}
}

func logsContainSecret(s string) bool {
	return strings.Contains(s, userToken) || strings.Contains(s, delegatedToken) || strings.Contains(s, clientSecret)
}

type apiCall struct {
	Method       string
	Path         string
	RawQuery     string
	Auth         []string
	Body         string
	ContentType  string
	Payload      json.RawMessage
	Unexpected   []string
	tokenInOther bool
	callerLeaked bool
}

type fakeAPI struct {
	mu           sync.Mutex
	calls        []apiCall
	token        string
	mode         string
	base         string
	expiresIn    int
	client       *apiclient.Client
	images       bool
	imageConfirm bool
	confirms     int
	plan         string
	confirmBody  any
	serviceBody  any
	healthBody   any
}

func (f *fakeAPI) setMode(mode string) {
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
}

func (f *fakeAPI) setPlan(plan string) {
	f.mu.Lock()
	f.plan = plan
	f.mu.Unlock()
}

func (f *fakeAPI) setConfirmBody(body any) {
	f.mu.Lock()
	f.confirmBody = body
	f.mu.Unlock()
}

func (f *fakeAPI) setServiceBody(body any) {
	f.mu.Lock()
	f.serviceBody = body
	f.mu.Unlock()
}

func (f *fakeAPI) setHealthBody(body any) {
	f.mu.Lock()
	f.healthBody = body
	f.mu.Unlock()
}

func (f *fakeAPI) noteConfirm() {
	f.mu.Lock()
	f.confirms++
	f.mu.Unlock()
}

func (f *fakeAPI) confirmCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.confirms
}

func (f *fakeAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeAPI) last() apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func (f *fakeAPI) snapshot() []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apiCall(nil), f.calls...)
}

func (f *fakeAPI) exchangeCount() int {
	n := 0
	for _, call := range f.snapshot() {
		if call.Path == "/oauth/token" {
			n++
		}
	}
	return n
}

func (f *fakeAPI) v1Count() int {
	n := 0
	for _, call := range f.snapshot() {
		if strings.HasPrefix(call.Path, "/v1/") {
			n++
		}
	}
	return n
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/oauth/token" {
		f.serveExchange(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	call := apiCall{
		Method:      r.Method,
		Path:        r.URL.Path,
		RawQuery:    r.URL.RawQuery,
		Auth:        append([]string(nil), r.Header.Values("Authorization")...),
		Body:        string(body),
		ContentType: r.Header.Get("Content-Type"),
		Unexpected:  unexpectedHeaders(r.Header, len(body) > 0),
	}
	if f.token != "" {
		for key, values := range r.Header {
			if strings.EqualFold(key, "Authorization") {
				continue
			}
			for _, value := range values {
				if strings.Contains(value, f.token) {
					call.tokenInOther = true
				}
			}
		}
	}
	if strings.HasPrefix(r.URL.Path, "/v1/") && f.token != "" {
		if strings.Contains(call.Body, f.token) || strings.Contains(call.RawQuery, f.token) || strings.Contains(call.Path, f.token) || strings.Contains(call.ContentType, f.token) {
			call.callerLeaked = true
		}
		for _, value := range call.Auth {
			if strings.Contains(value, f.token) {
				call.callerLeaked = true
			}
		}
	}
	status, code, msg, payload := f.reply(r)
	raw, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "encode", http.StatusInternalServerError)
		return
	}
	call.Payload = raw
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	writeEnvelope(w, status, code, msg, raw)
}

func (f *fakeAPI) serveExchange(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	call := apiCall{
		Method:      r.Method,
		Path:        r.URL.Path,
		RawQuery:    r.URL.RawQuery,
		Auth:        append([]string(nil), r.Header.Values("Authorization")...),
		Body:        string(body),
		ContentType: r.Header.Get("Content-Type"),
	}
	f.mu.Lock()
	mode := f.mode
	expires := f.expiresIn
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	if expires <= 0 {
		expires = 300
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	switch mode {
	case "invalid_grant", "invalid_request":
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":%q,"error_description":"rejected"}`, mode)
		return
	case "as-500":
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server_error"}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"access_token":%q,"issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":%d}`, delegatedToken, expires)
}

func (f *fakeAPI) reply(r *http.Request) (int, string, string, any) {
	f.mu.Lock()
	mode := f.mode
	images := f.images
	imageConfirm := f.imageConfirm
	plan := f.plan
	confirmBody := f.confirmBody
	serviceBody := f.serviceBody
	healthBody := f.healthBody
	f.mu.Unlock()
	switch mode {
	case "forbidden":
		return http.StatusForbidden, "forbidden", "not a member", nil
	case "metrics-share-processes":
		if r.Method == http.MethodGet && r.URL.Path == "/v1/hosts/"+hostID+"/processes" {
			return http.StatusForbidden, "forbidden", "metrics share cannot list processes", nil
		}
		return http.StatusNotFound, "not_found", "no such route " + r.URL.Path, nil
	case "unauthorized":
		return http.StatusUnauthorized, "unauthorized", "session expired", nil
	case "health-error":
		return http.StatusBadRequest, "bad_request", "token=s3cret", nil
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/me":
		return http.StatusOK, "ok", "", map[string]any{
			"user":       map[string]any{"id": "55555555-5555-4555-8555-555555555555", "email": "ada@example", "name": "Ada", "avatar_url": nil},
			"identities": []any{map[string]any{"provider": "google", "email": "ada@example"}},
			"projects": []any{map[string]any{
				"id": projectID, "name": "lab", "slug": "lab", "role": "owner", "created_at": "2026-10-01T00:00:00Z",
			}},
		}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/"+manyProjectID+"/hosts":
		items := make([]any, 201)
		projects := make([]any, 201)
		for i := range items {
			items[i] = map[string]any{"id": fmt.Sprintf("item-%03d", i), "name": fmt.Sprintf("h%d", i), "status": "online"}
			projects[i] = map[string]any{"id": fmt.Sprintf("proj-%03d", i), "name": fmt.Sprintf("p%d", i)}
		}
		return http.StatusOK, "ok", "", map[string]any{"items": items, "projects": projects, "next_cursor": nil}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/"+projectID+"/hosts":
		return http.StatusOK, "ok", "", map[string]any{
			"items": []any{
				map[string]any{
					"id": hostID, "name": "pi1", "status": "online",
					"relation": "owner", "share_level": nil,
				},
				map[string]any{
					"id": sharedHostID, "name": "pi2", "status": "online",
					"relation": "shared", "share_level": "metrics",
				},
			},
			"next_cursor": nil,
		}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/hosts/"+hostID+"/services":
		if mode == "docker-list" {
			return http.StatusOK, "ok", "", map[string]any{
				"items": []any{
					map[string]any{
						"id": serviceID, "name": "redis", "gone_at": nil,
						"health": map[string]any{"status": "down", "source": "docker", "latency_ms": nil},
					},
					map[string]any{
						"id": serviceID, "name": "api", "gone_at": nil,
						"health": map[string]any{"status": "up", "source": "check", "latency_ms": 4},
					},
					map[string]any{
						"id": serviceID, "name": "old", "gone_at": nil,
						"health": map[string]any{"status": "unknown", "source": nil, "latency_ms": nil},
					},
				},
				"next_cursor": nil,
			}
		}
		return http.StatusOK, "ok", "", map[string]any{
			"items": []any{map[string]any{
				"id": serviceID, "name": "api", "health": map[string]any{"status": "up"},
				"gone_at": nil,
			}},
			"next_cursor": nil,
		}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/"+projectID+"/services":
		return http.StatusOK, "ok", "", map[string]any{
			"items": []any{
				map[string]any{
					"id": serviceID, "name": "port-8080", "display_name": nil, "owner_user": "ada",
					"health":       map[string]any{"status": "up"},
					"host_name":    "pi1",
					"host_status":  "online",
					"project_name": "lab",
					"gone_at":      nil,
				},
				map[string]any{
					"id": "77777777-7777-4777-8777-777777777777", "name": "redis", "display_name": "Cache", "owner_user": nil,
					"health":       map[string]any{"status": "down"},
					"host_name":    "pi2",
					"host_status":  "online",
					"project_name": "lab",
					"gone_at":      "2026-10-15T14:13:00Z",
				},
			},
			"next_cursor": "next/1",
			"prev_cursor": nil,
			"total":       5,
		}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/hosts/"+hostID+"/metrics":
		return http.StatusOK, "ok", "", map[string]any{
			"m": "cpu", "from": "2026-10-13T00:00:00Z", "to": "2026-10-13T01:00:00Z", "step": 60,
			"series": []any{map[string]any{"name": "usage_percent", "unit": "%", "points": []any{[]any{"2026-10-13T00:00:00Z", 1.5}}}},
		}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/hosts/"+hostID+"/processes":
		return http.StatusOK, "ok", "", map[string]any{
			"sampled_at": "2026-10-13T00:00:00Z",
			"items":      []any{map[string]any{"pid": 1, "process_name": "bot"}},
		}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/services/"+serviceID+"/health":
		if healthBody != nil {
			return http.StatusOK, "ok", "", healthBody
		}
		return http.StatusOK, "ok", "", map[string]any{
			"items": []any{map[string]any{"t": "2026-10-13T00:00:00Z", "check_id": checkID, "result": "up", "http_status": 200, "latency_ms": 4}},
		}
	case r.Method == http.MethodPost && r.URL.Path == "/v1/hosts/"+hostID+"/snapshot":
		return http.StatusOK, "ok", "", map[string]any{
			"sampled_at": "2026-10-13T00:00:00Z", "cpu_percent": 12.5,
			"mem":   map[string]any{"total": 100, "used": 40, "available": 60, "used_percent": 40},
			"disks": []any{}, "load": []any{0.1, 0.2, 0.3}, "temp_celsius": nil, "uptime_sec": 10, "top": []any{},
		}
	case r.Method == http.MethodPost && r.URL.Path == "/v1/services/"+serviceID+"/probe":
		return http.StatusOK, "ok", "", map[string]any{
			"service": "api", "protocol": "http", "port": 8080, "path": "/health",
			"result": "up", "http_status": 200, "latency_ms": 7, "checked_at": "2026-10-13T00:00:00Z",
		}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/"+projectID+"/catalog":
		if mode == "catalog-down" {
			return http.StatusInternalServerError, "error", "catalog down", nil
		}
		if mode == "catalog-shape" {
			return http.StatusOK, "ok", "", []any{map[string]any{
				"name": imageEntry, "kind": "image",
				"spec": map[string]any{"env": map[string]any{"NODE_ENV": envLeak}},
			}}
		}
		if mode == "catalog-healthcheck" {
			return http.StatusOK, "ok", "", map[string]any{"items": []any{
				map[string]any{
					"name": "tra-backend", "kind": "program",
					"command_display": "pnpm start", "my_actions": []any{"run"},
				},
				map[string]any{
					"name": "redis", "kind": "image", "command_display": "redis:7",
					"spec": map[string]any{
						"kind": "service", "image": "redis:7",
						"healthcheck": map[string]any{
							"test": []any{"redis-cli", "argv-sentinel-do-not-echo"}, "interval_sec": 30,
						},
					},
					"my_actions": []any{"deploy"},
				},
				map[string]any{
					"name": "nginx", "kind": "image", "command_display": "nginx:1",
					"spec":       map[string]any{"kind": "service", "image": "nginx:1", "healthcheck": nil},
					"my_actions": []any{"deploy"},
				},
			}}
		}
		if mode == "catalog-logging" {
			return http.StatusOK, "ok", "", map[string]any{"items": []any{
				map[string]any{
					"name": "tra-backend", "kind": "program",
					"command_display": "pnpm start", "my_actions": []any{"run"},
					"spec": map[string]any{
						"logging": map[string]any{
							"max_size_mb": 9, "max_files": 9, "note": "program-log-do-not-keep",
						},
					},
				},
				map[string]any{
					"name": "redis", "kind": "image", "command_display": "redis:7",
					"spec": map[string]any{
						"kind": "service", "image": "redis:7",
						"logging": map[string]any{
							"max_size_mb": 5, "max_files": 2, "driver": "image-log-extra-do-not-keep",
						},
					},
					"my_actions": []any{"deploy"},
				},
				map[string]any{
					"name": "nginx", "kind": "image", "command_display": "nginx:1",
					"spec":       map[string]any{"kind": "service", "image": "nginx:1"},
					"my_actions": []any{"deploy"},
				},
			}}
		}
		return http.StatusOK, "ok", "", map[string]any{"items": catalogItems(images)}
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/projects/"+projectID+"/catalog/") && strings.HasSuffix(r.URL.Path, "/deploys"):
		if mode == "history-down" {
			return http.StatusInternalServerError, "error", "history down", nil
		}
		return http.StatusOK, "ok", "", deployHistory()
	case r.Method == http.MethodPost && catalogVerb(r.URL.Path, "deploy"):
		if mode == "acted" {
			return http.StatusOK, "ok", "", imageDeployResult()
		}
		entry := catalogEntryName(r.URL.Path)
		return http.StatusAccepted, "ok", "", confirmationPayloadText(imagePlan(entry, imageRefFor(entry)))
	case r.Method == http.MethodPost && catalogVerb(r.URL.Path, "rollback"):
		if mode == "acted" {
			return http.StatusOK, "ok", "", imageDeployResult()
		}
		entry := catalogEntryName(r.URL.Path)
		return http.StatusAccepted, "ok", "", confirmationPayloadText(imagePlan(entry, digestAB))
	case r.Method == http.MethodPost && catalogVerb(r.URL.Path, "run"):
		if mode == "acted" {
			return http.StatusOK, "ok", "", programStatusPayload()
		}
		return http.StatusAccepted, "ok", "", confirmationPayload("run")
	case r.Method == http.MethodPost && catalogVerb(r.URL.Path, "stop"):
		if mode == "acted" {
			return http.StatusOK, "ok", "", programStatusPayload()
		}
		return http.StatusAccepted, "ok", "", confirmationPayload("stop")
	case r.Method == http.MethodPost && catalogVerb(r.URL.Path, "restart"):
		if mode == "acted" {
			return http.StatusOK, "ok", "", programStatusPayload()
		}
		return http.StatusAccepted, "ok", "", confirmationPayload("restart")
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/"+imageEntry+"/logs"):
		if mode == "log-preview" {
			return http.StatusOK, "ok", "", map[string]any{
				"container": "lab-webadmin",
				"lines":     []any{"abcd…", "••••", "ready abcd… ••••", "password=hunter2"},
				"truncated": false,
			}
		}
		return http.StatusOK, "ok", "", map[string]any{
			"container": "lab-webadmin",
			"lines":     []any{"listening", "token=" + logLeak},
			"truncated": false,
		}
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/"+imageEntry+"/status"):
		if mode == "status-unknown" {
			return http.StatusOK, "ok", "", timeoutImageStatus()
		}
		if mode == "status-bad" {
			return http.StatusOK, "ok", "", map[string]any{
				"container": "lab-webadmin", "state": badImageState, "note": badImageState,
			}
		}
		return http.StatusOK, "ok", "", map[string]any{
			"container": "lab-webadmin", "state": "running", "exit_code": 0, "restart_count": 1,
			"started_at": "2026-10-15T00:00:00Z", "finished_at": nil, "digest": digestAB, "ports": []any{3031},
		}
	case r.Method == http.MethodPost && catalogVerb(r.URL.Path, "logs"):
		if strings.HasSuffix(r.URL.Path, "/long-log/logs") {
			lines := make([]any, 250)
			for i := range lines {
				lines[i] = fmt.Sprintf("line-%03d", i)
			}
			return http.StatusOK, "ok", "", map[string]any{
				"unit": "deploybot-lab-long-log.service", "lines": lines, "truncated": false,
			}
		}
		return http.StatusOK, "ok", "", map[string]any{
			"unit": "deploybot-lab-tra-backend.service", "lines": []any{"listening", "ready"}, "truncated": false,
		}
	case r.Method == http.MethodPost && catalogVerb(r.URL.Path, "status"):
		return http.StatusOK, "ok", "", programStatusPayload()
	case r.Method == http.MethodGet && r.URL.Path == "/v1/services/"+serviceID:
		if mode == "missing-service" {
			return http.StatusNotFound, "not_found", "no such service", nil
		}
		if serviceBody != nil {
			return http.StatusOK, "ok", "", serviceBody
		}
		return http.StatusOK, "ok", "", healthChecksServicePayload()
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/services/") && strings.HasSuffix(r.URL.Path, "/health-checks"):
		if mode == "acted" {
			return http.StatusOK, "ok", "", map[string]any{"checks": []any{}}
		}
		shown := healthChecksPlan
		if plan != "" {
			shown = plan
		}
		return http.StatusAccepted, "ok", "", map[string]any{
			"confirmation_id": confirmationID,
			"plan":            shown,
			"expires_at":      "2026-10-15T00:05:00Z",
			"note":            "hunter2plain",
		}
	case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/"+projectID+"/catalog":
		if mode == "acted" {
			return http.StatusCreated, "ok", "", catalogWriteEntry()
		}
		return http.StatusAccepted, "ok", "", confirmationPayloadText(catalogPlanOr("create", plan))
	case r.Method == http.MethodPut && r.URL.Path == "/v1/projects/"+projectID+"/catalog/"+imageEntry:
		if mode == "acted" {
			return http.StatusOK, "ok", "", catalogWriteEntry()
		}
		return http.StatusAccepted, "ok", "", confirmationPayloadText(catalogPlanOr("update", plan))
	case r.Method == http.MethodDelete && r.URL.Path == "/v1/projects/"+projectID+"/catalog/"+imageEntry:
		if mode == "acted" {
			return http.StatusOK, "ok", "", nil
		}
		return http.StatusAccepted, "ok", "", confirmationPayloadText(catalogPlanOr("delete", plan))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/confirmations/"+confirmationID:
		f.noteConfirm()
		switch mode {
		case "catalog-created":
			return http.StatusCreated, "ok", "", catalogWriteEntry()
		case "catalog-updated":
			return http.StatusOK, "ok", "", catalogWriteEntry()
		case "catalog-deleted":
			return http.StatusOK, "ok", "", nil
		case "catalog-bad":
			return http.StatusCreated, "ok", "", map[string]any{"ok": true, "secret": envLeak}
		}
		if mode == "health-checks" {
			if confirmBody != nil {
				return http.StatusOK, "ok", "", confirmBody
			}
			return http.StatusOK, "ok", "", healthChecksConfirmPayload()
		}
		if imageConfirm {
			switch mode {
			case "confirm-timeout":
				return http.StatusOK, "ok", "", timeoutDeployResult()
			case "confirm-bad-state":
				return http.StatusOK, "ok", "", badStateDeployResult()
			default:
				return http.StatusOK, "ok", "", imageDeployResult()
			}
		}
		return http.StatusOK, "ok", "", programStatusPayload()
	default:
		return http.StatusNotFound, "not_found", "no such route " + r.URL.Path, nil
	}
}

func catalogVerb(path, verb string) bool {
	prefix := "/v1/projects/" + projectID + "/catalog/"
	suffix := "/" + verb
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	return name != "" && !strings.Contains(name, "/")
}

func confirmationPayload(verb string) map[string]any {
	return map[string]any{
		"confirmation_id": confirmationID,
		"plan":            "will " + verb + " program tra-backend (pnpm run dev, as joe) on pi1",
		"expires_at":      "2026-10-15T00:05:00Z",
	}
}

func programStatusPayload() map[string]any {
	return map[string]any{
		"unit":         "deploybot-lab-tra-backend.service",
		"active_state": "active",
		"sub_state":    "running",
		"main_pid":     4242,
		"started_at":   "2026-10-15T00:00:00Z",
		"ports":        []any{3031},
	}
}

func writeEnvelope(w http.ResponseWriter, status int, code, msg string, payload json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code    string          `json:"code"`
		Msg     string          `json:"msg"`
		Payload json.RawMessage `json:"payload"`
	}{Code: code, Msg: msg, Payload: payload})
}

func unexpectedHeaders(h http.Header, hasBody bool) []string {
	var bad []string
	for key := range h {
		switch http.CanonicalHeaderKey(key) {
		case "Authorization", "Accept", "User-Agent", "Accept-Encoding", "Content-Length":
		case "Content-Type":
			if !hasBody {
				bad = append(bad, "Content-Type")
			}
		default:
			bad = append(bad, key)
		}
	}
	return bad
}

type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newTestServer(t *testing.T, fake *fakeAPI) (*httptest.Server, *safeBuf) {
	t.Helper()
	return newTestServerWithOrigin(t, fake, "https://mcp.example/")
}

func newTestServerWithOrigin(t *testing.T, fake *fakeAPI, publicOrigin string) (*httptest.Server, *safeBuf) {
	t.Helper()
	apiSrv := httptest.NewServer(fake)
	t.Cleanup(apiSrv.Close)
	fake.base = apiSrv.URL
	api, err := apiclient.New(apiSrv.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	fake.client = api
	logs := &safeBuf{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	handler, err := newHandler(config{
		Listen:       "127.0.0.1:0",
		APIURL:       apiSrv.URL,
		PublicOrigin: publicOrigin,
		ClientSecret: clientSecret,
	}, api, logger)
	if err != nil {
		t.Fatal(err)
	}
	mcpSrv := httptest.NewServer(handler)
	t.Cleanup(mcpSrv.Close)
	return mcpSrv, logs
}

type tripLog struct {
	mu    sync.Mutex
	trips []recordedTrip
}

type recordedTrip struct {
	status int
	body   string
}

func (l *tripLog) snapshot() []recordedTrip {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]recordedTrip(nil), l.trips...)
}

type authRT struct {
	base  http.RoundTripper
	token string
	log   *tripLog
}

func (a *authRT) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	if a.token != "" {
		clone.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := a.base.RoundTrip(clone)
	if resp != nil {
		a.log.mu.Lock()
		a.log.trips = append(a.log.trips, recordedTrip{status: resp.StatusCode, body: string(body)})
		a.log.mu.Unlock()
	}
	return resp, err
}

func connectMCP(t *testing.T, mcpSrv *httptest.Server, token string) (*mcp.ClientSession, *tripLog) {
	t.Helper()
	log := &tripLog{}
	client := mcp.NewClient(&mcp.Implementation{Name: "bot-mcp-server-test", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             mcpSrv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: &authRT{base: http.DefaultTransport, token: token, log: log}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, log
}

func mustCall(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

func textOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty tool result")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content %T", res.Content[0])
	}
	return text.Text
}

func structuredMap(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content %T", res.StructuredContent)
	}
	return m
}

func assertLast(t *testing.T, fake *fakeAPI, method, path, rawQuery, body string) {
	t.Helper()
	call := fake.last()
	if call.Method != method || call.Path != path || call.RawQuery != rawQuery || call.Body != body {
		t.Fatalf("api call method=%s path=%s query=%s body=%s", call.Method, call.Path, call.RawQuery, call.Body)
	}
	assertDelegatedV1(t, call)
	if call.tokenInOther {
		t.Fatal("token appeared outside Authorization")
	}
	if len(call.Unexpected) != 0 {
		t.Fatalf("unexpected headers %v", call.Unexpected)
	}
}

func assertDelegatedV1(t *testing.T, call apiCall) {
	t.Helper()
	if !strings.HasPrefix(call.Path, "/v1/") {
		t.Fatalf("path %s", call.Path)
	}
	if call.callerLeaked || call.tokenInOther {
		t.Fatal("caller token reached /v1")
	}
	if len(call.Auth) != 1 || call.Auth[0] != "Bearer "+delegatedToken {
		t.Fatalf("authorization %q", call.Auth)
	}
	blob := call.Body + " " + call.RawQuery + " " + call.Path + " " + call.ContentType
	if strings.Contains(blob, userToken) || strings.Contains(blob, clientSecret) || strings.Contains(call.Auth[0], clientSecret) {
		t.Fatal("caller token or client secret reached /v1")
	}
}

func assertExchange(t *testing.T, call apiCall, resource string) {
	t.Helper()
	if call.Method != http.MethodPost || call.Path != "/oauth/token" {
		t.Fatalf("exchange %s %s", call.Method, call.Path)
	}
	raw := url.QueryEscape("bot-mcp-server") + ":" + url.QueryEscape(clientSecret)
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
	if len(call.Auth) != 1 || call.Auth[0] != wantAuth {
		t.Fatalf("exchange auth %q", call.Auth)
	}
	if !strings.HasPrefix(call.ContentType, "application/x-www-form-urlencoded") {
		t.Fatalf("content-type %s", call.ContentType)
	}
	form, err := url.ParseQuery(call.Body)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:token-exchange" {
		t.Fatalf("grant_type %q", form.Get("grant_type"))
	}
	if form.Get("subject_token") != userToken {
		t.Fatalf("subject_token %q", form.Get("subject_token"))
	}
	if form.Get("subject_token_type") != "urn:ietf:params:oauth:token-type:access_token" {
		t.Fatalf("subject_token_type %q", form.Get("subject_token_type"))
	}
	if form.Get("resource") != resource {
		t.Fatalf("resource %q", form.Get("resource"))
	}
	if strings.Contains(call.Auth[0], userToken) {
		t.Fatal("caller token in exchange authorization")
	}
}

func assertTool(t *testing.T, fake *fakeAPI, res *mcp.CallToolResult, method, path, rawQuery, body string) {
	t.Helper()
	assertLast(t, fake, method, path, rawQuery, body)
	assertPayload(t, res, fake.last().Payload)
}

func assertPayload(t *testing.T, res *mcp.CallToolResult, payload json.RawMessage) {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool error %s", textOf(t, res))
	}
	got, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var decoded, want any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("structured content\n got %s\nwant %s", got, payload)
	}
}

func assertServiceGoneAt(t *testing.T, res *mcp.CallToolResult, index int, want any) {
	t.Helper()
	items, ok := structuredMap(t, res)["items"].([]any)
	if !ok || index >= len(items) {
		t.Fatalf("items %#v", structuredMap(t, res)["items"])
	}
	row, ok := items[index].(map[string]any)
	if !ok {
		t.Fatalf("item %d %T", index, items[index])
	}
	got, ok := row["gone_at"]
	if !ok {
		t.Fatalf("item %d dropped gone_at %#v", index, row)
	}
	if got != want {
		t.Fatalf("item %d gone_at %#v, want %#v", index, got, want)
	}
}

func assertKeptPrefix(t *testing.T, body map[string]any, key, prefix string) {
	t.Helper()
	items, ok := body[key].([]any)
	if !ok || len(items) != 200 {
		t.Fatalf("%s len %d ok=%v", key, len(items), ok)
	}
	for i, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("%s[%d] %T", key, i, item)
		}
		if row["id"] != fmt.Sprintf("%s%03d", prefix, i) {
			t.Fatalf("%s[%d] id %v", key, i, row["id"])
		}
	}
}
