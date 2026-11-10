package engine

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Managed shell lifecycle states (Ledger.Shell.State).
const (
	ShellPreparing   = "preparing"
	ShellDispatched  = "dispatched"
	ShellRunning     = "running"
	ShellTerminating = "terminating"
	ShellConfirmed   = "confirmed"
	ShellAborted     = "aborted"
)

// Managed shell termination tokens. They are the public `termination` values
// and the runner's own end reasons.
const (
	terminationNotStarted = "not_started"
	terminationExited     = "exited"
	terminationTimeout    = "timeout"
	terminationCancelled  = "cancelled"
	terminationUnknown    = "unknown"
)

// shellPollInterval is the bounded spacing of runner metadata reads.
const shellPollInterval = 200 * time.Millisecond

// shellState is the engine-owned identity of one managed runner. It survives
// the ordinary work slot while the chain is unresolved, so admission stays
// closed until the exact process group is confirmed gone.
type shellState struct {
	RunnerID    string
	Dir         string
	OperationID string
	Agent       string
	Origin      string
	Epoch       int64

	AppInstanceID     string
	ModeGeneration    string
	DisplayID         int
	DisplayGeneration string
	Root              bool

	State        string
	Phase        string
	CommandRunes int

	StartedAt  time.Time
	DeadlineAt time.Time

	Start    device.ShellStart
	HasStart bool
	Done     device.ShellDone
	HasDone  bool

	Termination string
	ExitCode    *int
	OutputBytes int
	Truncated   *bool
	ConfirmedAt time.Time
}

// settled reports whether this run no longer keeps the admission barrier.
func (s *shellState) settled() bool {
	return s == nil || s.State == ShellConfirmed || s.State == ShellAborted
}

func (s *shellState) ledger() *LedgerShell {
	if s == nil {
		return nil
	}
	record := &LedgerShell{
		RunnerID: s.RunnerID, Dir: s.Dir, OperationID: s.OperationID, Agent: s.Agent,
		Origin: s.Origin, Epoch: s.Epoch, AppInstanceID: s.AppInstanceID,
		ModeGeneration: s.ModeGeneration, DisplayID: s.DisplayID,
		DisplayGeneration: s.DisplayGeneration, Root: s.Root,
		State: s.State, Phase: s.Phase, CommandRunes: s.CommandRunes,
		StartedAt: s.StartedAt, DeadlineAt: s.DeadlineAt,
		Termination: s.Termination, ExitCode: s.ExitCode,
		OutputBytes: s.OutputBytes, Truncated: s.Truncated,
		ConfirmedAt: s.ConfirmedAt,
	}
	if s.HasStart {
		record.StartPID = s.Start.PID
		record.StartPGID = s.Start.PGID
		record.StartTicks = s.Start.StartTicks
		record.BootID = s.Start.BootID
	}
	return record
}

func shellStateFromLedger(record *LedgerShell) *shellState {
	if record == nil {
		return nil
	}
	run := &shellState{
		RunnerID: record.RunnerID, Dir: record.Dir, OperationID: record.OperationID,
		Agent: record.Agent, Origin: record.Origin, Epoch: record.Epoch, AppInstanceID: record.AppInstanceID,
		ModeGeneration: record.ModeGeneration, DisplayID: record.DisplayID,
		DisplayGeneration: record.DisplayGeneration, Root: record.Root,
		State: record.State, Phase: record.Phase, CommandRunes: record.CommandRunes,
		StartedAt: record.StartedAt, DeadlineAt: record.DeadlineAt,
		Termination: record.Termination, ExitCode: record.ExitCode,
		OutputBytes: record.OutputBytes, Truncated: record.Truncated,
		ConfirmedAt: record.ConfirmedAt,
	}
	if record.StartPID > 0 {
		run.HasStart = true
		run.Start = device.ShellStart{
			HasIdentity: true, PID: record.StartPID, PGID: record.StartPGID,
			StartTicks: record.StartTicks, BootID: record.BootID,
		}
	}
	if run.State == "" {
		run.State = ShellDispatched
	}
	return run
}

