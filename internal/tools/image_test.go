package tools

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
)

func TestBudgetFromCatalog(t *testing.T) {
	if deployServiceBudget != 990*time.Second {
		t.Fatalf("service budget %s", deployServiceBudget)
	}
	if apiclient.MaxCallTimeout != 4000*time.Second {
		t.Fatalf("unknown budget %s", apiclient.MaxCallTimeout)
	}
	raw := func(items ...string) json.RawMessage {
		return json.RawMessage(`{"items":[` + strings.Join(items, ",") + `]}`)
	}
	svc := `{"name":"webadmin","kind":"image","spec":{"kind":"service","healthy_after_sec":5,"timeout_sec":10}}`
	if got := waitFromCatalog(raw(svc), "webadmin"); got.limit != deployServiceBudget || !got.deploy {
		t.Fatalf("service %+v", got)
	}
	nilJob := `{"name":"webjob","kind":"image","spec":{"kind":"job"}}`
	if got := waitFromCatalog(raw(nilJob), "webjob"); got.limit != deployServiceBudget || !got.deploy {
		t.Fatalf("default job %+v", got)
	}
	cases := []struct {
		sec  int
		want time.Duration
	}{
		{sec: 10, want: 400 * time.Second},
		{sec: 120, want: 510 * time.Second},
		{sec: 3600, want: 3990 * time.Second},
		{sec: 9, want: apiclient.MaxCallTimeout},
		{sec: 3601, want: apiclient.MaxCallTimeout},
	}
	for _, tc := range cases {
		item := `{"name":"webjob","kind":"image","spec":{"kind":"job","timeout_sec":` + strconv.Itoa(tc.sec) + `}}`
		got := waitFromCatalog(raw(item), "webjob")
		if got.limit != tc.want || !got.deploy {
			t.Fatalf("timeout_sec %d budget %+v want %s", tc.sec, got, tc.want)
		}
	}
	program := `{"name":"tra-backend","kind":"program","spec":{"kind":"job","timeout_sec":120}}`
	if got := waitFromCatalog(raw(program), "tra-backend"); got.deploy || got.limit != apiclient.Timeout {
		t.Fatalf("program %+v", got)
	}
	other := `{"name":"webadmin","kind":"image","spec":{"kind":"other"}}`
	if got := waitFromCatalog(raw(other), "webadmin"); got.limit != apiclient.MaxCallTimeout || !got.deploy {
		t.Fatalf("kind %+v", got)
	}
	if got := waitFromCatalog(raw(svc), "missing"); got.limit != apiclient.MaxCallTimeout || !got.deploy {
		t.Fatalf("missing %+v", got)
	}
	if got := waitFromCatalog(json.RawMessage(`{`), "webadmin"); got.limit != apiclient.MaxCallTimeout || !got.deploy {
		t.Fatalf("bad json %+v", got)
	}
}

func TestStoredZeroTimers(t *testing.T) {
	const leak = "img-env-value-do-not-leak"
	raw := json.RawMessage(`{"items":[
		{"name":"webadmin","kind":"image","command_display":"ghcr.io/example/webadmin:latest",
		 "spec":{"kind":"service","image":"ghcr.io/example/webadmin:latest","healthy_after_sec":30,"timeout_sec":0,"env":{"NODE_ENV":"` + leak + `"}}},
		{"name":"webjob","kind":"image","command_display":"ghcr.io/example/webjob:1",
		 "spec":{"kind":"job","image":"ghcr.io/example/webjob:1","healthy_after_sec":0,"timeout_sec":120,"env":{"NODE_ENV":"` + leak + `"}}}
	]}`)
	svc := waitFromCatalog(raw, "webadmin")
	if svc.limit != deployServiceBudget || !svc.deploy {
		t.Fatalf("service timeout_sec 0 %+v", svc)
	}
	job := waitFromCatalog(raw, "webjob")
	if job.limit != 510*time.Second || !job.deploy {
		t.Fatalf("job healthy_after_sec 0 %+v", job)
	}
	out, err := projectCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), leak) || strings.Contains(string(out), "timeout_sec") || strings.Contains(string(out), "healthy_after") {
		t.Fatalf("catalog %s", out)
	}
	text := summarizeCatalog(out)
	if text != "2 catalog entries: webadmin (image service), webjob (image job)" {
		t.Fatalf("text %q", text)
	}
}

