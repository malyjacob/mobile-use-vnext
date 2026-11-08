package engine

import (
	"context"
	"encoding/json/v2"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// waitDisplayExists polls the display identity for a bounded grace period.
// SurfaceFlinger/DisplayManager can expose a newly created display a moment
// after the App reports it; a single transient miss is not absence.
func (e *Engine) waitDisplayExists(ctx context.Context, displayID int,
	timeout time.Duration) (device.DisplayState, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		state, err := e.dev.ProbeDisplay(probeCtx, displayID)
		cancel()
		if err == nil && state.Exists {
			return state, nil
		}
		lastErr = err
		if time.Now().After(deadline) || ctx.Err() != nil {
			return device.DisplayState{}, lastErr
		}
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return device.DisplayState{}, lastErr
		}
	}
}

// displayConfirmedAbsent requires two consecutive definitive absence probes so
// a transient listing gap is never reported as physical release.
func (e *Engine) displayConfirmedAbsent(ctx context.Context, displayID int) (bool, bool) {
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return false, false
			}
		}
		absent, known := e.displayAbsent(ctx, displayID)
		if !known {
			return false, false
		}
		if !absent {
			return false, true
		}
	}
	return true, true
}

// captureAndPublish performs one complete read-only acquisition and, if all
// commit gates still hold, publishes exactly one new observation. The capture
// and tree work happen outside the state lock.
func (e *Engine) captureAndPublish(ctx context.Context, work *workState, session *sessionState,
	forceFull bool, phase string) (*protocol.Observation, *observationState, *protocol.APIError) {
	e.setWorkPhase(work, phase+".capture")
	snapshot := e.sessionForWork(work)
	if session == nil || snapshot == nil || snapshot.Epoch != session.Epoch {
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "原会话已撤销")
	}
	session = snapshot
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	state, err := e.dev.ProbeDisplay(probeCtx, session.Display.ID)
	cancel()
	if err != nil || !state.Exists {
		return nil, nil, protocol.NewError(503, protocol.CodeDisplayUnavailable, "目标屏幕不可用")
	}
	e.setWorkPhase(work, phase+".tree")
	cap, err := e.dev.Capture(ctx, session.Display)
	if err != nil {
		return nil, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "未能取得节点树")
	}
	now := time.Now()
	if cap.CapturedAt.IsZero() {
		cap.CapturedAt = now
	}
	cap.Display = session.Display
	if now.Sub(cap.CapturedAt) > e.cfg.CaptureMaxAge {
		return nil, nil, protocol.NewError(409, protocol.CodeStaleScreen, "节点采集已过期")
	}
	if session.Display.ID != 0 {
		for _, node := range cap.Nodes {
			if node.Package == e.cfg.ControlPackage && node.Visible {
				return nil, nil, protocol.NewError(409, "control_ui_on_virtual_display",
					"控制界面出现在目标虚拟屏上")
			}
		}
	}
	e.setWorkPhase(work, phase+".project")
	projection, err := e.projector.Build(&cap)
	if err != nil {
		return nil, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	fullTree := &protocol.Tree{
		Format: "full", Scope: protocol.Scope, Root: projection.Tree.Root,
	}
	fullBytes, err := json.Marshal(fullTree)
	if err != nil || len(fullBytes) > e.cfg.MaxFullTreeBytes {
		return nil, nil, protocol.NewError(503, protocol.CodeTreeTooLarge, "完整公开树超过服务上限")
	}

	e.mu.Lock()
	currentSession := e.session
	if currentSession == nil || currentSession.State != SessionActive ||
		!e.workAuthorizedLocked(work, time.Now()) {
		e.mu.Unlock()
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "原会话已撤销")
	}
	baseline := e.baseline
	revision := currentSession.Revision
	e.mu.Unlock()

	useDelta := !forceFull && baseline != nil && baseline.Origin == session.Origin &&
		revision-baseline.Revision < 12 && now.Sub(baseline.CapturedAt) < 180*time.Second
	var delta *protocol.Delta
	if useDelta {
		diff, diffErr := e.differ.Diff(baseline.ObservationID, baseline.Tree, projection.Tree)
		if diffErr != nil {
			useDelta = false
		} else {
			encoded, encodeErr := json.Marshal(diff)
			if encodeErr == nil && len(encoded) < int(float64(len(fullBytes))*0.70) {
				delta = diff
			} else {
				useDelta = false
			}
		}
	}

	e.setWorkPhase(work, phase+".commit")
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session == nil || e.session.State != SessionActive ||
		!e.workAuthorizedLocked(work, time.Now()) {
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "原会话已撤销")
	}
	if e.session.Display.Generation != session.Display.Generation ||
		e.session.Display.ID != session.Display.ID {
		return nil, nil, protocol.NewError(409, protocol.CodeStaleScreen, "目标屏幕代次已变化")
	}
	if time.Since(cap.CapturedAt) > e.cfg.CaptureMaxAge {
		return nil, nil, protocol.NewError(409, protocol.CodeStaleScreen, "节点采集已过期")
	}
	e.session.Revision++
	observationID := e.nextObservationIDLocked()
	actionableUntil := cap.CapturedAt.Add(e.cfg.ObservationLifetime)
	if actionableUntil.After(e.session.ExpiresAt) {
		actionableUntil = e.session.ExpiresAt
	}
	if actionableUntil.Before(cap.CapturedAt) {
		actionableUntil = cap.CapturedAt
	}
	if e.current != nil && e.current.Status == ObservationReady {
		e.current.Status = ObservationRetired
		e.current.RetiredAt = time.Now()
	}
	obs := &observationState{
		ID: observationID, Origin: e.session.Origin, Revision: e.session.Revision,
		Agent: e.session.Agent, SessionEpoch: e.session.Epoch,
		DisplayID: e.session.Display.ID, DisplayGeneration: e.session.Display.Generation,
		CapturedAt: cap.CapturedAt, ActionableUntil: actionableUntil,
		Screen: e.session.Display, Foreground: cap.Foreground,
		Projection: projection, Status: ObservationReady, Full: !useDelta,
	}
	e.current = obs
	e.observations[observationID] = obs
	e.pruneObservationsLocked()
	e.touchSessionLocked(time.Now())
	public := e.observationPublicLocked(obs, fullTree, delta)
	return public, obs, nil
}

