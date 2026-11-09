package engine

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// recovery test helpers shared by the tool tests. They pin the whole object,
// not just the entry: an entry without its reference and input state is not a
// usable next step.

func recoveryOf(result *protocol.Result) *protocol.Recovery {
	if result == nil {
		return nil
	}
	return result.Recovery
}

func recoveryIs(r *protocol.Recovery, entry, reference, inputState, cause string) bool {
	return r != nil && r.Entry == entry && r.Reference == reference &&
		r.InputState == inputState && r.Cause == cause
}

func requireNoRecovery(t *testing.T, result *protocol.Result) {
	t.Helper()
	if r := recoveryOf(result); r != nil {
		t.Fatalf("recovery = %+v, want omitted", r)
	}
}

func requireRecovery(t *testing.T, result *protocol.Result, entry, reference, inputState, cause string) {
	t.Helper()
	r := recoveryOf(result)
	if r == nil {
		t.Fatalf("recovery = nil, want entry=%s reference=%s input_state=%q cause=%q",
			entry, reference, inputState, cause)
	}
	if !recoveryIs(r, entry, reference, inputState, cause) {
		t.Fatalf("recovery = %+v, want entry=%s reference=%s input_state=%q cause=%q",
			r, entry, reference, inputState, cause)
	}
}

func wantRecovery(entry, reference, inputState, cause string) *protocol.Recovery {
	return &protocol.Recovery{Entry: entry, Reference: reference, InputState: inputState, Cause: cause}
}

// planCase is one fact combination that a former `next` write point could
// produce, together with its complete expected projection.
type planCase struct {
	name string
	in   recoveryInput
	want *protocol.Recovery
}

