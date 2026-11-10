package main

import (
	"bytes"
	"flag"
	"io"
	"log/slog"
	"strings"
	"testing"

	mcpserver "github.com/malyjacob/mobile-use-vnext/internal/mcp"
)

func configuredModeForTest(t *testing.T, args []string, environment map[string]string) (mcpserver.ResultMode, error) {
	t.Helper()
	flags := flag.NewFlagSet("device-mcp-test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	value := flags.String("result-mode", "", "result mode")
	if err := flags.Parse(args); err != nil {
		t.Fatalf("FlagSet.Parse(%v): %v", args, err)
	}
	return configuredResultMode(flags, *value, func(key string) (string, bool) {
		value, present := environment[key]
		return value, present
	})
}

func TestResultModeDefaultAndPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		environment map[string]string
		want        mcpserver.ResultMode
		wantErr     bool
	}{
		{name: "default both", want: mcpserver.ResultModeBoth},
		{name: "environment text", environment: map[string]string{mcpserver.ResultModeEnv: "text"}, want: mcpserver.ResultModeText},
		{name: "flag structured over environment text", args: []string{"-result-mode=structured"}, environment: map[string]string{mcpserver.ResultModeEnv: "text"}, want: mcpserver.ResultModeStructured},
		{name: "flag text over invalid environment", args: []string{"-result-mode=text"}, environment: map[string]string{mcpserver.ResultModeEnv: "legacy"}, want: mcpserver.ResultModeText},
		{name: "empty environment rejected", environment: map[string]string{mcpserver.ResultModeEnv: ""}, wantErr: true},
		{name: "invalid environment rejected", environment: map[string]string{mcpserver.ResultModeEnv: "Text"}, wantErr: true},
		{name: "explicit empty beats valid environment", args: []string{"-result-mode="}, environment: map[string]string{mcpserver.ResultModeEnv: "text"}, wantErr: true},
		{name: "explicit invalid beats valid environment", args: []string{"-result-mode=legacy"}, environment: map[string]string{mcpserver.ResultModeEnv: "text"}, wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := configuredModeForTest(t, test.args, test.environment)
			if test.wantErr {
				if err == nil {
					t.Fatalf("configured result mode = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("configured result mode: %v", err)
			}
			if got != test.want {
				t.Fatalf("configured result mode = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRunRejectsInvalidResultModeWithExitTwo(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		environment map[string]string
	}{
		{
			name:        "explicit empty",
			args:        []string{"-config", "unused.json", "-result-mode="},
			environment: map[string]string{mcpserver.ResultModeEnv: "text"},
		},
		{
			name:        "invalid environment",
			args:        []string{"-config", "unused.json"},
			environment: map[string]string{mcpserver.ResultModeEnv: "legacy"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(test.args, &stdout, &stderr, func(key string) (string, bool) {
				value, present := test.environment[key]
				return value, present
			})
			if code != 2 {
				t.Fatalf("run() = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), "invalid result mode") {
				t.Fatalf("stderr = %q, want invalid result mode", stderr.String())
			}
		})
	}
}

func TestLogMCPStartIncludesResultMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []mcpserver.ResultMode{
		mcpserver.ResultModeBoth,
		mcpserver.ResultModeText,
		mcpserver.ResultModeStructured,
	} {
		mode := mode
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&output, nil))
			logMCPStart(logger, "stdio", mode)
			logged := output.String()
			if !strings.Contains(logged, "msg=mcp.start") {
				t.Fatalf("log = %q, want mcp.start", logged)
			}
			if !strings.Contains(logged, "result_mode="+mode.String()) {
				t.Fatalf("log = %q, want result_mode=%s", logged, mode.String())
			}
		})
	}
}
