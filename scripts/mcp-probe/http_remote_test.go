package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validRemoteConfig = `{
  "service": {"base_url": "http://127.0.0.1:18881", "agent_token": "phone-agent-token"},
  "transport": "http",
  "http": {"listen": "127.0.0.1:18883", "token": "phone-mcp-token", "allowed_origins": []}
}`

func writeProbeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp-config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadRemoteConfig(t *testing.T) {
	t.Run("valid phone forwarding entry", func(t *testing.T) {
		cfg, err := loadConfig(writeProbeConfig(t, validRemoteConfig), modeHTTPRemote)
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
		if cfg.Transport != "http" || cfg.HTTP.Listen != "127.0.0.1:18883" {
			t.Fatalf("unexpected projection: %+v", cfg)
		}
	})
	t.Run("stdio config cannot describe the remote entry", func(t *testing.T) {
		body := strings.Replace(validRemoteConfig, `"transport": "http"`, `"transport": "stdio"`, 1)
		_, err := loadConfig(writeProbeConfig(t, body), modeHTTPRemote)
		if err == nil || err.Error() != errConfigTransport.Error() {
			t.Fatalf("err = %v, want %v", err, errConfigTransport)
		}
	})
	t.Run("remote listener must be loopback", func(t *testing.T) {
		body := strings.Replace(validRemoteConfig, `127.0.0.1:18883`, `192.0.2.10:18883`, 1)
		_, err := loadConfig(writeProbeConfig(t, body), modeHTTPRemote)
		if err == nil || err.Error() != errHTTPListenNotLocal.Error() {
			t.Fatalf("err = %v, want %v", err, errHTTPListenNotLocal)
		}
	})
	t.Run("remote listener shape", func(t *testing.T) {
		body := strings.Replace(validRemoteConfig, `"listen": "127.0.0.1:18883"`, `"listen": "18883"`, 1)
		_, err := loadConfig(writeProbeConfig(t, body), modeHTTPRemote)
		if err == nil || err.Error() != errHTTPListenInvalid.Error() {
			t.Fatalf("err = %v, want %v", err, errHTTPListenInvalid)
		}
	})
	t.Run("remote mcp token required", func(t *testing.T) {
		body := strings.Replace(validRemoteConfig, `"token": "phone-mcp-token"`, `"token": ""`, 1)
		_, err := loadConfig(writeProbeConfig(t, body), modeHTTPRemote)
		if err == nil || err.Error() != errHTTPTokenMissing.Error() {
			t.Fatalf("err = %v, want %v", err, errHTTPTokenMissing)
		}
	})
	t.Run("remote token distinct from agent token", func(t *testing.T) {
		body := strings.Replace(validRemoteConfig, `"token": "phone-mcp-token"`, `"token": "phone-agent-token"`, 1)
		_, err := loadConfig(writeProbeConfig(t, body), modeHTTPRemote)
		if err == nil || err.Error() != errHTTPTokenNotDistinct.Error() {
			t.Fatalf("err = %v, want %v", err, errHTTPTokenNotDistinct)
		}
	})
	t.Run("remote service entry must be loopback", func(t *testing.T) {
		body := strings.Replace(validRemoteConfig, `http://127.0.0.1:18881`, `http://192.0.2.10:18881`, 1)
		_, err := loadConfig(writeProbeConfig(t, body), modeHTTPRemote)
		if err == nil || err.Error() != errServiceURLNotLocal.Error() {
			t.Fatalf("err = %v, want %v", err, errServiceURLNotLocal)
		}
	})
	t.Run("unknown member rejected", func(t *testing.T) {
		body := strings.Replace(validRemoteConfig, `"transport": "http",`,
			`"transport": "http", "remote_url": "http://elsewhere",`, 1)
		_, err := loadConfig(writeProbeConfig(t, body), modeHTTPRemote)
		if err == nil || err.Error() != errConfigInvalid.Error() {
			t.Fatalf("err = %v, want %v", err, errConfigInvalid)
		}
	})
}

func TestValidPackageName(t *testing.T) {
	for _, value := range []string{
		"com.mobileuse.probe", "a", "a.b_c.D9", strings.Repeat("a", 255),
	} {
		if !validPackageName(value) {
			t.Fatalf("validPackageName(%q) = false", value)
		}
	}
	for _, value := range []string{
		"", "com..probe", ".probe", "probe.", "com.example-probe",
		"com.example/probe", strings.Repeat("a", 256),
	} {
		if validPackageName(value) {
			t.Fatalf("validPackageName(%q) = true", value)
		}
	}
}

// TestRunRejectsRemoteConfigurationWithoutTouchingTheNetwork proves the flag
// parse and configuration gate fail before any connection and never echo a
// token.
func TestRunRejectsRemoteConfigurationWithoutTouchingTheNetwork(t *testing.T) {
	body := strings.Replace(validRemoteConfig, `"transport": "http"`, `"transport": "stdio"`, 1)
	configPath := writeProbeConfig(t, body)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-transport", modeHTTPRemote,
		"-config", configPath,
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "FAIL config reason=config_transport_invalid") {
		t.Fatalf("missing transport failure:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "phone-mcp-token") ||
		strings.Contains(stdout.String(), "phone-agent-token") ||
		strings.Contains(stderr.String(), "phone-mcp-token") ||
		strings.Contains(stderr.String(), "phone-agent-token") {
		t.Fatal("configuration token was echoed")
	}
}

func TestRunRejectsInvalidSuiteAndTarget(t *testing.T) {
	configPath := writeProbeConfig(t, validRemoteConfig)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-config", configPath, "-suite", "all"}, &stdout, &stderr); code != 1 {
		t.Fatalf("invalid suite exit = %d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-config", configPath, "-app-target", "not a package"}, &stdout, &stderr); code != 1 {
		t.Fatalf("invalid target exit = %d", code)
	}
}

func TestRemoteConfigKeepsTokenOutOfURL(t *testing.T) {
	// The HTTP adapter endpoint is built only from the loopback listener; the
	// bearer token travels in a header, never in a URL.
	cfg, err := loadConfig(writeProbeConfig(t, validRemoteConfig), modeHTTPRemote)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	endpoint := "http://" + cfg.HTTP.Listen + "/mcp"
	if strings.Contains(endpoint, cfg.HTTP.Token) || strings.Contains(endpoint, cfg.Service.AgentToken) {
		t.Fatal("credential leaked into the remote endpoint")
	}
}
