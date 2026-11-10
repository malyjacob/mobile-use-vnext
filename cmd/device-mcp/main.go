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
	"io"
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
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv))
}

func run(args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	flags := flag.NewFlagSet("device-mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "private MCP adapter config")
	transport := flags.String("transport", "", "transport override: stdio or http")
	resultModeValue := flags.String("result-mode", "", "default result mode: both, text, or structured")
	showVersion := flags.Bool("version", false, "print release version")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, protocol.ReleaseVersion)
		return 0
	}
	resultMode, err := configuredResultMode(flags, *resultModeValue, lookupEnv)
	if err != nil {
		fmt.Fprintln(stderr, "device-mcp: invalid result mode:", err)
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "device-mcp: -config is required")
		return 2
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, "device-mcp: configuration rejected:", err)
		return 2
	}
	if *transport != "" {
		if *transport != "stdio" && *transport != "http" {
			fmt.Fprintln(stderr, "device-mcp: -transport must be stdio or http")
			return 2
		}
		cfg.Transport = *transport
	}
	if cfg.Transport == "http" && cfg.HTTP.Listen == "" {
		fmt.Fprintln(stderr, "device-mcp: http transport requires http.listen")
		return 2
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	service, err := client.New(client.Config{
		BaseURL: cfg.Service.BaseURL, AgentToken: cfg.Service.AgentToken,
	})
	if err != nil {
		logger.Error("mcp.config_failed", "code", "service_client")
		return 2
	}
	server, err := mcpserver.NewServer(mcpserver.Options{
		Client: service, Logger: logger, Version: protocol.ReleaseVersion,
		DefaultResultMode: resultMode,
	})
	if err != nil {
		logger.Error("mcp.start_failed", "code", "tool_registration")
		return 3
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.Transport == "stdio" {
		logMCPStart(logger, "stdio", resultMode)
		if err := server.Run(ctx, &sdk.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("mcp.stopped", "code", "stdio")
			return 1
		}
		return 0
	}
	return serveHTTP(ctx, cfg, server, logger, resultMode)
}

func configuredResultMode(flags *flag.FlagSet, flagValue string, lookupEnv func(string) (string, bool)) (mcpserver.ResultMode, error) {
	if flags == nil || lookupEnv == nil {
		return mcpserver.ResultModeBoth, errors.New("result mode configuration is unavailable")
	}
	explicit := false
	flags.Visit(func(current *flag.Flag) {
		if current.Name == "result-mode" {
			explicit = true
		}
	})
	if explicit {
		return mcpserver.ParseResultMode(flagValue)
	}
	if value, present := lookupEnv(mcpserver.ResultModeEnv); present {
		return mcpserver.ParseResultMode(value)
	}
	return mcpserver.ResultModeBoth, nil
}

func logMCPStart(logger *slog.Logger, transport string, resultMode mcpserver.ResultMode) {
	logger.Info("mcp.start", "transport", transport, "tools", 12, "result_mode", resultMode.String())
}

// serveHTTP runs the Streamable HTTP entry behind its own bearer identity and
// origin allowlist. The MCP session is stateless per request, so one client
// cannot inherit another client's MCP session or tool ownership.
func serveHTTP(ctx context.Context, cfg *Config, server *sdk.Server, logger *slog.Logger, resultMode mcpserver.ResultMode) int {
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
	logMCPStart(logger, "http", resultMode)
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
