package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactTextVectors(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"Authorization: Bearer abc123", "Authorization=[REDACTED]"},
		{"AWS_SECRET_ACCESS_KEY=AKIAxyz", "AWS_SECRET_ACCESS_KEY=[REDACTED]"},
		{`password="two words" next`, "password=[REDACTED] next"},
		{"postgres://app:s3cret@127.0.0.1/db", "postgres://app:[REDACTED]@127.0.0.1/db"},
		{"redis://localhost:6379", "redis://localhost:6379"},
		{"/usr/bin/node build", "/usr/bin/node build"},
		{"postgres: joe tour_agent 127.0.0.1(5000) idle", "postgres: joe tour_agent 127.0.0.1(5000) idle"},
		{"redis://:s3cret@127.0.0.1:6379", "redis://:[REDACTED]@127.0.0.1:6379"},
		{"https://user:p@ss@host/db", "https://user:[REDACTED]@host/db"},
		{"ssh://git@host:22/repo", "ssh://git@host:22/repo"},
		{"http://127.0.0.1:3031/health", "http://127.0.0.1:3031/health"},
		{"a://u:p@h/x b://c:d@e/y", "a://u:[REDACTED]@h/x b://c:[REDACTED]@e/y"},
		{"pull ghp_abcdefghijklmnopqrstuvwxyz", "pull [REDACTED]"},
		{"github_pat_11AAAAA", "[REDACTED]"},
		{"key AKIAIOSFODNN7EXAMPLE next", "key [REDACTED] next"},
		{"see Bearer eyJhbGciOi.eyJzdWI.sig", "see [REDACTED]"},
		{"bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig", "[REDACTED]"},
		{"pull gho_abcdefghijklmnopqrstuvwxyz", "pull [REDACTED]"},
		{"ghu_abcdefghijklmnopqrstuvwxyz", "[REDACTED]"},
		{"ghs_abcdefghijklmnopqrstuvwxyz", "[REDACTED]"},
		{"ghr_abcdefghijklmnopqrstuvwxyz", "[REDACTED]"},
		{"glpat-abc123def", "[REDACTED]"},
	}
	for _, tc := range cases {
		if got := redactText(tc.in); got != tc.out {
			t.Errorf("%q → %q, want %q", tc.in, got, tc.out)
		}
	}
}

func TestRedactLinesCapsAndTotal(t *testing.T) {
	long := strings.Repeat("a", maxLogLineBytes+100)
	raw := []byte(`{"unit":"u","lines":["` + long + `","token=abc"],"truncated":false}`)
	got := redactLines(raw)
	var body struct {
		Lines     []string `json:"lines"`
		Truncated bool     `json:"truncated"`
	}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Lines) != 2 || len(body.Lines[0]) != maxLogLineBytes {
		t.Fatalf("lines %#v", body.Lines)
	}
	if body.Lines[1] != "token=[REDACTED]" {
		t.Fatalf("line %q", body.Lines[1])
	}
	if !body.Truncated {
		t.Fatal("truncated not set")
	}
	if strings.Contains(string(got), "token=abc") {
		t.Fatal("secret survived")
	}

	line := strings.Repeat("b", 1000)
	var many []string
	for i := 0; i < 100; i++ {
		many = append(many, line)
	}
	buf, err := json.Marshal(map[string]any{"lines": many, "truncated": false})
	if err != nil {
		t.Fatal(err)
	}
	got = redactLines(buf)
	var cut struct {
		Lines     []string `json:"lines"`
		Truncated bool     `json:"truncated"`
	}
	if err := json.Unmarshal(got, &cut); err != nil {
		t.Fatal(err)
	}
	if !cut.Truncated || len(cut.Lines) >= 100 {
		t.Fatalf("kept %d truncated %v", len(cut.Lines), cut.Truncated)
	}
	total := 0
	for _, s := range cut.Lines {
		total += len(s)
	}
	if total > maxLogBytes {
		t.Fatalf("total %d", total)
	}

	// Redaction that shortens a line is not a cut: truncated stays false.
	secret := []byte(`{"lines":["password=\"two words\" next"],"truncated":false}`)
	got = redactLines(secret)
	var redacted struct {
		Lines     []string `json:"lines"`
		Truncated bool     `json:"truncated"`
	}
	if err := json.Unmarshal(got, &redacted); err != nil {
		t.Fatal(err)
	}
	if redacted.Truncated || len(redacted.Lines) != 1 || redacted.Lines[0] != "password=[REDACTED] next" {
		t.Fatalf("redaction-only %#v", redacted)
	}
}

func TestRedactLinesDropsNonStringShapes(t *testing.T) {
	const leak = "postgres://app:s3cret@db/app"
	got := redactLines([]byte(`{"container":"app","lines":"DATABASE_URL=` + leak + `"}`))
	if strings.Contains(string(got), "s3cret") || strings.Contains(string(got), leak) {
		t.Fatalf("string lines %s", got)
	}
	var body struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Lines) != 1 || body.Lines[0] != "DATABASE_URL=postgres://app:[REDACTED]@db/app" {
		t.Fatalf("lines %#v", body.Lines)
	}

	got = redactLines([]byte(`{"lines":[{"msg":"password=hunter2"}]}`))
	if strings.Contains(string(got), "hunter2") || strings.Contains(string(got), "msg") {
		t.Fatalf("object line %s", got)
	}

	got = redactLines([]byte(`not-json ` + leak))
	if string(got) != `{}` || strings.Contains(string(got), "s3cret") {
		t.Fatalf("bad json %s", got)
	}
}

func TestRedactLinesKeepsSecretPreviews(t *testing.T) {
	raw := []byte(`{"container":"app","lines":["abcd…","••••","ready abcd… ••••","password=hunter2"],"truncated":false}`)
	got := redactLines(raw)
	var body struct {
		Lines     []string `json:"lines"`
		Truncated bool     `json:"truncated"`
	}
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatal(err)
	}
	if body.Truncated || len(body.Lines) != 4 {
		t.Fatalf("lines %#v truncated %v", body.Lines, body.Truncated)
	}
	if body.Lines[0] != "abcd…" || body.Lines[1] != "••••" || body.Lines[2] != "ready abcd… ••••" {
		t.Fatalf("previews %#v", body.Lines[:3])
	}
	if body.Lines[3] != "password=[REDACTED]" {
		t.Fatalf("keyword line %q", body.Lines[3])
	}
}

func TestPresentRedactsStatusLines(t *testing.T) {
	raw := []byte(`{"unit":"u","active_state":"failed","lines":["AWS_SECRET_ACCESS_KEY=AKIAxyz"],"truncated":false}`)
	res := present(raw, "status")
	out, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "AKIAxyz") {
		t.Fatalf("secret in structured content %s", out)
	}
	if !strings.Contains(string(out), "AWS_SECRET_ACCESS_KEY=[REDACTED]") {
		t.Fatalf("structured %s", out)
	}
}
