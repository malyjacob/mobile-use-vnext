package main

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// appLifecycleChecks is the controlled writable-loop suite. It mutates only the
// dedicated test-target package, never switches the session display mode, and
// rebuilds the controlled task with a fresh ready O before each dangerous
// action instead of replaying a consumed O or reusing a previous result.
func (p *probe) appLifecycleChecks(ctx context.Context, start startSummary) bool {
	if start.Mode != "virtual" {
		// The operator confirms a primary session in the control App. The probe
		// must never switch modes by itself, so a primary session is reported as
		// skipped rather than exercised with user tasks.
		p.reporter.skip("app_lifecycle", "session_mode_not_virtual", field("session_mode", start.Mode))
		return true
	}
	target := p.appTarget
	if target == "" {
		target = controlledAppPackage
	}
	readyO := start.ObservationID

	open, failure := p.call(ctx, "app", map[string]any{
		"kind": "open", "reference": readyO, "target": map[string]any{"app": target},
	})
	if failure != nil {
		p.reporter.fail("app_open", failure.reason, failure.fields...)
		return false
	}
	if open.result.IsError || resultOutcome(open) != outcomeExecuted ||
		open.view.App == nil || open.view.App.Kind != "open" {
		p.reporter.fail("app_open", "open_not_executed",
			field("outcome", resultOutcome(open)), field("code", codeOf(open.view)))
		return false
	}
	openApp := open.view.App
	if openApp.Package != target || openApp.TaskID == nil || *openApp.TaskID <= 0 ||
		openApp.DisplayID == nil || *openApp.DisplayID <= 0 ||
		openApp.Ownership != "owned" || openApp.Reused == nil ||
		!openApp.HasStatePreserved {
		p.reporter.fail("app_open", "open_facts_unexpected",
			field("ownership", openApp.Ownership), field("task_id", integerString(openApp.TaskID)),
			field("display_id", integerString(openApp.DisplayID)))
		return false
	}
	taskID := *openApp.TaskID
	displayID := *openApp.DisplayID
	nextO, failure := appPostObservation(open.view)
	if failure != nil {
		p.reporter.fail("app_open", failure.reason, failure.fields...)
		return false
	}
	p.reporter.pass("app_open",
		field("package", target), field("task_id", strconv.Itoa(taskID)),
		field("display_id", strconv.Itoa(displayID)), "ownership=owned",
		field("reused", boolString(openApp.Reused)), "state_preserved=present")

	list, failure := p.call(ctx, "app", map[string]any{
		"kind": "list", "reference": start.OperationID, "app": target,
	})
	if failure != nil {
		p.reporter.fail("app_list_controlled", failure.reason, failure.fields...)
		return false
	}
	if list.result.IsError || list.view.App == nil || list.view.App.Kind != "list" ||
		list.view.App.DisplayID == nil || *list.view.App.DisplayID != displayID {
		p.reporter.fail("app_list_controlled", "controlled_task_missing", field("code", codeOf(list.view)))
		return false
	}
	if !appContainsTask(list.view.App, taskID, "owned") {
		p.reporter.fail("app_list_controlled", "controlled_task_not_owned")
		return false
	}
	p.reporter.pass("app_list_controlled",
		field("task_id", strconv.Itoa(taskID)), field("display_id", strconv.Itoa(displayID)),
		"ownership=owned")

	// A kill is attempted only on the controlled owned task. The contract kills
	// the whole package once per Android user and never removes the task window,
	// so the probe validates the package/user/task/process facts instead of
	// requiring the window to disappear. A refusal keeps the original close path.
	kill, failure := p.call(ctx, "app", map[string]any{
		"kind": "kill", "reference": nextO, "target": map[string]any{"task_id": taskID},
	})
	if failure != nil {
		p.reporter.fail("app_kill", failure.reason, failure.fields...)
		return false
	}
	killCode := codeOf(kill.view)
	killOutcome := resultOutcome(kill)
	switch {
	case killOutcome == "uncertain" || killCode == "kill_unconfirmed":
		// A dispatched command with an unconfirmed result is an honest contract
		// outcome: the verified identity and frozen facts still apply, the final
		// read stays unknown, and the task window is not removed.
		if kill.view.App == nil || kill.view.App.Kind != "kill" ||
			kill.view.App.SourceTaskID == nil || *kill.view.App.SourceTaskID != taskID {
			p.reporter.fail("app_kill", "kill_facts_unexpected",
				field("outcome", killOutcome), field("code", killCode))
			return false
		}
		if !containsInt(kill.view.App.TargetTaskIDs, taskID) {
			p.reporter.fail("app_kill", "kill_target_tasks_missing_source",
				field("task_id", strconv.Itoa(taskID)),
				field("target_tasks", intListText(kill.view.App.TargetTaskIDs, kill.view.App.HasTargetTaskIDs)))
			return false
		}
		if failure := checkKillFacts(kill.view.App); failure != nil {
			p.reporter.fail("app_kill", failure.reason, failure.fields...)
			return false
		}
		p.reporter.pass("app_kill", append(killFactFields(kill.view.App, true),
			"outcome=uncertain", "code="+killCode)...)
		// An uncertain result retired the O; rebuild the ready reference before
		// the cleanup close runs.
		updated, failure := p.readyObservation(ctx, start.OperationID)
		if failure != nil {
			p.reporter.fail("app_close", failure.reason, failure.fields...)
			return false
		}
		nextO = updated
	case kill.result.IsError:
		if killOutcome == outcomeExecuted {
			p.reporter.fail("app_kill_refused", "executed_result_marked_error")
			return false
		}
		if !appKillRefusalCode(killCode) {
			p.reporter.fail("app_kill_refused", "unexpected_refusal", field("code", killCode))
			return false
		}
		if kill.view.App != nil {
			if kill.view.App.Kind != "kill" {
				p.reporter.fail("app_kill_refused", "app_kind_mismatch", field("kind", kill.view.App.Kind))
				return false
			}
			if failure := checkNoFinalKillFacts(kill.view.App); failure != nil {
				p.reporter.fail("app_kill_refused", failure.reason, failure.fields...)
				return false
			}
		}
		p.reporter.pass("app_kill_refused",
			field("task_id", strconv.Itoa(taskID)), field("code", killCode),
			"outcome="+outcomeOrNone(killOutcome))
		// A refusal before claim keeps the O usable, but a refusal after claim
		// consumes it; rebuild one ready observation so cleanup never replays a
		// stale reference.
		updated, failure := p.readyObservation(ctx, start.OperationID)
		if failure != nil {
			p.reporter.fail("app_close", failure.reason, failure.fields...)
			return false
		}
		nextO = updated
	default:
		if killOutcome != outcomeExecuted || kill.view.App == nil ||
			kill.view.App.Kind != "kill" || kill.view.App.SourceTaskID == nil ||
			*kill.view.App.SourceTaskID != taskID || kill.view.App.Package != target ||
			kill.view.App.DisplayID == nil || *kill.view.App.DisplayID != displayID {
			p.reporter.fail("app_kill", "kill_facts_unexpected",
				field("outcome", killOutcome), field("code", killCode))
			return false
		}
		if !containsInt(kill.view.App.TargetTaskIDs, taskID) {
			p.reporter.fail("app_kill", "kill_target_tasks_missing_source",
				field("task_id", strconv.Itoa(taskID)),
				field("target_tasks", intListText(kill.view.App.TargetTaskIDs, kill.view.App.HasTargetTaskIDs)))
			return false
		}
		if failure := checkKillFacts(kill.view.App); failure != nil {
			p.reporter.fail("app_kill", failure.reason, failure.fields...)
			return false
		}
		if updated, failure := appPostObservation(kill.view); failure != nil {
			p.reporter.fail("app_kill", failure.reason, failure.fields...)
			return false
		} else {
			nextO = updated
		}
		p.reporter.pass("app_kill", killFactFields(kill.view.App, true)...)
	}

	// A force-stopped package keeps its task window: close that same window when
	// it is still listed. If the window did disappear, rebuild the controlled
	// owned task so app.close still runs against a live task.
	windowIDs, failure := p.packageTaskIDs(ctx, start.OperationID, target)
	if failure != nil {
		p.reporter.fail("app_close", failure.reason, failure.fields...)
		return false
	}
	closedExecuted := false
	if containsInt(windowIDs, taskID) {
		nextO, closedExecuted, failure = p.closeControlledTask(ctx, nextO, taskID)
		if failure != nil {
			p.reporter.fail("app_close", failure.reason, failure.fields...)
			return false
		}
	} else {
		reopenO, failure := p.readyObservation(ctx, start.OperationID)
		if failure != nil {
			p.reporter.fail("app_close", failure.reason, failure.fields...)
			return false
		}
		reopened, failure := p.call(ctx, "app", map[string]any{
			"kind": "open", "reference": reopenO, "target": map[string]any{"app": target},
		})
		if failure != nil {
			p.reporter.fail("app_close", failure.reason, failure.fields...)
			return false
		}
		if reopened.result.IsError || resultOutcome(reopened) != outcomeExecuted ||
			reopened.view.App == nil || reopened.view.App.Kind != "open" {
			p.reporter.fail("app_close", "reopen_after_kill_not_executed",
				field("outcome", resultOutcome(reopened)), field("code", codeOf(reopened.view)))
			return false
		}
		reopenedApp := reopened.view.App
		if reopenedApp.Package != target || reopenedApp.TaskID == nil ||
			*reopenedApp.TaskID <= 0 || reopenedApp.DisplayID == nil ||
			*reopenedApp.DisplayID <= 0 || reopenedApp.Ownership != "owned" {
			p.reporter.fail("app_close", "reopen_after_kill_facts_unexpected",
				field("ownership", reopenedApp.Ownership),
				field("task_id", integerString(reopenedApp.TaskID)),
				field("display_id", integerString(reopenedApp.DisplayID)))
			return false
		}
		reopenedTaskID := *reopenedApp.TaskID
		reopenReadyO, failure := appPostObservation(reopened.view)
		if failure != nil {
			p.reporter.fail("app_close", failure.reason, failure.fields...)
			return false
		}
		nextO, closedExecuted, failure = p.closeControlledTask(ctx, reopenReadyO, reopenedTaskID)
		if failure != nil {
			p.reporter.fail("app_close", failure.reason, failure.fields...)
			return false
		}
	}

	// Closed loop: a confirmed close of an owned task is the only thing that
	// establishes the five-minute close eligibility behind the package entry.
	if !p.appPackageKillChecks(ctx, start, target, closedExecuted) {
		return false
	}

	// claim needs a second controlled task that already lives on the primary
	// display. The probe never creates one by switching modes; it only claims a
	// task it can prove is the controlled target. A fresh full observation
	// rebuilds the ready-O precondition even if close ended uncertain.
	primary, failure := p.call(ctx, "app", map[string]any{
		"kind": "list", "reference": start.OperationID, "scope": "primary", "app": target,
	})
	if failure != nil || primary.result.IsError || primary.view.App == nil {
		p.reporter.skip("app_claim", "primary_list_unavailable")
		return true
	}
	candidates := primary.view.App.TaskItems
	if len(candidates) == 0 {
		p.reporter.skip("app_claim", "controlled_primary_task_absent")
		return true
	}
	claimO, failure := p.readyObservation(ctx, start.OperationID)
	if failure != nil {
		p.reporter.fail("app_claim", failure.reason, failure.fields...)
		return false
	}
	claimTask := candidates[0].TaskID
	claim, failure := p.call(ctx, "app", map[string]any{
		"kind": "claim", "reference": claimO,
		"target": map[string]any{"task_id": claimTask},
	})
	if failure != nil {
		p.reporter.fail("app_claim", failure.reason, failure.fields...)
		return false
	}
	if claim.result.IsError || resultOutcome(claim) != outcomeExecuted ||
		claim.view.App == nil || claim.view.App.Kind != "claim" ||
		claim.view.App.Ownership != "borrowed" || claim.view.App.SourceDisplayID == nil ||
		*claim.view.App.SourceDisplayID != 0 || claim.view.App.TaskID == nil ||
		*claim.view.App.TaskID != claimTask || claim.view.App.DisplayID == nil ||
		*claim.view.App.DisplayID != displayID || !claim.view.App.HasStatePreserved {
		p.reporter.fail("app_claim", "claim_facts_unexpected",
			field("outcome", resultOutcome(claim)), field("code", codeOf(claim.view)))
		return false
	}
	p.reporter.pass("app_claim",
		field("task_id", strconv.Itoa(claimTask)),
		field("display_id", integerString(claim.view.App.DisplayID)),
		"ownership=borrowed", "source_display_id=0")
	return true
}

