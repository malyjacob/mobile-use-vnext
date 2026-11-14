package main

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Config is the private local service configuration. It never enters Git: the
// deployment writes it with mode 0600 on the phone.
type Config struct {
	Server struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	} `json:"server"`
	StateDir string `json:"state_dir"`
	ADB      struct {
		Path           string `json:"path"`
		Serial         string `json:"serial"`
		U2Port         int    `json:"u2_port"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	} `json:"adb"`
	Tokens struct {
		Agent   string `json:"agent"`
		Control string `json:"control"`
	} `json:"tokens"`
	AgentName      string `json:"agent_name"`
	ControlPackage string `json:"control_package"`
	U2JarPath      string `json:"u2_jar_path"`
	AppListDexPath string `json:"applist_dex_path"`
	ReleaseVersion string `json:"release_version"`
}

// maxADBTimeoutSeconds is the hard upper bound of the private per-command ADB
// timeout. It is the budget invariant of every synchronous acquisition, not a
// tuning knob: see the wait/tool budget chain in the interface documentation.
const maxADBTimeoutSeconds = 15

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, errors.New("config is not valid JSON")
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Host != "127.0.0.1" {
		return nil, errors.New("the service must bind to 127.0.0.1")
	}
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return nil, errors.New("server port is invalid")
	}
	if cfg.StateDir == "" {
		return nil, errors.New("state_dir is required")
	}
	if cfg.ADB.Path == "" {
		cfg.ADB.Path = "adb"
	}
	if strings.TrimSpace(cfg.ADB.Serial) == "" {
		return nil, errors.New("adb serial is required")
	}
	if cfg.ADB.U2Port == 0 {
		cfg.ADB.U2Port = 9008
	}
	if cfg.ADB.TimeoutSeconds <= 0 {
		cfg.ADB.TimeoutSeconds = 8
	}
	// The single hierarchy-dump ceiling is the floor of every acquisition. A
	// larger per-command ADB timeout would silently raise that floor and push
	// the worst-case synchronous tool result past the fixed 90s HTTP window.
	if cfg.ADB.TimeoutSeconds > maxADBTimeoutSeconds {
		return nil, fmt.Errorf("adb timeout_seconds must not exceed %d", maxADBTimeoutSeconds)
	}
	if cfg.Tokens.Agent == "" || cfg.Tokens.Control == "" {
		return nil, errors.New("both agent and control tokens are required")
	}
	if cfg.Tokens.Agent == cfg.Tokens.Control {
		return nil, errors.New("agent and control tokens must differ")
	}
	if cfg.AgentName == "" {
		cfg.AgentName = "agent"
	}
	if cfg.ControlPackage == "" {
		cfg.ControlPackage = "com.mobileuse.vnext"
	}
	if cfg.U2JarPath == "" {
		return nil, errors.New("u2_jar_path is required")
	}
	if _, err := os.Stat(cfg.U2JarPath); err != nil {
		return nil, errors.New("u2 jar artifact is missing")
	}
	if cfg.AppListDexPath == "" {
		cfg.AppListDexPath = filepath.Join(filepath.Dir(cfg.U2JarPath), "applist.dex")
	}
	if _, err := os.Stat(cfg.AppListDexPath); err != nil {
		return nil, errors.New("app list artifact is missing")
	}
	if cfg.ReleaseVersion == "" {
		cfg.ReleaseVersion = protocol.ReleaseVersion
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	return &cfg, nil
}

func (c *Config) adbTimeout() time.Duration {
	return time.Duration(c.ADB.TimeoutSeconds) * time.Second
}

func (c *Config) address() string {
	return net.JoinHostPort(c.Server.Host, fmt.Sprintf("%d", c.Server.Port))
}
