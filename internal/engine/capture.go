package engine

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"sort"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/imaging"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
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
// The compact read tree and its canonical bytes are produced once, outside the
// state lock, and never recomputed at commit time.
type treeAttempt struct {
	capture      device.Capture
	projection   *Projection
	compact      *protocol.ReadTree
	compactBytes []byte
	cost         int
	// commit publishes the candidate's continuity state. It runs at the single
	// commit point; a rejected candidate is simply dropped.
	commit func()
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

	// A phase with a settle policy waits for a converged tree before anything
	// else is read. The settle loop must not sit between the image read and the
	// tree read: it would stretch the fixed image/tree span and turn a valid
	// acquisition into image_stale.
	policy := e.settlePolicy(phase)
	var (
		attempt = &imageAttempt{}
		run     *settleRun
		tree    *treeAttempt
		coreErr *protocol.APIError
	)
	if policy.Enabled {
		run, coreErr = e.settleCapture(ctx, work, session, phase, policy)
		if coreErr != nil && coreErr.Code == protocol.CodeStopped {
			// Revoked work never publishes an observation.
			return nil, nil, coreErr
		}
	}
	if wantImage {
		var imageErr *protocol.APIError
		attempt, imageErr = e.readImageForAcquisition(ctx, work, session, phase, probe.SFID)
		if imageErr != nil {
			return failedObservation(imageErr, imageFailure(attempt.err), nil), nil, imageErr
		}
	}
	if coreErr == nil {
		if run != nil {
			tree, coreErr = e.captureSettledTree(ctx, work, session, phase, policy, run, wantImage)
		} else {
			tree, coreErr = e.captureTree(ctx, work, session, phase)
		}
	}
	if tree == nil {
		// The tree attempt failed. An already read image is still encoded once
		// and may be returned as read-only material of this acquisition.
		e.encodeImage(attempt, session)
		if attempt.meta != nil {
			if attempt.meta.CapturedAt == "" {
				attempt.meta.CapturedAt = formatTime(attempt.source.CapturedAt)
			}
			attempt.meta.Base64 = base64.StdEncoding.EncodeToString(attempt.data)
			screen := screenOf(session.Display)
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
			eventlog.Str("operation_id", work.OperationID),
			eventlog.Str("code", attempt.err.Code), eventlog.Bool("available", attempt.read))
	}

	// The observation is actionable only while both attached materials are: the
	// earlier of the tree read start and the image read start.
	base := tree.capture.CapturedAt
	if attempt.meta != nil && attempt.source.CapturedAt.Before(base) {
		base = attempt.source.CapturedAt
	}
	// The quality is the projection of the run whose frame is published; a
	// phase without a settle policy never sampled and says exactly that.
	quality := qualityOf(policy, run)
	public, obs, publishErr := e.publishTreeAttempt(work, session, phase, tree, base,
		true, forceFull, status, image, quality)
	// The quality fact is recorded after the commit outcome is known and outside
	// the state lock; a failed commit never invents an observation id.
	e.logQuality(work.OperationID, phase, public, quality)
	return public, obs, publishErr
}

// Baseline pool policy. A delta may only be expressed against a confirmed full
// observation close enough in revision and time, so the pool keeps at most
// maxBaselines entries and prunes everything outside the fixed window. The
// historical page-return threshold is stricter than the primary one: a merely
// similar different page must not force a delta against a baseline the caller
// did not just read.
const (
	maxBaselineRevisionSpan = 4
	maxBaselines            = 4
	maxBaselineAge          = 180 * time.Second
)