// Shell runs one managed command. The reference only locates the original
// session: it may name a retired/claimed/expired O of the live session or a P
// whose SessionOrigin is that session. The command text is passed to the
// runner as stdin bytes only; it is never persisted, logged or echoed.
//
// A request-context cancellation does not abandon the exact-chain barrier: the
// termination confirmation uses an independent bounded context.
func (e *Engine) Shell(ctx context.Context, agent, p string, req *protocol.ShellRequest) (*protocol.Result, *protocol.APIError) {
	_ = ctx
	op, duplicate, apiErr := e.registerOperation(p, agent, "shell")
	if apiErr != nil {
		return nil, apiErr
	}
	if duplicate != nil {
		return duplicate, nil
	}
	if req == nil || req.Command == "" {
		return e.finishWithError(op, "admission", protocol.Invalid("shell 需要非空 command")), nil
	}
	e.setOperationReference(op, referenceKind(req.Reference))

	session, admissionErr := e.shellAdmissionLocked(agent, req.Reference)
	if admissionErr != nil {
		return e.finishWithError(op, "admission", admissionErr), nil
	}

	work, busy := e.acquireWork(op, WorkShell)
	if busy != nil {
		// A busy device never consumes or retires the caller's observation.
		return e.finishWithError(op, "admission", busy), nil
	}
	defer e.releaseWork(work)
	e.setOperationPhase(op, "admission")

	// The root probe is a read-only capability check; it neither starts a
	// shell nor touches the reading boundary.
	if req.Root {
		probeCtx, cancel := context.WithTimeout(work.Ctx, 5*time.Second)
		available, rootErr := e.dev.RootAvailable(probeCtx)
		cancel()
		if rootErr != nil || !available {
			return e.finishWithError(op, "admission",
				protocol.NewError(409, protocol.CodeRootUnavailable, "未取得 root 权限")), nil
		}
	}

	runID := NewUUID()
	run := &shellState{
		RunnerID: runID, OperationID: op.ID, Agent: agent,
		Origin: session.Origin, Epoch: session.Epoch,
		AppInstanceID: session.AppInstanceID, ModeGeneration: session.ModeGeneration,
		DisplayID: session.Display.ID, DisplayGeneration: session.Display.Generation,
		Root: req.Root, State: ShellPreparing, Phase: "preparing",
		CommandRunes: len([]rune(req.Command)),
		StartedAt:    time.Now(),
	}
	run.DeadlineAt = run.StartedAt.Add(e.cfg.ShellTimeout)

	// Ownership is durable before any device write. A failed durable write
	// means the command is not dispatched at all.
	e.mu.Lock()
	if !e.workAuthorizedLocked(work, time.Now()) || e.session == nil || e.session.Epoch != session.Epoch {
		e.mu.Unlock()
		return e.finishWithError(op, "admission",
			protocol.NewError(409, protocol.CodeStopped, "命令未派发：原会话已撤销")), nil
	}
	op.SessionOrigin = session.Origin
	e.shell = run
	e.mu.Unlock()

	if err := e.persistSync(context.Background()); err != nil {
		return e.finishShellNotStarted(op, work, run,
			protocol.CodeDeviceUnavailable, "恢复账本不可写，命令未派发"), nil
	}

	// Dispatch boundary. One lock acquisition records the unknown dispatch
	// before any device write. The ready observation keeps its status and the
	// confirmed baseline is kept, so the next act revalidates the live target
	// against that O and the next observe still has its delta reference. The
	// projector is deliberately not reset: leaving its ref counter alone keeps
	// conservatively allocated refs.
	dispatchAt := time.Now()
	e.mu.Lock()
	if !e.workAuthorizedLocked(work, dispatchAt) || e.session == nil ||
		e.session.Epoch != run.Epoch || e.session.State != SessionActive {
		e.mu.Unlock()
		return e.finishShellNotStarted(op, work, run,
			protocol.CodeStopped, "命令未派发：原会话已撤销"), nil
	}
	work.DispatchUnknown = true
	e.session.ExecutionUnknown = true
	e.executionEvidenceRevision++
	run.State, run.Phase = ShellDispatched, "dispatch"
	run.StartedAt = dispatchAt
	run.DeadlineAt = dispatchAt.Add(e.cfg.ShellTimeout)
	e.shell = run
	e.touchSessionLocked(dispatchAt)
	e.mu.Unlock()
	e.logShell("shell.dispatch", run, eventlog.Str("phase", "dispatch"))

	// The boundary is crossed; a revocation before prepare is provably not
	// started and is reported as such (never as a dispatched unknown).
	if e.workRevoked(work) {
		return e.finishShellNotStarted(op, work, run, protocol.CodeStopped,
			"命令未启动：原会话已撤销"), nil
	}

	prepareCtx, cancelPrepare := context.WithTimeout(work.Ctx, 30*time.Second)
	prepareErr := e.dev.PrepareManagedShell(prepareCtx, run.RunnerID, []byte(req.Command))
	cancelPrepare()
	if prepareErr != nil {
		if e.workRevoked(work) {
			return e.finishShellNotStarted(op, work, run, protocol.CodeStopped,
				"命令未启动：原会话已撤销"), nil
		}
		return e.finishShellNotStarted(op, work, run, protocol.CodeShellStartFailed,
			"命令未能启动"), nil
	}

	startCtx, cancelStart := context.WithTimeout(work.Ctx, e.cfg.ShellStartTimeout+20*time.Second)
	start, startErr := e.dev.StartManagedShell(startCtx, run.RunnerID, run.Root, e.cfg.ShellTimeout)
	cancelStart()
	if startErr != nil {
		if errors.Is(startErr, device.ErrShellNotStarted) {
			if e.workRevoked(work) {
				return e.finishShellNotStarted(op, work, run, protocol.CodeStopped,
					"命令未启动：原会话已撤销"), nil
			}
			return e.finishShellNotStarted(op, work, run, protocol.CodeShellStartFailed,
				"命令未能启动"), nil
		}
		if e.workRevoked(work) {
			// The dispatch may still have reached the runner; only a confirmed
			// stop may release the barrier.
			return e.finishShellRevoked(op, work, run), nil
		}
		return e.finishShellUncertain(op, run, "无法确认本次命令开始状态"), nil
	}

	e.mu.Lock()
	run.Start, run.HasStart = start, start.HasIdentity
	run.State, run.Phase = ShellRunning, "running"
	run.DeadlineAt = time.Now().Add(e.cfg.ShellTimeout)
	e.mu.Unlock()
	e.persistAsync()

	waited := e.awaitManagedShell(work, run, run.DeadlineAt)
	switch {
	case waited.revoked:
		return e.finishShellRevoked(op, work, run), nil
	case waited.hasDone:
		return e.finishShellDone(op, work, run, waited.done), nil
	default:
		return e.finishShellTimeout(op, work, run), nil
	}
}

