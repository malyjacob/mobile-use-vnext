package engine

import (
	"context"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// SyncApp accepts one control App heartbeat, records its local facts, processes
// command results and stop intent, and returns the next control view and at
// most one display command. It never performs device I/O.
func (e *Engine) SyncApp(ctx context.Context, req *protocol.AppSyncRequest) (*protocol.AppSyncResponse, *protocol.APIError) {
	if req.ContractVersion != protocol.ContractVersion {
		return nil, protocol.NewError(409, protocol.CodeContractMismatch,
			"控制端与服务的普通契约版本不匹配")
	}
	if !ValidateUUID(req.AppInstanceID) {
		return nil, protocol.Invalid("app_instance_id 必须是 UUID")
	}
	if req.SelectedMode != "virtual" && req.SelectedMode != "primary" {
		return nil, protocol.Invalid("selected_mode 必须是 virtual 或 primary")
	}
	if req.ModeGeneration == "" || len(req.ModeGeneration) > 64 {
		return nil, protocol.Invalid("mode_generation 无效")
	}
	if req.PrimaryDisplay != nil {
		if err := req.PrimaryDisplay.Validate(); err != nil || req.PrimaryDisplay.DisplayID != 0 {
			return nil, protocol.Invalid("primary_display 描述无效")
		}
	}
	if req.Display != nil {
		if err := req.Display.Validate(); err != nil || req.Display.DisplayID == 0 {
			return nil, protocol.Invalid("display 描述无效")
		}
		if req.Display.DisplayID == 0 {
			return nil, protocol.Invalid("虚拟显示不能是主屏")
		}
	}
	if req.StopPending != nil {
		if req.StopPending.Seq <= 0 || len(req.StopPending.Reason) > 64 {
			return nil, protocol.Invalid("stop_pending 无效")
		}
	}
	for _, result := range req.CommandResults {
		if result.CommandID == "" || len(result.CommandID) > 64 {
			return nil, protocol.Invalid("command_results 无效")
		}
		if result.ErrorCode != nil && len(*result.ErrorCode) > 64 {
			return nil, protocol.Invalid("command_results 错误码无效")
		}
	}
	if len(req.MessageAcks) > maxMessageAcks {
		return nil, protocol.Invalid("message_acks 超过长度上限").WithDetail("limit", maxMessageAcks)
	}
	for _, id := range req.MessageAcks {
		if !validMessageID(id) {
			return nil, protocol.Invalid("message_acks 无效")
		}
	}

	now := time.Now()
	var revoked *sessionState
	var instanceRevoked *sessionState
	var needRecovery bool
	var messagesChanged bool

	e.mu.Lock()
	instanceChanged := e.app.InstanceID != "" && e.app.InstanceID != req.AppInstanceID
	if instanceChanged {
		instanceRevoked = e.onAppInstanceChangedLocked(req.AppInstanceID, now)
	}
	e.app = appState{
		InstanceID:      req.AppInstanceID,
		Version:         req.AppVersion,
		Mode:            req.SelectedMode,
		ModeGeneration:  req.ModeGeneration,
		ModeReady:       req.ModeReady,
		StopReady:       req.StopReady,
		Notifications:   req.NotificationsEnabled,
		Background:      req.BackgroundRunning,
		Power:           req.PowerInteractive,
		Keyguard:        req.KeyguardLocked,
		Primary:         appDisplayToDevice(req.PrimaryDisplay),
		Display:         appDisplayToDevice(req.Display),
		DisplayRaw:      req.Display,
		LastSync:        now,
		StopPending:     req.StopPending,
		ContractMatched: true,
	}
	if req.Display != nil && e.session != nil && e.session.State == SessionStarting &&
		e.session.AppInstanceID == req.AppInstanceID && req.Display.Generation == e.session.CreateCommand {
		e.session.Display = *e.app.Display
		if e.log != nil {
			e.log.Event(eventlog.Debug, "control.display_reported",
				eventlog.Int("display_id", e.app.Display.ID),
				eventlog.Str("command_id", e.session.CreateCommand))
		}
	}
	e.acceptCommandResultsLocked(req)
	if req.StopPending != nil {
		accepted, session := e.bindStopLocked(req.AppInstanceID, req.StopPending.Seq, req.StopPending.Reason, now)
		if accepted {
			revoked = session
		}
	}
	if e.recovering && !e.recoveryLoading && e.recovery == nil && e.blockedReason == "" {
		ready, _ := e.appReadyLocked(now)
		if ready && e.cleanup == nil {
			e.recovering = false
		}
	}
	needRecovery = e.recovery != nil
	messagesChanged = e.ackMessagesLocked(req.MessageAcks)
	view := e.controlViewLocked(now)
	commands := e.pendingCommandsLocked(now)
	messages := e.deliveryMessagesLocked()
	response := &protocol.AppSyncResponse{
		ContractVersion:   protocol.ContractVersion,
		ServiceInstanceID: e.cfg.ServiceInstanceID,
		Control:           view,
		Commands:          commands,
		Messages:          messages,
	}
	if req.StopPending != nil && e.stopBoundary != nil &&
		e.stopBoundary.AppInstanceID == req.AppInstanceID &&
		e.stopBoundary.Seq >= req.StopPending.Seq {
		state := "accepted"
		if e.stopBoundary.Seq > req.StopPending.Seq {
			state = "duplicate"
		}
		response.Stop = &protocol.AppStopAck{Seq: req.StopPending.Seq, State: state}
	}
	e.broadcastAppLocked()
	e.mu.Unlock()

	if revoked != nil {
		e.launchCleanup(revoked, "user_stop")
	}
	if instanceRevoked != nil {
		e.launchCleanup(instanceRevoked, "app_restarted")
	}
	if needRecovery {
		e.wg.Add(1)
		go func() { defer e.wg.Done(); e.driveRecovery(context.Background()) }()
	}
	if messagesChanged {
		// Releasing acknowledged messages happens outside the state lock and
		// never blocks the heartbeat's control facts.
		e.persistMessages()
	}
	e.persistAsync()
	return response, nil
}

func appDisplayToDevice(display *protocol.AppDisplay) *device.Display {
	if display == nil {
		return nil
	}
	return &device.Display{
		ID: display.DisplayID, Width: display.Width, Height: display.Height,
		Rotation: display.Rotation, Density: display.Density, Generation: display.Generation,
	}
}

// onAppInstanceChangedLocked discards old-instance commands and revokes a
// session whose physical owner disappeared. The original responsibility stays
// in the ledger and recovery, never silently rebound to the new instance.
func (e *Engine) onAppInstanceChangedLocked(newInstance string, now time.Time) *sessionState {
	retained := e.commands[:0]
	for _, command := range e.commands {
		if command.AppInstance == newInstance {
			retained = append(retained, command)
		}
	}
	e.commands = retained
	if e.session != nil && e.session.State != SessionEnded &&
		e.session.AppInstanceID != "" && e.session.AppInstanceID != newInstance {
		e.revokeSessionLocked("app_restarted", now)
		return e.session
	}
	return nil
}

func (e *Engine) acceptCommandResultsLocked(req *protocol.AppSyncRequest) {
	for _, result := range req.CommandResults {
		for i, command := range e.commands {
			if command.Command.ID != result.CommandID {
				continue
			}
			if command.AppInstance != "" && command.AppInstance != req.AppInstanceID {
				continue
			}
			ok := result.OK
			command.Result = &ok
			if result.ErrorCode != nil {
				command.ErrorCode = *result.ErrorCode
			}
			if command.Kind == "create_display" && e.session != nil &&
				e.session.CreateCommand == command.Command.ID {
				value := result.OK
				e.session.CreateResult = &value
				if result.ErrorCode != nil {
					e.session.CreateError = *result.ErrorCode
				}
				if e.log != nil {
					e.log.Event(eventlog.Debug, "control.create_result",
						eventlog.Str("command_id", command.Command.ID),
						eventlog.Bool("available", result.OK))
				}
			}
			// Any acknowledgement closes this delivery, including ok=false
			// without an optional error code. It must never be re-executed.
			e.commands = append(e.commands[:i], e.commands[i+1:]...)
			break
		}
	}
}

func (e *Engine) pendingCommandsLocked(now time.Time) []protocol.AppCommand {
	out := []protocol.AppCommand{}
	retained := e.commands[:0]
	for _, command := range e.commands {
		if command.AppInstance != e.app.InstanceID {
			continue
		}
		ttl := e.cfg.CleanupTimeout
		if command.Kind == "create_display" {
			ttl = e.cfg.StartTimeout
			if e.session == nil || e.session.State != SessionStarting ||
				e.session.AppInstanceID != e.app.InstanceID || e.session.CreateCommand != command.Command.ID {
				continue
			}
		}
		if now.Sub(command.RequestedAt) >= ttl {
			continue
		}
		retained = append(retained, command)
		if len(out) > 0 || !command.DeliveredAt.IsZero() || (command.Kind == "create_display" && !command.Ready) {
			continue
		}
		command.DeliveredAt = now
		out = append(out, command.Command)
		if e.log != nil {
			e.log.Event(eventlog.Debug, "control.command_delivered",
				eventlog.Str("command_id", command.Command.ID), eventlog.Str("kind", command.Kind))
		}
	}
	e.commands = retained
	return out
}

// controlComparable is the revision fingerprint of actual control facts.
type controlComparable struct {
	availability                               string
	reason                                     string
	sessOrigin, sessAgent, sessMode, sessState string
	sessExpires, sessEnded, sessReason         string
	workOp, workKind, workPhase                string
	cleanupState, cleanupExec, cleanupTasks    string
	cleanupRecents, cleanupDisplay             string
}

func (e *Engine) controlViewLocked(now time.Time) protocol.ControlView {
	availability, reason := e.availabilityLocked(now)
	view := protocol.ControlView{
		Availability: protocol.Availability{State: availability, Reason: reason},
	}
	if e.session != nil {
		switch {
		case e.session.State == SessionEnded && e.cleanup != nil &&
			e.cleanup.State == protocol.CleanupComplete:
			// The completed old session no longer occupies the control view.
		default:
			view.Session = &protocol.ControlSession{
				Origin: e.session.Origin, Agent: e.session.Agent, Mode: e.session.Mode,
				State: e.session.State, StartedAt: formatTime(e.session.StartedAt),
			}
			switch e.session.State {
			case SessionStarting, SessionActive:
				view.Session.ExpiresAt = formatTime(e.session.ExpiresAt)
			case SessionEnded:
				view.Session.EndedAt = formatTime(e.session.EndedAt)
				view.Session.Reason = e.session.Reason
			}
		}
	}
	if e.work != nil {
		view.Work = &protocol.ControlWork{
			OperationID: e.work.OperationID, Kind: e.work.Kind, Phase: e.work.Phase,
		}
	}
	if e.cleanup != nil && view.Session != nil && view.Session.State == SessionEnded {
		view.Cleanup = &protocol.ControlCleanup{
			State: e.cleanup.State, Execution: e.cleanup.Execution,
			Tasks: e.cleanup.Tasks, Recents: e.cleanup.Recents, Display: e.cleanup.Display,
		}
	}
	fingerprint := controlComparable{
		availability: availability, reason: reason,
		cleanupState: cleanupStateOf(view.Cleanup), cleanupExec: cleanupExecOf(view.Cleanup),
		cleanupTasks: cleanupTasksOf(view.Cleanup), cleanupRecents: cleanupRecentsOf(view.Cleanup),
		cleanupDisplay: cleanupDisplayOf(view.Cleanup),
	}
	if view.Session != nil {
		fingerprint.sessOrigin = view.Session.Origin
		fingerprint.sessAgent = view.Session.Agent
		fingerprint.sessMode = view.Session.Mode
		fingerprint.sessState = view.Session.State
		fingerprint.sessExpires = view.Session.ExpiresAt
		fingerprint.sessEnded = view.Session.EndedAt
		fingerprint.sessReason = view.Session.Reason
	}
	if view.Work != nil {
		fingerprint.workOp = view.Work.OperationID
		fingerprint.workKind = view.Work.Kind
		fingerprint.workPhase = view.Work.Phase
	}
	if e.lastFingerprint == nil || *e.lastFingerprint != fingerprint {
		e.controlRev++
		e.lastFingerprint = &fingerprint
	}
	view.Revision = e.controlRev
	return view
}

func cleanupStateOf(c *protocol.ControlCleanup) string {
	if c == nil {
		return ""
	}
	return c.State
}
func cleanupExecOf(c *protocol.ControlCleanup) string {
	if c == nil {
		return ""
	}
	return c.Execution
}
func cleanupTasksOf(c *protocol.ControlCleanup) string {
	if c == nil {
		return ""
	}
	return c.Tasks
}
func cleanupRecentsOf(c *protocol.ControlCleanup) string {
	if c == nil {
		return ""
	}
	return c.Recents
}
func cleanupDisplayOf(c *protocol.ControlCleanup) string {
	if c == nil {
		return ""
	}
	return c.Display
}

// bindStopLocked records one minimal stop boundary and revokes the original
// session, if any. It performs no I/O and never waits for physical cleanup.
func (e *Engine) bindStopLocked(appInstance string, seq int64, reason string, now time.Time) (bool, *sessionState) {
	if appInstance == "" || seq <= 0 {
		return false, nil
	}
	if e.stopBoundary != nil && e.stopBoundary.AppInstanceID == appInstance &&
		seq <= e.stopBoundary.Seq {
		return false, nil
	}
	if highest := e.stopSeqByApp[appInstance]; seq <= highest {
		return false, nil
	}
	e.stopSeqByApp[appInstance] = seq
	session := e.session
	if session != nil && session.State != SessionEnded {
		session = e.revokeSessionLocked(reason, now)
	} else {
		session = nil
	}
	e.stopBoundary = &stopBoundaryState{
		AppInstanceID: appInstance, Seq: seq, AcceptedAt: now,
	}
	if session != nil {
		e.stopBoundary.State = "revoked"
	} else {
		e.stopBoundary.State = "recorded"
	}
	if e.current != nil && e.current.Status == ObservationReady {
		e.current.Status = ObservationRetired
		e.current.RetiredAt = now
	}
	e.broadcastAppLocked()
	return true, session
}

// Stop is the minimal independent stop carrier. It never waits for ordinary
// work, the ledger writer or physical cleanup.
func (e *Engine) Stop(ctx context.Context, req *protocol.ControlStopRequest) (*protocol.ControlStopResponse, *protocol.APIError) {
	if !ValidateUUID(req.AppInstanceID) {
		return nil, protocol.Invalid("app_instance_id 必须是 UUID")
	}
	if req.StopSeq <= 0 {
		return nil, protocol.Invalid("stop_seq 无效")
	}
	if req.Reason == "" || len(req.Reason) > 64 {
		req.Reason = "user_stop"
	}
	now := time.Now()
	e.mu.Lock()
	accepted, session := e.bindStopLocked(req.AppInstanceID, req.StopSeq, req.Reason, now)
	state := "duplicate"
	if accepted {
		if e.stopBoundary != nil {
			state = e.stopBoundary.State
		}
	}
	response := &protocol.ControlStopResponse{
		ContractVersion: protocol.ContractVersion, ServiceInstanceID: e.cfg.ServiceInstanceID,
		Accepted: true, StopSeq: req.StopSeq, State: state,
	}
	e.mu.Unlock()
	if accepted && session != nil {
		e.launchCleanup(session, "user_stop")
	}
	e.persistAsync()
	return response, nil
}

// StartRecovery loads the durable ledger and establishes the startup barrier.
// The service stays recovering until old ownership is resolved and the paired
// App facts are valid.
func (e *Engine) StartRecovery(ctx context.Context) error {
	e.loadMessages()
	e.mu.Lock()
	e.recovering, e.recoveryLoading = true, true
	e.mu.Unlock()
	record, err := e.store.Load()
	if err != nil {
		e.mu.Lock()
		e.blockedReason = "ledger_corrupt"
		e.recovering, e.recoveryLoading = false, false
		e.mu.Unlock()
		if e.log != nil {
			e.log.Event(eventlog.Error, "ledger.load_failed", eventlog.Str("code", "ledger_corrupt"))
		}
		return nil
	}
	e.mu.Lock()
	e.recovering, e.recoveryLoading = true, false
	e.recovery = record
	if record != nil {
		e.droppedUnconfirmed = record.DroppedUnconfirmed
	}
	e.restoreRecoveryLocked(record)
	if record != nil && record.Stop != nil {
		e.stopBoundary = &stopBoundaryState{
			AppInstanceID: record.Stop.AppInstanceID, Seq: record.Stop.Seq,
			AcceptedAt: record.Stop.AcceptedAt, State: "recorded",
		}
		e.stopSeqByApp[record.Stop.AppInstanceID] = record.Stop.Seq
	}
	e.mu.Unlock()
	if record == nil {
		return nil
	}
	e.driveRecovery(ctx)
	return nil
}

// driveRecovery resolves the durable old ownership. It only reads device
// evidence and issues a precise close through the paired App when needed.
func (e *Engine) driveRecovery(ctx context.Context) {
	e.mu.Lock()
	record := e.recovery
	if record == nil || e.recoveryRunning || e.recoveryLoading || e.shuttingDown {
		e.mu.Unlock()
		return
	}
	ready, _ := e.appReadyLocked(time.Now())
	if !ready || !e.deviceReady {
		e.recoveryReason = "control_sync_pending"
		e.mu.Unlock()
		return
	}
	e.recoveryRunning = true
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.recoveryRunning = false; e.broadcastAppLocked(); e.mu.Unlock() }()

	e.recoveryRetire(ctx, record)
}

