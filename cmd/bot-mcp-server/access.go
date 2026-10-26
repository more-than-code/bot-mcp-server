package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"
)

// accessPeekLimit bounds how much of the body the access log keeps to name
// the JSON-RPC method. A larger body is served as usual; its method is not
// logged.
const accessPeekLimit = 64 << 10

// rpcMethodName is the shape of a logged JSON-RPC method: up to four
// slash-separated words of letters and underscores (for example tools/call,
// subscriptions/listen, notifications/roots/list_changed). Anything else is
// "other", so a client cannot write free text into the log. A shape, not a
// fixed list, so methods from newer protocol versions stay visible.
var rpcMethodName = regexp.MustCompile(`^[a-z][A-Za-z_]{0,31}(/[a-z][A-Za-z_]{0,31}){0,3}$`)

// toolName is the shape of a logged tool name; anything else is "other".
var toolName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)

var httpMethods = map[string]bool{
	http.MethodGet: true, http.MethodPost: true, http.MethodDelete: true,
	http.MethodHead: true, http.MethodOptions: true,
}

// withAccessLog logs one line per /mcp request: HTTP method, status,
// duration, the JSON-RPC method (and tool name for tools/call), and whether
// the client went away before the handler finished. It never logs headers,
// tokens, ids or arguments.
//
// The body is not read here. A capturing reader keeps a copy of what the
// handler itself reads, so a request refused by the Host or token check is
// never read, and its line has no rpc.
func withAccessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var body *captureBody
		if r.Method == http.MethodPost && r.Body != nil {
			body = &captureBody{ReadCloser: r.Body}
			r.Body = body
		}
		sw := &accessWriter{ResponseWriter: w}
		defer func() {
			rec := recover()
			code := sw.code
			if code == 0 {
				code = http.StatusOK
			}
			method := r.Method
			if !httpMethods[method] {
				method = "other"
			}
			attrs := []any{"method", method, "status", code, "ms", time.Since(start).Milliseconds()}
			if body != nil && body.complete() {
				rpc, tool := rpcMethod(body.buf.Bytes())
				attrs = append(attrs, "rpc", rpc)
				if tool != "" {
					attrs = append(attrs, "tool", tool)
				}
			}
			if r.Context().Err() != nil {
				attrs = append(attrs, "client_closed", true)
			}
			if rec != nil {
				attrs = append(attrs, "aborted", true)
			}
			logger.Info("request", attrs...)
			if rec != nil {
				panic(rec)
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// rpcMethod returns the JSON-RPC method and, for tools/call, the tool name.
func rpcMethod(body []byte) (string, string) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return "batch", ""
	}
	var msg struct {
		Method *string          `json:"method"`
		Params json.RawMessage  `json:"params"`
		Result *json.RawMessage `json:"result"`
		Error  *json.RawMessage `json:"error"`
	}
	if json.Unmarshal(trimmed, &msg) != nil {
		return "other", ""
	}
	if msg.Method == nil {
		if msg.Result != nil || msg.Error != nil {
			return "response", ""
		}
		return "other", ""
	}
	if !rpcMethodName.MatchString(*msg.Method) {
		return "other", ""
	}
	if *msg.Method != "tools/call" {
		return *msg.Method, ""
	}
	var params struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(msg.Params, &params) != nil || !toolName.MatchString(params.Name) {
		return "tools/call", "other"
	}
	return "tools/call", params.Name
}

// captureBody copies up to accessPeekLimit bytes of what the handler reads.
type captureBody struct {
	io.ReadCloser
	buf  bytes.Buffer
	over bool
	eof  bool
}

func (b *captureBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if room := accessPeekLimit - b.buf.Len(); n > room {
		b.buf.Write(p[:room])
		b.over = true
	} else {
		b.buf.Write(p[:n])
	}
	if err == io.EOF {
		b.eof = true
	}
	return n, err
}

// complete reports whether the handler read the whole body within the limit.
func (b *captureBody) complete() bool { return b.eof && !b.over }

// accessWriter records the final status. Unwrap keeps http.ResponseController
// working, which withWriteBudget needs for SetWriteDeadline.
type accessWriter struct {
	http.ResponseWriter
	code int
}

func (w *accessWriter) WriteHeader(code int) {
	if w.code == 0 && (code >= 200 || code == http.StatusSwitchingProtocols) {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *accessWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *accessWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *accessWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