// shellAdmissionLocked resolves the reference to the current active session and
// applies the base gates. It performs no device I/O and never claims or retires
// the referenced observation.
func (e *Engine) shellAdmissionLocked(agent, reference string) (*sessionState, *protocol.APIError) {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	kind, obs, op := e.findReferenceLocked(reference)
	var session *sessionState
	switch kind {
	case "O":
		if obs == nil || obs.Agent != agent {
			return nil, protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")
		}
		session = e.sessionByEpochLocked(obs.SessionEpoch)
		if session == nil {
			return nil, protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原观察没有可用的会话")
		}
	case "P":
		if op == nil {
			if _, closed := e.closed[reference]; closed {
				return nil, protocol.NewError(409, protocol.CodeRequestClosed,
					"原请求已封闭且未执行")
			}
			return nil, protocol.NewError(409, protocol.CodeOperationUnknown, "原操作不可用")
		}
		if op.Agent != agent {
			return nil, protocol.NewError(409, protocol.CodeOperationNotFound, "原操作不可用")
		}
		if op.SessionOrigin == "" {
			return nil, protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原操作没有可用的会话")
		}
		session = e.sessionByOriginLocked(op.SessionOrigin)
		if session == nil {
			return nil, protocol.NewError(409, protocol.CodeReferenceNoSession,
				"原操作没有可用的会话")
		}
	default:
		return nil, protocol.Invalid("reference 类型无效")
	}
	switch {
	case session.State == SessionEnded:
		return nil, protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	case session != e.session || session.State != SessionActive:
		return nil, protocol.NewError(409, protocol.CodeReferenceNoSession,
			"原会话不是当前活动会话")
	case !sessionBeforeDeadline(session, now):
		return nil, protocol.NewError(409, protocol.CodeSessionExpired, "原会话已过期")
	}
	if ready, why := e.appReadyLocked(now); !ready {
		return nil, startBlockedError(why)
	}
	if session.AppInstanceID != e.app.InstanceID {
		return nil, startBlockedError(protocol.CodeControlUnavailable)
	}
	if session.Mode != e.app.Mode || session.ModeGeneration != e.app.ModeGeneration {
		return nil, startBlockedError(protocol.CodeModeNotSelected)
	}
	return session, nil
}

