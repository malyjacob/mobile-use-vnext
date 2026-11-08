package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/client"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// lostResponseTransport sends the request to the real service but discards the
// response and reports a transport failure. The service has already processed
// the request, which is exactly the missed-response recovery case.
type lostResponseTransport struct {
	base http.RoundTripper
}

func (t *lostResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	_ = response.Body.Close()
	return nil, errors.New("mcp-probe: response intentionally lost")
}

// runPRecovery proves that one lost service response is recoverable from the
// operation ID without dispatching the start a second time.
func runPRecovery(ctx context.Context, cfg *config, reporter *reporter) int {
	service, err := client.New(client.Config{
		BaseURL:    cfg.Service.BaseURL,
		AgentToken: cfg.Service.AgentToken,
	})
	if err != nil {
		reporter.fail("config", "service_client_invalid")
		return reporter.finish(false)
	}

	initial := statusResultOf(service.Status(ctx, ""))
	if initial == nil {
		reporter.fail("status", "status_unavailable")
		return reporter.finish(false)
	}
	if initial.CanStart == nil || !*initial.CanStart {
		reporter.fail("status", "can_start_false", field("blocking_code", initial.BlockingCode))
		return reporter.finish(true)
	}
	reporter.pass("status", "can_start=true")

	lossy, err := client.New(client.Config{
		BaseURL:    cfg.Service.BaseURL,
		AgentToken: cfg.Service.AgentToken,
		HTTPClient: &http.Client{Transport: &lostResponseTransport{base: http.DefaultTransport}},
	})
	if err != nil {
		reporter.fail("config", "service_client_invalid")
		return reporter.finish(false)
	}

	response, startErr := lossy.Start(ctx)
	if response != nil {
		reporter.fail("missed_response", "response_delivered")
		return reporter.finish(false)
	}
	var clientErr *client.Error
	if !errors.As(startErr, &clientErr) {
		reporter.fail("missed_response", "unexpected_error")
		return reporter.finish(false)
	}
	if !clientErr.Transport {
		reporter.fail("missed_response", "not_a_transport_error", field("code", clientErr.Code))
		return reporter.finish(false)
	}
	operationID := clientErr.OperationID
	if !validReference(operationID, 'P') {
		reporter.fail("missed_response", "operation_id_invalid")
		return reporter.finish(false)
	}
	reporter.pass("missed_response", field("P", operationID), "transport=true")

	original, failure := pollOperationClosed(ctx, service, operationID)
	if failure != nil {
		reporter.fail("status_recovery", failure.reason, failure.fields...)
		return reporter.finish(false)
	}
	operationState := original.OperationState
	if operationState == "" {
		operationState = requestClosedLabel
	}
	reporter.pass("status_recovery", field("operation_state", operationState))

	status := statusResultOf(service.Status(ctx, ""))
	if status == nil {
		reporter.fail("single_dispatch", "status_unavailable")
		return reporter.finish(false)
	}
	if status.Session == nil || status.Session.State != sessionActive {
		reporter.fail("single_dispatch", "session_not_active",
			field("session", sessionStateOfSession(status.Session)))
		return reporter.finish(false)
	}
	if status.StartOperation != operationID || status.Session.Origin != operationID {
		reporter.fail("single_dispatch", "session_origin_mismatch",
			field("start_operation_id", status.StartOperation))
		return reporter.finish(false)
	}
	reporter.pass("single_dispatch", "session=active", field("start_operation_id", operationID))

	observeResponse, observeErr := service.Observe(ctx, &protocol.ObserveRequest{
		Reference: operationID,
		Full:      true,
	})
	observeResult := statusResultOf(observeResponse, observeErr)
	if observeResult == nil {
		reporter.fail("observe", "observe_failed")
		return reporter.finish(false)
	}
	observation := observeResult.Observation
	if observation == nil || !validReference(observation.ID, 'O') {
		reporter.fail("observe", "observation_missing",
			field("code", protocolErrorCode(observeResult.Error)))
		return reporter.finish(false)
	}
	format := protocolTreeFormat(observation)
	if format != treeFull {
		reporter.fail("observe", "tree_not_full", field("tree_format", format))
		return reporter.finish(false)
	}
	if observation.Status != observationComplete && observation.Status != observationPartial {
		reporter.fail("observe", "observation_status_unexpected", field("status", observation.Status))
		return reporter.finish(false)
	}
	reporter.pass("observe",
		field("reference", operationID), field("O", observation.ID),
		"tree=full", field("status", observation.Status))

	endResponse, endErr := service.End(ctx, operationID)
	endResult := statusResultOf(endResponse, endErr)
	if endResult == nil {
		reporter.fail("end", "end_failed")
		return reporter.finish(false)
	}
	if endResult.Outcome != outcomeExecuted {
		reporter.fail("end", "outcome_not_executed",
			field("outcome", endResult.Outcome), field("code", protocolErrorCode(endResult.Error)))
		return reporter.finish(false)
	}
	if endResult.Session == nil || endResult.Session.State != sessionEnded {
		reporter.fail("end", "session_not_ended", field("session", sessionStateOfSession(endResult.Session)))
		return reporter.finish(false)
	}
	after, failure := pollCanStart(ctx, service)
	if failure != nil {
		reporter.fail("end", "cleanup_timeout", field("can_start", "false"))
		return reporter.finish(false)
	}
	cleanup := "none"
	switch {
	case after != nil && after.Cleanup != nil && after.Cleanup.State != "":
		cleanup = after.Cleanup.State
	case endResult.Cleanup != nil && endResult.Cleanup.State != "":
		cleanup = endResult.Cleanup.State
	}
	reporter.pass("end", "outcome=executed", "session=ended",
		field("cleanup.state", cleanup), "can_start=true")

	duplicateResponse, duplicateErr := service.Start(ctx, client.WithOperationID(operationID))
	duplicate := statusResultOf(duplicateResponse, duplicateErr)
	if duplicate == nil {
		reporter.fail("duplicate", "original_operation_unavailable",
			field("code", clientErrorCode(duplicateErr)))
		return reporter.finish(false)
	}
	if duplicate.OperationID != operationID {
		reporter.fail("duplicate", "original_operation_not_returned",
			field("operation_id", duplicate.OperationID))
		return reporter.finish(false)
	}
	closed := duplicate.OperationState == operationFinished ||
		(duplicate.RequestClosed != nil && *duplicate.RequestClosed)
	if !closed {
		reporter.fail("duplicate", "operation_not_finished",
			field("operation_state", duplicate.OperationState))
		return reporter.finish(false)
	}
	afterDuplicate := statusResultOf(service.Status(ctx, ""))
	if afterDuplicate != nil {
		if afterDuplicate.Session != nil && afterDuplicate.Session.State == sessionActive {
			reporter.fail("duplicate", "new_session_active")
			return reporter.finish(false)
		}
		if afterDuplicate.StartOperation != "" && afterDuplicate.StartOperation != operationID {
			reporter.fail("duplicate", "start_operation_changed",
				field("start_operation_id", afterDuplicate.StartOperation))
			return reporter.finish(false)
		}
		if afterDuplicate.CanStart != nil && !*afterDuplicate.CanStart {
			reporter.fail("duplicate", "service_not_admissible")
			return reporter.finish(false)
		}
	}
	state := duplicate.OperationState
	if state == "" && duplicate.RequestClosed != nil && *duplicate.RequestClosed {
		state = requestClosedLabel
	}
	reporter.pass("duplicate", field("P", operationID),
		field("operation_state", state), "new_session=false")
	return reporter.finish(false)
}

