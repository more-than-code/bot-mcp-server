package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	deviceSecret = "device-secret-do-not-print"
	sessionToken = "session-token.abc~def"
	userCode     = "ABCD-EFGH"
	verifyURL    = "https://console.example/device?code=ABCD-EFGH"
)

func TestLoginDeviceFlow(t *testing.T) {
	fake := &deviceAPI{}
	home, srv := newSession(t, fake)

	var sleeps []time.Duration
	var stdout, stderr strings.Builder
	code := run([]string{"login", "--api", srv.URL}, &stdout, &stderr, func(d time.Duration) {
		sleeps = append(sleeps, d)
	}, nil)
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	wantSleep := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second}
	if len(sleeps) != len(wantSleep) {
		t.Fatalf("sleeps %v", sleeps)
	}
	for i := range wantSleep {
		if sleeps[i] != wantSleep[i] {
			t.Fatalf("sleeps %v", sleeps)
		}
	}
	out := stdout.String()
	if !strings.Contains(out, "verification_uri_complete: "+verifyURL+"\n") {
		t.Fatalf("stdout %q", out)
	}
	if !strings.Contains(out, "user_code: "+userCode+"\n") {
		t.Fatalf("stdout %q", out)
	}
	if strings.Contains(out, deviceSecret) || strings.Contains(stderr.String(), deviceSecret) {
		t.Fatal("device code was printed")
	}
	if !strings.Contains(out, "saved token to ~/.config/deploybot/token\n") {
		t.Fatalf("stdout %q", out)
	}
	if fake.clientName != "deploybot" {
		t.Fatalf("client_name %q", fake.clientName)
	}
	for _, header := range fake.auths {
		if header != "" {
			t.Fatal("device flow sent an Authorization header")
		}
	}
	if fake.polls != 3 {
		t.Fatalf("polls %d", fake.polls)
	}

	path := filepath.Join(home, ".config", "deploybot", "token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %o", dirInfo.Mode().Perm())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(tokenFile{API: srv.URL, Token: sessionToken})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("file %s", got)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "token" {
		t.Fatalf("dir entries %#v", entries)
	}
}

func TestLoginPrintDoesNotSave(t *testing.T) {
	fake := &deviceAPI{}
	home, srv := newSession(t, fake)
	var stdout, stderr strings.Builder
	code := run([]string{"login", "--api", srv.URL, "--print"}, &stdout, &stderr, func(time.Duration) {}, nil)
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), sessionToken+"\n") {
		t.Fatal("token was not printed")
	}
	if strings.Contains(stdout.String(), deviceSecret) {
		t.Fatal("device code was printed")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "deploybot", "token")); !os.IsNotExist(err) {
		t.Fatalf("token file err %v", err)
	}
}

func TestLoginExpiredToken(t *testing.T) {
	fake := &deviceAPI{pollMode: "expired_token"}
	home, srv := newSession(t, fake)
	var stdout, stderr strings.Builder
	var sleeps []time.Duration
	code := run([]string{"login", "--api", srv.URL}, &stdout, &stderr, func(d time.Duration) {
		sleeps = append(sleeps, d)
	}, nil)
	if code != 2 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "expired_token:") {
		t.Fatalf("stderr %q", stderr.String())
	}
	if len(sleeps) != 1 || sleeps[0] != 5*time.Second {
		t.Fatalf("sleeps %v", sleeps)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "deploybot", "token")); !os.IsNotExist(err) {
		t.Fatalf("token file err %v", err)
	}
}

func TestLoginAccessDenied(t *testing.T) {
	fake := &deviceAPI{pollMode: "access_denied"}
	home, srv := newSession(t, fake)
	var stdout, stderr strings.Builder
	code := run([]string{"login", "--api", srv.URL}, &stdout, &stderr, func(time.Duration) {}, nil)
	if code != 3 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "access_denied:") {
		t.Fatalf("stderr %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "deploybot", "token")); !os.IsNotExist(err) {
		t.Fatalf("token file err %v", err)
	}
}