// shellWait is one bounded wait result for the managed runner.
type shellWait struct {
	done    device.ShellDone
	hasDone bool
	revoked bool
}

// awaitManagedShell polls the runner metadata until a done receipt appears, the
// execution budget expires or the work is revoked. Device round trips happen
// outside the state lock.
func (e *Engine) awaitManagedShell(work *workState, run *shellState, deadline time.Time) shellWait {
	timer := time.NewTimer(shellPollInterval)
	defer timer.Stop()
	for {
		if e.workRevoked(work) {
			return shellWait{revoked: true}
		}
		inspectCtx, cancel := context.WithTimeout(work.Ctx, 8*time.Second)
		snapshot, err := e.dev.InspectManagedShell(inspectCtx, run.RunnerID)
		cancel()
		if err == nil {
			e.recordShellSnapshot(run, snapshot)
			if snapshot.HasDone {
				return shellWait{done: snapshot.Done, hasDone: true}
			}
		}
		if !time.Now().Before(deadline) {
			return shellWait{}
		}
		select {
		case <-timer.C:
		case <-work.Ctx.Done():
		}
		timer.Reset(shellPollInterval)
	}
}

func (e *Engine) recordShellSnapshot(run *shellState, snapshot device.ShellSnapshot) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if snapshot.HasStart && !run.HasStart {
		run.Start, run.HasStart = snapshot.Start, true
	}
	if snapshot.HasDone {
		run.Done, run.HasDone = snapshot.Done, true
	}
}

// finishShellDone classifies a runner that produced its own end receipt.
func (e *Engine) finishShellDone(op *operationState, work *workState, run *shellState,
	done device.ShellDone) *protocol.Result {
	switch done.Reason {
	case terminationExited:
		return e.finishShellConfirmed(op, work, run, terminationExited, done.ExitCode, "finished")
	case terminationTimeout:
		return e.finishShellConfirmed(op, work, run, terminationTimeout, nil, "finished")
	case terminationCancelled:
		return e.finishShellConfirmed(op, work, run, terminationCancelled, nil, "finished")
	default:
		// The runner finalized but could not prove how the command ended. The
		// chain is gone; the action result stays unknown.
		e.confirmShellBarrier(run, terminationUnknown, nil)
		e.persistAsync()
		e.removeManagedShellDir(run)
		return e.finishShellUncertain(op, run, "无法确认本次命令结束状态")
	}
}

