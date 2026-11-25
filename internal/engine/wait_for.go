package engine

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// probeOnFailTimeout bounds the early-exit display probe the waiting phase runs
// after a run of consecutive failed reads.
const probeOnFailTimeout = 5 * time.Second

// waitCaptureReserve bounds the whole verification acquisition of one condition
// wait. It is the fixed reserve the public time budget is derived from: the
// waiting window plus this reserve stays inside the work window, and the work
// window stays inside the synchronous HTTP ceiling. The individual step
// budgets (display probe, image read, image target re-check, hierarchy read)
// may add up to more than this reserve on a very slow device, in which case the
// acquisition fails as a bounded failure instead of overrunning the budget.
const waitCaptureReserve = 35 * time.Second

// waitFrame is one valid readable sample of a condition wait. It carries the
// complete public tree of that frame without advancing ref continuity, so a
// discarded sample can never shift the refs of a published observation.
type waitFrame struct {
	capture device.Capture
	tree    *protocol.Tree
	digest  [32]byte
}

// waitFrameSource is the closed set of origins of the frame a wait publishes.
// It stays internal: the public result expresses the same fact through the
// frozen trigger, the quality and the observation id alone.
type waitFrameSource string

const (
	// waitFrameMatched is the frame that satisfied a literal condition or the
	// stability gate during the waiting phase.
	waitFrameMatched waitFrameSource = "matched"
	// waitFrameTerminalReuse is the last successful poll frame, published
	// without another device read because it is still fresh and no later
	// attempt failed.
	waitFrameTerminalReuse waitFrameSource = "terminal_reuse"
	// waitFrameTerminalCapture is a fresh read performed by the verification
	// acquisition.
	waitFrameTerminalCapture waitFrameSource = "terminal_capture"
)

// waitRun accumulates the facts of one waiting phase. Only the three bounded
// quality facts and the frozen trigger reach the result; the frames themselves
// stay local to the call.
type waitRun struct {
	samples     int
	consecutive int
	lastDigest  [32]byte
	// lastFrame is the latest successful Capture+Preview+digest frame. It is
	// cleared by every failed polling attempt, so it exists only while the
	// most recent attempt succeeded.
	lastFrame *waitFrame
	// reuse records that the selection returned lastFrame as terminal_reuse
	// instead of a matched candidate or a fresh terminal read.
	reuse bool
	// stopReason is the real reason the waiting phase ended: condition, stable,
	// timeout or max_samples. It is fixed before the verification acquisition.
	stopReason string
}

// waitPublication is one completed verification acquisition: what was
// published, the quality fact that describes exactly that frame, and the
// single-frame conditions that hold on that same frame.
type waitPublication struct {
	public  *protocol.Observation
	quality *protocol.Quality
	matched []string
}

