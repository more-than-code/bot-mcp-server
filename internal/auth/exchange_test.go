package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExchangeRequestShapeAndCacheHit(t *testing.T) {
	const secret = "a+b/c="
	const subject = "caller.token~1"
	var calls atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("form %v", err)
		}
		if r.URL.Path != "/oauth/token" {
			t.Errorf("path %s", r.URL.Path)
		}
		if strings.Contains(r.URL.Path, "/base/") {
			t.Errorf("token endpoint kept the api path %s", r.URL.Path)
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(r.Header.Get("Authorization"), "Basic "))
		if err != nil {
			t.Errorf("basic %v", err)
		}
		if string(raw) != "bot-mcp-server:a%2Bb%2Fc%3D" {
			t.Errorf("basic %q", raw)
		}
		if strings.Contains(r.Header.Get("Authorization"), subject) {
			t.Error("caller token in authorization")
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Errorf("content-type %s", ct)
		}
		if form.Get("grant_type") != grantExchange || form.Get("subject_token") != subject || form.Get("subject_token_type") != tokenTypeAccess {
			t.Errorf("form %s", body)
		}
		if form.Get("resource") != srv.URL {
			t.Errorf("resource %q", form.Get("resource"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"delegated.token","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":300}`)
	}))
	defer srv.Close()

	ex, err := NewExchanger(srv.URL+"/base", secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ex.Delegated(context.Background(), subject)
	if err != nil || tok != "delegated.token" {
		t.Fatalf("token %q err %v", tok, err)
	}
	tok, err = ex.Delegated(context.Background(), subject)
	if err != nil || tok != "delegated.token" {
		t.Fatalf("cached token %q err %v", tok, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("exchanges %d", got)
	}
}

func TestDelegatedCacheExpiry(t *testing.T) {
	start := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	now := start
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"delegated.token","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":90}`)
	}))
	defer srv.Close()
	ex, err := NewExchanger(srv.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	ex.cache.now = func() time.Time { return now }

	if _, err := ex.Delegated(context.Background(), "caller-token"); err != nil {
		t.Fatal(err)
	}
	now = start.Add(59 * time.Second)
	if _, err := ex.Delegated(context.Background(), "caller-token"); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("exchanges before expiry %d", got)
	}
	now = start.Add(60 * time.Second)
	if _, err := ex.Delegated(context.Background(), "caller-token"); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("exchanges after expiry %d", got)
	}
}

func TestDelegatedWithinSkewIsNotCached(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"delegated.token","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":30}`)
	}))
	defer srv.Close()
	ex, err := NewExchanger(srv.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		tok, err := ex.Delegated(context.Background(), "caller-token")
		if err != nil || tok != "delegated.token" {
			t.Fatalf("token %q err %v", tok, err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("exchanges %d", got)
	}
}

func TestExchangeSingleflight(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"delegated.token","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":300}`)
	}))
	defer srv.Close()
	ex, err := NewExchanger(srv.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	const n = 32
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := ex.Delegated(context.Background(), "caller-token")
			if err != nil {
				errCh <- err
				return
			}
			if tok != "delegated.token" {
				errCh <- errors.New("unexpected delegated token")
			}
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() == 0 {
		close(release)
		wg.Wait()
		t.Fatal("exchange did not start")
	}
	time.Sleep(200 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		close(release)
		wg.Wait()
		t.Fatalf("concurrent exchanges %d", got)
	}
	close(release)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("exchanges %d", got)
	}
}