// finishShellTimeout terminates a run that exceeded its execution budget. A
// supervisor timeout is reported only when the exact process group is
// confirmed gone; otherwise the result is uncertain.
func (e *Engine) finishShellTimeout(op *operationState, work *workState, run *shellState) *protocol.Result {
	termination, err := e.terminateShellRun(run, terminationTimeout)
	if err != nil || !termination.Confirmed {
		return e.finishShellUncertain(op, run, "命令超时后无法确认受管链已停止")
	}
	return e.finishShellConfirmed(op, work, run, terminationTimeout, nil, "finished")
}

// finishShellRevoked terminates a cancelled run. A natural exit recorded before
// the signal keeps its own fact; a confirmed stop is a cancellation fact.
func (e *Engine) finishShellRevoked(op *operationState, work *workState, run *shellState) *protocol.Result {
	termination, err := e.terminateShellRun(run, terminationCancelled)
	if err != nil || !termination.Confirmed {
		return e.finishShellUncertain(op, run, "取消后无法确认受管链已停止")
	}
	if termination.ExitCode != nil {
		return e.finishShellConfirmed(op, work, run, terminationExited, termination.ExitCode, "executed_revoked")
	}
	return e.finishShellConfirmed(op, work, run, terminationCancelled, nil, "executed_revoked")
}

// finishShellNotStarted records a provably unstarted command. The dispatch
// boundary was crossed, so the response carries termination=not_started. The
// ready observation was never touched and no observation is published.
func (e *Engine) finishShellNotStarted(op *operationState, work *workState, run *shellState,
	code, message string) *protocol.Result {
	e.mu.Lock()
	e.noteShellTerminalLocked(run)
	run.State, run.Phase = ShellAborted, "not_started"
	run.Termination = terminationNotStarted
	run.ConfirmedAt = time.Now()
	work.DispatchUnknown = false
	if e.session != nil && e.session.Epoch == run.Epoch {
		e.session.ExecutionUnknown = e.uiPendingLocked()
	}
	if e.shell == run {
		e.shell = nil
	}
	e.confirmCleanupBarrierLocked()
	e.mu.Unlock()
	e.persistAsync()
	e.removeManagedShellDir(run)

	apiErr := protocol.NewError(protocol.Status(code), code, message)
	apiErr.Outcome = OutcomeNotExecuted
	result := &protocol.Result{
		Outcome: OutcomeNotExecuted,
		Shell:   &protocol.ShellView{Termination: terminationNotStarted},
	}
	return e.finishShellResult(op, work, result, OutcomeNotExecuted, apiErr)
}

// finishShellUncertain keeps the exact-chain barrier closed. No observation is
// published and no output is claimed.
func (e *Engine) finishShellUncertain(op *operationState, run *shellState, message string) *protocol.Result {
	e.mu.Lock()
	run.Phase = "unconfirmed"
	e.mu.Unlock()
	e.persistAsync()
	apiErr := protocol.NewError(504, protocol.CodeShellUnknown, message)
	apiErr.Outcome = OutcomeUncertain
	result := &protocol.Result{
		Outcome: OutcomeUncertain,
		Shell:   &protocol.ShellView{Termination: terminationUnknown},
	}
	e.logShell("shell.uncertain", run,
		eventlog.Str("phase", "uncertain"), eventlog.Str("state", run.State))
	return e.finishShellResult(op, nil, result, OutcomeUncertain, apiErr)
}