func TestImageStateUnknown(t *testing.T) {
	const bad = "up-state-do-not-echo"
	timeout := []byte(`{"deploy":{"outcome":"timeout","digest":null,"exit_code":null},"status":{"container":null,"state":"unknown","exit_code":null,"restart_count":0,"started_at":null,"finished_at":null,"digest":null,"ports":[]},"lines":[]}`)
	res := actedOrPending(http.StatusOK, timeout)
	if res.IsError || toolText(t, res) != "timeout unknown" {
		t.Fatalf("timeout text %q err=%v", toolText(t, res), res.IsError)
	}
	blob, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), `"state":"unknown"`) || strings.Contains(string(blob), `"container"`) {
		t.Fatalf("timeout structured %s", blob)
	}
	line, ok := summarizeImageStatus([]byte(`{"container":null,"state":"unknown","exit_code":null,"restart_count":0,"digest":null,"ports":[]}`))
	if !ok || line != "image unknown restarts 0" || !strings.Contains(line, "unknown") {
		t.Fatalf("status line %q ok=%v", line, ok)
	}
	if _, ok := summarizeImageStatus([]byte(`{"state":"` + bad + `"}`)); ok {
		t.Fatal("unlisted state summarized")
	}
	if !imageStateRejected([]byte(`{"state":"` + bad + `","note":"` + bad + `"}`)) {
		t.Fatal("unlisted status was accepted")
	}
	rejected := actedOrPending(http.StatusOK, []byte(`{"state":"`+bad+`","container":"lab"}`))
	if !rejected.IsError || toolText(t, rejected) != "error: status response was not usable" || strings.Contains(toolText(t, rejected), bad) {
		t.Fatalf("bare status %q", toolText(t, rejected))
	}
	badDeploy := actedOrPending(http.StatusOK, []byte(`{"deploy":{"outcome":"failed","exit_code":1},"status":{"state":"`+bad+`"},"lines":[]}`))
	if badDeploy.IsError || strings.Contains(toolText(t, badDeploy), bad) || !strings.Contains(toolText(t, badDeploy), "failed") {
		t.Fatalf("bad deploy text %q", toolText(t, badDeploy))
	}
	blob, err = json.Marshal(badDeploy.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), bad) || strings.Contains(string(blob), `"status"`) {
		t.Fatalf("bad deploy structured %s", blob)
	}
	if imageStateRejected([]byte(`{"unit":"u.service","active_state":"active","sub_state":"running"}`)) {
		t.Fatal("program status rejected")
	}
}

