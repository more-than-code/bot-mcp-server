package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

const (
	imageEntry  = "webadmin"
	jobEntry    = "webjob"
	envLeak     = "img-env-value-do-not-leak"
	cmdLeak     = "img-cmd-do-not-leak"
	entryLeak   = "img-entry-do-not-leak"
	targetLeak  = "img-target-do-not-leak"
	secretLeak  = "img-secret-ref-do-not-leak"
	userLeak    = "img-user-do-not-leak"
	historyLeak = "img-history-do-not-leak"
	logLeak     = "img-log-do-not-leak"
)

var (
	digestAB = "sha256:" + strings.Repeat("ab", 32)
	digestCD = "sha256:" + strings.Repeat("cd", 32)
	digestEF = "sha256:" + strings.Repeat("ef", 32)
	digest12 = "sha256:" + strings.Repeat("12", 32)
)

func catalogItems(images bool) []any {
	items := []any{programCatalogItem()}
	if !images {
		return items
	}
	items = append(items, imageServiceItem(), imageJobItem())
	return items
}

func programCatalogItem() map[string]any {
	return map[string]any{
		"id": "88888888-8888-4888-8888-888888888888", "project_id": projectID,
		"name": entryName, "kind": "program",
		"command_display": "pnpm run dev\u202ehidden",
		"spec_hash":       "abc",
		"spec": map[string]any{
			"env":  map[string]any{"DATABASE_URL": "postgres://app:s3cret@db/app"},
			"args": []any{"--token", "raw-token"},
		},
		"my_actions": []any{"run", "stop", "restart", "logs", "status"},
	}
}

func imageServiceItem() map[string]any {
	return map[string]any{
		"name": imageEntry, "kind": "image",
		"command_display": "ghcr.io/example/web\u202eadmin:latest",
		"spec": map[string]any{
			"kind":              "service",
			"image":             "ghcr.io/example/web\u202eadmin:latest",
			"command":           cmdLeak,
			"entrypoint":        entryLeak,
			"user":              userLeak,
			"healthy_after_sec": 30,
			"ports":             []any{map[string]any{"host": 3031, "container": 3000, "public": false}},
			"env":               map[string]any{"NODE_ENV": envLeak},
			"secret_env":        map[string]any{"DB_URL": secretLeak},
			"env_sets":          []any{"base", "web"},
			"volumes": []any{
				map[string]any{"name": "webdata", "target": targetLeak, "read_only": true},
				map[string]any{"name": "services", "target": "/var/lib"},
			},
			"restart":     "unless-stopped",
			"memory_mb":   256,
			"timeout_sec": 0,
		},
		"my_actions": []any{"deploy", "logs", "status"},
	}
}

func imageJobItem() map[string]any {
	return map[string]any{
		"name": jobEntry, "kind": "image",
		"command_display": "ghcr.io/example/webjob:1",
		"spec": map[string]any{
			"kind":              "job",
			"image":             "ghcr.io/example/webjob:1",
			"healthy_after_sec": 0,
			"timeout_sec":       120,
			"env":               map[string]any{"NODE_ENV": envLeak},
		},
		"my_actions": []any{"deploy", "logs", "status"},
	}
}

func deployHistory() map[string]any {
	return map[string]any{"items": []any{
		map[string]any{
			"digest": digestAB, "outcome": "succeeded",
			"started_at": "2026-10-15T01:00:00Z", "finished_at": "2026-10-15T01:01:00Z",
			"note": historyLeak,
		},
		map[string]any{
			"digest": digestCD, "outcome": "unchanged",
			"finished_at": "2026-10-15T02:01:00Z",
		},
		map[string]any{
			"digest": digestEF, "outcome": "failed",
			"started_at": "2026-10-15T03:00:00Z",
		},
		map[string]any{
			"digest": digest12, "outcome": "succeeded",
			"finished_at": "2026-10-15T04:00:00Z",
		},
	}}
}

func imageDeployResult() map[string]any {
	return map[string]any{
		"deploy": map[string]any{
			"outcome": "succeeded", "digest": digestAB, "exit_code": 0,
		},
		"status": map[string]any{"container": "lab-webadmin", "state": "running"},
		"lines":  []any{"starting", "token=" + logLeak, "ready", "done"},
	}
}

const badImageState = "up-state-do-not-echo"

func timeoutImageStatus() map[string]any {
	return map[string]any{
		"container": nil, "state": "unknown", "exit_code": nil,
		"restart_count": 0, "started_at": nil, "finished_at": nil,
		"digest": nil, "ports": []any{},
	}
}

func timeoutDeployResult() map[string]any {
	return map[string]any{
		"deploy": map[string]any{"outcome": "timeout", "digest": nil, "exit_code": nil},
		"status": timeoutImageStatus(),
		"lines":  []any{},
	}
}