// finishShellConfirmed records one confirmed end, reads the bounded output
// once and promotes the cleanup barrier. It publishes no observation: the
// screen is only inspected again by the caller's next read.
func (e *Engine) finishShellConfirmed(op *operationState, work *workState, run *shellState,
	termination string, exitCode *int, phase string) *protocol.Result {
	output, truncated, outputErr := e.readShellOutput(run)
	e.confirmShellBarrier(run, termination, exitCode)
	e.persistAsync()
	e.removeManagedShellDir(run)

	result := &protocol.Result{
		Outcome: OutcomeExecuted,
		Shell: &protocol.ShellView{
			Termination: termination, ExitCode: exitCode,
			Output: output, Truncated: truncated, OutputError: outputErr,
		},
	}
	extra := []eventlog.Field{
		eventlog.Str("phase", phase),
		eventlog.Str("termination", termination),
		eventlog.Int("bytes", run.OutputBytes),
	}
	if exitCode != nil {
		extra = append(extra, eventlog.Int("exit_code", *exitCode))
	}
	if truncated != nil {
		extra = append(extra, eventlog.Bool("truncated", *truncated))
	}
	e.logShell("shell.finish", run, extra...)
	return e.finishShellResult(op, work, result, OutcomeExecuted, nil)
}

// confirmShellBarrier marks the exact runner gone and clears the barrier. It is
// release-of-barrier evidence only: the action outcome is never rewritten.
func (e *Engine) confirmShellBarrier(run *shellState, termination string, exitCode *int) {
	e.mu.Lock()
	e.noteShellTerminalLocked(run)
	run.State, run.Phase = ShellConfirmed, "confirmed"
	run.Termination, run.ExitCode = termination, exitCode
	if run.HasDone {
		run.Truncated = run.Done.Truncated
		if run.Done.HasBytes {
			run.OutputBytes = run.Done.Bytes
		}
	}
	run.ConfirmedAt = time.Now()
	if e.session != nil && e.session.Epoch == run.Epoch {
		e.session.ExecutionUnknown = e.uiPendingLocked()
	}
	if e.work != nil && e.work.OperationID == run.OperationID {
		e.work.DispatchUnknown = false
	}
	if e.shell == run {
		e.shell = nil
	}
	e.confirmCleanupBarrierLocked()
	e.mu.Unlock()
	e.launchExecutionCleanupRecheck()
}

// readShellOutput reads the retained prefix exactly once. An unreadable output
// is reported in shell.output_error and never changes the end fact.
func (e *Engine) readShellOutput(run *shellState) (*string, *bool, *protocol.Error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	data, err := e.dev.ReadManagedShellOutput(ctx, run.RunnerID)
	if err != nil {
		truncated := run.Truncated
		if truncated == nil && run.HasDone {
			truncated = run.Done.Truncated
		}
		return nil, truncated, &protocol.Error{
			Code: protocol.CodeShellOutput, Message: "命令已结束，但未能取得输出内容",
		}
	}
	limit := e.cfg.ShellOutputBytes
	if limit <= 0 {
		limit = 65536
	}
	text := strings.ToValidUTF8(string(data), "\uFFFD")
	truncated := run.Truncated
	if truncated == nil && run.HasDone {
		// The runner's own receipt is trusted truncation evidence even when the
		// text could not be read.
		truncated = run.Done.Truncated
	}
	if len(text) > limit {
		text = text[:limit]
		// A byte limit may split the last multi-byte rune; trim only that
		// incomplete tail so the text projection stays valid.
		for len(text) > 0 && !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		value := true
		truncated = &value
	} else if truncated == nil && run.Done.HasBytes {
		value := run.Done.Bytes > limit
		truncated = &value
	}
	e.mu.Lock()
	run.OutputBytes = len(text)
	e.mu.Unlock()
	return &text, truncated, nil
}

// terminateShellRun stops the exact recorded process group with an independent
// bounded context. Cancelling the work context never cancels this confirmation.
func (e *Engine) terminateShellRun(run *shellState, reason string) (device.ShellTermination, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.ShellConfirmTimeout+3*time.Second)
	defer cancel()
	e.mu.Lock()
	if !run.settled() {
		run.State, run.Phase = ShellTerminating, "terminating"
	}
	e.mu.Unlock()
	return e.dev.TerminateManagedShell(ctx, run.RunnerID, run.Root, reason)
}

func (e *Engine) removeManagedShellDir(run *shellState) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = e.dev.RemoveManagedShell(ctx, run.RunnerID, run.Root)
}