// publishTreeAttempt commits exactly one already-read frame as the session's
// current observation and returns its public projection. Multi-baseline delta
// selection and every commit gate live here, so the ordinary acquisition and
// the frame a rejected act delivers share one funnel: a candidate never reaches
// the public state unless the session, the work ownership, the display
// generation and the capture age all still hold at this moment. quality is the
// fixed quality fact of the attempt; the object is immutable per call and
// shared with the public projection.
//
// Delta selection has two read-only steps. Step 1 diffs against the baseline
// the projector's continuity state is aligned with (or the newest confirmed
// baseline) and wins outright below 70% of the full encoding. Only when
// allowPoolFallback is set and Step 1 produced no delta does Step 2 probe the
// remaining historical baselines: each probe projects the same capture against
// that baseline without publishing continuity state, and only a delta below 50%
// of the candidate's full encoding may win. Otherwise the default candidate is
// published as a full tree.
func (e *Engine) publishTreeAttempt(work *workState, session *sessionState, phase string,
	tree *treeAttempt, base time.Time, allowPoolFallback bool, forceFull bool,
	status string, image *protocol.ImageMeta, quality *protocol.Quality) (*protocol.Observation, *observationState, *protocol.APIError) {
	stopped := protocol.NewError(409, protocol.CodeStopped, "原会话已撤销")
	if tree == nil || tree.projection == nil || session == nil {
		return nil, nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	e.mu.Lock()
	currentSession := e.session
	if currentSession == nil || currentSession.State != SessionActive ||
		!e.workAuthorizedLocked(work, time.Now()) {
		e.mu.Unlock()
		return nil, nil, stopped
	}
	nextRevision := currentSession.Revision + 1
	// Snapshot the baselines usable by the revision being published. This is a
	// non-mutating filter: a rejected acquisition must not prune a baseline
	// that is still the base of the current observation, so the pool is only
	// rewritten after every commit gate has passed.
	pool := e.baselinePoolForRevisionLocked(nextRevision, time.Now())
	activeBaselineID := e.activeBaselineID
	e.mu.Unlock()

	// The default candidate was projected against the projector's current
	// continuity state. A historical baseline may replace it, so every
	// committed field is held separately and a discarded candidate's commit
	// function is never called.
	chosenCompact, chosenProjection := tree.compact, tree.projection
	chosenCost, chosenCommit := tree.cost, tree.commit
	var delta *protocol.ReadDelta
	// poolSource is the closed baseline_pool enum of the published attempt:
	// primary (active or newest baseline), historical (page-return probe) or
	// full (no accepted delta).
	poolSource := "full"
	if !forceFull && len(pool) > 0 {
		// Step 1 (primary path): the active baseline or the newest confirmed
		// one. A successful primary delta never enters Step 2, so a temporary
		// banner that disappears and reappears cannot resurrect its old ref.
		primary := baselineWithID(pool, activeBaselineID)
		if primary == nil {
			primary = pool[0]
		}
		delta, _ = e.boundedBaselineDelta(primary, tree.compact, tree.compactBytes, 7)
		if delta != nil {
			poolSource = "primary"
		}
		if delta == nil && allowPoolFallback {
			// Step 2 (historical page-return probe): try the remaining
			// baselines newest first and keep the smallest delta that passes
			// the stricter 50% rule. A candidate that cannot be published is
			// skipped; the fallback full tree stays available.
			bestBytes := 0
			for _, candidate := range pool {
				if candidate == primary {
					continue
				}
				if e.workRevoked(work) || work.Ctx.Err() != nil {
					return nil, nil, stopped
				}
				projection, commit, err := work.Projector.CandidateAgainst(candidate.Projection, &tree.capture)
				if err != nil || projection == nil || projection.Tree == nil {
					continue
				}
				compact, compactBytes, cost, ok := e.historicalCandidate(projection)
				if !ok {
					continue
				}
				candidateDelta, deltaBytes := e.boundedBaselineDelta(candidate, compact, compactBytes, 5)
				if candidateDelta == nil || (bestBytes != 0 && deltaBytes >= bestBytes) {
					continue
				}
				bestBytes = deltaBytes
				delta = candidateDelta
				poolSource = "historical"
				chosenCompact, chosenProjection = compact, projection
				chosenCost, chosenCommit = cost, commit
			}
		}
	}

	e.setWorkPhase(work, phase+".commit")
	e.mu.Lock()
	now := time.Now()
	if e.session == nil || e.session.State != SessionActive ||
		!e.workAuthorizedLocked(work, now) {
		e.mu.Unlock()
		return nil, nil, stopped
	}
	// Bind publication to the captured material as well as the caller's session
	// snapshot. Some callers hold the live session pointer, which a display
	// report can update while projection is outside the lock.
	if e.session.Display.Generation != session.Display.Generation ||
		e.session.Display.ID != session.Display.ID ||
		e.session.Display.Generation != tree.capture.Display.Generation ||
		e.session.Display.ID != tree.capture.Display.ID {
		e.mu.Unlock()
		return nil, nil, protocol.NewError(409, protocol.CodeStaleScreen, "目标屏幕代次已变化")
	}
	if now.Sub(tree.capture.CapturedAt) > e.cfg.CaptureMaxAge {
		e.mu.Unlock()
		return nil, nil, protocol.NewError(409, protocol.CodeStaleScreen, "节点采集已过期")
	}
	// Use one commit-time pool for choosing, protecting and retaining bases.
	// An entry that expired while the probe ran cannot back this observation.
	commitPool := e.baselinePoolForRevisionLocked(e.session.Revision+1, now)
	fallbackFull := func() {
		delta = nil
		poolSource = "full"
		chosenCompact, chosenProjection = tree.compact, tree.projection
		chosenCost, chosenCommit = tree.cost, tree.commit
	}
	if delta != nil && baselineWithID(commitPool, delta.Base) == nil {
		fallbackFull()
	}
	// Decide retention before changing the pool or deleting any observation.
	victims, fits := e.planRetentionLocked(chosenCost, commitPool)
	if !fits && poolSource == "historical" && tree.cost < chosenCost {
		fallbackFull()
		victims, fits = e.planRetentionLocked(chosenCost, commitPool)
	}
	if !fits {
		e.mu.Unlock()
		return nil, nil, budgetError("retained_snapshots", e.cfg.MaxRetainedSnapshotCostBytes)
	}
	e.baselines = commitPool
	e.session.Revision++
	observationID := e.nextObservationIDLocked()
	// The observation keeps the actionable window of the caller-provided base,
	// which is the earliest material read of one acquisition, and never extends
	// past the session lease.
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
		Quality: quality,
		Screen:  e.session.Display, Foreground: tree.capture.Foreground,
		Projection: chosenProjection, Compact: chosenCompact, Cost: chosenCost,
		Status: ObservationReady, Full: delta == nil,
	}
	for _, victim := range victims {
		delete(e.observations, victim.ID)
	}
	// The candidate's continuity state becomes visible only here: every commit
	// gate and the retention dry-run already passed.
	if chosenCommit != nil {
		chosenCommit()
	}
	// Only a delta keeps an active baseline. A published full tree clears it
	// until the next confirmed full observation; the pool itself stays.
	if delta != nil {
		e.activeBaselineID = delta.Base
	} else {
		e.activeBaselineID = ""
	}
	e.current = obs
	e.observations[observationID] = obs
	e.pruneObservationsLocked()
	ttl := e.cfg.SessionTTL
	if work.Kind == WorkStart {
		ttl = e.cfg.StartSessionTTL
	}
	e.extendSessionLocked(time.Now(), ttl)
	public := e.observationPublicLocked(obs, status, chosenCompact, delta, image, quality)
	e.mu.Unlock()
	// The baseline path is a diagnostic fact of a certain commit; it is
	// recorded outside the state lock.
	e.logBaseline(work.OperationID, poolSource, delta != nil)
	return public, obs, nil
}

