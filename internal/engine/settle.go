package engine

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"fmt"
	"hash"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// maxSettleBudget bounds any single settle run. Together with the per-phase
// work windows this keeps one convergence wait inside a small fraction of the
// operation it belongs to.
const maxSettleBudget = 3 * time.Second

// SettlePolicy is the fixed convergence policy of one acquisition phase. It is
// service policy: no request field carries it and the calling agent can
// neither shorten nor extend it.
type SettlePolicy struct {
	Enabled    bool
	MinSamples int           // samples a converged pair needs at minimum
	MaxSamples int           // hard frame limit
	MinDwell   time.Duration // required quiet window between two sampled frames
	MaxBudget  time.Duration // total wall-clock limit of one settle run
}

// SettlePolicies holds the per-phase policies. observe and shell have none:
// observe runs at the caller's pace and shell is not a UI acquisition.
type SettlePolicies struct {
	Act     SettlePolicy
	Start   SettlePolicy
	OpenApp SettlePolicy
}

// DefaultSettlePolicies returns the initial per-phase values. They are sized
// against the work windows of the three phases and are expected to be
// calibrated on real devices; calibration changes these numbers only.
func DefaultSettlePolicies() SettlePolicies {
	return SettlePolicies{
		Act:     SettlePolicy{Enabled: true, MinSamples: 2, MaxSamples: 5, MinDwell: 400 * time.Millisecond, MaxBudget: 2 * time.Second},
		Start:   SettlePolicy{Enabled: true, MinSamples: 2, MaxSamples: 4, MinDwell: 300 * time.Millisecond, MaxBudget: 1500 * time.Millisecond},
		OpenApp: SettlePolicy{Enabled: true, MinSamples: 2, MaxSamples: 5, MinDwell: 600 * time.Millisecond, MaxBudget: 3 * time.Second},
	}
}

// Validate rejects a policy set that cannot behave as documented. It is checked
// once when the process builds its configuration, never per call.
func (p SettlePolicies) Validate() error {
	for _, entry := range []struct {
		name   string
		policy SettlePolicy
	}{
		{"settle.act", p.Act},
		{"settle.start", p.Start},
		{"settle.open_app", p.OpenApp},
	} {
		if err := entry.policy.validate(); err != nil {
			return fmt.Errorf("%s: %w", entry.name, err)
		}
	}
	return nil
}

func (p SettlePolicy) validate() error {
	if !p.Enabled {
		return nil
	}
	if p.MaxSamples < 2 {
		return errors.New("max samples must be at least 2")
	}
	if p.MinSamples < 2 || p.MinSamples > p.MaxSamples {
		return errors.New("min samples must be between 2 and max samples")
	}
	if p.MinDwell < 0 {
		return errors.New("min dwell must not be negative")
	}
	if p.MinDwell > p.MaxBudget {
		return errors.New("min dwell must not exceed the budget")
	}
	if p.MaxBudget > maxSettleBudget {
		return errors.New("budget must not exceed the settle limit")
	}
	return nil
}

// settlePolicy returns the fixed policy of one acquisition phase. A phase
// without a policy never waits for convergence.
func (e *Engine) settlePolicy(phase string) SettlePolicy {
	switch phase {
	case "act":
		return e.cfg.Settle.Act
	case "start":
		return e.cfg.Settle.Start
	case "open_app", "app.open":
		return e.cfg.Settle.OpenApp
	case "app.claim", "app.handoff", "app.close", "app.kill":
		return e.cfg.Settle.Act
	default:
		return SettlePolicy{}
	}
}

// sampleDigest is the settle criterion. Two samples describe the same page
// state only when all three layers agree:
//
//	L1 the raw node identity chain in preorder, i.e. exactly what live
//	   revalidation compares;
//	L2 the canonical public tree, i.e. exactly what the agent reads;
//	L3 the context: foreground state/package/task and display geometry.
//
// The layers are a superset of revalidation, so a converged pair can never be
// followed by a target_changed caused by content that changed after it.
type sampleDigest struct {
	hierarchy [32]byte
	tree      [32]byte
	screen    [32]byte
}

