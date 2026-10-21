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

// writeTimeout is the server-wide bound. /health, unauthenticated requests,
// and every tool except a long confirm stay on it. A confirm that is about
// to perform an image deploy or rollback, or whose budget is unknown, extends
// only that response to the budget plus writeTimeout, and never past
// mcpWriteTimeout. There is no in-flight cap: no number was approved.
const writeTimeout = 70 * time.Second

const mcpWriteTimeout = apiclient.MaxCallTimeout + 70*time.Second

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
		// WriteTimeout stays short. A long confirm extends its own response.
		WriteTimeout: writeTimeout,
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
// /mcp checks Host, then exchanges the caller token, before any MCP handling.
// The MCP handler is stateless: the SDK accepts protocol 2026-07-28 only in
// that mode, and this server keeps no session of its own.
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
		Version: "0.2.7",
	}, &mcp.ServerOptions{
		Logger:       logger,
		Capabilities: &mcp.ServerCapabilities{},
	})
	tools.Register(server, api)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		Logger:                       logger,
		PropagateRequestCancellation: true,
		// hostCheck allows the public Host on this loopback listener.
		// The SDK check would reject it, so it is off.
		DisableLocalhostProtection: true,
	})

	checked, err := hostCheck(cfg.PublicOrigin, logger, exchanger.RequireDelegated(cfg.PublicOrigin, withWriteBudget(mcpHandler)))
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("GET /.well-known/oauth-protected-resource", auth.ProtectedResource(cfg.PublicOrigin, cfg.APIURL))
	mux.Handle("/mcp", checked)
	return mux, nil
}

// withWriteBudget lets a long confirm extend this response's write deadline.
// The deadline stays at the server WriteTimeout until that confirm asks.
func withWriteBudget(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fn := func(budget time.Duration) error {
			if budget <= 0 {
				return nil
			}
			limit := budget + writeTimeout
			if limit > mcpWriteTimeout {
				limit = mcpWriteTimeout
			}
			return http.NewResponseController(w).SetWriteDeadline(time.Now().Add(limit))
		}
		ctx := tools.WithWriteBudget(r.Context(), fn)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
