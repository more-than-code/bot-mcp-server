package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestRPCMethod(t *testing.T) {
	cases := []struct {
		body, rpc, tool string
	}{
		{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, "initialize", ""},
		{`{"jsonrpc":"2.0","method":"notifications/initialized"}`, "notifications/initialized", ""},
		{`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"confirm","arguments":{"confirmation_id":"c1"}}}`, "tools/call", "confirm"},
		{`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"bad name\nlevel=ERROR"}}`, "tools/call", "other"},
		{`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ADMIN.LOGIN.OK"}}`, "tools/call", "other"},
		{`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":[1]}`, "tools/call", "other"},
		{`{"jsonrpc":"2.0","id":4,"method":"subscriptions/listen"}`, "subscriptions/listen", ""},
		{`{"jsonrpc":"2.0","id":4,"method":"server/discover"}`, "server/discover", ""},
		{`{"jsonrpc":"2.0","id":4,"method":"notifications/roots/list_changed"}`, "notifications/roots/list_changed", ""},
		{`{"jsonrpc":"2.0","id":4,"method":"Admin.Login OK"}`, "other", ""},
		{`{"jsonrpc":"2.0","id":4,"method":"a/b/c/d/e"}`, "other", ""},
		{`{"jsonrpc":"2.0","id":4,"method":"x\ny"}`, "other", ""},
		{`{"jsonrpc":"2.0","id":5,"result":{}}`, "response", ""},
		{`{"jsonrpc":"2.0","id":5,"error":{"code":1}}`, "response", ""},
		{`{"foo":1}`, "other", ""},
		{` [{"method":"ping"}]`, "batch", ""},
		{`not json`, "other", ""},
	}
	for _, c := range cases {
		rpc, tool := rpcMethod([]byte(c.body))
		if rpc != c.rpc || tool != c.tool {
			t.Errorf("rpcMethod(%q) = %q, %q; want %q, %q", c.body, rpc, tool, c.rpc, c.tool)
		}
	}
}

func accessLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// logKeys returns the attribute keys of one text-handler line.
func logKeys(line string) []string {
	var keys []string
	for _, field := range strings.Fields(line) {
		if k, _, ok := strings.Cut(field, "="); ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func readAllHandler(status int, got *[]byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if got != nil {
			*got = b
		}
		if status != 0 {
			w.WriteHeader(status)
		}
	})
}

func TestAccessLogKeepsBodyAndLogsNoArguments(t *testing.T) {
	logger, buf := accessLogger()
	var got []byte
	h := withAccessLog(logger, readAllHandler(http.StatusAccepted, &got))
	body := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"deploy_entry","arguments":{"entry":"secret-arg-value"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer token-value-xyz")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if string(got) != body {
		t.Fatalf("handler body = %q", got)
	}
	line := buf.String()
	for _, want := range []string{"msg=request", "method=POST", "status=202", "rpc=tools/call", "tool=deploy_entry"} {
		if !strings.Contains(line, want) {
			t.Errorf("log %q lacks %q", line, want)
		}
	}
	if keys := strings.Join(logKeys(line), ","); keys != "level,method,ms,msg,rpc,status,time,tool" {
		t.Errorf("log keys %s", keys)
	}
	for _, bad := range []string{"secret-arg-value", "token-value-xyz"} {
		if strings.Contains(line, bad) {
			t.Errorf("log %q contains %q", line, bad)
		}
	}
}

func TestAccessLogPeekLimitBoundary(t *testing.T) {
	head := `{"method":"ping","pad":"`
	tail := `"}`
	for _, c := range []struct {
		size    int
		logsRPC bool
	}{{accessPeekLimit, true}, {accessPeekLimit + 1, false}} {
		logger, buf := accessLogger()
		var got []byte
		body := head + strings.Repeat("a", c.size-len(head)-len(tail)) + tail
		withAccessLog(logger, readAllHandler(0, &got)).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))
		if string(got) != body {
			t.Fatalf("size %d: handler read %d bytes", c.size, len(got))
		}
		line := buf.String()
		if strings.Contains(line, "rpc=ping") != c.logsRPC || !strings.Contains(line, "status=200") {
			t.Fatalf("size %d: log %q", c.size, line)
		}
	}
}

// A body the handler never reads is never read by the log either.
func TestAccessLogDoesNotReadUnreadBody(t *testing.T) {
	logger, buf := accessLogger()
	pr, pw := io.Pipe()
	defer pw.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h := withAccessLog(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "forbidden", http.StatusForbidden)
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", pr))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("access log blocked on a body the handler did not read")
	}
	if line := buf.String(); !strings.Contains(line, "status=403") || strings.Contains(line, "rpc=") {
		t.Fatalf("log %q", line)
	}
}

