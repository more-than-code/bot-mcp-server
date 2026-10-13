// Package auth extracts the caller's Bearer token for one MCP request.
// The token is kept only in the request context. It is never logged here.
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
)

type contextKey struct{}

// MaxTokenBytes is the longest Bearer token this server will accept.
// Session tokens are opaque and short; the cap stops a huge header from
// being copied into the API call.
const MaxTokenBytes = 4096

// WithToken returns a context that carries token.
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, contextKey{}, token)
}

// TokenFromContext returns the Bearer token stored by RequireBearer.
// The boolean is false when the context has no token.
func TokenFromContext(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(contextKey{}).(string)
	if !ok || token == "" {
		return "", false
	}
	return token, true
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

// WWWAuthenticate is the challenge for a missing or invalid token.
func WWWAuthenticate(publicOrigin string) string {
	origin := strings.TrimRight(publicOrigin, "/")
	return `Bearer resource_metadata="` + origin + `/.well-known/oauth-protected-resource"`
}

// WriteUnauthorized writes the 401 challenge. It does not echo the header.
func WriteUnauthorized(w http.ResponseWriter, publicOrigin string) {
	w.Header().Set("WWW-Authenticate", WWWAuthenticate(publicOrigin))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code": "unauthorized",
		"msg":  "missing or invalid bearer token",
	})
}

// RequireBearer rejects requests that lack a Bearer token before next runs.
func RequireBearer(publicOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := ParseBearer(r.Header.Get("Authorization"))
		if !ok {
			WriteUnauthorized(w, publicOrigin)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithToken(r.Context(), token)))
	})
}

type protectedResourceDoc struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

// ProtectedResource serves the OAuth protected-resource metadata document.
// publicOrigin is this MCP server. apiURL is bot-api-server, the authorization server.
func ProtectedResource(publicOrigin, apiURL string) http.Handler {
	doc := protectedResourceDoc{
		Resource:               strings.TrimRight(publicOrigin, "/"),
		AuthorizationServers:   []string{strings.TrimRight(apiURL, "/")},
		BearerMethodsSupported: []string{"header"},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
