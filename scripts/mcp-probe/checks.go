package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strconv"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/malyjacob/mobile-use-vnext/internal/client"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const (
	pollInterval             = 500 * time.Millisecond
	pollTimeout              = 30 * time.Second
	cancellationDelay        = 400 * time.Millisecond
	cancellationReturnWindow = 15 * time.Second
	maxWaitMS                = 8000
)

// toolNames is the complete public MCP catalog, in registration order.
var toolNames = []string{
	"start", "status", "observe", "act", "inspect", "keepalive",
	"end", "apps", "open_app", "notify", "shell", "diagnose",
}

// probe carries the connected session and the typed service client used for
// independent status polling.
type probe struct {
	reporter *reporter
	session  *sdk.ClientSession
	service  *client.Client
}

// schemaView is the probe's projection of one published input schema.
type schemaView struct {
	Type                 string         `json:"type"`
	Properties           map[string]any `json:"properties"`
	Required             []string       `json:"required"`
	AdditionalProperties *bool          `json:"additionalProperties"`
}

// runChecks performs the catalog, session and cancellation checks. It reports
// whether the service was not admissible before device work (BLOCKED).
func (p *probe) runChecks(ctx context.Context) bool {
	if !p.catalogCheck(ctx) {
		return false
	}
	status, ok := p.statusCheck(ctx)
	if !ok {
		return false
	}
	if status.CanStart == nil || !*status.CanStart {
		p.reporter.fail("status", "can_start_false", field("blocking_code", status.BlockingCode))
		return true
	}
	p.reporter.pass("status", "can_start=true")

	pID, oID, ok := p.startCheck(ctx)
	if !ok {
		return false
	}
	observedID, ok := p.observeCheck(ctx, oID)
	if !ok {
		return false
	}
	if !p.actCheck(ctx, observedID) {
		return false
	}
	if !p.endCheck(ctx, pID) {
		return false
	}
	if !p.cancellationCheck(ctx) {
		return false
	}
	return false
}

// call invokes one MCP tool and projects its result. MCP-level errors become a
// controlled check failure; service failures stay in the structured result.
func (p *probe) call(ctx context.Context, name string, arguments any) (*callResult, *checkError) {
	result, err := p.session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil, failf("tool_call_failed")
	}
	return project(result)
}

// catalogCheck asserts the exact tool catalog and the published schema shape.
func (p *probe) catalogCheck(ctx context.Context) bool {
	list, err := p.session.ListTools(ctx, nil)
	if err != nil {
		p.reporter.fail("tools", "list_failed")
		return false
	}
	seen := make(map[string]bool, len(list.Tools))
	for _, tool := range list.Tools {
		seen[tool.Name] = true
	}
	if len(list.Tools) != len(toolNames) {
		p.reporter.fail("tools", "catalog_size_mismatch",
			field("count", strconv.Itoa(len(list.Tools))))
		return false
	}
	for _, wanted := range toolNames {
		if !seen[wanted] {
			p.reporter.fail("tools", "tool_missing", field("tool", wanted))
			return false
		}
	}
	for name := range seen {
		if !containsString(toolNames, name) {
			p.reporter.fail("tools", "tool_unexpected", field("tool", name))
			return false
		}
	}
	p.reporter.pass("tools", "count=12")

	if failure := checkSchemas(list.Tools); failure != nil {
		p.reporter.fail("schema", failure.reason, failure.fields...)
		return false
	}
	p.reporter.pass("schema",
		"objects=12", "additional_properties_false=12",
		"required_start=0", "required_observe=1", "required_act=2", "required_status=0")
	return true
}

// checkSchemas validates every published input schema without echoing it.
func checkSchemas(tools []*sdk.Tool) *checkError {
	for _, tool := range tools {
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return failf("input_schema_invalid", field("tool", tool.Name))
		}
		var schema schemaView
		if err := json.Unmarshal(encoded, &schema); err != nil {
			return failf("input_schema_invalid", field("tool", tool.Name))
		}
		if schema.Type != "object" {
			return failf("input_schema_not_object", field("tool", tool.Name))
		}
		if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
			return failf("additional_properties_open", field("tool", tool.Name))
		}
		for _, required := range schema.Required {
			if _, declared := schema.Properties[required]; !declared {
				return failf("required_not_declared", field("tool", tool.Name))
			}
		}
		switch tool.Name {
		case "start":
			if len(schema.Properties) != 0 || len(schema.Required) != 0 {
				return failf("start_schema_mismatch", field("tool", "start"))
			}
		case "status":
			if len(schema.Required) != 0 {
				return failf("required_mismatch", field("tool", "status"))
			}
		case "observe":
			if !sameStringSet(schema.Required, []string{"reference"}) {
				return failf("required_mismatch", field("tool", "observe"))
			}
		case "act":
			if !sameStringSet(schema.Required, []string{"observation_id", "operation"}) {
				return failf("required_mismatch", field("tool", "act"))
			}
		}
	}
	return nil
}

