package auth

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	clientID             = "bot-mcp-server"
	grantExchange        = "urn:ietf:params:oauth:grant-type:token-exchange"
	tokenTypeAccess      = "urn:ietf:params:oauth:token-type:access_token"
	expirySkew           = 30 * time.Second
	maxCacheEntries      = 10000
	maxInflightExchanges = 256
	maxDenials           = 10000
	denialTTL            = 60 * time.Second
	exchangeTimeout      = 10 * time.Second
	maxTokenBody         = 64 << 10
	maxExpiresIn         = 24 * 60 * 60
)

var (
	errInvalidToken = errors.New("invalid token")
	errUnavailable  = errors.New("authorization server unavailable")
)

// Exchanger trades a caller token for a short-lived API token.
// The client secret authenticates this server. It is never a user credential.
type Exchanger struct {
	endpoint string
	origin   string
	secret   string
	client   *http.Client
	cache    *tokenCache
	logger   *slog.Logger
}

// LogValue omits the client secret so a log of the exchanger cannot leak it.
func (e *Exchanger) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("endpoint", e.endpoint),
		slog.String("resource", e.origin),
	)
}

// NewExchanger builds an exchanger for the API origin of apiURL.
// The token endpoint is that origin plus /oauth/token. secret is the raw
// client secret, not a file path.
func NewExchanger(apiURL, secret string, logger *slog.Logger) (*Exchanger, error) {
	if secret == "" {
		return nil, errors.New("client secret is empty")
	}
	origin, err := apiOrigin(apiURL)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Exchanger{
		endpoint: origin + "/oauth/token",
		origin:   origin,
		secret:   secret,
		client: &http.Client{
			Timeout: exchangeTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		cache:  newTokenCache(maxCacheEntries, time.Now),
		logger: logger,
	}, nil
}

func apiOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("api url must be an absolute http(s) URL")
	}
	u.User = nil
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// Delegated returns the API token for subject, from the cache or a new exchange.
func (e *Exchanger) Delegated(ctx context.Context, subject string) (string, error) {
	if !ValidToken(subject) {
		return "", errInvalidToken
	}
	return e.cache.token(ctx, cacheKey(subject), func() (string, time.Time, error) {
		return e.exchange(ctx, subject)
	})
}

// RequireDelegated exchanges the caller token and then runs next.
// A missing bearer is 401. A refused token is 401 before next runs.
// The authorization server being down, or this request's context ending
// while it waits for an exchange, is 503. Cancellation is never a 401.
// next sees only the delegated token.
func (e *Exchanger) RequireDelegated(publicOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject, ok := ParseBearer(r.Header.Get("Authorization"))
		if !ok {
			WriteUnauthorized(w, publicOrigin)
			return
		}
		delegated, err := e.Delegated(r.Context(), subject)
		if err != nil {
			if errors.Is(err, errInvalidToken) {
				WriteInvalidToken(w, publicOrigin)
				return
			}
			WriteUnavailable(w)
			return
		}
		key := cacheKey(subject)
		ctx := WithDelegatedToken(r.Context(), delegated, func() {
			e.cache.drop(key, delegated)
		})
		downstream := r.Clone(ctx)
		// The MCP SDK copies this header onto the tool request. Drop it so
		// tool code cannot read the caller token.
		downstream.Header.Del("Authorization")
		next.ServeHTTP(w, downstream)
	})
}

func (e *Exchanger) exchange(ctx context.Context, subject string) (string, time.Time, error) {
	form := url.Values{}
	form.Set("grant_type", grantExchange)
	form.Set("subject_token", subject)
	form.Set("subject_token_type", tokenTypeAccess)
	form.Set("resource", e.origin)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		e.logFailure(0, "")
		return "", time.Time{}, errUnavailable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", basicAuthHeader(clientID, e.secret))
	resp, err := e.client.Do(req)
	if err != nil {
		e.logFailure(0, "")
		return "", time.Time{}, errUnavailable
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenBody))
	if err != nil {
		e.logFailure(resp.StatusCode, "")
		return "", time.Time{}, errUnavailable
	}
	return e.classify(resp.StatusCode, body)
}

