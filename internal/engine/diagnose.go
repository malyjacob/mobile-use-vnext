package engine

import (
	"sort"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Diagnosis is a bounded report: fixed summaries, controlled phases/codes and
// whitelisted timings only. It never carries logs, trees, input text, commands
// or command output, and it never repairs or changes anything.
const (
	maxDiagnosisBytes   = 16 * 1024
	maxDiagnosisEntries = 8
)

// diagnosisEvent is one internally ordered event; the string form is produced
// only after sorting by the real instants.
type diagnosisEvent struct {
	at    time.Time
	phase string
	code  string
}

// Diagnose reports bounded facts about one original record or about the
// caller's own recent state. It performs no device I/O, takes no work position,
// allocates no operation and renews no lease.
func (e *Engine) Diagnose(agent string, req *protocol.DiagnoseRequest) (*protocol.Result, *protocol.APIError) {
	now := time.Now()
	e.mu.Lock()
	var view *protocol.DiagnosisView
	if req != nil && req.HasRef {
		view = e.diagnoseReferenceLocked(agent, req.Reference, now)
	} else {
		view = e.diagnoseOwnLocked(agent, now)
	}
	e.mu.Unlock()
	trimDiagnosisBudget(view, maxDiagnosisBytes)
	return &protocol.Result{Diagnosis: view}, nil
}

// diagnoseReferenceLocked resolves one O/P by original ownership. An unknown
// or foreign record is a normal query fact, not a transport error.
func (e *Engine) diagnoseReferenceLocked(agent, reference string, now time.Time) *protocol.DiagnosisView {
	kind, obs, op := e.findReferenceLocked(reference)
	switch kind {
	case "O":
		if obs == nil || obs.Agent != agent {
			return e.unavailableDiagnosisLocked(now)
		}
		return e.diagnoseObservationLocked(obs, now)
	case "P":
		if op == nil || op.Agent != agent {
			return e.unavailableDiagnosisLocked(now)
		}
		return e.diagnoseOperationLocked(op, now)
	default:
		return e.unavailableDiagnosisLocked(now)
	}
}

// diagnoseObservationLocked reports the retained observation header and the
// session/cleanup facts that decide whether it can still be used.
func (e *Engine) diagnoseObservationLocked(obs *observationState, now time.Time) *protocol.DiagnosisView {
	view := &protocol.DiagnosisView{
		Coverage: "original_record",
		Issues:   make([]protocol.DiagnosisIssue, 0, 4),
		TimingMS: e.diagnosisTimingLocked(now),
		Timeline: make([]protocol.DiagnosisEvent, 0, 4),
	}
	var events []diagnosisEvent
	session := e.sessionByEpochLocked(obs.SessionEpoch)
	cleanup := e.cleanupForSessionLocked(session)
	e.addCleanupTimingLocked(view.TimingMS, cleanup, now)
	switch {
	case session == nil:
		view.Issues = append(view.Issues, protocol.DiagnosisIssue{
			Phase: "session", Code: protocol.CodeSessionNotFound,
		})
	case session.State == SessionEnded:
		view.Issues = append(view.Issues, protocol.DiagnosisIssue{
			Phase: "session", Code: protocol.CodeSessionEnded,
		})
		events = append(events, diagnosisEvent{
			at: session.EndedAt, phase: "session", code: session.Reason,
		})
	default:
		events = append(events, diagnosisEvent{
			at: session.StartedAt, phase: "session", code: session.State,
		})
	}
	e.appendCleanupIssueLocked(&view.Issues, cleanup)
	view.Summary = "原观察记录可用"
	if len(view.Issues) > 0 {
		view.Summary = "原观察记录存在未完成问题"
	}
	var dropped bool
	view.Timeline, dropped = diagnosisEvents(events, maxDiagnosisEntries)
	if dropped {
		view.Truncated = true
	}
	return view
}

// diagnoseOperationLocked reports the original operation facts and any session
// or cleanup fact that still matters.
func (e *Engine) diagnoseOperationLocked(op *operationState, now time.Time) *protocol.DiagnosisView {
	view := &protocol.DiagnosisView{
		Operation: op.ID,
		Coverage:  "original_record",
		Issues:    make([]protocol.DiagnosisIssue, 0, 4),
		TimingMS:  e.diagnosisTimingLocked(now),
		Timeline:  make([]protocol.DiagnosisEvent, 0, 4),
	}
	cleanup := e.cleanupForOriginLocked(op.SessionOrigin)
	e.addCleanupTimingLocked(view.TimingMS, cleanup, now)
	var events []diagnosisEvent
	if op.State == OperationFinished {
		view.Outcome = op.Outcome
		if !op.CreatedAt.IsZero() && !op.FinishedAt.IsZero() {
			if elapsed := op.FinishedAt.Sub(op.CreatedAt); elapsed >= 0 {
				view.TimingMS["operation"] = elapsed.Milliseconds()
			}
		}
		code := op.Outcome
		if op.Error != nil {
			view.Issues = append(view.Issues, protocol.DiagnosisIssue{
				Phase: op.Phase, Code: op.Error.Code,
			})
			code = op.Error.Code
		}
		events = append(events, diagnosisEvent{
			at: op.FinishedAt, phase: op.Kind, code: code,
		})
	} else {
		if !op.CreatedAt.IsZero() {
			if elapsed := now.Sub(op.CreatedAt); elapsed >= 0 {
				view.TimingMS["operation"] = elapsed.Milliseconds()
			}
		}
		events = append(events, diagnosisEvent{
			at: op.CreatedAt, phase: op.Kind, code: "running",
		})
	}
	if op.SessionOrigin != "" {
		session := e.sessionByOriginLocked(op.SessionOrigin)
		switch {
		case session == nil:
			view.Issues = append(view.Issues, protocol.DiagnosisIssue{
				Phase: "session", Code: protocol.CodeSessionNotFound,
			})
		case session.State == SessionEnded:
			view.Issues = append(view.Issues, protocol.DiagnosisIssue{
				Phase: "session", Code: protocol.CodeSessionEnded,
			})
			events = append(events, diagnosisEvent{
				at: session.EndedAt, phase: "session", code: session.Reason,
			})
		default:
			events = append(events, diagnosisEvent{
				at: session.StartedAt, phase: "session", code: session.State,
			})
		}
	}
	e.appendCleanupIssueLocked(&view.Issues, cleanup)
	view.Summary = "原操作记录可用"
	switch {
	case op.State == OperationRunning:
		view.Summary = "原操作仍在进行"
	case len(view.Issues) > 0:
		view.Summary = "原操作记录存在未完成问题"
	}
	var dropped bool
	view.Timeline, dropped = diagnosisEvents(events, maxDiagnosisEntries)
	if dropped {
		view.Truncated = true
	}
	return view
}

// diagnoseOwnLocked summarizes the caller's own recent state. It never claims
// "never failed"; an empty report only means no issue was recorded.
func (e *Engine) diagnoseOwnLocked(agent string, now time.Time) *protocol.DiagnosisView {
	view := &protocol.DiagnosisView{
		Coverage: "own_records",
		Issues:   make([]protocol.DiagnosisIssue, 0, maxDiagnosisEntries),
		TimingMS: e.diagnosisTimingLocked(now),
		Timeline: make([]protocol.DiagnosisEvent, 0, maxDiagnosisEntries),
	}
	if e.droppedUnconfirmed > 0 {
		view.Issues = append(view.Issues, protocol.DiagnosisIssue{Phase: "cleanup", Code: "retired_cleanup_dropped"})
	}
	cleanup := e.cleanupForSessionLocked(e.latestSessionForAgentLocked(agent))
	e.addCleanupTimingLocked(view.TimingMS, cleanup, now)
	if state, reason := e.availabilityLocked(now); state != AvailabilityReady && reason != "" {
		view.Issues = append(view.Issues, protocol.DiagnosisIssue{
			Phase: "availability", Code: reason,
		})
	}
	e.appendCleanupIssueLocked(&view.Issues, cleanup)

	failed := make([]*operationState, 0, len(e.operations))
	running := make([]*operationState, 0, 4)
	for _, op := range e.operations {
		if op.Agent != agent {
			continue
		}
		if op.State == OperationRunning {
			running = append(running, op)
			continue
		}
		if op.Error != nil || op.Outcome == OutcomeUncertain {
			failed = append(failed, op)
		}
	}
	sort.Slice(failed, func(i, j int) bool { return operationFinishedAt(failed[i]).Before(operationFinishedAt(failed[j])) })
	// Newest failures first, but never push out the higher-priority
	// availability/cleanup facts at the front.
	added := 0
	for i := len(failed) - 1; i >= 0 && len(view.Issues) < maxDiagnosisEntries; i-- {
		code := failed[i].Outcome
		if failed[i].Error != nil {
			code = failed[i].Error.Code
		}
		view.Issues = append(view.Issues, protocol.DiagnosisIssue{
			Phase: failed[i].Phase, Code: code,
		})
		added++
	}
	if added < len(failed) {
		view.Truncated = true
	}

	events := make([]diagnosisEvent, 0, len(running)+len(failed))
	for _, op := range running {
		events = append(events, diagnosisEvent{at: op.CreatedAt, phase: op.Kind, code: "running"})
	}
	for _, op := range failed {
		code := op.Outcome
		if op.Error != nil {
			code = op.Error.Code
		}
		events = append(events, diagnosisEvent{
			at: operationFinishedAt(op), phase: op.Kind, code: code,
		})
	}
	var dropped bool
	view.Timeline, dropped = diagnosisEvents(events, maxDiagnosisEntries)
	if dropped {
		view.Truncated = true
	}
	switch {
	case len(view.Issues) > 0:
		view.Summary = "记录到近期问题"
	case len(running) > 0:
		view.Summary = "有操作仍在进行"
	default:
		view.Summary = "未记录到近期问题"
	}
	return view
}

func (e *Engine) unavailableDiagnosisLocked(now time.Time) *protocol.DiagnosisView {
	return &protocol.DiagnosisView{
		Summary:  "未找到可用的原记录",
		Issues:   make([]protocol.DiagnosisIssue, 0),
		TimingMS: e.diagnosisTimingLocked(now),
		Timeline: make([]protocol.DiagnosisEvent, 0),
		Coverage: "record_unavailable",
	}
}

// diagnosisTimingLocked returns only whitelisted timing keys.
func (e *Engine) diagnosisTimingLocked(now time.Time) map[string]int64 {
	timing := map[string]int64{}
	if !e.startedAt.IsZero() {
		if elapsed := now.Sub(e.startedAt); elapsed >= 0 {
			timing["since_start"] = elapsed.Milliseconds()
		}
	}
	return timing
}

// addCleanupTimingLocked reports elapsed cleanup only while it is unfinished;
// the bounded state carries no completion instant to report as a duration.
func (e *Engine) addCleanupTimingLocked(timing map[string]int64, cleanup *cleanupState, now time.Time) {
	if cleanup == nil || cleanup.StartedAt.IsZero() ||
		cleanup.State == protocol.CleanupComplete {
		return
	}
	if elapsed := now.Sub(cleanup.StartedAt); elapsed >= 0 {
		timing["cleanup"] = elapsed.Milliseconds()
	}
}

// appendCleanupIssueLocked reports one controlled cleanup problem, never its
// internal error text.
func (e *Engine) appendCleanupIssueLocked(issues *[]protocol.DiagnosisIssue, cleanup *cleanupState) {
	if cleanup == nil {
		return
	}
	switch {
	case cleanup.Error != nil:
		*issues = append(*issues, protocol.DiagnosisIssue{
			Phase: "cleanup", Code: cleanup.Error.Code,
		})
	case cleanup.State == protocol.CleanupUnconfirmed:
		*issues = append(*issues, protocol.DiagnosisIssue{
			Phase: "cleanup", Code: protocol.CodeCleanupUnconfirmed,
		})
	}
}

func operationFinishedAt(op *operationState) time.Time {
	if !op.FinishedAt.IsZero() {
		return op.FinishedAt
	}
	return op.CreatedAt
}

// diagnosisEvents orders events oldest first and keeps at most limit of the
// newest complete events, reporting whether anything was dropped.
func diagnosisEvents(events []diagnosisEvent, limit int) ([]protocol.DiagnosisEvent, bool) {
	sort.SliceStable(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })
	dropped := limit > 0 && len(events) > limit
	if dropped {
		events = events[len(events)-limit:]
	}
	out := make([]protocol.DiagnosisEvent, 0, len(events))
	for _, event := range events {
		out = append(out, protocol.DiagnosisEvent{
			At: formatTime(event.at), Phase: event.phase, Code: event.code,
		})
	}
	return out, dropped
}

// trimDiagnosisBudget drops the oldest timeline events and then the oldest
// issues until the canonical JSON object fits its byte budget.
func trimDiagnosisBudget(view *protocol.DiagnosisView, limit int) {
	if view == nil || limit <= 0 {
		return
	}
	for {
		encoded, err := protocol.JSON(view)
		if err != nil || len(encoded) <= limit {
			return
		}
		switch {
		case len(view.Timeline) > 0:
			view.Timeline = view.Timeline[1:]
		case len(view.Issues) > 0:
			view.Issues = view.Issues[1:]
		default:
			return
		}
		view.Truncated = true
	}
}
