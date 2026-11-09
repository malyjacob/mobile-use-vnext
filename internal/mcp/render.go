package mcp

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// renderText builds the deterministic readable projection carried in the
// single TextContent. It preserves stage facts as key=value lines and never
// includes image bytes, tree nodes, shell output text, notification text or
// application name lists.
func renderText(result *protocol.Result, cancelled bool, cancellation string) string {
	if result == nil {
		if cancelled {
			return "cancelled=true\ncancellation=" + cancellation + "\n"
		}
		return ""
	}
	var builder strings.Builder
	line := func(key, value string) {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(value)
		builder.WriteByte('\n')
	}
	boolValue := func(value bool) string {
		if value {
			return "true"
		}
		return "false"
	}
	intValue := func(value int) string { return strconv.Itoa(value) }
	writeError := func(prefix string, value *protocol.Error) {
		if value == nil {
			return
		}
		if value.Code != "" {
			line(prefix+".code", value.Code)
		}
		if value.Message != "" {
			line(prefix+".message", value.Message)
		}
	}

	if result.OperationID != "" {
		line("operation_id", result.OperationID)
	}
	if result.OperationState != "" {
		line("operation_state", result.OperationState)
	}
	if result.Outcome != "" {
		line("outcome", result.Outcome)
	}
	if result.Phase != "" {
		line("phase", result.Phase)
	}
	if result.TargetReference != "" {
		line("target_reference", result.TargetReference)
	}
	if result.RequestClosed != nil {
		line("request_closed", boolValue(*result.RequestClosed))
	}
	if result.BlockingOperationID != "" {
		line("blocking_operation_id", result.BlockingOperationID)
	}
	if result.CanStart != nil {
		line("can_start", boolValue(*result.CanStart))
	}
	if result.BlockingCode != "" {
		line("blocking_code", result.BlockingCode)
	}
	if result.StartOperation != "" {
		line("start_operation_id", result.StartOperation)
	}
	if result.ObservationState != "" {
		line("observation_state", result.ObservationState)
	}
	if result.InputState != "" {
		line("input_state", result.InputState)
	}
	if result.MessageID != "" {
		line("message_id", result.MessageID)
	}
	if result.Saved != nil {
		line("saved", boolValue(*result.Saved))
	}

	if result.IncludeSession {
		if result.Session == nil {
			line("session", "null")
		} else {
			session := result.Session
			line("session.origin", session.Origin)
			line("session.state", session.State)
			line("session.mode", session.Mode)
			if session.Agent != "" {
				line("session.agent", session.Agent)
			}
			if session.StartedAt != "" {
				line("session.started_at", session.StartedAt)
			}
			if session.ExpiresAt != "" {
				line("session.expires_at", session.ExpiresAt)
			}
			if session.EndedAt != "" {
				line("session.ended_at", session.EndedAt)
			}
			if session.Reason != "" {
				line("session.reason", session.Reason)
			}
		}
	}

	if observation := result.Observation; observation != nil {
		line("observation.status", observation.Status)
		if observation.ID != "" {
			line("observation.id", observation.ID)
		}
		if observation.Origin != "" {
			line("observation.origin", observation.Origin)
		}
		if observation.Revision > 0 {
			line("observation.revision", intValue(observation.Revision))
		}
		if observation.CapturedAt != "" {
			line("observation.captured_at", observation.CapturedAt)
		}
		if observation.ActionableUntil != "" {
			line("observation.actionable_until", observation.ActionableUntil)
		}
		if observation.Screen != nil {
			line("observation.screen.width", intValue(observation.Screen.Width))
			line("observation.screen.height", intValue(observation.Screen.Height))
			line("observation.screen.rotation", intValue(observation.Screen.Rotation))
		}
		if foreground := observation.Foreground; foreground != nil {
			line("observation.foreground.state", foreground.State)
			if foreground.Package != "" {
				line("observation.foreground.package", foreground.Package)
			}
			if foreground.HasTask {
				if foreground.TaskID == nil {
					line("observation.foreground.task_id", "null")
				} else {
					line("observation.foreground.task_id", intValue(*foreground.TaskID))
				}
			}
			if foreground.Reason != "" {
				line("observation.foreground.reason", foreground.Reason)
			}
		}
		if tree := observation.Tree; tree != nil {
			switch {
			case tree.Full != nil:
				line("observation.tree.format", "full")
				if tree.Full.Scope != "" {
					line("observation.tree.scope", tree.Full.Scope)
				}
			case tree.Delta != nil:
				line("observation.tree.format", "delta")
				if tree.Delta.Base != "" {
					line("observation.tree.base", tree.Delta.Base)
				}
			}
		}
		if len(observation.Missing) > 0 {
			line("observation.missing", strings.Join(observation.Missing, ","))
		}
		if image := observation.Image; image != nil {
			if image.CapturedAt != "" {
				line("observation.image.captured_at", image.CapturedAt)
			}
			if image.Width > 0 && image.Height > 0 {
				line("observation.image.width", intValue(image.Width))
				line("observation.image.height", intValue(image.Height))
			}
			if image.MediaType != "" {
				line("observation.image.media_type", image.MediaType)
			}
			line("observation.image.read_only", boolValue(image.ReadOnly))
			writeError("observation.image.error", image.Error)
		}
		writeError("observation.error", observation.Error)
	}

	if inspection := result.Inspection; inspection != nil {
		if inspection.ObservationID != "" {
			line("inspection.observation_id", inspection.ObservationID)
		}
		if inspection.Origin != "" {
			line("inspection.origin", inspection.Origin)
		}
		line("inspection.revision", intValue(inspection.Revision))
		if inspection.CapturedAt != "" {
			line("inspection.captured_at", inspection.CapturedAt)
		}
		if inspection.Scope != "" {
			line("inspection.scope", inspection.Scope)
		}
		if inspection.HasTotal {
			line("inspection.total", intValue(inspection.Total))
		}
		if inspection.Truncated != nil {
			line("inspection.truncated", boolValue(*inspection.Truncated))
		}
		line("inspection.match_count", intValue(len(inspection.Matches)))
	}

	if apps := result.Apps; apps != nil {
		line("apps.items_count", intValue(len(apps.Items)))
		line("apps.total", intValue(apps.Total))
		line("apps.truncated", boolValue(apps.Truncated))
		if apps.CapturedAt != "" {
			line("apps.captured_at", apps.CapturedAt)
		}
	}

	if app := result.App; app != nil {
		if app.Package != "" {
			line("app.package", app.Package)
		}
		line("app.task_id", intValue(app.TaskID))
		line("app.display_id", intValue(app.DisplayID))
		line("app.reused", boolValue(app.Reused))
		if app.StatePreserved != nil {
			line("app.state_preserved", boolValue(*app.StatePreserved))
		}
	}

	if shell := result.Shell; shell != nil {
		if shell.Termination != "" {
			line("shell.termination", shell.Termination)
		}
		if shell.ExitCode == nil {
			line("shell.exit_code", "null")
		} else {
			line("shell.exit_code", intValue(*shell.ExitCode))
		}
		if shell.Truncated == nil {
			line("shell.truncated", "null")
		} else {
			line("shell.truncated", boolValue(*shell.Truncated))
		}
		if shell.Output == nil {
			line("shell.output_bytes", "null")
		} else {
			line("shell.output_bytes", intValue(len(*shell.Output)))
		}
		writeError("shell.output_error", shell.OutputError)
	}

	if notification := result.Notification; notification != nil {
		if notification.State != "" {
			line("notification.state", notification.State)
		}
		writeError("notification.error", notification.Error)
	}

	if diagnosis := result.Diagnosis; diagnosis != nil {
		line("diagnosis.summary", diagnosis.Summary)
		issues := make([]string, 0, len(diagnosis.Issues))
		for _, issue := range diagnosis.Issues {
			issues = append(issues, issue.Phase+":"+issue.Code)
		}
		line("diagnosis.issues", strings.Join(issues, ","))
		if diagnosis.Coverage != "" {
			line("diagnosis.coverage", diagnosis.Coverage)
		}
		line("diagnosis.truncated", boolValue(diagnosis.Truncated))
		timingKeys := make([]string, 0, len(diagnosis.TimingMS))
		for key := range diagnosis.TimingMS {
			timingKeys = append(timingKeys, key)
		}
		sort.Strings(timingKeys)
		for _, key := range timingKeys {
			line("diagnosis.timing_ms."+key, strconv.FormatInt(diagnosis.TimingMS[key], 10))
		}
		if diagnosis.Operation != "" {
			line("diagnosis.operation_id", diagnosis.Operation)
		}
		if diagnosis.Outcome != "" {
			line("diagnosis.outcome", diagnosis.Outcome)
		}
	}

	if cleanup := result.Cleanup; cleanup != nil {
		if cleanup.State != "" {
			line("cleanup.state", cleanup.State)
		}
		if cleanup.Execution != "" {
			line("cleanup.execution", cleanup.Execution)
		}
		if cleanup.Tasks != "" {
			line("cleanup.tasks", cleanup.Tasks)
		}
		if cleanup.Recents != "" {
			line("cleanup.recents", cleanup.Recents)
		}
		if cleanup.Display != "" {
			line("cleanup.display", cleanup.Display)
		}
		writeError("cleanup.error", cleanup.Error)
	}

	if result.Error != nil {
		writeError("error", result.Error)
		if len(result.Error.Details) > 0 {
			keys := make([]string, 0, len(result.Error.Details))
			for key := range result.Error.Details {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				line("error.details."+key, detailValue(result.Error.Details[key]))
			}
		}
	}
	if recovery := result.Recovery; recovery != nil {
		if recovery.Entry != "" {
			line("recovery.entry", recovery.Entry)
		}
		if recovery.Reference != "" {
			line("recovery.reference", recovery.Reference)
		}
		if recovery.InputState != "" {
			line("recovery.input_state", recovery.InputState)
		}
		if recovery.Cause != "" {
			line("recovery.cause", recovery.Cause)
		}
	}
	if cancelled {
		line("cancelled", "true")
		if cancellation != "" {
			line("cancellation", cancellation)
		}
	}
	return builder.String()
}

// detailValue renders one whitelisted error detail without ever inserting a
// multi-line or non-JSON representation.
func detailValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "null"
		}
		return string(encoded)
	}
}