func (e *Engine) setRecoveryReason(reason string) {
	e.mu.Lock()
	e.recoveryReason = reason
	e.mu.Unlock()
}

func (e *Engine) finishRecovery(record *Ledger, app appState, displayID int, reason string) {
	e.mu.Lock()
	ready, _ := e.appReadyLocked(time.Now())
	if e.recovery != record || !ready || !e.deviceReady || e.shuttingDown ||
		e.app.InstanceID != app.InstanceID || e.app.ModeGeneration != app.ModeGeneration || e.app.Display != nil {
		e.recoveryReason = "control_sync_pending"
		e.mu.Unlock()
		return
	}
	e.recovery = nil
	e.ui, e.legacyUI = nil, nil
	e.uiCheckpointReady = false
	e.recovering = false
	e.recoveryReason = ""
	execution, recents := protocol.CleanupNotApplicable, protocol.CleanupNotApplicable
	if record.UI != nil && record.UI.State != "may_dispatch" {
		execution = protocol.CleanupConfirmed
	}
	if record.Cleanup != nil {
		if record.Cleanup.Execution != "" {
			execution = record.Cleanup.Execution
		}
		if record.Cleanup.Recents != "" {
			recents = record.Cleanup.Recents
		}
	}
	tasks, display := protocol.CleanupNotApplicable, protocol.CleanupNotApplicable
	if displayID > 0 {
		tasks, display = protocol.CleanupConfirmed, protocol.CleanupConfirmed
	}
	e.cleanup = &cleanupState{
		State: protocol.CleanupComplete, Execution: execution,
		Tasks: tasks, Recents: recents, Display: display, Reason: reason, StartedAt: time.Now(),
	}
	if e.session != nil {
		e.lastSession, e.session = e.session, nil
	}
	e.commands = nil
	e.broadcastAppLocked()
	e.mu.Unlock()
	if e.log != nil {
		e.log.Event(eventlog.Info, "recovery.complete", eventlog.Str("reason", reason))
	}
	e.persistAsync()
}