func TestLoginLocalDeadline(t *testing.T) {
	fake := &deviceAPI{pollMode: "pending"}
	home, srv := newSession(t, fake)
	start := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	calls := 0
	now := func() time.Time {
		calls++
		if calls == 1 {
			return start
		}
		return start.Add(2 * time.Hour)
	}
	var sleeps []time.Duration
	var stdout, stderr strings.Builder
	code := run([]string{"login", "--api", srv.URL}, &stdout, &stderr, func(d time.Duration) {
		sleeps = append(sleeps, d)
	}, now)
	if code != 2 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if stderr.String() != "expired_token: device code expired\n" {
		t.Fatalf("stderr %q", stderr.String())
	}
	if len(sleeps) != 1 || sleeps[0] != 5*time.Second {
		t.Fatalf("sleeps %v", sleeps)
	}
	if fake.polls != 0 {
		t.Fatalf("polls %d", fake.polls)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "deploybot", "token")); !os.IsNotExist(err) {
		t.Fatalf("token file err %v", err)
	}
}

func TestLoginIntervalFloor(t *testing.T) {
	fake := &deviceAPI{pollMode: "immediate", useTiming: true, expiresIn: 600, interval: 0}
	_, srv := newSession(t, fake)
	var sleeps []time.Duration
	var stdout, stderr strings.Builder
	code := run([]string{"login", "--api", srv.URL, "--print"}, &stdout, &stderr, func(d time.Duration) {
		sleeps = append(sleeps, d)
	}, nil)
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if len(sleeps) != 1 || sleeps[0] != time.Second {
		t.Fatalf("sleeps %v", sleeps)
	}
	if fake.polls != 1 {
		t.Fatalf("polls %d", fake.polls)
	}
}

func TestLoginPollCap(t *testing.T) {
	fake := &deviceAPI{pollMode: "pending", useTiming: true, expiresIn: 3, interval: 1}
	_, srv := newSession(t, fake)
	frozen := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	var sleeps []time.Duration
	var stdout, stderr strings.Builder
	code := run([]string{"login", "--api", srv.URL}, &stdout, &stderr, func(d time.Duration) {
		sleeps = append(sleeps, d)
	}, func() time.Time { return frozen })
	if code != 2 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if stderr.String() != "expired_token: device code expired\n" {
		t.Fatalf("stderr %q", stderr.String())
	}
	if fake.polls != 8 {
		t.Fatalf("polls %d", fake.polls)
	}
	if len(sleeps) != 9 {
		t.Fatalf("sleeps %v", sleeps)
	}
	for _, slept := range sleeps {
		if slept != time.Second {
			t.Fatalf("sleeps %v", sleeps)
		}
	}
}