// WaitFor waits for a literal condition or a fixed stability gate on the current
// ordinary work flow and then delivers one new observation.
//
// The waiting phase never publishes: it only samples the target display in
// memory. The single commit point stays in publishTreeAttempt, so STOP, end, a
// client disconnect, a display-generation change or an over-age capture all
// still refuse the publication after the wait returned.
func (e *Engine) WaitFor(ctx context.Context, agent, p string,
	req *protocol.WaitForRequest) (finalResult *protocol.Result, finalErr *protocol.APIError) {
	if req == nil {
		return nil, protocol.Invalid("wait_for 请求缺失")
	}
	op, duplicate, apiErr := e.registerOperation(p, agent, "wait_for")
	if apiErr != nil {
		return nil, apiErr
	}
	if duplicate != nil {
		return duplicate, nil
	}
	defer func() { e.applyFrozenResult(op, &finalResult) }()
	e.setOperationReference(op, referenceKind(req.Reference))

	// A condition and its own negation name the same frame. The wire-level
	// equality is already refused by the decoder; the folded equality is the
	// same contradiction written differently.
	if req.HasQuery && req.HasAbsentQuery && foldText(req.Query) == foldText(req.AbsentQuery) {
		return e.finishObserveError(op, "admission",
			protocol.Invalid("query 与 absent_query 是同一条件")), nil
	}

	timeout := time.Duration(req.Timeout) * time.Second
	work, forceFull, admissionErr := e.acquireWaitWork(op, agent, req.Reference, ctx, timeout)
	if admissionErr != nil {
		return e.finishObserveError(op, "admission", admissionErr), nil
	}
	defer e.releaseWork(work)

	// T0: admission is complete and the caller's own wait window starts here.
	started := time.Now()
	e.setOperationPhase(op, "wait")
	e.setWorkPhase(work, "wait_for.poll")

	session := e.sessionForWork(work)
	if session == nil {
		return e.finishObserveError(op, "wait", stoppedWaitError()), nil
	}
	run := &waitRun{stopReason: protocol.QualityStopNotSampled}
	trigger, candidate, pollErr := e.pollWaitConditions(work, session, req, run,
		started.Add(timeout))
	if pollErr != nil {
		return e.finishObserveError(op, "wait", pollErr), nil
	}
	source := waitFrameTerminalCapture
	switch {
	case candidate == nil:
	case run.reuse:
		source = waitFrameTerminalReuse
	default:
		source = waitFrameMatched
	}

	e.setOperationPhase(op, "capture")
	e.setWorkPhase(work, "wait_for.capture")
	// Keep the display identity under which the waiting candidate was read.
	// A fresh session snapshot cannot reauthorize an old Preview after a
	// display-generation change; publication rechecks this original identity.
	if e.workRevoked(work) {
		return e.finishObserveError(op, "capture", stoppedWaitError()), nil
	}
	captureCtx, captureCancel := context.WithTimeout(work.Ctx, waitCaptureReserve)
	publication, captureErr := e.completeWaitCapture(captureCtx, work, session, candidate,
		req, run, trigger, source, forceFull)
	captureCancel()
	// T2: the elapsed fact covers admission through publication, including the
	// verification acquisition.
	wait := &protocol.WaitResult{
		Trigger:   trigger,
		ElapsedMS: time.Since(started).Milliseconds(),
	}
	result := newResult(op)
	if captureErr != nil {
		// The acquisition may still return read-only image material of its own
		// failed tree; never replace that with a synthetic shape.
		if publication != nil && publication.public != nil {
			result.Observation = publication.public
		} else {
			result.Observation = &protocol.Observation{
				Status: "failed", Missing: []string{"tree"}, Error: captureErr.Public(),
			}
		}
		e.mu.Lock()
		if e.session != nil && e.session.State == SessionEnded {
			result.IncludeSession = true
			result.Session = e.sessionViewLocked(e.session)
		}
		e.mu.Unlock()
		// The frozen trigger and the elapsed time stay visible; nothing was
		// published, so no condition can be reported as matched on a frame.
		result.Wait = wait
		e.setOperationWait(op, wait)
		result.Recovery = e.finishOperation(op, "", "failed", captureErr)
		return result, nil
	}
	result.Observation = publication.public
	if len(publication.matched) > 0 {
		wait.Matched = publication.matched
	}
	result.Wait = wait
	e.setOperationWait(op, wait)
	e.log.Event(eventlog.Debug, "wait.done",
		eventlog.Str("operation_id", op.ID),
		eventlog.Str("trigger", trigger),
		eventlog.Int("samples", run.samples),
		eventlog.Bool("stable", run.consecutive >= e.cfg.Wait.StableFrames),
		eventlog.Duration("ms", time.Since(started)))
	e.finishOperation(op, "", "finished", nil)
	return result, nil
}

