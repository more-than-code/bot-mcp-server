package apiclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoForwardsTokenAndDoesNotFollowRedirects(t *testing.T) {
	const token = "tok.with~special_chars"
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/stolen" {
			t.Errorf("followed redirect")
			return
		}
		if r.URL.Path != "/v1/me" {
			t.Errorf("path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization %q", got)
		}
		if len(r.Header.Values("Authorization")) != 1 {
			t.Errorf("Authorization values %d", len(r.Header.Values("Authorization")))
		}
		for key, vals := range r.Header {
			if key == "Authorization" {
				continue
			}
			for _, val := range vals {
				if val == token {
					t.Errorf("token in header %s", key)
				}
			}
		}
		w.Header().Set("Location", "/stolen")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	client, err := apiclientNew(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(context.Background(), token, http.MethodGet, "/v1/me", nil, nil)
	if err == nil {
		t.Fatal("expected error from redirect")
	}
	if hits != 1 {
		t.Fatalf("hits %d", hits)
	}
}

func TestDoOmitsAuthorizationWhenTokenEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected Authorization")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) == "" || r.URL.Path != "/v1/auth/device/code" {
			t.Errorf("path %s body %s", r.URL.Path, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"ok","msg":"","payload":{"device_code":"d","user_code":"ABCD-EFGH","verification_uri":"https://c/device","verification_uri_complete":"https://c/device?code=ABCD-EFGH","expires_in":600,"interval":5}}`))
	}))
	defer srv.Close()

	client, err := apiclientNew(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.RequestDeviceCode(context.Background(), "deploybot")
	if err != nil {
		t.Fatal(err)
	}
	if got.UserCode != "ABCD-EFGH" || got.Interval != 5 {
		t.Fatalf("%+v", got)
	}
}

func TestDoStatusKeepsAccepted(t *testing.T) {
	const token = "delegated"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("Authorization %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"code":"ok","msg":"","payload":{"confirmation_id":"abc","plan":"will run","expires_at":"2026-10-15T00:05:00Z"}}`))
	}))
	defer srv.Close()

	client, err := apiclientNew(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	status, payload, err := client.DoStatus(context.Background(), token, http.MethodPost, "/v1/projects/p/catalog/e/run", nil, map[string]string{"host_id": "h"})
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusAccepted {
		t.Fatalf("status %d", status)
	}
	if !strings.Contains(string(payload), `"plan":"will run"`) {
		t.Fatalf("payload %s", payload)
	}
}

func TestNewRejectsUserInfo(t *testing.T) {
	if _, err := New("https://user:secret@api.example", false); err == nil {
		t.Fatal("accepted user info")
	}
}

func TestNewRequiresHTTPS(t *testing.T) {
	if _, err := New("http://example.com", false); err == nil {
		t.Fatal("accepted cleartext")
	}
	if _, err := New("http://example.com", true); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"http://127.0.0.1:9", "http://localhost:9", "http://[::1]:9", "https://api.example"} {
		if _, err := New(raw, false); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}

func apiclientNew(raw string) (*Client, error) { return New(raw, false) }