func TestCacheEvictsOldest(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		subject := form.Get("subject_token")
		mu.Lock()
		calls[subject]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"d-`+subject+`","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":300}`)
	}))
	defer srv.Close()
	ex, err := NewExchanger(srv.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	ex.cache.max = 2
	for _, subject := range []string{"tok-a", "tok-b", "tok-c"} {
		tok, err := ex.Delegated(context.Background(), subject)
		if err != nil || tok != "d-"+subject {
			t.Fatalf("%s token %q err %v", subject, tok, err)
		}
	}
	if _, err := ex.Delegated(context.Background(), "tok-c"); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Delegated(context.Background(), "tok-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Delegated(context.Background(), "tok-a"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["tok-a"] != 2 || calls["tok-b"] != 1 || calls["tok-c"] != 1 {
		t.Fatalf("exchanges %#v", calls)
	}
}

func TestExchangeInvalidToken(t *testing.T) {
	const subject = "caller.token~1"
	const secret = "client-secret"
	var calls atomic.Int32
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		mode := form.Get("grant_type")
		_ = mode
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"rejected `+subject+`"}`)
	}))
	defer srv.Close()
	ex, err := NewExchanger(srv.URL, secret, logger)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	h := ex.RequireDelegated("https://mcp.example/", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+subject)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", rec.Code)
		}
		want := `Bearer error="invalid_token", resource_metadata="https://mcp.example/.well-known/oauth-protected-resource"`
		if got := rec.Header().Get("WWW-Authenticate"); got != want {
			t.Fatalf("WWW-Authenticate %q", got)
		}
		if strings.Contains(rec.Body.String(), subject) || strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("body leaked secret material %s", rec.Body.String())
		}
	}
	if called {
		t.Fatal("MCP handler ran")
	}
	if calls.Load() != 1 {
		t.Fatalf("exchanges %d", calls.Load())
	}
	if strings.Contains(logs.String(), subject) || strings.Contains(logs.String(), secret) {
		t.Fatalf("logs leaked secret material %s", logs.String())
	}
	if !strings.Contains(logs.String(), "invalid_grant") {
		t.Fatalf("logs %s", logs.String())
	}
}

func TestExchangeInvalidRequest(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_request"}`)
	}))
	defer srv.Close()
	ex, err := NewExchanger(srv.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	h := ex.RequireDelegated("https://mcp.example", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next ran")
	}))
	want := `Bearer error="invalid_token", resource_metadata="https://mcp.example/.well-known/oauth-protected-resource"`
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer caller-token")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", rec.Code)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != want {
			t.Fatalf("WWW-Authenticate %q", got)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("exchanges %d", calls.Load())
	}
}

func TestExchangeUnavailable(t *testing.T) {
	cases := []struct {
		name string
		h    http.HandlerFunc
	}{
		{name: "status 500", h: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"server_error"}`)
		}},
		{name: "other oauth error", h: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"unsupported_grant_type"}`)
		}},
		{name: "invalid client", h: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", "Basic")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"invalid_client"}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.h)
			defer srv.Close()
			assertUnavailable(t, srv.URL)
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		u := srv.URL
		srv.Close()
		assertUnavailable(t, u)
	})
}

func assertUnavailable(t *testing.T, apiURL string) {
	t.Helper()
	ex, err := NewExchanger(apiURL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer caller-token")
	ex.RequireDelegated("https://mcp.example", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next ran")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("WWW-Authenticate %q", rec.Header().Get("WWW-Authenticate"))
	}
}

func TestRejectDropsCacheAndStripsCaller(t *testing.T) {
	const subject = "caller.token~1"
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"delegated.token","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":300}`)
	}))
	defer srv.Close()
	ex, err := NewExchanger(srv.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	h := ex.RequireDelegated("https://mcp.example", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("caller authorization reached the handler")
		}
		tok, ok := DelegatedFromContext(r.Context())
		if !ok || tok != "delegated.token" {
			t.Errorf("delegated %q ok %v", tok, ok)
		}
		seen = append(seen, tok)
		if len(seen) == 1 {
			NoteRejected(r.Context())
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("body"))
		req.Header.Set("Authorization", "Bearer "+subject)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status %d", rec.Code)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("exchanges %d", calls.Load())
	}
}

func TestMissingBearerStillUnauthorized(t *testing.T) {
	ex, err := NewExchanger("https://api.example", "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	ex.RequireDelegated("https://mcp.example/", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next ran")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
	want := `Bearer resource_metadata="https://mcp.example/.well-known/oauth-protected-resource"`
	if got := rec.Header().Get("WWW-Authenticate"); got != want {
		t.Fatalf("WWW-Authenticate %q", got)
	}
}

func TestDistinctSubjectsDoNotCross(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		subject := form.Get("subject_token")
		calls.Add(1)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"delegated-`+subject+`","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":300}`)
	}))
	defer srv.Close()
	ex, err := NewExchanger(srv.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	subjects := []string{"subject-aaa", "subject-bbb"}
	var wg sync.WaitGroup
	got := make([]string, len(subjects))
	errs := make([]error, len(subjects))
	for i, subject := range subjects {
		wg.Add(1)
		go func(i int, subject string) {
			defer wg.Done()
			got[i], errs[i] = ex.Delegated(context.Background(), subject)
		}(i, subject)
	}
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 2 {
		close(release)
		wg.Wait()
		t.Fatalf("exchanges %d", calls.Load())
	}
	close(release)
	wg.Wait()
	for i, subject := range subjects {
		if errs[i] != nil || got[i] != "delegated-"+subject {
			t.Errorf("%s token %q err %v", subject, got[i], errs[i])
		}
	}
}

