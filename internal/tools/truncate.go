package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// truncateLists cuts items, projects, and points arrays longer than max.
// The original JSON is returned unchanged when nothing is cut, so numbers
// are not re-encoded. The note is empty when nothing is cut.
func truncateLists(raw json.RawMessage, max int) (json.RawMessage, string) {
	if max <= 0 || len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return raw, ""
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return raw, ""
	}
	changed := false
	walkTruncate(v, max, &changed)
	if !changed {
		return raw, ""
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw, ""
	}
	return out, fmt.Sprintf("truncated to %d items.", max)
}

func walkTruncate(v any, max int, changed *bool) {
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			if arr, ok := child.([]any); ok && truncatableKey(key) && len(arr) > max {
				node[key] = arr[:max]
				*changed = true
				continue
			}
			walkTruncate(child, max, changed)
		}
	case []any:
		for _, child := range node {
			walkTruncate(child, max, changed)
		}
	}
}

func truncatableKey(key string) bool {
	return key == "items" || key == "projects" || key == "points"
}