func (e *Exchanger) classify(status int, body []byte) (string, time.Time, error) {
	var resp tokenResponse
	_ = json.Unmarshal(body, &resp)
	if status >= 500 {
		e.logFailure(status, resp.Error)
		return "", time.Time{}, errUnavailable
	}
	if resp.Error == "invalid_grant" || resp.Error == "invalid_request" {
		e.logFailure(status, resp.Error)
		return "", time.Time{}, errInvalidToken
	}
	if resp.Error != "" || status != http.StatusOK {
		e.logFailure(status, resp.Error)
		return "", time.Time{}, errUnavailable
	}
	if !strings.EqualFold(resp.TokenType, "Bearer") || resp.IssuedTokenType != tokenTypeAccess {
		e.logFailure(status, "")
		return "", time.Time{}, errUnavailable
	}
	if !ValidToken(resp.AccessToken) || resp.ExpiresIn <= 0 || resp.ExpiresIn > maxExpiresIn {
		e.logFailure(status, "")
		return "", time.Time{}, errUnavailable
	}
	until := e.cache.now().Add(time.Duration(resp.ExpiresIn)*time.Second - expirySkew)
	return resp.AccessToken, until, nil
}

func (e *Exchanger) logFailure(status int, oauthError string) {
	e.logger.Error("token exchange failed", "status", status, "oauth_error", oauthErrorCode(oauthError))
}

func oauthErrorCode(code string) string {
	switch code {
	case "invalid_grant", "invalid_request", "invalid_client", "unauthorized_client",
		"invalid_target", "invalid_scope", "unsupported_grant_type", "server_error":
		return code
	case "":
		return ""
	default:
		return "other"
	}
}

func basicAuthHeader(id, secret string) string {
	raw := url.QueryEscape(id) + ":" + url.QueryEscape(secret)
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
}

type tokenResponse struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int    `json:"expires_in"`
	Error           string `json:"error"`
}

func cacheKey(token string) [32]byte {
	return sha256.Sum256([]byte(token))
}

// tokenCache stores delegated tokens until 30s before they expire.
// Calls for one caller token share a single in-flight exchange.
type tokenCache struct {
	mu        sync.Mutex
	items     map[[32]byte]*list.Element
	order     *list.List // front is the oldest entry
	inflight  map[[32]byte]*flight
	max       int
	maxFlight int
	denied    map[[32]byte]*list.Element
	deniedOrd *list.List // front is the oldest refusal
	maxDenied int
	now       func() time.Time
}

type flight struct {
	done    chan struct{}
	token   string
	err     error
	waiters int
}

type cacheSlot struct {
	key     [32]byte
	token   string
	expires time.Time
}

func newTokenCache(max int, now func() time.Time) *tokenCache {
	if max < 1 {
		max = maxCacheEntries
	}
	if now == nil {
		now = time.Now
	}
	return &tokenCache{
		items:     make(map[[32]byte]*list.Element),
		order:     list.New(),
		inflight:  make(map[[32]byte]*flight),
		max:       max,
		maxFlight: maxInflightExchanges,
		denied:    make(map[[32]byte]*list.Element),
		deniedOrd: list.New(),
		maxDenied: maxDenials,
		now:       now,
	}
}

