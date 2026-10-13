package auth

import "testing"

func TestParseBearer(t *testing.T) {
	token := "tok.with~special_chars"
	got, ok := ParseBearer("Bearer " + token)
	if !ok || got != token {
		t.Fatalf("got %q %v", got, ok)
	}
	got, ok = ParseBearer("bearer " + token)
	if !ok || got != token {
		t.Fatalf("scheme case: got %q %v", got, ok)
	}
	for _, header := range []string{"", "Bearer", "Bearer ", "Bearer a b", "Basic " + token, "Bearer " + token + "\n"} {
		if _, ok := ParseBearer(header); ok {
			t.Fatalf("accepted %q", header)
		}
	}
}

func TestWWWAuthenticate(t *testing.T) {
	got := WWWAuthenticate("https://mcp.example/")
	want := `Bearer resource_metadata="https://mcp.example/.well-known/oauth-protected-resource"`
	if got != want {
		t.Fatalf("got %s", got)
	}
}
