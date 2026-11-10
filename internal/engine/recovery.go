package engine

import (
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// This file is the single source of every tool-result recovery projection. An
// engine path never writes an entry, reference, input state or cause literal:
// it only hands the facts it already owns to planRecovery, which is pure,
// total and O(1). Access-layer errors are planned by protocol.AccessRecovery
// instead; the two mappings never call each other.

// recoveryInput is the complete, side-effect-free input of planRecovery. The
// planner reads only these members: it never reads a Result, the device, the
// ledger or the clock.
type recoveryInput struct {
	Tool string
	// Reference is the kind of reference this request already used: "O", "P"
	// or "" (no reference). For status self it also states whether a start
	// operation ID exists ("P") or not ("none").
	Reference string
	Code      string
	Phase     string
	Outcome   string
	Claimed   bool
	// Delivered records that this result carries the observation a rejected act
	// published. When nothing was dispatched, it makes the next call an
	// immediate retry against the delivered O instead of a new acquisition.
	Delivered bool
	// InputState is the usability of the observation this request referenced.
	// It is not persisted; callers derive it from the facts they already hold.
	InputState string
	// Cleanup is the cleanup fact visible when the result is finished:
	// none / pending / unconfirmed / complete.
	Cleanup string
}

// recoveryCleanupNone is the recovery-input value for "no cleanup record".
const recoveryCleanupNone = "none"

// planRecovery maps one result's facts to its single recovery projection, or
// nil when the result carries no pending recovery fact. The dispatch order is
// the contract: tool-specific tables first, then session/observation/status/
// capture codes, then outcome/phase, then the zero-cost retry window.
//
// It is a variable only so the reverse-control test can prove that the engine
// derives the projection here and nowhere else. Production never assigns it.
var planRecovery = planRecoveryFor

func planRecoveryFor(in recoveryInput) *protocol.Recovery {
	switch in.Tool {
	case "diagnose":
		// diagnose never returns an error and has no advancing reference.
		return nil
	case "end":
		return endPlan(in)
	case "status":
		return statusPlan(in)
	case "start":
		return startPlan(in)
	}
	if in.Code == protocol.CodeContractMismatch {
		return newRecovery(protocol.RecoveryEntryUpdate, protocol.RecoveryReferenceNone, "", "")
	}
	switch in.Tool {
	case "apps", "notify":
		return discoveryPlan(in)
	case "shell":
		return shellPlan(in)
	}
	if isSessionEndCode(in.Code) {
		return newRecovery(protocol.RecoveryEntryEnd, protocol.RecoveryReferenceP, in.InputState, "")
	}
	if _, ok := observationDeadCodes[in.Code]; ok {
		return newRecovery(protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP, in.InputState, "")
	}
	if _, ok := statusCodes[in.Code]; ok {
		return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, in.InputState, "")
	}
	if _, ok := captureFailureCodes[in.Code]; ok {
		// Capture failures consume the observation for the two observation
		// consumers. The one pre-claim path (a static control-UI rejection) is
		// still "nothing happened" and falls through to the retry window.
		if in.Claimed || (in.Tool != "act" && in.Tool != "open_app") {
			return newRecovery(protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP, in.InputState, "")
		}
	}
	if in.Outcome == OutcomeUncertain {
		return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, in.InputState, "")
	}
	if in.Phase == "post_observation_failed" {
		return newRecovery(protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
			recoveryInputRetired(in.InputState), protocol.RecoveryCausePostObservationFailed)
	}
	if in.Phase == "executed_revoked" {
		// The session was revoked during the action: whatever the old O's
		// state was, its input authority is gone.
		return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP,
			protocol.RecoveryInputRevoked, protocol.RecoveryCauseExecutedRevoked)
	}
	if in.Delivered && in.Outcome == OutcomeNotExecuted {
		// The rejected act published the frame it had already read, so an
		// unclaimed, ready observation exists even though this operation claimed
		// one. Nothing was dispatched: retry the same tool against the delivered
		// O with a new P.
		return newRecovery(protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
			protocol.RecoveryInputEffective, "")
	}
	if in.Claimed && in.Code != "" {
		return newRecovery(protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP, in.InputState, "")
	}
	if in.Outcome == OutcomeNotExecuted && !in.Claimed &&
		(in.Tool == "act" || in.Tool == "open_app") {
		if _, ok := userCodes[in.Code]; ok {
			return newRecovery(protocol.RecoveryEntryUser, protocol.RecoveryReferenceNone,
				protocol.RecoveryInputEffective, "")
		}
		// Nothing was dispatched and the observation was never claimed: the
		// caller may retry the same tool against the same O with a new P.
		return newRecovery(protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
			protocol.RecoveryInputEffective, "")
	}
	return nil
}

