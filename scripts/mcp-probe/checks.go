package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strconv"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/malyjacob/mobile-use-vnext/internal/client"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const (
	pollInterval             = 500 * time.Millisecond
	pollTimeout              = 30 * time.Second
	cancellationDelay        = 400 * time.Millisecond
	cancellationReturnWindow = 15 * time.Second
	maxWaitMS                = 8000
	probeShellOutput         = "mobile-use-mcp-probe"
	probeUnknownObservation  = "Oprobe-unknown"
	probeEmptyQuery          = "com.mobileuse.probe.no-such-application"

	suiteSmoke        = "smoke"
	suiteAppLifecycle = "app-lifecycle"

	// controlledAppPackage is the dedicated writable-loop target built from
	// android-control/test-target. The app-lifecycle suite never mutates any
	// other package by default.
	controlledAppPackage = "com.mobileuse.probe"
)

// toolNames is the complete public MCP catalog, in registration order. apps and
// open_app were merged into app; any of the old names existing is a failure.
var toolNames = []string{
	"start", "status", "observe", "act", "inspect", "keepalive",
	"end", "app", "notify", "shell", "diagnose",
}

// legacyToolNames must never appear in tools/list after the app merge.
var legacyToolNames = []string{"apps", "open_app"}

// probe carries the connected session and the typed service client used for
// independent status polling.
type probe struct {
	reporter   *reporter
	session    *sdk.ClientSession
	service    *client.Client
	resultMode string
	suite      string
	appTarget  string
	manifests  []string
}

// schemaView is the probe's projection of one published input schema.
type schemaView struct {
	Type                 string         `json:"type"`
	Properties           map[string]any `json:"properties"`
	Required             []string       `json:"required"`
	AdditionalProperties *bool          `json:"additionalProperties"`
}

// startSummary is the stable identity of one ordinary start.
type startSummary struct {
	OperationID   string
	ObservationID string
	Mode          string
}

// runChecks performs the catalog, session and cancellation checks. It reports
// whether the service was not admissible before device work (BLOCKED).
func (p *probe) runChecks(ctx context.Context) bool {
	if !p.catalogCheck(ctx) {
		return false
	}
	status, ok := p.statusCheck(ctx)
	if !ok {
		return false
	}
	if status.CanStart == nil || !*status.CanStart {
		p.reporter.fail("status", "can_start_false", field("blocking_code", status.BlockingCode))
		return true
	}
	p.reporter.pass("status", "can_start=true")
	if !p.errorProjectionCheck(ctx) {
		return false
	}

	start, ok := p.startCheck(ctx)
	if !ok {
		return false
	}
	if !p.appQueryChecks(ctx, start.OperationID) {
		return false
	}
	if p.suiteMode() == suiteAppLifecycle {
		if !p.appLifecycleChecks(ctx, start) {
			return false
		}
	} else {
		observedID, ok := p.observeCheck(ctx, start.ObservationID)
		if !ok {
			return false
		}
		if !p.contentProjectionCheck(ctx, start.OperationID, observedID) {
			return false
		}
		if !p.inspectCheck(ctx, observedID) {
			return false
		}
		if !p.actCheck(ctx, observedID) {
			return false
		}
	}
	if !p.endCheck(ctx, start.OperationID) {
		return false
	}
	if !p.cancellationCheck(ctx) {
		return false
	}
	return false
}

func (p *probe) suiteMode() string {
	if p.suite == "" {
		return suiteSmoke
	}
	return p.suite
}

// call invokes one MCP tool and projects its result. MCP-level errors become a
// controlled check failure; service failures stay in the structured result.
func (p *probe) call(ctx context.Context, name string, arguments any) (*callResult, *checkError) {
	result, err := p.session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil, failf("tool_call_failed")
	}
	mode := p.resultMode
	if mode == "" {
		mode = resultModeBoth
	}
	return project(result, mode)
}

// errorProjectionCheck uses a read-only lookup of an unknown observation. It
// proves the mode's error envelope without allocating a P or dispatching a
// device action.
func (p *probe) errorProjectionCheck(ctx context.Context) bool {
	projected, failure := p.call(ctx, "inspect", map[string]any{
		"observation_id": probeUnknownObservation,
	})
	if failure != nil {
		p.reporter.fail("result_error", failure.reason, failure.fields...)
		return false
	}
	if !projected.result.IsError || projected.view.Error == nil {
		p.reporter.fail("result_error", "error_projection_missing")
		return false
	}
	p.reporter.pass("result_error",
		field("mode", p.mode()), field("code", projected.view.Error.Code),
		"is_error=true")
	return true
}

