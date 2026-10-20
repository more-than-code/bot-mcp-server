package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fixedHostRT sends every request with Host set to host. The connection
// target stays the request URL, which for httptest is the loopback listener.
type fixedHostRT struct {
	base  http.RoundTripper
	host  string
	token string
	log   *tripLog
}

func (a *fixedHostRT) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	clone.Host = a.host
	if a.token != "" {
		clone.Header.Set("Authorization", "Bearer "+a.token)
	}
	base := a.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(clone)
	if resp != nil && a.log != nil {
		a.log.mu.Lock()
		a.log.trips = append(a.log.trips, recordedTrip{status: resp.StatusCode, body: string(body)})
		a.log.mu.Unlock()
	}
	return resp, err
}

func postMCPHost(t *testing.T, srv *httptest.Server, host, token string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header.Clone(), string(raw)
}

func TestMCPHostRefusedBeforeExchange(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, logs := newTestServer(t, fake)

	status, header, body := postMCPHost(t, mcpSrv, "evil.example", userToken)
	if status != http.StatusForbidden {
		t.Fatalf("status %d body %q", status, body)
	}
	if got := header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("content-type %q", got)
	}
	const want = "forbidden: invalid Host header\n"
	if body != want {
		t.Fatalf("body %q", body)
	}
	if strings.Contains(body, "evil.example") {
		t.Fatalf("body echoes host %q", body)
	}
	if fake.exchangeCount() != 0 || fake.count() != 0 {
		t.Fatalf("api calls %d exchanges %d", fake.count(), fake.exchangeCount())
	}
	logged := logs.String()
	if !strings.Contains(logged, "mcp host refused") {
		t.Fatalf("log %q", logged)
	}
	if strings.Contains(logged, "evil.example") || strings.Contains(logged, userToken) {
		t.Fatalf("log contains host or token %q", logged)
	}

	healthReq, err := http.NewRequest(http.MethodGet, mcpSrv.URL+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	healthReq.Host = "evil.example"
	healthResp, err := http.DefaultClient.Do(healthReq)
	if err != nil {
		t.Fatal(err)
	}
	defer healthResp.Body.Close()
	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("health status %d", healthResp.StatusCode)
	}
}

func TestMCPPublicHostOnLoopbackReachesTools(t *testing.T) {
	const publicHost = "mcp.example.test"
	fake := &fakeAPI{token: userToken}
	mcpSrv, _ := newTestServerWithOrigin(t, fake, "https://"+publicHost)
	u, err := url.Parse(mcpSrv.URL)
	if err != nil {
		t.Fatal(err)
	}
	listenHost, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	if listenHost != "127.0.0.1" {
		t.Fatalf("listener %s", mcpSrv.URL)
	}

	log := &tripLog{}
	client := mcp.NewClient(&mcp.Implementation{Name: "bot-mcp-server-test", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: mcpSrv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: &fixedHostRT{
			base:  http.DefaultTransport,
			host:  publicHost,
			token: userToken,
			log:   log,
		}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	listed, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var names []string
	found := false
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.Name == "list_projects" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tools %v", names)
	}
	if fake.exchangeCount() == 0 {
		t.Fatal("public host did not reach token exchange")
	}
	for _, trip := range log.snapshot() {
		if trip.status == http.StatusForbidden {
			t.Fatalf("host refusal status %d", trip.status)
		}
	}

	before := fake.exchangeCount()
	status, header, body := postMCPHost(t, mcpSrv, "evil.example", userToken)
	if status != http.StatusForbidden {
		t.Fatalf("evil status %d body %q", status, body)
	}
	if got := header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("content-type %q", got)
	}
	if body != "forbidden: invalid Host header\n" {
		t.Fatalf("evil body %q", body)
	}
	if strings.Contains(body, "evil.example") {
		t.Fatalf("body echoes host %q", body)
	}
	if fake.exchangeCount() != before {
		t.Fatalf("exchanges %d, want %d", fake.exchangeCount(), before)
	}
}

func TestHostAllowed(t *testing.T) {
	const httpsOrigin = "https://mcp.example.test"
	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{name: "public bare", origin: httpsOrigin, host: "mcp.example.test", want: true},
		{name: "public default port", origin: httpsOrigin, host: "mcp.example.test:443", want: true},
		{name: "public other port", origin: httpsOrigin, host: "mcp.example.test:8443", want: false},
		{name: "uppercase public", origin: httpsOrigin, host: "MCP.EXAMPLE.TEST", want: true},
		{name: "uppercase public with default port", origin: httpsOrigin, host: "MCP.EXAMPLE.TEST:443", want: true},
		{name: "localhost with port", origin: httpsOrigin, host: "localhost:8091", want: true},
		{name: "ipv4 loopback", origin: httpsOrigin, host: "127.0.0.1", want: true},
		{name: "ipv6 loopback with port", origin: httpsOrigin, host: "[::1]:8091", want: true},
		{name: "ipv6 loopback bare", origin: httpsOrigin, host: "[::1]", want: true},
		{name: "empty", origin: httpsOrigin, host: "", want: false},
		{name: "evil", origin: httpsOrigin, host: "evil.example", want: false},
		{name: "localhost suffix", origin: httpsOrigin, host: "localhost.evil.example", want: false},
		{name: "explicit origin port", origin: "https://mcp.example.test:8443", host: "mcp.example.test:8443", want: true},
		{name: "explicit origin port bare refused", origin: "https://mcp.example.test:8443", host: "mcp.example.test", want: false},
		{name: "explicit origin port default refused", origin: "https://mcp.example.test:8443", host: "mcp.example.test:443", want: false},
		{name: "explicit default origin port bare", origin: "https://mcp.example.test:443", host: "mcp.example.test", want: true},
		{name: "explicit default origin port with port", origin: "https://mcp.example.test:443", host: "mcp.example.test:443", want: true},
		{name: "explicit default origin port other refused", origin: "https://mcp.example.test:443", host: "mcp.example.test:8443", want: false},
		{name: "http bare", origin: "http://mcp.example.test", host: "mcp.example.test", want: true},
		{name: "http default port", origin: "http://mcp.example.test", host: "mcp.example.test:80", want: true},
		{name: "http other port", origin: "http://mcp.example.test", host: "mcp.example.test:443", want: false},
		{name: "empty port is not bare", origin: httpsOrigin, host: "mcp.example.test:", want: false},
		{name: "not the whole loopback range", origin: httpsOrigin, host: "127.0.0.2", want: false},
		{name: "evil localhost label", origin: httpsOrigin, host: "evil.localhost", want: false},
		{name: "uppercase origin", origin: "https://MCP.Example.Test", host: "mcp.example.test", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allow, err := newHostAllowlist(tc.origin)
			if err != nil {
				t.Fatal(err)
			}
			if got := hostAllowed(allow, tc.host); got != tc.want {
				t.Fatalf("hostAllowed(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestHostCheckRejectsBadOrigin(t *testing.T) {
	_, err := hostCheck("mcp.example.test", nil, http.NotFoundHandler())
	if err == nil {
		t.Fatal("accepted origin without a scheme")
	}
}