// recoveryCases enumerates every tool path that used to write `next`, by fact
// combination rather than by code constant: the same code on a different path
// (pre-claim vs post-claim, O vs P reference) has a different next step.
func recoveryCases() []planCase {
	const (
		o    = protocol.RecoveryReferenceO
		p    = protocol.RecoveryReferenceP
		none = protocol.RecoveryReferenceNone

		eff     = protocol.RecoveryInputEffective
		expired = protocol.RecoveryInputExpired
		revoked = protocol.RecoveryInputRevoked
		claimed = protocol.RecoveryInputClaimed
		retired = protocol.RecoveryInputRetired

		postObs     = protocol.RecoveryCausePostObservationFailed
		execRevoked = protocol.RecoveryCauseExecutedRevoked
		cleanup     = protocol.RecoveryCauseCleanupPending
		running     = protocol.RecoveryCauseRunning
	)
	notExecuted := OutcomeNotExecuted
	uncertain := OutcomeUncertain
	executed := OutcomeExecuted

	act := func(code, phase, outcome string, isClaimed bool, input string) recoveryInput {
		return recoveryInput{Tool: "act", Reference: o, Code: code, Phase: phase,
			Outcome: outcome, Claimed: isClaimed, InputState: input}
	}
	openApp := func(code, phase, outcome string, isClaimed bool, input string) recoveryInput {
		return recoveryInput{Tool: "open_app", Reference: o, Code: code, Phase: phase,
			Outcome: outcome, Claimed: isClaimed, InputState: input}
	}

	return []planCase{
		// act: session lifecycle.
		{"act session ended", act(protocol.CodeSessionEnded, "admission", notExecuted, false, revoked),
			wantRecovery(protocol.RecoveryEntryEnd, p, revoked, "")},
		{"act session expired", act(protocol.CodeSessionExpired, "admission", notExecuted, false, revoked),
			wantRecovery(protocol.RecoveryEntryEnd, p, revoked, "")},
		{"act claim revoked", act(protocol.CodeStopped, "claim", notExecuted, false, revoked),
			wantRecovery(protocol.RecoveryEntryEnd, p, revoked, "")},
		// act: the observation this request referenced is unknown or dead.
		{"act observation not found", act(protocol.CodeObservationGone, "admission", notExecuted, false, ""),
			wantRecovery(protocol.RecoveryEntryObserve, p, "", "")},
		{"act observation retired", act(protocol.CodeObservationRetired, "admission", notExecuted, false, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"act observation claimed", act(protocol.CodeObservationClaimed, "admission", notExecuted, false, claimed),
			wantRecovery(protocol.RecoveryEntryObserve, p, claimed, "")},
		{"act observation expired", act(protocol.CodeObservationExpired, "admission", notExecuted, false, expired),
			wantRecovery(protocol.RecoveryEntryObserve, p, expired, "")},
		{"act no ready observation", act(protocol.CodeNoReadyObservation, "admission", notExecuted, false, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		// act: static rejections. Nothing was claimed or dispatched.
		{"act node not found static", act(protocol.CodeNodeNotFound, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryRetry, o, eff, "")},
		{"act node disabled", act(protocol.CodeNodeDisabled, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryRetry, o, eff, "")},
		{"act unsupported node action", act(protocol.CodeNodeUnsupported, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryRetry, o, eff, "")},
		{"act invalid coordinates", act(protocol.CodeInvalidCoordinates, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryRetry, o, eff, "")},
		{"act ambiguous point", act(protocol.CodeAmbiguousPoint, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryRetry, o, eff, "")},
		{"act unsupported key", act(protocol.CodeInvalidKey, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryRetry, o, eff, "")},
		{"act invalid request static", act(protocol.CodeInvalidRequest, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryRetry, o, eff, "")},
		{"act text target static", act(protocol.CodeTextTargetUnknown, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryRetry, o, eff, "")},
		// act: device busy before the claim still leaves the O usable.
		{"act device busy", act(protocol.CodeDeviceBusy, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryStatus, p, eff, "")},
		// act: post-claim capture and freshness failures.
		{"act display unavailable", act(protocol.CodeDisplayUnavailable, "revalidate", notExecuted, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"act hierarchy unavailable", act(protocol.CodeHierarchyFailed, "revalidate", notExecuted, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"act stale screen", act(protocol.CodeStaleScreen, "revalidate", notExecuted, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"act tree too large", act(protocol.CodeTreeTooLarge, "revalidate", notExecuted, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"act control ui", act(protocol.CodeControlUI, "revalidate", notExecuted, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"act target changed", act(protocol.CodeTargetChanged, "revalidate", notExecuted, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"act foreground changed", act(protocol.CodeForegroundChanged, "revalidate", notExecuted, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"act foreground unknown", act(protocol.CodeForegroundUnknown, "revalidate", notExecuted, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"act node not found post claim", act(protocol.CodeNodeNotFound, "revalidate", notExecuted, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"act unsupported operation post claim", act(protocol.CodeInvalidRequest, "dispatch", notExecuted, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		// act: dispatched but unconfirmed.
		{"act action uncertain", act(protocol.CodeActionUnknown, "dispatch", uncertain, true, retired),
			wantRecovery(protocol.RecoveryEntryStatus, p, retired, "")},
		{"act text target uncertain", act(protocol.CodeTextTargetUnknown, "dispatch", uncertain, true, retired),
			wantRecovery(protocol.RecoveryEntryStatus, p, retired, "")},
		{"act text result uncertain", act(protocol.CodeTextResultUnknown, "dispatch", uncertain, true, retired),
			wantRecovery(protocol.RecoveryEntryStatus, p, retired, "")},
		// act: degraded successes.
		{"act post observation failed", act("", "post_observation_failed", executed, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, postObs)},
		{"act executed revoked", act("", "executed_revoked", executed, true, retired),
			wantRecovery(protocol.RecoveryEntryStatus, p, revoked, execRevoked)},
		{"act success", act("", "finished", executed, true, ""), nil},

		// open_app: pre-claim rejections.
		{"open_app missing app", openApp(protocol.CodeInvalidRequest, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryRetry, o, eff, "")},
		{"open_app launcher unresolved", openApp(protocol.CodeDeviceUnavailable, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryRetry, o, eff, "")},
		{"open_app task list unreadable", openApp(protocol.CodeDeviceUnavailable, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryRetry, o, eff, "")},
		{"open_app app not found", openApp(protocol.CodeAppNotFound, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryUser, none, eff, "")},
		{"open_app control ui static", openApp(protocol.CodeControlUI, "admission", notExecuted, false, eff),
			wantRecovery(protocol.RecoveryEntryRetry, o, eff, "")},
		// open_app: post-claim stages.
		{"open_app display unavailable", openApp(protocol.CodeDisplayUnavailable, "revalidate", notExecuted, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"open_app launch failed", openApp(protocol.CodeAppLaunchFailed, "dispatch", notExecuted, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"open_app task focus uncertain", openApp(protocol.CodeTaskFocus, "dispatch", uncertain, true, retired),
			wantRecovery(protocol.RecoveryEntryStatus, p, retired, "")},
		{"open_app launch uncertain", openApp(protocol.CodeAppLaunchUnknown, "dispatch", uncertain, true, retired),
			wantRecovery(protocol.RecoveryEntryStatus, p, retired, "")},
		{"open_app moved", openApp(protocol.CodeAppMoved, "dispatch", uncertain, true, retired),
			wantRecovery(protocol.RecoveryEntryStatus, p, retired, "")},
		{"open_app post observation failed", openApp("", "post_observation_failed", executed, true, retired),
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, postObs)},
		{"open_app executed revoked", openApp("", "executed_revoked", executed, true, retired),
			wantRecovery(protocol.RecoveryEntryStatus, p, revoked, execRevoked)},
		{"open_app success", openApp("", "finished", executed, true, ""), nil},

		// observe: the O is read, never claimed.
		{"observe session ended", recoveryInput{Tool: "observe", Reference: o, Code: protocol.CodeSessionEnded,
			InputState: revoked}, wantRecovery(protocol.RecoveryEntryEnd, p, revoked, "")},
		{"observe session ended P", recoveryInput{Tool: "observe", Reference: p, Code: protocol.CodeSessionEnded},
			wantRecovery(protocol.RecoveryEntryEnd, p, "", "")},
		{"observe request closed", recoveryInput{Tool: "observe", Reference: p, Code: protocol.CodeRequestClosed},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"observe operation unknown", recoveryInput{Tool: "observe", Reference: p, Code: protocol.CodeOperationUnknown},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"observe reference no session", recoveryInput{Tool: "observe", Reference: p, Code: protocol.CodeReferenceNoSession},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"observe observation not found", recoveryInput{Tool: "observe", Reference: o, Code: protocol.CodeObservationGone},
			wantRecovery(protocol.RecoveryEntryObserve, p, "", "")},
		{"observe hierarchy unavailable", recoveryInput{Tool: "observe", Reference: o, Code: protocol.CodeHierarchyFailed},
			wantRecovery(protocol.RecoveryEntryObserve, p, "", "")},
		{"observe display unavailable", recoveryInput{Tool: "observe", Reference: o, Code: protocol.CodeDisplayUnavailable},
			wantRecovery(protocol.RecoveryEntryObserve, p, "", "")},
		{"observe stale screen", recoveryInput{Tool: "observe", Reference: o, Code: protocol.CodeStaleScreen},
			wantRecovery(protocol.RecoveryEntryObserve, p, "", "")},
		{"observe tree too large", recoveryInput{Tool: "observe", Reference: o, Code: protocol.CodeTreeTooLarge},
			wantRecovery(protocol.RecoveryEntryObserve, p, "", "")},
		{"observe content unavailable", recoveryInput{Tool: "observe", Reference: o, Code: protocol.CodeContentUnavailable,
			InputState: retired}, wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"observe success", recoveryInput{Tool: "observe", Reference: o, Phase: "finished"}, nil},
		{"inspect oversized", recoveryInput{Tool: "inspect", Reference: o, Code: protocol.CodeInspectionTooLarge}, nil},

		// start: lifecycle and admission.
		{"start session exists", recoveryInput{Tool: "start", Code: protocol.CodeSessionExists},
			wantRecovery(protocol.RecoveryEntryEnd, p, "", "")},
		{"start display create failed", recoveryInput{Tool: "start", Code: protocol.CodeDisplayCreate, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryUser, none, "", "")},
		{"start mode not selected", recoveryInput{Tool: "start", Code: protocol.CodeModeNotSelected, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryUser, none, "", "")},
		{"start control unavailable", recoveryInput{Tool: "start", Code: protocol.CodeControlUnavailable, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryUser, none, "", "")},
		{"start device locked", recoveryInput{Tool: "start", Code: protocol.CodeDeviceLocked, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryUser, none, "", "")},
		{"start device not interactive", recoveryInput{Tool: "start", Code: protocol.CodeDeviceNotAwake, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryUser, none, "", "")},
		{"start display unavailable", recoveryInput{Tool: "start", Code: protocol.CodeDisplayUnavailable, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryUser, none, "", "")},
		{"start device busy", recoveryInput{Tool: "start", Code: protocol.CodeDeviceBusy, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"start recovering", recoveryInput{Tool: "start", Code: protocol.CodeRecovering, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"start cleanup unconfirmed", recoveryInput{Tool: "start", Code: protocol.CodeCleanupUnconfirmed, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"start ledger unavailable", recoveryInput{Tool: "start", Code: "ledger_unavailable", Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"start first observation active", recoveryInput{Tool: "start", Phase: "first_observation_failed",
			Outcome: executed, InputState: retired}, wantRecovery(protocol.RecoveryEntryObserve, p, retired, postObs)},
		{"start first observation revoked", recoveryInput{Tool: "start", Phase: "first_observation_failed",
			Outcome: executed, InputState: revoked}, wantRecovery(protocol.RecoveryEntryStatus, p, revoked, cleanup)},
		{"start revoked cleanup pending", recoveryInput{Tool: "start", Code: protocol.CodeStopped, Outcome: notExecuted,
			InputState: revoked, Cleanup: protocol.CleanupPending}, wantRecovery(protocol.RecoveryEntryStatus, p, revoked, "")},
		{"start revoked cleanup complete", recoveryInput{Tool: "start", Code: protocol.CodeStopped, Outcome: notExecuted,
			InputState: revoked, Cleanup: protocol.CleanupComplete}, wantRecovery(protocol.RecoveryEntryEnd, p, revoked, "")},
		{"start success", recoveryInput{Tool: "start", Phase: "active", Outcome: executed}, nil},

		// end: terminal tool; cleanup progress is the only next step.
		{"end cleanup pending", recoveryInput{Tool: "end", Phase: "ended", Outcome: executed,
			Cleanup: protocol.CleanupPending}, wantRecovery(protocol.RecoveryEntryStatus, p, revoked, cleanup)},
		{"end cleanup unconfirmed", recoveryInput{Tool: "end", Phase: "ended", Outcome: executed,
			Cleanup: protocol.CleanupUnconfirmed}, wantRecovery(protocol.RecoveryEntryStatus, p, revoked, cleanup)},
		{"end cleanup complete", recoveryInput{Tool: "end", Phase: "ended", Outcome: executed,
			Cleanup: protocol.CleanupComplete}, nil},
		{"end closed before admission", recoveryInput{Tool: "end", Phase: "closed_before_admission",
			Outcome: executed}, nil},
		{"end session not found", recoveryInput{Tool: "end", Code: protocol.CodeSessionNotFound,
			Outcome: notExecuted}, wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"end reference no session", recoveryInput{Tool: "end", Code: protocol.CodeReferenceNoSession,
			Outcome: notExecuted}, wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"end observation not found", recoveryInput{Tool: "end", Code: protocol.CodeObservationGone,
			Outcome: notExecuted}, nil},
		{"end operation not found", recoveryInput{Tool: "end", Code: protocol.CodeOperationNotFound,
			Outcome: notExecuted}, nil},
		{"end invalid request", recoveryInput{Tool: "end", Code: protocol.CodeInvalidRequest,
			Outcome: notExecuted}, nil},

		// status: three views.
		{"status self with P", recoveryInput{Tool: "status", Reference: p, Phase: "self",
			Code: protocol.CodeRecovering}, wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"status self without P", recoveryInput{Tool: "status", Reference: none, Phase: "self",
			Code: protocol.CodeRecovering}, wantRecovery(protocol.RecoveryEntryStatus, none, "", "")},
		{"status observation ended", recoveryInput{Tool: "status", Reference: o, Phase: "observation",
			Code: protocol.CodeSessionEnded, InputState: revoked}, wantRecovery(protocol.RecoveryEntryEnd, o, revoked, "")},
		{"status observation expired", recoveryInput{Tool: "status", Reference: o, Phase: "observation",
			Code: protocol.CodeObservationExpired, InputState: expired},
			wantRecovery(protocol.RecoveryEntryObserve, p, expired, "")},
		{"status observation claimed", recoveryInput{Tool: "status", Reference: o, Phase: "observation",
			Code: protocol.CodeObservationClaimed, InputState: claimed},
			wantRecovery(protocol.RecoveryEntryObserve, p, claimed, "")},
		{"status observation retired", recoveryInput{Tool: "status", Reference: o, Phase: "observation",
			Code: protocol.CodeObservationRetired, InputState: retired},
			wantRecovery(protocol.RecoveryEntryObserve, p, retired, "")},
		{"status observation effective", recoveryInput{Tool: "status", Reference: o, Phase: "observation"},
			nil},
		{"status operation running", recoveryInput{Tool: "status", Reference: p, Phase: "running"},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", running)},
		{"status operation cleanup pending", recoveryInput{Tool: "status", Reference: p, Phase: "operation",
			Cleanup: protocol.CleanupPending}, wantRecovery(protocol.RecoveryEntryStatus, p, revoked, cleanup)},
		{"status operation finished", recoveryInput{Tool: "status", Reference: p, Phase: "operation",
			Cleanup: protocol.CleanupComplete}, nil},

		// keepalive.
		{"keepalive reference no session", recoveryInput{Tool: "keepalive", Reference: p,
			Code: protocol.CodeReferenceNoSession, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"keepalive operation unknown", recoveryInput{Tool: "keepalive", Reference: p,
			Code: protocol.CodeOperationUnknown, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"keepalive observation not found", recoveryInput{Tool: "keepalive", Reference: o,
			Code: protocol.CodeObservationGone, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryObserve, p, "", "")},
		{"keepalive session ended", recoveryInput{Tool: "keepalive", Reference: p,
			Code: protocol.CodeSessionEnded, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryEnd, p, "", "")},
		{"keepalive session ended O", recoveryInput{Tool: "keepalive", Reference: o,
			Code: protocol.CodeSessionEnded, Outcome: notExecuted, InputState: revoked},
			wantRecovery(protocol.RecoveryEntryEnd, p, revoked, "")},

		// apps / notify: session-free discovery.
		{"apps device busy", recoveryInput{Tool: "apps", Code: protocol.CodeDeviceBusy},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"apps device unavailable", recoveryInput{Tool: "apps", Code: protocol.CodeDeviceUnavailable},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"apps list unavailable", recoveryInput{Tool: "apps", Code: protocol.CodeAppListUnavailable},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"apps control revoked", recoveryInput{Tool: "apps", Code: protocol.CodeStopped},
			wantRecovery(protocol.RecoveryEntryEnd, p, "", "")},
		{"apps invalid request", recoveryInput{Tool: "apps", Code: protocol.CodeInvalidRequest}, nil},
		{"apps success", recoveryInput{Tool: "apps", Phase: "finished"}, nil},
		{"notify operation unknown", recoveryInput{Tool: "notify", Code: protocol.CodeOperationUnknown},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"notify device unavailable", recoveryInput{Tool: "notify", Code: protocol.CodeDeviceUnavailable},
			wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"notify invalid request", recoveryInput{Tool: "notify", Code: protocol.CodeInvalidRequest}, nil},
		{"notify success", recoveryInput{Tool: "notify", Phase: "saved", Outcome: executed}, nil},

		// shell: no retry by design.
		{"shell root unavailable", recoveryInput{Tool: "shell", Reference: o, Code: protocol.CodeRootUnavailable,
			Outcome: notExecuted}, wantRecovery(protocol.RecoveryEntryUser, none, "", "")},
		{"shell control revoked O", recoveryInput{Tool: "shell", Reference: o, Code: protocol.CodeStopped,
			Outcome: notExecuted, InputState: revoked}, wantRecovery(protocol.RecoveryEntryEnd, p, revoked, "")},
		{"shell control revoked P", recoveryInput{Tool: "shell", Reference: p, Code: protocol.CodeStopped,
			Outcome: notExecuted}, wantRecovery(protocol.RecoveryEntryEnd, p, "", "")},
		{"shell observation not found", recoveryInput{Tool: "shell", Reference: o,
			Code: protocol.CodeObservationGone, Outcome: notExecuted},
			wantRecovery(protocol.RecoveryEntryObserve, p, "", "")},
		{"shell start failed", recoveryInput{Tool: "shell", Reference: o, Code: protocol.CodeShellStartFailed,
			Outcome: notExecuted}, wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"shell result unconfirmed", recoveryInput{Tool: "shell", Reference: o, Code: protocol.CodeShellUnknown,
			Outcome: uncertain}, wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"shell device unavailable", recoveryInput{Tool: "shell", Reference: o, Code: protocol.CodeDeviceUnavailable,
			Outcome: notExecuted}, wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"shell request closed", recoveryInput{Tool: "shell", Reference: p, Code: protocol.CodeRequestClosed,
			Outcome: notExecuted}, wantRecovery(protocol.RecoveryEntryStatus, p, "", "")},
		{"shell invalid request", recoveryInput{Tool: "shell", Reference: o, Code: protocol.CodeInvalidRequest,
			Outcome: notExecuted}, nil},
		{"shell success", recoveryInput{Tool: "shell", Reference: o, Phase: "finished", Outcome: executed}, nil},
		{"shell revoked success", recoveryInput{Tool: "shell", Reference: o, Phase: "executed_revoked",
			Outcome: executed}, wantRecovery(protocol.RecoveryEntryStatus, p, revoked, execRevoked)},

		// diagnose and contract mismatch.
		{"diagnose any", recoveryInput{Tool: "diagnose", Code: protocol.CodeHierarchyFailed}, nil},
		{"contract mismatch", recoveryInput{Tool: "act", Reference: o, Code: protocol.CodeContractMismatch},
			wantRecovery(protocol.RecoveryEntryUpdate, none, "", "")},
	}
}

// TestRecoveryCoversEveryWritePoint is T1: every fact combination that used to
// write `next`, including the same code on different paths, has exactly one
// deterministic projection or an explicit omission.
func TestRecoveryCoversEveryWritePoint(t *testing.T) {
	for _, table := range []struct {
		name string
		keys map[string]struct{}
	}{
		{"observationDeadCodes", observationDeadCodes},
		{"statusCodes", statusCodes},
		{"captureFailureCodes", captureFailureCodes},
		{"userCodes", userCodes},
	} {
		if len(table.keys) == 0 {
			t.Fatalf("%s is empty", table.name)
		}
		for code := range table.keys {
			if strings.TrimSpace(code) != code || code == "" {
				t.Fatalf("%s contains an invalid code %q", table.name, code)
			}
		}
	}
	for _, test := range recoveryCases() {
		test := test
		t.Run(test.name, func(t *testing.T) {
			got := planRecovery(test.in)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("planRecovery(%+v) = %+v, want %+v", test.in, got, test.want)
			}
		})
	}
	// The access layer is a separate, closed mapping.
	for _, code := range []string{
		protocol.CodeInvalidRequest, protocol.CodeUnauthorized, protocol.CodeForbidden,
		protocol.CodeDeviceBusy, protocol.CodeSessionEnded,
	} {
		if got := protocol.AccessRecovery(code); got != nil {
			t.Fatalf("AccessRecovery(%s) = %+v, want omitted", code, got)
		}
	}
	if got := protocol.AccessRecovery(protocol.CodeContractMismatch); !recoveryIs(got,
		protocol.RecoveryEntryUpdate, protocol.RecoveryReferenceNone, "", "") {
		t.Fatalf("AccessRecovery(contract_mismatch) = %+v", got)
	}
}

// TestRecoveryInvariants is T8: I1-I9 hold for every case above.
func TestRecoveryInvariants(t *testing.T) {
	entries := map[string]bool{
		protocol.RecoveryEntryRetry: true, protocol.RecoveryEntryObserve: true,
		protocol.RecoveryEntryStatus: true, protocol.RecoveryEntryEnd: true,
		protocol.RecoveryEntryUser: true, protocol.RecoveryEntryUpdate: true,
	}
	references := map[string]bool{
		protocol.RecoveryReferenceP: true, protocol.RecoveryReferenceO: true,
		protocol.RecoveryReferenceNone: true,
	}
	for _, test := range recoveryCases() {
		r := planRecovery(test.in)
		if r == nil {
			continue
		}
		name := test.name
		// I1 and I2.
		if !entries[r.Entry] {
			t.Fatalf("%s: I1 entry %q", name, r.Entry)
		}
		if !references[r.Reference] {
			t.Fatalf("%s: I2 reference %q", name, r.Reference)
		}
		switch r.Entry {
		case protocol.RecoveryEntryRetry:
			// I3: retry requires an unclaimed, still usable O.
			if r.Reference != protocol.RecoveryReferenceO || r.InputState != protocol.RecoveryInputEffective {
				t.Fatalf("%s: I3 %+v", name, r)
			}
		case protocol.RecoveryEntryObserve:
			// I4: observe always names the next P and never claims the old O.
			if r.Reference != protocol.RecoveryReferenceP {
				t.Fatalf("%s: I4 reference %+v", name, r)
			}
			if r.InputState == protocol.RecoveryInputEffective {
				t.Fatalf("%s: I4 effective input with observe %+v", name, r)
			}
		case protocol.RecoveryEntryEnd:
			// I5: end names P, except the status(O) revocation branch.
			if r.Reference != protocol.RecoveryReferenceP && r.Reference != protocol.RecoveryReferenceO {
				t.Fatalf("%s: I5 reference %+v", name, r)
			}
			if r.Reference == protocol.RecoveryReferenceO &&
				r.InputState != protocol.RecoveryInputRevoked {
				t.Fatalf("%s: I5 O reference without revoked %+v", name, r)
			}
		case protocol.RecoveryEntryUser, protocol.RecoveryEntryUpdate:
			// I6.
			if r.Reference != protocol.RecoveryReferenceNone {
				t.Fatalf("%s: I6 %+v", name, r)
			}
		}
		// I7: a cause never coexists with an error.
		if r.Cause != "" && test.in.Code != "" {
			t.Fatalf("%s: I7 cause with code %q: %+v", name, test.in.Code, r)
		}
		// I8: the same input maps to the same output.
		if again := planRecovery(test.in); !reflect.DeepEqual(r, again) {
			t.Fatalf("%s: I8 %+v != %+v", name, r, again)
		}
	}
}

// TestRecoveryRetryKeepsObservation is T2: a static rejection says retry with
// the same O and a new P, and that retry actually succeeds.
func TestRecoveryRetryKeepsObservation(t *testing.T) {
	h := newHarness(t)
	h.device.scriptCaptures(overlayCaptureStep())
	start := h.startSession()
	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	requireNotExecuted(t, result, ambiguousPointCode)
	requireRecovery(t, result, protocol.RecoveryEntryRetry, protocol.RecoveryReferenceO,
		protocol.RecoveryInputEffective, "")
	if status := observationStatus(h, start.Observation.ID); status != ObservationReady {
		t.Fatalf("observation status = %q, want ready", status)
	}

	h.device.scriptCaptures()
	retry, apiErr := h.engine.Act(context.Background(), "agent", "Pretry", &protocol.ActRequest{
		ObservationID: start.Observation.ID,
		Operation:     protocol.Operation{Type: "key", Key: "BACK"},
	})
	if apiErr != nil {
		t.Fatalf("retry rejected: %v", apiErr)
	}
	if retry.Outcome != OutcomeExecuted {
		t.Fatalf("retry outcome = %q, want executed", retry.Outcome)
	}
	requireNoRecovery(t, retry)
}

// TestRecoveryObserveAfterClaim is T3: a post-claim target change consumes the
// O and points at a fresh observe(P).
func TestRecoveryObserveAfterClaim(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	button := findRole(currentTree(t, h.engine).Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("button not found in tree")
	}
	h.device.scriptCaptures(movedButtonCaptureStep())
	result, apiErr := h.engine.Act(context.Background(), "agent", "Ptarget", &protocol.ActRequest{
		ObservationID: start.Observation.ID,
		Operation: protocol.Operation{Type: "tap",
			Target: &protocol.Target{UseRef: true, Ref: button.Ref}},
	})
	if apiErr != nil {
		t.Fatalf("act rejected: %v", apiErr)
	}
	requireNotExecuted(t, result, protocol.CodeTargetChanged)
	requireRecovery(t, result, protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
		protocol.RecoveryInputRetired, "")
	if status := observationStatus(h, start.Observation.ID); status != ObservationRetired {
		t.Fatalf("claimed observation status = %q, want retired", status)
	}
}

// TestRecoveryPostObservationFailed is T4: the action stays executed and the
// degraded success carries the machine-readable cause.
func TestRecoveryPostObservationFailed(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	h.engine.cfg.MaxFullTreeBytes = 16
	request := tapButtonRequest(t, h, start.Observation.ID)
	result, apiErr := h.engine.Act(context.Background(), "agent", "", request)
	if apiErr != nil {
		t.Fatalf("act rejected: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("outcome = %q, want executed", result.Outcome)
	}
	requireRecovery(t, result, protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
		protocol.RecoveryInputRetired, protocol.RecoveryCausePostObservationFailed)
}

// TestRecoveryUncertainIsStatus is T5: every unconfirmed dispatch points at
// status, never at retry.
func TestRecoveryUncertainIsStatus(t *testing.T) {
	for _, code := range []string{
		protocol.CodeActionUnknown, protocol.CodeTextTargetUnknown, protocol.CodeTextResultUnknown,
	} {
		got := planRecovery(recoveryInput{Tool: "act", Reference: protocol.RecoveryReferenceO,
			Code: code, Outcome: OutcomeUncertain, Claimed: true,
			InputState: protocol.RecoveryInputRetired})
		if !recoveryIs(got, protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP,
			protocol.RecoveryInputRetired, "") {
			t.Fatalf("%s: recovery = %+v", code, got)
		}
	}
}

// TestRecoverySessionEndIsEnd is T6: an ended session cannot be observed again;
// the engine says end.
func TestRecoverySessionEndIsEnd(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	if _, apiErr := h.engine.End("agent", "Pend", start.OperationID); apiErr != nil {
		t.Fatalf("end rejected: %v", apiErr)
	}
	result, apiErr := h.engine.Act(context.Background(), "agent", "Pact", &protocol.ActRequest{
		ObservationID: start.Observation.ID,
		Operation:     protocol.Operation{Type: "key", Key: "BACK"},
	})
	if apiErr != nil {
		t.Fatalf("act transport error: %v", apiErr)
	}
	requireNotExecuted(t, result, protocol.CodeSessionEnded)
	requireRecovery(t, result, protocol.RecoveryEntryEnd, protocol.RecoveryReferenceP,
		protocol.RecoveryInputRevoked, "")
}

// TestStatusExpiredObservationKeepsExpiredInputState pins the expired branch of
// status(O): a ready O past its input deadline is reported as expired, not as
// an already-retired observation.
func TestStatusExpiredObservationKeepsExpiredInputState(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	observationID := start.Observation.ID
	h.engine.mu.Lock()
	h.engine.observations[observationID].ActionableUntil = time.Now().Add(-time.Minute)
	h.engine.mu.Unlock()

	result, apiErr := h.engine.Status("agent", observationID)
	if apiErr != nil {
		t.Fatalf("status rejected: %v", apiErr)
	}
	if result.ObservationState != ObservationExpired {
		t.Fatalf("observation_state = %q, want expired", result.ObservationState)
	}
	if result.InputState != protocol.RecoveryInputExpired {
		t.Fatalf("input_state = %q, want expired", result.InputState)
	}
	requireRecovery(t, result, protocol.RecoveryEntryObserve, protocol.RecoveryReferenceP,
		protocol.RecoveryInputExpired, "")
}

// TestRecoveryOmittedOnSuccess is T7: ordinary successes carry no next-step
// object.
func TestRecoveryOmittedOnSuccess(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	if start.Recovery != nil {
		t.Fatalf("start success carried recovery %+v", start.Recovery)
	}
	act, apiErr := h.engine.Act(context.Background(), "agent", "", tapButtonRequest(t, h, start.Observation.ID))
	if apiErr != nil {
		t.Fatalf("act rejected: %v", apiErr)
	}
	requireNoRecovery(t, act)
	obs, apiErr := h.engine.Observe(context.Background(), "agent", "Pobs",
		&protocol.ObserveRequest{Reference: act.Observation.ID})
	if apiErr != nil {
		t.Fatalf("observe rejected: %v", apiErr)
	}
	requireNoRecovery(t, obs)
}

// TestRecoveryIsNotReadByEngine is T9, the reverse control: with the planner
// disabled the same engine paths return no projection, so T2-T6's assertions
// are what detect the missing mapping.
func TestRecoveryIsNotReadByEngine(t *testing.T) {
	original := planRecovery
	planRecovery = func(recoveryInput) *protocol.Recovery { return nil }
	t.Cleanup(func() { planRecovery = original })

	h := newHarness(t)
	start := h.startSession()
	h.device.scriptCaptures(overlayCaptureStep())
	result := h.actCoordinate(t, start.Observation.ID, protocol.Operation{
		Type: "tap", Target: &protocol.Target{X: 500, Y: 150},
	})
	if result.Recovery != nil {
		t.Fatalf("engine synthesized a projection without the planner: %+v", result.Recovery)
	}
}

// TestResultJSONHasNoNext is T10: the wire member is gone, and recovery is the
// final member of the fixed order.
func TestResultJSONHasNoNext(t *testing.T) {
	result := &protocol.Result{
		OperationID: "P1",
		Outcome:     OutcomeNotExecuted,
		Error:       &protocol.Error{Code: protocol.CodeSessionEnded, Message: "原会话已结束"},
		Recovery: wantRecovery(protocol.RecoveryEntryEnd, protocol.RecoveryReferenceP,
			protocol.RecoveryInputRevoked, ""),
	}
	data, err := protocol.JSON(result)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	text := string(data)
	if strings.Contains(text, `"next"`) {
		t.Fatalf("result still carries next: %s", text)
	}
	const tail = `"recovery":{"entry":"end","reference":"P","input_state":"revoked"}}`
	if !strings.HasSuffix(text, tail) {
		t.Fatalf("recovery is not the final member: %s", text)
	}
	var decoded protocol.Result
	if err := protocol.Decode(data, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(decoded.Recovery, result.Recovery) {
		t.Fatalf("decoded recovery = %+v, want %+v", decoded.Recovery, result.Recovery)
	}
}

// TestContractVersionMatchesGenerated is T11: the compiled contract version,
// the single version source and the generated constant agree.
func TestContractVersionMatchesGenerated(t *testing.T) {
	data, err := os.ReadFile("../../release/versions.json")
	if err != nil {
		t.Fatalf("read version source: %v", err)
	}
	var versions struct {
		ContractVersion int    `json:"contract_version"`
		ReleaseVersion  string `json:"release_version"`
	}
	if err := json.Unmarshal(data, &versions); err != nil {
		t.Fatalf("decode version source: %v", err)
	}
	if versions.ContractVersion != 3 || protocol.ContractVersion != "3" {
		t.Fatalf("contract version source=%d compiled=%q, want 3",
			versions.ContractVersion, protocol.ContractVersion)
	}
	if versions.ReleaseVersion != protocol.ReleaseVersion {
		t.Fatalf("release version source=%q compiled=%q",
			versions.ReleaseVersion, protocol.ReleaseVersion)
	}
}

// TestRecoveryRejectedOnRequest is T12, the second reverse control: the service
// only writes recovery; it never reads one from a request.
func TestRecoveryRejectedOnRequest(t *testing.T) {
	body := []byte(`{"observation_id":"O1","operation":{"type":"key","key":"BACK"},` +
		`"recovery":{"entry":"retry","reference":"O"}}`)
	if _, apiErr := protocol.DecodeAct(body); apiErr == nil ||
		apiErr.Code != protocol.CodeInvalidRequest {
		t.Fatalf("request with recovery was admitted: %+v", apiErr)
	}
	if _, apiErr := protocol.DecodeStart([]byte(`{"recovery":{"entry":"status","reference":"P"}}`)); apiErr == nil ||
		apiErr.Code != protocol.CodeInvalidRequest {
		t.Fatalf("start request with recovery was admitted: %+v", apiErr)
	}
}