// catalogCheck asserts the exact 11-tool catalog, the live/source semantic
// equality and the source fingerprint identity.
func (p *probe) catalogCheck(ctx context.Context) bool {
	list, err := p.session.ListTools(ctx, nil)
	if err != nil {
		p.reporter.fail("tools", "list_failed")
		return false
	}
	if failure := checkLiveCatalog(list.Tools); failure != nil {
		p.reporter.fail("tools", failure.reason, failure.fields...)
		return false
	}
	p.reporter.pass("tools",
		field("count", strconv.Itoa(len(list.Tools))),
		field("source_count", strconv.Itoa(len(toolNames))),
		"legacy_absent=true")
	p.reporter.pass("catalog",
		field("tools", strconv.Itoa(len(toolNames))),
		"names=set_equal", "description=identical", "schema=semantic_equal")
	if !p.catalogIdentityCheck() {
		return false
	}
	p.reporter.pass("act_scroll_schema",
		"type=scroll", "container=declared", "direction=up/down/left/right",
		"duration=50-2000")

	if failure := checkSchemas(list.Tools); failure != nil {
		p.reporter.fail("schema", failure.reason, failure.fields...)
		return false
	}
	p.reporter.pass("schema",
		field("objects", strconv.Itoa(len(toolNames))),
		field("additional_properties_false", strconv.Itoa(len(toolNames))),
		"required_start=0", "required_observe=1", "required_act=2",
		"required_status=0", "required_app=1")
	return true
}

// checkSchemas validates every published input schema without echoing it.
func checkSchemas(tools []*sdk.Tool) *checkError {
	for _, tool := range tools {
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return failf("input_schema_invalid", field("tool", tool.Name))
		}
		var schema schemaView
		if err := json.Unmarshal(encoded, &schema); err != nil {
			return failf("input_schema_invalid", field("tool", tool.Name))
		}
		if schema.Type != "object" {
			return failf("input_schema_not_object", field("tool", tool.Name))
		}
		if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
			return failf("additional_properties_open", field("tool", tool.Name))
		}
		for _, required := range schema.Required {
			if _, declared := schema.Properties[required]; !declared {
				return failf("required_not_declared", field("tool", tool.Name))
			}
		}
		switch tool.Name {
		case "start":
			if len(schema.Properties) != 0 || len(schema.Required) != 0 {
				return failf("start_schema_mismatch", field("tool", "start"))
			}
		case "status":
			if len(schema.Required) != 0 {
				return failf("required_mismatch", field("tool", "status"))
			}
		case "observe":
			if !sameStringSet(schema.Required, []string{"reference"}) {
				return failf("required_mismatch", field("tool", "observe"))
			}
		case "act":
			if !sameStringSet(schema.Required, []string{"observation_id", "operation"}) {
				return failf("required_mismatch", field("tool", "act"))
			}
		case "app":
			if !sameStringSet(schema.Required, []string{"kind"}) {
				return failf("required_mismatch", field("tool", "app"))
			}
			kind, declared := schema.Properties["kind"]
			if !declared {
				return failf("app_schema_kind_missing", field("tool", "app"))
			}
			kindObject, ok := kind.(map[string]any)
			if !ok {
				return failf("app_schema_kind_invalid", field("tool", "app"))
			}
			if values, ok := kindObject["enum"].([]any); !ok || len(values) != 7 {
				return failf("app_schema_kind_enum", field("tool", "app"))
			}
		}
	}
	return nil
}

// statusCheck calls status with an empty object.
func (p *probe) statusCheck(ctx context.Context) (resultView, bool) {
	projected, failure := p.call(ctx, "status", map[string]any{})
	if failure != nil {
		p.reporter.fail("status", failure.reason, failure.fields...)
		return resultView{}, false
	}
	if projected.result.IsError {
		p.reporter.fail("status", "tool_is_error", field("code", codeOf(projected.view)))
		return resultView{}, false
	}
	return projected.view, true
}

