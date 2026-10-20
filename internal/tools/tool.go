// Package tools registers the MCP tools. Each tool calls bot-api-server with
// the delegated token for this request. The caller's token is never sent.
// Catalog run, stop, restart, deploy, rollback, set_health_checks, and catalog
// image create, replace, and delete do not act until confirm.
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
	"time"
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
	registerGetHealthChecks(server, api)
	registerListCatalog(server, api)
	registerRunEntry(server, api)
	registerStopEntry(server, api)
	registerRestartEntry(server, api)
	registerDeployEntry(server, api)
	registerRollbackEntry(server, api)
	registerSetHealthChecks(server, api)
	registerCreateCatalogEntry(server, api)
	registerUpdateCatalogEntry(server, api)
	registerDeleteCatalogEntry(server, api)
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

// mutatingAnnotations marks a tool that changes a host or a catalog entry.
// openWorld is false for run, stop, restart, deploy, rollback,
// set_health_checks, and catalog create, replace, and delete: those calls
// do not act. confirm sets it true because a program or image confirm can
// contact the host. A catalog-write confirm does not.
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
		return toolError(apiErr.Code, redactText(apiErr.Msg))
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

func putStatus(ctx context.Context, api *apiclient.Client, path string, body any) (int, json.RawMessage, *mcp.CallToolResult) {
	return callStatus(ctx, api, http.MethodPut, path, nil, body)
}

// postWithin is postStatus with a per-call deadline. A deadline is reported
// as still running on the host. Only confirm uses it, and only for an image
// deploy or rollback. Other calls keep the default deadline and the generic
// API error.
func postWithin(ctx context.Context, api *apiclient.Client, path string, body any, timeout time.Duration) (int, json.RawMessage, *mcp.CallToolResult) {
	return callStatusWithin(ctx, api, http.MethodPost, path, nil, body, timeout)
}

func callStatusWithin(ctx context.Context, api *apiclient.Client, method, path string, query url.Values, body any, timeout time.Duration) (int, json.RawMessage, *mcp.CallToolResult) {
	token, ok := auth.DelegatedFromContext(ctx)
	if !ok {
		return 0, nil, toolError("unauthorized", "missing bearer token")
	}
	status, payload, err := api.DoStatusWithin(ctx, timeout, token, method, path, query, body)
	if err != nil {
		if errors.Is(err, apiclient.ErrDeadline) {
			return 0, nil, stillRunning()
		}
		var apiErr *apiclient.Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			auth.NoteRejected(ctx)
		}
		return 0, nil, fromAPI(err)
	}
	return status, payload, nil
}

func stillRunning() *mcp.CallToolResult {
	return toolError("error", "still running on the host, check entry_status later")
}

// actedOrPending reports a program status only for HTTP 200.
// HTTP 202 is a confirmation: nothing ran, and the result is the plan.
// confirm uses this. run, stop, and restart use confirmationOnly.
func actedOrPending(status int, payload json.RawMessage) *mcp.CallToolResult {
	switch status {
	case http.StatusAccepted:
		return pendingConfirmation(payload)
	case http.StatusCreated:
		return catalogWriteResult(payload, "created")
	case http.StatusOK:
		if isNullJSON(payload) {
			return catalogWriteResult(payload, "deleted")
		}
		if catalogEntryPayload(payload) {
			return catalogWriteResult(payload, "updated")
		}
		if projected, ok, err := projectHealthChecks(payload); ok {
			if err != nil {
				return toolError("error", errHealthChecksShape.Error())
			}
			summary, sumOK := summarizeHealthChecks(projected)
			if !sumOK {
				return toolError("error", errHealthChecksShape.Error())
			}
			return present(projected, summary)
		}
		payload = redactLines(payload)
		payload = projectDeployResult(payload)
		if imageStateRejected(payload) {
			return toolError("error", errStatusShape.Error())
		}
		return present(payload, summarizeActed(payload))
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

// errConfirmationShape is fixed. It must not include the confirmation body.
var errConfirmationShape = errors.New("confirmation response was not usable")

// errPlanTooLong is fixed. The plan text is not copied into the tool error.
var errPlanTooLong = errors.New("plan too long")

// errStatusShape is fixed. It must not include the status body or the state.
var errStatusShape = errors.New("status response was not usable")

// confirmationHealth is confirmationOnly with the health-check plan cap.
// A plan longer than that cap is an error. It is not shortened.
func confirmationHealth(status int, payload json.RawMessage) *mcp.CallToolResult {
	if status != http.StatusAccepted {
		return toolError("error", "expected a confirmation")
	}
	return pendingHealthConfirmation(payload)
}

// pendingConfirmation shows the plan and the confirmation id.
// The text says nothing ran yet. The payload is not summarized as a program status.
// The structured result is an allowlist: confirmation_id, plan, expires_at,
// history, and force when it is true. plan is redacted and capped.
func pendingConfirmation(payload json.RawMessage) *mcp.CallToolResult {
	return finishConfirmation(projectConfirmation(payload))
}

func pendingHealthConfirmation(payload json.RawMessage) *mcp.CallToolResult {
	return finishConfirmation(projectConfirmationLimited(payload, false))
}

func finishConfirmation(projected json.RawMessage, err error) *mcp.CallToolResult {
	if errors.Is(err, errPlanTooLong) {
		return toolError("error", "plan too long to show; send fewer checks")
	}
	if err != nil {
		return toolError("error", errConfirmationShape.Error())
	}
	payload := projected
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
		// projectConfirmation already redacted and capped the plan.
		b.WriteString(body.Plan)
	}
	b.WriteString(historyLines(payload))
	return present(payload, b.String())
}

// projectConfirmation returns only confirmation_id, plan, expires_at, history,
// and force when true. plan is redacted, then capped. force: true is part of
// the plan string. Anything else in the body is dropped. A non-object is an error.
func projectConfirmation(raw json.RawMessage) (json.RawMessage, error) {
	return projectConfirmationLimited(raw, true)
}

// projectConfirmationLimited is projectConfirmation.
// clamp false is the set_health_checks plan: redact and keep it up to
// maxHealthPlanRunes, or errPlanTooLong. It is not cut to maxPlanRunes.
func projectConfirmationLimited(raw json.RawMessage, clamp bool) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errConfirmationShape
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return nil, errConfirmationShape
	}
	out := map[string]any{}
	if s, ok := body["confirmation_id"].(string); ok {
		out["confirmation_id"] = publicString(s)
	}
	if s, ok := body["expires_at"].(string); ok {
		out["expires_at"] = publicString(s)
	}
	plan, _ := body["plan"].(string)
	plan = redactText(plan)
	force, _ := body["force"].(bool)
	limit := maxPlanRunes
	if !clamp {
		limit = maxHealthPlanRunes
	}
	if clamp {
		if force {
			room := limit - len([]rune("\nforce: true"))
			if plan == "" {
				plan = "force: true"
			} else {
				plan = clampField(plan, room) + "\nforce: true"
			}
			out["force"] = true
		} else {
			plan = clampField(plan, limit)
		}
	} else {
		plan = clampField(plan, max(len([]rune(plan)), 1))
		if force {
			if plan == "" {
				plan = "force: true"
			} else {
				plan += "\nforce: true"
			}
			out["force"] = true
		}
		if len([]rune(plan)) > limit {
			return nil, errPlanTooLong
		}
	}
	if plan != "" {
		out["plan"] = plan
	}
	if hist, ok := body["history"].([]any); ok {
		out["history"] = projectHistoryRows(hist)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, errConfirmationShape
	}
	return encoded, nil
}