func unresolvedCleanupFact(state string) bool {
	return state == protocol.CleanupPending || state == protocol.CleanupUnconfirmed
}

func (e *Engine) restoreRecoveryLocked(record *Ledger) {
	if record == nil {
		return
	}
	if s := record.Session; s != nil {
		e.nextSession++
		e.session = &sessionState{Origin: s.Origin, Agent: s.Agent, Mode: s.Mode, State: SessionEnded,
			StartedAt: s.StartedAt, EndedAt: time.Now(), Reason: "service_restarted", Epoch: e.nextSession,
			AppInstanceID: s.AppInstanceID, ModeGeneration: s.ModeGeneration, CleanupStarted: true,
			Display: device.Display{ID: s.DisplayID, SFID: s.SFID, Width: s.Width, Height: s.Height, Density: s.Density, Rotation: s.Rotation, Generation: s.DisplayGeneration}}
		if len(record.Tasks) > 0 {
			e.session.Tasks = tasksFromLedger(record.Tasks)
		}
		e.operations[s.Origin] = &operationState{ID: s.Origin, Agent: s.Agent, Kind: "start", State: OperationFinished,
			SessionOrigin: s.Origin, Outcome: OutcomeUncertain, Phase: "service_restarted", CreatedAt: s.StartedAt, FinishedAt: time.Now()}
	}
	if c := record.PendingClose; c != nil && c.DisplayID > 0 && c.AppInstanceID != "" && c.DisplayGeneration != "" {
		id := c.DisplayID
		e.commands = append(e.commands, &appCommand{Command: protocol.AppCommand{ID: c.ID, Kind: "close_display",
			TargetAppInstanceID: c.AppInstanceID, TargetDisplayID: &id, TargetGeneration: c.DisplayGeneration},
			Kind: "close_display", AppInstance: c.AppInstanceID, RequestedAt: c.RequestedAt})
	}
	// The runner identity is restored for barrier resolution only: it never
	// revives a session and never grants new input authority.
	e.shell = shellStateFromLedger(record.Shell)
	// The durable closure progress is restored so recovery can still tell a
	// shell-caused execution uncertainty (promotable by exact-chain release
	// evidence) from a sticky UI-input one. No chain is restarted.
	if c := record.Cleanup; c != nil {
		e.cleanup = &cleanupState{
			State: c.State, Execution: c.Execution, Tasks: c.Tasks,
			Recents: c.Recents, Display: c.Display, Reason: c.Reason,
			ExecReason: c.ExecReason, StartedAt: c.StartedAt,
			checkedTaskIDs:        append([]int(nil), c.CheckedTaskIDs...),
			needsExecutionRecheck: c.ExecutionRecheckRequired,
		}
	}
	e.restoreUIRecoveryLocked(record)
}