// startCheck runs one ordinary start and records P, O1 and the session mode.
func (p *probe) startCheck(ctx context.Context) (startSummary, bool) {
	projected, failure := p.call(ctx, "start", map[string]any{})
	if failure != nil {
		p.reporter.fail("start", failure.reason, failure.fields...)
		return startSummary{}, false
	}
	view := projected.view
	if projected.result.IsError {
		p.reporter.fail("start", "tool_is_error", field("code", codeOf(view)))
		return startSummary{}, false
	}
	if view.Outcome != outcomeExecuted {
		p.reporter.fail("start", "outcome_not_executed", field("outcome", view.Outcome), field("code", codeOf(view)))
		return startSummary{}, false
	}
	if !validReference(view.OperationID, 'P') {
		p.reporter.fail("start", "operation_id_invalid")
		return startSummary{}, false
	}
	if view.Session == nil || view.Session.State != sessionActive {
		p.reporter.fail("start", "session_not_active", field("session", sessionStateOf(view.Session)))
		return startSummary{}, false
	}
	if view.Observation == nil || !validReference(view.Observation.ID, 'O') {
		p.reporter.fail("start", "observation_missing")
		return startSummary{}, false
	}
	if format := treeFormatOf(view.Observation); format != treeFull {
		p.reporter.fail("start", "tree_not_full",
			field("tree_format", format), field("observation_status", view.Observation.Status))
		return startSummary{}, false
	}
	p.reporter.pass("start",
		field("P", view.OperationID), field("O1", view.Observation.ID),
		"session=active", field("session_mode", view.Session.Mode), "tree=full")
	return startSummary{
		OperationID:   view.OperationID,
		ObservationID: view.Observation.ID,
		Mode:          view.Session.Mode,
	}, true
}

// beginSession performs a start without printing a check line; the caller uses
// it for the second session of the cancellation check.
func (p *probe) beginSession(ctx context.Context) (string, string, *checkError) {
	projected, failure := p.call(ctx, "start", map[string]any{})
	if failure != nil {
		return "", "", failure
	}
	view := projected.view
	if projected.result.IsError || view.Outcome != outcomeExecuted {
		return "", "", failf("start_not_executed", field("code", codeOf(view)))
	}
	if !validReference(view.OperationID, 'P') {
		return "", "", failf("start_operation_invalid")
	}
	if view.Session == nil || view.Session.State != sessionActive {
		return "", "", failf("start_session_not_active", field("session", sessionStateOf(view.Session)))
	}
	if view.Observation == nil || !validReference(view.Observation.ID, 'O') {
		return "", "", failf("start_observation_missing")
	}
	return view.OperationID, view.Observation.ID, nil
}

// observeCheck requests one forced full observation with an image and asserts
// both the tree facts and the image carrier contract.
func (p *probe) observeCheck(ctx context.Context, reference string) (string, bool) {
	projected, failure := p.call(ctx, "observe", map[string]any{
		"reference": reference, "full": true, "image": true,
	})
	if failure != nil {
		p.reporter.fail("observe", failure.reason, failure.fields...)
		return "", false
	}
	view := projected.view
	if projected.result.IsError {
		p.reporter.fail("observe", "tool_is_error", field("code", codeOf(view)))
		return "", false
	}
	observation := view.Observation
	if observation == nil || !validReference(observation.ID, 'O') {
		p.reporter.fail("observe", "observation_missing")
		return "", false
	}
	if observation.Status != observationComplete && observation.Status != observationPartial {
		p.reporter.fail("observe", "observation_status_unexpected", field("status", observation.Status))
		return "", false
	}
	format := treeFormatOf(observation)
	if format != treeFull && format != treeDelta {
		p.reporter.fail("observe", "tree_format_missing", field("tree_format", format))
		return "", false
	}
	raw, failure := imageBytes(projected, observation)
	if failure != nil {
		p.reporter.fail("observe", failure.reason, failure.fields...)
		return "", false
	}
	if failure := checkNoImageCarrier(projected, raw); failure != nil {
		p.reporter.fail("observe", failure.reason, failure.fields...)
		return "", false
	}
	if p.mode() != resultModeStructured {
		if failure := checkObservationTreeBlock(projected, observation); failure != nil {
			p.reporter.fail("observe", failure.reason, failure.fields...)
			return "", false
		}
	}
	p.reporter.pass("observe",
		field("reference", reference), field("id", observation.ID),
		field("status", observation.Status), field("tree_format", format),
		"image_mime="+imageJPEG, field("image_bytes", strconv.Itoa(len(raw))),
		field("width", strconv.Itoa(observation.Image.Width)),
		field("height", strconv.Itoa(observation.Image.Height)))
	return observation.ID, true
}

// checkObservationTreeBlock asserts that the readable projection carries one
// complete observation tree block with the matching format and id.
func checkObservationTreeBlock(projected *callResult, observation *observationView) *checkError {
	format := treeFormatOf(observation)
	block, failure := treeBlockText(projected, "tree v1 format="+format+" id="+observation.ID)
	if failure != nil {
		return failure
	}
	if strings.Contains(block, " omitted ") {
		return failf("tree_block_omitted", field("tree_format", format))
	}
	if !strings.HasSuffix(block, "end tree\n") {
		return failf("tree_block_unterminated", field("tree_format", format))
	}
	return nil
}