// finishShellResult records the operation facts. A managed command never
// captures, publishes or retires an observation: the ready O keeps its status
// and the confirmed baseline stays, so the next act revalidates the live target
// against that O and the next observe still has its delta reference. A command
// that changes the screen is answered by that revalidation, not by this result.
func (e *Engine) finishShellResult(op *operationState, work *workState, result *protocol.Result,
	outcome string, apiErr *protocol.APIError) *protocol.Result {
	if apiErr != nil {
		result.Error = apiErr.Public()
	}
	if work == nil || e.workRevoked(work) {
		result.Recovery = e.finishOperation(op, outcome, "executed_revoked", apiErr)
		e.addShellSession(op, result)
		return result
	}
	result.Recovery = e.finishOperation(op, outcome, "finished", apiErr)
	return result
}

// addShellSession attaches the original session view without changing any
// authority. A completed closure may already have moved it to lastSession.
func (e *Engine) addShellSession(op *operationState, result *protocol.Result) {
	e.mu.Lock()
	defer e.mu.Unlock()
	session := e.session
	if session == nil && e.lastSession != nil && op != nil && e.lastSession.Origin == op.SessionOrigin {
		session = e.lastSession
	}
	if session == nil {
		return
	}
	result.IncludeSession = true
	result.Session = e.sessionViewLocked(session)
}

// confirmCleanupBarrierLocked applies release-of-barrier evidence to a cleanup
// record whose execution uncertainty came from this shell chain. It promotes
// only the barrier fact (cleanup.execution); it never rewrites the action
// outcome, the session state or any observation.
func (e *Engine) confirmCleanupBarrierLocked() {
	cleanup := e.cleanup
	if cleanup == nil || e.uiPendingLocked() || e.legacyUI != nil || e.shell != nil && !e.shell.settled() {
		return
	}
	if cleanup.ExecReason == "ui_input" && e.ui == nil {
		return
	}
	if cleanup.Execution != protocol.CleanupUnconfirmed {
		return
	}
	cleanup.Execution = protocol.CleanupConfirmed
	if cleanup.Error != nil && cleanup.Error.Code == "execution_unconfirmed" {
		cleanup.Error = nil
	}
	// Recovery keeps its own copy of the durable closure facts; mirror the
	// promotion so the same pass may complete instead of blocking forever.
	if e.recovery != nil && e.recovery.Cleanup != nil {
		e.recovery.Cleanup.Execution = cleanup.Execution
		e.recovery.Cleanup.State = cleanup.State
	}
	e.broadcastAppLocked()
}

// closureComplete is the single completeness rule for the cleanup five-tuple.
func closureComplete(cleanup *cleanupState) bool {
	if cleanup == nil || cleanup.needsExecutionRecheck {
		return false
	}
	return cleanup.Execution != protocol.CleanupPending &&
		cleanup.Execution != protocol.CleanupUnconfirmed &&
		cleanup.Display != protocol.CleanupPending &&
		cleanup.Display != protocol.CleanupUnconfirmed &&
		cleanup.Tasks != protocol.CleanupPending &&
		cleanup.Tasks != protocol.CleanupUnconfirmed &&
		cleanup.Recents != protocol.CleanupPending &&
		cleanup.Recents != protocol.CleanupUnconfirmed
}