// enqueueCloseCommand adds one precise close command for the original display.
func (e *Engine) enqueueCloseCommand(displayID int, generation, appInstance string) {
	id := NewUUID()
	e.mu.Lock()
	defer e.mu.Unlock()
	if displayID <= 0 || appInstance == "" || generation == "" {
		return
	}
	for _, pending := range e.commands {
		if pending.Kind == "close_display" && pending.Command.TargetDisplayID != nil &&
			*pending.Command.TargetDisplayID == displayID && pending.Command.TargetGeneration == generation &&
			pending.AppInstance == appInstance && pending.DeliveredAt.IsZero() {
			return
		}
	}
	e.commands = append(e.commands, &appCommand{
		Command: protocol.AppCommand{
			ID: id, Kind: "close_display", TargetAppInstanceID: appInstance,
			TargetDisplayID: &displayID, TargetGeneration: generation,
		},
		RequestedAt: time.Now(), Kind: "close_display", AppInstance: appInstance,
	})
	e.broadcastAppLocked()
}

// ledgerRecordLocked snapshots the minimal durable ownership facts.
func (e *Engine) ledgerRecordLocked() *Ledger {
	e.trimRetiredLocked()
	record := &Ledger{DroppedUnconfirmed: e.droppedUnconfirmed, Format: ledgerFormat, Version: ledgerVersion, ServiceInstanceID: e.cfg.ServiceInstanceID, UpdatedAt: time.Now(), AppInstanceID: e.app.InstanceID}
	if e.recovery != nil {
		return CloneLedger(e.recovery)
	}
	if e.session != nil && e.session.State != SessionEnded {
		record.Session = ledgerSessionOf(e.session)
		record.Tasks = ledgerTasksOf(e.session.Tasks)
		record.Shell = e.shell.ledger()
		if e.ui != nil {
			v := e.ui.record
			record.UI = &v
		}
		if e.legacyUI != nil {
			v := *e.legacyUI
			record.LegacyUI = &v
		}
		for _, command := range e.commands {
			if command.Kind == "create_display" && command.Command.ID == e.session.CreateCommand {
				record.PendingCreate = &LedgerCommand{ID: command.Command.ID, Kind: command.Kind, AppInstanceID: command.AppInstance, RequestedAt: command.RequestedAt}
			}
		}
	}
	for _, r := range e.retired {
		record.Retired = append(record.Retired, r.ledger())
	}
	if e.stopBoundary != nil {
		record.Stop = &LedgerStop{AppInstanceID: e.stopBoundary.AppInstanceID, Seq: e.stopBoundary.Seq, AcceptedAt: e.stopBoundary.AcceptedAt}
	}
	return record
}