// statusCheck calls status with an empty object.
func (p *probe) statusCheck(ctx context.Context) (resultView, bool) {
	projected, failure := p.call(ctx, "status", map[string]any{})
	if failure != nil {
		p.reporter.fail("status", failure.reason, failure.fields...)
		return resultView{}, false
	}
	if projected.result.IsError {
		p.reporter.fail("status", "tool_is_error", field("code", codeOf(projected.view)))
		return resultView{}, false
	}
	return projected.view, true
}

// startCheck runs one ordinary start and records P and O1.
func (p *probe) startCheck(ctx context.Context) (string, string, bool) {
	projected, failure := p.call(ctx, "start", map[string]any{})
	if failure != nil {
		p.reporter.fail("start", failure.reason, failure.fields...)
		return "", "", false
	}
	view := projected.view
	if projected.result.IsError {
		p.reporter.fail("start", "tool_is_error", field("code", codeOf(view)))
		return "", "", false
	}
	if view.Outcome != outcomeExecuted {
		p.reporter.fail("start", "outcome_not_executed", field("outcome", view.Outcome), field("code", codeOf(view)))
		return "", "", false
	}
	if !validReference(view.OperationID, 'P') {
		p.reporter.fail("start", "operation_id_invalid")
		return "", "", false
	}
	if view.Session == nil || view.Session.State != sessionActive {
		p.reporter.fail("start", "session_not_active", field("session", sessionStateOf(view.Session)))
		return "", "", false
	}
	if view.Observation == nil || !validReference(view.Observation.ID, 'O') {
		p.reporter.fail("start", "observation_missing")
		return "", "", false
	}
	if format := treeFormatOf(view.Observation); format != treeFull {
		p.reporter.fail("start", "tree_not_full",
			field("tree_format", format), field("observation_status", view.Observation.Status))
		return "", "", false
	}
	p.reporter.pass("start",
		field("P", view.OperationID), field("O1", view.Observation.ID),
		"session=active", "tree=full")
	return view.OperationID, view.Observation.ID, true
}

// beginSession performs a start without printing a check line; the caller uses
// it for the second session of the cancellation check.
func (p *probe) beginSession(ctx context.Context) (string, string, *checkError) {
	projected, failure := p.call(ctx, "start", map[string]any{})
	if failure != nil {
		return "", "", failure
	}
	view := projected.view
	if projected.result.IsError || view.Outcome != outcomeExecuted {
		return "", "", failf("start_not_executed", field("code", codeOf(view)))
	}
	if !validReference(view.OperationID, 'P') {
		return "", "", failf("start_operation_invalid")
	}
	if view.Session == nil || view.Session.State != sessionActive {
		return "", "", failf("start_session_not_active", field("session", sessionStateOf(view.Session)))
	}
	if view.Observation == nil || !validReference(view.Observation.ID, 'O') {
		return "", "", failf("start_observation_missing")
	}
	return view.OperationID, view.Observation.ID, nil
}

// observeCheck requests one forced full observation with an image and asserts
// both the tree facts and the image carrier contract.
func (p *probe) observeCheck(ctx context.Context, reference string) (string, bool) {
	projected, failure := p.call(ctx, "observe", map[string]any{
		"reference": reference, "full": true, "image": true,
	})
	if failure != nil {
		p.reporter.fail("observe", failure.reason, failure.fields...)
		return "", false
	}
	view := projected.view
	if projected.result.IsError {
		p.reporter.fail("observe", "tool_is_error", field("code", codeOf(view)))
		return "", false
	}
	observation := view.Observation
	if observation == nil || !validReference(observation.ID, 'O') {
		p.reporter.fail("observe", "observation_missing")
		return "", false
	}
	if observation.Status != observationComplete && observation.Status != observationPartial {
		p.reporter.fail("observe", "observation_status_unexpected", field("status", observation.Status))
		return "", false
	}
	format := treeFormatOf(observation)
	if format != treeFull && format != treeDelta {
		p.reporter.fail("observe", "tree_format_missing", field("tree_format", format))
		return "", false
	}
	raw, failure := imageBytes(projected, observation)
	if failure != nil {
		p.reporter.fail("observe", failure.reason, failure.fields...)
		return "", false
	}
	if failure := checkNoImageCarrier(projected, raw); failure != nil {
		p.reporter.fail("observe", failure.reason, failure.fields...)
		return "", false
	}
	p.reporter.pass("observe",
		field("reference", reference), field("id", observation.ID),
		field("status", observation.Status), field("tree_format", format),
		"image_mime="+imageJPEG, field("image_bytes", strconv.Itoa(len(raw))),
		field("width", strconv.Itoa(observation.Image.Width)),
		field("height", strconv.Itoa(observation.Image.Height)))
	return observation.ID, true
}

