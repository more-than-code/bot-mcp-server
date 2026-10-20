package main

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// hostAllowlist is the /mcp Host policy, parsed once from BOT_MCP_PUBLIC_ORIGIN.
type hostAllowlist struct {
	name        string // lowercase host, without brackets or a port
	port        string // set only when the origin names a port
	defaultPort string // "443" or "80"; used when port is empty
}

func newHostAllowlist(publicOrigin string) (hostAllowlist, error) {
	u, err := url.Parse(publicOrigin)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return hostAllowlist{}, errors.New("BOT_MCP_PUBLIC_ORIGIN must be an absolute http(s) URL")
	}
	def := "443"
	if u.Scheme == "http" {
		def = "80"
	}
	port := u.Port()
	if port == def {
		// An explicit default port is the same origin as none.
		port = ""
	}
	return hostAllowlist{
		name:        strings.ToLower(u.Hostname()),
		port:        port,
		defaultPort: def,
	}, nil
}

// hostAllowed reports whether the request Host may reach /mcp.
// The public host matches bare, or with the scheme's default port, unless
// the origin itself names a port: then only that port matches. localhost,
// 127.0.0.1, and ::1 match with any port or none. A name that only contains
// one of those strings does not match.
func hostAllowed(allow hostAllowlist, header string) bool {
	name, port, hasPort, ok := splitHostHeader(header)
	if !ok {
		return false
	}
	name = strings.ToLower(name)
	if isLoopbackHost(name) {
		return true
	}
	if name != allow.name {
		return false
	}
	if allow.port != "" {
		return hasPort && port == allow.port
	}
	return !hasPort || port == allow.defaultPort
}

func isLoopbackHost(name string) bool {
	switch name {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// splitHostHeader splits a Host header into a name and an optional port.
// net.SplitHostPort handles a name, an IPv4 address, or a bracketed IPv6
// address that includes a port. The returned name has no brackets. hasPort
// is true whenever a colon was present, including an empty port.
func splitHostHeader(header string) (name, port string, hasPort, ok bool) {
	if header == "" {
		return "", "", false, false
	}
	if h, p, err := net.SplitHostPort(header); err == nil {
		if h == "" {
			return "", "", false, false
		}
		return h, p, true, true
	}
	if strings.HasPrefix(header, "[") && strings.HasSuffix(header, "]") {
		inner := header[1 : len(header)-1]
		if inner == "" || strings.ContainsAny(inner, "[]") {
			return "", "", false, false
		}
		return inner, "", false, true
	}
	if strings.Contains(header, ":") {
		return "", "", false, false
	}
	return header, "", false, true
}

// hostCheck refuses /mcp unless Host is on the allowlist. It runs before the
// token exchange. The body is fixed and does not include the header value.
func hostCheck(publicOrigin string, logger *slog.Logger, next http.Handler) (http.Handler, error) {
	allow, err := newHostAllowlist(publicOrigin)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hostAllowed(allow, r.Host) {
			next.ServeHTTP(w, r)
			return
		}
		logger.Warn("mcp host refused")
		http.Error(w, "forbidden: invalid Host header", http.StatusForbidden)
	}), nil
}
