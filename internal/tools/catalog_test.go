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