// endPlan maps the terminal tool. Pending cleanup has a bounded advancing
// status step; unconfirmed cleanup has stopped and remains a reported fact.
func endPlan(in recoveryInput) *protocol.Recovery {
	if in.Phase == "closed_before_admission" {
		return nil
	}
	cleanupPending := in.Cleanup == protocol.CleanupPending
	if in.Code == "" || isSessionEndCode(in.Code) {
		if cleanupPending {
			return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP,
				protocol.RecoveryInputRevoked, protocol.RecoveryCauseCleanupPending)
		}
		return nil
	}
	switch in.Code {
	case protocol.CodeSessionNotFound, protocol.CodeReferenceNoSession:
		return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, "", "")
	}
	return nil
}

// statusPlan maps the three status views. The caller selects the view with
// Phase: self (no reference), observation (status(O)) or operation (status(P)).
func statusPlan(in recoveryInput) *protocol.Recovery {
	switch in.Phase {
	case "self":
		if in.Reference == protocol.RecoveryReferenceNone {
			return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceNone, "", "")
		}
		return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, "", "")
	case "running":
		return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, "",
			protocol.RecoveryCauseRunning)
	case "observation":
		switch in.Code {
		case protocol.CodeSessionEnded, protocol.CodeSessionExpired, protocol.CodeStopped:
			// The result has no P for this session, and end accepts an O.
			return newRecovery(protocol.RecoveryEntryEnd, protocol.RecoveryReferenceO,
				protocol.RecoveryInputRevoked, "")
		case protocol.CodeObservationExpired:
			return newRecovery(protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
				protocol.RecoveryInputExpired, "")
		case protocol.CodeObservationClaimed:
			return newRecovery(protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
				protocol.RecoveryInputClaimed, "")
		case protocol.CodeObservationRetired:
			return newRecovery(protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
				protocol.RecoveryInputRetired, "")
		}
		return nil
	case "operation":
		if in.Cleanup == protocol.CleanupPending {
			return newRecovery(protocol.RecoveryEntryStatus, in.Reference,
				protocol.RecoveryInputRevoked, protocol.RecoveryCauseCleanupPending)
		}
		return nil
	}
	return nil
}