// pollOperationClosed waits until the original operation is no longer running.
// It never resends the operation.
func pollOperationClosed(ctx context.Context, service *client.Client, operationID string) (*protocol.Result, *checkError) {
	deadline := time.Now().Add(pollTimeout)
	var last *protocol.Result
	for {
		if result := statusResultOf(service.Status(ctx, operationID)); result != nil {
			last = result
			if result.OperationState == operationFinished ||
				(result.RequestClosed != nil && *result.RequestClosed) {
				return result, nil
			}
		}
		if time.Now().After(deadline) {
			return last, failf("operation_not_closed")
		}
		select {
		case <-ctx.Done():
			return last, failf("operation_not_closed")
		case <-time.After(pollInterval):
		}
	}
}

func protocolTreeFormat(observation *protocol.Observation) string {
	if observation == nil || observation.Tree == nil {
		return "none"
	}
	if observation.Tree.Full != nil {
		return treeFull
	}
	if observation.Tree.Delta != nil {
		return treeDelta
	}
	return "none"
}

func protocolErrorCode(value *protocol.Error) string {
	if value == nil {
		return "none"
	}
	return value.Code
}

func sessionStateOfSession(session *protocol.Session) string {
	if session == nil {
		return "none"
	}
	return session.State
}

func clientErrorCode(err error) string {
	var clientErr *client.Error
	if errors.As(err, &clientErr) {
		return clientErr.Code
	}
	return "none"
}
