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
	mcpsource "github.com/malyjacob/mobile-use-vnext/internal/mcp"
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
	"start", "status", "observe", "snap", "wait_for", "act", "inspect", "keepalive",
	"end", "app", "notify", "shell", "diagnose",
}

// legacyToolNames must never appear in tools/list after the app merge.
var legacyToolNames = []string{"apps", "open_app"}

// probe carries the connected session and the typed service client used for
// independent status polling. compatInitialize optionally reads one
// compatibility initialize result; it is set for every real transport and is
// nil for the service-only p-recovery entry.
type probe struct {
	reporter         *reporter
	session          *sdk.ClientSession
	service          *client.Client
	resultMode       string
	suite            string
	appTarget        string
	manifests        []string
	compatInitialize func(context.Context) (*sdk.InitializeResult, *checkError)
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

// runChecks performs the discovery, catalog, session and cancellation checks.
// It reports whether the service was not admissible before device work
// (BLOCKED).
func (p *probe) runChecks(ctx context.Context) bool {
	if !p.instructionsCheck() {
		return false
	}
	if !p.compatibilityInstructionsCheck(ctx) {
		return false
	}
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
	cleanupNeeded := true
	defer func() {
		if cleanupNeeded {
			p.cleanupOwnedSession(ctx, start.OperationID)
		}
	}()
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
		if _, ok := p.snapCheck(ctx, observedID); !ok {
			return false
		}
		waitedID, ok := p.waitForCheck(ctx, observedID)
		if !ok {
			return false
		}
		if waitedID != "" {
			observedID = waitedID
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
	// endCheck makes the single end attempt for the normal path. If it fails,
	// the known P may already have been accepted, so the defer must not replay it.
	cleanupNeeded = false
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
	return project(result, mode, requestRefOf(arguments))
}

// requestRefOf extracts the single-call reference from the already decoded
// arguments. It exists only to validate an exact recovery ref and never
// persists across calls.
func requestRefOf(arguments any) string {
	object, ok := arguments.(map[string]any)
	if !ok {
		return ""
	}
	reference, _ := object["reference"].(string)
	return reference
}

// resultOutcome resolves the effective outcome of one projected call. In the
// text-only channel v4 omits outcome=executed for a plain successful action;
// that omission is the documented default and must not read as "no outcome".
func resultOutcome(projected *callResult) string {
	if projected.view.Outcome != "" {
		return projected.view.Outcome
	}
	if projected.textOnly && !projected.result.IsError && projected.textHeader != nil &&
		!projected.textHeader.HasOutcome && projected.textHeader.State == "" {
		return outcomeExecuted
	}
	return ""
}

// errorProjectionCheck uses a read-only lookup of an unknown observation. It
// proves the mode's error envelope without allocating a P or dispatching a
// device action.
func (p *probe) errorProjectionCheck(ctx context.Context) bool {
	projected, failure := p.call(ctx, "inspect", map[string]any{
		"reference": probeUnknownObservation,
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

// instructionsCheck is mandatory for every transport that has an MCP session.
// It compares the live discovery instructions with the source accessor instead
// of duplicating the text, so a running adapter cannot publish a stale guide.
func (p *probe) instructionsCheck() bool {
	if p.session == nil {
		p.reporter.fail("instructions", "session_missing")
		return false
	}
	result := p.session.InitializeResult()
	if failure := checkInstructions(result); failure != nil {
		p.reporter.fail("instructions", failure.reason, failure.fields...)
		return false
	}
	p.reporter.pass("instructions",
		field("bytes", strconv.Itoa(len(result.Instructions))),
		field("source_bytes", strconv.Itoa(len(mcpsource.ServerInstructions()))),
		"source_match=true", "hidden_result_terms=absent", field("protocol", result.ProtocolVersion))
	return true
}

// compatibilityInstructionsCheck is the additional initialize-era check. It
// reads exactly one compatibility initialize result (a legacy stdio session or
// a single stateless HTTP initialize request) and proves it carries the same
// instructions. It is skipped only when the probe has no compatibility reader
// (the service-only p-recovery entry).
func (p *probe) compatibilityInstructionsCheck(ctx context.Context) bool {
	if p.compatInitialize == nil {
		p.reporter.skip("instructions_compat", "no_secondary_transport")
		return true
	}
	result, failure := p.compatInitialize(ctx)
	if failure != nil {
		p.reporter.fail("instructions_compat", failure.reason, failure.fields...)
		return false
	}
	if failure := checkInstructions(result); failure != nil {
		p.reporter.fail("instructions_compat", failure.reason, failure.fields...)
		return false
	}
	if result.ProtocolVersion != compatibilityProtocolVersion {
		p.reporter.fail("instructions_compat", "protocol_not_compatibility",
			field("protocol", result.ProtocolVersion))
		return false
	}
	p.reporter.pass("instructions_compat",
		field("protocol", result.ProtocolVersion),
		field("bytes", strconv.Itoa(len(result.Instructions))), "source_match=true", "hidden_result_terms=absent")
	return true
}

// checkInstructions reduces one initialize/discover result to a controlled
// failure. It never echoes the instructions text.
func checkInstructions(result *sdk.InitializeResult) *checkError {
	if result == nil || result.Instructions == "" {
		return failf("instructions_missing")
	}
	if containsHiddenResultTerm(result.Instructions) {
		return failf("instructions_hidden_result_term")
	}
	want := mcpsource.ServerInstructions()
	if want == "" {
		return failf("source_instructions_missing")
	}
	if result.Instructions != want {
		return failf("instructions_mismatch",
			field("bytes", strconv.Itoa(len(result.Instructions))),
			field("source_bytes", strconv.Itoa(len(want))))
	}
	return nil
}

// catalogCheck asserts the exact 13-tool catalog, the live/source semantic
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
		"legacy_absent=true", "hidden_result_terms=absent")
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
			if !sameStringSet(schema.Required, []string{"reference", "operation"}) {
				return failf("required_mismatch", field("tool", "act"))
			}
			if _, retired := schema.Properties["observation_id"]; retired {
				return failf("act_retired_observation_id", field("tool", "act"))
			}
			operation, ok := schema.Properties["operation"].(map[string]any)
			if !ok {
				return failf("act_operation_schema_invalid", field("tool", "act"))
			}
			properties, ok := operation["properties"].(map[string]any)
			if !ok {
				return failf("act_operation_schema_invalid", field("tool", "act"))
			}
			// bubble and auto_submit are branch-qualified booleans; submit_ref
			// is the branch-qualified closed string selector. None may publish a
			// JSON Schema default as if it applied to coordinates or other
			// actions.
			for member, wantType := range map[string]string{
				"bubble": "boolean", "auto_submit": "boolean", "submit_ref": "string",
			} {
				option, declared := properties[member].(map[string]any)
				if !declared || option["type"] != wantType {
					return failf("act_branch_member_missing", field("member", member))
				}
				// A branch-qualified default must not be published as if it
				// applied to coordinates or another action.
				if _, published := option["default"]; published {
					return failf("act_branch_member_default", field("member", member))
				}
			}
			submitRef, ok := properties["submit_ref"].(map[string]any)
			if !ok || submitRef["minLength"] != float64(1) || submitRef["maxLength"] != float64(64) ||
				submitRef["pattern"] != "^(enter_key|n[1-9][0-9]*)$" {
				return failf("act_submit_ref_shape_invalid")
			}
			if _, retired := properties["press_enter"]; retired {
				return failf("act_retired_press_enter", field("member", "press_enter"))
			}
		case "app":
			var tree any
			if err := json.Unmarshal(encoded, &tree); err != nil {
				return failf("input_schema_invalid", field("tool", tool.Name))
			}
			if failure := checkAppSchema(schema, tree); failure != nil {
				return failure
			}
		}
	}
	return nil
}

// checkAppSchema pins the flat app surface: the fixed root member set, kind
// required, a flat target object, and no conditional JSON-Schema applicators
// anywhere. Branch admission remains the server-side strict decoder.
func checkAppSchema(schema schemaView, tree any) *checkError {
	wantMembers := []string{
		"kind", "query", "reference", "scope", "app",
		"target", "restore_foreground", "allow_borrowed", "allow_stop_playback",
	}
	if len(schema.Properties) != len(wantMembers) {
		return failf("app_schema_members", field("count", strconv.Itoa(len(schema.Properties))))
	}
	for _, member := range wantMembers {
		if _, declared := schema.Properties[member]; !declared {
			return failf("app_schema_member_missing", field("member", member))
		}
	}
	if _, retired := schema.Properties["observation_id"]; retired {
		return failf("app_retired_observation_id")
	}
	if !sameStringSet(schema.Required, []string{"kind"}) {
		return failf("required_mismatch", field("tool", "app"))
	}
	kind, declared := schema.Properties["kind"].(map[string]any)
	if !declared {
		return failf("app_schema_kind_missing", field("tool", "app"))
	}
	if values, ok := kind["enum"].([]any); !ok || len(values) != 7 {
		return failf("app_schema_kind_enum", field("tool", "app"))
	}
	if keyword, found := conditionalApplicator(tree); found {
		return failf("app_schema_conditional", field("keyword", keyword))
	}
	target, declared := schema.Properties["target"].(map[string]any)
	if !declared || target["type"] != "object" {
		return failf("app_target_not_object")
	}
	properties, ok := target["properties"].(map[string]any)
	if !ok || len(properties) != 2 {
		return failf("app_target_members")
	}
	for _, member := range []string{"task_id", "app"} {
		if _, declared := properties[member]; !declared {
			return failf("app_target_member_missing", field("member", member))
		}
	}
	if additional, ok := target["additionalProperties"].(bool); !ok || additional {
		return failf("app_target_additional_properties")
	}
	return nil
}

// conditionalApplicator walks a decoded schema and returns the first
// branch-describing keyword. It is called with the complete root value so a
// root-level allOf/if/then/else/not/oneOf cannot escape inspection.
func conditionalApplicator(value any) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch key {
			case "allOf", "if", "then", "else", "not", "oneOf":
				return key, true
			}
			if keyword, found := conditionalApplicator(child); found {
				return keyword, found
			}
		}
	case []any:
		for _, child := range typed {
			if keyword, found := conditionalApplicator(child); found {
				return keyword, found
			}
		}
	}
	return "", false
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
	if resultOutcome(projected) != outcomeExecuted {
		p.reporter.fail("start", "outcome_not_executed", field("outcome", resultOutcome(projected)), field("code", codeOf(view)))
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
	if p.mode() != resultModeStructured {
		if failure := checkObservationTreeBlock(projected, view.Observation); failure != nil {
			p.reporter.fail("start", failure.reason, failure.fields...)
			return startSummary{}, false
		}
	}
	passFields := []string{
		field("P", view.OperationID), field("O1", view.Observation.ID),
		"session=active", field("session_mode", view.Session.Mode), "tree=full",
	}
	if p.mode() != resultModeStructured {
		passFields = append(passFields, "tree_block=v5")
	}
	p.reporter.pass("start",
		passFields...)
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
	if projected.result.IsError || resultOutcome(projected) != outcomeExecuted {
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
	passFields := []string{
		field("reference", reference), field("id", observation.ID),
		field("status", observation.Status), field("tree_format", format),
		"image_mime=" + imageJPEG, field("image_bytes", strconv.Itoa(len(raw))),
		field("width", strconv.Itoa(observation.Image.Width)),
		field("height", strconv.Itoa(observation.Image.Height)),
	}
	if p.mode() != resultModeStructured {
		passFields = append(passFields, "tree_block=v5")
	}
	p.reporter.pass("observe", passFields...)
	return observation.ID, true
}

// snapCheck proves the one top-level image read: the tool returns a P and a
// complete JPEG metadata object, delivers exactly one ImageContent, keeps the
// text/structured body free of base64, and never publishes an observation or
// an action outcome.
func (p *probe) snapCheck(ctx context.Context, reference string) (string, bool) {
	projected, failure := p.call(ctx, "snap", map[string]any{"reference": reference})
	if failure != nil {
		p.reporter.fail("snap", failure.reason, failure.fields...)
		return "", false
	}
	view := projected.view
	if projected.result.IsError {
		p.reporter.fail("snap", "tool_is_error", field("code", codeOf(view)))
		return "", false
	}
	if view.Observation != nil || view.Outcome != "" {
		p.reporter.fail("snap", "snap_success_shape_unexpected")
		return "", false
	}
	if !validReference(view.OperationID, 'P') {
		p.reporter.fail("snap", "operation_id_invalid")
		return "", false
	}
	raw, failure := snapImageBytes(projected, view.Image)
	if failure != nil {
		p.reporter.fail("snap", failure.reason, failure.fields...)
		return "", false
	}
	if failure := checkNoImageCarrier(projected, raw); failure != nil {
		p.reporter.fail("snap", failure.reason, failure.fields...)
		return "", false
	}
	p.reporter.pass("snap",
		field("reference", reference), field("operation_id", view.OperationID),
		"image_mime="+imageJPEG, field("image_bytes", strconv.Itoa(len(raw))),
		field("width", strconv.Itoa(view.Image.Width)),
		field("height", strconv.Itoa(view.Image.Height)),
		field("screen_width", strconv.Itoa(view.Image.Screen.Width)),
		field("screen_height", strconv.Itoa(view.Image.Screen.Height)),
		field("read_only", "true"))
	return view.OperationID, true
}

// checkObservationTreeBlock asserts that the readable projection carries one
// complete observation tree block with the matching format and id.
func checkObservationTreeBlock(projected *callResult, observation *observationView) *checkError {
	if observation == nil {
		return failf("text_tree_block_missing")
	}
	if !validTreeTextO(observation.ID) {
		return failf("text_tree_id_missing")
	}
	format := treeFormatOf(observation)
	headerPrefix := ""
	switch format {
	case treeFull:
		headerPrefix = "tree v5 full nodes="
	case treeDelta:
		headerPrefix = "tree v5 delta base="
	default:
		return failf("text_tree_format_invalid")
	}
	block, failure := treeBlockText(projected, headerPrefix)
	if failure != nil {
		return failure
	}
	if strings.Contains(block, " omitted ") {
		return failf("tree_block_omitted", field("tree_format", format))
	}
	if !strings.HasSuffix(block, "end tree\n") {
		return failf("tree_block_unterminated", field("tree_format", format))
	}
	if format == treeDelta {
		firstLine := block
		if separator := strings.IndexByte(block, '\n'); separator >= 0 {
			firstLine = block[:separator]
		}
		rawBase, failure := treeHeaderBase(firstLine)
		if failure != nil {
			return failure
		}
		base, failure := resolveTreeBase(observation.ID, rawBase)
		if failure != nil {
			return failure
		}
		structuredBase := ""
		if observation.Tree != nil {
			structuredBase = observation.Tree.Base
		}
		if structuredBase != base {
			return failf("text_tree_base_mismatch",
				field("header", base),
				field("structured", structuredBase))
		}
	}
	if containsCompactTreeChain(block) {
		return failf("tree_chain_present", field("tree_format", format))
	}
	return nil
}

func treeHeaderBase(header string) (string, *checkError) {
	rest, found := strings.CutPrefix(header, "tree v5 delta base=")
	if !found {
		return "", failf("text_tree_header_invalid")
	}
	separator := strings.LastIndex(rest, " changes=")
	if separator <= 0 {
		return "", failf("text_tree_header_invalid")
	}
	return rest[:separator], nil
}

// containsCompactTreeChain reports whether a readable compact block uses the
// historical square-bracket or explicit chain definition. A live product must
// emit only ordinary node definitions after the compact-pruning contract.
func containsCompactTreeChain(block string) bool {
	for _, raw := range strings.Split(block, "\n") {
		line := strings.TrimLeft(raw, " ")
		tokens, ok := treeLineTokens(line)
		if !ok || len(tokens) == 0 {
			continue
		}
		if tokens[0] == "chain" || strings.HasPrefix(tokens[0], "[") {
			return true
		}
	}
	return false
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

	// Tolerant branch: find ignores every declared member it does not use. A
	// redundant reference to an unknown observation must not be resolved,
	// rejected or consumed by this read-only query.
	tolerant, failure := p.call(ctx, "app", map[string]any{
		"kind": "find", "reference": probeUnknownObservation,
	})
	if failure != nil {
		p.reporter.fail("app_admission", failure.reason, failure.fields...)
		return false
	}
	if tolerant.result.IsError || tolerant.view.App == nil ||
		tolerant.view.App.Kind != "find" || tolerant.view.Outcome != "" ||
		!tolerant.view.App.HasItems || tolerant.view.App.Items == nil {
		p.reporter.fail("app_admission", "redundant_member_not_tolerated",
			field("code", codeOf(tolerant.view)), field("outcome", tolerant.view.Outcome))
		return false
	}
	// The read-only query consumed nothing: the session reference still serves a
	// task listing after the tolerant call.
	after, failure := p.call(ctx, "app", map[string]any{
		"kind": "list", "reference": reference,
	})
	if failure != nil {
		p.reporter.fail("app_admission", failure.reason, failure.fields...)
		return false
	}
	if after.result.IsError || after.view.App == nil || after.view.App.Kind != "list" {
		p.reporter.fail("app_admission", "reference_unusable_after_tolerant_find",
			field("code", codeOf(after.view)))
		return false
	}
	p.reporter.pass("app_admission", "kind=find", "cross_member=reference",
		"is_error=false", "outcome=none")
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

// checkQualityTextLine proves the v4 text projection never carries a quality
// token, regardless of the structured quality value.
func checkQualityTextLine(projected *callResult) *checkError {
	for _, text := range projected.texts {
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(line, "obs ") {
				continue
			}
			for _, token := range strings.Fields(line) {
				if strings.HasPrefix(token, "quality=") {
					return failf("text_line_unexpected", field("line", "quality"))
				}
			}
		}
	}
	return nil
}

// checkEffectTextLine proves the v4 text projection never carries the
// structured-only effect fact.
func checkEffectTextLine(projected *callResult) *checkError {
	for _, text := range projected.texts {
		for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			if strings.HasPrefix(line, "effect=") || strings.HasPrefix(line, "effect.") {
				return failf("text_line_unexpected", field("line", "effect"))
			}
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
// waitForCheck runs the shortest legal condition wait in pure-sleep form. It
// proves the tool is reachable, that a timeout is a success with a published
// frame, and that the frozen trigger and the elapsed fact are both carried.
func (p *probe) waitForCheck(ctx context.Context, reference string) (string, bool) {
	projected, failure := p.call(ctx, "wait_for", map[string]any{
		"reference": reference, "timeout": 1,
	})
	if failure != nil {
		p.reporter.fail("wait_for", failure.reason, failure.fields...)
		return "", false
	}
	view := projected.view
	if projected.result.IsError {
		p.reporter.fail("wait_for", "tool_is_error", field("code", codeOf(view)))
		return "", false
	}
	if view.Wait == nil {
		p.reporter.fail("wait_for", "wait_fact_missing")
		return "", false
	}
	if view.Wait.Trigger != waitTriggerTimeout {
		p.reporter.fail("wait_for", "trigger_unexpected", field("trigger", view.Wait.Trigger))
		return "", false
	}
	if len(view.Wait.Matched) != 1 || view.Wait.Matched[0] != waitTriggerTimeout {
		p.reporter.fail("wait_for", "matched_unexpected",
			field("matched", strconv.Itoa(len(view.Wait.Matched))))
		return "", false
	}
	if view.Wait.ElapsedMS < 1000 {
		p.reporter.fail("wait_for", "elapsed_below_window",
			field("ms", strconv.FormatInt(view.Wait.ElapsedMS, 10)))
		return "", false
	}
	observation := view.Observation
	if observation == nil || !validReference(observation.ID, 'O') {
		p.reporter.fail("wait_for", "observation_missing")
		return "", false
	}
	quality := observation.Quality
	qualityText := "omitted"
	if projected.textOnly {
		if failure := checkObservationQualityOmitted(projected); failure != nil {
			p.reporter.fail("wait_for", failure.reason, failure.fields...)
			return "", false
		}
		if failure := checkQualityTextLine(projected); failure != nil {
			p.reporter.fail("wait_for", failure.reason, failure.fields...)
			return "", false
		}
	} else {
		if quality == nil {
			p.reporter.fail("wait_for", "quality_unexpected", field("state", qualityStateOf(quality)))
			return "", false
		}
		if quality.State != qualityUnknown ||
			quality.Samples != 0 || quality.StopReason != qualityStopNotSampled {
			p.reporter.fail("wait_for", "quality_unexpected",
				field("state", qualityStateOf(quality)))
			return "", false
		}
		qualityText = quality.State + "/" + quality.StopReason
		if p.mode() == resultModeBoth {
			if failure := checkQualityTextLine(projected); failure != nil {
				p.reporter.fail("wait_for", failure.reason, failure.fields...)
				return "", false
			}
		}
	}
	p.reporter.pass("wait_for",
		field("trigger", view.Wait.Trigger),
		field("matched", strconv.Itoa(len(view.Wait.Matched))),
		field("ms", strconv.FormatInt(view.Wait.ElapsedMS, 10)),
		field("id", observation.ID), field("status", observation.Status),
		field("quality", qualityText))
	return observation.ID, true
}

// qualityStateOf keeps a nil quality distinguishable in a failure field.
func qualityStateOf(quality *qualityView) string {
	if quality == nil {
		return "missing"
	}
	return quality.State
}

func (p *probe) inspectCheck(ctx context.Context, observationID string) bool {
	projected, failure := p.call(ctx, "inspect", map[string]any{"reference": observationID})
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
		if p.mode() != resultModeStructured && !hasInspectBlock(projected) {
			p.reporter.fail("inspect", "text_inspect_block_missing")
			return false
		}
		p.reporter.fail("inspect", "inspection_missing")
		return false
	}
	if p.mode() == resultModeStructured {
		p.reporter.pass("inspect",
			field("scope", view.Inspection.Scope), field("observation", observationID),
			"structured=true")
		return true
	}
	if failure := checkInspectionTreeBlock(projected, view.Inspection, observationID); failure != nil {
		p.reporter.fail("inspect", failure.reason, failure.fields...)
		return false
	}
	p.reporter.pass("inspect",
		field("scope", view.Inspection.Scope), field("observation", observationID),
		"inspect_block=complete", "tree_block=v3")
	return true
}

func checkInspectionTreeBlock(projected *callResult, inspection *inspectionView,
	observationID string) *checkError {
	if inspection == nil {
		return failf("text_inspect_block_missing")
	}
	prefix := "inspect v3 scope=" + inspection.Scope + " observation=" + observationID
	block, failure := treeBlockText(projected, prefix)
	if failure != nil {
		return failure
	}
	if strings.Contains(block, " omitted ") {
		return failf("inspect_block_omitted", field("scope", inspection.Scope))
	}
	if !strings.HasSuffix(block, "end inspect\n") {
		return failf("inspect_block_unterminated", field("scope", inspection.Scope))
	}
	return nil
}

func hasInspectBlock(projected *callResult) bool {
	for _, text := range projected.texts {
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "inspect v3 ") {
				return true
			}
		}
	}
	return false
}

