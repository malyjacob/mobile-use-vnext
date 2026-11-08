package engine

import (
	"context"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// appListCache is one trusted application-list read. It is memory-only and is
// invalidated by TTL or by a paired-App instance change; a cache hit never
// takes the work position and never renews a lease.
type appListCache struct {
	entries     []device.AppEntry
	capturedAt  time.Time
	appInstance string
}

// Apps returns the installed launchable applications. It is a session-free
// discovery tool: a fresh read takes the single device work position, while a
// fresh trusted cache may be served even when another work holds it.
func (e *Engine) Apps(ctx context.Context, agent, p string, req *protocol.AppsRequest) (*protocol.Result, *protocol.APIError) {
	op, duplicate, apiErr := e.registerDetachedOperation(p, agent, "apps")
	if apiErr != nil {
		return nil, apiErr
	}
	if duplicate != nil {
		return duplicate, nil
	}
	if req == nil {
		return e.finishWithError(op, "admission", protocol.Invalid("apps 请求缺失")), nil
	}
	now := time.Now()
	ttl := e.cfg.AppListTTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	e.mu.Lock()
	if e.appsCache != nil && e.appsCache.appInstance == e.app.InstanceID &&
		!e.appsCache.capturedAt.IsZero() && now.Sub(e.appsCache.capturedAt) <= ttl {
		entries := append([]device.AppEntry(nil), e.appsCache.entries...)
		capturedAt := e.appsCache.capturedAt
		e.mu.Unlock()
		return e.finishApps(op, entries, capturedAt, req), nil
	}
	state, reason := e.availabilityLocked(now)
	e.mu.Unlock()
	if state != AvailabilityReady {
		return e.finishAppsError(op, "admission", appsUnavailable(reason)), nil
	}

	work, busy := e.acquireWork(op, WorkApps)
	if busy != nil {
		return e.finishAppsError(op, "admission", busy), nil
	}
	defer e.releaseWork(work)

	e.setOperationPhase(op, "read")
	entries, err := e.dev.AppList(work.Ctx)
	if err != nil {
		if e.detachedWorkRevoked(work) {
			return e.finishAppsError(op, "read",
				protocol.NewError(409, protocol.CodeStopped, "应用清单读取已撤销")), nil
		}
		return e.finishAppsError(op, "read",
			protocol.NewError(503, protocol.CodeAppListUnavailable, "无法从手机读取已安装应用")), nil
	}
	capturedAt := time.Now()
	e.mu.Lock()
	e.appsCache = &appListCache{
		entries:     append([]device.AppEntry(nil), entries...),
		capturedAt:  capturedAt,
		appInstance: e.app.InstanceID,
	}
	e.mu.Unlock()
	return e.finishApps(op, entries, capturedAt, req), nil
}

// finishApps filters by the literal folded query and applies the 32 KiB result
// budget to complete entries. total always counts all matches before the
// budget, and truncated is explicit.
func (e *Engine) finishApps(op *operationState, entries []device.AppEntry,
	capturedAt time.Time, req *protocol.AppsRequest) *protocol.Result {
	matches := make([]device.AppEntry, 0, len(entries))
	if !req.HasQuery || req.Query == "" {
		matches = append(matches, entries...)
	} else {
		needle := protocol.Fold(req.Query)
		for _, entry := range entries {
			if strings.Contains(protocol.Fold(entry.Name), needle) ||
				strings.Contains(protocol.Fold(entry.Package), needle) {
				matches = append(matches, entry)
			}
		}
	}
	total := len(matches)
	limit := e.cfg.MaxAppsBytes
	if limit <= 0 {
		limit = 32 * 1024
	}
	build := func(count int) *protocol.AppsView {
		items := make([]protocol.AppEntry, 0, count)
		for _, entry := range matches[:count] {
			items = append(items, protocol.AppEntry{Name: entry.Name, Package: entry.Package})
		}
		return &protocol.AppsView{
			Items: items, Total: total, Truncated: count < total,
			CapturedAt: formatTime(capturedAt),
		}
	}
	view := build(total)
	if encoded, err := protocol.JSON(view); err != nil {
		view = build(0)
	} else if len(encoded) > limit {
		low, high := 0, total
		for low < high {
			mid := (low + high + 1) / 2
			candidate := build(mid)
			data, err := protocol.JSON(candidate)
			if err == nil && len(data) <= limit {
				low = mid
			} else {
				high = mid - 1
			}
		}
		view = build(low)
	}
	result := newResult(op)
	result.Apps = view
	e.finishOperation(op, "", "finished", nil)
	return result
}

// appsUnavailable maps a control/availability blocker to a controlled tool
// error without inventing a new availability authority.
func appsUnavailable(reason string) *protocol.APIError {
	if reason == "" {
		reason = protocol.CodeDeviceUnavailable
	}
	status := protocol.Status(reason)
	if status == 500 {
		status = 409
	}
	return protocol.NewError(status, reason, "当前不可读取应用清单").WithNext("status")
}

// finishAppsError records a discovery failure without inventing an action
// outcome: apps observes no action to execute or not execute.
func (e *Engine) finishAppsError(op *operationState, phase string, apiErr *protocol.APIError) *protocol.Result {
	e.finishOperation(op, "", phase, apiErr)
	result := newResult(op)
	result.Error = apiErr.Public()
	result.Next = apiErr.Next
	if apiErr.Details != nil {
		if blocking, ok := apiErr.Details["blocking_operation_id"].(string); ok {
			result.BlockingOperationID = blocking
		}
	}
	return result
}
