package engine

import (
	"context"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Start admits one session attempt, creates or binds the target display, and
// publishes the first full observation. The session is established even if the
// first tree capture fails; in that case the outcome is executed and the caller
// recovers with observe(P).
func (e *Engine) Start(ctx context.Context, agent, p string) (*protocol.Result, *protocol.APIError) {
	op, duplicate, apiErr := e.registerOperation(p, agent, "start")
	if apiErr != nil {
		return nil, apiErr
	}
	if duplicate != nil {
		return duplicate, nil
	}
	now := time.Now()
	e.mu.Lock()
	ok, reason := e.canStartLocked(now)
	if !ok {
		var sessionOrigin string
		var sessionAgent string
		if e.session != nil {
			sessionOrigin = e.session.Origin
			sessionAgent = e.session.Agent
		}
		e.mu.Unlock()
		apiErr := startBlockedError(reason)
		if reason == protocol.CodeSessionExists && sessionOrigin != "" && sessionAgent == agent {
			apiErr.Details = map[string]any{"start_operation_id": sessionOrigin}
		}
		return e.finishWithError(op, "admission", apiErr), nil
	}
	e.mu.Unlock()

	work, busy := e.acquireWork(op, WorkStart)
	if busy != nil {
		return e.finishWithError(op, "admission", busy), nil
	}
	defer e.releaseWork(work)
	e.setOperationPhase(op, "admission")

	e.mu.Lock()
	mode := e.app.Mode
	appInstance := e.app.InstanceID
	modeGeneration := e.app.ModeGeneration
	var primary device.Display
	if e.app.Primary != nil {
		primary = *e.app.Primary
	}
	e.mu.Unlock()

	var width, height, density int
	if mode == "virtual" {
		w, h, d, err := e.dev.LogicalSize(work.Ctx)
		if err != nil {
			return e.finishWithError(op, "admission",
				protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法读取当前屏幕尺寸")), nil
		}
		width, height, density = w, h, d
	}

	commandID := ""
	if mode == "virtual" {
		commandID = NewUUID()
	}
	e.mu.Lock()
	ok, reason = e.canStartStateLocked(time.Now())
	if ok && (e.work != work || work.Revoked || work.Ctx.Err() != nil ||
		e.app.Mode != mode || e.app.InstanceID != appInstance || e.app.ModeGeneration != modeGeneration) {
		ok, reason = false, protocol.CodeStopped
	}
	if !ok {
		e.mu.Unlock()
		return e.finishWithError(op, "admission", startBlockedError(reason)), nil
	}
	e.nextSession++
	session := &sessionState{
		Origin: op.ID, Agent: agent, Mode: mode, State: SessionStarting,
		StartedAt: now, HardExpiresAt: now.Add(e.cfg.SessionHardLimit),
		AppInstanceID: appInstance, ModeGeneration: modeGeneration,
		Epoch: e.nextSession,
	}
	session.ExpiresAt = now.Add(e.cfg.SessionTTL)
	if session.ExpiresAt.After(session.HardExpiresAt) {
		session.ExpiresAt = session.HardExpiresAt
	}
	if mode == "primary" {
		session.Display = primary
	} else {
		session.Display = device.Display{
			Width: width, Height: height, Density: density, Generation: modeGeneration,
		}
	}
	e.session = session
	op.SessionOrigin = op.ID
	work.SessionEpoch = session.Epoch
	e.baseline = nil
	e.current = nil
	e.lastSession = nil
	if e.cleanup != nil && e.cleanup.State == protocol.CleanupComplete {
		e.cleanup = nil
	}
	var command *appCommand
	if mode == "virtual" {
		session.CreateCommand = commandID
		command = &appCommand{
			Command: protocol.AppCommand{
				ID: commandID, Kind: "create_display",
				Width: width, Height: height, DensityDPI: density,
			},
			RequestedAt: now, Kind: "create_display", AppInstance: appInstance,
		}
		e.commands = append(e.commands, command)
	}
	e.broadcastAppLocked()
	e.mu.Unlock()

	// The ordinary work position serializes projector state, not the state lock.
	e.projector.Reset()

	// Save original ownership and the create intent before touching Android.
	persistCtx, persistCancel := context.WithTimeout(ctx, e.cfg.LedgerTimeout)
	persistErr := e.persistSync(persistCtx)
	persistCancel()
	if persistErr != nil {
		e.abortStart(session, "ledger_unavailable")
		return e.finishWithError(op, "admission",
			protocol.NewError(503, protocol.CodeDeviceUnavailable, "恢复账本不可用，未开始创建")), nil
	}

	if e.workRevoked(work) {
		e.abortStart(session, protocol.CodeStopped)
		return e.finishRevokedStart(op, session), nil
	}
	e.setOperationPhase(op, "display_create")
	if mode == "virtual" {
		helperCtx, helperCancel := context.WithTimeout(work.Ctx, 10*time.Second)
		helperErr := e.dev.StartDisplayHelper(helperCtx, session.CreateCommand, appInstance)
		helperCancel()
		if helperErr != nil {
			e.abortStart(session, "display_helper_failed")
			return e.finishWithError(op, "display_create",
				protocol.NewErrorOutcome(503, protocol.CodeDisplayUnavailable,
					"显示创建助手未能启动", OutcomeNotExecuted)), nil
		}
		e.mu.Lock()
		stillStarting := e.workAuthorizedLocked(work, time.Now()) && e.session == session && session.State == SessionStarting
		if command != nil && stillStarting {
			command.Ready = true
		}
		e.broadcastAppLocked()
		e.mu.Unlock()
		if !stillStarting {
			return e.finishRevokedStart(op, session), nil
		}

		display, waitErr := e.waitForDisplay(work, session)
		if waitErr != nil {
			e.abortStart(session, waitErr.Code)
			result := e.finishWithError(op, "display_create", waitErr)
			e.mu.Lock()
			result.IncludeSession = true
			result.Session = e.sessionViewLocked(e.session)
			e.mu.Unlock()
			return result, nil
		}
		probe, probeErr := e.waitDisplayExists(work.Ctx, display.ID,
			remainingStartBudget(session, e.cfg.StartTimeout))
		if probeErr != nil || !probe.Exists {
			e.abortStart(session, "display_create_unconfirmed")
			result := e.finishWithError(op, "display_create",
				protocol.NewErrorOutcome(503, protocol.CodeDisplayCreate,
					"虚拟屏创建结果未确认", OutcomeUncertain))
			e.mu.Lock()
			result.IncludeSession = true
			result.Session = e.sessionViewLocked(e.session)
			result.Cleanup = e.cleanupViewLocked()
			e.mu.Unlock()
			return result, nil
		}
		display.SFID = probe.SFID
		if !e.activateSession(work, session, display) {
			return e.finishRevokedStart(op, session), nil
		}
	} else {
		probe, probeErr := e.waitDisplayExists(work.Ctx, 0, 4*time.Second)
		if probeErr != nil || !probe.Exists {
			e.abortStart(session, "primary_unavailable")
			return e.finishWithError(op, "display_bind",
				protocol.NewErrorOutcome(503, protocol.CodeDisplayUnavailable,
					"主屏共享目标不可用", OutcomeNotExecuted)), nil
		}
		primary.SFID = probe.SFID
		if !e.activateSession(work, session, primary) {
			return e.finishRevokedStart(op, session), nil
		}
	}

	persistCtx, persistCancel = context.WithTimeout(ctx, e.cfg.LedgerTimeout)
	_ = e.persistSync(persistCtx)
	persistCancel()

	e.setOperationPhase(op, "first_observation")
	public, _, obsErr := e.captureAndPublish(work.Ctx, work, session, true, false, "start")
	result := newResult(op)
	result.Outcome = OutcomeExecuted
	e.mu.Lock()
	result.IncludeSession = true
	result.Session = e.sessionViewLocked(e.session)
	e.mu.Unlock()
	if obsErr != nil {
		result.Observation = &protocol.Observation{
			Status: "failed", Missing: []string{"tree"}, Error: obsErr.Public(),
		}
		switch obsErr.Code {
		case "control_ui_on_virtual_display", protocol.CodeDisplayUnavailable, protocol.CodeStaleScreen:
			e.mu.Lock()
			e.revokeSessionLocked(obsErr.Code, time.Now())
			session = e.session
			e.broadcastAppLocked()
			e.mu.Unlock()
			e.startCleanup(session, obsErr.Code)
			e.mu.Lock()
			result.Session = e.sessionViewLocked(e.session)
			result.Cleanup = e.cleanupViewLocked()
			e.mu.Unlock()
		}
		// The two first-observation branches share the phase but not the next
		// step: an active session recovers with observe(P), a revoked one is a
		// closure/status fact. Record which fact this is before planning.
		e.mu.Lock()
		if e.session != nil && e.session.State == SessionEnded {
			op.InputState = protocol.RecoveryInputRevoked
		} else {
			op.InputState = protocol.RecoveryInputRetired
		}
		e.mu.Unlock()
		result.Recovery = e.finishOperation(op, OutcomeExecuted, "first_observation_failed", nil)
		return result, nil
	}
	result.Observation = public
	result.Recovery = e.finishOperation(op, OutcomeExecuted, "active", nil)
	return result, nil
}

// activateSession commits device evidence only to the original live owner.
func (e *Engine) activateSession(work *workState, session *sessionState, display device.Display) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session != session || session.State != SessionStarting || !e.workAuthorizedLocked(work, time.Now()) {
		return false
	}
	if session.Mode == "virtual" && (session.Display.ID != display.ID || session.Display.Generation != display.Generation) {
		return false
	}
	session.Display = display
	session.State = SessionActive
	e.touchSessionLocked(time.Now())
	e.removeCommandLocked(session.CreateCommand)
	e.broadcastAppLocked()
	return true
}

func (e *Engine) finishRevokedStart(op *operationState, session *sessionState) *protocol.Result {
	result := e.finishWithError(op, "display_create", protocol.NewError(409, protocol.CodeStopped, "启动已撤销"))
	e.mu.Lock()
	result.IncludeSession = true
	result.Session = e.sessionViewLocked(e.sessionByEpochLocked(session.Epoch))
	result.Cleanup = e.cleanupViewLocked()
	e.mu.Unlock()
	return result
}

// remainingStartBudget returns the time left in the create phase, with a small
// minimum so a fast receipt still gets a reasonable surface-registration grace.
func remainingStartBudget(session *sessionState, total time.Duration) time.Duration {
	remaining := time.Until(session.StartedAt.Add(total))
	if remaining < 3*time.Second {
		return 3 * time.Second
	}
	return remaining
}

// waitForDisplay waits for the App's created descriptor, bounded by the create
// phase. It never assumes success from the delivery of the create command.
func (e *Engine) waitForDisplay(work *workState, session *sessionState) (device.Display, *protocol.APIError) {
	deadline := time.Now().Add(e.cfg.StartTimeout)
	for {
		if e.workRevoked(work) {
			return device.Display{}, protocol.NewError(409, protocol.CodeStopped, "启动已撤销")
		}
		e.mu.Lock()
		if session.CreateResult != nil && !*session.CreateResult {
			code := session.CreateError
			e.mu.Unlock()
			apiErr := protocol.NewErrorOutcome(503, protocol.CodeDisplayCreate,
				"控制端未能创建虚拟屏", OutcomeNotExecuted)
			if code != "" {
				apiErr.WithDetail("app_code", controlledAppCode(code))
			}
			return device.Display{}, apiErr
		}
		// The App's descriptor alone is not creation evidence: it may still hold
		// a retiring screen from an earlier attempt. Wait for the create command's
		// explicit success receipt as well.
		created := session.CreateResult != nil && *session.CreateResult
		if created && session.Display.ID > 0 && e.app.Display != nil &&
			e.app.Display.ID == session.Display.ID {
			display := session.Display
			e.mu.Unlock()
			return display, nil
		}
		change := e.appNotify
		e.mu.Unlock()
		if time.Now().After(deadline) {
			return device.Display{}, protocol.NewErrorOutcome(503, protocol.CodeDisplayCreate,
				"虚拟屏创建结果未确认", OutcomeUncertain)
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-change:
		case <-timer.C:
		case <-work.Ctx.Done():
		}
		timer.Stop()
	}
}

func (e *Engine) removeCommandLocked(commandID string) {
	if commandID == "" {
		return
	}
	out := e.commands[:0]
	for _, command := range e.commands {
		if command.Command.ID == commandID {
			continue
		}
		out = append(out, command)
	}
	e.commands = out
}

func (e *Engine) abortStart(session *sessionState, reason string) {
	e.mu.Lock()
	if e.session == session {
		e.revokeSessionLocked(reason, time.Now())
	}
	e.broadcastAppLocked()
	e.mu.Unlock()
	e.startCleanup(session, reason)
}

func (e *Engine) startCleanup(session *sessionState, reason string) {
	e.launchCleanup(session, reason)
}

func startBlockedError(reason string) *protocol.APIError {
	message := "当前不可开始会话"
	code := reason
	switch reason {
	case protocol.CodeSessionExists:
		message = "已有活动会话"
	case protocol.CodeDeviceBusy:
		message = "设备工作尚未完成"
	case protocol.CodeControlUnavailable:
		message = "控制端不可用"
	case protocol.CodeStopUnavailable:
		message = "停止通道未就绪"
	case protocol.CodeModeNotSelected:
		message = "尚未选择可用模式"
	case protocol.CodeDeviceLocked:
		message = "设备已锁定"
	case protocol.CodeDeviceNotAwake:
		message = "设备未唤醒"
	case protocol.CodeRecovering, "recovery_pending", "old_resource_unconfirmed",
		"control_sync_pending", "no_display_recorded":
		message = "服务正在核验旧资源"
	case protocol.CodeCleanupUnconfirmed:
		message = "原资源收尾未确认"
	case "ledger_unavailable", "ledger_corrupt":
		message = "恢复账本不可用"
	case protocol.CodeDisplayUnavailable:
		message = "目标屏幕不可用"
	case protocol.CodeStopped:
		message = "已收到停止，等待控制端同步"
	}
	status := protocol.Status(code)
	if status == 500 {
		status = 409
	}
	return protocol.NewError(status, code, message)
}

// controlledAppCode keeps App error identifiers bounded and log-safe.
func controlledAppCode(code string) string {
	if len(code) > 64 {
		return code[:64]
	}
	return code
}