func TestProjectCatalogImageOmitsValues(t *testing.T) {
	const (
		envLeak    = "img-env-value-do-not-leak"
		cmdLeak    = "img-cmd-do-not-leak"
		entryLeak  = "img-entry-do-not-leak"
		targetLeak = "img-target-do-not-leak"
		secretLeak = "img-secret-ref-do-not-leak"
		userLeak   = "img-user-do-not-leak"
	)
	raw := []byte(`{"items":[
		{"id":"88888888-8888-4888-8888-888888888888","name":"tra-backend","kind":"program",
		 "command_display":"pnpm run dev\u202ehidden","spec_hash":"abc",
		 "spec":{"env":{"DATABASE_URL":"postgres://app:s3cret@db/app"},"args":["--token","raw-token"]},
		 "my_actions":["run","stop","restart","logs","status"]},
		{"name":"webadmin","kind":"image","command_display":"ghcr.io/example/web\u202eadmin:latest",
		 "spec":{"kind":"service","image":"ghcr.io/example/web\u202eadmin:latest",
		   "command":"` + cmdLeak + `","entrypoint":"` + entryLeak + `","user":"` + userLeak + `",
		   "ports":[{"host":3031,"container":3000,"public":false,"proto":"tcp"}],
		   "env":{"NODE_ENV":"` + envLeak + `"},
		   "secret_env":{"DB_URL":"` + secretLeak + `"},
		   "env_sets":["base","web"],
		   "volumes":[{"name":"webdata","target":"` + targetLeak + `","read_only":true},{"name":"services","target":"/var"}],
		   "restart":"unless-stopped","memory_mb":256,"timeout_sec":120},
		 "my_actions":["deploy","logs","status"]}
	]}`)
	out, err := projectCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, leak := range []string{envLeak, cmdLeak, entryLeak, targetLeak, secretLeak, userLeak, "s3cret", "raw-token", "DATABASE_URL", "unless-stopped"} {
		if strings.Contains(text, leak) {
			t.Fatalf("kept %s in %s", leak, text)
		}
	}
	if strings.Contains(text, `\u202e`) {
		t.Fatalf("kept bidi in %s", text)
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := dec.Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items %d", len(body.Items))
	}
	prog := body.Items[0]
	if len(prog) != 4 || prog["name"] != "tra-backend" || prog["kind"] != "program" {
		t.Fatalf("program %#v", prog)
	}
	img := body.Items[1]
	for _, key := range []string{"spec", "command", "entrypoint", "user", "secret_env", "env", "restart", "memory_mb", "timeout_sec"} {
		if _, ok := img[key]; ok {
			t.Fatalf("image kept %s", key)
		}
	}
	if img["kind"] != "image" || img["image_kind"] != "service" || img["image"] != "ghcr.io/example/web\uFFFDadmin:latest" {
		t.Fatalf("image %#v", img)
	}
	keys, _ := img["env_keys"].([]any)
	if len(keys) != 2 || keys[0] != "DB_URL" || keys[1] != "NODE_ENV" {
		t.Fatalf("env_keys %#v", img["env_keys"])
	}
	sets, _ := img["env_sets"].([]any)
	if len(sets) != 2 || sets[0] != "base" || sets[1] != "web" {
		t.Fatalf("env_sets %#v", img["env_sets"])
	}
	vols, _ := img["volumes"].([]any)
	if len(vols) != 2 || vols[0] != "webdata" || vols[1] != "services" {
		t.Fatalf("volumes %#v", img["volumes"])
	}
	ports, _ := img["ports"].([]any)
	port, _ := ports[0].(map[string]any)
	if port["host"] != json.Number("3031") || port["public"] != false {
		t.Fatalf("port %#v", port)
	}
	if _, ok := port["proto"]; ok {
		t.Fatal("kept proto")
	}
	actions, _ := img["my_actions"].([]any)
	if len(actions) != 3 || actions[0] != "deploy" {
		t.Fatalf("my_actions %#v", img["my_actions"])
	}
}

func TestProjectCatalogFailsClosed(t *testing.T) {
	const leak = "img-env-value-do-not-leak"
	for _, raw := range []string{
		`[{"spec":{"env":{"NODE_ENV":"` + leak + `"}}}]`,
		`{"items":null,"spec":{"env":{"NODE_ENV":"` + leak + `"}}}`,
		`{"spec":{"secret_env":{"DB_URL":"` + leak + `"}}}`,
		`not-json ` + leak,
	} {
		_, err := projectCatalog(json.RawMessage(raw))
		if err == nil || err.Error() != errCatalogShape.Error() || strings.Contains(err.Error(), leak) {
			t.Fatalf("raw %s err %v", raw, err)
		}
	}
	out, err := projectCatalog(json.RawMessage(`{"items":["` + leak + `"],"debug":"` + leak + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"items":[]}` || strings.Contains(string(out), leak) {
		t.Fatalf("out %s", out)
	}
	out, err = projectCatalog(json.RawMessage(`{"items":[{"name":"webadmin","kind":"image","spec":{"kind":"service","image":"ghcr.io/example/web:1","ports":[{"host":"` + leak + `","container":3000,"public":"yes"}],"env":{"NODE_ENV":"` + leak + `"}},"my_actions":["deploy",{"token":"` + leak + `"}]}],"note":"` + leak + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), leak) || strings.Contains(string(out), "note") {
		t.Fatalf("out %s", out)
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	dec.UseNumber()
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := dec.Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items %#v", body.Items)
	}
	actions, _ := body.Items[0]["my_actions"].([]any)
	if len(actions) != 1 || actions[0] != "deploy" {
		t.Fatalf("actions %#v", actions)
	}
	ports, _ := body.Items[0]["ports"].([]any)
	if len(ports) != 1 {
		t.Fatalf("ports %#v", ports)
	}
	port, _ := ports[0].(map[string]any)
	if _, ok := port["host"]; ok || port["public"] != nil {
		t.Fatalf("port %#v", port)
	}
	if port["container"] != json.Number("3000") {
		t.Fatalf("port %#v", port)
	}
	keys, _ := body.Items[0]["env_keys"].([]any)
	if len(keys) != 1 || keys[0] != "NODE_ENV" {
		t.Fatalf("env_keys %#v", keys)
	}
}

func TestProjectCatalogRedactsImageUserinfo(t *testing.T) {
	raw := []byte(`{"items":[{"name":"webadmin","kind":"image","command_display":"https://user:s3cret@registry.example/app:tag","spec":{"kind":"service","image":"https://user:s3cret@registry.example/app:tag"}}]}`)
	out, err := projectCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "s3cret") || !strings.Contains(string(out), "https://user:[REDACTED]@registry.example/app:tag") {
		t.Fatalf("out %s", out)
	}
}