// sampleFrame is one tree-only sample of a settle run.
type sampleFrame struct {
	capture device.Capture
	digest  sampleDigest
	took    time.Duration // wall-clock cost of reading and digesting this frame
}

// settleRun is the accumulated state of one convergence attempt. It is local
// to the acquisition and never reaches the state lock or the public result.
type settleRun struct {
	frame    *sampleFrame
	samples  int
	stable   bool
	started  time.Time
	lastTook time.Duration
}

// captureSample reads one frame and digests it without touching continuity
// state. It runs entirely outside the state lock.
func (e *Engine) captureSample(ctx context.Context, work *workState, session *sessionState,
	phase string) (*sampleFrame, *protocol.APIError) {
	started := time.Now()
	cap, apiErr := e.readCapture(ctx, work, session, phase+".settle")
	if apiErr != nil {
		return nil, apiErr
	}
	projection, err := work.Projector.Preview(&cap)
	if err != nil {
		return nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	digest, err := sampleDigestOf(&cap, projection.Tree)
	if err != nil {
		return nil, protocol.NewError(503, protocol.CodeHierarchyFailed, "公开树构造失败")
	}
	return &sampleFrame{capture: cap, digest: digest, took: time.Since(started)}, nil
}

// settleCapture samples the target display until two consecutive frames agree
// or the frame/budget limits are exhausted. It always returns at least one
// frame; running out of budget is not a failed acquisition. A revoked work or
// an expired context is reported as stopped, and the caller must not publish.
func (e *Engine) settleCapture(ctx context.Context, work *workState, session *sessionState,
	phase string, policy SettlePolicy) (*settleRun, *protocol.APIError) {
	run := &settleRun{started: time.Now()}
	frame, apiErr := e.captureSample(ctx, work, session, phase)
	if apiErr != nil {
		return nil, apiErr
	}
	run.frame = frame
	run.samples = 1
	run.lastTook = frame.took
	if apiErr := e.settleUntilStable(ctx, work, session, phase, policy, run); apiErr != nil {
		return nil, apiErr
	}
	return run, nil
}

// settleUntilStable extends one run with further samples. The sampling period
// is the measured cost of a sample, never a fixed interval, so a slow device
// automatically samples less often. The quiet window is enforced as a minimum
// distance between two sample starts, which is why a converged pair is always
// published as its later frame: the earlier one is never re-sent after a wait.
func (e *Engine) settleUntilStable(ctx context.Context, work *workState, session *sessionState,
	phase string, policy SettlePolicy, run *settleRun) *protocol.APIError {
	stopped := protocol.NewError(409, protocol.CodeStopped, "原会话已撤销")
	for {
		if e.workRevoked(work) || ctx.Err() != nil {
			return stopped
		}
		gap := policy.MinDwell - time.Since(run.frame.capture.CapturedAt)
		if gap < 0 {
			gap = 0
		}
		// Budget pre-check: estimate the next sample from the measured cost of
		// the previous one and never start a sample that cannot fit.
		if run.samples+1 > policy.MaxSamples ||
			time.Since(run.started)+gap+run.lastTook > policy.MaxBudget {
			return nil
		}
		if !pauseInterruptible(ctx, gap) {
			return stopped
		}
		next, apiErr := e.captureSample(ctx, work, session, phase)
		if apiErr != nil {
			if apiErr.Code == protocol.CodeStopped {
				return apiErr
			}
			// One unreadable sample is not a failed post observation: the frame
			// before it is still valid evidence of the same acquisition.
			return nil
		}
		run.samples++
		run.lastTook = next.took
		if next.digest == run.frame.digest && run.samples >= policy.MinSamples {
			run.frame = next
			run.stable = true
			return nil
		}
		run.frame = next
	}
}

// pauseInterruptible waits for gap or until the work context is done. It
// reports whether the wait completed.
func pauseInterruptible(ctx context.Context, gap time.Duration) bool {
	if gap <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(gap)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// sampleDigestOf hashes the three settle layers of one frame.
func sampleDigestOf(cap *device.Capture, tree *protocol.Tree) (sampleDigest, error) {
	encoded, err := json.Marshal(tree)
	if err != nil {
		return sampleDigest{}, err
	}
	return sampleDigest{
		hierarchy: hierarchyDigest(cap),
		tree:      sha256.Sum256(encoded),
		screen:    screenDigest(cap),
	}, nil
}

// rawIdentityFieldCount is the number of fields rawIdentity has today. The
// digest below writes exactly those fields; the field-coverage test fails when
// identityOf grows without the digest being extended in the same change.
const rawIdentityFieldCount = 19

// hierarchyDigest is L1: every node's identity chained to its parent's
// fingerprint, hashed in preorder.
func hierarchyDigest(cap *device.Capture) [32]byte {
	h := sha256.New()
	type pending struct {
		index  int
		parent [32]byte
	}
	stack := []pending{{index: cap.Root}}
	seen := make([]bool, len(cap.Nodes))
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top.index < 0 || top.index >= len(cap.Nodes) || seen[top.index] {
			continue
		}
		seen[top.index] = true
		node := &cap.Nodes[top.index]
		fingerprint := identityFingerprint(node, top.parent)
		h.Write(fingerprint[:])
		for i := len(node.Children) - 1; i >= 0; i-- {
			stack = append(stack, pending{index: node.Children[i], parent: fingerprint})
		}
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// identityFingerprint hashes one node identity together with its parent's
// fingerprint. The zero parent keeps the root well defined.
func identityFingerprint(node *device.RawNode, parent [32]byte) [32]byte {
	id := identityOf(node)
	h := sha256.New()
	writeText(h, id.Package)
	writeText(h, id.Class)
	writeText(h, id.ResourceID)
	writeText(h, id.Text)
	writeText(h, id.Description)
	writeInt(h, int64(id.Bounds.Left))
	writeInt(h, int64(id.Bounds.Top))
	writeInt(h, int64(id.Bounds.Right))
	writeInt(h, int64(id.Bounds.Bottom))
	for _, flag := range []bool{
		id.Clickable, id.LongClickable, id.Scrollable, id.Editable, id.Password,
		id.HasEnabled, id.Enabled,
		id.HasChecked, id.Checked,
		id.HasSelected, id.Selected,
		id.HasFocused, id.Focused,
	} {
		writeBool(h, flag)
	}
	h.Write(parent[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// screenDigest is L3: the context a tree belongs to. A foreground switch or a
// geometry change makes two otherwise identical trees different frames.
func screenDigest(cap *device.Capture) [32]byte {
	h := sha256.New()
	writeText(h, cap.Foreground.State)
	writeText(h, cap.Foreground.Package)
	if cap.Foreground.TaskID == nil {
		writeBool(h, false)
	} else {
		writeBool(h, true)
		writeInt(h, int64(*cap.Foreground.TaskID))
	}
	writeInt(h, int64(cap.Display.Width))
	writeInt(h, int64(cap.Display.Height))
	writeInt(h, int64(cap.Display.Rotation))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// writeText writes a length-prefixed string so field boundaries cannot be
// confused with field content.
func writeText(h hash.Hash, value string) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	h.Write(length[:])
	h.Write([]byte(value))
}

func writeInt(h hash.Hash, value int64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	h.Write(encoded[:])
}

func writeBool(h hash.Hash, value bool) {
	if value {
		h.Write([]byte{1})
		return
	}
	h.Write([]byte{0})
}

// logSettle records the convergence facts of one post observation. They are
// diagnostic only and never part of the published observation.
func (e *Engine) logSettle(phase string, run *settleRun) {
	if run == nil {
		return
	}
	e.log.Event(eventlog.Debug, "observation.settle",
		eventlog.Str("phase", phase),
		eventlog.Int("samples", run.samples),
		eventlog.Bool("stable", run.stable),
		eventlog.Duration("ms", time.Since(run.started)))
}
