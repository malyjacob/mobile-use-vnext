package mcp

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTPConfig configures the private Streamable HTTP entry point. Token is the
// bearer credential required on every request; AllowedOrigins is the exact
// origin allowlist used when a request carries an Origin header.
type HTTPConfig struct {
	Token          string
	AllowedOrigins []string
}

// NewHTTPHandler wraps the SDK Streamable HTTP handler with bearer
// authentication and an exact Origin allowlist. Every request builds its own
// authenticated MCP session (Stateless), and SSE responses stay enabled.
func NewHTTPHandler(server *sdk.Server, cfg HTTPConfig) http.Handler {
	allowedOrigins := append([]string(nil), cfg.AllowedOrigins...)
	streamable := sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{
			Stateless:                    true,
			PropagateRequestCancellation: true,
			MaxRequestBodyBytes:          1 << 20,
		},
	)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !authorized(request, cfg.Token) {
			writer.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		if origin := request.Header.Get("Origin"); origin != "" && !originAllowed(origin, allowedOrigins) {
			http.Error(writer, "forbidden", http.StatusForbidden)
			return
		}
		if _, err := ResultModeFromHeader(request.Header, ResultModeBoth); err != nil {
			http.Error(writer, "invalid result mode", http.StatusBadRequest)
			return
		}
		streamable.ServeHTTP(writer, request)
	})
}

// authorized compares the presented bearer token in constant time. An empty
// configured token never authorizes anyone.
func authorized(request *http.Request, expected string) bool {
	if expected == "" {
		return false
	}
	header := request.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	presented := header[len(prefix):]
	if presented == "" {
		return false
	}
	presentedHash := sha256.Sum256([]byte(presented))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(presentedHash[:], expectedHash[:]) == 1
}

// originAllowed compares exact scheme://host[:port] values, ignoring one or
// more trailing slashes. It never reflects or normalizes a caller origin.
func originAllowed(origin string, allowed []string) bool {
	normalized := strings.TrimRight(origin, "/")
	if normalized == "" {
		return false
	}
	for _, candidate := range allowed {
		if strings.TrimRight(candidate, "/") == normalized {
			return true
		}
	}
	return false
}