// pollWaitConditions runs the waiting phase. It returns the frozen trigger and,
// only when a condition or the stability gate actually held, the frame to
// publish. A timeout or sample-guard exit may still return the last successful
// poll frame for terminal_reuse; a pure sleep, a revoked wait or a run whose
// last attempt failed returns a nil candidate, which the verification
// acquisition re-reads exactly once.
func (e *Engine) pollWaitConditions(work *workState, session *sessionState,
	req *protocol.WaitForRequest, run *waitRun, deadline time.Time) (string, *waitFrame, *protocol.APIError) {
	policy := e.cfg.Wait
	conditioned := req.HasQuery || req.HasAbsentQuery
	if !conditioned && !req.Stable {
		// Pure sleep: one terminal acquisition is the only device work.
		if !pauseInterruptible(work.Ctx, time.Until(deadline)) {
			return "", nil, stoppedWaitError()
		}
		run.stopReason = protocol.QualityStopTimeout
		return protocol.WaitTriggerTimeout, nil, nil
	}
	folded := foldText(req.Query)
	foldedAbsent := foldText(req.AbsentQuery)
	failStreak := 0
	for {
		if work.Ctx.Err() != nil || e.workRevoked(work) {
			return "", nil, stoppedWaitError()
		}
		if time.Until(deadline) <= 0 {
			run.stopReason = protocol.QualityStopTimeout
			return protocol.WaitTriggerTimeout, e.terminalReuse(run), nil
		}
		// The guard counts valid readable frames only, so a device that never
		// produces a frame is bounded by its own deadline, not by this counter.
		if run.samples >= policy.MaxSamples {
			run.stopReason = protocol.QualityStopMaxSamples
			return protocol.WaitTriggerTimeout, e.terminalReuse(run), nil
		}

		frame, frameTimeout, attempted := e.sampleWaitFrame(work, session, deadline, req.Stable)
		if frame == nil {
			// A slow (capped) read, a device error, a Preview/digest failure or
			// a display-probe failure all end the chain: a frame read before the
			// failed attempt is never published as still-fresh evidence. Running
			// out of the caller's window before a read starts is not an attempt,
			// so it must not invalidate the last success.
			if attempted {
				run.lastFrame = nil
			}
			if work.Ctx.Err() != nil {
				return "", nil, stoppedWaitError()
			}
			// Running out of the caller's own window is never a device failure.
			if time.Until(deadline) <= 0 {
				if req.Stable && attempted {
					// An attempted failure breaks the content-stability chain
					// even when the caller's deadline expires on the same return.
					run.consecutive = 0
				}
				run.stopReason = protocol.QualityStopTimeout
				return protocol.WaitTriggerTimeout, e.terminalReuse(run), nil
			}
			// A frame that hit only its own single-read cap is a transient miss:
			// it resets the stability counter but never the caller's deadline.
			run.consecutive = 0
			failStreak++
			if !frameTimeout && failStreak >= policy.ProbeOnFailStreak {
				remProbe := min(probeOnFailTimeout, time.Until(deadline))
				if remProbe <= 0 {
					run.stopReason = protocol.QualityStopTimeout
					return protocol.WaitTriggerTimeout, e.terminalReuse(run), nil
				}
				probeCtx, cancel := context.WithTimeout(work.Ctx, remProbe)
				state, probeErr := e.dev.ProbeDisplay(probeCtx, session.Display.ID)
				cancel()
				switch {
				case probeErr == nil && state.Exists:
					failStreak = 0
				case work.Ctx.Err() != nil:
					return "", nil, stoppedWaitError()
				case time.Until(deadline) <= 0:
					run.stopReason = protocol.QualityStopTimeout
					return protocol.WaitTriggerTimeout, e.terminalReuse(run), nil
				default:
					return "", nil, protocol.NewError(503, protocol.CodeDisplayUnavailable,
						"目标屏幕不可用")
				}
			}
			if !pauseInterruptible(work.Ctx, min(policy.MinDwell, time.Until(deadline))) {
				return "", nil, stoppedWaitError()
			}
			continue
		}

		failStreak = 0
		if run.consecutive > 0 && frame.digest == run.lastDigest {
			run.consecutive++
		} else {
			run.consecutive = 1
		}
		run.samples++
		run.lastDigest = frame.digest
		// Only the latest successful attempt may be reused, so the candidate is
		// replaced on every readable frame and cleared on every failure above.
		run.lastFrame = frame

		readable := hasReadableContent(frame.tree)
		queryHit := req.HasQuery && readable && treeMatchesFolded(frame.tree, folded)
		absentHit := req.HasAbsentQuery && readable && !treeMatchesFolded(frame.tree, foldedAbsent)
		stableHit := run.consecutive >= policy.StableFrames
		switch {
		case req.Stable && conditioned:
			// The gate: the condition must still hold on a frame that also
			// completes the fixed number of consecutive equal frames.
			if !conditionHit(queryHit, absentHit) || !stableHit {
				break
			}
			return waitConditionTrigger(queryHit, absentHit), frame, nil
		case req.Stable:
			if stableHit {
				run.stopReason = protocol.QualityStopStable
				return protocol.WaitTriggerStable, frame, nil
			}
		default:
			if conditionHit(queryHit, absentHit) {
				run.stopReason = protocol.QualityStopCondition
				return waitConditionTrigger(queryHit, absentHit), frame, nil
			}
		}

		// The quiet window is a minimum distance between two sample starts; a
		// read that already took longer than the window is not padded.
		gap := policy.MinDwell - time.Since(frame.capture.CapturedAt)
		if gap <= 0 {
			continue
		}
		remaining := time.Until(deadline)
		if gap >= remaining {
			if !pauseInterruptible(work.Ctx, remaining) {
				return "", nil, stoppedWaitError()
			}
			run.stopReason = protocol.QualityStopTimeout
			return protocol.WaitTriggerTimeout, e.terminalReuse(run), nil
		}
		if !pauseInterruptible(work.Ctx, gap) {
			return "", nil, stoppedWaitError()
		}
	}
}

