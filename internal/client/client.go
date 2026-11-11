// Package client is the typed HTTP client for the mobile-use-vnext device
// service. It sends only canonical protocol request bodies, applies the
// ordinary-contract headers, performs at most one HTTP request per logical
// call and projects service results, observation images and controlled errors
// for the MCP adapter and verification code.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const (
	defaultTimeout   = 90 * time.Second
	maxResponseBytes = 8 << 20
	maxHealthBytes   = 64 << 10

	authorizationHeader = "Authorization"
	contractHeader      = "X-Mobile-Use-Contract"
	operationHeader     = "X-Mobile-Use-Operation"
)

// Config is the immutable client configuration.
type Config struct {
	BaseURL    string        // e.g. http://127.0.0.1:18881
	AgentToken string        // bearer credential for /api/*
	HTTPClient *http.Client  // optional; default a fresh client with no redirects
	Timeout    time.Duration // optional per-call deadline, default 90s
}

// Image is one decoded observation image. Base64 is the HTTP transport carrier
// only; the client decodes it once and clears it from the structured result.
type Image struct {
	MediaType  string
	Data       []byte // decoded bytes
	Width      int
	Height     int
	CapturedAt string
	ReadOnly   bool
	Screen     *protocol.Screen
}

// Response is one service response. OperationID is the P allocated or supplied
// for this call and is empty for status, inspect and diagnose.
type Response struct {
	HTTPStatus  int
	OperationID string // P allocated/used for this call; "" for status/inspect/diagnose
	Result      *protocol.Result
	Image       *Image // decoded from observation.image.base64; Base64 is cleared on Result
}

// Instance is the read-only discovery projection built from GET /health and
// POST /api/status {}.
type Instance struct {
	ContractVersion   string
	ServiceInstanceID string
	ReleaseVersion    string
	CanStart          bool
	BlockingCode      string
	StartOperationID  string
	Session           *protocol.Session
	Cleanup           *protocol.Cleanup
}

// callOptions collects per-call overrides.
type callOptions struct {
	operationID string
	explicit    bool
}

// CallOption overrides one call's pre-assigned operation ID.
type CallOption func(*callOptions)

// WithOperationID uses id instead of a generated operation ID. The ID is
// validated before any request is sent.
func WithOperationID(id string) CallOption {
	return func(options *callOptions) {
		options.operationID = id
		options.explicit = true
	}
}

// Client is safe for concurrent use. It owns no business state and keeps no
// second copy of the service authority.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	timeout    time.Duration
}

// New validates the configuration and builds a client. Redirects are never
// followed, including on a caller-supplied HTTP client.
func New(cfg Config) (*Client, error) {
	baseURL, err := validateBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if cfg.AgentToken == "" {
		return nil, errors.New("client: agent token is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	httpClient := &http.Client{}
	if cfg.HTTPClient != nil {
		// Copy so the caller's client is not mutated.
		clone := *cfg.HTTPClient
		httpClient = &clone
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{
		baseURL:    baseURL,
		token:      cfg.AgentToken,
		httpClient: httpClient,
		timeout:    timeout,
	}, nil
}

// validateBaseURL accepts an absolute http(s) URL with a host and no user
// information, query or fragment. Trailing path slashes are trimmed so tool
// paths can be appended once.
func validateBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("client: base URL is not a valid URL")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("client: base URL must use http or https")
	}
	if parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" {
		return "", errors.New("client: base URL must include a host")
	}
	if parsed.User != nil {
		return "", errors.New("client: base URL must not contain user information")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return "", errors.New("client: base URL must not contain a query")
	}
	if parsed.Fragment != "" || parsed.RawFragment != "" || strings.Contains(raw, "#") {
		return "", errors.New("client: base URL must not contain a fragment")
	}
	parsed.Scheme = scheme
	if parsed.Path == "/" {
		parsed.Path = ""
	} else {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
	}
	if parsed.RawPath != "" {
		parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	}
	return parsed.String(), nil
}

// Start calls POST /api/start with the canonical empty object.
func (c *Client) Start(ctx context.Context, opts ...CallOption) (*Response, error) {
	operation, operationErr := c.newOperation(opts)
	if operationErr != nil {
		return nil, operationErr
	}
	body, err := (&protocol.StartRequest{}).Encode()
	if err != nil {
		return nil, invalidRequest(operation, messageInvalidRequest)
	}
	return c.post(ctx, "start", body, operation)
}

