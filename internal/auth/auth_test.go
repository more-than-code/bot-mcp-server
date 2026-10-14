package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

func TestWWWAuthenticateInvalidToken(t *testing.T) {
	got := WWWAuthenticateInvalidToken("https://mcp.example/")
	want := `Bearer error="invalid_token", resource_metadata="https://mcp.example/.well-known/oauth-protected-resource"`
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestProtectedResourceIssuerIsOrigin(t *testing.T) {
	h := ProtectedResource("https://mcp.example/", "https://api.example:8443/base?q=1#frag")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var doc protectedResourceDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != "https://api.example:8443" {
		t.Fatalf("authorization_servers %#v", doc.AuthorizationServers)
	}
	if doc.Resource != "https://mcp.example" {
		t.Fatalf("resource %q", doc.Resource)
	}
}