// appPackageKillChecks runs the two package-entry kill scenarios after the
// controlled task has been closed. A close that executed established the close
// eligibility; a live owned same-package task would authorize the package entry
// as well. Both scenarios are environment-dependent, so an unprovable setup is
// reported as SKIP instead of a failure.
func (p *probe) appPackageKillChecks(ctx context.Context, start startSummary, target string, closeEligible bool) bool {
	// Scenario A: while the controlled package is also live on the primary
	// display, a package kill must be refused, never dispatched.
	if !p.appPrimaryRefusedCheck(ctx, start, target, closeEligible) {
		return false
	}
	// Scenario B: open, close, then kill the whole package by name.
	return p.appCloseThenPackageKill(ctx, start, target)
}

const (
	killRefusedNotOwned      = "kill_refused_not_owned"
	killRefusedTaskElsewhere = "kill_refused_task_elsewhere"
)

// appPrimaryRefusedCheck proves the ownership guard of the package entry: a
// same-package live task on the primary display, while this session holds an
// owned task or a confirmed-close eligibility, must be refused before dispatch.
// It returns true when the scenario ran or was skipped, and false only on a
// contract failure.
func (p *probe) appPrimaryRefusedCheck(ctx context.Context, start startSummary, target string, closeEligible bool) bool {
	primary, failure := p.call(ctx, "app", map[string]any{
		"kind": "list", "reference": start.OperationID, "scope": "primary", "app": target,
	})
	if failure != nil || primary.result.IsError || primary.view.App == nil ||
		primary.view.App.Kind != "list" || !primary.view.App.HasTaskItems {
		p.reporter.skip("app_kill_primary_refused", "primary_list_unavailable")
		return true
	}
	primaryTaskID := 0
	for _, item := range primary.view.App.TaskItems {
		if item.Package == target && item.TaskID > 0 {
			primaryTaskID = item.TaskID
			break
		}
	}
	if primaryTaskID == 0 {
		p.reporter.skip("app_kill_primary_refused", "controlled_primary_task_absent")
		return true
	}
	sessionIDs, failure := p.packageTaskIDs(ctx, start.OperationID, target)
	if failure != nil {
		p.reporter.skip("app_kill_primary_refused", "session_task_list_unavailable",
			failure.fields...)
		return true
	}
	if !closeEligible && len(sessionIDs) == 0 {
		p.reporter.skip("app_kill_primary_refused", "no_owned_task_or_close_eligibility")
		return true
	}
	killO, failure := p.readyObservation(ctx, start.OperationID)
	if failure != nil {
		p.reporter.skip("app_kill_primary_refused", "ready_observation_missing",
			failure.fields...)
		return true
	}
	refused, failure := p.call(ctx, "app", map[string]any{
		"kind": "kill", "reference": killO, "target": map[string]any{"app": target},
	})
	if failure != nil {
		p.reporter.fail("app_kill_primary_refused", failure.reason, failure.fields...)
		return false
	}
	if refused.result.IsError {
		code := codeOf(refused.view)
		if resultOutcome(refused) == outcomeExecuted {
			p.reporter.fail("app_kill_primary_refused", "executed_result_marked_error")
			return false
		}
		if code != killRefusedNotOwned && code != killRefusedTaskElsewhere {
			p.reporter.fail("app_kill_primary_refused", "unexpected_refusal", field("code", code))
			return false
		}
		if refused.view.App != nil {
			if refused.view.App.Kind != "kill" {
				p.reporter.fail("app_kill_primary_refused", "app_kind_mismatch",
					field("kind", refused.view.App.Kind))
				return false
			}
			if failure := checkNoFinalKillFacts(refused.view.App); failure != nil {
				p.reporter.fail("app_kill_primary_refused", failure.reason, failure.fields...)
				return false
			}
		}
		p.reporter.pass("app_kill_primary_refused",
			field("primary_task", strconv.Itoa(primaryTaskID)), field("code", code),
			"outcome="+outcomeOrNone(resultOutcome(refused)))
		return true
	}
	p.reporter.fail("app_kill_primary_refused", "primary_task_not_refused",
		field("primary_task", strconv.Itoa(primaryTaskID)),
		field("package_state", packageStateOf(refused.view)))
	return false
}

