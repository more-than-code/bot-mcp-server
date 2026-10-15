// Package tools registers the MCP tools. Each tool calls bot-api-server with
// the delegated token for this request. The caller's token is never sent.
// Catalog run, stop, and restart do not act until confirm.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
	"github.com/more-than-code/bot-mcp-server/internal/auth"
)

// maxListItems is the longest list returned to a client. The text summary
// says when a longer API list was cut.
const maxListItems = 200

// maxErrorBytes caps code and msg copied into a tool error.
const maxErrorBytes = 1024

// Register adds the phase 1 tools and the catalog tools to server.
func Register(server *mcp.Server, api *apiclient.Client) {
	registerListProjects(server, api)
	registerListHosts(server, api)
	registerListServices(server, api)
	registerQueryMetrics(server, api)
	registerListProcesses(server, api)
	registerServiceHealthHistory(server, api)
	registerHostSnapshot(server, api)
	registerProbeService(server, api)
	registerListCatalog(server, api)
	registerRunEntry(server, api)
	registerStopEntry(server, api)
	registerRestartEntry(server, api)
	registerConfirm(server, api)
	registerEntryLogs(server, api)
	registerEntryStatus(server, api)
}

func addTool(server *mcp.Server, tool *mcp.Tool, h func(context.Context, json.RawMessage) *mcp.CallToolResult) {
	server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
		defer func() {
			if recover() != nil {
				// The panic value can contain arguments. Log only the tool name.
				slog.Default().Error("tool panicked", "tool", tool.Name)
				res = toolError("error", "internal error")
				err = nil
			}
		}()
		args := json.RawMessage(nil)
		if req != nil && req.Params != nil {
			args = req.Params.Arguments
		}
		res = h(ctx, args)
		return res, nil
	})
}

func readOnlyAnnotations(openWorld bool) *mcp.ToolAnnotations {
	open := openWorld
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &open}
}

// mutatingAnnotations marks a tool that changes a host. openWorld is false
// for run, stop, and restart: those calls do not act. confirm sets it true
// because that call contacts the host.
func mutatingAnnotations(openWorld bool) *mcp.ToolAnnotations {
	open := openWorld
	destructive := true
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: &destructive,
		IdempotentHint:  true,
		OpenWorldHint:   &open,
	}
}

func toolError(code, msg string) *mcp.CallToolResult {
	code = clip(code, maxErrorBytes)
	msg = clip(msg, maxErrorBytes)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: code + ": " + msg}},
		IsError: true,
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func badRequest(err error) *mcp.CallToolResult {
	return toolError("bad_request", err.Error())
}

func fromAPI(err error) *mcp.CallToolResult {
	var apiErr *apiclient.Error
	if errors.As(err, &apiErr) {
		return toolError(apiErr.Code, apiErr.Msg)
	}
	return toolError("error", "api request failed")
}

// call sends one API request with the delegated token from ctx.
// It never sends the caller's token. A 401 drops that delegated token from
// the exchange cache so the next request exchanges again.
func call(ctx context.Context, api *apiclient.Client, method, path string, query url.Values, body any) (json.RawMessage, *mcp.CallToolResult) {
	_, payload, fail := callStatus(ctx, api, method, path, query, body)
	return payload, fail
}

// callStatus is call plus the HTTP status of an ok envelope. Status 202 is
// not a tool error here; the caller decides what it means.
func callStatus(ctx context.Context, api *apiclient.Client, method, path string, query url.Values, body any) (int, json.RawMessage, *mcp.CallToolResult) {
	token, ok := auth.DelegatedFromContext(ctx)
	if !ok {
		return 0, nil, toolError("unauthorized", "missing bearer token")
	}
	status, payload, err := api.DoStatus(ctx, token, method, path, query, body)
	if err != nil {
		var apiErr *apiclient.Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			auth.NoteRejected(ctx)
		}
		return 0, nil, fromAPI(err)
	}
	return status, payload, nil
}