func badStateDeployResult() map[string]any {
	return map[string]any{
		"deploy": map[string]any{"outcome": "failed", "exit_code": 1},
		"status": map[string]any{"container": "lab-webadmin", "state": badImageState},
		"lines":  []any{},
	}
}

func confirmationPayloadText(plan string) map[string]any {
	return map[string]any{
		"confirmation_id": confirmationID,
		"plan":            plan,
		"expires_at":      "2026-10-15T00:05:00Z",
	}
}

func imagePlan(entry, ref string) string {
	return "will deploy " + entry + " (" + ref + ") on pi1"
}

func catalogEntryName(path string) string {
	prefix := "/v1/projects/" + projectID + "/catalog/"
	rest := strings.TrimPrefix(path, prefix)
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

func imageRefFor(entry string) string {
	switch entry {
	case imageEntry:
		return "ghcr.io/example/webadmin:latest"
	case jobEntry:
		return "ghcr.io/example/webjob:1"
	default:
		return "ghcr.io/example/" + entry + ":latest"
	}
}

func TestImageCatalog(t *testing.T) {
	fake := &fakeAPI{token: userToken, images: true}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	res := mustCall(t, cs, "list_catalog", map[string]any{"project_id": projectID})
	if res.IsError {
		t.Fatalf("list_catalog %s", textOf(t, res))
	}
	if textOf(t, res) != "3 catalog entries: tra-backend (program), webadmin (image service), webjob (image job)" {
		t.Fatalf("text %q", textOf(t, res))
	}
	blob := mustJSON(t, res.StructuredContent)
	for _, leak := range imageLeaks() {
		if strings.Contains(blob, leak) {
			t.Fatalf("catalog contains %s", leak)
		}
	}
	if strings.Contains(blob, `\u202e`) {
		t.Fatalf("image ref %s", blob)
	}
	items, _ := structuredMap(t, res)["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items %d", len(items))
	}
	prog, _ := items[0].(map[string]any)
	if prog["name"] != entryName || prog["kind"] != "program" || len(prog) != 5 {
		t.Fatalf("program %#v", prog)
	}
	display, _ := prog["command_display"].(string)
	if display != "pnpm run dev\uFFFDhidden" {
		t.Fatalf("command_display %q", display)
	}
	img, _ := items[1].(map[string]any)
	if img["kind"] != "image" || img["image_kind"] != "service" || img["healthcheck"] != false {
		t.Fatalf("image %#v", img)
	}
	if img["image"] != "ghcr.io/example/web\uFFFDadmin:latest" {
		t.Fatalf("image ref %#v", img["image"])
	}
	keys, _ := img["env_keys"].([]any)
	if len(keys) != 2 || keys[0] != "DB_URL" || keys[1] != "NODE_ENV" {
		t.Fatalf("env_keys %#v", img["env_keys"])
	}
	sets, _ := img["env_sets"].([]any)
	if len(sets) != 2 || sets[0] != "base" || sets[1] != "web" {
		t.Fatalf("env_sets %#v", sets)
	}
	vols, _ := img["volumes"].([]any)
	if len(vols) != 2 || vols[0] != "webdata" || vols[1] != "services" {
		t.Fatalf("volumes %#v", vols)
	}
	ports, _ := img["ports"].([]any)
	port, _ := ports[0].(map[string]any)
	if !numberIs(port["host"], 3031) || port["public"] != false {
		t.Fatalf("port %#v", port)
	}
	actions, _ := img["my_actions"].([]any)
	if !stringListHas(actions, "deploy") {
		t.Fatalf("my_actions %#v", actions)
	}
	for _, key := range []string{"spec", "command", "entrypoint", "user", "env", "secret_env", "restart", "memory_mb"} {
		if _, ok := img[key]; ok {
			t.Fatalf("kept %s", key)
		}
	}
	job, _ := items[2].(map[string]any)
	if job["image_kind"] != "job" || job["image"] != "ghcr.io/example/webjob:1" || job["healthcheck"] != false {
		t.Fatalf("job %#v", job)
	}
	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestDeployEntryConfirm(t *testing.T) {
	fake := &fakeAPI{token: userToken, images: true}
	mcpSrv, logs := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	deployPath := "/v1/projects/" + projectID + "/catalog/" + imageEntry + "/deploy"
	deploysPath := "/v1/projects/" + projectID + "/catalog/" + imageEntry + "/deploys"

	before := fake.count()
	res := mustCall(t, cs, "deploy_entry", map[string]any{
		"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "deploy-1",
	})
	if fake.confirmCount() != 0 {
		t.Fatal("deploy performed a confirm")
	}
	calls := callsSince(fake, before)
	assertOneBody(t, calls, http.MethodPost, deployPath, `{"host_id":"`+hostID+`","idempotency_key":"deploy-1"}`)
	if len(findCalls(calls, http.MethodGet, deploysPath)) != 1 {
		t.Fatal("missing history fetch")
	}
	want := "nothing ran yet. confirmation id " + confirmationID + ". Expires 2026-10-15T00:05:00Z.\n" +
		imagePlan(imageEntry, imageRefFor(imageEntry)) + "\n" +
		"recent deploys:\n" +
		"sha256:abababababab succeeded 2026-10-15T01:01:00Z\n" +
		"sha256:cdcdcdcdcdcd unchanged 2026-10-15T02:01:00Z\n" +
		"sha256:efefefefefef failed 2026-10-15T03:00:00Z"
	if textOf(t, res) != want {
		t.Fatalf("text %q", textOf(t, res))
	}
	blob := mustJSON(t, res.StructuredContent)
	for _, leak := range append(imageLeaks(), logLeak) {
		if strings.Contains(blob, leak) || strings.Contains(textOf(t, res), leak) {
			t.Fatalf("result contains %s", leak)
		}
	}
	if strings.Contains(textOf(t, res), digestAB) || strings.Contains(textOf(t, res), "sha256:121212121212") {
		t.Fatalf("text kept a full or dropped digest %q", textOf(t, res))
	}
	hist, _ := structuredMap(t, res)["history"].([]any)
	if len(hist) != 3 {
		t.Fatalf("history %#v", structuredMap(t, res)["history"])
	}
	row, _ := hist[0].(map[string]any)
	if row["digest"] != digestAB || row["outcome"] != "succeeded" || row["time"] != "2026-10-15T01:01:00Z" || len(row) != 3 {
		t.Fatalf("history row %#v", row)
	}
	third, _ := hist[2].(map[string]any)
	if third["time"] != "2026-10-15T03:00:00Z" || third["digest"] != digestEF {
		t.Fatalf("third %#v", third)
	}

	fake.imageConfirm = true
	res = mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "confirm-1",
	})
	if fake.confirmCount() != 1 {
		t.Fatalf("confirms %d", fake.confirmCount())
	}
	assertLast(t, fake, http.MethodPost, "/v1/confirmations/"+confirmationID, "", `{"idempotency_key":"confirm-1"}`)
	confirmText := "succeeded sha256:abababababab exit 0\ntoken=[REDACTED]\nready\ndone"
	if textOf(t, res) != confirmText {
		t.Fatalf("confirm text %q", textOf(t, res))
	}
	confirmed := mustJSON(t, res.StructuredContent)
	if strings.Contains(confirmed, logLeak) || strings.Contains(textOf(t, res), logLeak) || strings.Contains(textOf(t, res), "starting") {
		t.Fatalf("confirm leaked %s text %q", confirmed, textOf(t, res))
	}

	before = fake.count()
	res = mustCall(t, cs, "rollback_entry", map[string]any{
		"project_id": projectID, "entry": jobEntry, "host_id": hostID,
		"digest": digestAB, "idempotency_key": "rollback-1",
	})
	if fake.confirmCount() != 1 {
		t.Fatal("rollback performed a confirm")
	}
	calls = callsSince(fake, before)
	rollbackPath := "/v1/projects/" + projectID + "/catalog/" + jobEntry + "/rollback"
	assertOneBody(t, calls, http.MethodPost, rollbackPath, `{"host_id":"`+hostID+`","digest":"`+digestAB+`","idempotency_key":"rollback-1"}`)
	for _, call := range calls {
		if strings.HasSuffix(call.Path, "/deploys") {
			t.Fatal("rollback fetched history")
		}
	}
	if !strings.Contains(textOf(t, res), "nothing ran yet") || !strings.Contains(textOf(t, res), imagePlan(jobEntry, digestAB)) {
		t.Fatalf("rollback text %q", textOf(t, res))
	}
	if _, ok := structuredMap(t, res)["history"]; ok {
		t.Fatal("rollback attached history")
	}

	for _, tc := range []struct {
		name string
		args map[string]any
		body string
	}{
		{
			name: "force true",
			args: map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "deploy-2", "force": true},
			body: `{"host_id":"` + hostID + `","force":true,"idempotency_key":"deploy-2"}`,
		},
		{
			name: "force false",
			args: map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "deploy-3", "force": false},
			body: `{"host_id":"` + hostID + `","force":false,"idempotency_key":"deploy-3"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from := fake.count()
			got := mustCall(t, cs, "deploy_entry", tc.args)
			if got.IsError || !strings.Contains(textOf(t, got), "nothing ran yet") {
				t.Fatalf("error=%v text %q", got.IsError, textOf(t, got))
			}
			if tc.name == "force true" && !strings.Contains(textOf(t, got), "\nforce: true") {
				t.Fatalf("text %q", textOf(t, got))
			}
			if tc.name == "force true" {
				if structuredMap(t, got)["force"] != true {
					t.Fatalf("force %#v", structuredMap(t, got)["force"])
				}
				plan, _ := structuredMap(t, got)["plan"].(string)
				if !strings.Contains(plan, "\nforce: true") {
					t.Fatalf("plan %q", plan)
				}
			}
			if tc.name == "force false" && strings.Contains(textOf(t, got), "force: true") {
				t.Fatalf("text %q", textOf(t, got))
			}
			if tc.name == "force false" {
				if _, ok := structuredMap(t, got)["force"]; ok {
					t.Fatal("force false was stored")
				}
			}
			assertOneBody(t, callsSince(fake, from), http.MethodPost, deployPath, tc.body)
		})
	}

	if logsContainSecret(logs.String()) {
		t.Fatal("secret material appeared in logs")
	}
}

func TestDeployDoesNotActOnHTTP200(t *testing.T) {
	fake := &fakeAPI{token: userToken, images: true, mode: "acted"}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "deploy_entry", map[string]any{
		"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "deploy-1",
	})
	if !res.IsError || textOf(t, res) != "error: expected a confirmation" {
		t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
	}
	if fake.confirmCount() != 0 {
		t.Fatal("confirm ran")
	}
	for _, call := range fake.snapshot() {
		if strings.HasSuffix(call.Path, "/deploys") {
			t.Fatal("history fetched without a 202")
		}
	}
}

func TestDeployHistoryFailureStillReturnsPlan(t *testing.T) {
	fake := &fakeAPI{token: userToken, images: true, mode: "history-down"}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "deploy_entry", map[string]any{
		"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "deploy-1",
	})
	if res.IsError || !strings.Contains(textOf(t, res), "nothing ran yet") || !strings.Contains(textOf(t, res), imagePlan(imageEntry, imageRefFor(imageEntry))) {
		t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
	}
	if strings.Contains(textOf(t, res), "recent deploys:") {
		t.Fatalf("text %q", textOf(t, res))
	}
	if _, ok := structuredMap(t, res)["history"]; ok {
		t.Fatal("history attached after a failed fetch")
	}
	if fake.confirmCount() != 0 {
		t.Fatal("confirm ran")
	}
}

func TestImageStateOnTools(t *testing.T) {
	fake := &fakeAPI{token: userToken, images: true, imageConfirm: true}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	fake.setMode("confirm-timeout")

	res := mustCall(t, cs, "deploy_entry", map[string]any{
		"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "deploy-1",
	})
	if res.IsError || !strings.Contains(textOf(t, res), "nothing ran yet") {
		t.Fatalf("deploy %s", textOf(t, res))
	}
	res = mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "confirm-1",
		"project_id": projectID, "entry": imageEntry,
	})
	if res.IsError || textOf(t, res) != "timeout unknown" {
		t.Fatalf("deploy confirm %q", textOf(t, res))
	}
	status, _ := structuredMap(t, res)["status"].(map[string]any)
	if status["state"] != "unknown" || !numberIs(status["restart_count"], 0) {
		t.Fatalf("deploy status %#v", status)
	}
	if _, ok := status["container"]; ok {
		t.Fatalf("null container kept %#v", status)
	}
	ports, _ := status["ports"].([]any)
	if len(ports) != 0 {
		t.Fatalf("ports %#v", status["ports"])
	}

	res = mustCall(t, cs, "rollback_entry", map[string]any{
		"project_id": projectID, "entry": imageEntry, "host_id": hostID,
		"digest": digestAB, "idempotency_key": "rollback-1",
	})
	if res.IsError || !strings.Contains(textOf(t, res), "nothing ran yet") {
		t.Fatalf("rollback %s", textOf(t, res))
	}
	res = mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "confirm-2",
		"project_id": projectID, "entry": imageEntry,
	})
	if res.IsError || textOf(t, res) != "timeout unknown" {
		t.Fatalf("rollback confirm %q", textOf(t, res))
	}

	fake.setMode("status-unknown")
	res = mustCall(t, cs, "entry_status", map[string]any{
		"project_id": projectID, "entry": imageEntry, "host_id": hostID,
	})
	if res.IsError || textOf(t, res) != "image unknown restarts 0" || structuredMap(t, res)["state"] != "unknown" {
		t.Fatalf("status %q %#v", textOf(t, res), structuredMap(t, res))
	}

	fake.setMode("status-bad")
	res = mustCall(t, cs, "entry_status", map[string]any{
		"project_id": projectID, "entry": imageEntry, "host_id": hostID,
	})
	if !res.IsError || textOf(t, res) != "error: status response was not usable" || strings.Contains(textOf(t, res), badImageState) {
		t.Fatalf("bad status %q", textOf(t, res))
	}

	fake.setMode("confirm-bad-state")
	res = mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "confirm-3",
		"project_id": projectID, "entry": imageEntry,
	})
	if res.IsError || strings.Contains(textOf(t, res), badImageState) || !strings.Contains(textOf(t, res), "failed") {
		t.Fatalf("bad confirm %q", textOf(t, res))
	}
	if _, ok := structuredMap(t, res)["status"]; ok || strings.Contains(mustJSON(t, res.StructuredContent), badImageState) {
		t.Fatalf("bad confirm kept status %s", mustJSON(t, res.StructuredContent))
	}
}

func TestImageLogsStatus(t *testing.T) {
	fake := &fakeAPI{token: userToken, images: true}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	res := mustCall(t, cs, "entry_logs", map[string]any{
		"project_id": projectID, "entry": imageEntry, "host_id": hostID,
	})
	assertLast(t, fake, http.MethodPost, "/v1/projects/"+projectID+"/catalog/"+imageEntry+"/logs", "", `{"host_id":"`+hostID+`"}`)
	if textOf(t, res) != "2 log lines for lab-webadmin" {
		t.Fatalf("logs text %q", textOf(t, res))
	}
	logged := mustJSON(t, res.StructuredContent)
	if strings.Contains(logged, logLeak) || !strings.Contains(logged, "token=[REDACTED]") {
		t.Fatalf("logs %s", logged)
	}

	res = mustCall(t, cs, "entry_status", map[string]any{
		"project_id": projectID, "entry": imageEntry, "host_id": hostID,
	})
	if textOf(t, res) != "lab-webadmin running sha256:abababababab exit 0 restarts 1" {
		t.Fatalf("status text %q", textOf(t, res))
	}
	body := structuredMap(t, res)
	if body["container"] != "lab-webadmin" || body["state"] != "running" || body["digest"] != digestAB {
		t.Fatalf("status %#v", body)
	}
}

func TestDeployTimeoutText(t *testing.T) {
	fake := &fakeAPI{token: userToken, images: true}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	rt := &pathErrRT{base: http.DefaultTransport}
	fake.client.SetRoundTripper(rt)

	rt.errFor = func(path string) error {
		if strings.HasSuffix(path, "/deploy") || strings.HasSuffix(path, "/rollback") {
			return context.DeadlineExceeded
		}
		return nil
	}
	before := fake.count()
	res := mustCall(t, cs, "deploy_entry", map[string]any{
		"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "deploy-1",
	})
	if textOf(t, res) != "error: api request failed" || strings.Contains(textOf(t, res), "still running") {
		t.Fatalf("text %q", textOf(t, res))
	}
	for _, call := range callsSince(fake, before) {
		if strings.HasSuffix(call.Path, "/deploys") {
			t.Fatal("history fetched after a timed out deploy")
		}
	}

	rt.errFor = func(path string) error {
		if strings.HasSuffix(path, "/deploy") {
			return errors.New("dial tcp: connection refused")
		}
		return nil
	}
	res = mustCall(t, cs, "deploy_entry", map[string]any{
		"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "deploy-1",
	})
	if textOf(t, res) != "error: api request failed" || strings.Contains(textOf(t, res), "still running") {
		t.Fatalf("text %q", textOf(t, res))
	}

	rt.errFor = func(path string) error {
		if strings.Contains(path, "/confirmations/") {
			return context.DeadlineExceeded
		}
		return nil
	}
	res = mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "confirm-1",
	})
	if textOf(t, res) != "error: api request failed" || strings.Contains(textOf(t, res), "still running") {
		t.Fatalf("program confirm text %q", textOf(t, res))
	}

	res = mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "confirm-1",
		"project_id": projectID, "entry": imageEntry,
	})
	if textOf(t, res) != "error: still running on the host, check entry_status later" {
		t.Fatalf("image confirm text %q", textOf(t, res))
	}
}

func TestImageCallDeadlines(t *testing.T) {
	if apiclient.Timeout != 35*time.Second || apiclient.MaxCallTimeout != 50*time.Second {
		t.Fatalf("timeouts default %s max %s", apiclient.Timeout, apiclient.MaxCallTimeout)
	}
	fake := &fakeAPI{token: userToken, images: true}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	rt := &deadlineRT{base: http.DefaultTransport}
	fake.client.SetRoundTripper(rt)
	catalogPath := "/v1/projects/" + projectID + "/catalog"

	check := func(name, method, path string, want time.Duration, hits []deadlineHit) {
		t.Helper()
		var matched []deadlineHit
		for _, hit := range hits {
			if hit.method == method && hit.path == path {
				matched = append(matched, hit)
			}
		}
		if len(matched) != 1 {
			t.Fatalf("%s %s %s hits %d in %+v", name, method, path, len(matched), hits)
		}
		got := matched[0].remain
		if got < want-15*time.Second || got > want+time.Second {
			t.Fatalf("%s deadline %s want %s", name, got, want)
		}
	}
	step := func(call func()) []deadlineHit {
		t.Helper()
		from := rt.count()
		call()
		return rt.since(from)
	}

	hits := step(func() {
		mustCall(t, cs, "list_catalog", map[string]any{"project_id": projectID})
	})
	check("list", http.MethodGet, catalogPath, apiclient.Timeout, hits)

	hits = step(func() {
		mustCall(t, cs, "run_entry", map[string]any{
			"project_id": projectID, "entry": entryName, "host_id": hostID, "idempotency_key": "run-1",
		})
	})
	check("run", http.MethodPost, catalogPath+"/"+entryName+"/run", apiclient.Timeout, hits)

	hits = step(func() {
		mustCall(t, cs, "entry_logs", map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID})
	})
	check("logs", http.MethodPost, catalogPath+"/"+imageEntry+"/logs", apiclient.Timeout, hits)

	hits = step(func() {
		mustCall(t, cs, "entry_status", map[string]any{"project_id": projectID, "entry": imageEntry, "host_id": hostID})
	})
	check("status", http.MethodPost, catalogPath+"/"+imageEntry+"/status", apiclient.Timeout, hits)

	hits = step(func() {
		res := mustCall(t, cs, "deploy_entry", map[string]any{
			"project_id": projectID, "entry": imageEntry, "host_id": hostID, "idempotency_key": "deploy-1",
		})
		if res.IsError {
			t.Fatalf("deploy %s", textOf(t, res))
		}
	})
	check("deploy", http.MethodPost, catalogPath+"/"+imageEntry+"/deploy", apiclient.Timeout, hits)
	check("deploys", http.MethodGet, catalogPath+"/"+imageEntry+"/deploys", apiclient.Timeout, hits)
	for _, hit := range hits {
		if hit.path == catalogPath {
			t.Fatal("deploy prepare read the catalog")
		}
	}

	hits = step(func() {
		res := mustCall(t, cs, "rollback_entry", map[string]any{
			"project_id": projectID, "entry": jobEntry, "host_id": hostID,
			"digest": digestAB, "idempotency_key": "rollback-1",
		})
		if res.IsError {
			t.Fatalf("rollback %s", textOf(t, res))
		}
	})
	check("rollback", http.MethodPost, catalogPath+"/"+jobEntry+"/rollback", apiclient.Timeout, hits)
	for _, hit := range hits {
		if hit.path == catalogPath || strings.HasSuffix(hit.path, "/deploys") {
			t.Fatal("rollback used a long lookup")
		}
	}

	hits = step(func() {
		res := mustCall(t, cs, "deploy_entry", map[string]any{
			"project_id": projectID, "entry": "no-such", "host_id": hostID, "idempotency_key": "deploy-x",
		})
		if res.IsError {
			t.Fatalf("unknown deploy %s", textOf(t, res))
		}
	})
	check("unknown", http.MethodPost, catalogPath+"/no-such/deploy", apiclient.Timeout, hits)

	confirmPath := "/v1/confirmations/" + confirmationID
	hits = step(func() {
		mustCall(t, cs, "confirm", map[string]any{
			"confirmation_id": confirmationID, "idempotency_key": "confirm-plain",
		})
	})
	check("confirm plain", http.MethodPost, confirmPath, apiclient.Timeout, hits)
	for _, hit := range hits {
		if hit.path == catalogPath {
			t.Fatal("program confirm read the catalog")
		}
	}

	hits = step(func() {
		res := mustCall(t, cs, "confirm", map[string]any{
			"confirmation_id": confirmationID, "idempotency_key": "confirm-prog",
			"project_id": projectID, "entry": entryName,
		})
		if res.IsError {
			t.Fatalf("program confirm %s", textOf(t, res))
		}
	})
	check("program catalog", http.MethodGet, catalogPath, apiclient.Timeout, hits)
	check("program confirm", http.MethodPost, confirmPath, apiclient.Timeout, hits)
	assertLast(t, fake, http.MethodPost, confirmPath, "", `{"idempotency_key":"confirm-prog"}`)

	hits = step(func() {
		res := mustCall(t, cs, "confirm", map[string]any{
			"confirmation_id": confirmationID, "idempotency_key": "confirm-svc",
			"project_id": projectID, "entry": imageEntry,
		})
		if res.IsError {
			t.Fatalf("service confirm %s", textOf(t, res))
		}
	})
	check("service catalog", http.MethodGet, catalogPath, apiclient.Timeout, hits)
	check("service confirm", http.MethodPost, confirmPath, apiclient.MaxCallTimeout, hits)
	assertLast(t, fake, http.MethodPost, confirmPath, "", `{"idempotency_key":"confirm-svc"}`)

	hits = step(func() {
		res := mustCall(t, cs, "confirm", map[string]any{
			"confirmation_id": confirmationID, "idempotency_key": "confirm-job",
			"project_id": projectID, "entry": jobEntry,
		})
		if res.IsError {
			t.Fatalf("job confirm %s", textOf(t, res))
		}
	})
	check("job confirm", http.MethodPost, confirmPath, apiclient.MaxCallTimeout, hits)

	hits = step(func() {
		res := mustCall(t, cs, "confirm", map[string]any{
			"confirmation_id": confirmationID, "idempotency_key": "confirm-miss",
			"project_id": projectID, "entry": "no-such",
		})
		if res.IsError {
			t.Fatalf("unknown confirm %s", textOf(t, res))
		}
	})
	check("unknown confirm", http.MethodPost, confirmPath, apiclient.MaxCallTimeout, hits)

	fake.setMode("catalog-down")
	hits = step(func() {
		res := mustCall(t, cs, "confirm", map[string]any{
			"confirmation_id": confirmationID, "idempotency_key": "confirm-down",
			"project_id": projectID, "entry": imageEntry,
		})
		if res.IsError {
			t.Fatalf("catalog down confirm %s", textOf(t, res))
		}
	})
	check("catalog down confirm", http.MethodPost, confirmPath, apiclient.MaxCallTimeout, hits)
}

func TestWriteTimeoutStaysShort(t *testing.T) {
	if writeTimeout != 70*time.Second {
		t.Fatalf("server writeTimeout %s", writeTimeout)
	}
	if mcpWriteTimeout != apiclient.MaxCallTimeout+70*time.Second || mcpWriteTimeout <= apiclient.MaxCallTimeout {
		t.Fatalf("mcp write deadline %s", mcpWriteTimeout)
	}
}

func TestRoutesWriteDeadline(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	apiSrv := httptest.NewServer(fake)
	t.Cleanup(apiSrv.Close)
	api, err := apiclient.New(apiSrv.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newHandler(config{
		Listen:       "127.0.0.1:0",
		APIURL:       apiSrv.URL,
		PublicOrigin: "https://mcp.example/",
		ClientSecret: clientSecret,
	}, api, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	health := &deadlineRec{ResponseWriter: httptest.NewRecorder()}
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if !health.set.IsZero() {
		t.Fatal("health extended the write deadline")
	}
	anon := &deadlineRec{ResponseWriter: httptest.NewRecorder()}
	anonReq := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	// A path-only NewRequest has an empty Host, which /mcp now refuses before
	// the write-budget wrapper. Set a loopback Host so these still reach it.
	anonReq.Host = "127.0.0.1"
	handler.ServeHTTP(anon, anonReq)
	if !anon.set.IsZero() {
		t.Fatal("unauthenticated mcp extended the write deadline")
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer "+userToken)
	authed := &deadlineRec{ResponseWriter: httptest.NewRecorder()}
	handler.ServeHTTP(authed, req)
	if !authed.set.IsZero() {
		t.Fatal("short mcp request extended the write deadline")
	}
}

func TestLongConfirmExtendsWriteDeadline(t *testing.T) {
	fake := &fakeAPI{token: userToken, images: true}
	apiSrv := httptest.NewServer(fake)
	t.Cleanup(apiSrv.Close)
	api, err := apiclient.New(apiSrv.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newHandler(config{
		Listen:       "127.0.0.1:0",
		APIURL:       apiSrv.URL,
		PublicOrigin: "https://mcp.example/",
		ClientSecret: clientSecret,
	}, api, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []time.Duration
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &deadlineRec{ResponseWriter: w}
		handler.ServeHTTP(rec, r)
		if !rec.set.IsZero() {
			mu.Lock()
			got = append(got, time.Until(rec.set))
			mu.Unlock()
		}
	})
	mcpSrv := httptest.NewServer(wrapped)
	t.Cleanup(mcpSrv.Close)
	cs, _ := connectMCP(t, mcpSrv, userToken)

	res := mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "confirm-prog",
		"project_id": projectID, "entry": entryName,
	})
	if res.IsError {
		t.Fatalf("program confirm %s", textOf(t, res))
	}
	if len(got) != 0 {
		t.Fatalf("program confirm extended the write deadline by %s", got)
	}

	res = mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "confirm-svc",
		"project_id": projectID, "entry": imageEntry,
	})
	if res.IsError {
		t.Fatalf("service confirm %s", textOf(t, res))
	}
	if len(got) != 1 || !nearBudget(got[0], apiclient.MaxCallTimeout+writeTimeout) {
		t.Fatalf("service write deadline %s", got)
	}

	res = mustCall(t, cs, "confirm", map[string]any{
		"confirmation_id": confirmationID, "idempotency_key": "confirm-miss",
		"project_id": projectID, "entry": "no-such",
	})
	if res.IsError {
		t.Fatalf("unknown confirm %s", textOf(t, res))
	}
	if len(got) != 2 || !nearBudget(got[1], apiclient.MaxCallTimeout+writeTimeout) {
		t.Fatalf("unknown write deadline %s", got)
	}
}

func nearBudget(got, want time.Duration) bool {
	return got > want-15*time.Second && got < want+time.Second
}

func TestClientCancelStopsConfirm(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServer(t, fake)
	started := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	fake.client.SetRoundTripper(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/confirmations/") {
			return http.DefaultTransport.RoundTrip(r)
		}
		once.Do(func() { close(started) })
		<-r.Context().Done()
		close(stopped)
		return nil, r.Context().Err()
	}))
	cs, _ := connectMCP(t, mcpSrv, userToken)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = cs.CallTool(ctx, &mcp.CallToolParams{
			Name: "confirm",
			Arguments: map[string]any{
				"confirmation_id": confirmationID,
				"idempotency_key": "cancel-1",
			},
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("confirm did not reach the api")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled confirm kept the upstream call")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestCatalogRejectsUnusableBody(t *testing.T) {
	fake := &fakeAPI{token: userToken, mode: "catalog-shape"}
	mcpSrv, _ := newTestServer(t, fake)
	cs, _ := connectMCP(t, mcpSrv, userToken)
	res := mustCall(t, cs, "list_catalog", map[string]any{"project_id": projectID})
	if !res.IsError || textOf(t, res) != "error: catalog response was not usable" {
		t.Fatalf("error=%v text %q", res.IsError, textOf(t, res))
	}
	if strings.Contains(textOf(t, res), envLeak) {
		t.Fatalf("text %q", textOf(t, res))
	}
}

type deadlineRec struct {
	http.ResponseWriter
	set time.Time
}

func (d *deadlineRec) SetWriteDeadline(deadline time.Time) error {
	d.set = deadline
	return nil
}

func imageLeaks() []string {
	return []string{envLeak, cmdLeak, entryLeak, targetLeak, secretLeak, userLeak, historyLeak, "s3cret", "raw-token", "DATABASE_URL"}
}

func callsSince(fake *fakeAPI, n int) []apiCall {
	all := fake.snapshot()
	if n > len(all) {
		return nil
	}
	return all[n:]
}

func findCalls(calls []apiCall, method, path string) []apiCall {
	var out []apiCall
	for _, call := range calls {
		if call.Method == method && call.Path == path {
			out = append(out, call)
		}
	}
	return out
}

func assertOneBody(t *testing.T, calls []apiCall, method, path, body string) {
	t.Helper()
	found := findCalls(calls, method, path)
	if len(found) != 1 {
		t.Fatalf("%s %s calls %d", method, path, len(found))
	}
	if found[0].Body != body {
		t.Fatalf("body %s", found[0].Body)
	}
	assertDelegatedV1(t, found[0])
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func numberIs(v any, want float64) bool {
	n, ok := v.(float64)
	return ok && n == want
}

func stringListHas(items []any, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

type deadlineHit struct {
	method string
	path   string
	remain time.Duration
}

type deadlineRT struct {
	base http.RoundTripper
	mu   sync.Mutex
	hits []deadlineHit
}

func (d *deadlineRT) RoundTrip(r *http.Request) (*http.Response, error) {
	hit := deadlineHit{method: r.Method, path: r.URL.Path}
	if dl, ok := r.Context().Deadline(); ok {
		hit.remain = time.Until(dl)
	}
	d.mu.Lock()
	d.hits = append(d.hits, hit)
	d.mu.Unlock()
	return d.base.RoundTrip(r)
}

func (d *deadlineRT) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.hits)
}

func (d *deadlineRT) since(n int) []deadlineHit {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]deadlineHit(nil), d.hits[n:]...)
}

type pathErrRT struct {
	base   http.RoundTripper
	errFor func(path string) error
}

func (p *pathErrRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if p.errFor != nil {
		if err := p.errFor(r.URL.Path); err != nil {
			return nil, err
		}
	}
	return p.base.RoundTrip(r)
}