// appCloseThenPackageKill starts from a clean session: it opens the controlled
// target, closes it through a confirmed close so the close eligibility exists,
// then kills the whole package by name. Success and a controlled refusal are
// both PASS; an unprovable setup is SKIP.
func (p *probe) appCloseThenPackageKill(ctx context.Context, start startSummary, target string) bool {
	openO, failure := p.readyObservation(ctx, start.OperationID)
	if failure != nil {
		p.reporter.skip("app_kill_package", "ready_observation_missing", failure.fields...)
		return true
	}
	opened, failure := p.call(ctx, "app", map[string]any{
		"kind": "open", "reference": openO, "target": map[string]any{"app": target},
	})
	if failure != nil {
		p.reporter.fail("app_kill_package", failure.reason, failure.fields...)
		return false
	}
	if opened.result.IsError || resultOutcome(opened) != outcomeExecuted ||
		opened.view.App == nil || opened.view.App.Kind != "open" ||
		opened.view.App.TaskID == nil || *opened.view.App.TaskID <= 0 ||
		opened.view.App.DisplayID == nil || *opened.view.App.DisplayID <= 0 ||
		opened.view.App.Ownership != "owned" {
		p.reporter.skip("app_kill_package", "package_kill_open_not_executed",
			field("outcome", resultOutcome(opened)), field("code", codeOf(opened.view)))
		return true
	}
	displayID := *opened.view.App.DisplayID
	taskID := *opened.view.App.TaskID
	closeO, failure := appPostObservation(opened.view)
	if failure != nil {
		p.reporter.fail("app_kill_package", failure.reason, failure.fields...)
		return false
	}
	killO, closedExecuted, failure := p.closeControlledTask(ctx, closeO, taskID)
	if failure != nil {
		p.reporter.fail("app_kill_package", failure.reason, failure.fields...)
		return false
	}
	if !closedExecuted {
		p.reporter.skip("app_kill_package", "package_kill_close_not_executed",
			field("task_id", strconv.Itoa(taskID)))
		return true
	}
	expected, failure := p.packageTaskIDs(ctx, start.OperationID, target)
	if failure != nil {
		p.reporter.fail("app_kill_package", failure.reason, failure.fields...)
		return false
	}
	killed, failure := p.call(ctx, "app", map[string]any{
		"kind": "kill", "reference": killO, "target": map[string]any{"app": target},
	})
	if failure != nil {
		p.reporter.fail("app_kill_package", failure.reason, failure.fields...)
		return false
	}
	killCode := codeOf(killed.view)
	killOutcome := resultOutcome(killed)
	switch {
	case killOutcome == "uncertain" || killCode == "kill_unconfirmed":
		// Dispatched once, final evidence unknown: the frozen facts still apply
		// and the package entry never carries a source task.
		if killed.view.App == nil || killed.view.App.Kind != "kill" {
			p.reporter.fail("app_kill_package", "kill_facts_unexpected",
				field("outcome", killOutcome), field("code", killCode))
			return false
		}
		app := killed.view.App
		if app.Package != target || app.DisplayID == nil || *app.DisplayID != displayID ||
			app.SourceTaskID != nil {
			p.reporter.fail("app_kill_package", "package_kill_facts_unexpected",
				field("pkg", app.Package), field("display", integerString(app.DisplayID)),
				field("src_task", integerString(app.SourceTaskID)))
			return false
		}
		if failure := checkKillFacts(app); failure != nil {
			p.reporter.fail("app_kill_package", failure.reason, failure.fields...)
			return false
		}
		if !sameIntSet(app.TargetTaskIDs, expected) {
			p.reporter.fail("app_kill_package", "package_target_tasks_mismatch",
				field("target_tasks", intListText(app.TargetTaskIDs, app.HasTargetTaskIDs)),
				field("session_tasks", intListText(expected, true)))
			return false
		}
		p.reporter.pass("app_kill_package", append(killFactFields(app, false),
			"outcome=uncertain", "code="+killCode)...)
		return true
	case killed.result.IsError:
		if killOutcome == outcomeExecuted {
			p.reporter.fail("app_kill_package", "executed_result_marked_error")
			return false
		}
		if !appKillRefusalCode(killCode) {
			p.reporter.fail("app_kill_package", "unexpected_refusal", field("code", killCode))
			return false
		}
		if killed.view.App != nil {
			if killed.view.App.Kind != "kill" {
				p.reporter.fail("app_kill_package", "app_kind_mismatch",
					field("kind", killed.view.App.Kind))
				return false
			}
			if failure := checkNoFinalKillFacts(killed.view.App); failure != nil {
				p.reporter.fail("app_kill_package", failure.reason, failure.fields...)
				return false
			}
			if killed.view.App.SourceTaskID != nil {
				p.reporter.fail("app_kill_package", "package_entry_has_source_task")
				return false
			}
			// A pre-dispatch refusal still carries the frozen selection: with no
			// live session task after the close, the set must be omitted or empty.
			if killed.view.App.HasTargetTaskIDs {
				if failure := checkAscendingIDs(killed.view.App.TargetTaskIDs, "target_task_ids"); failure != nil {
					p.reporter.fail("app_kill_package", failure.reason, failure.fields...)
					return false
				}
				if len(expected) == 0 && len(killed.view.App.TargetTaskIDs) != 0 {
					p.reporter.fail("app_kill_package", "package_target_tasks_mismatch",
						field("target_tasks", intListText(killed.view.App.TargetTaskIDs, true)),
						field("session_tasks", intListText(expected, true)))
					return false
				}
			}
		}
		p.reporter.pass("app_kill_package",
			field("pkg", target), field("refused", killCode),
			"outcome="+outcomeOrNone(killOutcome))
		return true
	}
	if killOutcome != outcomeExecuted || killed.view.App == nil ||
		killed.view.App.Kind != "kill" {
		p.reporter.fail("app_kill_package", "kill_facts_unexpected",
			field("outcome", killOutcome), field("code", killCode))
		return false
	}
	app := killed.view.App
	if app.Package != target || app.DisplayID == nil || *app.DisplayID != displayID ||
		app.SourceTaskID != nil {
		p.reporter.fail("app_kill_package", "package_kill_facts_unexpected",
			field("pkg", app.Package), field("display", integerString(app.DisplayID)),
			field("src_task", integerString(app.SourceTaskID)))
		return false
	}
	if failure := checkKillFacts(app); failure != nil {
		p.reporter.fail("app_kill_package", failure.reason, failure.fields...)
		return false
	}
	if !sameIntSet(app.TargetTaskIDs, expected) {
		p.reporter.fail("app_kill_package", "package_target_tasks_mismatch",
			field("target_tasks", intListText(app.TargetTaskIDs, app.HasTargetTaskIDs)),
			field("session_tasks", intListText(expected, true)))
		return false
	}
	p.reporter.pass("app_kill_package", killFactFields(app, false)...)
	return true
}

