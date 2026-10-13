package tools

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestTruncateListsLeavesShortPayloadUntouched(t *testing.T) {
	raw := json.RawMessage(`{"items":[{"n":1},{"n":2}],"next_cursor":null}`)
	got, note := truncateLists(raw, 200)
	if note != "" {
		t.Fatalf("note %q", note)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("re-encoded short payload: %s", got)
	}
}

func TestTruncateListsCutsItemsAndPoints(t *testing.T) {
	var items []any
	var points []any
	for i := 0; i < 205; i++ {
		items = append(items, map[string]any{"n": i})
		points = append(points, []any{"2026-10-13T00:00:00Z", json.Number("9007199254740993")})
	}
	body, err := json.Marshal(map[string]any{
		"items": items,
		"series": []any{
			map[string]any{"name": "usage_percent", "points": points},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, note := truncateLists(body, 200)
	if note != "truncated to 200 items." {
		t.Fatalf("note %q", note)
	}
	var out struct {
		Items  []any `json:"items"`
		Series []struct {
			Points [][]json.RawMessage `json:"points"`
		} `json:"series"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 200 || len(out.Series) != 1 || len(out.Series[0].Points) != 200 {
		t.Fatalf("lens items=%d series=%d points=%d", len(out.Items), len(out.Series), len(out.Series[0].Points))
	}
	if string(out.Series[0].Points[0][1]) != "9007199254740993" {
		t.Fatalf("point re-encoded: %s", out.Series[0].Points[0][1])
	}
}