// logBaseline records which delta path produced one published observation. The
// pool value is the closed enum primary/historical/full and format is
// delta/full; a ref, a base identity, tree content or text is never logged.
func (e *Engine) logBaseline(operationID, pool string, delta bool) {
	format := "full"
	if delta {
		format = "delta"
	}
	e.log.Event(eventlog.Debug, "observation.baseline",
		eventlog.Str("operation_id", operationID),
		eventlog.Str("baseline_pool", pool),
		eventlog.Str("format", format))
}

// baselineWithID returns the pool entry with the given observation identity, or
// nil when the identity is empty or no longer pooled. The pool is ordered
// newest first, so a caller that does not find the active identity falls back
// to the newest confirmed baseline.
func baselineWithID(pool []*baselineState, id string) *baselineState {
	if id == "" {
		return nil
	}
	for _, b := range pool {
		if b != nil && b.ObservationID == id {
			return b
		}
	}
	return nil
}

// hasBaselineLocked reports whether the pool still holds the given baseline.
func (e *Engine) hasBaselineLocked(id string) bool {
	return baselineWithID(e.baselines, id) != nil
}

// sortBaselinesLocked keeps the pool ordered by Revision descending. Two
// confirmed observations never share a revision, so the order is total.
func (e *Engine) sortBaselinesLocked() {
	sort.SliceStable(e.baselines, func(left, right int) bool {
		return e.baselines[left].Revision > e.baselines[right].Revision
	})
}