func TestInflightCapReturnsUnavailable(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"delegated.token","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":300}`)
	}))
	defer srv.Close()
	ex, err := NewExchanger(srv.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	ex.cache.maxFlight = 1
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = ex.Delegated(context.Background(), "subject-aaa")
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(release)
		<-done
		t.Fatal("exchange did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer subject-bbb")
	ex.RequireDelegated("https://mcp.example", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next ran")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("WWW-Authenticate %q", rec.Header().Get("WWW-Authenticate"))
	}
	if calls.Load() != 1 {
		t.Fatalf("exchanges %d", calls.Load())
	}
	close(release)
	<-done
}

func TestDenialRememberedThenExpires(t *testing.T) {
	start := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	now := start
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	}))
	defer srv.Close()
	ex, err := NewExchanger(srv.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	ex.cache.now = func() time.Time { return now }
	h := ex.RequireDelegated("https://mcp.example", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next ran")
	}))
	want := `Bearer error="invalid_token", resource_metadata="https://mcp.example/.well-known/oauth-protected-resource"`
	ask := func() {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer caller-token")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", rec.Code)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != want {
			t.Fatalf("WWW-Authenticate %q", got)
		}
	}
	ask()
	now = start.Add(59 * time.Second)
	ask()
	if got := calls.Load(); got != 1 {
		t.Fatalf("exchanges inside the denial window %d", got)
	}
	now = start.Add(60 * time.Second)
	ask()
	if got := calls.Load(); got != 2 {
		t.Fatalf("exchanges after the denial window %d", got)
	}
}

func TestServerErrorIsNotDenied(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	}))
	defer srv.Close()
	ex, err := NewExchanger(srv.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, err := ex.Delegated(context.Background(), "caller-token")
		if !errors.Is(err, errUnavailable) {
			t.Fatalf("err %v", err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("exchanges %d", got)
	}
}

func TestCancelledWaiterReturnsAndOthersFinish(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"delegated.token","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":300}`)
	}))
	defer srv.Close()
	closed := false
	closeRelease := func() {
		if !closed {
			close(release)
			closed = true
		}
	}
	defer closeRelease()
	ex, err := NewExchanger(srv.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "caller-token"
	key := cacheKey(subject)
	type gotTok struct {
		tok string
		err error
	}
	leaderCh := make(chan gotTok, 1)
	go func() {
		tok, err := ex.Delegated(context.Background(), subject)
		leaderCh <- gotTok{tok, err}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("exchange did not start")
	}
	otherCh := make(chan gotTok, 1)
	go func() {
		tok, err := ex.Delegated(context.Background(), subject)
		otherCh <- gotTok{tok, err}
	}()
	if !waitForWaiters(t, ex, key, 1) {
		t.Fatal("other waiter did not join")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	httpDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+subject)
		ex.RequireDelegated("https://mcp.example", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("next ran")
		})).ServeHTTP(rec, req)
		httpDone <- rec
	}()
	if !waitForWaiters(t, ex, key, 2) {
		t.Fatal("cancelled waiter did not join")
	}
	cancel()
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-httpDone:
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter blocked on the exchange")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("WWW-Authenticate %q", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("exchanges while blocked %d", calls.Load())
	}
	closeRelease()
	for _, ch := range []chan gotTok{leaderCh, otherCh} {
		select {
		case got := <-ch:
			if got.err != nil || got.tok != "delegated.token" {
				t.Fatalf("token %q err %v", got.tok, got.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("waiter did not finish")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("exchanges %d", calls.Load())
	}
}

func waitForWaiters(t *testing.T, ex *Exchanger, key [32]byte, n int) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ex.cache.mu.Lock()
		f := ex.cache.inflight[key]
		got := 0
		if f != nil {
			got = f.waiters
		}
		ex.cache.mu.Unlock()
		if got >= n {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}
