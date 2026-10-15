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