// Status calls POST /api/status. An empty reference encodes {}; status never
// uses or allocates an operation ID.
func (c *Client) Status(ctx context.Context, reference string) (*Response, error) {
	request := &protocol.StatusRequest{Reference: reference, HasRef: reference != ""}
	body, err := request.Encode()
	if err != nil {
		return nil, invalidRequest("", messageInvalidRequest)
	}
	return c.post(ctx, "status", body, "")
}

// Observe calls POST /api/observe.
func (c *Client) Observe(ctx context.Context, request *protocol.ObserveRequest, opts ...CallOption) (*Response, error) {
	operation, operationErr := c.newOperation(opts)
	if operationErr != nil {
		return nil, operationErr
	}
	if request == nil {
		return nil, invalidRequest(operation, messageMissingRequest)
	}
	body, err := request.Encode()
	if err != nil {
		return nil, invalidRequest(operation, messageInvalidRequest)
	}
	return c.post(ctx, "observe", body, operation)
}

// Act calls POST /api/act.
func (c *Client) Act(ctx context.Context, request *protocol.ActRequest, opts ...CallOption) (*Response, error) {
	operation, operationErr := c.newOperation(opts)
	if operationErr != nil {
		return nil, operationErr
	}
	if request == nil {
		return nil, invalidRequest(operation, messageMissingRequest)
	}
	body, err := request.Encode()
	if err != nil {
		return nil, invalidRequest(operation, messageInvalidRequest)
	}
	return c.post(ctx, "act", body, operation)
}

// End calls POST /api/end.
func (c *Client) End(ctx context.Context, reference string, opts ...CallOption) (*Response, error) {
	operation, operationErr := c.newOperation(opts)
	if operationErr != nil {
		return nil, operationErr
	}
	body, err := (&protocol.EndRequest{Reference: reference}).Encode()
	if err != nil {
		return nil, invalidRequest(operation, messageInvalidRequest)
	}
	return c.post(ctx, "end", body, operation)
}

// Inspect calls POST /api/inspect. It is read-only and never uses a P.
func (c *Client) Inspect(ctx context.Context, request *protocol.InspectRequest) (*Response, error) {
	if request == nil {
		return nil, invalidRequest("", messageMissingRequest)
	}
	body, err := request.Encode()
	if err != nil {
		return nil, invalidRequest("", messageInvalidRequest)
	}
	return c.post(ctx, "inspect", body, "")
}

// Keepalive resets the session lease to two minutes from server application
// time. The omitted duration may shorten an existing lease, including start's
// five-minute lease. Success reports the new session.expires_at.
func (c *Client) Keepalive(ctx context.Context, reference string, opts ...CallOption) (*Response, error) {
	return c.keepalive(ctx, &protocol.KeepaliveRequest{Reference: reference}, opts)
}

// KeepaliveWithDuration resets the lease to durationMinutes (1–120) from server
// application time, possibly shortening it. Ordinary activity may extend it
// again. Reusing a recognizable P reads its original operation without updating
// the lease; a changed duration requires a new operation.
func (c *Client) KeepaliveWithDuration(ctx context.Context, reference string, durationMinutes int, opts ...CallOption) (*Response, error) {
	return c.keepalive(ctx, &protocol.KeepaliveRequest{
		Reference: reference, DurationMinutes: durationMinutes, HasDurationMinutes: true,
	}, opts)
}

func (c *Client) keepalive(ctx context.Context, request *protocol.KeepaliveRequest, opts []CallOption) (*Response, error) {
	operation, operationErr := c.newOperation(opts)
	if operationErr != nil {
		return nil, operationErr
	}
	body, err := request.Encode()
	if err != nil {
		return nil, invalidRequest(operation, messageInvalidRequest)
	}
	return c.post(ctx, "keepalive", body, operation)
}

// App calls POST /api/app with one strict kind branch. The kind decides the
// admitted members; Encode rejects a branch that is not fully specified before
// any request is sent. The pre-assigned P is retained for every kind.
func (c *Client) App(ctx context.Context, request *protocol.AppRequest, opts ...CallOption) (*Response, error) {
	operation, operationErr := c.newOperation(opts)
	if operationErr != nil {
		return nil, operationErr
	}
	if request == nil {
		return nil, invalidRequest(operation, messageMissingRequest)
	}
	body, err := request.Encode()
	if err != nil {
		return nil, invalidRequest(operation, messageInvalidRequest)
	}
	return c.post(ctx, "app", body, operation)
}

