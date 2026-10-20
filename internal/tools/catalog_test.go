package tools

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPendingConfirmationIsNotProgramStatus(t *testing.T) {
	const id = "Abcdefghijklmnopqrstuvwxyz0123456789_-ABCDE"
	payload := []byte(`{"confirmation_id":"` + id + `","plan":"will run\nprog\u202eram","expires_at":"2026-10-15T00:05:00Z"}`)
	res := actedOrPending(http.StatusAccepted, payload)
	if res.IsError {
		t.Fatal("202 was a tool error")
	}
	text := toolText(t, res)
	if !strings.Contains(text, "nothing ran yet") || !strings.Contains(text, id) {
		t.Fatalf("text %q", text)
	}
	if !strings.Contains(text, "will run\uFFFDprog\uFFFDram") {
		t.Fatalf("plan %q", text)
	}
	if strings.Contains(text, "\nprog") || strings.Contains(text, "pid ") {
		t.Fatalf("plan not sanitized or treated as status: %q", text)
	}
	plan := structuredString(t, res, "plan")
	if plan != "will run\uFFFDprog\uFFFDram" || strings.ContainsRune(plan, '\u202e') || strings.Contains(plan, "\n") {
		t.Fatalf("structured plan %q", plan)
	}
	for _, r := range plan {
		if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp) {
			t.Fatalf("structured plan has %U", r)
		}
	}

	long := strings.Repeat("p", maxPlanRunes+40)
	capped := actedOrPending(http.StatusAccepted, []byte(`{"confirmation_id":"`+id+`","plan":"`+long+`","expires_at":"2026-10-15T00:05:00Z"}`))
	gotPlan := structuredString(t, capped, "plan")
	if len([]rune(gotPlan)) != maxPlanRunes || !strings.HasSuffix(gotPlan, "…") {
		t.Fatalf("plan cap %d %q", len([]rune(gotPlan)), gotPlan[:20])
	}

	status := []byte(`{"unit":"u","active_state":"active","sub_state":"running","main_pid":7}`)
	got := actedOrPending(http.StatusOK, status)
	if got.IsError || !strings.Contains(toolText(t, got), "pid 7") {
		t.Fatalf("200 text %q error=%v", toolText(t, got), got.IsError)
	}
	other := actedOrPending(http.StatusCreated, status)
	if !other.IsError {
		t.Fatal("201 treated as success")
	}
}

func TestConfirmationCatalogKeepsTheAPIPlan(t *testing.T) {
	const id = "Abcdefghijklmnopqrstuvwxyz0123456789_-ABCDE"
	plan := "secret_env OTHER←svc.OTHER, TOKEN←svc.TOKEN; env MODE=plain-mode-value\u202e"
	plan += strings.Repeat("p", maxPlanRunes)
	body, err := json.Marshal(map[string]any{
		"confirmation_id": id,
		"plan":            plan,
		"expires_at":      "2026-10-15T00:05:00Z",
		"note":            "drop-me",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := confirmationCatalog(http.StatusAccepted, body)
	if res.IsError {
		t.Fatal(toolText(t, res))
	}
	want := strings.ReplaceAll(plan, "\u202e", "\uFFFD")
	if got := structuredString(t, res, "plan"); got != want {
		t.Fatalf("plan len %d want %d", len([]rune(got)), len([]rune(want)))
	}
	if structuredString(t, res, "confirmation_id") != id {
		t.Fatal("missing confirmation id")
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "drop-me") || strings.Contains(string(raw), "secret_env=[REDACTED]") {
		t.Fatalf("structured %s", raw)
	}

	huge := strings.Repeat("q", maxCatalogPlanRunes+1)
	over, err := json.Marshal(map[string]any{
		"confirmation_id": id,
		"plan":            huge,
		"expires_at":      "2026-10-15T00:05:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	bad := confirmationCatalog(http.StatusAccepted, over)
	if !bad.IsError || toolText(t, bad) != "error: plan too long to show" {
		t.Fatalf("over error=%v text %q", bad.IsError, toolText(t, bad))
	}
	if strings.Contains(toolText(t, bad), id) || bad.StructuredContent != nil {
		t.Fatal("over-long plan returned a confirmation")
	}
}

func TestConfirmationOnlyRejectsHTTP200(t *testing.T) {
	status := []byte(`{"unit":"u","active_state":"active","sub_state":"running","main_pid":7}`)
	res := confirmationOnly(http.StatusOK, status)
	if !res.IsError {
		t.Fatal("200 treated as success")
	}
	if strings.Contains(toolText(t, res), "pid ") || strings.Contains(toolText(t, res), "nothing ran yet") {
		t.Fatalf("text %q", toolText(t, res))
	}
	accepted := confirmationOnly(http.StatusAccepted, []byte(`{"confirmation_id":"Abcdefghijklmnopqrstuvwxyz0123456789_-ABCDE","plan":"will run","expires_at":"2026-10-15T00:05:00Z"}`))
	if accepted.IsError || !strings.Contains(toolText(t, accepted), "nothing ran yet") {
		t.Fatalf("202 error=%v text %q", accepted.IsError, toolText(t, accepted))
	}
}

func structuredString(t *testing.T, res *mcp.CallToolResult, key string) string {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	s, _ := body[key].(string)
	return s
}