// appQueryChecks covers the read-only app kinds on a live adapter. These calls
// never consume a ready O, so they are safe in the default smoke suite.
func (p *probe) appQueryChecks(ctx context.Context, reference string) bool {
	find, failure := p.call(ctx, "app", map[string]any{"kind": "find"})
	if failure != nil {
		p.reporter.fail("app_find", failure.reason, failure.fields...)
		return false
	}
	if find.result.IsError || find.view.App == nil || find.view.App.Kind != "find" {
		p.reporter.fail("app_find", "find_result_missing", field("code", codeOf(find.view)))
		return false
	}
	qApp := find.view.App
	if find.view.Outcome != "" {
		p.reporter.fail("app_find", "query_has_outcome", field("outcome", find.view.Outcome))
		return false
	}
	if !qApp.HasItems || qApp.Items == nil || !qApp.HasTotal || qApp.Truncated == nil || qApp.CapturedAt == "" {
		p.reporter.fail("app_find", "find_facts_missing")
		return false
	}
	if qApp.Total < len(qApp.Items) || (!*qApp.Truncated && qApp.Total != len(qApp.Items)) {
		p.reporter.fail("app_find", "find_total_mismatch",
			field("items", strconv.Itoa(len(qApp.Items))), field("total", strconv.Itoa(qApp.Total)))
		return false
	}
	for _, item := range qApp.Items {
		if item.Name == "" || item.Package == "" {
			p.reporter.fail("app_find", "find_item_incomplete")
			return false
		}
	}
	p.reporter.pass("app_find",
		field("mode", p.mode()), field("items", strconv.Itoa(len(qApp.Items))),
		field("total", strconv.Itoa(qApp.Total)), "outcome=none")

	empty, failure := p.call(ctx, "app", map[string]any{
		"kind": "find", "query": probeEmptyQuery,
	})
	if failure != nil {
		p.reporter.fail("app_find_empty", failure.reason, failure.fields...)
		return false
	}
	if empty.result.IsError || empty.view.App == nil || empty.view.App.Kind != "find" {
		p.reporter.fail("app_find_empty", "empty_result_missing", field("code", codeOf(empty.view)))
		return false
	}
	eApp := empty.view.App
	if empty.view.Outcome != "" || !eApp.HasItems || len(eApp.Items) != 0 ||
		!eApp.HasTotal || eApp.Total != 0 || eApp.Truncated == nil || *eApp.Truncated {
		p.reporter.fail("app_find_empty", "empty_facts_unexpected",
			field("items", strconv.Itoa(len(eApp.Items))), field("total", strconv.Itoa(eApp.Total)))
		return false
	}
	p.reporter.pass("app_find_empty", "items=0", "total=0", "truncated=false", "outcome=none")

	list, failure := p.call(ctx, "app", map[string]any{
		"kind": "list", "reference": reference,
	})
	if failure != nil {
		p.reporter.fail("app_list", failure.reason, failure.fields...)
		return false
	}
	if list.result.IsError || list.view.App == nil || list.view.App.Kind != "list" {
		p.reporter.fail("app_list", "list_result_missing", field("code", codeOf(list.view)))
		return false
	}
	lApp := list.view.App
	if list.view.Outcome != "" {
		p.reporter.fail("app_list", "query_has_outcome", field("outcome", list.view.Outcome))
		return false
	}
	if lApp.DisplayID == nil || !lApp.HasTaskItems || !lApp.HasTotal ||
		lApp.Truncated == nil || lApp.CapturedAt == "" || lApp.Foreground == nil {
		p.reporter.fail("app_list", "list_facts_missing")
		return false
	}
	if lApp.Total < len(lApp.TaskItems) || (!*lApp.Truncated && lApp.Total != len(lApp.TaskItems)) {
		p.reporter.fail("app_list", "list_total_mismatch",
			field("items", strconv.Itoa(len(lApp.TaskItems))), field("total", strconv.Itoa(lApp.Total)))
		return false
	}
	previousTaskID := 0
	for _, item := range lApp.TaskItems {
		if item.TaskID <= previousTaskID || item.Package == "" ||
			!appOwnershipSet[item.Ownership] {
			p.reporter.fail("app_list", "list_item_invalid")
			return false
		}
		previousTaskID = item.TaskID
	}
	if failure := checkForeground(lApp.Foreground); failure != nil {
		p.reporter.fail("app_list", failure.reason, failure.fields...)
		return false
	}
	p.reporter.pass("app_list",
		field("mode", p.mode()), field("display_id", integerString(lApp.DisplayID)),
		field("items", strconv.Itoa(len(lApp.TaskItems))), field("total", strconv.Itoa(lApp.Total)),
		field("foreground", lApp.Foreground.State), "outcome=none")

	primary, failure := p.call(ctx, "app", map[string]any{
		"kind": "list", "reference": reference, "scope": "primary",
	})
	if failure != nil {
		p.reporter.fail("app_list_primary", failure.reason, failure.fields...)
		return false
	}
	if primary.result.IsError || primary.view.App == nil || primary.view.App.Kind != "list" ||
		primary.view.Outcome != "" {
		p.reporter.fail("app_list_primary", "primary_result_missing", field("code", codeOf(primary.view)))
		return false
	}
	pApp := primary.view.App
	if pApp.DisplayID == nil || *pApp.DisplayID != 0 || !pApp.HasTaskItems ||
		!pApp.HasTotal || pApp.Truncated == nil || pApp.CapturedAt == "" ||
		pApp.Foreground == nil {
		p.reporter.fail("app_list_primary", "primary_facts_unexpected",
			field("display_id", integerString(pApp.DisplayID)))
		return false
	}
	if failure := checkForeground(pApp.Foreground); failure != nil {
		p.reporter.fail("app_list_primary", failure.reason, failure.fields...)
		return false
	}
	p.reporter.pass("app_list_primary",
		field("display_id", integerString(pApp.DisplayID)),
		field("items", strconv.Itoa(len(pApp.TaskItems))),
		field("foreground", pApp.Foreground.State), "outcome=none")

	// Cross-kind member: find never admits reference. A strict adapter rejects
	// before allocating an operation, so there is no P and no app result.
	admission, failure := p.call(ctx, "app", map[string]any{
		"kind": "find", "reference": reference,
	})
	if failure != nil {
		p.reporter.fail("app_admission", failure.reason, failure.fields...)
		return false
	}
	if !admission.result.IsError || admission.view.Error == nil ||
		admission.view.Error.Code != "invalid_request" ||
		admission.view.App != nil || admission.view.Outcome != "" ||
		admission.view.OperationID != "" {
		p.reporter.fail("app_admission", "cross_kind_not_rejected",
			field("code", codeOf(admission.view)), field("outcome", admission.view.Outcome))
		return false
	}
	p.reporter.pass("app_admission", "kind=find", "cross_member=reference",
		"code=invalid_request", "is_error=true", "operation_id=none")
	return true
}

