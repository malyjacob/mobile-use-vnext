// Package mcp is the transport-independent MCP adapter core. It registers the
// fixed tool catalog, performs strict admission with the shared protocol
// decoders, allocates the operation ID before each work call, projects results
// as readable text plus canonical structured JSON, and bounds cancellation
// cleanup. Both the stdio and Streamable HTTP entry points share these rules.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/malyjacob/mobile-use-vnext/internal/client"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// protocolVersion is the single MCP protocol revision this adapter speaks.
// The SDK server narrows negotiation to exactly this version.
const protocolVersion = "2026-07-28"

const (
	cancellationEndTimeout   = 5 * time.Second
	cancellationDrainTimeout = 2 * time.Second
)

// Options configures the MCP server. Client and Logger are required for
// normal operation; Client must address the device service with an Agent
// credential held in private configuration.
type Options struct {
	Client  *client.Client
	Logger  *slog.Logger
	Version string
}

// NewServer registers exactly the public tool catalog and returns the SDK
// server. The adapter holds no session, lease or device authority: every
// work call goes through the device service client.
func NewServer(opts Options) (*sdk.Server, error) {
	if opts.Client == nil {
		return nil, errors.New("mcp: device service client is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	version := opts.Version
	if version == "" {
		version = protocol.ReleaseVersion
	}
	server := sdk.NewServer(&sdk.Implementation{
		Name:    "mobile-use-vnext",
		Version: version,
	}, &sdk.ServerOptions{
		SupportedProtocolVersions: []string{protocolVersion},
	})
	adapter := &adapter{client: opts.Client, logger: logger}
	for _, definition := range toolCatalog {
		definition := definition
		server.AddTool(&sdk.Tool{
			Name:        definition.name,
			Description: definition.description,
			InputSchema: definition.schema,
		}, func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return adapter.handle(ctx, definition, requestArguments(request)), nil
		})
	}
	return server, nil
}

// adapter is stateless beyond its service client and logger. It never keeps
// operation IDs, observations or sessions across calls.
type adapter struct {
	client *client.Client
	logger *slog.Logger
}

// execution is one fully projected tool call.
type execution struct {
	result       *protocol.Result
	image        *client.Image
	service      bool
	admission    bool
	cancelled    bool
	cancellation string
	code         string
}

func (a *adapter) handle(ctx context.Context, definition toolDefinition, arguments []byte) *sdk.CallToolResult {
	started := time.Now()
	execution := a.run(ctx, definition, arguments)
	isError := execution.isError(definition.name)

	text := ""
	if execution.admission {
		if execution.result != nil && execution.result.Error != nil {
			text = "invalid_request: " + execution.result.Error.Message
		} else {
			text = "invalid_request: 请求参数不符合工具契约"
		}
	} else {
		text = renderText(execution.result, execution.cancelled, execution.cancellation)
		if block := renderTreeText(execution.result); block != "" {
			if text != "" {
				text = strings.TrimRight(text, "\n") + "\n\n"
			}
			text += block
		}
	}
	content := []sdk.Content{&sdk.TextContent{Text: text}}
	if execution.image != nil && len(execution.image.Data) > 0 && execution.image.MediaType != "" {
		content = append(content, &sdk.ImageContent{
			Data:     execution.image.Data,
			MIMEType: execution.image.MediaType,
		})
	}

	logger := a.logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	logger.Info("mcp.tool",
		"tool", definition.name,
		"duration_ms", time.Since(started).Milliseconds(),
		"is_error", isError,
		"code", execution.code,
	)

	return &sdk.CallToolResult{
		Content:           content,
		StructuredContent: structuredContent(execution.result),
		IsError:           isError,
	}
}

// run performs admission, allocates the operation ID for work tools, then
// invokes the client under the cancellation wrapper.
func (a *adapter) run(ctx context.Context, definition toolDefinition, arguments []byte) *execution {
	prepared, apiErr := prepareCall(a.client, definition, arguments)
	if apiErr != nil {
		return &execution{
			result: &protocol.Result{
				Error:    apiErr.Public(),
				Recovery: protocol.AccessRecovery(apiErr.Code),
			},
			admission: true,
			code:      apiErr.Code,
		}
	}
	if definition.readOnly {
		return a.invoke(ctx, prepared, "")
	}
	operationID, err := client.NewOperationID()
	if err != nil {
		return &execution{
			result: &protocol.Result{
				Error: &protocol.Error{
					Code:    protocol.CodeInternal,
					Message: "操作编号生成失败",
				},
			},
			code: protocol.CodeInternal,
		}
	}
	return a.invoke(ctx, prepared, operationID)
}

// preparedCall is one admitted request. run executes the client method with
// the pre-allocated operation ID; read-only calls ignore it.
type preparedCall struct {
	readOnly bool
	run      func(ctx context.Context, operationID string) (*client.Response, error)
}

type callOutcome struct {
	response *client.Response
	err      error
}

// invoke runs the client call in its own goroutine so cancellation can be
// observed without ever using the cancelled context for cleanup.
func (a *adapter) invoke(ctx context.Context, call *preparedCall, operationID string) *execution {
	done := make(chan callOutcome, 1)
	go func() {
		response, err := call.run(ctx, operationID)
		done <- callOutcome{response: response, err: err}
	}()

	select {
	case outcome := <-done:
		if ctx.Err() != nil && outcome.err != nil && !hasServiceResult(outcome) {
			return a.cancelled(call, operationID, done, outcome, true)
		}
		return projectCall(outcome.response, outcome.err, operationID)
	case <-ctx.Done():
		select {
		case outcome := <-done:
			if hasServiceResult(outcome) {
				return projectCall(outcome.response, outcome.err, operationID)
			}
			return a.cancelled(call, operationID, done, outcome, true)
		default:
			return a.cancelled(call, operationID, done, callOutcome{}, false)
		}
	}
}

// cancelled builds the cancellation result. Work calls issue a bounded end
// with a background context; read-only calls never do. Any service result the
// original call already produced is preserved, and end's session/cleanup
// facts fill only the gaps.
func (a *adapter) cancelled(call *preparedCall, operationID string, done <-chan callOutcome, known callOutcome, haveKnown bool) *execution {
	var endResult *protocol.Result
	if !call.readOnly {
		endCtx, cancel := context.WithTimeout(context.Background(), cancellationEndTimeout)
		endResponse, endErr := a.client.End(endCtx, operationID)
		cancel()
		endResult = pickResult(endResponse, endErr)
	}

	if !haveKnown {
		timer := time.NewTimer(cancellationDrainTimeout)
		select {
		case known = <-done:
			haveKnown = true
		case <-timer.C:
		}
		timer.Stop()
	}

	base := pickResult(known.response, known.err)
	service := base != nil
	if base == nil {
		base = &protocol.Result{Recovery: adapterRecovery("call_cancelled")}
	}
	if call.readOnly {
		if !service && base.Error == nil {
			base.Error = &protocol.Error{
				Code:    "call_cancelled",
				Message: "调用已取消；只读工具未发起 end",
			}
		}
	} else {
		base.OperationID = operationID
		if base.Error == nil {
			base.Error = &protocol.Error{
				Code:    "call_cancelled",
				Message: cancellationEndMessage(operationID),
			}
		}
	}
	if endResult != nil {
		if !base.IncludeSession {
			base.IncludeSession = endResult.IncludeSession
			base.Session = endResult.Session
		}
		if base.Cleanup == nil {
			base.Cleanup = endResult.Cleanup
		}
	}
	if base.Recovery == nil && base.Error != nil {
		base.Recovery = adapterRecovery(base.Error.Code)
	}
	clearImageBase64(base)

	cancellation := cancellationEndMessage(operationID)
	if call.readOnly {
		cancellation = "调用已取消；只读工具未发起 end"
	}
	var image *client.Image
	if known.response != nil {
		image = known.response.Image
	}
	return &execution{
		result:       base,
		image:        image,
		service:      service,
		cancelled:    true,
		cancellation: cancellation,
		code:         logCode(base),
	}
}

func cancellationEndMessage(operationID string) string {
	return fmt.Sprintf("调用已取消；已对本次操作发起有界 end(%s)，不证明设备动作已撤回或执行链已结束", operationID)
}

// projectCall converts one client response or error into a canonical result.
// A service structured failure keeps its facts; an adapter failure gets a
// controlled error without any raw transport text.
func projectCall(response *client.Response, err error, operationID string) *execution {
	result := pickResult(response, err)
	service := result != nil
	if result == nil {
		code, message := controlledError(err)
		result = &protocol.Result{
			OperationID: operationID,
			Error:       &protocol.Error{Code: code, Message: message},
			Recovery:    adapterRecovery(code),
		}
	} else {
		if result.OperationID == "" && operationID != "" {
			result.OperationID = operationID
		}
		if err != nil && result.Error == nil {
			code, message := controlledError(err)
			result.Error = &protocol.Error{Code: code, Message: message}
			if result.Recovery == nil {
				result.Recovery = adapterRecovery(code)
			}
		}
	}
	clearImageBase64(result)
	var image *client.Image
	if response != nil {
		image = response.Image
	}
	return &execution{
		result:  result,
		image:   image,
		service: service,
		code:    logCode(result),
	}
}

func pickResult(response *client.Response, err error) *protocol.Result {
	if response != nil && response.Result != nil {
		return response.Result
	}
	var clientErr *client.Error
	if errors.As(err, &clientErr) && clientErr.Result != nil {
		return clientErr.Result
	}
	return nil
}

func hasServiceResult(outcome callOutcome) bool {
	return pickResult(outcome.response, outcome.err) != nil
}

// adapterRecovery is the adapter's own projection for failures that never
// reached a tool result. A transport failure or a cancelled call has one next
// step: poll status with the operation's P. Admission errors stay bare.
func adapterRecovery(code string) *protocol.Recovery {
	switch code {
	case "call_cancelled", client.ErrorTransport:
		return &protocol.Recovery{
			Entry: protocol.RecoveryEntryStatus, Reference: protocol.RecoveryReferenceP,
		}
	}
	return nil
}

// controlledError reduces any adapter failure to a stable code and a
// controlled short sentence. It never returns a raw Go error string.
func controlledError(err error) (string, string) {
	var clientErr *client.Error
	if errors.As(err, &clientErr) {
		code := clientErr.Code
		if code == "" {
			code = client.ErrorResponse
		}
		message := clientErr.Message
		if message == "" {
			message = "服务请求失败"
		}
		return code, message
	}
	if errors.Is(err, context.Canceled) {
		return "call_cancelled", "调用已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return client.ErrorTransport, "服务请求未完成"
	}
	return protocol.CodeInternal, "适配器内部失败"
}

func clearImageBase64(result *protocol.Result) {
	if result == nil || result.Observation == nil || result.Observation.Image == nil {
		return
	}
	result.Observation.Image.Base64 = ""
}

// structuredContent is the canonical result JSON with the transport image
// carrier removed. It is always an object so clients can rely on the shape.
func structuredContent(result *protocol.Result) json.RawMessage {
	if result == nil {
		result = &protocol.Result{}
	}
	clearImageBase64(result)
	encoded, err := protocol.JSON(result)
	if err != nil {
		return json.RawMessage(`{"error":{"code":"internal_error","message":"结果编码失败"}}`)
	}
	return json.RawMessage(encoded)
}

// isError applies the per-tool success rule. It distinguishes a served query
// fact from an admission, protocol, transport or service-level error, so
// status/diagnose stay non-error only when a result actually carries the
// queried facts.
func (e *execution) isError(tool string) bool {
	result := e.result
	switch tool {
	case "status", "diagnose":
		return result == nil || result.Error != nil
	case "observe":
		return result == nil || result.Observation == nil || result.Observation.ID == ""
	case "act":
		return result == nil || result.Outcome != "executed"
	case "inspect":
		return result == nil || result.Inspection == nil
	case "apps":
		return result == nil || result.Apps == nil || result.Error != nil
	default:
		return result == nil || result.Outcome != "executed"
	}
}

func logCode(result *protocol.Result) string {
	if result == nil {
		return ""
	}
	if result.Error != nil {
		return result.Error.Code
	}
	if observation := result.Observation; observation != nil {
		if observation.Error != nil {
			return observation.Error.Code
		}
		if observation.Image != nil && observation.Image.Error != nil {
			return observation.Image.Error.Code
		}
	}
	if result.Shell != nil && result.Shell.OutputError != nil {
		return result.Shell.OutputError.Code
	}
	if result.Notification != nil && result.Notification.Error != nil {
		return result.Notification.Error.Code
	}
	if result.Cleanup != nil && result.Cleanup.Error != nil {
		return result.Cleanup.Error.Code
	}
	return ""
}

func requestArguments(request *sdk.CallToolRequest) []byte {
	if request == nil || request.Params == nil || len(request.Params.Arguments) == 0 {
		return []byte("{}")
	}
	return request.Params.Arguments
}

// prepareCall decodes one tool's raw arguments with the shared strict
// decoder. No client method is selected until admission succeeds.
func prepareCall(service *client.Client, definition toolDefinition, body []byte) (*preparedCall, *protocol.APIError) {
	call := &preparedCall{readOnly: definition.readOnly}
	switch definition.name {
	case "start":
		if _, apiErr := protocol.DecodeStart(body); apiErr != nil {
			return nil, apiErr
		}
		call.run = func(ctx context.Context, operationID string) (*client.Response, error) {
			return service.Start(ctx, client.WithOperationID(operationID))
		}
	case "status":
		request, apiErr := protocol.DecodeStatus(body)
		if apiErr != nil {
			return nil, apiErr
		}
		call.run = func(ctx context.Context, _ string) (*client.Response, error) {
			return service.Status(ctx, request.Reference)
		}
	case "observe":
		request, apiErr := protocol.DecodeObserve(body)
		if apiErr != nil {
			return nil, apiErr
		}
		call.run = func(ctx context.Context, operationID string) (*client.Response, error) {
			return service.Observe(ctx, &protocol.ObserveRequest{
				Reference: request.Reference,
				Full:      request.Full,
				Image:     request.Image,
				WaitMS:    request.WaitMS,
			}, client.WithOperationID(operationID))
		}
	case "act":
		request, apiErr := protocol.DecodeAct(body)
		if apiErr != nil {
			return nil, apiErr
		}
		call.run = func(ctx context.Context, operationID string) (*client.Response, error) {
			return service.Act(ctx, &protocol.ActRequest{
				ObservationID: request.ObservationID,
				Operation:     request.Operation,
				Image:         request.Image,
			}, client.WithOperationID(operationID))
		}
	case "inspect":
		request, apiErr := protocol.DecodeInspect(body)
		if apiErr != nil {
			return nil, apiErr
		}
		call.run = func(ctx context.Context, _ string) (*client.Response, error) {
			return service.Inspect(ctx, &protocol.InspectRequest{
				ObservationID: request.ObservationID,
				Node:          request.Node,
				Query:         request.Query,
				HasNode:       request.HasNode,
				HasQuery:      request.HasQuery,
			})
		}
	case "keepalive":
		request, apiErr := protocol.DecodeKeepalive(body)
		if apiErr != nil {
			return nil, apiErr
		}
		call.run = func(ctx context.Context, operationID string) (*client.Response, error) {
			return service.Keepalive(ctx, request.Reference, client.WithOperationID(operationID))
		}
	case "end":
		request, apiErr := protocol.DecodeEnd(body)
		if apiErr != nil {
			return nil, apiErr
		}
		call.run = func(ctx context.Context, operationID string) (*client.Response, error) {
			return service.End(ctx, request.Reference, client.WithOperationID(operationID))
		}
	case "apps":
		request, apiErr := protocol.DecodeApps(body)
		if apiErr != nil {
			return nil, apiErr
		}
		call.run = func(ctx context.Context, operationID string) (*client.Response, error) {
			return service.Apps(ctx, &protocol.AppsRequest{
				Query:    request.Query,
				HasQuery: request.HasQuery,
			}, client.WithOperationID(operationID))
		}
	case "open_app":
		request, apiErr := protocol.DecodeOpenApp(body)
		if apiErr != nil {
			return nil, apiErr
		}
		call.run = func(ctx context.Context, operationID string) (*client.Response, error) {
			return service.OpenApp(ctx, &protocol.OpenAppRequest{
				ObservationID: request.ObservationID,
				App:           request.App,
			}, client.WithOperationID(operationID))
		}
	case "notify":
		request, apiErr := protocol.DecodeNotify(body)
		if apiErr != nil {
			return nil, apiErr
		}
		call.run = func(ctx context.Context, operationID string) (*client.Response, error) {
			return service.Notify(ctx, &protocol.NotifyRequest{Text: request.Text},
				client.WithOperationID(operationID))
		}
	case "shell":
		request, apiErr := protocol.DecodeShell(body)
		if apiErr != nil {
			return nil, apiErr
		}
		call.run = func(ctx context.Context, operationID string) (*client.Response, error) {
			return service.Shell(ctx, &protocol.ShellRequest{
				Reference: request.Reference,
				Command:   request.Command,
				Root:      request.Root,
			}, client.WithOperationID(operationID))
		}
	case "diagnose":
		request, apiErr := protocol.DecodeDiagnose(body)
		if apiErr != nil {
			return nil, apiErr
		}
		call.run = func(ctx context.Context, _ string) (*client.Response, error) {
			return service.Diagnose(ctx, &protocol.DiagnoseRequest{
				Reference: request.Reference,
				HasRef:    request.HasRef,
			})
		}
	default:
		return nil, protocol.NewError(protocol.Status(protocol.CodeInternal),
			protocol.CodeInternal, "未知工具")
	}
	return call, nil
}