// appPostObservation returns the fresh ready O published by one successful app
// mutation, or a controlled failure when the mandatory full post-observation is
// missing.
func appPostObservation(view resultView) (string, *checkError) {
	observation := view.Observation
	if observation == nil {
		return "", failf("app_post_observation_missing")
	}
	if observation.Status != observationComplete && observation.Status != observationPartial {
		return "", failf("app_post_observation_status", field("status", observation.Status))
	}
	if treeFormatOf(observation) != treeFull {
		return "", failf("app_post_observation_not_full", field("tree_format", treeFormatOf(observation)))
	}
	if !validReference(observation.ID, 'O') {
		return "", failf("app_post_observation_id_invalid")
	}
	return observation.ID, nil
}

// packageTaskIDs reads the controlled package's live session tasks through the
// read-only app.list projection. A truncated list cannot prove absence, so it
// is reported as a failure instead of a silently smaller set.
func (p *probe) packageTaskIDs(ctx context.Context, reference, target string) ([]int, *checkError) {
	list, failure := p.call(ctx, "app", map[string]any{
		"kind": "list", "reference": reference, "app": target,
	})
	if failure != nil {
		return nil, failure
	}
	if list.result.IsError || list.view.App == nil || list.view.App.Kind != "list" {
		return nil, failf("package_list_unavailable", field("code", codeOf(list.view)))
	}
	if !list.view.App.HasTaskItems {
		return nil, failf("package_list_items_missing")
	}
	if list.view.App.Truncated == nil {
		return nil, failf("package_list_truncated_missing")
	}
	if *list.view.App.Truncated {
		return nil, failf("package_list_truncated")
	}
	ids := make([]int, 0, len(list.view.App.TaskItems))
	for _, item := range list.view.App.TaskItems {
		if item.Package == target && item.TaskID > 0 {
			ids = append(ids, item.TaskID)
		}
	}
	sort.Ints(ids)
	return ids, nil
}

