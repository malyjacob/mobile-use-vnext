package main

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{},"clientInfo":{"name":"mcp-probe","version":"1"}}}`

// newRawHTTPClient builds a bounded client for the raw authentication checks.
// It never follows a redirect and never uses an environment proxy.
func newRawHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:       nil,
			DialContext: dialer.DialContext,
		},
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// rawPost sends one minimal JSON-RPC initialize request and returns only the
// HTTP status. No body is read or printed.
func rawPost(ctx context.Context, client *http.Client, endpoint, body, token, origin string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	_ = response.Body.Close()
	return response.StatusCode, nil
}

// runNegativeHTTPChecks proves the adapter's HTTP entry enforces its own
// bearer token and Origin allowlist before any tool call.
func runNegativeHTTPChecks(ctx context.Context, cfg *config, reporter *reporter) bool {
	endpoint := "http://" + cfg.HTTP.Listen + "/mcp"
	client := newRawHTTPClient()

	status, err := rawPost(ctx, client, endpoint, initializeBody, "", "")
	if err != nil {
		reporter.fail("auth_missing", "request_failed")
		return false
	}
	if status != http.StatusUnauthorized {
		reporter.fail("auth_missing", "unexpected_status", field("status", strconv.Itoa(status)))
		return false
	}
	reporter.pass("auth_missing", "status=401")

	status, err = rawPost(ctx, client, endpoint, initializeBody, cfg.HTTP.Token, "http://evil.example")
	if err != nil {
		reporter.fail("origin_denied", "request_failed")
		return false
	}
	if status != http.StatusForbidden {
		reporter.fail("origin_denied", "unexpected_status", field("status", strconv.Itoa(status)))
		return false
	}
	reporter.pass("origin_denied", "status=403")

	allowedOrigin := ""
	if len(cfg.HTTP.AllowedOrigins) > 0 {
		allowedOrigin = cfg.HTTP.AllowedOrigins[0]
	}
	status, err = rawPost(ctx, client, endpoint, initializeBody, cfg.HTTP.Token, allowedOrigin)
	if err != nil {
		reporter.fail("origin_allowed", "request_failed")
		return false
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		reporter.fail("origin_allowed", "unexpected_status", field("status", strconv.Itoa(status)))
		return false
	}
	reporter.pass("origin_allowed", field("status", strconv.Itoa(status)))

	status, err = rawPost(ctx, client, endpoint, initializeBody, cfg.Service.AgentToken, "")
	if err != nil {
		reporter.fail("agent_token_rejected", "request_failed")
		return false
	}
	if status != http.StatusUnauthorized {
		reporter.fail("agent_token_rejected", "unexpected_status", field("status", strconv.Itoa(status)))
		return false
	}
	reporter.pass("agent_token_rejected", "status=401")
	return true
}