// checkForeground validates one list foreground branch without assuming any
// device state. A null task_id is allowed on present; unknown must name a
// reason.
func checkForeground(foreground *foregroundView) *checkError {
	switch foreground.State {
	case "present":
		if !foreground.HasTask {
			return failf("foreground_task_missing")
		}
	case "none":
	case "unknown":
		if foreground.Reason == "" {
			return failf("foreground_reason_missing")
		}
	default:
		return failf("foreground_state_invalid", field("state", foreground.State))
	}
	return nil
}

func (p *probe) contentProjectionCheck(ctx context.Context, startID, observationID string) bool {
	shell, failure := p.call(ctx, "shell", map[string]any{
		"reference": observationID,
		"command":   "echo " + probeShellOutput,
	})
	if failure != nil {
		p.reporter.fail("shell_projection", failure.reason, failure.fields...)
		return false
	}
	if shell.result.IsError || shell.view.Shell == nil {
		p.reporter.fail("shell_projection", "shell_result_missing", field("code", codeOf(shell.view)))
		return false
	}
	if shell.view.Shell.Termination != "exited" ||
		shell.view.Shell.ExitCode == nil || *shell.view.Shell.ExitCode != 0 ||
		shell.view.Shell.Output == nil || *shell.view.Shell.Output != probeShellOutput+"\n" ||
		shell.view.Shell.Truncated == nil || *shell.view.Shell.Truncated {
		p.reporter.fail("shell_projection", "shell_facts_unexpected",
			field("termination", shell.view.Shell.Termination),
			field("exit_code", integerString(shell.view.Shell.ExitCode)))
		return false
	}
	if p.mode() != resultModeStructured {
		if failure := requireTextBlock(shell, "shell v1 ", "end shell", "shell_block_missing"); failure != nil {
			p.reporter.fail("shell_projection", failure.reason, failure.fields...)
			return false
		}
	}
	p.reporter.pass("shell_projection",
		field("mode", p.mode()), "termination=exited", "exit_code=0", "output=present")

	diagnosis, failure := p.call(ctx, "diagnose", map[string]any{"reference": startID})
	if failure != nil {
		p.reporter.fail("timeline_projection", failure.reason, failure.fields...)
		return false
	}
	if diagnosis.result.IsError || diagnosis.view.Diagnosis == nil {
		p.reporter.fail("timeline_projection", "diagnosis_result_missing", field("code", codeOf(diagnosis.view)))
		return false
	}
	if len(diagnosis.view.Diagnosis.Timeline) == 0 {
		p.reporter.fail("timeline_projection", "timeline_empty")
		return false
	}
	for _, event := range diagnosis.view.Diagnosis.Timeline {
		if event.At == "" || event.Phase == "" || event.Code == "" {
			p.reporter.fail("timeline_projection", "timeline_event_incomplete")
			return false
		}
	}
	if p.mode() != resultModeStructured {
		if failure := requireTextBlock(diagnosis, "timeline v1 events=", "end timeline", "timeline_block_missing"); failure != nil {
			p.reporter.fail("timeline_projection", failure.reason, failure.fields...)
			return false
		}
	}
	p.reporter.pass("timeline_projection",
		field("mode", p.mode()),
		field("events", strconv.Itoa(len(diagnosis.view.Diagnosis.Timeline))))
	return true
}

