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
	// tokenShape catches common token literals the keyword list does not name.
	// The bearer alternative is case-insensitive. GitHub prefixes stay exact.
	// This is best-effort. It is not the env-value boundary.
	tokenShape = regexp.MustCompile(`ghp_[A-Za-z0-9]+|gho_[A-Za-z0-9]+|ghu_[A-Za-z0-9]+|ghs_[A-Za-z0-9]+|ghr_[A-Za-z0-9]+|github_pat_[A-Za-z0-9_]+|\bAKIA[A-Z0-9]{16}\b|glpat-[A-Za-z0-9_-]+|(?i:bearer\s+eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)?)`)
)

const (
	maxLogLineBytes = 2048
	maxLogBytes     = 64 << 10
)

func redactText(s string) string {
	s = urlUserinfo.ReplaceAllString(s, `${1}[REDACTED]@`)
	s = secretWord.ReplaceAllString(s, `${1}=[REDACTED]`)
	return tokenShape.ReplaceAllString(s, `[REDACTED]`)
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

// redactLines rewrites every lines value. A string array is redacted and capped,
// and the array stops at 64 KiB. A shortened array sets truncated on that object.
// A lines value that is not an array of strings is not returned: a string is
// redacted into a one-element array, and any other shape is dropped.
// A body that is not JSON becomes an empty object. The raw body is never returned.
func redactLines(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []byte(`{}`)
	}
	changed := false
	walkRedactLines(v, &changed)
	if !changed {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return []byte(`{}`)
	}
	return out
}

func walkRedactLines(v any, changed *bool) {
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			if key == "lines" {
				switch line := child.(type) {
				case []any:
					next, shortened := redactLineArray(line)
					if shortened || !linesEqual(line, next) {
						node[key] = next
						*changed = true
					}
					if shortened {
						node["truncated"] = true
						*changed = true
					}
				case string:
					node[key] = []any{capBytes(redactText(line), maxLogLineBytes)}
					*changed = true
				default:
					delete(node, key)
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
			shortened = true
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