// baselineEligibleLocked reports whether one pool entry may back a delta of
// the observation at nextRevision: the same session origin and target display
// identity (a display generation change invalidates ref continuity), a revision
// span inside maxBaselineRevisionSpan and an age younger than maxBaselineAge.
func (e *Engine) baselineEligibleLocked(b *baselineState, nextRevision int, now time.Time) bool {
	if b == nil || b.Tree == nil || e.session == nil {
		return false
	}
	return b.Origin == e.session.Origin &&
		b.DisplayID == e.session.Display.ID &&
		b.DisplayGeneration == e.session.Display.Generation &&
		nextRevision-b.Revision <= maxBaselineRevisionSpan &&
		now.Sub(b.CapturedAt) < maxBaselineAge
}

// baselinePoolForRevisionLocked returns the confirmed baselines usable by the
// observation at nextRevision, in pool order, without changing the pool. A
// rejected acquisition therefore never prunes the base of the current
// observation; only a certain commit rewrites the pool.
func (e *Engine) baselinePoolForRevisionLocked(nextRevision int, now time.Time) []*baselineState {
	if len(e.baselines) == 0 {
		return nil
	}
	pool := make([]*baselineState, 0, len(e.baselines))
	for _, b := range e.baselines {
		if e.baselineEligibleLocked(b, nextRevision, now) {
			pool = append(pool, b)
		}
	}
	return pool
}

// pruneBaselinesLocked drops every pool entry that can no longer serve the
// observation at nextRevision and keeps the remaining entries newest first,
// bounded to maxBaselines. The active identity is cleared when its entry is
// pruned. The survivor slice is rebuilt so a dropped entry never stays
// reachable through the pool's backing array.
func (e *Engine) pruneBaselinesLocked(nextRevision int) {
	e.baselines = e.baselinePoolForRevisionLocked(nextRevision, time.Now())
	if e.activeBaselineID != "" && !e.hasBaselineLocked(e.activeBaselineID) {
		e.activeBaselineID = ""
	}
}

// truncateBaselinesLocked bounds the pool to maxBaselines and releases the
// dropped tail references, so an out-of-window snapshot never stays reachable
// through the pool.
func (e *Engine) truncateBaselinesLocked() {
	if len(e.baselines) <= maxBaselines {
		return
	}
	for i := maxBaselines; i < len(e.baselines); i++ {
		e.baselines[i] = nil
	}
	e.baselines = e.baselines[:maxBaselines]
}

