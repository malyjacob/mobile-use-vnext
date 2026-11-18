// Command mcp-probe verifies one MCP adapter entry of the device service from
// the outside. It reads only the private MCP configuration, connects through
// the official MCP SDK, and checks the public 13-tool surface, one ordinary
// session round trip, the unified app tool's read-only and controlled
// lifecycle branches, cancellation cleanup and (for the HTTP entries) the
// adapter's own authentication boundary. The http-remote transport only
// connects to an already running phone adapter through its private loopback
// forwarding entry; it never starts or stops a remote process. The
// app-lifecycle suite mutates only the dedicated test-target package and never
// switches the session display mode.
//
// The probe prints one PASS/FAIL/SKIP line per check and a final RESULT line.
// It never prints configuration values, tokens, base64 image data, node trees,
// shell output or notification text. Exit codes: 0 all checks passed, 1 a
// check failed, 2 the service was not admissible before device work.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/client"
)

const (
	modeStdio      = "stdio"
	modeHTTP       = "http"
	modeHTTPRemote = "http-remote"
	modePRecovery  = "p-recovery"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// manifestPaths collects -manifest flags; the same release/host-tools manifest
// fragment can be supplied more than once and every one must agree.
type manifestPaths []string

func (m *manifestPaths) String() string { return strings.Join(*m, ",") }

func (m *manifestPaths) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("empty manifest path")
	}
	*m = append(*m, value)
	return nil
}

// run parses the flags, loads the private configuration and dispatches one
// mode. It returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mcp-probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "private MCP configuration (required)")
	binaryPath := flags.String("binary", "build/device-mcp.exe", "MCP adapter executable")
	transport := flags.String("transport", modeStdio, "stdio, http, http-remote or p-recovery")
	resultMode := flags.String("result-mode", resultModeBoth, "both, text or structured")
	suite := flags.String("suite", suiteSmoke, "smoke or app-lifecycle")
	appTarget := flags.String("app-target", controlledAppPackage, "controlled test package for app-lifecycle")
	timeoutSeconds := flags.Int("timeout", 180, "overall probe timeout in seconds")
	var manifests manifestPaths
	flags.Var(&manifests, "manifest", "release or host-tools manifest to compare the catalog fingerprint (repeatable)")
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
	case modeStdio, modeHTTP, modeHTTPRemote, modePRecovery:
	default:
		fmt.Fprintln(stderr, "mcp-probe: -transport must be stdio, http, http-remote or p-recovery")
		return 1
	}
	selectedResultMode, ok := parseResultMode(*resultMode)
	if !ok {
		fmt.Fprintln(stderr, "mcp-probe: -result-mode must be both, text or structured")
		return 1
	}
	selectedSuite := *suite
	switch selectedSuite {
	case suiteSmoke, suiteAppLifecycle:
	default:
		fmt.Fprintln(stderr, "mcp-probe: -suite must be smoke or app-lifecycle")
		return 1
	}
	if !validPackageName(*appTarget) {
		fmt.Fprintln(stderr, "mcp-probe: -app-target must be one Android package name")
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
	return runAdapterMode(ctx, cfg, reporter, mode, *binaryPath, *configPath,
		selectedResultMode, selectedSuite, *appTarget, manifests)
}

func parseResultMode(value string) (string, bool) {
	switch value {
	case resultModeBoth, resultModeText, resultModeStructured:
		return value, true
	default:
		return "", false
	}
}

// runAdapterMode connects to the adapter over the requested transport and runs
// the shared MCP checks. The host HTTP entry starts its own bounded child and
// additionally proves the bearer and Origin boundary. The http-remote entry
// never starts or stops a process: it must reach an already running phone
// adapter through the private loopback forwarding entry.
func runAdapterMode(ctx context.Context, cfg *config, reporter *reporter,
	mode, binaryPath, configPath, resultMode, suite, appTarget string,
	manifests []string) int {
	service, err := client.New(client.Config{
		BaseURL:    cfg.Service.BaseURL,
		AgentToken: cfg.Service.AgentToken,
	})
	if err != nil {
		reporter.fail("config", "service_client_invalid")
		return reporter.finish(false)
	}
	probe := &probe{
		reporter:   reporter,
		service:    service,
		resultMode: resultMode,
		suite:      suite,
		appTarget:  appTarget,
		manifests:  manifests,
	}

	switch mode {
	case modeHTTP:
		child, childErr := startHTTPChild(ctx, binaryPath, configPath, cfg.HTTP.Listen)
		if childErr != nil {
			reporter.fail("connect", childErr.reason, childErr.fields...)
			return reporter.finish(false)
		}
		defer child.stop()
		if !runNegativeHTTPChecks(ctx, cfg, reporter) {
			return reporter.finish(false)
		}
		session, stop, connectErr := connectHTTPAdapter(ctx, cfg, resultMode)
		if connectErr != nil {
			reporter.fail("connect", connectErr.reason, connectErr.fields...)
			return reporter.finish(false)
		}
		defer stop()
		probe.session = session
		probe.compatInitialize = compatibilityHTTPInitialize(cfg)
		reporter.pass("connect", "transport=http", field("result_mode", resultMode))
	case modeHTTPRemote:
		// Reuse the typed service client's health/contract check before any MCP
		// call. This never builds, starts or stops a process; a failed discover
		// means the configured forwarding entry is not the expected phone pair.
		instance, discoverErr := service.Discover(ctx)
		if discoverErr != nil {
			reporter.fail("service_remote", "discover_failed", field("code", clientErrorCode(discoverErr)))
			return reporter.finish(false)
		}
		reporter.pass("service_remote",
			"transport=http-remote", field("contract", instance.ContractVersion),
			field("release", instance.ReleaseVersion), "process_managed=false")
		if !runNegativeHTTPChecks(ctx, cfg, reporter) {
			return reporter.finish(false)
		}
		session, stop, connectErr := connectHTTPAdapter(ctx, cfg, resultMode)
		if connectErr != nil {
			reporter.fail("connect", connectErr.reason, connectErr.fields...)
			return reporter.finish(false)
		}
		defer stop()
		probe.session = session
		probe.compatInitialize = compatibilityHTTPInitialize(cfg)
		reporter.pass("connect", "transport=http-remote",
			field("result_mode", resultMode), "process_managed=false")
	default:
		session, stop, connectErr := connectStdioAdapter(ctx, binaryPath, configPath, resultMode)
		if connectErr != nil {
			reporter.fail("connect", connectErr.reason, connectErr.fields...)
			return reporter.finish(false)
		}
		defer stop()
		probe.session = session
		probe.compatInitialize = compatibilityStdioInitialize(binaryPath, configPath, resultMode)
		reporter.pass("connect", "transport=stdio", field("result_mode", resultMode))
	}

	return reporter.finish(probe.runChecks(ctx))
}