// token returns a cached token or runs exchange once for this key.
// A subject refused with invalid_grant or invalid_request is answered from
// the denial cache and does not call exchange. A new exchange past maxFlight
// returns unavailable without calling exchange. Waiters share one result.
// A waiter whose ctx ends returns that cancellation error and leaves the
// exchange running. A panic in exchange still wakes the remaining waiters.
func (c *tokenCache) token(ctx context.Context, key [32]byte, exchange func() (string, time.Time, error)) (token string, err error) {
	c.mu.Lock()
	if found, ok := c.lookupLocked(key); ok {
		c.mu.Unlock()
		return found, nil
	}
	if c.lookupDeniedLocked(key) {
		c.mu.Unlock()
		return "", errInvalidToken
	}
	if f, ok := c.inflight[key]; ok {
		f.waiters++
		c.mu.Unlock()
		select {
		case <-f.done:
			token, err = f.token, f.err
		case <-ctx.Done():
			err = ctx.Err()
		}
		c.mu.Lock()
		f.waiters--
		c.mu.Unlock()
		return token, err
	}
	if len(c.inflight) >= c.maxFlight {
		c.mu.Unlock()
		return "", errUnavailable
	}
	f := &flight{done: make(chan struct{})}
	c.inflight[key] = f
	c.mu.Unlock()

	var expires time.Time
	defer func() {
		if rec := recover(); rec != nil {
			if err == nil {
				err = errUnavailable
			}
			c.finish(key, f, token, expires, err)
			panic(rec)
		}
		c.finish(key, f, token, expires, err)
	}()
	token, expires, err = exchange()
	return token, err
}

func (c *tokenCache) finish(key [32]byte, f *flight, token string, expires time.Time, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.storeLocked(key, token, expires)
	} else if errors.Is(err, errInvalidToken) {
		c.denyLocked(key)
	}
	delete(c.inflight, key)
	f.token = token
	f.err = err
	close(f.done)
}

func (c *tokenCache) lookupLocked(key [32]byte) (string, bool) {
	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	slot := el.Value.(*cacheSlot)
	if !c.now().Before(slot.expires) {
		c.removeLocked(el)
		return "", false
	}
	return slot.token, true
}

func (c *tokenCache) storeLocked(key [32]byte, token string, expires time.Time) {
	if token == "" || !expires.After(c.now()) {
		return
	}
	if el, ok := c.items[key]; ok {
		slot := el.Value.(*cacheSlot)
		slot.token = token
		slot.expires = expires
		c.order.MoveToBack(el)
		return
	}
	for c.order.Len() >= c.max {
		c.removeLocked(c.order.Front())
	}
	el := c.order.PushBack(&cacheSlot{key: key, token: token, expires: expires})
	c.items[key] = el
}

// drop removes the entry when it still holds token. A newer exchange for the
// same caller token is left in place.
func (c *tokenCache) drop(key [32]byte, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return
	}
	if el.Value.(*cacheSlot).token != token {
		return
	}
	c.removeLocked(el)
}

func (c *tokenCache) removeLocked(el *list.Element) {
	if el == nil {
		return
	}
	slot := el.Value.(*cacheSlot)
	delete(c.items, slot.key)
	c.order.Remove(el)
}

type denial struct {
	key     [32]byte
	expires time.Time
}

// lookupDeniedLocked reports a refusal that is still inside its 60s window.
// An entry whose time has come is removed.
func (c *tokenCache) lookupDeniedLocked(key [32]byte) bool {
	el, ok := c.denied[key]
	if !ok {
		return false
	}
	d := el.Value.(*denial)
	if !c.now().Before(d.expires) {
		c.removeDenialLocked(el)
		return false
	}
	return true
}

// denyLocked remembers a subject the authorization server refused.
// The raw token is not stored. The oldest entry is dropped at the cap.
func (c *tokenCache) denyLocked(key [32]byte) {
	until := c.now().Add(denialTTL)
	if el, ok := c.denied[key]; ok {
		el.Value.(*denial).expires = until
		c.deniedOrd.MoveToBack(el)
		return
	}
	for c.deniedOrd.Len() >= c.maxDenied {
		c.removeDenialLocked(c.deniedOrd.Front())
	}
	el := c.deniedOrd.PushBack(&denial{key: key, expires: until})
	c.denied[key] = el
}

func (c *tokenCache) removeDenialLocked(el *list.Element) {
	if el == nil {
		return
	}
	d := el.Value.(*denial)
	delete(c.denied, d.key)
	c.deniedOrd.Remove(el)
}
