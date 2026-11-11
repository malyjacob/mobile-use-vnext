package engine

import (
	"context"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// appQuery dispatches the two read-only app kinds. Neither consumes the current
// observation, renews a lease nor publishes an action outcome.
func (e *Engine) appQuery(ctx context.Context, op *operationState, req *protocol.AppRequest) (*protocol.Result, *protocol.APIError) {
	switch req.Kind {
	case protocol.AppKindFind:
		return e.appFind(ctx, op, req), nil
	case protocol.AppKindList:
		return e.appList(ctx, op, req)
	}
	return nil, protocol.Invalid("不支持的 app 查询 kind")
}

// appFind lists launchable applications with the optional literal folded query.
// A fresh trusted cache may be served without taking the work position; a fresh
// read takes the query work position and honours the request context.
func (e *Engine) appFind(ctx context.Context, op *operationState, req *protocol.AppRequest) *protocol.Result {
	now := time.Now()
	ttl := e.cfg.AppListTTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	e.mu.Lock()
	if barrier := e.ordinaryBarrierLocked(op, now); barrier != nil {
		e.mu.Unlock()
		return e.finishAppQueryError(op, "admission", appKindOnly(req.Kind), barrier)
	}
	if ctx.Err() != nil {
		e.mu.Unlock()
		return e.appQueryClosed(op, nil)
	}
	if e.appsCache != nil && e.appsCache.appInstance == e.app.InstanceID &&
		!e.appsCache.capturedAt.IsZero() && now.Sub(e.appsCache.capturedAt) <= ttl {
		entries := append([]device.AppEntry(nil), e.appsCache.entries...)
		capturedAt := e.appsCache.capturedAt
		e.mu.Unlock()
		return e.finishAppQuerySuccess(op, findView(entries, capturedAt, req, e.cfg.MaxAppsBytes))
	}
	state, reason := e.availabilityLocked(now)
	e.mu.Unlock()
	if state != AvailabilityReady {
		return e.finishAppQueryError(op, "admission", appKindOnly(req.Kind), appsUnavailable(reason))
	}

	work, busy := e.acquireAppQueryWork(op, ctx)
	if busy != nil {
		return e.finishAppQueryError(op, "admission", appKindOnly(req.Kind), busy)
	}
	defer e.releaseWork(work)

	e.setWorkPhase(work, "read")
	entries, err := e.dev.AppList(work.Ctx)
	if err != nil {
		if e.appQueryContextRevoked(work) {
			return e.appQueryClosed(op, work)
		}
		return e.finishAppQueryError(op, "read", appKindOnly(req.Kind),
			protocol.NewError(503, protocol.CodeAppListUnavailable, "无法从手机读取已安装应用"))
	}
	capturedAt := time.Now()
	if e.appQueryContextRevoked(work) {
		return e.appQueryClosed(op, work)
	}
	e.mu.Lock()
	e.appsCache = &appListCache{
		entries:     append([]device.AppEntry(nil), entries...),
		capturedAt:  capturedAt,
		appInstance: e.app.InstanceID,
	}
	e.mu.Unlock()
	return e.finishAppQuerySuccess(op, findView(entries, capturedAt, req, e.cfg.MaxAppsBytes))
}

// findView filters by the literal folded query and applies the 32 KiB result
// budget to complete entries. total always counts all matches before the budget.
func findView(entries []device.AppEntry, capturedAt time.Time, req *protocol.AppRequest, limit int) *protocol.AppView {
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
	if limit <= 0 {
		limit = 32 * 1024
	}
	build := func(count int) *protocol.AppView {
		items := make([]protocol.AppItem, 0, count)
		for _, entry := range matches[:count] {
			items = append(items, protocol.AppItem{Name: entry.Name, Package: entry.Package})
		}
		return &protocol.AppView{
			Kind: protocol.AppKindFind, Items: items, HasItems: true,
			Total: total, HasTotal: true,
			Truncated: appBoolPtr(count < total), CapturedAt: formatTime(capturedAt),
		}
	}
	view := build(total)
	encoded, err := protocol.JSON(view)
	if err != nil {
		return build(0)
	}
	if len(encoded) <= limit {
		return view
	}
	low, high := 0, total
	for low < high {
		mid := (low + high + 1) / 2
		candidate, err := protocol.JSON(build(mid))
		if err == nil && len(candidate) <= limit {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return build(low)
}

// appListSessionByOperationLocked resolves a P reference to its original
// session. The P is a locator, never a second authority: the current session
// record still decides whether the reference is usable.
func (e *Engine) appListSessionByOperationLocked(agent, reference string, now time.Time) (*sessionState, *protocol.APIError) {
	record := e.operations[reference]
	if record == nil {
		if _, closed := e.closed[reference]; closed {
			return nil, protocol.NewError(409, protocol.CodeRequestClosed, "原操作已关闭")
		}
		return nil, protocol.NewError(409, protocol.CodeOperationUnknown, "原操作不可用")
	}
	if record.Agent != agent {
		return nil, protocol.NewError(409, protocol.CodeOperationNotFound, "原操作不可用")
	}
	session := e.sessionByOriginLocked(record.SessionOrigin)
	if session == nil || session.State != SessionActive {
		return nil, protocol.NewError(409, protocol.CodeSessionEnded, "原会话已结束")
	}
	if !sessionBeforeDeadline(session, now) {
		return nil, protocol.NewError(409, protocol.CodeSessionExpired, "原会话已过期")
	}
	return session, nil
}

// appList lists the live tasks of one display plus the foreground fact. It is a
// pure read: no task is claimed, adopted or moved, and the observation is only
// referenced to prove the original session.
func (e *Engine) appList(ctx context.Context, op *operationState, req *protocol.AppRequest) (*protocol.Result, *protocol.APIError) {
	now := time.Now()
	e.mu.Lock()
	var session *sessionState
	var staticErr *protocol.APIError
	if len(req.Reference) > 0 && req.Reference[0] == 'P' {
		session, staticErr = e.appListSessionByOperationLocked(op.Agent, req.Reference, now)
	} else {
		_, session, staticErr = e.appStaticCheckLocked(op.Agent, req.Reference, now)
	}
	var displayID int
	if staticErr == nil {
		displayID = session.Display.ID
		if session.Mode == "primary" || req.Scope == protocol.AppScopePrimary {
			displayID = 0
		}
	}
	e.mu.Unlock()
	if staticErr != nil {
		return e.finishAppQueryError(op, "admission", appKindOnly(req.Kind), staticErr), nil
	}

	work, busy := e.acquireAppQueryWork(op, ctx)
	if busy != nil {
		return e.finishAppQueryError(op, "admission", appKindOnly(req.Kind), busy), nil
	}
	defer e.releaseWork(work)

	e.setWorkPhase(work, "read")
	all, err := e.dev.TaskList(work.Ctx)
	if err != nil {
		if e.appQueryContextRevoked(work) {
			return e.appQueryClosed(op, work), nil
		}
		return e.finishAppQueryError(op, "read", appViewWithDisplay(req.Kind, displayID),
			protocol.NewError(503, protocol.CodeDeviceUnavailable, "无法读取任务清单")), nil
	}
	capturedAt := time.Now()
	foreground, foregroundErr := e.dev.Foreground(work.Ctx, displayID)
	if e.appQueryContextRevoked(work) {
		return e.appQueryClosed(op, work), nil
	}

	e.mu.Lock()
	entries := map[int]taskEntry{}
	if e.session != nil && e.session.State != SessionEnded && e.session.Display.ID == displayID {
		for _, entry := range e.session.Tasks {
			entries[entry.TaskID] = entry
		}
	}
	e.mu.Unlock()

	items := make([]protocol.AppTaskItem, 0, len(all))
	for _, task := range all {
		if task.DisplayID != displayID || task.TaskID <= 0 {
			continue
		}
		if req.HasFilter && task.Package != req.FilterApp {
			continue
		}
		item := protocol.AppTaskItem{
			TaskID: task.TaskID, Package: task.Package,
			Ownership: protocol.TaskOwnershipShared,
		}
		if entry, ok := entries[task.TaskID]; ok && entry.Ownership != "" {
			item.Ownership = entry.Ownership
			if entry.OriginDisplay != nil {
				item.OriginDisplay = appIntPtr(*entry.OriginDisplay)
			}
		}
		items = append(items, item)
	}
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].TaskID < items[j-1].TaskID; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}

	var foregroundView *protocol.Foreground
	if foregroundErr == nil {
		foregroundView = publicForeground(foreground)
	} else {
		foregroundView = &protocol.Foreground{
			State: device.ForegroundUnknown, Reason: "foreground_unreadable",
		}
	}
	view := listView(items, displayID, foregroundView, capturedAt, e.cfg.MaxAppsBytes)
	return e.finishAppQuerySuccess(op, view), nil
}

// listView applies the 32 KiB budget to complete list items.
func listView(items []protocol.AppTaskItem, displayID int, foreground *protocol.Foreground,
	capturedAt time.Time, limit int) *protocol.AppView {
	total := len(items)
	if limit <= 0 {
		limit = 32 * 1024
	}
	build := func(count int) *protocol.AppView {
		selected := make([]protocol.AppTaskItem, 0, count)
		for _, item := range items[:count] {
			copy := item
			selected = append(selected, copy)
		}
		return &protocol.AppView{
			Kind: protocol.AppKindList, DisplayID: appIntPtr(displayID),
			TaskItems: selected, HasItems: true, Total: total, HasTotal: true,
			Truncated: appBoolPtr(count < total), CapturedAt: formatTime(capturedAt),
			Foreground: foreground,
		}
	}
	view := build(total)
	encoded, err := protocol.JSON(view)
	if err != nil {
		return build(0)
	}
	if len(encoded) <= limit {
		return view
	}
	low, high := 0, total
	for low < high {
		mid := (low + high + 1) / 2
		candidate, err := protocol.JSON(build(mid))
		if err == nil && len(candidate) <= limit {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return build(low)
}

// publicForeground projects one adapter foreground record.
func publicForeground(foreground device.Foreground) *protocol.Foreground {
	view := &protocol.Foreground{State: foreground.State}
	switch foreground.State {
	case device.ForegroundPresent:
		view.Package = foreground.Package
		view.TaskID = foreground.TaskID
	case device.ForegroundUnknown:
		view.Reason = foreground.Reason
		if view.Reason == "" {
			view.Reason = "foreground_unknown"
		}
	}
	return view
}
