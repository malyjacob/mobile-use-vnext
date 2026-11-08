package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Config is the private MCP adapter configuration. It binds the adapter to one
// trusted service Agent credential; it never enters Git, is never printed and
// never becomes an MCP tool parameter.
type Config struct {
	Service struct {
		BaseURL    string `json:"base_url"`
		AgentToken string `json:"agent_token"`
	} `json:"service"`
	Transport string `json:"transport"`
	HTTP      struct {
		Listen         string   `json:"listen"`
		Token          string   `json:"token"`
		AllowedOrigins []string `json:"allowed_origins"`
	} `json:"http"`
}

// loadConfig reads the strict private configuration and rejects unsafe values.
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read config: the private MCP config is unavailable")
	}
	var cfg Config
	if err := protocol.Decode(data, &cfg); err != nil {
		return nil, errors.New("config: unknown, duplicate or mistyped member")
	}
	if cfg.Transport != "stdio" && cfg.Transport != "http" {
		return nil, errors.New("config: transport must be stdio or http")
	}
	if err := validateService(cfg.Service.BaseURL, cfg.Service.AgentToken); err != nil {
		return nil, err
	}
	if cfg.Transport == "http" {
		if err := validateHTTP(cfg.HTTP.Listen, cfg.HTTP.Token, cfg.Service.AgentToken); err != nil {
			return nil, err
		}
		for _, origin := range cfg.HTTP.AllowedOrigins {
			if err := validateOrigin(origin); err != nil {
				return nil, err
			}
		}
	}
	return &cfg, nil
}

// validateService requires a loopback service URL so the Agent credential can
// never be sent to a remote host by configuration mistake.
func validateService(raw, token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("config: service agent_token is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return errors.New("config: service base_url is invalid")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("config: service base_url must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("config: service base_url must not carry credentials or query")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("config: service base_url must target the loopback interface")
	}
	return nil
}

// validateHTTP requires a loopback listener and a host-side bearer token that
// is distinct from the service Agent credential.
func validateHTTP(listen, token, agentToken string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("config: http token is required")
	}
	if token == agentToken {
		return errors.New("config: the MCP bearer token must differ from the service agent token")
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return errors.New("config: http listen must be host:port")
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("config: http listen must target the loopback interface")
	}
	return nil
}

// validateOrigin accepts only a scheme, host and optional port.
func validateOrigin(origin string) error {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("config: allowed origin %q is invalid", origin)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("config: allowed origins must use http or https")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return fmt.Errorf("config: allowed origin %q must be scheme://host[:port] only", origin)
	}
	return nil
}