// terminalReuse returns the frame selected for a terminal_reuse publication, or
// nil when the verification acquisition must read a fresh terminal frame.
//
// The frame qualifies only when the most recent polling attempt succeeded (a
// later failure cleared it), it is still inside the publish age at selection
// time, and the waiting phase ended by its frozen timeout or sample guard. A
// revocation, lease loss or work loss ends the poll with an error before this
// selector; the commit gate re-checks all of them, the display identity and the
// age again.
func (e *Engine) terminalReuse(run *waitRun) *waitFrame {
	if run == nil || run.lastFrame == nil {
		return nil
	}
	if time.Since(run.lastFrame.capture.CapturedAt) > e.cfg.CaptureMaxAge {
		return nil
	}
	run.reuse = true
	return run.lastFrame
}

// sampleWaitFrame reads, projects and digests exactly one poll frame under the
// lesser of the caller's remaining window and the fixed single-read cap. The
// second result reports whether the failure was that cap rather than a device
// or tree failure. The third result reports whether a device read was actually
// attempted: a window that expired before the read started is not a failed
// attempt and must not invalidate the previous successful frame.
func (e *Engine) sampleWaitFrame(work *workState, session *sessionState,
	deadline time.Time, stable bool) (*waitFrame, bool, bool) {
	frameTimeout := min(time.Until(deadline), e.cfg.Wait.FrameTimeout)
	if frameTimeout <= 0 {
		return nil, false, false
	}
	frameCtx, cancel := context.WithTimeout(work.Ctx, frameTimeout)
	defer cancel()
	cap, apiErr := e.readPollCapture(frameCtx, session)
	if apiErr != nil {
		return nil, frameCtx.Err() != nil, true
	}
	projection, projectErr := work.Projector.Preview(&cap)
	if projectErr != nil {
		return nil, false, true
	}
	digest, digestErr := waitDigestOf(&cap, projection.Tree, stable)
	if digestErr != nil {
		return nil, false, true
	}
	return &waitFrame{capture: cap, tree: projection.Tree, digest: digest}, false, true
}

// completeWaitCapture runs the verification acquisition that publishes the one
// new observation and returns the quality fact of exactly that frame.
//
// A matched or reused candidate frame is never re-read from the device: a
// second read could no longer satisfy the condition, and it would break the
// stability result the caller was promised. Only a wait that ended without a
// reusable candidate reads the terminal frame itself, exactly once. All three
// sources share projectCapture and the single publishTreeAttempt commit gate.
func (e *Engine) completeWaitCapture(ctx context.Context, work *workState,
	session *sessionState, candidate *waitFrame, req *protocol.WaitForRequest,
	run *waitRun, trigger string, source waitFrameSource, forceFull bool) (*waitPublication, *protocol.APIError) {
	const phase = "wait_for"
	publication := &waitPublication{}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	probe, probeErr := e.dev.ProbeDisplay(probeCtx, session.Display.ID)
	cancel()
	if probeErr != nil || !probe.Exists {
		coreErr := protocol.NewError(503, protocol.CodeDisplayUnavailable, "目标屏幕不可用")
		if !req.Image {
			return publication, coreErr
		}
		// No image can be read without a bound target surface.
		imageErr := protocol.NewError(503, protocol.CodeImageCapture, "目标屏幕不可用")
		publication.public = failedObservation(coreErr, imageFailure(imageErr), nil)
		return publication, coreErr
	}

	attempt := &imageAttempt{}
	if req.Image {
		var imageErr *protocol.APIError
		attempt, imageErr = e.readImageForAcquisition(ctx, work, session, phase, probe.SFID)
		if imageErr != nil {
			publication.public = failedObservation(imageErr, imageFailure(attempt.err), nil)
			return publication, imageErr
		}
	}

	var (
		tree    *treeAttempt
		coreErr *protocol.APIError
	)
	if candidate != nil {
		// The frame that satisfied the condition is published as it was read;
		// the projection is local CPU work and never touches the device again.
		tree, coreErr = e.projectCapture(work, phase, candidate.capture)
	} else {
		tree, coreErr = e.captureTree(ctx, work, session, phase)
		if tree != nil {
			absorbWaitTerminalFrame(run, tree, req.Stable)
		}
	}
	publication.quality = waitQualityOf(e.cfg.Wait, run, source)
	if tree == nil {
		e.encodeImage(attempt, session)
		if attempt.meta != nil {
			if attempt.meta.CapturedAt == "" {
				attempt.meta.CapturedAt = formatTime(attempt.source.CapturedAt)
			}
			attempt.meta.Base64 = encodeBase64(attempt.data)
			screen := screenOf(session.Display)
			publication.public = failedObservation(coreErr, attempt.meta, screen)
			return publication, coreErr
		}
		if !req.Image {
			return publication, coreErr
		}
		if attempt.err == nil {
			attempt.err = protocol.NewError(503, protocol.CodeImageEncode, "图片编码失败")
		}
		publication.public = failedObservation(coreErr, imageFailure(attempt.err), nil)
		return publication, coreErr
	}

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
		attempt.meta.Base64 = encodeBase64(attempt.data)
		image = attempt.meta
	case req.Image:
		status = "partial"
		if attempt.err == nil {
			attempt.err = protocol.NewError(503, protocol.CodeImageEncode, "图片编码失败")
		}
		image = imageFailure(attempt.err)
		e.log.Event(eventlog.Warn, "observation.image",
			eventlog.Str("operation_id", work.OperationID),
			eventlog.Str("code", attempt.err.Code), eventlog.Bool("available", attempt.read))
	}

	base := tree.capture.CapturedAt
	if attempt.meta != nil && attempt.source.CapturedAt.Before(base) {
		base = attempt.source.CapturedAt
	}
	publication.matched = waitMatched(tree.projection.Tree, req, trigger)
	public, _, publishErr := e.publishTreeAttempt(work, session, phase, tree, base,
		treeSelection{forceFull: forceFull || req.Full}, status, image, publication.quality, nil)
	e.logQuality(work.OperationID, phase, public, publication.quality)
	if publishErr != nil {
		return publication, publishErr
	}
	publication.public = public
	return publication, nil
}