// actCheck consumes the observed O with a key action and asserts the post
// observation and its image. A refused key is a controlled failure, never a
// retry.
func (p *probe) actCheck(ctx context.Context, observationID string) bool {
	projected, failure := p.call(ctx, "act", map[string]any{
		"reference": observationID,
		"operation": map[string]any{"type": "key", "key": "BACK"},
		"image":     true,
	})
	if failure != nil {
		p.reporter.fail("act", failure.reason, failure.fields...)
		return false
	}
	view := projected.view
	if resultOutcome(projected) != outcomeExecuted {
		code := codeOf(view)
		outcome := resultOutcome(projected)
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
	if view.TextInput != nil {
		p.reporter.fail("act", "key_carries_text_input")
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
	if failure := checkScrollEffect(projected, false); failure != nil {
		p.reporter.fail("act", failure.reason, failure.fields...)
		return false
	}
	if p.mode() != resultModeStructured {
		if failure := checkQualityTextLine(projected); failure != nil {
			p.reporter.fail("act", failure.reason, failure.fields...)
			return false
		}
	}
	qualityText, samplesText, stopText := "omitted", "omitted", "omitted"
	if observation.Quality != nil {
		qualityText = observation.Quality.State
		samplesText = strconv.Itoa(observation.Quality.Samples)
		stopText = observation.Quality.StopReason
	}
	p.reporter.pass("act",
		"outcome=executed", "key=BACK", field("id", observation.ID),
		field("status", observation.Status), "image_mime="+imageJPEG,
		field("image_bytes", strconv.Itoa(len(raw))),
		field("width", strconv.Itoa(observation.Image.Width)),
		field("height", strconv.Itoa(observation.Image.Height)),
		field("quality", qualityText),
		field("samples", samplesText),
		field("stop_reason", stopText),
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
	if resultOutcome(projected) != outcomeExecuted {
		p.reporter.fail("end", "outcome_not_executed", field("outcome", resultOutcome(projected)), field("code", codeOf(view)))
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

// cleanupOwnedSession is used only after startCheck returned the exact P this
// probe created. It preserves cleanup when the check context was cancelled and
// performs at most this one fallback end.
func (p *probe) cleanupOwnedSession(parent context.Context, startID string) {
	if p.session == nil || !validReference(startID, 'P') {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	projected, failure := p.call(ctx, "end", map[string]any{"reference": startID})
	if failure != nil {
		p.reporter.fail("cleanup_owned_session", failure.reason, failure.fields...)
		return
	}
	if projected.result.IsError || resultOutcome(projected) != outcomeExecuted ||
		projected.view.Session == nil || projected.view.Session.State != sessionEnded {
		p.reporter.fail("cleanup_owned_session", "not_confirmed",
			field("code", codeOf(projected.view)),
			field("outcome", resultOutcome(projected)),
			field("session", sessionStateOf(projected.view.Session)))
		return
	}
	p.reporter.pass("cleanup_owned_session",
		field("P", startID), "outcome=executed", "session=ended")
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