func (p *probe) mode() string {
	if p.resultMode == "" {
		return resultModeBoth
	}
	return p.resultMode
}

func requireTextBlock(projected *callResult, prefix, end, reason string) *checkError {
	block, failure := resultBlockText(projected, prefix, end, reason)
	if failure != nil {
		return failure
	}
	if strings.Contains(block, " omitted ") {
		return failf("text_block_omitted")
	}
	return nil
}

// requireTextLine asserts one complete readable line is present. It proves the
// text projection carries a fact without trusting the decoder that already
// parsed it.
func requireTextLine(projected *callResult, wanted string) *checkError {
	for _, text := range projected.texts {
		for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			if line == wanted {
				return nil
			}
		}
	}
	return failf("text_line_missing", field("line", wanted))
}

// requireQualityTextLines asserts the readable projection spells out all three
// quality members with the same values as the typed projection. samples=0 must
// be rendered explicitly.
func requireQualityTextLines(projected *callResult, quality *qualityView) *checkError {
	for _, wanted := range []string{
		"observation.quality.state=" + quality.State,
		"observation.quality.samples=" + strconv.Itoa(quality.Samples),
		"observation.quality.stop_reason=" + quality.StopReason,
	} {
		if failure := requireTextLine(projected, wanted); failure != nil {
			return failure
		}
	}
	return nil
}

func integerString(value *int) string {
	if value == nil {
		return "none"
	}
	return strconv.Itoa(*value)
}

// inspectCheck reads the observed tree through inspect and asserts the
// readable inspection block carries a header and a trailer.
func (p *probe) inspectCheck(ctx context.Context, observationID string) bool {
	projected, failure := p.call(ctx, "inspect", map[string]any{"observation_id": observationID})
	if failure != nil {
		p.reporter.fail("inspect", failure.reason, failure.fields...)
		return false
	}
	view := projected.view
	if projected.result.IsError {
		p.reporter.fail("inspect", "tool_is_error", field("code", codeOf(view)))
		return false
	}
	if view.Inspection == nil || view.Inspection.ObservationID != observationID {
		p.reporter.fail("inspect", "inspection_missing")
		return false
	}
	if p.mode() == resultModeStructured {
		p.reporter.pass("inspect",
			field("scope", view.Inspection.Scope), field("observation", observationID),
			"structured=true")
		return true
	}
	prefix := "inspect v1 scope=" + view.Inspection.Scope + " observation=" + observationID
	block, failure := treeBlockText(projected, prefix)
	if failure != nil {
		p.reporter.fail("inspect", failure.reason, failure.fields...)
		return false
	}
	if strings.Contains(block, " omitted ") {
		p.reporter.fail("inspect", "inspect_block_omitted", field("scope", view.Inspection.Scope))
		return false
	}
	if !strings.HasSuffix(block, "end inspect\n") {
		p.reporter.fail("inspect", "inspect_block_unterminated", field("scope", view.Inspection.Scope))
		return false
	}
	p.reporter.pass("inspect",
		field("scope", view.Inspection.Scope), field("observation", observationID),
		"tree_block=complete")
	return true
}