// closeControlledTask closes one live controlled task and reports the shared
// app_close pass line. It returns the post-close ready O when the close
// executed, and whether the close executed so the caller can rely on the
// close eligibility it establishes.
func (p *probe) closeControlledTask(ctx context.Context, reference string, taskID int) (string, bool, *checkError) {
	closed, failure := p.call(ctx, "app", map[string]any{
		"kind": "close", "reference": reference, "target": map[string]any{"task_id": taskID},
	})
	if failure != nil {
		return "", false, failure
	}
	if failure := checkCloseResult(closed, taskID); failure != nil {
		return "", false, failure
	}
	nextO := reference
	executed := resultOutcome(closed) == outcomeExecuted
	if executed {
		updated, failure := appPostObservation(closed.view)
		if failure != nil {
			return "", false, failure
		}
		nextO = updated
	}
	p.reporter.pass("app_close",
		field("task_id", strconv.Itoa(taskID)),
		field("outcome", resultOutcome(closed)),
		field("tasks", strconv.Itoa(len(closed.view.App.Tasks))),
		"is_error="+strconv.FormatBool(closed.result.IsError))
	return nextO, executed, nil
}

// readyObservation requests one fresh full observation through the session
// reference and returns its ready O. It consumes no app target and is safe to
// repeat before a new dangerous action.
func (p *probe) readyObservation(ctx context.Context, reference string) (string, *checkError) {
	projected, failure := p.call(ctx, "observe", map[string]any{
		"reference": reference, "full": true,
	})
	if failure != nil {
		return "", failure
	}
	if projected.result.IsError || projected.view.Observation == nil ||
		!validReference(projected.view.Observation.ID, 'O') {
		return "", failf("ready_observation_missing", field("code", codeOf(projected.view)))
	}
	if treeFormatOf(projected.view.Observation) != treeFull {
		return "", failf("ready_observation_not_full")
	}
	return projected.view.Observation.ID, nil
}

