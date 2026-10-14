// Package auth authenticates one MCP request.
// The caller's bearer token is exchanged for a delegated API token. Only the
// delegated token is placed on the request context. Neither token is logged.
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
)

type delegatedTokenKey struct{}
type rejectKey struct{}

// MaxTokenBytes is the longest Bearer token this server will accept.
// Session tokens are opaque and short; the cap stops a huge header from
// being copied into an exchange or an API call.
const MaxTokenBytes = 4096

// WithDelegatedToken returns a context that carries the delegated API token.
// onReject, when set, is called if that token is rejected by the API.
// The caller's own token is not stored.
func WithDelegatedToken(ctx context.Context, token string, onReject func()) context.Context {
	ctx = context.WithValue(ctx, delegatedTokenKey{}, token)
	if onReject != nil {
		ctx = context.WithValue(ctx, rejectKey{}, onReject)
	}
	return ctx
}

// DelegatedFromContext returns the delegated API token for this request.
// The boolean is false when the context has no delegated token.
func DelegatedFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(delegatedTokenKey{}).(string)
	if !ok || token == "" {
		return "", false
	}
	return token, true
}

// NoteRejected drops the cached delegated token after the API rejects it.
// It does nothing when the context has no exchange cache.
func NoteRejected(ctx context.Context) {
	fn, _ := ctx.Value(rejectKey{}).(func())
	if fn != nil {
		fn()
	}
}

// ParseBearer returns the token from an Authorization header value.
// The scheme must be Bearer. The token bytes after the single space are
// returned unchanged. Empty tokens and tokens with whitespace or controls
// are rejected.
func ParseBearer(header string) (string, bool) {
	const scheme = "Bearer"
	if len(header) < len(scheme)+2 {
		return "", false
	}
	if !strings.EqualFold(header[:len(scheme)], scheme) || header[len(scheme)] != ' ' {
		return "", false
	}
	token := header[len(scheme)+1:]
	if !ValidToken(token) {
		return "", false
	}
	return token, true
}

// ValidToken reports whether token can be forwarded as an HTTP header value
// without changing its bytes.
func ValidToken(token string) bool {
	if token == "" || len(token) > MaxTokenBytes {
		return false
	}
	for _, r := range token {
		if r > unicode.MaxASCII || unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func resourceMetadata(publicOrigin string) string {
	origin := strings.TrimRight(publicOrigin, "/")
	return origin + "/.well-known/oauth-protected-resource"
}

// WWWAuthenticate is the challenge for a missing bearer token.
func WWWAuthenticate(publicOrigin string) string {
	return `Bearer resource_metadata="` + resourceMetadata(publicOrigin) + `"`
}

// WWWAuthenticateInvalidToken is the challenge for a rejected caller token.
func WWWAuthenticateInvalidToken(publicOrigin string) string {
	return `Bearer error="invalid_token", resource_metadata="` + resourceMetadata(publicOrigin) + `"`
}

func writeJSON(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code": code,
		"msg":  msg,
	})
}

// WriteUnauthorized writes the 401 challenge for a missing bearer token.
// It does not echo the header that was sent.
func WriteUnauthorized(w http.ResponseWriter, publicOrigin string) {
	w.Header().Set("WWW-Authenticate", WWWAuthenticate(publicOrigin))
	writeJSON(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
}

// WriteInvalidToken writes the 401 challenge for a caller token the
// authorization server refused to exchange.
func WriteInvalidToken(w http.ResponseWriter, publicOrigin string) {
	w.Header().Set("WWW-Authenticate", WWWAuthenticateInvalidToken(publicOrigin))
	writeJSON(w, http.StatusUnauthorized, "unauthorized", "invalid token")
}

// WriteUnavailable writes 503 when the authorization server cannot be reached.
// There is no WWW-Authenticate challenge, so a client keeps its token.
func WriteUnavailable(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, "unavailable", "authorization server unavailable")
}

type protectedResourceDoc struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ScopesSupported        []string `json:"scopes_supported"`
}

// ProtectedResource serves the OAuth protected-resource metadata document.
// publicOrigin is this MCP server. apiURL is bot-api-server. authorization_servers
// is that URL's origin only (scheme, host, and port).
func ProtectedResource(publicOrigin, apiURL string) http.Handler {
	issuer, issuerErr := apiOrigin(apiURL)
	doc := protectedResourceDoc{
		Resource:               strings.TrimRight(publicOrigin, "/"),
		AuthorizationServers:   []string{issuer},
		BearerMethodsSupported: []string{"header"},
		ScopesSupported:        []string{"deploybot"},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if issuerErr != nil {
			http.Error(w, "misconfigured authorization server", http.StatusInternalServerError)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
}