func TestLoginSaveFailureSignsOut(t *testing.T) {
	fake := &deviceAPI{pollMode: "immediate"}
	home, srv := newSession(t, fake)
	if err := os.WriteFile(filepath.Join(home, ".config"), []byte("not-a-directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := run([]string{"login", "--api", srv.URL}, &stdout, &stderr, func(time.Duration) {}, nil)
	if code != 1 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if stderr.String() != "could not save token\n" {
		t.Fatalf("stderr %q", stderr.String())
	}
	if strings.Contains(stdout.String(), sessionToken) || strings.Contains(stderr.String(), sessionToken) {
		t.Fatal("token appeared in login output")
	}
	if len(fake.signoutAuth) != 1 || fake.signoutAuth[0] != "Bearer "+sessionToken {
		t.Fatalf("signout auth %v", fake.signoutAuth)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "deploybot", "token")); err == nil {
		t.Fatal("token file was created")
	}
}

func TestLoginRejectsCleartextHTTP(t *testing.T) {
	var stdout, stderr strings.Builder
	code := run([]string{"login", "--api", "http://example.com"}, &stdout, &stderr, func(time.Duration) {}, nil)
	if code != 1 || !strings.Contains(stderr.String(), "https") {
		t.Fatalf("exit %d stderr %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"login", "--api", "http://user:secret@example.com", "--insecure-http"}, &stdout, &stderr, func(time.Duration) {}, nil)
	if code != 1 || !strings.Contains(stderr.String(), "user info") {
		t.Fatalf("exit %d stderr %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "secret") || strings.Contains(stdout.String(), "secret") {
		t.Fatal("userinfo was printed")
	}
}

func TestLogout(t *testing.T) {
	cases := []struct {
		name     string
		signout  string
		wantExit int
		wantErr  string
		keep     bool
	}{
		{name: "ok", wantExit: 0},
		{name: "unauthorized", signout: "unauthorized", wantExit: 0},
		{name: "failure", signout: "error", wantExit: 1, wantErr: "signout failed; token kept, retry later or use --force", keep: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &deviceAPI{}
			home, srv := newSession(t, fake)
			var stdout, stderr strings.Builder
			if code := run([]string{"login", "--api", srv.URL}, &stdout, &stderr, func(time.Duration) {}, nil); code != 0 {
				t.Fatalf("login exit %d %s", code, stderr.String())
			}
			fake.setSignout(tc.signout)
			stdout.Reset()
			stderr.Reset()
			code := run([]string{"logout", "--api", srv.URL}, &stdout, &stderr, nil, nil)
			if code != tc.wantExit {
				t.Fatalf("exit %d stderr %s", code, stderr.String())
			}
			if tc.wantExit == 0 && stdout.String() != "logged out\n" {
				t.Fatalf("stdout %q", stdout.String())
			}
			if tc.wantErr != "" && stderr.String() != tc.wantErr+"\n" {
				t.Fatalf("stderr %q", stderr.String())
			}
			if strings.Contains(stderr.String(), sessionToken) || strings.Contains(stdout.String(), sessionToken) {
				t.Fatal("token appeared in logout output")
			}
			_, err := os.Stat(filepath.Join(home, ".config", "deploybot", "token"))
			if tc.keep {
				if err != nil {
					t.Fatal(err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("token file err %v", err)
			}
			if len(fake.signoutAuth) != 1 || fake.signoutAuth[0] != "Bearer "+sessionToken {
				t.Fatal("signout authorization was not the saved token")
			}
		})
	}
}

func TestLogoutForce(t *testing.T) {
	fake := &deviceAPI{}
	home, srv := newSession(t, fake)
	var stdout, stderr strings.Builder
	if code := run([]string{"login", "--api", srv.URL}, &stdout, &stderr, func(time.Duration) {}, nil); code != 0 {
		t.Fatalf("login exit %d %s", code, stderr.String())
	}
	fake.setSignout("error")
	stdout.Reset()
	stderr.Reset()
	code := run([]string{"logout", "--force"}, &stdout, &stderr, nil, nil)
	if code != 0 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if stdout.String() != "logged out\n" {
		t.Fatalf("stdout %q", stdout.String())
	}
	if strings.Contains(stdout.String(), sessionToken) || strings.Contains(stderr.String(), sessionToken) {
		t.Fatal("token appeared in logout output")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "deploybot", "token")); !os.IsNotExist(err) {
		t.Fatalf("token file err %v", err)
	}
	if len(fake.signoutAuth) != 1 || fake.signoutAuth[0] != "Bearer "+sessionToken {
		t.Fatalf("signout auth %v", fake.signoutAuth)
	}
}

func TestLogoutUsesSavedIssuer(t *testing.T) {
	fake := &deviceAPI{}
	home, srv := newSession(t, fake)
	var stdout, stderr strings.Builder
	if code := run([]string{"login", "--api", srv.URL}, &stdout, &stderr, func(time.Duration) {}, nil); code != 0 {
		t.Fatalf("login exit %d %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code := run([]string{"logout"}, &stdout, &stderr, nil, nil)
	if code != 0 || stdout.String() != "logged out\n" {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if len(fake.signoutAuth) != 1 || fake.signoutAuth[0] != "Bearer "+sessionToken {
		t.Fatalf("signout auth %v", fake.signoutAuth)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "deploybot", "token")); !os.IsNotExist(err) {
		t.Fatalf("token file err %v", err)
	}
}

func TestLogoutIssuerMismatchSendsNothing(t *testing.T) {
	cases := []struct {
		name string
		args func(srvURL string) []string
		env  string
	}{
		{name: "flag", args: func(string) []string { return []string{"logout", "--api", "https://other.example"} }},
		{name: "env", args: func(string) []string { return []string{"logout"} }, env: "https://other.example"},
		{name: "scheme case", args: func(srvURL string) []string {
			return []string{"logout", "--api", strings.Replace(srvURL, "http://", "HTTP://", 1)}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &deviceAPI{}
			home, srv := newSession(t, fake)
			var stdout, stderr strings.Builder
			if code := run([]string{"login", "--api", srv.URL}, &stdout, &stderr, func(time.Duration) {}, nil); code != 0 {
				t.Fatalf("login exit %d %s", code, stderr.String())
			}
			if tc.env != "" {
				t.Setenv("DEPLOYBOT_API_URL", tc.env)
			}
			stdout.Reset()
			stderr.Reset()
			code := run(tc.args(srv.URL), &stdout, &stderr, nil, nil)
			if code != 1 {
				t.Fatalf("exit %d stderr %s", code, stderr.String())
			}
			if stderr.String() != "api url does not match the saved issuer\n" {
				t.Fatalf("stderr %q", stderr.String())
			}
			if stdout.String() != "" {
				t.Fatalf("stdout %q", stdout.String())
			}
			if len(fake.signoutAuth) != 0 {
				t.Fatalf("signout was sent: %v", fake.signoutAuth)
			}
			if _, err := os.Stat(filepath.Join(home, ".config", "deploybot", "token")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLogoutTrailingSlashMatchesSavedIssuer(t *testing.T) {
	fake := &deviceAPI{}
	_, srv := newSession(t, fake)
	var stdout, stderr strings.Builder
	if code := run([]string{"login", "--api", srv.URL}, &stdout, &stderr, func(time.Duration) {}, nil); code != 0 {
		t.Fatalf("login exit %d %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code := run([]string{"logout", "--api", srv.URL + "/"}, &stdout, &stderr, nil, nil)
	if code != 0 || stdout.String() != "logged out\n" {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if len(fake.signoutAuth) != 1 || fake.signoutAuth[0] != "Bearer "+sessionToken {
		t.Fatalf("signout auth %v", fake.signoutAuth)
	}
}

func TestLogoutForceMismatchDeletesWithoutSending(t *testing.T) {
	fake := &deviceAPI{}
	home, srv := newSession(t, fake)
	var stdout, stderr strings.Builder
	if code := run([]string{"login", "--api", srv.URL}, &stdout, &stderr, func(time.Duration) {}, nil); code != 0 {
		t.Fatalf("login exit %d %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code := run([]string{"logout", "--api", "https://other.example", "--force"}, &stdout, &stderr, nil, nil)
	if code != 0 || stdout.String() != "logged out\n" {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if len(fake.signoutAuth) != 0 {
		t.Fatalf("signout was sent: %v", fake.signoutAuth)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "deploybot", "token")); !os.IsNotExist(err) {
		t.Fatalf("token file err %v", err)
	}
}

func TestLogoutLegacyRequiresExplicitAPI(t *testing.T) {
	fake := &deviceAPI{}
	home, srv := newSession(t, fake)
	path := writeTokenBytes(t, home, []byte(sessionToken))
	t.Setenv("DEPLOYBOT_API_URL", srv.URL)
	var stdout, stderr strings.Builder
	code := run([]string{"logout"}, &stdout, &stderr, nil, nil)
	if code != 1 {
		t.Fatalf("exit %d stderr %s", code, stderr.String())
	}
	if stderr.String() != "token file has no issuer; pass --api\n" {
		t.Fatalf("stderr %q", stderr.String())
	}
	if len(fake.signoutAuth) != 0 {
		t.Fatalf("signout was sent: %v", fake.signoutAuth)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"logout", "--api", srv.URL}, &stdout, &stderr, nil, nil)
	if code != 0 || stdout.String() != "logged out\n" {
		t.Fatalf("exit %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	if len(fake.signoutAuth) != 1 || fake.signoutAuth[0] != "Bearer "+sessionToken {
		t.Fatalf("signout auth %v", fake.signoutAuth)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file err %v", err)
	}
}

func TestLogoutRejectsSavedCleartext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEPLOYBOT_API_URL", "")
	body, err := json.Marshal(tokenFile{API: "http://example.com", Token: sessionToken})
	if err != nil {
		t.Fatal(err)
	}
	path := writeTokenBytes(t, home, body)
	var stdout, stderr strings.Builder
	code := run([]string{"logout"}, &stdout, &stderr, nil, nil)
	if code != 1 || !strings.Contains(stderr.String(), "https") {
		t.Fatalf("exit %d stderr %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), sessionToken) || strings.Contains(stdout.String(), sessionToken) {
		t.Fatal("token appeared in logout output")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestSameIssuer(t *testing.T) {
	if !sameIssuer("https://api.example/", "https://api.example") {
		t.Fatal("trailing slash")
	}
	if !sameIssuer("https://api.example//", "https://api.example") {
		t.Fatal("repeated trailing slash")
	}
	if sameIssuer("https://API.example", "https://api.example") {
		t.Fatal("host case was folded")
	}
	if sameIssuer("https://api.example/v1", "https://api.example") {
		t.Fatal("path was ignored")
	}
}

func newSession(t *testing.T, fake *deviceAPI) (home string, srv *httptest.Server) {
	t.Helper()
	t.Cleanup(fake.check(t))
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEPLOYBOT_API_URL", "")
	srv = httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return home, srv
}

func writeTokenBytes(t *testing.T, home string, body []byte) string {
	t.Helper()
	dir := filepath.Join(home, ".config", "deploybot")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type deviceAPI struct {
	mu          sync.Mutex
	polls       int
	clientName  string
	auths       []string
	signoutAuth []string
	pollMode    string
	signout     string
	useTiming   bool
	expiresIn   int
	interval    int
	problems    []string
}

func (f *deviceAPI) setSignout(code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signout = code
}

func (f *deviceAPI) check(t *testing.T) func() {
	return func() {
		t.Helper()
		f.mu.Lock()
		problems := append([]string(nil), f.problems...)
		f.mu.Unlock()
		for _, problem := range problems {
			t.Error(problem)
		}
	}
}

func (f *deviceAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, key := range headerProblems(r.Header, len(body) > 0) {
		f.problems = append(f.problems, "unexpected header "+key+" on "+r.Method+" "+r.URL.Path)
	}
	switch r.URL.Path {
	case "/v1/auth/device/code":
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		var in struct {
			ClientName string `json:"client_name"`
		}
		_ = json.Unmarshal(body, &in)
		f.clientName = in.ClientName
		expires := 600
		interval := 5
		if f.useTiming {
			expires = f.expiresIn
			interval = f.interval
		}
		writeEnv(w, http.StatusOK, "ok", "", map[string]any{
			"device_code":               deviceSecret,
			"user_code":                 userCode,
			"verification_uri":          "https://console.example/device",
			"verification_uri_complete": verifyURL,
			"expires_in":                expires,
			"interval":                  interval,
		})
	case "/v1/auth/device/token":
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		if r.Method != http.MethodPost {
			f.problems = append(f.problems, "device token method "+r.Method)
		}
		wantBody := `{"device_code":"` + deviceSecret + `"}`
		if string(body) != wantBody {
			f.problems = append(f.problems, "device token body "+string(body))
		}
		f.polls++
		switch f.pollMode {
		case "expired_token":
			writeEnv(w, http.StatusBadRequest, "expired_token", "the device code expired", nil)
		case "access_denied":
			writeEnv(w, http.StatusBadRequest, "access_denied", "the user denied the request", nil)
		case "pending":
			writeEnv(w, http.StatusBadRequest, "authorization_pending", "pending", nil)
		case "immediate":
			writeEnv(w, http.StatusOK, "ok", "", map[string]any{
				"token":      sessionToken,
				"expires_at": "2026-11-12T00:00:00Z",
			})
		default:
			switch f.polls {
			case 1:
				writeEnv(w, http.StatusBadRequest, "authorization_pending", "pending", nil)
			case 2:
				writeEnv(w, http.StatusBadRequest, "slow_down", "slow down", nil)
			default:
				writeEnv(w, http.StatusOK, "ok", "", map[string]any{
					"token":      sessionToken,
					"expires_at": "2026-11-12T00:00:00Z",
				})
			}
		}
	case "/v1/auth/signout":
		f.signoutAuth = append(f.signoutAuth, r.Header.Get("Authorization"))
		switch f.signout {
		case "unauthorized":
			writeEnv(w, http.StatusUnauthorized, "unauthorized", "nope", nil)
		case "error":
			writeEnv(w, http.StatusInternalServerError, "error", "boom", nil)
		default:
			writeEnv(w, http.StatusOK, "ok", "", nil)
		}
	default:
		writeEnv(w, http.StatusNotFound, "not_found", "no such route", nil)
	}
}

func headerProblems(h http.Header, hasBody bool) []string {
	var bad []string
	for key := range h {
		switch http.CanonicalHeaderKey(key) {
		case "Authorization", "Accept", "User-Agent", "Accept-Encoding", "Content-Length":
		case "Content-Type":
			if !hasBody {
				bad = append(bad, "Content-Type")
			}
		default:
			bad = append(bad, key)
		}
	}
	return bad
}

func writeEnv(w http.ResponseWriter, status int, code, msg string, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": msg, "payload": payload})
}