func appContainsTask(app *appView, taskID int, ownership string) bool {
	for _, item := range app.TaskItems {
		if item.TaskID == taskID && item.Ownership == ownership {
			return true
		}
	}
	return false
}

// checkCloseResult asserts the per-item close facts and the IsError rule:
// complete=false caused only by unconfirmed recents does not become IsError.
func checkCloseResult(closed *callResult, taskID int) *checkError {
	if closed.view.App == nil || closed.view.App.Kind != "close" {
		return failf("close_result_missing", field("code", codeOf(closed.view)))
	}
	app := closed.view.App
	if app.DisplayID == nil || !app.HasTasks || len(app.Tasks) == 0 || app.Complete == nil {
		return failf("close_facts_missing")
	}
	executed := resultOutcome(closed) == outcomeExecuted
	if closed.result.IsError == executed {
		return failf("close_is_error_mismatch", field("outcome", outcomeOrNone(resultOutcome(closed))))
	}
	found := false
	allExecuted := true
	allRecents := true
	for _, task := range app.Tasks {
		if task.TaskID <= 0 {
			return failf("close_task_id_invalid")
		}
		switch task.Outcome {
		case outcomeExecuted:
			if !task.HasRecentsCleaned {
				return failf("close_recents_missing", field("task_id", strconv.Itoa(task.TaskID)))
			}
			if task.RecentsCleaned == nil || !*task.RecentsCleaned {
				allRecents = false
			}
		case "not_executed", "uncertain":
			if task.HasRecentsCleaned {
				return failf("close_recents_not_applicable", field("task_id", strconv.Itoa(task.TaskID)))
			}
			allExecuted = false
			allRecents = false
		default:
			return failf("close_task_outcome_invalid", field("outcome", task.Outcome))
		}
		if task.TaskID == taskID {
			found = true
		}
	}
	if !found {
		return failf("close_target_absent", field("task_id", strconv.Itoa(taskID)))
	}
	if *app.Complete != (allExecuted && allRecents) {
		return failf("close_complete_mismatch", "complete="+boolString(app.Complete))
	}
	return nil
}