// Notify calls POST /api/notify.
func (c *Client) Notify(ctx context.Context, request *protocol.NotifyRequest, opts ...CallOption) (*Response, error) {
	operation, operationErr := c.newOperation(opts)
	if operationErr != nil {
		return nil, operationErr
	}
	if request == nil {
		return nil, invalidRequest(operation, messageMissingRequest)
	}
	body, err := request.Encode()
	if err != nil {
		return nil, invalidRequest(operation, messageInvalidRequest)
	}
	return c.post(ctx, "notify", body, operation)
}

// Shell calls POST /api/shell.
func (c *Client) Shell(ctx context.Context, request *protocol.ShellRequest, opts ...CallOption) (*Response, error) {
	operation, operationErr := c.newOperation(opts)
	if operationErr != nil {
		return nil, operationErr
	}
	if request == nil {
		return nil, invalidRequest(operation, messageMissingRequest)
	}
	body, err := request.Encode()
	if err != nil {
		return nil, invalidRequest(operation, messageInvalidRequest)
	}
	return c.post(ctx, "shell", body, operation)
}

// Diagnose calls POST /api/diagnose. It is read-only and never uses a P.
func (c *Client) Diagnose(ctx context.Context, request *protocol.DiagnoseRequest) (*Response, error) {
	if request == nil {
		return nil, invalidRequest("", messageMissingRequest)
	}
	body, err := request.Encode()
	if err != nil {
		return nil, invalidRequest("", messageInvalidRequest)
	}
	return c.post(ctx, "diagnose", body, "")
}

// Discover reads GET /health and then POST /api/status {}. It performs exactly
// those two read-only requests and never allocates a P.
func (c *Client) Discover(ctx context.Context) (*Instance, error) {
	health, healthStatus, healthErr := c.fetchHealth(ctx)
	if healthErr != nil {
		return nil, healthErr
	}
	if health.Status != "ok" {
		return nil, &Error{
			Code:       ErrorResponse,
			Message:    messageHealthStatus,
			HTTPStatus: healthStatus,
		}
	}
	if health.ContractVersion != protocol.ContractVersion {
		return nil, &Error{
			Code:       protocol.CodeContractMismatch,
			Message:    messageContractMismatch,
			HTTPStatus: healthStatus,
		}
	}

	response, err := c.Status(ctx, "")
	if err != nil {
		return nil, err
	}
	if response == nil || response.Result == nil {
		return nil, &Error{Code: ErrorResponse, Message: messageInvalidResponse}
	}
	result := response.Result
	instance := &Instance{
		ContractVersion:   health.ContractVersion,
		ServiceInstanceID: health.ServiceInstanceID,
		ReleaseVersion:    health.ReleaseVersion,
		BlockingCode:      result.BlockingCode,
		StartOperationID:  result.StartOperation,
		Session:           result.Session,
		Cleanup:           result.Cleanup,
	}
	if result.CanStart != nil {
		instance.CanStart = *result.CanStart
	}
	return instance, nil
}

// fetchHealth performs the unauthenticated bounded GET /health.
func (c *Client) fetchHealth(ctx context.Context) (*protocol.Health, int, *Error) {
	if ctx == nil {
		return nil, 0, invalidRequest("", messageMissingContext)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return nil, 0, transportFailure("")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, 0, transportFailure("")
	}
	defer response.Body.Close()

	body, err := readBounded(response.Body, maxHealthBytes)
	if err != nil {
		if errors.Is(err, errBodyTooLarge) {
			return nil, 0, &Error{
				Code:       ErrorTooLarge,
				Message:    messageTooLarge,
				HTTPStatus: response.StatusCode,
			}
		}
		return nil, 0, transportFailure("")
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, 0, &Error{
			Code:       ErrorResponse,
			Message:    messageHealthHTTP,
			HTTPStatus: response.StatusCode,
		}
	}
	var health protocol.Health
	if err := json.Unmarshal(body, &health); err != nil {
		return nil, 0, &Error{
			Code:       ErrorResponse,
			Message:    messageInvalidResponse,
			HTTPStatus: response.StatusCode,
		}
	}
	return &health, response.StatusCode, nil
}