// boundedBaselineDelta computes one delta against a confirmed baseline and
// accepts it only when it is strictly smaller than ratioTenths/10 of the
// candidate's compact full encoding. An equal-size delta is not smaller and
// therefore falls back to the full tree.
func (e *Engine) boundedBaselineDelta(b *baselineState, compact *protocol.ReadTree,
	compactBytes []byte, ratioTenths int) (*protocol.ReadDelta, int) {
	if b == nil || b.Tree == nil || compact == nil {
		return nil, 0
	}
	diff, err := e.differ.Diff(b.ObservationID, b.Tree, compact)
	if err != nil {
		return nil, 0
	}
	encoded, err := json.Marshal(diff)
	if err != nil || 10*len(encoded) >= ratioTenths*len(compactBytes) {
		return nil, 0
	}
	return diff, len(encoded)
}

// historicalCandidate rebuilds the public compact representation of a
// historical-baseline projection through the same public and internal budget
// gate as the default candidate. A candidate that cannot be published is
// skipped safely instead of failing the acquisition.
func (e *Engine) historicalCandidate(projection *Projection) (*protocol.ReadTree, []byte, int, bool) {
	if projection == nil || projection.Tree == nil || projection.Capture == nil {
		return nil, nil, 0, false
	}
	compact, compactBytes, cost, apiErr := e.publicCandidate(*projection.Capture, projection)
	if apiErr != nil {
		return nil, nil, 0, false
	}
	return compact, compactBytes, cost, true
}

// budgetError builds the controlled tree budget failure. The details name the
// budget and its numeric limit; no tree content is ever attached.
func budgetError(budget string, limit int) *protocol.APIError {
	message := "完整公开树超过服务上限"
	switch budget {
	case "internal_snapshot":
		message = "节点快照超过服务上限"
	case "retained_snapshots":
		message = "留存节点快照超过服务上限"
	}
	return protocol.NewError(503, protocol.CodeTreeTooLarge, message).
		WithDetail("budget", budget).WithDetail("limit", limit)
}

// planRetentionLocked is a bounded dry-run over the retained observation
// metadata. It returns the oldest unprotected victims that make room for one
// new snapshot, or fits=false without changing anything. Only baselines that
// can still serve the observation being published are protected: an O that may
// be emitted as a delta base must stay readable with inspect, while an
// out-of-window entry no longer carries that guarantee. The pool itself is
// rewritten only after this dry-run passes.
func (e *Engine) planRetentionLocked(newCost int, pool []*baselineState) ([]*observationState, bool) {
	total := 0
	for _, obs := range e.observations {
		if obs != nil {
			total += obs.Cost
		}
	}
	if total+newCost <= e.cfg.MaxRetainedSnapshotCostBytes {
		return nil, true
	}
	protected := map[string]bool{}
	if e.current != nil {
		protected[e.current.ID] = true
	}
	for _, b := range pool {
		if b != nil && b.ObservationID != "" {
			protected[b.ObservationID] = true
		}
	}
	for id, obs := range e.observations {
		if obs != nil && obs.Status == ObservationClaimed {
			protected[id] = true
		}
	}
	candidates := make([]*observationState, 0, len(e.observations))
	for id, obs := range e.observations {
		if obs == nil || protected[id] {
			continue
		}
		candidates = append(candidates, obs)
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].CapturedAt.Before(candidates[right].CapturedAt)
	})
	var victims []*observationState
	for _, victim := range candidates {
		total -= victim.Cost
		victims = append(victims, victim)
		if total+newCost <= e.cfg.MaxRetainedSnapshotCostBytes {
			return victims, true
		}
	}
	return nil, false
}

// captureTree reads the hierarchy and builds the complete public tree
// candidate. It performs no commit and no image work.
func (e *Engine) captureTree(ctx context.Context, work *workState, session *sessionState,
	phase string) (*treeAttempt, *protocol.APIError) {
	cap, apiErr := e.readCapture(ctx, work, session, phase)
	if apiErr != nil {
		return nil, apiErr
	}
	return e.projectCapture(work, phase, cap)
}

