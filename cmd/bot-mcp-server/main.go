package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/more-than-code/bot-mcp-server/internal/apiclient"
	"github.com/more-than-code/bot-mcp-server/internal/auth"
	"github.com/more-than-code/bot-mcp-server/internal/tools"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	cfg, err := configFrom(os.Getenv)
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}
	api, err := apiclient.New(cfg.APIURL, cfg.AllowInsecureHTTP)
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}
	handler, err := newHandler(cfg, api, logger)
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(1)
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		// WriteTimeout covers handler time, including the 35s API client timeout.
		WriteTimeout: 70 * time.Second,
		IdleTimeout:  120 * time.Second,
		ErrorLog:     slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	logger.Info("listening", "addr", cfg.Listen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server", "err", err)
		os.Exit(1)
	}
}

// newHandler serves health, protected-resource metadata, and the MCP endpoint.
// /mcp exchanges the caller token before any MCP handling. The MCP handler is
// stateless: the SDK accepts protocol 2026-07-28 only in that mode, and this
// server keeps no session of its own.
func newHandler(cfg config, api *apiclient.Client, logger *slog.Logger) (http.Handler, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	exchanger, err := auth.NewExchanger(cfg.APIURL, cfg.ClientSecret, logger)
	if err != nil {
		return nil, err
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "bot-mcp-server",
		Version: "0.1.0",
	}, &mcp.ServerOptions{
		Logger:       logger,
		Capabilities: &mcp.ServerCapabilities{},
	})
	tools.Register(server, api)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		Logger:       logger,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("GET /.well-known/oauth-protected-resource", auth.ProtectedResource(cfg.PublicOrigin, cfg.APIURL))
	mux.Handle("/mcp", exchanger.RequireDelegated(cfg.PublicOrigin, mcpHandler))
	return mux, nil
}