// startPlan maps start, which never references an existing observation. The
// two first-observation branches differ only by whether the failure revoked
// the session: an active session recovers with observe(P), a revoked one is a
// closure/status fact.
func startPlan(in recoveryInput) *protocol.Recovery {
	if in.Code == "ui_input_reboot_required" {
		return newRecovery(protocol.RecoveryEntryUser, protocol.RecoveryReferenceNone, "", "")
	}
	switch in.Code {
	case "":
		if in.Phase != "first_observation_failed" {
			return nil
		}
		if in.InputState == protocol.RecoveryInputRevoked ||
			in.Cleanup == protocol.CleanupPending || in.Cleanup == protocol.CleanupUnconfirmed {
			return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP,
				protocol.RecoveryInputRevoked, protocol.RecoveryCauseCleanupPending)
		}
		return newRecovery(protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
			protocol.RecoveryInputRetired, protocol.RecoveryCausePostObservationFailed)
	case protocol.CodeSessionExists:
		return newRecovery(protocol.RecoveryEntryEnd, protocol.RecoveryReferenceP, "", "")
	case protocol.CodeDeviceBusy, protocol.CodeDeviceUnavailable,
		protocol.CodeRecovering, protocol.CodeCleanupUnconfirmed,
		"recovery_pending", "old_resource_unconfirmed", "control_sync_pending", "ui_input_unconfirmed",
		"no_display_recorded", "cleanup_pending", "shell_pending",
		"shell_chain_unconfirmed", "ledger_unavailable", "ledger_corrupt":
		return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, "", "")
	}
	if isSessionEndCode(in.Code) {
		if in.Cleanup == protocol.CleanupPending || in.Cleanup == protocol.CleanupUnconfirmed {
			// A revoked start is an error result: the live cleanup fact is a
			// status entry, never a cause (cause requires the absence of error).
			return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP,
				protocol.RecoveryInputRevoked, "")
		}
		return newRecovery(protocol.RecoveryEntryEnd, protocol.RecoveryReferenceP,
			protocol.RecoveryInputRevoked, "")
	}
	if _, ok := userCodes[in.Code]; ok {
		return newRecovery(protocol.RecoveryEntryUser, protocol.RecoveryReferenceNone, "", "")
	}
	if in.Code == protocol.CodeDisplayUnavailable {
		// start's display failure is a user/binding precondition, not a
		// capture failure of an existing observation.
		return newRecovery(protocol.RecoveryEntryUser, protocol.RecoveryReferenceNone, "", "")
	}
	return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, "", "")
}

// discoveryPlan maps the two session-free discovery tools. They never reuse an
// observation, so they cannot produce retry; a controlled failure is either a
// session-end fact or a transient/unavailable fact to poll.
func discoveryPlan(in recoveryInput) *protocol.Recovery {
	switch in.Code {
	case "", protocol.CodeInvalidRequest:
		return nil
	}
	if isSessionEndCode(in.Code) {
		return newRecovery(protocol.RecoveryEntryEnd, protocol.RecoveryReferenceP, "", "")
	}
	return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, "", "")
}

// shellPlan maps the managed shell tool. A shell command never reuses or
// consumes the ready observation and never retries: an unconfirmed execution
// chain must be confirmed through status first.
func shellPlan(in recoveryInput) *protocol.Recovery {
	switch in.Code {
	case "":
		if in.Phase == "executed_revoked" {
			return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP,
				protocol.RecoveryInputRevoked, protocol.RecoveryCauseExecutedRevoked)
		}
		return nil
	case protocol.CodeInvalidRequest:
		return nil
	}
	if isSessionEndCode(in.Code) {
		return newRecovery(protocol.RecoveryEntryEnd, protocol.RecoveryReferenceP,
			inputStateFor(shellTool, in.Reference, in.Code, in.Claimed), "")
	}
	if _, ok := observationDeadCodes[in.Code]; ok {
		return newRecovery(protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP, "", "")
	}
	if in.Code == protocol.CodeRootUnavailable {
		return newRecovery(protocol.RecoveryEntryUser, protocol.RecoveryReferenceNone, "", "")
	}
	return newRecovery(protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, "", "")
}

const shellTool = "shell"

// recoveryReplay drops a delivery promise a replayed view cannot keep. A view
// built from the operation record never carries the delivered observation, so
// retry would name an O the caller cannot resolve from that response; the honest
// next step is one new acquisition.
func recoveryReplay(r *protocol.Recovery) *protocol.Recovery {
	if r == nil || r.Entry != protocol.RecoveryEntryRetry {
		return r
	}
	return newRecovery(protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
		protocol.RecoveryInputRetired, "")
}

// referenceKind classifies an admitted reference by its first byte. The caller
// has already proven the reference is owned and well formed; this helper only
// records which kind it was for planning.
func referenceKind(reference string) string {
	if len(reference) == 0 {
		return ""
	}
	switch reference[0] {
	case 'O':
		return protocol.RecoveryReferenceO
	case 'P':
		return protocol.RecoveryReferenceP
	}
	return ""
}