// actCheck consumes the observed O with a key action and asserts the post
// observation and its image. A refused key is a controlled failure, never a
// retry.
func (p *probe) actCheck(ctx context.Context, observationID string) bool {
	projected, failure := p.call(ctx, "act", map[string]any{
		"observation_id": observationID,
		"operation":      map[string]any{"type": "key", "key": "BACK"},
		"image":          true,
	})
	if failure != nil {
		p.reporter.fail("act", failure.reason, failure.fields...)
		return false
	}
	view := projected.view
	if view.Outcome != outcomeExecuted {
		code := codeOf(view)
		outcome := view.Outcome
		if outcome == "" {
			outcome = "none"
		}
		p.reporter.fail("act", "not_executed", field("code", code), field("outcome", outcome))
		return false
	}
	if projected.result.IsError {
		p.reporter.fail("act", "tool_is_error", field("code", codeOf(view)))
		return false
	}
	observation := view.Observation
	if observation == nil || !validReference(observation.ID, 'O') {
		p.reporter.fail("act", "post_observation_missing")
		return false
	}
	if observation.Status != observationComplete && observation.Status != observationPartial {
		p.reporter.fail("act", "post_observation_status_unexpected", field("status", observation.Status))
		return false
	}
	raw, failure := imageBytes(projected, observation)
	if failure != nil {
		p.reporter.fail("act", failure.reason, failure.fields...)
		return false
	}
	if failure := checkNoImageCarrier(projected, raw); failure != nil {
		p.reporter.fail("act", failure.reason, failure.fields...)
		return false
	}
	p.reporter.pass("act",
		"outcome=executed", "key=BACK", field("id", observation.ID),
		field("status", observation.Status), "image_mime="+imageJPEG,
		field("image_bytes", strconv.Itoa(len(raw))),
		field("width", strconv.Itoa(observation.Image.Width)),
		field("height", strconv.Itoa(observation.Image.Height)))
	return true
}

// endCheck ends the session and waits for the service to become admissible
// again, which is the client-visible proof that cleanup completed.
func (p *probe) endCheck(ctx context.Context, startID string) bool {
	projected, failure := p.call(ctx, "end", map[string]any{"reference": startID})
	if failure != nil {
		p.reporter.fail("end", failure.reason, failure.fields...)
		return false
	}
	view := projected.view
	if projected.result.IsError {
		p.reporter.fail("end", "tool_is_error", field("code", codeOf(view)))
		return false
	}
	if view.Outcome != outcomeExecuted {
		p.reporter.fail("end", "outcome_not_executed", field("outcome", view.Outcome), field("code", codeOf(view)))
		return false
	}
	if view.Session == nil || view.Session.State != sessionEnded {
		p.reporter.fail("end", "session_not_ended", field("session", sessionStateOf(view.Session)))
		return false
	}
	last, failure := pollCanStart(ctx, p.service)
	if failure != nil {
		p.reporter.fail("end", "cleanup_timeout", field("can_start", "false"))
		return false
	}
	p.reporter.pass("end",
		"outcome=executed", "session=ended",
		field("cleanup.state", cleanupStateOf(last, view)))
	return true
}