func TestConfirmationDropsUnknownFields(t *testing.T) {
	const leak = "img-env-value-do-not-leak"
	const id = "Abcdefghijklmnopqrstuvwxyz0123456789_-ABCDE"
	raw := []byte(`{"confirmation_id":"` + id + `","plan":"will deploy https://user:s3cret@registry.example/app:tag","expires_at":"2026-10-15T00:05:00Z","env":{"NODE_ENV":"` + leak + `"},"note":"` + leak + `"}`)
	res := pendingConfirmation(raw)
	if res.IsError {
		t.Fatal(toolText(t, res))
	}
	blob, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), leak) || strings.Contains(string(blob), "s3cret") || strings.Contains(string(blob), `"note"`) || strings.Contains(string(blob), `"env"`) {
		t.Fatalf("structured %s", blob)
	}
	if !strings.Contains(string(blob), `https://user:[REDACTED]@registry.example/app:tag`) {
		t.Fatalf("plan %s", blob)
	}
	bad := pendingConfirmation([]byte(`not-json ` + leak))
	if !bad.IsError || toolText(t, bad) != "error: confirmation response was not usable" || strings.Contains(toolText(t, bad), leak) {
		t.Fatalf("text %q", toolText(t, bad))
	}
}

func TestDeployResultDropsUnknownFields(t *testing.T) {
	const leak = "img-env-value-do-not-leak"
	full := "sha256:" + strings.Repeat("ab", 32)
	raw := []byte(`{"deploy":{"outcome":"succeeded","digest":"` + full + `","exit_code":0,"by":"user","env":{"NODE_ENV":"` + leak + `"}},"status":{"container":"lab","state":"running","secret":"` + leak + `"},"lines":["ok",{"msg":"` + leak + `"}],"note":"` + leak + `"}`)
	res := actedOrPending(http.StatusOK, raw)
	if res.IsError {
		t.Fatal(toolText(t, res))
	}
	blob, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), leak) || strings.Contains(string(blob), `"note"`) || strings.Contains(string(blob), `"by"`) || strings.Contains(string(blob), `"env"`) || strings.Contains(string(blob), `"secret"`) {
		t.Fatalf("structured %s", blob)
	}
	text := toolText(t, res)
	if !strings.Contains(text, "succeeded") || !strings.Contains(text, "exit 0") || strings.Contains(text, leak) {
		t.Fatalf("text %q", text)
	}
}

func TestFromAPIRedactsMsg(t *testing.T) {
	res := fromAPI(&apiclient.Error{Code: "bad_request", Msg: "env NODE_ENV is invalid: token=img-log-do-not-leak"})
	text := toolText(t, res)
	if !strings.HasPrefix(text, "bad_request:") || strings.Contains(text, "img-log-do-not-leak") || !strings.Contains(text, "token=[REDACTED]") {
		t.Fatalf("text %q", text)
	}
}

func TestProjectHistoryKeepsThree(t *testing.T) {
	const leak = "img-history-do-not-leak"
	ab := "sha256:" + strings.Repeat("ab", 32)
	cd := "sha256:" + strings.Repeat("cd", 32)
	ef := "sha256:" + strings.Repeat("ef", 32)
	twelve := "sha256:" + strings.Repeat("12", 32)
	raw := []byte(`{"items":[
		{"digest":"` + ab + `","outcome":"succeeded","started_at":"2026-10-15T01:00:00Z","finished_at":"2026-10-15T01:01:00Z","note":"` + leak + `"},
		{"digest":"` + cd + `","outcome":"unchanged","finished_at":"2026-10-15T02:01:00Z"},
		{"digest":"` + ef + `","outcome":"failed","started_at":"2026-10-15T03:00:00Z"},
		{"digest":"` + twelve + `","outcome":"succeeded","finished_at":"2026-10-15T04:00:00Z"},
		{"digest":"","outcome":""}
	]}`)
	got := projectHistory(raw)
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), leak) || strings.Contains(string(out), twelve) || strings.Contains(string(out), "2026-10-15T04:00:00Z") {
		t.Fatalf("history %s", out)
	}
	if len(got) != 3 {
		t.Fatalf("len %d %s", len(got), out)
	}
	third, _ := got[2].(map[string]any)
	if third["digest"] != ef || third["time"] != "2026-10-15T03:00:00Z" || len(third) != 3 {
		t.Fatalf("third %#v", third)
	}
	first, _ := got[0].(map[string]any)
	if first["digest"] != ab || first["time"] != "2026-10-15T01:01:00Z" {
		t.Fatalf("first %#v", first)
	}
	if len(projectHistory([]byte(`{"items":[]}`))) != 0 {
		t.Fatal("empty history")
	}
}