// The four code tables. They are the closed vocabulary the engine can write;
// a code missing from all four falls to the default branches above and is
// exposed by the table-driven coverage test.
var (
	// observationDeadCodes: the referenced O can never be used again.
	observationDeadCodes = map[string]struct{}{
		protocol.CodeObservationGone:    {},
		protocol.CodeObservationRetired: {},
		protocol.CodeObservationClaimed: {},
		protocol.CodeObservationExpired: {},
		protocol.CodeNoReadyObservation: {},
		protocol.CodeContentUnavailable: {},
	}
	// statusCodes: the outcome or closure is not yet determined.
	statusCodes = map[string]struct{}{
		protocol.CodeDeviceBusy:         {},
		protocol.CodeRequestClosed:      {},
		protocol.CodeOperationUnknown:   {},
		protocol.CodeOperationNotFound:  {},
		protocol.CodeReferenceNoSession: {},
	}
	// captureFailureCodes: fresh evidence could not be acquired.
	captureFailureCodes = map[string]struct{}{
		protocol.CodeHierarchyFailed:    {},
		protocol.CodeDisplayUnavailable: {},
		protocol.CodeStaleScreen:        {},
		protocol.CodeTreeTooLarge:       {},
		protocol.CodeControlUI:          {},
	}
	// userCodes: no API call can succeed until a person changes the device.
	userCodes = map[string]struct{}{
		protocol.CodeDeviceLocked:       {},
		protocol.CodeDeviceNotAwake:     {},
		protocol.CodeStopUnavailable:    {},
		protocol.CodeModeNotSelected:    {},
		protocol.CodeControlUnavailable: {},
		protocol.CodeAppNotFound:        {},
		protocol.CodeRootUnavailable:    {},
		protocol.CodeDisplayCreate:      {},
	}
)

// isSessionEndCode reports the codes that state the original session is over,
// expired or revoked.
func isSessionEndCode(code string) bool {
	switch code {
	case protocol.CodeSessionEnded, protocol.CodeSessionExpired, protocol.CodeStopped:
		return true
	}
	return false
}

// inputStateFor derives the usability of the observation a request referenced.
// It is pure: the caller already owns the code, the claim fact and the session
// fact. An empty result means the result does not state that usability.
func inputStateFor(tool, reference, code string, claimed bool) string {
	switch code {
	case protocol.CodeObservationExpired:
		return protocol.RecoveryInputExpired
	case protocol.CodeObservationClaimed:
		return protocol.RecoveryInputClaimed
	case protocol.CodeObservationRetired, protocol.CodeContentUnavailable:
		return protocol.RecoveryInputRetired
	case protocol.CodeObservationGone:
		// The O is not resolvable at all; there is nothing to report.
		return ""
	}
	if isSessionEndCode(code) {
		if reference == protocol.RecoveryReferenceO || tool == "end" {
			return protocol.RecoveryInputRevoked
		}
		return ""
	}
	if tool == "act" || tool == "open_app" {
		if claimed {
			return protocol.RecoveryInputRetired
		}
		if code != "" {
			// A pre-claim rejection leaves the O ready and actionable.
			return protocol.RecoveryInputEffective
		}
	}
	return ""
}

// cleanupValue projects the internal cleanup record onto the planner input.
func cleanupValue(cleanup *cleanupState) string {
	if cleanup == nil {
		return recoveryCleanupNone
	}
	switch cleanup.State {
	case protocol.CleanupPending, protocol.CleanupUnconfirmed, protocol.CleanupComplete:
		return cleanup.State
	}
	return cleanup.State
}

// recoveryInputRetired keeps an explicit state but never leaves a degraded
// success without the observation-usability fact it implies.
func recoveryInputRetired(state string) string {
	if state != "" {
		return state
	}
	return protocol.RecoveryInputRetired
}

// newRecovery builds one projection value. Only planRecovery and its sub-plans
// call it, so no other file constructs a recovery literal.
func newRecovery(entry, reference, inputState, cause string) *protocol.Recovery {
	return &protocol.Recovery{Entry: entry, Reference: reference, InputState: inputState, Cause: cause}
}