// appKillRefusalCode reports whether one kill refusal is a controlled service
// decision instead of an unexpected failure.
func appKillRefusalCode(code string) bool {
	if strings.HasPrefix(code, "kill_refused_") {
		return true
	}
	return oneOf(code, "task_not_found", "task_lifecycle_busy", "root_unavailable",
		"control_revoked")
}

// checkNoFinalKillFacts rejects process/task final-sampling facts on a kill that
// never dispatched. package_state appears only after dispatch.
func checkNoFinalKillFacts(app *appView) *checkError {
	if app.HasKilledPIDs || app.HasRemainingTaskIDs || app.HasRemainingPIDs || app.HasRestarted {
		return failf("not_executed_has_final_facts",
			field("killed_pids", intListText(app.KilledPIDs, app.HasKilledPIDs)),
			field("remaining_tasks", intListText(app.RemainingTaskIDs, app.HasRemainingTaskIDs)),
			field("remaining_pids", intListText(app.RemainingPIDs, app.HasRemainingPIDs)),
			field("restarted", boolString(app.Restarted)))
	}
	if app.PackageState != "" {
		return failf("not_executed_has_package_state", field("package_state", app.PackageState))
	}
	return nil
}

// checkKillFacts validates the dispatched kill facts: user, frozen and final
// task sets, pre-dispatch and final process sets, restarted, and the closed
// package state. A final read that stayed unknown keeps restarted explicitly
// null and omits the two final PID arrays; it may never be encoded as success.
func checkKillFacts(app *appView) *checkError {
	if app.AndroidUserID == nil {
		return failf("kill_android_user_missing")
	}
	if *app.AndroidUserID < 0 {
		return failf("kill_android_user_invalid", field("user", strconv.Itoa(*app.AndroidUserID)))
	}
	if !app.HasTargetTaskIDs {
		return failf("kill_target_tasks_missing")
	}
	if failure := checkAscendingIDs(app.TargetTaskIDs, "target_task_ids"); failure != nil {
		return failure
	}
	if app.HasRemainingTaskIDs {
		if failure := checkAscendingIDs(app.RemainingTaskIDs, "remaining_task_ids"); failure != nil {
			return failure
		}
	}
	if !appPackageStateSet[app.PackageState] {
		return failf("kill_package_state_invalid", field("package_state", app.PackageState))
	}
	// restarted always appears on a dispatched result: a boolean when the final
	// process read completed, an explicit null when it stayed unknown.
	if !app.HasRestarted {
		return failf("kill_restarted_missing")
	}
	if !app.HasTargetPIDs {
		return failf("kill_target_pids_missing")
	}
	if failure := checkAscendingIDs(app.TargetPIDs, "target_pids"); failure != nil {
		return failure
	}
	processKnown := app.Restarted != nil
	if processKnown != (app.HasKilledPIDs && app.HasRemainingPIDs) {
		// killed_pids and remaining_pids exist exactly when the final process
		// read completed.
		return failf("kill_process_facts_mismatch",
			field("restarted", boolString(app.Restarted)),
			field("killed_pids", presenceText(app.HasKilledPIDs)),
			field("remaining_pids", presenceText(app.HasRemainingPIDs)))
	}
	if processKnown {
		if failure := checkAscendingIDs(app.KilledPIDs, "killed_pids"); failure != nil {
			return failure
		}
		if failure := checkAscendingIDs(app.RemainingPIDs, "remaining_pids"); failure != nil {
			return failure
		}
		for _, pid := range app.KilledPIDs {
			if !containsInt(app.TargetPIDs, pid) {
				return failf("kill_killed_pid_not_original", field("pid", strconv.Itoa(pid)))
			}
		}
		for _, pid := range app.TargetPIDs {
			killed := containsInt(app.KilledPIDs, pid)
			remaining := containsInt(app.RemainingPIDs, pid)
			if killed == remaining {
				return failf("kill_pid_partition_invalid", field("pid", strconv.Itoa(pid)))
			}
		}
		newInstances := 0
		for _, pid := range app.RemainingPIDs {
			if !containsInt(app.TargetPIDs, pid) {
				newInstances++
			}
		}
		if *app.Restarted != (newInstances > 0) {
			return failf("kill_restarted_mismatch",
				field("restarted", boolString(app.Restarted)),
				field("new_pids", strconv.Itoa(newInstances)))
		}
	}
	switch app.PackageState {
	case "stopped_at_check":
		if !app.HasRemainingTaskIDs || len(app.RemainingTaskIDs) != 0 {
			return failf("kill_stopped_with_tasks",
				field("remaining_tasks", intListText(app.RemainingTaskIDs, app.HasRemainingTaskIDs)))
		}
		if !processKnown || len(app.RemainingPIDs) != 0 {
			return failf("kill_stopped_with_processes",
				field("remaining_pids", intListText(app.RemainingPIDs, app.HasRemainingPIDs)))
		}
	case "still_present":
		tasksPresent := app.HasRemainingTaskIDs && len(app.RemainingTaskIDs) > 0
		processesPresent := processKnown && len(app.RemainingPIDs) > 0
		if !tasksPresent && !processesPresent {
			return failf("kill_still_present_without_evidence",
				field("remaining_tasks", intListText(app.RemainingTaskIDs, app.HasRemainingTaskIDs)),
				field("remaining_pids", intListText(app.RemainingPIDs, app.HasRemainingPIDs)))
		}
	case "unknown":
		// Anything not proven empty and not proven present. A complete final
		// read that is empty on both sides would be stopped_at_check instead,
		// and one that proves a live task or process is still_present.
		if app.HasRemainingTaskIDs && len(app.RemainingTaskIDs) == 0 &&
			processKnown && len(app.RemainingPIDs) == 0 {
			return failf("kill_unknown_but_empty")
		}
		if app.HasRemainingTaskIDs && len(app.RemainingTaskIDs) > 0 {
			return failf("kill_unknown_with_tasks",
				field("remaining_tasks", intListText(app.RemainingTaskIDs, true)))
		}
		if processKnown && len(app.RemainingPIDs) > 0 {
			return failf("kill_unknown_with_processes",
				field("remaining_pids", intListText(app.RemainingPIDs, true)))
		}
	}
	return nil
}