// captureSettledTree turns a converged frame into the acquisition's tree.
// When an image was read after the settle loop one more read refreshes the
// tree so both materials describe nearly the same moment. The refresh is part
// of the same quality accounting: an equal refresh frame is the later frame of
// the pair and makes the run stable, a different one invalidates the earlier
// pair and continues the loop inside the remaining policy. The image refresh is
// the only sample that may take an act run past MaxSamples, and it may do so at
// most once.
func (e *Engine) captureSettledTree(ctx context.Context, work *workState, session *sessionState,
	phase string, policy SettlePolicy, run *settleRun, imageRead bool) (*treeAttempt, *protocol.APIError) {
	if imageRead {
		frame, apiErr := e.captureSample(ctx, work, session, phase)
		switch {
		case apiErr != nil && apiErr.Code == protocol.CodeStopped:
			return nil, apiErr
		case apiErr != nil:
			// The refresh failed; the settled frame remains valid evidence and
			// the image stays paired with the tree that gets published. A failed
			// read is not a sample: the run and its quality are untouched.
		case frame.digest == run.frame.digest && run.samples+1 >= policy.MinSamples:
			// The page did not move while the image was read: publish the later
			// of the two equal frames, never the earlier one after a wait, and
			// credit the pair the refresh just completed.
			run.frame = frame
			run.samples++
			run.stable = true
			run.stopReason = protocol.QualityStopStable
		default:
			// A different refresh frame (or an equal one that still cannot meet
			// MinSamples) invalidates any earlier pair. The final state comes
			// from the comparisons that follow, never from the old one.
			run.frame = frame
			run.samples++
			run.lastTook = frame.took
			run.stable = false
			if apiErr := e.settleUntilStable(ctx, work, session, phase, policy, run); apiErr != nil {
				return nil, apiErr
			}
		}
	}
	e.logSettle(work.OperationID, phase, run)
	return e.projectCapture(work, phase, run.frame.capture)
}

// readCapture reads one target hierarchy and applies the fixed per-capture
// validation that every acquisition path shares. It projects and commits
// nothing.
func (e *Engine) readCapture(ctx context.Context, work *workState, session *sessionState,
	phase string) (device.Capture, *protocol.APIError) {
	e.setWorkPhase(work, phase+".tree")
	cap, err := e.dev.Capture(ctx, session.Display)
	if err != nil {
		return device.Capture{}, protocol.NewError(503, protocol.CodeHierarchyFailed, "未能取得节点树")
	}
	return e.captureValidated(cap, session)
}

// readPollCapture reads one frame of the condition-wait poll loop. It is a
// deliberate sibling of readCapture and applies exactly the same per-capture
// validation through captureValidated; only the phase naming differs, because a
// poll sample must not rebroadcast a new work phase on every frame.
func (e *Engine) readPollCapture(ctx context.Context, session *sessionState) (device.Capture, *protocol.APIError) {
	cap, err := e.dev.Capture(ctx, session.Display)
	if err != nil {
		return device.Capture{}, protocol.NewError(503, protocol.CodeHierarchyFailed, "未能取得节点树")
	}
	return e.captureValidated(cap, session)
}

// captureValidated is the single per-capture validation source of every
// acquisition path: capture age, target display binding and the control-UI
// exclusion. It never projects, publishes or waits.
func (e *Engine) captureValidated(cap device.Capture, session *sessionState) (device.Capture, *protocol.APIError) {
	now := time.Now()
	if cap.CapturedAt.IsZero() {
		cap.CapturedAt = now
	}
	cap.Display = session.Display
	if now.Sub(cap.CapturedAt) > e.cfg.CaptureMaxAge {
		return device.Capture{}, protocol.NewError(409, protocol.CodeStaleScreen, "节点采集已过期")
	}
	if session.Display.ID != 0 {
		for _, node := range cap.Nodes {
			if node.Package == e.cfg.ControlPackage && node.Visible {
				return device.Capture{}, protocol.NewError(409, "control_ui_on_virtual_display",
					"控制界面出现在目标虚拟屏上")
			}
		}
	}
	return cap, nil
}

