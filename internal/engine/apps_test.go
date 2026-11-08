package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func TestAppsNormalPathReturnsFullList(t *testing.T) {
	h := newHarness(t)
	h.device.appEntries = []device.AppEntry{
		{Name: "设置", Package: "com.android.settings"},
		{Name: "Calculator", Package: "com.android.calculator"},
	}
	result, apiErr := h.engine.Apps(context.Background(), "agent", "Papps", &protocol.AppsRequest{})
	if apiErr != nil {
		t.Fatalf("Apps: %v", apiErr)
	}
	if result.OperationID != "Papps" || result.Outcome != "" {
		t.Fatalf("result=%+v", result)
	}
	if result.Apps == nil || len(result.Apps.Items) != 2 || result.Apps.Total != 2 || result.Apps.Truncated {
		t.Fatalf("apps=%+v", result.Apps)
	}
	if _, err := time.Parse(time.RFC3339Nano, result.Apps.CapturedAt); err != nil {
		t.Fatalf("captured_at=%q", result.Apps.CapturedAt)
	}
}

func TestAppsQueryFoldsAndFilters(t *testing.T) {
	h := newHarness(t)
	h.device.appEntries = []device.AppEntry{
		{Name: "Straße", Package: "com.example.strasse"},
		{Name: "设置", Package: "com.android.settings"},
		{Name: "Other", Package: "com.example.other"},
	}
	result, _ := h.engine.Apps(context.Background(), "agent", "P1", &protocol.AppsRequest{Query: "SS", HasQuery: true})
	if result.Apps == nil || len(result.Apps.Items) != 1 || result.Apps.Items[0].Package != "com.example.strasse" {
		t.Fatalf("folded query=%+v", result.Apps)
	}
	result, _ = h.engine.Apps(context.Background(), "agent", "P2", &protocol.AppsRequest{Query: "设置", HasQuery: true})
	if result.Apps == nil || len(result.Apps.Items) != 1 || result.Apps.Items[0].Name != "设置" {
		t.Fatalf("label query=%+v", result.Apps)
	}
}

func TestAppsBudgetTruncatesCompleteEntries(t *testing.T) {
	h := newHarness(t)
	entries := make([]device.AppEntry, 0, 400)
	for i := 0; i < 400; i++ {
		entries = append(entries, device.AppEntry{
			Name:    strings.Repeat("标", 80),
			Package: "com.example.application" + strings.Repeat("x", 20),
		})
	}
	h.device.appEntries = entries
	result, apiErr := h.engine.Apps(context.Background(), "agent", "Papps", &protocol.AppsRequest{})
	if apiErr != nil {
		t.Fatalf("Apps: %v", apiErr)
	}
	if result.Apps == nil || result.Apps.Total != 400 || !result.Apps.Truncated {
		t.Fatalf("apps=%+v", result.Apps)
	}
	encoded, err := protocol.JSON(result.Apps)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 32*1024 {
		t.Fatalf("apps result exceeded budget: %d", len(encoded))
	}
	if len(result.Apps.Items) == 0 || len(result.Apps.Items) == 400 {
		t.Fatalf("expected a non-trivial prefix, got %d", len(result.Apps.Items))
	}
}

func TestAppsEmptyResultStillReturnsEnvelope(t *testing.T) {
	h := newHarness(t)
	h.device.appEntries = []device.AppEntry{{Name: "A", Package: "com.example.a"}}
	result, _ := h.engine.Apps(context.Background(), "agent", "P1",
		&protocol.AppsRequest{Query: "missing", HasQuery: true})
	if result.Apps == nil || result.Apps.Items == nil || len(result.Apps.Items) != 0 ||
		result.Apps.Total != 0 || result.Apps.Truncated {
		t.Fatalf("apps=%+v", result.Apps)
	}
}

func TestAppsHelperFailureIs503(t *testing.T) {
	h := newHarness(t)
	h.device.appErr = context.DeadlineExceeded
	result, apiErr := h.engine.Apps(context.Background(), "agent", "P1", &protocol.AppsRequest{})
	if apiErr != nil {
		t.Fatalf("Apps: %v", apiErr)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeAppListUnavailable || result.Next != "status" {
		t.Fatalf("result=%+v", result)
	}
	if result.Outcome != "" {
		t.Fatalf("apps must not report an action outcome: %+v", result)
	}
}

func TestAppsCacheIsUsedWithoutDeviceRead(t *testing.T) {
	h := newHarness(t)
	h.device.appEntries = []device.AppEntry{{Name: "A", Package: "com.example.a"}}
	first, _ := h.engine.Apps(context.Background(), "agent", "P1", &protocol.AppsRequest{})
	second, _ := h.engine.Apps(context.Background(), "agent", "P2", &protocol.AppsRequest{})
	if first.Apps.CapturedAt != second.Apps.CapturedAt {
		t.Fatalf("cached captured_at changed: %q vs %q", first.Apps.CapturedAt, second.Apps.CapturedAt)
	}
	h.device.mu.Lock()
	calls := h.device.appCalls
	h.device.mu.Unlock()
	if calls != 1 {
		t.Fatalf("cache did not avoid a device read: calls=%d", calls)
	}
}

func TestAppsCacheInvalidatedOnAppInstanceChange(t *testing.T) {
	h := newHarness(t)
	h.device.appEntries = []device.AppEntry{{Name: "A", Package: "com.example.a"}}
	_, _ = h.engine.Apps(context.Background(), "agent", "P1", &protocol.AppsRequest{})
	h.appInstance = "33333333-3333-4333-8333-333333333333"
	h.sync(nil, nil)
	_, _ = h.engine.Apps(context.Background(), "agent", "P2", &protocol.AppsRequest{})
	h.device.mu.Lock()
	calls := h.device.appCalls
	h.device.mu.Unlock()
	if calls != 2 {
		t.Fatalf("new App instance must invalidate the cache: calls=%d", calls)
	}
}

func TestAppsDoesNotRenewLeaseAndIsDetachedFromSession(t *testing.T) {
	h := newHarness(t)
	h.device.appEntries = []device.AppEntry{{Name: "A", Package: "com.example.a"}}
	h.startSession()
	h.engine.mu.Lock()
	before := h.engine.session.ExpiresAt
	sessionOrigin := h.engine.session.Origin
	h.engine.mu.Unlock()
	if _, apiErr := h.engine.Apps(context.Background(), "agent", "Papps", &protocol.AppsRequest{}); apiErr != nil {
		t.Fatalf("Apps: %v", apiErr)
	}
	h.engine.mu.Lock()
	after := h.engine.session.ExpiresAt
	h.engine.mu.Unlock()
	if !after.Equal(before) {
		t.Fatalf("apps renewed the lease: %v -> %v", before, after)
	}
	result, apiErr := h.engine.End("agent", "Pend", "Papps")
	if apiErr != nil {
		t.Fatalf("End: %v", apiErr)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeReferenceNoSession {
		t.Fatalf("end(apps P) must not bind the session: %+v", result)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.session == nil || h.engine.session.State != SessionActive || h.engine.session.Origin != sessionOrigin {
		t.Fatalf("session changed after end(apps P): %+v", h.engine.session)
	}
}