// actCheck consumes the observed O with a key action and asserts the post
// observation and its image. A refused key is a controlled failure, never a
// retry.
func (p *probe) actCheck(ctx context.Context, observationID string) bool {
	projected, failure := p.call(ctx, "act", map[string]any{
		"observation_id": observationID,
		"operation":      map[string]any{"type": "key", "key": "BACK"},
		"image":          true,
	})
	if failure != nil {
		p.reporter.fail("act", failure.reason, failure.fields...)
		return false
	}
	view := projected.view
	if view.Outcome != outcomeExecuted {
		code := codeOf(view)
		outcome := view.Outcome
		if outcome == "" {
			outcome = "none"
		}
		p.reporter.fail("act", "not_executed", field("code", code), field("outcome", outcome))
		return false
	}
	if projected.result.IsError {
		p.reporter.fail("act", "tool_is_error", field("code", codeOf(view)))
		return false
	}
	observation := view.Observation
	if observation == nil || !validReference(observation.ID, 'O') {
		p.reporter.fail("act", "post_observation_missing")
		return false
	}
	if observation.Status != observationComplete && observation.Status != observationPartial {
		p.reporter.fail("act", "post_observation_status_unexpected", field("status", observation.Status))
		return false
	}
	raw, failure := imageBytes(projected, observation)
	if failure != nil {
		p.reporter.fail("act", failure.reason, failure.fields...)
		return false
	}
	if failure := checkNoImageCarrier(projected, raw); failure != nil {
		p.reporter.fail("act", failure.reason, failure.fields...)
		return false
	}
	if p.mode() != resultModeStructured {
		if failure := checkObservationTreeBlock(projected, observation); failure != nil {
			p.reporter.fail("act", failure.reason, failure.fields...)
			return false
		}
	}
	// The live service always emits quality for an act post observation. A
	// missing or invalid object is a contract failure, and the safe key action
	// must never carry a scroll effect.
	if failure := checkObservationQuality(projected, observation); failure != nil {
		p.reporter.fail("act", failure.reason, failure.fields...)
		return false
	}
	if failure := checkScrollEffect(view, projected.object, false); failure != nil {
		p.reporter.fail("act", failure.reason, failure.fields...)
		return false
	}
	if p.mode() != resultModeStructured {
		if failure := requireQualityTextLines(projected, observation.Quality); failure != nil {
			p.reporter.fail("act", failure.reason, failure.fields...)
			return false
		}
	}
	p.reporter.pass("act",
		"outcome=executed", "key=BACK", field("id", observation.ID),
		field("status", observation.Status), "image_mime="+imageJPEG,
		field("image_bytes", strconv.Itoa(len(raw))),
		field("width", strconv.Itoa(observation.Image.Width)),
		field("height", strconv.Itoa(observation.Image.Height)),
		field("quality", observation.Quality.State),
		field("samples", strconv.Itoa(observation.Quality.Samples)),
		field("stop_reason", observation.Quality.StopReason),
		"effect=absent")
	return true
}

// endCheck ends the session and waits for the service to become admissible
// again, which is the client-visible proof that cleanup completed.
func (p *probe) endCheck(ctx context.Context, startID string) bool {
	projected, failure := p.call(ctx, "end", map[string]any{"reference": startID})
	if failure != nil {
		p.reporter.fail("end", failure.reason, failure.fields...)
		return false
	}
	view := projected.view
	if projected.result.IsError {
		p.reporter.fail("end", "tool_is_error", field("code", codeOf(view)))
		return false
	}
	if view.Outcome != outcomeExecuted {
		p.reporter.fail("end", "outcome_not_executed", field("outcome", view.Outcome), field("code", codeOf(view)))
		return false
	}
	if view.Session == nil || view.Session.State != sessionEnded {
		p.reporter.fail("end", "session_not_ended", field("session", sessionStateOf(view.Session)))
		return false
	}
	last, failure := pollCanStart(ctx, p.service)
	if failure != nil {
		p.reporter.fail("end", "cleanup_timeout", field("can_start", "false"))
		return false
	}
	p.reporter.pass("end",
		"outcome=executed", "session=ended",
		field("cleanup.state", cleanupStateOf(last, view)))
	return true
}

