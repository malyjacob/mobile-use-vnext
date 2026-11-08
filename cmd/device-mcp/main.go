// Command device-mcp is the MCP adapter. Both transports share one tool
// registration, schema, strict admission and result projection from
// internal/mcp. stdout carries only MCP protocol frames in stdio mode; every
// diagnostic goes to stderr. The service Agent credential stays in the private
// configuration and is never a tool parameter or a log field.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/malyjacob/mobile-use-vnext/internal/client"
	mcpserver "github.com/malyjacob/mobile-use-vnext/internal/mcp"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func main() {
	os.Exit(run())
}

func run() int {
	configPath := flag.String("config", "", "private MCP adapter config")
	transport := flag.String("transport", "", "transport override: stdio or http")
	showVersion := flag.Bool("version", false, "print release version")
	flag.Parse()
	if *showVersion {
		fmt.Fprintln(os.Stdout, protocol.ReleaseVersion)
		return 0
	}
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "device-mcp: -config is required")
		return 2
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "device-mcp: configuration rejected:", err)
		return 2
	}
	if *transport != "" {
		if *transport != "stdio" && *transport != "http" {
			fmt.Fprintln(os.Stderr, "device-mcp: -transport must be stdio or http")
			return 2
		}
		cfg.Transport = *transport
	}
	if cfg.Transport == "http" && cfg.HTTP.Listen == "" {
		fmt.Fprintln(os.Stderr, "device-mcp: http transport requires http.listen")
		return 2
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	service, err := client.New(client.Config{
		BaseURL: cfg.Service.BaseURL, AgentToken: cfg.Service.AgentToken,
	})
	if err != nil {
		logger.Error("mcp.config_failed", "code", "service_client")
		return 2
	}
	server, err := mcpserver.NewServer(mcpserver.Options{
		Client: service, Logger: logger, Version: protocol.ReleaseVersion,
	})
	if err != nil {
		logger.Error("mcp.start_failed", "code", "tool_registration")
		return 3
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.Transport == "stdio" {
		logger.Info("mcp.start", "transport", "stdio", "tools", 12)
		if err := server.Run(ctx, &sdk.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("mcp.stopped", "code", "stdio")
			return 1
		}
		return 0
	}
	return serveHTTP(ctx, cfg, server, logger)
}

// serveHTTP runs the Streamable HTTP entry behind its own bearer identity and
// origin allowlist. The MCP session is stateless per request, so one client
// cannot inherit another client's MCP session or tool ownership.
func serveHTTP(ctx context.Context, cfg *Config, server *sdk.Server, logger *slog.Logger) int {
	handler := mcpserver.NewHTTPHandler(server, mcpserver.HTTPConfig{
		Token:          cfg.HTTP.Token,
		AllowedOrigins: cfg.HTTP.AllowedOrigins,
	})
	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 * 1024,
	}
	listener, err := net.Listen("tcp", cfg.HTTP.Listen)
	if err != nil {
		logger.Error("mcp.start_failed", "code", "listen")
		return 4
	}
	logger.Info("mcp.start", "transport", "http", "tools", 12)
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("mcp.stopped", "code", "http")
			return 1
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	return 0
}