func get(ctx context.Context, api *apiclient.Client, path string, query url.Values) (json.RawMessage, *mcp.CallToolResult) {
	return call(ctx, api, http.MethodGet, path, query, nil)
}

func post(ctx context.Context, api *apiclient.Client, path string, body any) (json.RawMessage, *mcp.CallToolResult) {
	return call(ctx, api, http.MethodPost, path, nil, body)
}

func postStatus(ctx context.Context, api *apiclient.Client, path string, body any) (int, json.RawMessage, *mcp.CallToolResult) {
	return callStatus(ctx, api, http.MethodPost, path, nil, body)
}

// actedOrPending reports a program status only for HTTP 200.
// HTTP 202 is a confirmation: nothing ran, and the result is the plan.
// confirm uses this. run, stop, and restart use confirmationOnly.
func actedOrPending(status int, payload json.RawMessage) *mcp.CallToolResult {
	switch status {
	case http.StatusAccepted:
		return pendingConfirmation(payload)
	case http.StatusOK:
		return present(payload, summarizeProgramStatus(payload))
	default:
		return toolError("error", "unexpected http status")
	}
}

// confirmationOnly accepts the 202 plan. Any other status, including 200,
// is an error: a delegated run, stop, or restart must not be reported as done.
func confirmationOnly(status int, payload json.RawMessage) *mcp.CallToolResult {
	if status != http.StatusAccepted {
		return toolError("error", "expected a confirmation")
	}
	return pendingConfirmation(payload)
}

// pendingConfirmation shows the plan and the confirmation id.
// The text says nothing ran yet. The payload is not summarized as a program status.
// Every string in the structured payload has controls replaced, and plan is capped.
func pendingConfirmation(payload json.RawMessage) *mcp.CallToolResult {
	payload = scrubConfirmation(payload)
	var body struct {
		ConfirmationID string `json:"confirmation_id"`
		Plan           string `json:"plan"`
		ExpiresAt      string `json:"expires_at"`
	}
	_ = json.Unmarshal(payload, &body)
	var b strings.Builder
	b.WriteString("nothing ran yet.")
	if body.ConfirmationID != "" {
		b.WriteString(" confirmation id ")
		b.WriteString(summaryField(body.ConfirmationID))
		b.WriteByte('.')
	}
	if body.ExpiresAt != "" {
		b.WriteString(" Expires ")
		b.WriteString(summaryField(body.ExpiresAt))
		b.WriteByte('.')
	}
	if body.Plan != "" {
		b.WriteByte('\n')
		b.WriteString(clampField(body.Plan, maxPlanRunes))
	}
	return present(payload, b.String())
}

// scrubConfirmation replaces controls in every string and caps plan.
// A payload with nothing to change is returned as the original bytes.
func scrubConfirmation(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	changed := false
	walkScrubStrings(v, &changed)
	if !changed {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

func walkScrubStrings(v any, changed *bool) {
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			s, ok := child.(string)
			if !ok {
				walkScrubStrings(child, changed)
				continue
			}
			next := replaceControls(s)
			if key == "plan" {
				next = clampField(s, maxPlanRunes)
			}
			if next != s {
				node[key] = next
				*changed = true
			}
		}
	case []any:
		for i, child := range node {
			s, ok := child.(string)
			if !ok {
				walkScrubStrings(child, changed)
				continue
			}
			next := replaceControls(s)
			if next != s {
				node[i] = next
				*changed = true
			}
		}
	}
}

// replaceControls maps Cc, Cf, Zl, and Zp to U+FFFD and does not shorten the text.
func replaceControls(s string) string {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return s
	}
	return clampField(s, n)
}

func present(payload json.RawMessage, summary string) *mcp.CallToolResult {
	out, note := truncateLists(redactLines(payload), maxListItems)
	if note != "" {
		summary = strings.TrimRight(summary, " ") + " " + note
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: summary}},
		StructuredContent: out,
	}
}

func decodeArgs(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid arguments: %s", err.Error())
	}
	return nil
}

func idPath(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteByte('/')
		b.WriteString(url.PathEscape(p))
	}
	return b.String()
}