// waitCleanupSettled bounds the display-teardown race between the previous end
// and a second start. It asserts nothing about cleanup; it only avoids turning
// an in-flight teardown into a cancellation failure.
func (p *probe) waitCleanupSettled(ctx context.Context, budget time.Duration) {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		result, err := p.service.Status(ctx, "")
		if err == nil && (result == nil || result.Result == nil ||
			result.Result.Cleanup == nil || result.Result.Cleanup.State != "pending") {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// cancellationCheck starts a second session, cancels a waiting observe and
// proves the adapter issued a bounded end for it.
func (p *probe) cancellationCheck(ctx context.Context) bool {
	// The previous end starts the independent cleanup chain. A second start is
	// admitted while that chain runs, so give the display teardown a bounded
	// moment instead of reporting a teardown race as a cancellation failure.
	p.waitCleanupSettled(ctx, 5*time.Second)
	startID, observationID, failure := p.beginSession(ctx)
	if failure != nil {
		p.reporter.fail("cancel", "second_start_failed", failure.fields...)
		return false
	}

	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		result *sdk.CallToolResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := p.session.CallTool(callCtx, &sdk.CallToolParams{
			Name: "observe",
			Arguments: map[string]any{
				"reference": observationID,
				"wait_ms":   maxWaitMS,
			},
		})
		done <- outcome{result: result, err: err}
	}()

	select {
	case <-time.After(cancellationDelay):
		cancel()
	case <-ctx.Done():
		cancel()
	}

	var returned bool
	var returnedErr error
	var isErrorResult bool
	select {
	case completed := <-done:
		returned = true
		returnedErr = completed.err
		isErrorResult = completed.result != nil && completed.result.IsError
	case <-time.After(cancellationReturnWindow):
	}
	if !returned {
		p.reporter.fail("cancel", "call_did_not_return")
		return false
	}
	if returnedErr == nil && !isErrorResult {
		p.reporter.fail("cancel", "call_not_cancelled")
		return false
	}

	last, failure := pollSessionEnded(ctx, p.service)
	if failure != nil {
		p.reporter.fail("cancel", "cleanup_timeout", field("can_start", "false"))
		return false
	}

	operationState := "none"
	operationResult := statusResultOf(p.service.Status(ctx, startID))
	if operationResult == nil {
		p.reporter.fail("cancel", "operation_status_unavailable")
		return false
	}
	switch {
	case operationResult.OperationState != "":
		operationState = operationResult.OperationState
	case operationResult.RequestClosed != nil && *operationResult.RequestClosed:
		operationState = requestClosedLabel
	}
	if operationState == operationRunning {
		p.reporter.fail("cancel", "operation_still_running", field("operation_state", operationState))
		return false
	}
	if operationState != operationFinished && operationState != operationNotSeen && operationState != requestClosedLabel {
		p.reporter.fail("cancel", "operation_state_unexpected", field("operation_state", operationState))
		return false
	}

	sessionState := "none"
	if last != nil && last.Session != nil {
		sessionState = last.Session.State
	}
	if sessionState == sessionActive || sessionState == sessionStarting {
		p.reporter.fail("cancel", "session_still_active", field("session", sessionState))
		return false
	}
	returnedKind := "context_error"
	if isErrorResult {
		returnedKind = "is_error"
	}
	p.reporter.pass("cancel",
		field("P", startID), "returned="+returnedKind,
		field("session", sessionState), "can_start=true",
		field("operation_state", operationState))
	return true
}

// pollCanStart polls status() until can_start is true or the deadline passes.
func pollCanStart(ctx context.Context, service *client.Client) (*protocol.Result, *checkError) {
	deadline := time.Now().Add(pollTimeout)
	var last *protocol.Result
	for {
		if result := statusResultOf(service.Status(ctx, "")); result != nil {
			last = result
			if result.CanStart != nil && *result.CanStart {
				return result, nil
			}
		}
		if time.Now().After(deadline) {
			return last, failf("cleanup_timeout")
		}
		select {
		case <-ctx.Done():
			return last, failf("cleanup_timeout")
		case <-time.After(pollInterval):
		}
	}
}

// pollSessionEnded polls status() until the original session is no longer
// active and the service is admissible again.
func pollSessionEnded(ctx context.Context, service *client.Client) (*protocol.Result, *checkError) {
	deadline := time.Now().Add(pollTimeout)
	var last *protocol.Result
	for {
		if result := statusResultOf(service.Status(ctx, "")); result != nil {
			last = result
			canStart := result.CanStart != nil && *result.CanStart
			ended := result.Session == nil ||
				(result.Session.State != sessionActive && result.Session.State != sessionStarting)
			if canStart && ended {
				return result, nil
			}
		}
		if time.Now().After(deadline) {
			return last, failf("cleanup_timeout")
		}
		select {
		case <-ctx.Done():
			return last, failf("cleanup_timeout")
		case <-time.After(pollInterval):
		}
	}
}

// statusResultOf returns the structured result of one client call, whether it
// arrived as a normal response or inside a typed client error.
func statusResultOf(response *client.Response, err error) *protocol.Result {
	if response != nil && response.Result != nil {
		return response.Result
	}
	var clientErr *client.Error
	if errors.As(err, &clientErr) && clientErr.Result != nil {
		return clientErr.Result
	}
	return nil
}

func cleanupStateOf(last *protocol.Result, endView resultView) string {
	if last != nil && last.Cleanup != nil && last.Cleanup.State != "" {
		return last.Cleanup.State
	}
	if endView.Cleanup != nil && endView.Cleanup.State != "" {
		return endView.Cleanup.State
	}
	return "none"
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func sameStringSet(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	seen := make(map[string]bool, len(actual))
	for _, value := range actual {
		seen[value] = true
	}
	for _, value := range expected {
		if !seen[value] {
			return false
		}
	}
	return true
}