// persistSync durably records the current ownership before a new side effect.
func (e *Engine) persistSync(ctx context.Context) error {
	e.persistMu.Lock()
	defer e.persistMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	record := e.ledgerRecordLocked()
	e.mu.Unlock()
	saveCtx, cancel := context.WithTimeout(ctx, e.cfg.LedgerTimeout)
	defer cancel()
	if _, err := e.store.Save(saveCtx, record); err != nil {
		e.mu.Lock()
		e.ledgerUnsafe = true
		e.mu.Unlock()
		if e.log != nil {
			e.log.Event(eventlog.Error, "ledger.save_failed", eventlog.Str("code", "ledger_unavailable"))
		}
		return err
	}
	e.mu.Lock()
	e.ledgerUnsafe = false
	e.mu.Unlock()
	return nil
}

// persistAsync records progress without blocking STOP or delivery.
func (e *Engine) persistAsync() {
	e.persistScheduleMu.Lock()
	e.persistRequested = true
	if e.persistRunning {
		e.persistScheduleMu.Unlock()
		return
	}
	e.persistRunning = true
	e.wg.Add(1)
	e.persistScheduleMu.Unlock()
	go func() {
		defer e.wg.Done()
		for {
			e.persistScheduleMu.Lock()
			e.persistRequested = false
			e.persistScheduleMu.Unlock()
			_ = e.persistSync(context.Background())
			e.persistScheduleMu.Lock()
			if !e.persistRequested {
				e.persistRunning = false
				e.persistScheduleMu.Unlock()
				return
			}
			e.persistScheduleMu.Unlock()
		}
	}()
}
