package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func leaseExpiry(t *testing.T, result *protocol.Result) time.Time {
	t.Helper()
	if result == nil || result.Session == nil {
		t.Fatalf("missing lease session: %+v", result)
	}
	expires, err := time.Parse(time.RFC3339Nano, result.Session.ExpiresAt)
	if err != nil {
		t.Fatalf("invalid lease expiry: %v", err)
	}
	return expires
}

func TestStartInitialLeaseFiveMinutes(t *testing.T) {
	h := newHarnessWithClock(t, false)
	before := time.Now()
	result := h.startSession()
	after := time.Now()
	expires := leaseExpiry(t, result)
	if expires.Before(before.Add(5*time.Minute)) || expires.After(after.Add(5*time.Minute)) {
		t.Fatalf("start expiry %v is outside five-minute application window [%v, %v]", expires, before, after)
	}
}

func TestKeepaliveDefaultCanShortenLease(t *testing.T) {
	h := newHarnessWithClock(t, false)
	start := h.startSession()
	h.engine.mu.Lock()
	h.engine.session.ExpiresAt = time.Now().Add(30 * time.Minute)
	inputUntil := h.engine.current.ActionableUntil
	baseline := h.engine.baseline
	h.engine.mu.Unlock()
	before := time.Now()
	result, apiErr := h.engine.Keepalive("agent", "Pshorten-default", &protocol.KeepaliveRequest{Reference: start.OperationID})
	after := time.Now()
	if apiErr != nil || result.Outcome != OutcomeExecuted {
		t.Fatalf("default reset rejected: result=%+v error=%v", result, apiErr)
	}
	expires := leaseExpiry(t, result)
	if expires.Before(before.Add(2*time.Minute)) || expires.After(after.Add(2*time.Minute)) {
		t.Fatalf("default reset did not replace long lease: %v", expires)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if !h.engine.session.ExpiresAt.Equal(expires) || !h.engine.current.ActionableUntil.Equal(inputUntil) || h.engine.baseline != baseline {
		t.Fatal("reset changed observation/baseline or returned another expiry")
	}
}

func TestKeepaliveRequestedMinutesResetThenOrdinaryActivityExtends(t *testing.T) {
	for _, minutes := range []int{1, 2, 120} {
		h := newHarnessWithClock(t, false)
		start := h.startSession()
		before := time.Now()
		result, apiErr := h.engine.Keepalive("agent", "Pexplicit", &protocol.KeepaliveRequest{
			Reference: start.OperationID, DurationMinutes: minutes, HasDurationMinutes: true,
		})
		after := time.Now()
		if apiErr != nil || result == nil || result.Error != nil || result.Outcome != OutcomeExecuted {
			t.Fatalf("explicit reset %d rejected: result=%+v error=%v", minutes, result, apiErr)
		}
		expires := leaseExpiry(t, result)
		duration := time.Duration(minutes) * time.Minute
		if expires.Before(before.Add(duration)) || expires.After(after.Add(duration)) {
			t.Fatalf("explicit %d minute reset returned %v", minutes, expires)
		}
		if result.Session.Origin != start.Session.Origin || result.Session.StartedAt != start.Session.StartedAt || result.Observation != nil {
			t.Fatal("reset changed original session or published an observation")
		}
		h.engine.mu.Lock()
		now := time.Now()
		want := expires
		if next := now.Add(h.engine.cfg.SessionTTL); next.After(want) {
			want = next
		}
		h.engine.touchSessionLocked(now)
		got := h.engine.session.ExpiresAt
		h.engine.mu.Unlock()
		if !got.Equal(want) {
			t.Fatalf("ordinary activity inherited explicit duration or shortened expiry: got=%v want=%v", got, want)
		}
		// A changed body under the same P is still the original operation;
		// its session view may show the ordinary activity's newer expiry.
		replay, apiErr := h.engine.Keepalive("agent", "Pexplicit", &protocol.KeepaliveRequest{
			Reference: start.OperationID, DurationMinutes: 120, HasDurationMinutes: true,
		})
		if apiErr != nil || !leaseExpiry(t, replay).Equal(want) {
			t.Fatalf("replayed operation reset the lease: result=%+v error=%v", replay, apiErr)
		}
	}
}

func TestKeepaliveRejectsExplicitInvalidMinutesWithoutMutation(t *testing.T) {
	h := newHarnessWithClock(t, false)
	start := h.startSession()
	before := leaseExpiry(t, start)
	for _, minutes := range []int{-1, 0, 121} {
		result, apiErr := h.engine.Keepalive("agent", "Pinvalid-duration", &protocol.KeepaliveRequest{
			Reference: start.OperationID, DurationMinutes: minutes, HasDurationMinutes: true,
		})
		if result != nil || apiErr == nil || apiErr.Code != protocol.CodeInvalidRequest {
			t.Fatalf("invalid %d minute reset admitted: result=%+v error=%v", minutes, result, apiErr)
		}
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if !h.engine.session.ExpiresAt.Equal(before) || h.engine.operations["Pinvalid-duration"] != nil {
		t.Fatal("invalid duration changed expiry or registered P")
	}
	resolution := h.engine.resolveKeepaliveLocked("agent", start.OperationID, before)
	if resolution.apiErr == nil || resolution.apiErr.Code != protocol.CodeSessionExpired {
		t.Fatal("equality at expiry was not rejected")
	}
}

func TestStartFirstObservationLeaseAndFailureKeepConcurrentReset(t *testing.T) {
	for _, fail := range []bool{false, true} {
		h := newHarnessWithClock(t, false)
		base := h.engine.projector
		var resetExpiry time.Time
		h.engine.projector = &hookedProjector{Projector: base, build: func(c *device.Capture) (*Projection, error) {
			h.engine.mu.Lock()
			activatedExpiry := h.engine.session.ExpiresAt
			h.engine.mu.Unlock()
			if activatedExpiry.Before(time.Now().Add(4 * time.Minute)) {
				t.Fatal("activation did not establish five-minute lease")
			}
			reset, apiErr := h.engine.Keepalive("agent", "Pstart-reset", &protocol.KeepaliveRequest{
				Reference: "Pstart-lease", DurationMinutes: 1, HasDurationMinutes: true,
			})
			if apiErr != nil || reset.Error != nil {
				t.Fatalf("reset during first observation rejected: %+v %v", reset, apiErr)
			}
			resetExpiry = leaseExpiry(t, reset)
			if fail {
				return nil, errors.New("test projection failure")
			}
			return base.Build(c)
		}}
		h.reportDisplayAfter(30 * time.Millisecond)
		before := time.Now()
		result, apiErr := h.engine.Start(context.Background(), "agent", "Pstart-lease")
		after := time.Now()
		if apiErr != nil || result == nil || result.Outcome != OutcomeExecuted || result.Session.State != SessionActive {
			t.Fatalf("start did not retain original active session: %+v %v", result, apiErr)
		}
		expires := leaseExpiry(t, result)
		if fail {
			if !expires.Equal(resetExpiry) || result.Observation == nil || result.Observation.Status != "failed" {
				t.Fatal("failed first observation renewed or rolled back concurrent reset")
			}
		} else if expires.Before(before.Add(5*time.Minute)) || expires.After(after.Add(5*time.Minute)) {
			t.Fatal("successful first observation did not renew five minutes")
		}
		if _, apiErr = h.engine.Start(context.Background(), "agent", "Pstart-lease"); apiErr != nil {
			t.Fatal(apiErr)
		}
		if _, apiErr = h.engine.Start(context.Background(), "agent", "Pstart-rejected"); apiErr != nil {
			t.Fatal(apiErr)
		}
		h.engine.mu.Lock()
		unchanged := h.engine.session.ExpiresAt.Equal(expires)
		h.engine.mu.Unlock()
		if !unchanged {
			t.Fatal("replayed or rejected start renewed existing session")
		}
	}
}

func TestOrdinaryLeasePassesFormerHardLimit(t *testing.T) {
	h := newHarnessWithClock(t, false)
	h.startSession()
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	session := h.engine.session
	now := session.StartedAt.Add(2 * time.Hour)
	session.ExpiresAt = now.Add(time.Second)
	h.engine.touchSessionLocked(now)
	if !session.ExpiresAt.Equal(now.Add(h.engine.cfg.SessionTTL)) || !sessionBeforeDeadline(session, now) {
		t.Fatalf("ordinary renewal still obeyed former cumulative hard limit: %v", session.ExpiresAt)
	}
}
