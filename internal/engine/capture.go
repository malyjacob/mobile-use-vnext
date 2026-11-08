package engine

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/imaging"
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

// imageAttempt is the single source read and the single output encode of one
// acquisition. It is local to the call: neither the source bytes nor the
// encoded bytes are retained in the observation history.
type imageAttempt struct {
	read   bool
	source device.ImageSource
	meta   *protocol.ImageMeta
	data   []byte
	err    *protocol.APIError
}

// treeAttempt is one complete public tree candidate plus its source capture.
type treeAttempt struct {
	capture    device.Capture
	projection *Projection
	full       *protocol.Tree
	fullBytes  []byte
}

// captureAndPublish performs one complete read-only acquisition and, if all
// commit gates still hold, publishes exactly one new observation. The capture
// and tree work happen outside the state lock.
//
// wantImage asks for one additional read of the same target surface. The image
// and the tree share the single commit below: the image is never published on
// its own, and an image failure never aborts a valid tree. A failed tree leaves
// the image as read-only material without an observation identity.
func (e *Engine) captureAndPublish(ctx context.Context, work *workState, session *sessionState,
	forceFull bool, wantImage bool, phase string) (*protocol.Observation, *observationState, *protocol.APIError) {
	e.setWorkPhase(work, phase+".capture")
	snapshot := e.sessionForWork(work)
	if session == nil || snapshot == nil || snapshot.Epoch != session.Epoch {
		return nil, nil, protocol.NewError(409, protocol.CodeStopped, "原会话已撤销")
	}
	session = snapshot
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	probe, probeErr := e.dev.ProbeDisplay(probeCtx, session.Display.ID)
	cancel()
	if probeErr != nil || !probe.Exists {
		coreErr := protocol.NewError(503, protocol.CodeDisplayUnavailable, "目标屏幕不可用")
		if !wantImage {
			return nil, nil, coreErr
		}
		// No image can be read without a bound target surface.
		imageErr := protocol.NewError(503, protocol.CodeImageCapture, "目标屏幕不可用")
		return failedObservation(coreErr, imageFailure(imageErr), nil), nil, coreErr
	}

	attempt := &imageAttempt{}
	if wantImage {
		attempt = e.readImage(ctx, work, phase, probe.SFID)
		// The second probe keeps the image and the tree bound to the same
		// target surface. A changed identity discards the image material.
		if attempt.read && !e.verifyImageTarget(ctx, session, probe.SFID) {
			coreErr := protocol.NewError(503, protocol.CodeDisplayUnavailable, "目标屏幕不可用")
			attempt.meta, attempt.data, attempt.read = nil, nil, false
			attempt.err = protocol.NewError(503, protocol.CodeImageCapture, "截图期间目标屏幕身份已变化")
			return failedObservation(coreErr, imageFailure(attempt.err), nil), nil, coreErr
		}
	}

	tree, coreErr := e.captureTree(ctx, work, session, phase)
	if tree == nil {
		// The tree attempt failed. An already read image is still encoded once
		// and may be returned as read-only material of this acquisition.
		e.encodeImage(attempt, session)
		if attempt.meta != nil {
			if attempt.meta.CapturedAt == "" {
				attempt.meta.CapturedAt = formatTime(attempt.source.CapturedAt)
			}
			attempt.meta.Base64 = base64.StdEncoding.EncodeToString(attempt.data)
			screen := &protocol.Screen{
				Width: session.Display.Width, Height: session.Display.Height,
				Rotation: session.Display.Rotation,
			}
			return failedObservation(coreErr, attempt.meta, screen), nil, coreErr
		}
		if !wantImage {
			return nil, nil, coreErr
		}
		if attempt.err == nil {
			attempt.err = protocol.NewError(503, protocol.CodeImageEncode, "图片编码失败")
		}
		return failedObservation(coreErr, imageFailure(attempt.err), nil), nil, coreErr
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
		revision-baseline.Revision < 12 && time.Since(baseline.CapturedAt) < 180*time.Second
	var delta *protocol.Delta
	if useDelta {
		diff, diffErr := e.differ.Diff(baseline.ObservationID, baseline.Tree, tree.projection.Tree)
		if diffErr != nil {
			useDelta = false
		} else {
			encoded, encodeErr := json.Marshal(diff)
			if encodeErr == nil && len(encoded) < int(float64(len(tree.fullBytes))*0.70) {
				delta = diff
			} else {
				useDelta = false
			}
		}
	}

	// The image is encoded exactly once, after the tree attempt, whatever its
	// outcome. Only an image read from the verified target surface and close in
	// time to the tree may be attached.
	e.encodeImage(attempt, session)
	if attempt.meta != nil {
		if staleErr := e.staleImage(attempt.source, tree.capture.CapturedAt); staleErr != nil {
			attempt.meta, attempt.data = nil, nil
			attempt.err = staleErr
		}
	}
	status := "complete"
	var image *protocol.ImageMeta
	switch {
	case attempt.meta != nil:
		attempt.meta.ReadOnly = false
		attempt.meta.Screen = nil
		attempt.meta.Error = nil
		if attempt.meta.CapturedAt == "" {
			attempt.meta.CapturedAt = formatTime(attempt.source.CapturedAt)
		}
		attempt.meta.Base64 = base64.StdEncoding.EncodeToString(attempt.data)
		image = attempt.meta
	case wantImage:
		// The tree is still a valid observation; only the requested image is
		// missing, so the observation is published as partial.
		status = "partial"
		if attempt.err == nil {
			attempt.err = protocol.NewError(503, protocol.CodeImageEncode, "图片编码失败")
		}
		image = imageFailure(attempt.err)
		e.log.Event(eventlog.Warn, "observation.image",
			eventlog.Str("code", attempt.err.Code), eventlog.Bool("available", attempt.read))
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
	if time.Since(tree.capture.CapturedAt) > e.cfg.CaptureMaxAge {
		return nil, nil, protocol.NewError(409, protocol.CodeStaleScreen, "节点采集已过期")
	}
	e.session.Revision++
	observationID := e.nextObservationIDLocked()
	// The observation is actionable only while both attached materials are:
	// the earlier of the tree read start and the image read start, never before
	// that base and never past the session lease.
	base := tree.capture.CapturedAt
	if attempt.meta != nil && attempt.source.CapturedAt.Before(base) {
		base = attempt.source.CapturedAt
	}
	actionableUntil := base.Add(e.cfg.ObservationLifetime)
	if actionableUntil.After(e.session.ExpiresAt) {
		actionableUntil = e.session.ExpiresAt
	}
	if actionableUntil.Before(base) {
		actionableUntil = base
	}
	if e.current != nil && e.current.Status == ObservationReady {
		e.current.Status = ObservationRetired
		e.current.RetiredAt = time.Now()
	}
	obs := &observationState{
		ID: observationID, Origin: e.session.Origin, Revision: e.session.Revision,
		Agent: e.session.Agent, SessionEpoch: e.session.Epoch,
		DisplayID: e.session.Display.ID, DisplayGeneration: e.session.Display.Generation,
		CapturedAt: tree.capture.CapturedAt, ActionableUntil: actionableUntil,
		Screen: e.session.Display, Foreground: tree.capture.Foreground,
		Projection: tree.projection, Status: ObservationReady, Full: !useDelta,
	}
	e.current = obs
	e.observations[observationID] = obs
	e.pruneObservationsLocked()
	e.touchSessionLocked(time.Now())
	public := e.observationPublicLocked(obs, status, tree.full, delta, image)
	return public, obs, nil
}

// captureTree reads the hierarchy and builds the complete public tree
// candidate. It performs no commit and no image work.
func (e *Engine) captureTree(ctx context.Context, work *workState, session *sessionState,
	phase string) (*treeAttempt, *protocol.APIError) {
	e.setWorkPhase(work, phase+".tree")
	cap, err := e.dev.Capture(ctx, session.Display)
	if err != nil {
		return nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "未能取得节点树")
	}
	now := time.Now()
	if cap.CapturedAt.IsZero() {
		cap.CapturedAt = now
	}
	cap.Display = session.Display
	if now.Sub(cap.CapturedAt) > e.cfg.CaptureMaxAge {
		return nil, protocol.NewError(409, protocol.CodeStaleScreen, "节点采集已过期")
	}
	if session.Display.ID != 0 {
		for _, node := range cap.Nodes {
			if node.Package == e.cfg.ControlPackage && node.Visible {
				return nil, protocol.NewError(409, "control_ui_on_virtual_display",
					"控制界面出现在目标虚拟屏上")
			}
		}
	}
	e.setWorkPhase(work, phase+".project")
	projection, err := e.projector.Build(&cap)
	if err != nil {
		return nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	fullTree := &protocol.Tree{
		Format: "full", Scope: protocol.Scope, Root: projection.Tree.Root,
	}
	fullBytes, err := json.Marshal(fullTree)
	if err != nil || len(fullBytes) > e.cfg.MaxFullTreeBytes {
		return nil, protocol.NewError(503, protocol.CodeTreeTooLarge, "完整公开树超过服务上限")
	}
	return &treeAttempt{capture: cap, projection: projection, full: fullTree, fullBytes: fullBytes}, nil
}

// readImage performs the bounded source read for one acquisition using the
// runtime display surface id, never a value carried by the session snapshot. A
// failure is recorded on the attempt and never aborts the core tree flow.
func (e *Engine) readImage(ctx context.Context, work *workState, phase string,
	sfid uint64) *imageAttempt {
	attempt := &imageAttempt{}
	if e.imaging == nil {
		attempt.err = protocol.NewError(503, protocol.CodeImageEncode, "图片编码不可用")
		return attempt
	}
	if sfid == 0 {
		attempt.err = protocol.NewError(503, protocol.CodeImageCapture, "目标屏幕没有可读的图形标识")
		return attempt
	}
	e.setWorkPhase(work, phase+".image")
	imageCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	source, err := e.dev.CaptureImage(imageCtx, sfid)
	cancel()
	if err != nil || len(source.Data) == 0 {
		attempt.err = protocol.NewError(503, protocol.CodeImageCapture, "未能读取目标屏幕截图")
		return attempt
	}
	attempt.read = true
	attempt.source = source
	return attempt
}

// verifyImageTarget re-reads the display identity after the image read so the
// image and the tree belong to the same target surface.
func (e *Engine) verifyImageTarget(ctx context.Context, session *sessionState, sfid uint64) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := e.dev.ProbeDisplay(probeCtx, session.Display.ID)
	return err == nil && state.Exists && state.SFID == sfid
}

// encodeImage performs the single output encode of one attempt. It runs after
// the tree attempt, whether that attempt succeeded or failed.
func (e *Engine) encodeImage(attempt *imageAttempt, session *sessionState) {
	if !attempt.read || attempt.err != nil {
		return
	}
	meta, data, err := e.imaging.Encode(attempt.source, session.Display,
		e.cfg.ImageMaxLongSide, e.cfg.ImageQuality, e.cfg.MaxImageBytes)
	if err != nil {
		attempt.err = imageEncodeError(err)
		return
	}
	if meta == nil || len(data) == 0 {
		attempt.err = protocol.NewError(503, protocol.CodeImageEncode, "图片编码结果为空")
		return
	}
	attempt.meta = meta
	attempt.data = data
}

// staleImage rejects an image whose source read start is further from the tree
// read start than the fixed span budget. Both reads belong to one acquisition,
// so the image read normally starts slightly before the tree read.
func (e *Engine) staleImage(source device.ImageSource, treeCapturedAt time.Time) *protocol.APIError {
	span := treeCapturedAt.Sub(source.CapturedAt)
	if span < 0 {
		span = -span
	}
	if span > e.cfg.ImageMaxSpan {
		return protocol.NewError(503, protocol.CodeImageStale, "截图与节点树的时间跨度过大")
	}
	return nil
}

// imageEncodeError maps one encoder failure to the public image code. The
// encoder is a pure algorithm from internal/imaging; the engine matches its
// sentinel errors instead of inspecting messages.
func imageEncodeError(err error) *protocol.APIError {
	switch {
	case errors.Is(err, imaging.ErrTooLarge):
		return protocol.NewError(503, protocol.CodeImageTooLarge, "图片超过服务上限")
	case errors.Is(err, imaging.ErrGeometry):
		return protocol.NewError(503, protocol.CodeImageCapture, "截图与目标屏幕不匹配")
	case errors.Is(err, imaging.ErrInvalidSource):
		return protocol.NewError(503, protocol.CodeImageCapture, "截图来源无效")
	default:
		return protocol.NewError(503, protocol.CodeImageEncode, "图片编码失败")
	}
}

// imageFailure is the placeholder image slot of a requested image that could
// not be attached. It carries only the controlled error, never dimensions,
// capture time or bytes.
func imageFailure(apiErr *protocol.APIError) *protocol.ImageMeta {
	return &protocol.ImageMeta{Error: apiErr.Public()}
}

// failedObservation is the public shape of an acquisition whose tree did not
// reach the commit gate. Read-only image material keeps the acquisition
// partial but never assigns an observation identity.
func failedObservation(coreErr *protocol.APIError, image *protocol.ImageMeta,
	screen *protocol.Screen) *protocol.Observation {
	observation := &protocol.Observation{
		Status: "failed", Missing: []string{"tree"}, Error: coreErr.Public(),
	}
	if image == nil {
		return observation
	}
	if image.Error != nil {
		observation.Image = image
		return observation
	}
	observation.Status = "partial"
	observation.Screen = screen
	image.ReadOnly = true
	image.Screen = screen
	observation.Image = image
	return observation
}

// observationPublicLocked is the single committed observation projection. The
// status and an optional image are decided by the acquisition; everything else
// keeps its fixed public meaning.
func (e *Engine) observationPublicLocked(obs *observationState, status string, full *protocol.Tree,
	delta *protocol.Delta, image *protocol.ImageMeta) *protocol.Observation {
	observation := &protocol.Observation{
		Status: status, ID: obs.ID, Origin: obs.Origin, Revision: obs.Revision,
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
	if image != nil {
		observation.Image = image
		if image.Error != nil {
			observation.Missing = []string{"image"}
		}
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
