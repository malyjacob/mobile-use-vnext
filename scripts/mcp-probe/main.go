// Command mcp-probe verifies one MCP adapter entry of the device service from
// the outside. It reads only the private MCP configuration, connects through
// the official MCP SDK, and checks the public tool surface, one ordinary
// session round trip, cancellation cleanup and (for the HTTP entry) the
// adapter's own authentication boundary.
//
// The probe prints one PASS/FAIL line per check and a final RESULT line. It
// never prints configuration values, tokens, base64 image data, node trees,
// shell output or notification text. Exit codes: 0 all checks passed, 1 a
// check failed, 2 the service was not admissible before device work.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/client"
)

const (
	modeStdio     = "stdio"
	modeHTTP      = "http"
	modePRecovery = "p-recovery"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses the flags, loads the private configuration and dispatches one
// mode. It returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mcp-probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "private MCP configuration (required)")
	binaryPath := flags.String("binary", "build/device-mcp.exe", "MCP adapter executable")
	transport := flags.String("transport", modeStdio, "stdio, http or p-recovery")
	timeoutSeconds := flags.Int("timeout", 180, "overall probe timeout in seconds")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "mcp-probe: -config is required")
		return 1
	}
	if *timeoutSeconds <= 0 {
		fmt.Fprintln(stderr, "mcp-probe: -timeout must be positive")
		return 1
	}
	mode := *transport
	switch mode {
	case modeStdio, modeHTTP, modePRecovery:
	default:
		fmt.Fprintln(stderr, "mcp-probe: -transport must be stdio, http or p-recovery")
		return 1
	}

	cfg, err := loadConfig(*configPath, mode)
	reporter := newReporter(stdout)
	if err != nil {
		reporter.fail("config", err.Error())
		return reporter.finish(false)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSeconds)*time.Second)
	defer cancel()

	if mode == modePRecovery {
		return runPRecovery(ctx, cfg, reporter)
	}
	return runAdapterMode(ctx, cfg, reporter, mode, *binaryPath, *configPath)
}

// runAdapterMode connects to the adapter over the requested transport and runs
// the shared MCP checks. The HTTP entry additionally proves its bearer and
// Origin boundary with raw requests before any tool call.
func runAdapterMode(ctx context.Context, cfg *config, reporter *reporter, mode, binaryPath, configPath string) int {
	service, err := client.New(client.Config{
		BaseURL:    cfg.Service.BaseURL,
		AgentToken: cfg.Service.AgentToken,
	})
	if err != nil {
		reporter.fail("config", "service_client_invalid")
		return reporter.finish(false)
	}
	probe := &probe{reporter: reporter, service: service}

	if mode == modeHTTP {
		child, childErr := startHTTPChild(ctx, binaryPath, configPath, cfg.HTTP.Listen)
		if childErr != nil {
			reporter.fail("connect", childErr.reason, childErr.fields...)
			return reporter.finish(false)
		}
		defer child.stop()
		if !runNegativeHTTPChecks(ctx, cfg, reporter) {
			return reporter.finish(false)
		}
		session, stop, connectErr := connectHTTPAdapter(ctx, cfg)
		if connectErr != nil {
			reporter.fail("connect", connectErr.reason, connectErr.fields...)
			return reporter.finish(false)
		}
		defer stop()
		probe.session = session
		reporter.pass("connect", "transport=http")
	} else {
		session, stop, connectErr := connectStdioAdapter(ctx, binaryPath, configPath)
		if connectErr != nil {
			reporter.fail("connect", connectErr.reason, connectErr.fields...)
			return reporter.finish(false)
		}
		defer stop()
		probe.session = session
		reporter.pass("connect", "transport=stdio")
	}

	return reporter.finish(probe.runChecks(ctx))
}
