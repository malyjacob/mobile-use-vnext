package main

import (
	"encoding/json/v2"
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

// config mirrors the private MCP adapter configuration. The probe reads the
// same file as the adapter but keeps its own strict local shape; it never
// imports the command package.
type config struct {
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

// Fixed configuration reasons. None of them echoes the file content, a URL or
// a credential.
var (
	errConfigUnreadable     = errors.New("config_unreadable")
	errConfigInvalid        = errors.New("config_invalid")
	errConfigTransport      = errors.New("config_transport_invalid")
	errAgentTokenMissing    = errors.New("agent_token_missing")
	errServiceURLInvalid    = errors.New("service_url_invalid")
	errServiceURLScheme     = errors.New("service_url_scheme")
	errServiceURLExtra      = errors.New("service_url_extra")
	errServiceURLNotLocal   = errors.New("service_url_not_loopback")
	errHTTPTokenMissing     = errors.New("http_token_missing")
	errHTTPTokenNotDistinct = errors.New("http_token_not_distinct")
	errHTTPListenInvalid    = errors.New("http_listen_invalid")
	errHTTPListenNotLocal   = errors.New("http_listen_not_loopback")
	errOriginInvalid        = errors.New("allowed_origin_invalid")
)

// loadConfig reads the private configuration strictly: unknown members,
// duplicate names and type mismatches are rejected before any connection.
func loadConfig(path, mode string) (*config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errConfigUnreadable
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg, json.RejectUnknownMembers(true)); err != nil {
		return nil, errConfigInvalid
	}
	if cfg.Transport != "stdio" && cfg.Transport != "http" {
		return nil, errConfigTransport
	}
	// The phone forwarding entry is an HTTP MCP endpoint; a stdio configuration
	// cannot describe it and must never be silently treated as remote HTTP.
	if mode == modeHTTPRemote && cfg.Transport != "http" {
		return nil, errConfigTransport
	}
	if err := validateService(cfg.Service.BaseURL, cfg.Service.AgentToken); err != nil {
		return nil, err
	}
	if mode == modeHTTP || mode == modeHTTPRemote || cfg.Transport == "http" {
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

// validPackageName applies the public app schema's package shape. It is used
// for the controlled app-lifecycle target, not as a device capability check.
func validPackageName(value string) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	for _, segment := range strings.Split(value, ".") {
		if segment == "" {
			return false
		}
		for _, character := range segment {
			switch {
			case character >= 'a' && character <= 'z',
				character >= 'A' && character <= 'Z',
				character >= '0' && character <= '9',
				character == '_':
			default:
				return false
			}
		}
	}
	return true
}

// validateService requires a loopback service URL so the Agent credential can
// never be sent to a remote host by configuration mistake.
func validateService(raw, token string) error {
	if strings.TrimSpace(token) == "" {
		return errAgentTokenMissing
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return errServiceURLInvalid
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errServiceURLScheme
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errServiceURLExtra
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errServiceURLNotLocal
	}
	return nil
}

// validateHTTP requires a loopback listener and a host-side bearer token that
// is distinct from the service Agent credential.
func validateHTTP(listen, token, agentToken string) error {
	if strings.TrimSpace(token) == "" {
		return errHTTPTokenMissing
	}
	if token == agentToken {
		return errHTTPTokenNotDistinct
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return errHTTPListenInvalid
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errHTTPListenNotLocal
	}
	return nil
}

// validateOrigin accepts only scheme://host[:port] with no path or query.
func validateOrigin(origin string) error {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errOriginInvalid
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errOriginInvalid
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return errOriginInvalid
	}
	return nil
}