// absorbWaitTerminalFrame lets the terminal frame of a timed-out wait take part
// in the same comparison the waiting phase used. A run without any valid frame
// stays exactly not_sampled: a pure sleep never sampled anything.
func absorbWaitTerminalFrame(run *waitRun, tree *treeAttempt, stable bool) {
	if run.samples == 0 {
		return
	}
	digest, err := waitDigestOf(&tree.capture, tree.projection.Tree, stable)
	if err != nil {
		run.consecutive = 0
		if stable {
			// A content digest failure is not a readable terminal sample.
			return
		}
		// Tree-digest failures keep the existing stable=false accounting so
		// its state, samples and stop_reason remain unchanged.
	} else if run.consecutive > 0 && digest == run.lastDigest {
		run.consecutive++
	} else {
		run.consecutive = 1
		run.lastDigest = digest
	}
	run.samples++
}

// waitQualityOf projects one waiting phase onto the quality of exactly the frame
// that is published. A matched candidate keeps the condition/stable comparison
// it was chosen by; a reused terminal frame reports the comparison the run
// already contains without counting the frame again; a re-read terminal frame
// has already been folded into the run by the caller.
func waitQualityOf(policy WaitPolicy, run *waitRun, source waitFrameSource) *protocol.Quality {
	if run == nil {
		return &protocol.Quality{
			State: protocol.QualityUnknown, Samples: 0,
			StopReason: protocol.QualityStopNotSampled,
		}
	}
	if source == waitFrameMatched {
		switch {
		case run.consecutive >= policy.StableFrames:
			return &protocol.Quality{
				State: protocol.QualityStable, Samples: run.samples,
				StopReason: protocol.QualityStopStable,
			}
		case run.samples >= 2:
			return &protocol.Quality{
				State: protocol.QualityUnstable, Samples: run.samples,
				StopReason: protocol.QualityStopCondition,
			}
		default:
			return &protocol.Quality{
				State: protocol.QualityUnknown, Samples: run.samples,
				StopReason: protocol.QualityStopCondition,
			}
		}
	}
	if source == waitFrameTerminalReuse {
		// The frame is already one of the counted samples; only the frozen stop
		// reason of the waiting phase describes why the run ended.
		stopReason := run.stopReason
		switch stopReason {
		case protocol.QualityStopTimeout, protocol.QualityStopMaxSamples:
		default:
			// Defensive: reuse is selected only on a timeout or a sample-guard
			// exit, and an empty reason must never escape as an enum gap.
			stopReason = protocol.QualityStopTimeout
		}
		switch {
		case run.consecutive >= policy.StableFrames:
			return &protocol.Quality{
				State: protocol.QualityStable, Samples: run.samples,
				StopReason: protocol.QualityStopStable,
			}
		case run.samples >= 2:
			return &protocol.Quality{
				State: protocol.QualityUnstable, Samples: run.samples,
				StopReason: stopReason,
			}
		default:
			return &protocol.Quality{
				State: protocol.QualityUnknown, Samples: run.samples,
				StopReason: stopReason,
			}
		}
	}
	if run.samples == 0 {
		return &protocol.Quality{
			State: protocol.QualityUnknown, Samples: 0,
			StopReason: protocol.QualityStopNotSampled,
		}
	}
	stopReason := run.stopReason
	switch stopReason {
	case protocol.QualityStopTimeout, protocol.QualityStopMaxSamples:
	default:
		// Defensive: a run without a candidate ended by its deadline or by the
		// sample guard, and an empty reason must never escape as an enum gap.
		stopReason = protocol.QualityStopTimeout
	}
	switch {
	case run.consecutive >= policy.StableFrames:
		return &protocol.Quality{
			State: protocol.QualityStable, Samples: run.samples,
			StopReason: protocol.QualityStopStable,
		}
	case run.samples >= 2:
		return &protocol.Quality{
			State: protocol.QualityUnstable, Samples: run.samples,
			StopReason: stopReason,
		}
	default:
		return &protocol.Quality{
			State: protocol.QualityUnknown, Samples: run.samples,
			StopReason: stopReason,
		}
	}
}

