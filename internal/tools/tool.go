// Package tools registers the phase 1 MCP tools. Each tool reads through
// bot-api-server with the caller's Bearer token and does not write.
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

// Register adds the eight phase 1 tools to server.
func Register(server *mcp.Server, api *apiclient.Client) {
	registerListProjects(server, api)
	registerListHosts(server, api)
	registerListServices(server, api)
	registerQueryMetrics(server, api)
	registerListProcesses(server, api)
	registerServiceHealthHistory(server, api)
	registerHostSnapshot(server, api)
	registerProbeService(server, api)
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

// call sends one API request with the token from ctx. It never calls the
// API without that token.
func call(ctx context.Context, api *apiclient.Client, method, path string, query url.Values, body any) (json.RawMessage, *mcp.CallToolResult) {
	token, ok := auth.TokenFromContext(ctx)
	if !ok {
		return nil, toolError("unauthorized", "missing bearer token")
	}
	payload, err := api.Do(ctx, token, method, path, query, body)
	if err != nil {
		return nil, fromAPI(err)
	}
	return payload, nil
}

func get(ctx context.Context, api *apiclient.Client, path string, query url.Values) (json.RawMessage, *mcp.CallToolResult) {
	return call(ctx, api, http.MethodGet, path, query, nil)
}

func post(ctx context.Context, api *apiclient.Client, path string, body any) (json.RawMessage, *mcp.CallToolResult) {
	return call(ctx, api, http.MethodPost, path, nil, body)
}

func present(payload json.RawMessage, summary string) *mcp.CallToolResult {
	out, note := truncateLists(payload, maxListItems)
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