// confirmUnresolvedShell is the bounded clock retry for a chain that was left
// unresolved (an unconfirmed start or an unconfirmed termination). A natural
// end or a dead process releases the barrier; a live chain is stopped only
// when its session is already gone, because an active session may still own a
// legitimately running command.
func (e *Engine) confirmUnresolvedShell(ctx context.Context) {
	e.mu.Lock()
	run := e.shell
	if run == nil || run.settled() || e.work != nil || e.shellProbing {
		e.mu.Unlock()
		return
	}
	e.shellProbing = true
	sessionGone := e.session == nil || e.session.State == SessionEnded
	cleanupActive := e.cleanup != nil && e.cleanup.State != protocol.CleanupComplete
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.shellProbing = false
		e.mu.Unlock()
	}()

	inspectCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	snapshot, err := e.dev.InspectManagedShell(inspectCtx, run.RunnerID)
	cancel()
	if err != nil {
		return
	}
	switch {
	case snapshot.HasDone:
		e.completeUnresolvedShell(run, snapshot.Done)
	case !snapshot.HasStart || !snapshot.Alive:
		e.completeUnresolvedShell(run, device.ShellDone{})
	default:
		if !sessionGone && !cleanupActive {
			return
		}
		termination, termErr := e.terminateShellRun(run, terminationCancelled)
		if termErr != nil || !termination.Confirmed {
			return
		}
		done := device.ShellDone{Reason: terminationCancelled}
		if termination.ExitCode != nil {
			done.Reason, done.ExitCode = terminationExited, termination.ExitCode
		}
		e.completeUnresolvedShell(run, done)
	}
}

// completeUnresolvedShell releases the barrier with the evidence available. It
// never reads output (the original request is over) and never rewrites the
// already-frozen action outcome.
func (e *Engine) completeUnresolvedShell(run *shellState, done device.ShellDone) {
	termination := done.Reason
	switch termination {
	case terminationExited, terminationTimeout, terminationCancelled:
	default:
		termination = terminationUnknown
	}
	exitCode := done.ExitCode
	if termination != terminationExited {
		exitCode = nil
	}
	e.recordRecoveredShellOperation(run, termination, exitCode)
	e.confirmShellBarrier(run, termination, exitCode)
	e.mu.Lock()
	run.Truncated = done.Truncated
	if done.HasBytes {
		run.OutputBytes = done.Bytes
	}
	e.mu.Unlock()
	e.persistAsync()
	e.removeManagedShellDir(run)
	e.logShell("shell.barrier_confirmed", run,
		eventlog.Str("phase", "late_confirmation"),
		eventlog.Str("termination", termination))
}

// recordRecoveredShellOperation makes a runner restored from the ledger
// addressable by status(P). An operation that already finished in this process
// is left untouched: a late barrier confirmation never rewrites the frozen
// action outcome.
func (e *Engine) recordRecoveredShellOperation(run *shellState, termination string, exitCode *int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	op := e.operations[run.OperationID]
	if op != nil && op.State == OperationFinished {
		return
	}
	if op == nil {
		if len(e.operations) >= e.cfg.MaxOperations {
			e.evictOperationsLocked()
		}
		op = &operationState{ID: run.OperationID, Agent: run.Agent, Kind: "shell", CreatedAt: run.StartedAt}
		e.operations[run.OperationID] = op
	}
	op.State = OperationFinished
	op.Phase = "previous_instance"
	op.SessionOrigin = run.Origin
	op.FinishedAt = time.Now()
	if termination == terminationExited && exitCode != nil {
		op.Outcome = OutcomeExecuted
		return
	}
	op.Outcome = OutcomeUncertain
	op.Error = &protocol.Error{Code: protocol.CodeShellUnknown, Message: "无法确认本次命令结束状态"}
}

func (e *Engine) logShell(name string, run *shellState, extra ...eventlog.Field) {
	if e.log == nil || run == nil {
		return
	}
	fields := []eventlog.Field{
		eventlog.Str("operation_id", run.OperationID),
		eventlog.Bool("root", run.Root),
		eventlog.Str("state", run.State),
	}
	if run.Origin != "" {
		fields = append(fields, eventlog.Str("origin", run.Origin))
	}
	if run.CommandRunes > 0 {
		fields = append(fields, eventlog.Int("count", run.CommandRunes))
	}
	if !run.StartedAt.IsZero() {
		fields = append(fields, eventlog.Duration("ms", time.Since(run.StartedAt)))
	}
	e.log.Event(eventlog.Info, name, append(fields, extra...)...)
}