func TestAccessLogMethodsAndDefaults(t *testing.T) {
	for _, c := range []struct {
		method, want string
	}{{http.MethodGet, "method=GET"}, {http.MethodDelete, "method=DELETE"}, {"BREW", "method=other"}} {
		logger, buf := accessLogger()
		req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
		req.Method = c.method
		withAccessLog(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), req)
		line := buf.String()
		if !strings.Contains(line, c.want) || !strings.Contains(line, "status=200") || strings.Contains(line, "rpc=") {
			t.Fatalf("%s: log %q", c.method, line)
		}
	}
}

func TestAccessLogClosesOriginalBody(t *testing.T) {
	logger, _ := accessLogger()
	body := &countingCloser{Reader: strings.NewReader(`{"method":"ping"}`)}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Body = body
	withAccessLog(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body.Close()
	})).ServeHTTP(httptest.NewRecorder(), req)
	if body.closed != 1 {
		t.Fatalf("closed %d times", body.closed)
	}
}

type countingCloser struct {
	io.Reader
	closed int
}

func (c *countingCloser) Close() error { c.closed++; return nil }

func TestAccessLogPanicStillLogs(t *testing.T) {
	logger, buf := accessLogger()
	h := withAccessLog(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	func() {
		defer func() {
			if rec := recover(); rec != http.ErrAbortHandler {
				t.Fatalf("recovered %v", rec)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`)))
	}()
	if line := buf.String(); !strings.Contains(line, "aborted=true") {
		t.Fatalf("log %q", line)
	}
}

// A client that disconnects while the handler runs is flagged.
func TestAccessLogClientClosed(t *testing.T) {
	buf := &safeBuf{}
	logger := slog.New(slog.NewTextHandler(buf, nil))
	entered := make(chan struct{})
	srv := httptest.NewServer(withAccessLog(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		close(entered)
		<-r.Context().Done()
	})))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(`{"method":"ping"}`))
	go func() { <-entered; cancel() }()
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(buf.String(), "client_closed=true") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if line := buf.String(); !strings.Contains(line, "client_closed=true") || !strings.Contains(line, "rpc=ping") {
		t.Fatalf("log %q", line)
	}
}

// A request that outlives the server ReadTimeout is not mistaken for a
// client close (the stdlib clears the read deadline before watching for EOF).
func TestAccessLogLongRequestNotClientClosed(t *testing.T) {
	logger, buf := accessLogger()
	srv := httptest.NewUnstartedServer(withAccessLog(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		time.Sleep(600 * time.Millisecond)
	})))
	srv.Config.ReadTimeout = 200 * time.Millisecond
	srv.Start()
	defer srv.Close()
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if line := buf.String(); strings.Contains(line, "client_closed") || !strings.Contains(line, "status=200") {
		t.Fatalf("log %q", line)
	}
}

// withWriteBudget relies on http.ResponseController reaching the real writer.
func TestAccessLogKeepsResponseController(t *testing.T) {
	logger, _ := accessLogger()
	errc := make(chan error, 1)
	srv := httptest.NewServer(withAccessLog(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errc <- http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
	})))
	defer srv.Close()
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if err := <-errc; err != nil {
		t.Fatalf("SetWriteDeadline through the access log: %v", err)
	}
}

// Through the full handler: refusals are logged with their status and no
// rpc; an accepted request reaches the SDK with its body and logs the rpc.
func TestAccessLogThroughHandler(t *testing.T) {
	fake := &fakeAPI{token: userToken}
	mcpSrv, logs := newTestServer(t, fake)

	if status, _, _ := postMCPHost(t, mcpSrv, "evil.example", userToken); status != http.StatusForbidden {
		t.Fatalf("wrong host status %d", status)
	}
	if status, _, _ := postMCPHost(t, mcpSrv, "", ""); status != http.StatusUnauthorized {
		t.Fatalf("no token status %d", status)
	}
	resp, body := postMCP(t, mcpSrv.URL, userToken)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status %d body %s", resp.StatusCode, body)
	}

	var lines []string
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, "msg=request") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("request lines %q", lines)
	}
	if !strings.Contains(lines[0], "status=403") || strings.Contains(lines[0], "rpc=") {
		t.Errorf("wrong host line %q", lines[0])
	}
	if !strings.Contains(lines[1], "status=401") || strings.Contains(lines[1], "rpc=") {
		t.Errorf("no token line %q", lines[1])
	}
	if !strings.Contains(lines[2], "status=200") || !strings.Contains(lines[2], "rpc=initialize") {
		t.Errorf("initialize line %q", lines[2])
	}
	if strings.Contains(logs.String(), userToken) {
		t.Error("log contains the token")
	}
}