// projectDeployResult allowlists an image deploy or rollback result.
// A payload without a deploy object is returned unchanged (program status,
// image status, logs). A deploy object keeps outcome, digest, and exit_code.
// status keeps the ImageStatus fields. A state outside the allowlist drops status.
// lines keeps strings only.
func projectDeployResult(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return []byte(`{}`)
	}
	deploy, ok := body["deploy"]
	if !ok {
		return raw
	}
	out := map[string]any{"deploy": projectImageDeploy(deploy)}
	if status, ok := projectImageStatus(body["status"]); ok {
		out["status"] = status
	}
	if _, ok := body["lines"]; ok {
		out["lines"] = stringList(body["lines"])
	}
	if truncated, ok := body["truncated"].(bool); ok {
		out["truncated"] = truncated
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return []byte(`{}`)
	}
	return encoded
}

func projectImageDeploy(v any) map[string]any {
	row, _ := v.(map[string]any)
	out := map[string]any{}
	if s, ok := row["outcome"].(string); ok {
		out["outcome"] = publicString(s)
	}
	if s, ok := row["digest"].(string); ok {
		out["digest"] = publicString(s)
	}
	if n, ok := row["exit_code"].(json.Number); ok {
		out["exit_code"] = n
	}
	return out
}

// imageStateRejected reports a status body whose state is outside the allowlist.
// A deploy result is not checked here: projectImageStatus already drops that status.
// A missing or empty state is a program status, or a status with nothing to show.
// The caller returns errStatusShape and does not return the body.
func imageStateRejected(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		return false
	}
	if _, ok := body["deploy"]; ok {
		return false
	}
	state, exists := body["state"]
	if !exists || state == nil {
		return false
	}
	s, ok := state.(string)
	if !ok || (s != "" && !imageStateOK(s)) {
		return true
	}
	return false
}

func projectImageStatus(v any) (map[string]any, bool) {
	row, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	if state, exists := row["state"]; exists && state != nil {
		s, isStr := state.(string)
		if !isStr || (s != "" && !imageStateOK(s)) {
			return nil, false
		}
	}
	out := map[string]any{}
	for _, key := range []string{"container", "started_at", "finished_at", "digest"} {
		if s, ok := row[key].(string); ok {
			out[key] = publicString(s)
		}
	}
	if state, ok := row["state"].(string); ok && imageStateOK(state) {
		out["state"] = state
	}
	for _, key := range []string{"exit_code", "restart_count"} {
		if n, ok := row[key].(json.Number); ok {
			out[key] = n
		}
	}
	if ports := numberList(row["ports"]); ports != nil {
		out["ports"] = ports
	}
	return out, true
}

func projectHistoryRows(items []any) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, map[string]any{
			"digest":  publicString(stringField(row, "digest")),
			"outcome": publicString(stringField(row, "outcome")),
			"time":    publicString(stringField(row, "time")),
		})
	}
	return out
}

func numberList(v any) []any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(arr))
	for _, item := range arr {
		n, ok := item.(json.Number)
		if !ok {
			continue
		}
		out = append(out, n)
	}
	return out
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
