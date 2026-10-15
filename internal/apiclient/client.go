// Package apiclient is a typed client for bot-api-server's v1 envelope.
// The only credential it sends is the Bearer token the caller passes in.
// An empty token omits Authorization, which is how the public device-code
// routes are called. Redirects are not followed, so the token cannot be
// replayed to another host.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Timeout is how long one API call may take. Live tools wait up to 30s on
// the agent; this leaves a few seconds for the hop.
const Timeout = 35 * time.Second

// maxResponseBytes caps a single API body so a hostile peer cannot exhaust memory.
const maxResponseBytes = 8 << 20

// Client calls one bot-api-server.
type Client struct {
	base *url.URL
	http *http.Client
}

// HTTPAllowed reports whether a cleartext http URL for hostname is permitted.
// Loopback is always permitted. allowInsecure permits any other host.
func HTTPAllowed(hostname string, allowInsecure bool) bool {
	if allowInsecure {
		return true
	}
	switch strings.ToLower(hostname) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// New parses baseURL and builds a client. The URL must be https, or http
// when HTTPAllowed permits it. It must name a host and must not carry user info.
func New(baseURL string, allowInsecureHTTP bool) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("api url must be an absolute http(s) URL")
	}
	if u.Scheme == "http" && !HTTPAllowed(u.Hostname(), allowInsecureHTTP) {
		return nil, errors.New("api url must use https")
	}
	if u.User != nil {
		return nil, errors.New("api url must not include user info")
	}
	u.Fragment = ""
	u.RawQuery = ""
	return &Client{
		base: u,
		http: &http.Client{
			Timeout: Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Error is an API envelope that was not code "ok", or a response that was
// not an envelope. Error text is only the code and msg, never a header or body.
type Error struct {
	Status int
	Code   string
	Msg    string
}

func (e *Error) Error() string {
	if e.Msg == "" {
		return e.Code
	}
	return e.Code + ": " + e.Msg
}

type envelope struct {
	Code    string          `json:"code"`
	Msg     string          `json:"msg"`
	Payload json.RawMessage `json:"payload"`
}

// Do performs one v1 call. token is sent as Authorization: Bearer only when
// it is non-empty. The returned bytes are the raw payload JSON.
// A 202 whose envelope code is ok is not an error. Callers that must not
// treat that as the action having run use DoStatus.
func (c *Client) Do(ctx context.Context, token, method, path string, query url.Values, body any) (json.RawMessage, error) {
	_, payload, err := c.DoStatus(ctx, token, method, path, query, body)
	return payload, err
}

// DoStatus is Do and also returns the HTTP status when the envelope code is ok.
// Status 202 is not an error: catalog run, stop, and restart answer 202 when
// nothing has run. Callers must not report that status as success.
func (c *Client) DoStatus(ctx context.Context, token, method, path string, query url.Values, body any) (int, json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, nil, errors.New("could not encode api request")
		}
		rdr = bytes.NewReader(buf)
	}
	u := c.base.JoinPath(path)
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return 0, nil, errors.New("could not build api request")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, errors.New("api request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, errors.New("api request failed")
	}
	payload, err := decodeEnvelope(resp.StatusCode, data)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, payload, nil
}

func decodeEnvelope(status int, data []byte) (json.RawMessage, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Code == "" {
		code := "error"
		msg := "response was not an api envelope"
		if status == http.StatusUnauthorized {
			code = "unauthorized"
			msg = "unauthorized"
		}
		return nil, &Error{Status: status, Code: code, Msg: msg}
	}
	if env.Code != "ok" {
		return nil, &Error{Status: status, Code: env.Code, Msg: env.Msg}
	}
	if status < 200 || status >= 300 {
		return nil, &Error{Status: status, Code: "error", Msg: "unexpected http status"}
	}
	if len(env.Payload) == 0 || string(env.Payload) == "null" {
		return json.RawMessage("null"), nil
	}
	return env.Payload, nil
}

// DeviceCode is the payload of POST /v1/auth/device/code.
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// RequestDeviceCode starts the device flow. It sends no Bearer token.
func (c *Client) RequestDeviceCode(ctx context.Context, clientName string) (DeviceCode, error) {
	raw, err := c.Do(ctx, "", http.MethodPost, "/v1/auth/device/code", nil, map[string]string{
		"client_name": clientName,
	})
	if err != nil {
		return DeviceCode{}, err
	}
	var out DeviceCode
	if err := json.Unmarshal(raw, &out); err != nil {
		return DeviceCode{}, errors.New("device code response was not valid")
	}
	return out, nil
}

// Session is the payload of a successful POST /v1/auth/device/token.
type Session struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// PollDeviceToken exchanges a device code. Pending and denied polls return
// an *Error whose Code is the API code. It sends no Bearer token.
func (c *Client) PollDeviceToken(ctx context.Context, deviceCode string) (Session, error) {
	raw, err := c.Do(ctx, "", http.MethodPost, "/v1/auth/device/token", nil, map[string]string{
		"device_code": deviceCode,
	})
	if err != nil {
		return Session{}, err
	}
	var out Session
	if err := json.Unmarshal(raw, &out); err != nil {
		return Session{}, errors.New("device token response was not valid")
	}
	return out, nil
}

// SignOut deletes the session identified by token.
func (c *Client) SignOut(ctx context.Context, token string) error {
	_, err := c.Do(ctx, token, http.MethodPost, "/v1/auth/signout", nil, nil)
	return err
}