// projectCapture builds the public reading tree candidate: the complete
// internal projection, its compact public representation, the canonical bytes
// of that representation and the internal accounting size. The public budget
// is checked on the compact encoding; the internal budget is checked on the
// complete snapshot. Neither check may delete or crop content.
func (e *Engine) projectCapture(work *workState, phase string, cap device.Capture) (*treeAttempt, *protocol.APIError) {
	e.setWorkPhase(work, phase+".project")
	projection, commit, err := work.Projector.Candidate(&cap)
	if err != nil {
		return nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	compact, compactBytes, cost, apiErr := e.publicCandidate(cap, projection)
	if apiErr != nil {
		return nil, apiErr
	}
	return &treeAttempt{
		capture: cap, projection: projection, compact: compact,
		compactBytes: compactBytes, cost: cost, commit: commit,
	}, nil
}

// publicCandidate validates one projected public tree against the public compact
// and internal snapshot budgets and returns its compact representation, its
// canonical bytes and the internal accounting cost. It is the single budget
// gate of the ordinary candidate and the historical-baseline probe, so the two
// paths cannot drift.
func (e *Engine) publicCandidate(cap device.Capture, projection *Projection) (*protocol.ReadTree, []byte, int, *protocol.APIError) {
	if projection == nil || projection.Tree == nil {
		return nil, nil, 0, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	compact := observation.ProjectCompactTree(projection.Tree)
	if compact == nil || compact.Validate() != nil {
		return nil, nil, 0, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	compactBytes, err := json.Marshal(compact)
	if err != nil {
		return nil, nil, 0, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	if len(compactBytes) > e.cfg.MaxFullTreeBytes {
		return nil, nil, 0, budgetError("public_full", e.cfg.MaxFullTreeBytes)
	}
	cost, err := observation.SnapshotCost(&cap, projection.Tree)
	if err != nil {
		return nil, nil, 0, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	if cost > e.cfg.MaxSnapshotCostBytes {
		return nil, nil, 0, budgetError("internal_snapshot", e.cfg.MaxSnapshotCostBytes)
	}
	return compact, compactBytes, cost, nil
}

// readImageForAcquisition reads the requested image and confirms that the
// target surface did not change during the read. A changed surface discards
// the material and fails the whole acquisition: image and tree would belong to
// different surfaces.
func (e *Engine) readImageForAcquisition(ctx context.Context, work *workState,
	session *sessionState, phase string, sfid uint64) (*imageAttempt, *protocol.APIError) {
	return e.readImageForAcquisitionOnDisplay(ctx, work, session.Display.ID, phase, sfid)
}

// readImageForAcquisitionOnDisplay is the same verified read bound to an
// explicit logical display id. Snap uses the id fixed at admission instead of a
// live session snapshot.
func (e *Engine) readImageForAcquisitionOnDisplay(ctx context.Context, work *workState,
	displayID int, phase string, sfid uint64) (*imageAttempt, *protocol.APIError) {
	attempt := e.readImage(ctx, work, phase, sfid)
	if attempt.read && !e.verifyImageTarget(ctx, displayID, sfid) {
		attempt.meta, attempt.data, attempt.read = nil, nil, false
		attempt.err = protocol.NewError(503, protocol.CodeImageCapture, "截图期间目标屏幕身份已变化")
		return attempt, protocol.NewError(503, protocol.CodeDisplayUnavailable, "目标屏幕不可用")
	}
	return attempt, nil
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
func (e *Engine) verifyImageTarget(ctx context.Context, displayID int, sfid uint64) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := e.dev.ProbeDisplay(probeCtx, displayID)
	return err == nil && state.Exists && state.SFID == sfid
}

// encodeImage performs the single output encode of one attempt. It runs after
// the tree attempt, whether that attempt succeeded or failed.
func (e *Engine) encodeImage(attempt *imageAttempt, session *sessionState) {
	e.encodeImageForDisplay(attempt, session.Display)
}

// encodeImageForDisplay is the same single encode bound to an explicit display
// snapshot. Snap encodes against the geometry fixed at admission.
func (e *Engine) encodeImageForDisplay(attempt *imageAttempt, display device.Display) {
	if !attempt.read || attempt.err != nil {
		return
	}
	meta, data, err := e.imaging.Encode(attempt.source, display,
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

// screenOf projects a device display onto the public screen description. The
// display identity travels with the geometry so a caller can address the same
// display from a managed shell without a second app lookup.
func screenOf(display device.Display) *protocol.Screen {
	id := display.ID
	return &protocol.Screen{
		Width: display.Width, Height: display.Height,
		Rotation: display.Rotation, DisplayID: &id,
	}
}

// observationPublicLocked is the single committed observation projection. The
// status, the optional image and the settle quality are decided by the
// acquisition; everything else keeps its fixed public meaning.
func (e *Engine) observationPublicLocked(obs *observationState, status string, full *protocol.ReadTree,
	delta *protocol.ReadDelta, image *protocol.ImageMeta, quality *protocol.Quality) *protocol.Observation {
	observation := &protocol.Observation{
		Status: status, ID: obs.ID, Origin: obs.Origin, Revision: obs.Revision,
		CapturedAt: formatTime(obs.CapturedAt), ActionableUntil: formatTime(obs.ActionableUntil),
		Quality: quality,
		Screen:  screenOf(obs.Screen),
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
		observation.Tree = &protocol.ReadTreeValue{Delta: delta}
	} else {
		observation.Tree = &protocol.ReadTreeValue{Full: full}
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
	for _, b := range e.baselines {
		if b != nil && b.ObservationID != "" {
			protected[b.ObservationID] = true
		}
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

// confirmBaselineLocked promotes one received full candidate into the bounded
// baseline pool. Only an admitted observe(O), wait_for(O) or a successful act
// claim may call it. The baseline keeps the compact public read tree the delta
// is expressed against and the complete internal projection a historical
// page-return probe diffs from; the complete internal snapshot stays with the
// retained observation.
//
// A baseline must stay retained: the pool protects its observation from
// eviction and inspect(reference=base) has to remain readable, so a candidate
// that is no longer the retained observation never enters. Only the current
// observation may claim the active continuity alignment; confirming a retained
// historical full (for example a replayed acknowledgement) must not point
// activeBaselineID at a frame the projector's b.prev is not aligned with.
func (e *Engine) confirmBaselineLocked(obs *observationState) {
	if obs == nil || !obs.Full || obs.Compact == nil || obs.Projection == nil {
		return
	}
	if e.observations[obs.ID] != obs {
		return
	}
	nextRevision := obs.Revision + 1
	if e.session != nil {
		nextRevision = e.session.Revision + 1
		if obs.Origin != e.session.Origin ||
			obs.DisplayID != e.session.Display.ID ||
			obs.DisplayGeneration != e.session.Display.Generation {
			return
		}
	}
	if nextRevision-obs.Revision > maxBaselineRevisionSpan ||
		time.Since(obs.CapturedAt) >= maxBaselineAge {
		return
	}
	e.pruneBaselinesLocked(nextRevision)
	entry := &baselineState{
		ObservationID: obs.ID, Origin: obs.Origin, Revision: obs.Revision,
		CapturedAt: obs.CapturedAt, DisplayID: obs.DisplayID,
		DisplayGeneration: obs.DisplayGeneration,
		Tree:              obs.Compact, Projection: obs.Projection,
	}
	replaced := false
	for i, b := range e.baselines {
		if b != nil && b.ObservationID == obs.ID {
			e.baselines[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		e.baselines = append(e.baselines, entry)
	}
	e.sortBaselinesLocked()
	e.truncateBaselinesLocked()
	if e.current == obs {
		e.activeBaselineID = obs.ID
	}
}