// cancellationCheck starts a second session, cancels a waiting observe and
// proves the adapter issued a bounded end for it.
func (p *probe) cancellationCheck(ctx context.Context) bool {
	startID, observationID, failure := p.beginSession(ctx)
	if failure != nil {
		p.reporter.fail("cancel", "second_start_failed", failure.fields...)
		return false
	}

	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		result *sdk.CallToolResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := p.session.CallTool(callCtx, &sdk.CallToolParams{
			Name: "observe",
			Arguments: map[string]any{
				"reference": observationID,
				"wait_ms":   maxWaitMS,
			},
		})
		done <- outcome{result: result, err: err}
	}()

	select {
	case <-time.After(cancellationDelay):
		cancel()
	case <-ctx.Done():
		cancel()
	}

	var returned bool
	var returnedErr error
	var isErrorResult bool
	select {
	case completed := <-done:
		returned = true
		returnedErr = completed.err
		isErrorResult = completed.result != nil && completed.result.IsError
	case <-time.After(cancellationReturnWindow):
	}
	if !returned {
		p.reporter.fail("cancel", "call_did_not_return")
		return false
	}
	if returnedErr == nil && !isErrorResult {
		p.reporter.fail("cancel", "call_not_cancelled")
		return false
	}

	last, failure := pollSessionEnded(ctx, p.service)
	if failure != nil {
		p.reporter.fail("cancel", "cleanup_timeout", field("can_start", "false"))
		return false
	}

	operationState := "none"
	operationResult := statusResultOf(p.service.Status(ctx, startID))
	if operationResult == nil {
		p.reporter.fail("cancel", "operation_status_unavailable")
		return false
	}
	switch {
	case operationResult.OperationState != "":
		operationState = operationResult.OperationState
	case operationResult.RequestClosed != nil && *operationResult.RequestClosed:
		operationState = requestClosedLabel
	}
	if operationState == operationRunning {
		p.reporter.fail("cancel", "operation_still_running", field("operation_state", operationState))
		return false
	}
	if operationState != operationFinished && operationState != operationNotSeen && operationState != requestClosedLabel {
		p.reporter.fail("cancel", "operation_state_unexpected", field("operation_state", operationState))
		return false
	}

	sessionState := "none"
	if last != nil && last.Session != nil {
		sessionState = last.Session.State
	}
	if sessionState == sessionActive || sessionState == sessionStarting {
		p.reporter.fail("cancel", "session_still_active", field("session", sessionState))
		return false
	}
	returnedKind := "context_error"
	if isErrorResult {
		returnedKind = "is_error"
	}
	p.reporter.pass("cancel",
		field("P", startID), "returned="+returnedKind,
		field("session", sessionState), "can_start=true",
		field("operation_state", operationState))
	return true
}

// pollCanStart polls status() until can_start is true or the deadline passes.
func pollCanStart(ctx context.Context, service *client.Client) (*protocol.Result, *checkError) {
	deadline := time.Now().Add(pollTimeout)
	var last *protocol.Result
	for {
		if result := statusResultOf(service.Status(ctx, "")); result != nil {
			last = result
			if result.CanStart != nil && *result.CanStart {
				return result, nil
			}
		}
		if time.Now().After(deadline) {
			return last, failf("cleanup_timeout")
		}
		select {
		case <-ctx.Done():
			return last, failf("cleanup_timeout")
		case <-time.After(pollInterval):
		}
	}
}

// pollSessionEnded polls status() until the original session is no longer
// active and the service is admissible again.
func pollSessionEnded(ctx context.Context, service *client.Client) (*protocol.Result, *checkError) {
	deadline := time.Now().Add(pollTimeout)
	var last *protocol.Result
	for {
		if result := statusResultOf(service.Status(ctx, "")); result != nil {
			last = result
			canStart := result.CanStart != nil && *result.CanStart
			ended := result.Session == nil ||
				(result.Session.State != sessionActive && result.Session.State != sessionStarting)
			if canStart && ended {
				return result, nil
			}
		}
		if time.Now().After(deadline) {
			return last, failf("cleanup_timeout")
		}
		select {
		case <-ctx.Done():
			return last, failf("cleanup_timeout")
		case <-time.After(pollInterval):
		}
	}
}

// statusResultOf returns the structured result of one client call, whether it
// arrived as a normal response or inside a typed client error.
func statusResultOf(response *client.Response, err error) *protocol.Result {
	if response != nil && response.Result != nil {
		return response.Result
	}
	var clientErr *client.Error
	if errors.As(err, &clientErr) && clientErr.Result != nil {
		return clientErr.Result
	}
	return nil
}

func cleanupStateOf(last *protocol.Result, endView resultView) string {
	if last != nil && last.Cleanup != nil && last.Cleanup.State != "" {
		return last.Cleanup.State
	}
	if endView.Cleanup != nil && endView.Cleanup.State != "" {
		return endView.Cleanup.State
	}
	return "none"
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func sameStringSet(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	seen := make(map[string]bool, len(actual))
	for _, value := range actual {
		seen[value] = true
	}
	for _, value := range expected {
		if !seen[value] {
			return false
		}
	}
	return true
}
