package tools

import (
	"bytes"
	"encoding/json"
	"regexp"
	"unicode/utf8"
)

// Redaction follows agent-protocol v1.0.3 (URL userinfo) then v1.0.2 (keywords).
// v1.3 log lines use that redaction, a 2048-byte cap per line, and 64 KiB total.
var (
	urlUserinfo = regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://[^/\s:@]*:)[^\s/]*@`)
	secretWord  = regexp.MustCompile(`(?i)([A-Za-z0-9_.-]*(?:authorization|api[_-]?key|password|passwd|token|secret|credential)[A-Za-z0-9_.-]*)(\s*[=:]\s*|\s+)(bearer\s+)?("[^"]*"|'[^']*'|\S+)`)
)

const (
	maxLogLineBytes = 2048
	maxLogBytes     = 64 << 10
)

func redactText(s string) string {
	s = urlUserinfo.ReplaceAllString(s, `${1}[REDACTED]@`)
	return secretWord.ReplaceAllString(s, `${1}=[REDACTED]`)
}

func capBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// redactLines rewrites every lines array: each string is redacted and capped,
// and the array stops at 64 KiB. A shortened array sets truncated on that object.
// The original JSON is returned when nothing changes.
func redactLines(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	changed := false
	walkRedactLines(v, &changed)
	if !changed {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

func walkRedactLines(v any, changed *bool) {
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			if key == "lines" {
				arr, ok := child.([]any)
				if !ok {
					continue
				}
				next, shortened := redactLineArray(arr)
				if shortened || !linesEqual(arr, next) {
					node[key] = next
					*changed = true
				}
				if shortened {
					node["truncated"] = true
					*changed = true
				}
				continue
			}
			walkRedactLines(child, changed)
		}
	case []any:
		for _, child := range node {
			walkRedactLines(child, changed)
		}
	}
}

func redactLineArray(arr []any) ([]any, bool) {
	out := make([]any, 0, len(arr))
	total := 0
	shortened := false
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			out = append(out, item)
			continue
		}
		redacted := redactText(s)
		next := capBytes(redacted, maxLogLineBytes)
		// A keyword replacement can be shorter than the secret. That is not a cut.
		if len(next) < len(redacted) {
			shortened = true
		}
		if total+len(next) > maxLogBytes {
			shortened = true
			break
		}
		total += len(next)
		out = append(out, next)
	}
	if len(out) < len(arr) {
		shortened = true
	}
	return out, shortened
}

func linesEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