// post performs one authenticated tool request.
func (c *Client) post(ctx context.Context, tool string, body []byte, operationID string) (*Response, error) {
	if ctx == nil {
		return nil, invalidRequest(operationID, messageMissingContext)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/"+tool, bytes.NewReader(body))
	if err != nil {
		return nil, transportFailure(operationID)
	}
	request.Header.Set(authorizationHeader, "Bearer "+c.token)
	request.Header.Set(contractHeader, protocol.ContractVersion)
	request.Header.Set("Content-Type", "application/json")
	if operationID != "" {
		request.Header.Set(operationHeader, operationID)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, transportFailure(operationID)
	}
	defer response.Body.Close()

	payload, err := readBounded(response.Body, maxResponseBytes)
	if err != nil {
		if errors.Is(err, errBodyTooLarge) {
			return nil, &Error{
				OperationID: operationID,
				Code:        ErrorTooLarge,
				Message:     messageTooLarge,
				HTTPStatus:  response.StatusCode,
			}
		}
		return nil, transportFailure(operationID)
	}

	var result protocol.Result
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, &Error{
			OperationID: operationID,
			Code:        ErrorResponse,
			Message:     messageInvalidResponse,
			HTTPStatus:  response.StatusCode,
		}
	}
	if result.Error != nil {
		// The service produced a structured failure: keep both the projection
		// and the controlled error. HTTP status alone never overrides this.
		return &Response{
			HTTPStatus:  response.StatusCode,
			OperationID: operationID,
			Result:      &result,
		}, &Error{
			OperationID: operationID,
			Code:        result.Error.Code,
			Message:     result.Error.Message,
			HTTPStatus:  response.StatusCode,
			Result:      &result,
		}
	}

	projected := &Response{
		HTTPStatus:  response.StatusCode,
		OperationID: operationID,
		Result:      &result,
	}
	if imageErr := decodeObservationImage(projected); imageErr != nil {
		return nil, imageErr
	}
	return projected, nil
}

// decodeObservationImage attaches the transport image once and removes the
// base64 carrier from the structured result. A malformed carrier is a response
// contract failure, not a usable image.
func decodeObservationImage(response *Response) *Error {
	if response == nil || response.Result == nil ||
		response.Result.Observation == nil || response.Result.Observation.Image == nil {
		return nil
	}
	meta := response.Result.Observation.Image
	if meta.Base64 == "" {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(meta.Base64)
	if err != nil || len(data) == 0 || meta.MediaType == "" || meta.Width <= 0 || meta.Height <= 0 {
		meta.Base64 = ""
		return &Error{
			OperationID: response.OperationID,
			Code:        ErrorResponse,
			Message:     messageInvalidResponse,
			HTTPStatus:  response.HTTPStatus,
			Result:      response.Result,
		}
	}
	response.Image = &Image{
		MediaType:  meta.MediaType,
		Data:       data,
		Width:      meta.Width,
		Height:     meta.Height,
		CapturedAt: meta.CapturedAt,
		ReadOnly:   meta.ReadOnly,
		Screen:     meta.Screen,
	}
	meta.Base64 = ""
	return nil
}

// readBounded reads at most limit bytes plus one detection byte.
func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errBodyTooLarge
	}
	return body, nil
}

// errBodyTooLarge marks a bounded read that saw more than its limit.
var errBodyTooLarge = errors.New("client: response body exceeds limit")

// newOperation validates an explicit P or allocates a fresh one before any
// request is built. It never sends.
func (c *Client) newOperation(opts []CallOption) (string, *Error) {
	var options callOptions
	for _, option := range opts {
		if option != nil {
			option(&options)
		}
	}
	if options.explicit {
		if !validOperationID(options.operationID) {
			return "", invalidRequest("", messageInvalidOperation)
		}
		return options.operationID, nil
	}
	operation, err := NewOperationID()
	if err != nil {
		return "", &Error{Code: protocol.CodeInternal, Message: messageOperationID}
	}
	return operation, nil
}

// NewOperationID allocates one cryptographically random operation ID of the
// form P plus 24 lowercase hexadecimal characters. Callers that must be able
// to recover an in-flight call (for example an MCP cancellation path that
// issues End for the same P) allocate it first and pass it with
// WithOperationID; the default allocation inside each work-tool call uses the
// same form.
func NewOperationID() (string, error) {
	var entropy [12]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return "P" + hex.EncodeToString(entropy[:]), nil
}

// validOperationID accepts 2..64 characters: a leading P followed by ASCII
// letters, digits, hyphen or underscore. The service requires the same minimum.
func validOperationID(operation string) bool {
	if len(operation) < 2 || len(operation) > 64 || operation[0] != 'P' {
		return false
	}
	for index := 1; index < len(operation); index++ {
		character := operation[index]
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '-', character == '_':
		default:
			return false
		}
	}
	return true
}