func TestShortDigestAndSummaries(t *testing.T) {
	full := "sha256:" + strings.Repeat("ab", 32)
	short := "sha256:" + strings.Repeat("ab", 6)
	if shortDigest(full) != short || shortDigest(full) == full {
		t.Fatalf("short %q", shortDigest(full))
	}
	upper := "sha256:" + strings.Repeat("AB", 32)
	if shortDigest(upper) != upper {
		t.Fatalf("upper %q", shortDigest(upper))
	}
	payload := []byte(`{"deploy":{"outcome":"succeeded","digest":"` + full + `","exit_code":0},"lines":["starting","token=img-log-do-not-leak","ready","done"]}`)
	text, ok := summarizeImageDeploy(payload)
	if !ok {
		t.Fatal("deploy summary missing")
	}
	if !strings.Contains(text, "succeeded") || !strings.Contains(text, short) || !strings.Contains(text, "exit 0") || !strings.Contains(text, "done") {
		t.Fatalf("text %q", text)
	}
	if strings.Contains(text, "starting") || strings.Contains(text, "img-log-do-not-leak") || strings.Contains(text, full) {
		t.Fatalf("text %q", text)
	}
	if !strings.Contains(text, "token=[REDACTED]") {
		t.Fatalf("text %q", text)
	}
	status := []byte(`{"container":"lab-webadmin","state":"running","exit_code":0,"restart_count":1,"digest":"` + full + `"}`)
	line, ok := summarizeImageStatus(status)
	if !ok || line != "lab-webadmin running "+short+" exit 0 restarts 1" {
		t.Fatalf("status %q ok=%v", line, ok)
	}
	if _, ok := summarizeImageStatus([]byte(`{"unit":"u.service","active_state":"active","state":"running"}`)); ok {
		t.Fatal("program status summarized as an image")
	}
	if _, ok := summarizeImageDeploy([]byte(`{"unit":"u.service","active_state":"active"}`)); ok {
		t.Fatal("program status summarized as a deploy")
	}
}

func TestRequireDigest(t *testing.T) {
	good := "sha256:" + strings.Repeat("ab", 32)
	if err := requireDigest(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "sha256:abcd", "sha256:" + strings.Repeat("AB", 32), strings.Repeat("ab", 32), good + "ab"} {
		if err := requireDigest(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestPendingConfirmationHistoryUsesShortDigest(t *testing.T) {
	full := "sha256:" + strings.Repeat("ab", 32)
	short := "sha256:" + strings.Repeat("ab", 6)
	const id = "Abcdefghijklmnopqrstuvwxyz0123456789_-ABCDE"
	plan := []byte(`{"confirmation_id":"` + id + `","plan":"will deploy webadmin (ghcr.io/example/webadmin:latest) on pi1","expires_at":"2026-10-15T00:05:00Z"}`)
	hist := []byte(`{"items":[{"digest":"` + full + `","outcome":"succeeded","finished_at":"2026-10-15T01:01:00Z","note":"img-history-do-not-leak"}]}`)
	res := pendingConfirmation(attachHistory(plan, hist))
	text := toolText(t, res)
	if !strings.Contains(text, "nothing ran yet") || !strings.Contains(text, "recent deploys:") || !strings.Contains(text, short+" succeeded") {
		t.Fatalf("text %q", text)
	}
	if strings.Contains(text, full) || strings.Contains(text, "img-history-do-not-leak") {
		t.Fatalf("text %q", text)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		History []struct {
			Digest string `json:"digest"`
		} `json:"history"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.History) != 1 || body.History[0].Digest != full {
		t.Fatalf("structured %s", raw)
	}
	if strings.Contains(string(raw), "img-history-do-not-leak") {
		t.Fatalf("structured %s", raw)
	}
}