// waitMatched reports which single-frame conditions hold on the published frame,
// in the fixed public order. A temporal fact is never mixed in; the stability
// result is expressed by observation.quality alone.
func waitMatched(tree *protocol.Tree, req *protocol.WaitForRequest, trigger string) []string {
	var matched []string
	if tree != nil && hasReadableContent(tree) {
		if req.HasQuery && treeMatchesFolded(tree, foldText(req.Query)) {
			matched = append(matched, protocol.WaitTriggerQuery)
		}
		if req.HasAbsentQuery && !treeMatchesFolded(tree, foldText(req.AbsentQuery)) {
			matched = append(matched, protocol.WaitTriggerAbsentQuery)
		}
	}
	if trigger == protocol.WaitTriggerTimeout {
		matched = append(matched, protocol.WaitTriggerTimeout)
	}
	return matched
}

// conditionHit reports whether one of the two literal conditions holds. The two
// are independent observations, not complements of each other.
func conditionHit(queryHit, absentHit bool) bool { return queryHit || absentHit }

// waitConditionTrigger resolves the fixed priority of the two conditions.
func waitConditionTrigger(queryHit, absentHit bool) string {
	if queryHit {
		return protocol.WaitTriggerQuery
	}
	if absentHit {
		return protocol.WaitTriggerAbsentQuery
	}
	return protocol.WaitTriggerTimeout
}

// hasReadableContent is the minimum reading evidence of one frame: at least one
// published content node besides the synthetic root. A frame that only carries
// the empty root of an activity transition is not a finished page, so an
// absent-condition wait never fires on it.
func hasReadableContent(tree *protocol.Tree) bool {
	if tree == nil {
		return false
	}
	return hasContentNode(tree.Root)
}

func hasContentNode(node *protocol.Node) bool {
	if node == nil {
		return false
	}
	for _, child := range node.Children {
		if child == nil {
			continue
		}
		// Context nodes exist for reading ancestry only: no geometry, no
		// capability and no readable content of their own.
		if !child.Context {
			return true
		}
		if hasContentNode(child) {
			return true
		}
	}
	return false
}

// treeMatchesFolded is one literal casefolded substring search over the published
// labels, values, hints and resources of a whole public tree.
func treeMatchesFolded(tree *protocol.Tree, folded string) bool {
	if tree == nil {
		return false
	}
	return nodeMatchesFolded(tree.Root, folded)
}

func nodeMatchesFolded(node *protocol.Node, folded string) bool {
	if node == nil || folded == "" {
		return false
	}
	if nodeMatchesQuery(node, folded) {
		return true
	}
	for _, child := range node.Children {
		if nodeMatchesFolded(child, folded) {
			return true
		}
	}
	return false
}

// encodeBase64 is the transport carrier of one already-encoded image.
func encodeBase64(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

// stoppedWaitError is the controlled revocation fact of a condition wait. It is
// the same code every other work tool reports and never the read-only query
// cancellation vocabulary.
func stoppedWaitError() *protocol.APIError {
	return protocol.NewError(409, protocol.CodeStopped, "等待已撤销")
}
