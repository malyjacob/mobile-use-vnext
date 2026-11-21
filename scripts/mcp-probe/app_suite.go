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

	// A kill is attempted only on the controlled owned task. While its window is
	// foreground the service must refuse; if the service instead executes, the
	// closed loop simply loses that task and close is skipped.
	killed := false
	kill, failure := p.call(ctx, "app", map[string]any{
		"kind": "kill", "reference": nextO, "target": map[string]any{"task_id": taskID},
	})
	if failure != nil {
		p.reporter.fail("app_kill", failure.reason, failure.fields...)
		return false
	}
	if kill.result.IsError {
		code := codeOf(kill.view)
		if resultOutcome(kill) == outcomeExecuted {
			p.reporter.fail("app_kill_refused", "executed_result_marked_error")
			return false
		}
		if !strings.HasPrefix(code, "kill_refused_") && code != "task_not_found" {
			p.reporter.fail("app_kill_refused", "unexpected_refusal", field("code", code))
			return false
		}
		if kill.view.App != nil {
			if kill.view.App.Kind != "kill" {
				p.reporter.fail("app_kill_refused", "app_kind_mismatch", field("kind", kill.view.App.Kind))
				return false
			}
			if kill.view.App.HasKilledPIDs {
				p.reporter.fail("app_kill_refused", "not_executed_has_killed_pids")
				return false
			}
		}
		p.reporter.pass("app_kill_refused",
			field("task_id", strconv.Itoa(taskID)), field("code", code),
			"outcome="+outcomeOrNone(resultOutcome(kill)))
	} else {
		if resultOutcome(kill) != outcomeExecuted || kill.view.App == nil ||
			kill.view.App.Kind != "kill" || kill.view.App.SourceTaskID == nil ||
			*kill.view.App.SourceTaskID != taskID {
			p.reporter.fail("app_kill", "kill_facts_unexpected",
				field("outcome", resultOutcome(kill)), field("code", codeOf(kill.view)))
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
		killed = true
		p.reporter.pass("app_kill",
			field("task_id", strconv.Itoa(taskID)),
			field("target_pids", strconv.Itoa(len(kill.view.App.TargetPIDs))),
			field("killed_pids", strconv.Itoa(len(kill.view.App.KilledPIDs))),
			"restarted="+boolString(kill.view.App.Restarted))
	}

	if killed {
		p.reporter.skip("app_close", "controlled_task_killed")
	} else {
		closed, failure := p.call(ctx, "app", map[string]any{
			"kind": "close", "reference": nextO, "target": map[string]any{"task_id": taskID},
		})
		if failure != nil {
			p.reporter.fail("app_close", failure.reason, failure.fields...)
			return false
		}
		if failure := checkCloseResult(closed, taskID); failure != nil {
			p.reporter.fail("app_close", failure.reason, failure.fields...)
			return false
		}
		if resultOutcome(closed) == outcomeExecuted {
			updated, failure := appPostObservation(closed.view)
			if failure != nil {
				p.reporter.fail("app_close", failure.reason, failure.fields...)
				return false
			}
			nextO = updated
		}
		p.reporter.pass("app_close",
			field("task_id", strconv.Itoa(taskID)),
			field("outcome", resultOutcome(closed)),
			field("tasks", strconv.Itoa(len(closed.view.App.Tasks))),
			"is_error="+strconv.FormatBool(closed.result.IsError))
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

// checkKillFacts validates the executed kill arrays: sorted, present and
// consistent with the restarted nullable fact.
func checkKillFacts(app *appView) *checkError {
	if !app.HasTargetPIDs || !app.HasKilledPIDs || !app.HasRestarted {
		return failf("kill_facts_missing")
	}
	for name, values := range map[string][]int{
		"target_pids": app.TargetPIDs, "killed_pids": app.KilledPIDs,
		"remaining_pids": app.RemainingPIDs,
	} {
		if name == "remaining_pids" && !app.HasRemainingPIDs {
			continue
		}
		if !sort.IntsAreSorted(values) {
			return failf("kill_pids_unsorted", field("field", name))
		}
	}
	return nil
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