func (e *Engine) observationPublicLocked(obs *observationState, full *protocol.Tree,
	delta *protocol.Delta) *protocol.Observation {
	observation := &protocol.Observation{
		Status: "complete", ID: obs.ID, Origin: obs.Origin, Revision: obs.Revision,
		CapturedAt: formatTime(obs.CapturedAt), ActionableUntil: formatTime(obs.ActionableUntil),
		Screen: &protocol.Screen{
			Width: obs.Screen.Width, Height: obs.Screen.Height, Rotation: obs.Screen.Rotation,
		},
	}
	foreground := &protocol.Foreground{State: obs.Foreground.State}
	switch obs.Foreground.State {
	case device.ForegroundPresent:
		foreground.Package = obs.Foreground.Package
		foreground.TaskID = obs.Foreground.TaskID
	case device.ForegroundUnknown:
		foreground.Reason = obs.Foreground.Reason
		if foreground.Reason == "" {
			foreground.Reason = "foreground_unknown"
		}
	}
	observation.Foreground = foreground
	if delta != nil {
		observation.Tree = &protocol.TreeValue{Delta: delta}
	} else {
		observation.Tree = &protocol.TreeValue{Full: full}
	}
	return observation
}

func (e *Engine) pruneObservationsLocked() {
	if len(e.observations) <= e.cfg.MaxObservations {
		return
	}
	protected := map[string]bool{}
	if e.current != nil {
		protected[e.current.ID] = true
	}
	if e.baseline != nil {
		protected[e.baseline.ObservationID] = true
	}
	for id, obs := range e.observations {
		if obs.Status == ObservationClaimed {
			protected[id] = true
		}
	}
	for len(e.observations) > e.cfg.MaxObservations {
		var oldest *observationState
		for _, obs := range e.observations {
			if protected[obs.ID] {
				continue
			}
			if oldest == nil || obs.CapturedAt.Before(oldest.CapturedAt) {
				oldest = obs
			}
		}
		if oldest == nil {
			return
		}
		delete(e.observations, oldest.ID)
	}
}

// confirmBaselineLocked promotes one received full candidate to the confirmed
// baseline. Only an admitted observe(O) or a successful act claim may call it.
func (e *Engine) confirmBaselineLocked(obs *observationState) {
	if obs == nil || !obs.Full || obs.Projection == nil {
		return
	}
	e.baseline = &baselineState{
		ObservationID: obs.ID, Origin: obs.Origin, Revision: obs.Revision,
		CapturedAt: obs.CapturedAt, Tree: obs.Projection.Tree,
	}
}
