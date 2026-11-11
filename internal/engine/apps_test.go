package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// appFindRequest builds one find request.
func appFindRequest(query string, hasQuery bool) *protocol.AppRequest {
	return &protocol.AppRequest{Kind: protocol.AppKindFind, Query: query, HasQuery: hasQuery}
}

func TestAppFindReturnsFullList(t *testing.T) {
	h := newHarness(t)
	h.device.appEntries = []device.AppEntry{
		{Name: "设置", Package: "com.android.settings"},
		{Name: "Calculator", Package: "com.android.calculator"},
	}
	result, apiErr := h.engine.App(context.Background(), "agent", "Pfind", appFindRequest("", false))
	if apiErr != nil {
		t.Fatalf("app find: %v", apiErr)
	}
	if result.OperationID != "Pfind" || result.Outcome != "" || result.Error != nil {
		t.Fatalf("result=%+v", result)
	}
	if result.App == nil || result.App.Kind != protocol.AppKindFind ||
		len(result.App.Items) != 2 || result.App.Total != 2 ||
		result.App.Truncated == nil || *result.App.Truncated {
		t.Fatalf("app=%+v", result.App)
	}
	if _, err := time.Parse(time.RFC3339Nano, result.App.CapturedAt); err != nil {
		t.Fatalf("captured_at=%q", result.App.CapturedAt)
	}
	if result.Observation != nil || result.Recovery != nil {
		t.Fatalf("query must not observe or recover: %+v", result)
	}
}

func TestAppFindFoldsLiteralQuery(t *testing.T) {
	h := newHarness(t)
	h.device.appEntries = []device.AppEntry{
		{Name: "Straße", Package: "com.example.strasse"},
		{Name: "设置", Package: "com.android.settings"},
	}
	result, _ := h.engine.App(context.Background(), "agent", "P1", appFindRequest("SS", true))
	if result.App == nil || len(result.App.Items) != 1 ||
		result.App.Items[0].Package != "com.example.strasse" {
		t.Fatalf("folded query=%+v", result.App)
	}
	result, _ = h.engine.App(context.Background(), "agent", "P2", appFindRequest("设置", true))
	if result.App == nil || len(result.App.Items) != 1 || result.App.Items[0].Name != "设置" {
		t.Fatalf("label query=%+v", result.App)
	}
}

func TestAppFindAppliesByteBudget(t *testing.T) {
	h := newHarness(t)
	entries := make([]device.AppEntry, 0, 400)
	for i := 0; i < 400; i++ {
		entries = append(entries, device.AppEntry{
			Name:    "应用",
			Package: "com.example.application." + string(rune('a'+i%26)) + string(rune('a'+i/26)),
		})
	}
	h.device.appEntries = entries
	h.engine.cfg.MaxAppsBytes = 4096
	result, apiErr := h.engine.App(context.Background(), "agent", "Papps", appFindRequest("", false))
	if apiErr != nil {
		t.Fatalf("app find: %v", apiErr)
	}
	if result.App == nil || result.App.Total != 400 ||
		result.App.Truncated == nil || !*result.App.Truncated {
		t.Fatalf("app=%+v", result.App)
	}
	encoded, err := protocol.JSON(result.App)
	if err != nil || len(encoded) > 4096 {
		t.Fatalf("budget exceeded: %d %v", len(encoded), err)
	}
	if len(result.App.Items) == 0 || len(result.App.Items) == 400 {
		t.Fatalf("expected a non-trivial prefix, got %d", len(result.App.Items))
	}
}

func TestAppFindEmptyResultKeepsEmptyArray(t *testing.T) {
	h := newHarness(t)
	h.device.appEntries = []device.AppEntry{{Name: "设置", Package: "com.android.settings"}}
	result, _ := h.engine.App(context.Background(), "agent", "P1", appFindRequest("missing", true))
	if result.App == nil || result.App.Items == nil || len(result.App.Items) != 0 ||
		result.App.Total != 0 || result.App.Truncated == nil || *result.App.Truncated {
		t.Fatalf("app=%+v", result.App)
	}
	if result.Outcome != "" {
		t.Fatalf("query must not carry an outcome: %+v", result)
	}
}

func TestAppFindReadFailureHasNoOutcomeAndNoItems(t *testing.T) {
	h := newHarness(t)
	h.device.appErr = errors.New("app list failed")
	result, apiErr := h.engine.App(context.Background(), "agent", "P1", appFindRequest("", false))
	if apiErr != nil {
		t.Fatalf("transport error: %v", apiErr)
	}
	if result.Error == nil || result.Error.Code != protocol.CodeAppListUnavailable {
		t.Fatalf("expected controlled read failure: %+v", result)
	}
	if result.Outcome != "" || result.App == nil || result.App.Items != nil || result.App.HasTotal {
		t.Fatalf("failed query must not invent a collection: %+v", result)
	}
}

func TestAppFindServesTrustedCacheWithoutWork(t *testing.T) {
	h := newHarness(t)
	h.device.appEntries = []device.AppEntry{{Name: "设置", Package: "com.android.settings"}}
	first, _ := h.engine.App(context.Background(), "agent", "P1", appFindRequest("", false))
	second, _ := h.engine.App(context.Background(), "agent", "P2", appFindRequest("", false))
	if first.App.CapturedAt != second.App.CapturedAt {
		t.Fatalf("cached captured_at changed: %q vs %q", first.App.CapturedAt, second.App.CapturedAt)
	}
	if h.device.appCalls != 1 {
		t.Fatalf("cache miss: app calls=%d", h.device.appCalls)
	}
}

func TestAppFindCacheInvalidatedByAppInstance(t *testing.T) {
	h := newHarness(t)
	h.device.appEntries = []device.AppEntry{{Name: "设置", Package: "com.android.settings"}}
	_, _ = h.engine.App(context.Background(), "agent", "P1", appFindRequest("", false))
	h.appInstance = "33333333-3333-4333-8333-333333333333"
	h.modeGen = "44444444-4444-4444-8444-444444444444"
	h.sync(nil, nil)
	_, _ = h.engine.App(context.Background(), "agent", "P2", appFindRequest("", false))
	if h.device.appCalls != 2 {
		t.Fatalf("instance change must invalidate the cache: calls=%d", h.device.appCalls)
	}
}