func checkAscendingIDs(values []int, name string) *checkError {
	for index, value := range values {
		if value <= 0 {
			return failf("kill_ids_invalid", field("field", name),
				field("value", strconv.Itoa(value)))
		}
		if index > 0 && value <= values[index-1] {
			return failf("kill_ids_unsorted", field("field", name))
		}
	}
	return nil
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func sameIntSet(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	leftSorted := append([]int(nil), left...)
	rightSorted := append([]int(nil), right...)
	sort.Ints(leftSorted)
	sort.Ints(rightSorted)
	for index := range leftSorted {
		if leftSorted[index] != rightSorted[index] {
			return false
		}
	}
	return true
}

// killFactFields renders the verified kill facts in the public wire order for
// one reporter line: pkg, user, src_task (task entry only), target_tasks,
// display, remaining_tasks, target_pids, killed_pids, remaining_pids,
// restarted, package_state.
func killFactFields(app *appView, withSourceTask bool) []string {
	fields := []string{
		field("pkg", app.Package),
		field("user", integerString(app.AndroidUserID)),
	}
	if withSourceTask {
		fields = append(fields, field("src_task", integerString(app.SourceTaskID)))
	}
	return append(fields,
		field("target_tasks", intListText(app.TargetTaskIDs, app.HasTargetTaskIDs)),
		field("display", integerString(app.DisplayID)),
		field("remaining_tasks", intListText(app.RemainingTaskIDs, app.HasRemainingTaskIDs)),
		field("target_pids", intListText(app.TargetPIDs, app.HasTargetPIDs)),
		field("killed_pids", intListText(app.KilledPIDs, app.HasKilledPIDs)),
		field("remaining_pids", intListText(app.RemainingPIDs, app.HasRemainingPIDs)),
		field("restarted", boolString(app.Restarted)),
		field("package_state", app.PackageState),
	)
}

// intListText renders one optional integer array as a single reporter token so
// an omitted member, an empty array and a non-empty set stay distinguishable.
func intListText(values []int, present bool) string {
	if !present {
		return "unknown"
	}
	if len(values) == 0 {
		return "empty"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ",")
}

func presenceText(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}

func packageStateOf(view resultView) string {
	if view.App == nil {
		return "none"
	}
	return view.App.PackageState
}

func boolString(value *bool) string {
	if value == nil {
		return "null"
	}
	return strconv.FormatBool(*value)
}

func outcomeOrNone(outcome string) string {
	if outcome == "" {
		return "none"
	}
	return outcome
}
