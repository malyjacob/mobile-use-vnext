package mcp

import (
	"bytes"
	jsonv2 "encoding/json/v2"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

type rmfWireField struct {
	goPath         string
	wirePath       string
	classification resultFieldKind
	coveredBy      string
	tuple          bool
}

type rmfManifest struct {
	fields     []rmfWireField
	classPaths map[string]struct{}
	exclusions map[string]string
	problems   []string
}

type rmfFixture struct {
	name   string
	result protocol.Result
}

var rmfKnownExclusions = map[string]string{
	"protocol.Result.IncludeSession":          "presence flag",
	"protocol.Foreground.HasTask":             "presence flag",
	"protocol.InspectionView.HasTotal":        "presence flag",
	"protocol.AppView.HasTotal":               "presence flag",
	"protocol.AppView.HasStatePreserved":      "presence flag",
	"protocol.AppView.HasItems":               "presence flag",
	"protocol.AppCloseTask.HasRecentsCleaned": "presence flag",
	"protocol.AppView.HasTasks":               "presence flag",
	"protocol.AppView.HasTargetPIDs":          "presence flag",
	"protocol.AppView.HasKilledPIDs":          "presence flag",
	"protocol.AppView.HasRemainingPIDs":       "presence flag",
	"protocol.AppView.HasRestarted":           "presence flag",
	"protocol.ImageMeta.Base64":               "transport-only",
	"protocol.ReadTreeValue.Full":             "variant wrapper",
	"protocol.ReadTreeValue.Delta":            "variant wrapper",
	// A read node carries exactly one definition anchor: its own ref or the
	// consecutive refs a chain folds. The chain form is never the root of a
	// complete tree, an add or a replace, so it cannot appear on every scanned
	// definition path; the composite inventory still pins both members.
	"protocol.ReadNode.Chain": "definition anchor variant",
	// ReadBounds selects the wire geometry form (null, "hidden" or one
	// rectangle). Only the rectangle is a tuple that needs per-member coverage.
	"protocol.ReadBounds.Kind": "public geometry variant",
	"protocol.Field.Set":       "generic Field wrapper",
	"protocol.Field.Value":     "generic Field wrapper",
}

// Explicit inventories close the reflection loophole where a new field always
// emitted by a parent's manual marshaler would otherwise inherit fieldJSON or
// fieldBlock and appear in an existing fixture without review. Classification
// still comes exclusively from resultFields; this inventory adds no codec.
var rmfCompositeInventory = map[string]string{
	"Screen":          "Width Height Rotation",
	"Foreground":      "State Package TaskID HasTask Reason",
	"ImageMeta":       "CapturedAt Width Height MediaType ReadOnly Screen Error Base64",
	"ReadTree":        "Format Scope View Root",
	"ReadNode":        "Ref Chain Role Bounds Label Actions Enabled Focused Selected Protected Checked Value Hint Axis Resource Context Children",
	"ReadDelta":       "Format Base View Set Add Remove Children Replace",
	"ReadSetNode":     "Label Actions Bounds Enabled Focused Selected Checked Value Hint Protected Axis Resource",
	"ReadBounds":      "Kind Rect",
	"AppItem":         "Name Package",
	"AppTaskItem":     "TaskID Package Ownership OriginDisplay",
	"AppCloseTask":    "TaskID Outcome RecentsCleaned HasRecentsCleaned Error",
	"ShellView":       "Termination ExitCode Output Truncated OutputError",
	"InspectionView":  "ObservationID Origin Revision CapturedAt Scope View Tree Root Matches Total HasTotal Truncated",
	"InspectionMatch": "Node Path",
	"DiagnosisIssue":  "Phase Code",
	"DiagnosisEvent":  "At Phase Code",
	"Error":           "Code Message Details",
	"Quality":         "State Samples StopReason",
	"Effect":          "State",
	"TextInputView":   "Write Enter",
	"WaitResult":      "Trigger Matched ElapsedMS",
}

func TestResultFieldManifest(t *testing.T) {
	manifest := rmfAnalyzeManifest()
	if len(manifest.problems) != 0 {
		sort.Strings(manifest.problems)
		t.Fatalf("result field manifest failed:\n%s", strings.Join(manifest.problems, "\n"))
	}

	for key := range resultFields {
		if _, ok := manifest.classPaths[key]; !ok {
			t.Errorf("resultFields contains %q without a reflected wire field", key)
		}
	}

	requiredPresence := map[string]string{
		"protocol.Result.IncludeSession":     "session",
		"protocol.Foreground.HasTask":        "observation.foreground.task_id",
		"protocol.InspectionView.HasTotal":   "inspection.total",
		"protocol.AppView.HasTotal":          "app.total",
		"protocol.AppView.HasStatePreserved": "app.state_preserved",
		"protocol.AppView.HasTasks":          "app.tasks",
		"protocol.AppView.HasTargetPIDs":     "app.target_pids",
		"protocol.AppView.HasKilledPIDs":     "app.killed_pids",
		"protocol.AppView.HasRemainingPIDs":  "app.remaining_pids",
		"protocol.AppView.HasRestarted":      "app.restarted",
	}
	for exclusion, fieldPath := range requiredPresence {
		if manifest.exclusions[exclusion] != "presence flag" {
			t.Errorf("%s is not declared as a presence flag", exclusion)
		}
		if !rmfHasWirePath(manifest.fields, fieldPath) {
			t.Errorf("%s does not map to reflected wire field %s", exclusion, fieldPath)
		}
	}
	if manifest.exclusions["protocol.ImageMeta.Base64"] != "transport-only" {
		t.Error("ImageMeta.Base64 is not declared transport-only")
	}
	for _, exclusion := range []string{"protocol.ReadTreeValue.Full", "protocol.ReadTreeValue.Delta"} {
		if manifest.exclusions[exclusion] != "variant wrapper" {
			t.Errorf("%s is not declared as a ReadTreeValue variant wrapper", exclusion)
		}
	}
	if manifest.exclusions["protocol.ReadNode.Chain"] != "definition anchor variant" {
		t.Error("ReadNode.Chain is not declared as the definition anchor variant")
	}
	if manifest.exclusions["protocol.ReadBounds.Kind"] != "public geometry variant" {
		t.Error("ReadBounds.Kind is not declared as the public geometry variant")
	}
	for _, exclusion := range []string{"protocol.Field.Set", "protocol.Field.Value"} {
		if manifest.exclusions[exclusion] != "generic Field wrapper" {
			t.Errorf("%s is not declared as a generic Field wrapper", exclusion)
		}
	}

	boundsType := reflect.TypeOf(protocol.Bounds{})
	wantBounds := []string{"Left", "Top", "Right", "Bottom"}
	if boundsType.NumField() != len(wantBounds) {
		t.Fatalf("Bounds has %d fields, want tuple fields %v", boundsType.NumField(), wantBounds)
	}
	for index, name := range wantBounds {
		if got := boundsType.Field(index).Name; got != name {
			t.Errorf("Bounds field %d = %s, want %s", index, got, name)
		}
	}
}

func TestCanonicalJSONCoversManifest(t *testing.T) {
	manifest := rmfAnalyzeManifest()
	if len(manifest.problems) != 0 {
		sort.Strings(manifest.problems)
		t.Fatalf("result field manifest failed:\n%s", strings.Join(manifest.problems, "\n"))
	}

	paths := map[string]struct{}{}
	canonicalOutputs := make([][]byte, 0)
	for _, fixture := range rmfResultFixtures() {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			value, err := canonicalResult(&execution{result: &fixture.result})
			if err != nil {
				t.Fatalf("canonicalResult: %v", err)
			}
			canonicalOutputs = append(canonicalOutputs, bytes.Clone(value))
			rmfCollectJSONPaths(t, value, paths)
		})
	}

	missing := make([]string, 0)
	for _, field := range manifest.fields {
		if !rmfJSONPathPresent(field.wirePath, paths) {
			missing = append(missing, fmt.Sprintf("%s -> %s", field.goPath, field.wirePath))
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		t.Fatalf("canonical fixtures omit manifest fields:\n%s", strings.Join(missing, "\n"))
	}

	for path := range paths {
		if path == "observation.image.base64" || strings.HasSuffix(path, ".base64") {
			t.Fatalf("canonical JSON retained transport-only field %q", path)
		}
	}
	joined := bytes.Join(canonicalOutputs, []byte{'\n'})
	for _, format := range []string{`"format":"full"`, `"format":"delta"`} {
		if !bytes.Contains(joined, []byte(format)) {
			t.Errorf("canonical fixtures do not cover TreeValue variant %s", format)
		}
	}
	for _, field := range []string{
		"app.items[*].name",
		"app.items[*].package",
		"diagnosis.timeline[*].at",
		"diagnosis.timeline[*].phase",
		"diagnosis.timeline[*].code",
	} {
		if !rmfJSONPathPresent(field, paths) {
			t.Errorf("manual parent marshaler omitted %s", field)
		}
	}
}

func TestCanonicalJSONDeterministic(t *testing.T) {
	var first []byte
	for iteration := 0; iteration < 32; iteration++ {
		got, err := canonicalResult(&execution{result: rmfDeterministicResult(iteration)})
		if err != nil {
			t.Fatalf("canonicalResult iteration %d: %v", iteration, err)
		}
		if iteration == 0 {
			first = bytes.Clone(got)
			continue
		}
		if !bytes.Equal(got, first) {
			t.Fatalf("canonicalResult iteration %d is not byte-deterministic\nfirst: %s\n got: %s", iteration, first, got)
		}
	}
}

func TestCancellationDetailsParity(t *testing.T) {
	tools := []struct {
		name     string
		readOnly bool
	}{
		{name: "work", readOnly: false},
		{name: "read_only", readOnly: true},
	}
	bases := []struct {
		name       string
		build      func(readOnly bool) *protocol.Result
		serviceErr bool
		noResult   bool
	}{
		{
			name: "service_result_without_error",
			build: func(readOnly bool) *protocol.Result {
				if readOnly {
					return &protocol.Result{OperationState: "not_seen"}
				}
				return &protocol.Result{OperationID: "P1", Outcome: "executed"}
			},
		},
		{
			name:     "no_service_result",
			build:    func(bool) *protocol.Result { return nil },
			noResult: true,
		},
		{
			name: "service_error",
			build: func(bool) *protocol.Result {
				return &protocol.Result{
					OperationID: "P1",
					Outcome:     "uncertain",
					Error: &protocol.Error{
						Code:    protocol.CodeUnauthorized,
						Message: "服务错误",
						Details: map[string]any{
							"phase":                  "service_error",
							"nearest_actionable_ref": "n516",
							"safe_point":             map[string]any{"x": 0, "y": 0},
							"task_ids":               []int{0, 42},
						},
					},
				}
			},
			serviceErr: true,
		},
	}

	for _, tool := range tools {
		tool := tool
		for _, base := range bases {
			base := base
			t.Run(tool.name+"/"+base.name, func(t *testing.T) {
				source := base.build(tool.readOnly)
				before := rmfResultBytes(t, source)
				execution := &execution{
					result:       source,
					service:      source != nil,
					cancelled:    true,
					cancellation: "测试取消",
				}
				canonical, err := canonicalResult(execution)
				if err != nil {
					t.Fatalf("canonicalResult: %v", err)
				}

				code, message, details := rmfDecodeCanonicalError(t, canonical)
				if base.serviceErr {
					if code != protocol.CodeUnauthorized || message != "服务错误" {
						t.Fatalf("canonical error = %s/%q, existing service error was not preserved", code, message)
					}
					if details["phase"] != "service_error" {
						t.Fatalf("canonical details lost existing detail: %#v", details)
					}
					safePoint, ok := details["safe_point"].(map[string]any)
					if !ok || safePoint["x"] != float64(0) || safePoint["y"] != float64(0) {
						t.Fatalf("canonical details lost nested safe_point: %#v", details)
					}
					taskIDs, ok := details["task_ids"].([]any)
					if !ok || len(taskIDs) != 2 || taskIDs[0] != float64(0) || taskIDs[1] != float64(42) {
						t.Fatalf("canonical details lost numeric array: %#v", details)
					}
				} else {
					if code != "call_cancelled" || message != "测试取消" {
						t.Fatalf("canonical error = %s/%q, want synthesized call_cancelled", code, message)
					}
				}
				if cancelled, ok := details["mcp.cancelled"].(bool); !ok || !cancelled {
					t.Fatalf("mcp.cancelled = %#v, want true", details["mcp.cancelled"])
				}
				if got, ok := details["mcp.cancellation"].(string); !ok || got != "测试取消" {
					t.Fatalf("mcp.cancellation = %#v, want %q", details["mcp.cancellation"], "测试取消")
				}
				for key := range details {
					if key == "mcp.cancelled" || key == "mcp.cancellation" {
						continue
					}
					if source == nil || source.Error == nil {
						t.Fatalf("canonical added non-MCP detail %q", key)
					}
					if _, ok := source.Error.Details[key]; !ok {
						t.Fatalf("canonical added non-MCP detail %q", key)
					}
				}

				wantText := assertResultTextParity(t, canonical, true)
				textMode := projectExecution(execution, tool.name, ResultModeText)
				bothMode := projectExecution(execution, tool.name, ResultModeBoth)
				structuredMode := projectExecution(execution, tool.name, ResultModeStructured)
				for modeName, projected := range map[string]bool{
					"text":       textMode.IsError,
					"both":       bothMode.IsError,
					"structured": structuredMode.IsError,
				} {
					if !projected {
						t.Errorf("%s mode IsError = false, want true", modeName)
					}
				}
				if textMode.StructuredContent != nil {
					t.Fatal("text mode unexpectedly retained StructuredContent")
				}
				if got := testTextContent(t, textMode); got != wantText {
					t.Errorf("text mode did not encode the canonical value")
				}
				if got := testTextContent(t, bothMode); got != wantText {
					t.Errorf("both mode text differs from the canonical encoding")
				}
				if !bytes.Equal(rmfStructuredBytes(t, bothMode.StructuredContent), canonical) {
					t.Errorf("both mode StructuredContent differs from canonical")
				}
				if !bytes.Equal(rmfStructuredBytes(t, structuredMode.StructuredContent), canonical) {
					t.Errorf("structured mode StructuredContent differs from canonical")
				}
				if got := testTextContent(t, structuredMode); !strings.Contains(got, `"mcp.cancelled"`) {
					t.Errorf("structured error diagnostic omits mcp.cancelled: %s", got)
				}

				if got := rmfResultBytes(t, source); !bytes.Equal(got, before) {
					t.Fatalf("canonical/projection mutated the original result\nbefore: %s\n after: %s", before, got)
				}
				if source != nil && source.Error != nil {
					if _, leaked := source.Error.Details["mcp.cancelled"]; leaked {
						t.Fatal("canonical cancellation mutated Error.Details")
					}
				}
			})
		}
	}
}

func TestReadOnlyCancelWithoutError(t *testing.T) {
	source := &protocol.Result{OperationState: "not_seen"}
	before := rmfResultBytes(t, source)
	execution := &execution{
		result:       source,
		service:      true,
		cancelled:    true,
		cancellation: "只读调用已取消",
	}
	canonical, err := canonicalResult(execution)
	if err != nil {
		t.Fatalf("canonicalResult: %v", err)
	}
	code, message, details := rmfDecodeCanonicalError(t, canonical)
	if code != "call_cancelled" || message != "只读调用已取消" {
		t.Fatalf("canonical error = %s/%q", code, message)
	}
	if details["mcp.cancelled"] != true || details["mcp.cancellation"] != "只读调用已取消" {
		t.Fatalf("canonical cancellation details = %#v", details)
	}

	textMode := projectExecution(execution, "status", ResultModeText)
	bothMode := projectExecution(execution, "status", ResultModeBoth)
	structuredMode := projectExecution(execution, "status", ResultModeStructured)
	for name, projected := range map[string]bool{
		"text":       textMode.IsError,
		"both":       bothMode.IsError,
		"structured": structuredMode.IsError,
	} {
		if !projected {
			t.Errorf("%s IsError = false, want cancellation override", name)
		}
	}
	if textMode.StructuredContent != nil {
		t.Fatal("text mode unexpectedly retained StructuredContent")
	}
	if !bytes.Equal(rmfStructuredBytes(t, bothMode.StructuredContent), canonical) ||
		!bytes.Equal(rmfStructuredBytes(t, structuredMode.StructuredContent), canonical) {
		t.Fatal("both/structured modes do not expose the canonical cancellation value")
	}
	if !strings.HasPrefix(testTextContent(t, textMode), "result v2 is_error=true\n") {
		t.Fatal("text mode did not use result v2 cancellation override")
	}
	if !strings.HasPrefix(testTextContent(t, structuredMode), "diagnostic v1 is_error=true\n") {
		t.Fatal("structured mode did not use diagnostic v1 cancellation override")
	}
	if !strings.Contains(testTextContent(t, bothMode), `"mcp.cancellation"`) {
		t.Fatal("text form omitted adapter cancellation details")
	}
	assertResultTextParity(t, canonical, true)
	if source.Error != nil {
		t.Fatalf("canonical/projection synthesized Error on the original result: %#v", source.Error)
	}
	if got := rmfResultBytes(t, source); !bytes.Equal(got, before) {
		t.Fatalf("canonical/projection mutated the read-only result\nbefore: %s\n after: %s", before, got)
	}
}

func TestAPINeverEmitsMCPDetails(t *testing.T) {
	result := &protocol.Result{
		Error: &protocol.Error{
			Code:    protocol.CodeUnauthorized,
			Message: "认证失败",
			Details: map[string]any{"phase": "authentication", "limit": 1},
		},
	}
	before := rmfResultBytes(t, result)
	apiJSON, err := protocol.JSON(result)
	if err != nil {
		t.Fatalf("protocol.JSON: %v", err)
	}
	if bytes.Contains(apiJSON, []byte("mcp.")) {
		t.Fatalf("protocol API JSON contains MCP adapter details: %s", apiJSON)
	}

	adapterJSON, err := canonicalResult(&execution{
		result:       result,
		cancelled:    true,
		cancellation: "取消",
	})
	if err != nil {
		t.Fatalf("canonicalResult: %v", err)
	}
	if !bytes.Contains(adapterJSON, []byte(`"mcp.cancelled":true`)) {
		t.Fatalf("adapter canonical JSON omitted cancellation details: %s", adapterJSON)
	}
	if got := rmfResultBytes(t, result); !bytes.Equal(got, before) {
		t.Fatalf("adapter projection mutated protocol result\nbefore: %s\n after: %s", before, got)
	}
	if _, ok := result.Error.Details["mcp.cancelled"]; ok {
		t.Fatal("adapter projection wrote mcp.* into the protocol result")
	}
}

func rmfAnalyzeManifest() rmfManifest {
	manifest := rmfManifest{
		classPaths: make(map[string]struct{}),
		exclusions: make(map[string]string),
	}
	var scanValue func(reflect.Type, string, string, map[reflect.Type]bool)
	scanValue = func(valueType reflect.Type, wirePath, coveredBy string, stack map[reflect.Type]bool) {
		valueType = rmfIndirectType(valueType)
		if valueType == nil {
			return
		}

		if valueType.PkgPath() == reflect.TypeOf(protocol.Result{}).PkgPath() &&
			strings.HasPrefix(valueType.Name(), "Field[") {
			rmfRecordExclusion(&manifest, "protocol.Field.Set")
			rmfRecordExclusion(&manifest, "protocol.Field.Value")
			if valueType.NumField() != 2 ||
				valueType.Field(0).Name != "Set" ||
				valueType.Field(1).Name != "Value" {
				manifest.problems = append(manifest.problems, "generic protocol.Field wrapper shape changed")
				return
			}
			scanValue(valueType.Field(1).Type, wirePath, coveredBy, stack)
			return
		}

		if valueType.PkgPath() == reflect.TypeOf(protocol.Result{}).PkgPath() && valueType.Name() == "ReadTreeValue" {
			rmfRecordExclusion(&manifest, "protocol.ReadTreeValue.Full")
			rmfRecordExclusion(&manifest, "protocol.ReadTreeValue.Delta")
			if valueType.NumField() != 2 ||
				valueType.Field(0).Name != "Full" ||
				valueType.Field(1).Name != "Delta" {
				manifest.problems = append(manifest.problems, "ReadTreeValue variant wrapper shape changed")
				return
			}
			scanValue(valueType.Field(0).Type, wirePath, coveredBy, stack)
			scanValue(valueType.Field(1).Type, wirePath, coveredBy, stack)
			return
		}

		// ReadBounds selects the wire geometry form: null, the controlled
		// string "hidden" or one rectangle tuple. Only the rectangle carries
		// delivered members, and it is encoded at the bounds position itself.
		if valueType == reflect.TypeOf(protocol.ReadBounds{}) {
			rmfRecordExclusion(&manifest, "protocol.ReadBounds.Kind")
			if valueType.NumField() != 2 ||
				valueType.Field(0).Name != "Kind" ||
				valueType.Field(1).Name != "Rect" {
				manifest.problems = append(manifest.problems, "ReadBounds public geometry shape changed")
				return
			}
			scanValue(valueType.Field(1).Type, wirePath, coveredBy, stack)
			return
		}

		if valueType == reflect.TypeOf(protocol.Bounds{}) {
			for index := 0; index < valueType.NumField(); index++ {
				field := valueType.Field(index)
				manifest.fields = append(manifest.fields, rmfWireField{
					goPath:    "protocol.Bounds." + field.Name,
					wirePath:  wirePath + "[*]",
					coveredBy: coveredBy,
					tuple:     true,
				})
			}
			return
		}

		switch valueType.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map:
			scanValue(valueType.Elem(), wirePath+"[*]", coveredBy, stack)
		case reflect.Struct:
			if stack[valueType] {
				return
			}
			stack[valueType] = true
			defer delete(stack, valueType)
			if coveredBy != "" {
				inventory, ok := rmfCompositeInventory[valueType.Name()]
				var names []string
				for i := 0; i < valueType.NumField(); i++ {
					if valueType.Field(i).PkgPath == "" {
						names = append(names, valueType.Field(i).Name)
					}
				}
				if !ok || strings.Join(names, " ") != inventory {
					manifest.problems = append(manifest.problems, fmt.Sprintf("%s composite inventory changed; classify and fixture every field: %s", valueType, strings.Join(names, " ")))
				}
			}
			for index := 0; index < valueType.NumField(); index++ {
				field := valueType.Field(index)
				if field.PkgPath != "" {
					continue
				}
				fieldWireName, ok := rmfFieldWireName(valueType, field)
				if !ok {
					continue
				}
				goPath := rmfTypePath(valueType) + "." + field.Name
				fieldPath := rmfJoinPath(wirePath, fieldWireName)
				if _, excluded := rmfKnownExclusions[goPath]; excluded {
					rmfRecordExclusion(&manifest, goPath)
					continue
				}

				kind, classified := resultFields[fieldPath]
				if classified {
					manifest.classPaths[fieldPath] = struct{}{}
					if problem := rmfClassificationTypeProblem(field.Type, kind); problem != "" {
						manifest.problems = append(manifest.problems,
							fmt.Sprintf("%s at %s: %s", goPath, fieldPath, problem))
					}
				} else if coveredBy == "" {
					manifest.problems = append(manifest.problems,
						fmt.Sprintf("%s has no resultFields classification at %s", goPath, fieldPath))
				}

				childCoveredBy := coveredBy
				if classified && (kind == fieldJSON || kind == fieldBlock) {
					childCoveredBy = fieldPath
				}
				manifest.fields = append(manifest.fields, rmfWireField{
					goPath:         goPath,
					wirePath:       fieldPath,
					classification: kind,
					coveredBy:      childCoveredBy,
				})
				scanValue(field.Type, fieldPath, childCoveredBy, stack)
			}
		}
	}

	scanValue(reflect.TypeOf(protocol.Result{}), "", "", make(map[reflect.Type]bool))
	sort.Slice(manifest.fields, func(i, j int) bool {
		if manifest.fields[i].goPath == manifest.fields[j].goPath {
			return manifest.fields[i].wirePath < manifest.fields[j].wirePath
		}
		return manifest.fields[i].goPath < manifest.fields[j].goPath
	})

	for exclusion := range rmfKnownExclusions {
		if _, observed := manifest.exclusions[exclusion]; !observed {
			manifest.problems = append(manifest.problems, fmt.Sprintf("declared exclusion %s was not reflected", exclusion))
		}
	}
	for exclusion, reason := range manifest.exclusions {
		want, declared := rmfKnownExclusions[exclusion]
		if !declared {
			manifest.problems = append(manifest.problems, fmt.Sprintf("new reflected wrapper/flag %s (%s) is not declared", exclusion, reason))
			continue
		}
		if reason != want {
			manifest.problems = append(manifest.problems,
				fmt.Sprintf("%s exclusion reason = %q, want %q", exclusion, reason, want))
		}
	}
	return manifest
}

func rmfRecordExclusion(manifest *rmfManifest, goPath string) {
	reason, ok := rmfKnownExclusions[goPath]
	if !ok {
		reason = "undeclared wrapper"
		manifest.problems = append(manifest.problems, fmt.Sprintf("unknown reflected wrapper %s", goPath))
	}
	manifest.exclusions[goPath] = reason
}

func rmfClassificationTypeProblem(fieldType reflect.Type, kind resultFieldKind) string {
	fieldType = rmfIndirectType(fieldType)
	if fieldType == nil {
		return ""
	}
	switch fieldType.Kind() {
	case reflect.String:
		if kind != fieldToken && kind != fieldString {
			return fmt.Sprintf("string field uses %s, want fieldToken or fieldString", rmfFieldKindName(kind))
		}
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		if kind != fieldLiteral {
			return fmt.Sprintf("boolean/numeric field uses %s, want fieldLiteral", rmfFieldKindName(kind))
		}
	}
	return ""
}

func rmfFieldKindName(kind resultFieldKind) string {
	switch kind {
	case fieldToken:
		return "fieldToken"
	case fieldString:
		return "fieldString"
	case fieldJSON:
		return "fieldJSON"
	case fieldObject:
		return "fieldObject"
	case fieldBlock:
		return "fieldBlock"
	case fieldLiteral:
		return "fieldLiteral"
	default:
		return fmt.Sprintf("resultFieldKind(%d)", kind)
	}
}

func rmfFieldWireName(owner reflect.Type, field reflect.StructField) (string, bool) {
	goPath := rmfTypePath(owner) + "." + field.Name
	switch goPath {
	case "protocol.Result.StartOperation":
		return "start_operation_id", true
	case "protocol.DiagnosisView.Operation":
		return "operation_id", true
	case "protocol.AppView.TaskItems":
		return "items", true
	case "protocol.AppView.TargetPIDs":
		return "target_pids", true
	case "protocol.AppView.KilledPIDs":
		return "killed_pids", true
	case "protocol.AppView.RemainingPIDs":
		return "remaining_pids", true
	}
	if tag := field.Tag.Get("json"); tag != "" {
		name := strings.Split(tag, ",")[0]
		if name == "-" {
			return "", false
		}
		if name != "" {
			return name, true
		}
	}
	return rmfSnakeCase(field.Name), true
}

func rmfSnakeCase(name string) string {
	runes := []rune(name)
	var builder strings.Builder
	builder.Grow(len(name) + 4)
	for index, current := range runes {
		if unicode.IsUpper(current) && index > 0 {
			previous := runes[index-1]
			nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) || unicode.IsUpper(previous) && nextIsLower {
				builder.WriteByte('_')
			}
		}
		builder.WriteRune(unicode.ToLower(current))
	}
	return builder.String()
}

func rmfTypePath(valueType reflect.Type) string {
	valueType = rmfIndirectType(valueType)
	if valueType == nil {
		return "<nil>"
	}
	if valueType.PkgPath() == reflect.TypeOf(protocol.Result{}).PkgPath() {
		return "protocol." + valueType.Name()
	}
	return valueType.String()
}

func rmfIndirectType(valueType reflect.Type) reflect.Type {
	for valueType != nil && valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	return valueType
}

func rmfJoinPath(base, member string) string {
	if base == "" {
		return member
	}
	return base + "." + member
}

func rmfHasWirePath(fields []rmfWireField, path string) bool {
	for _, field := range fields {
		if field.wirePath == path {
			return true
		}
	}
	return false
}

func rmfResultFixtures() []rmfFixture {
	fullTree := rmfFullTreeValue("n1")
	imageError := &protocol.Error{
		Code:    protocol.CodeImageCapture,
		Message: "图片不可用",
		Details: map[string]any{"phase": "image"},
	}
	full := rmfFixture{
		name: "full_and_all_top_level",
		result: protocol.Result{
			OperationID:         "P1",
			OperationState:      "claimed",
			Outcome:             "executed",
			Phase:               "completed",
			Effect:              &protocol.Effect{State: protocol.EffectObserved},
			TextInput:           &protocol.TextInputView{Write: protocol.TextInputExecuted, Enter: protocol.TextInputExecuted},
			TargetReference:     "O1",
			RequestClosed:       rmfPtr(false),
			BlockingOperationID: "P2",
			CanStart:            rmfPtr(false),
			BlockingCode:        "device_busy",
			StartOperation:      "P3",
			ObservationState:    "effective",
			InputState:          "effective",
			MessageID:           "M1",
			Saved:               rmfPtr(false),
			Wait: &protocol.WaitResult{
				Trigger:   protocol.WaitTriggerQuery,
				Matched:   []string{protocol.WaitTriggerQuery},
				ElapsedMS: 1500,
			},
			Session: &protocol.Session{
				Origin:    "P1",
				State:     "active",
				Mode:      "virtual",
				Agent:     "agent",
				StartedAt: "2026-11-10T00:00:00Z",
				ExpiresAt: "2026-11-10T00:10:00Z",
			},
			IncludeSession: true,
			Observation: &protocol.Observation{
				Status:          "complete",
				ID:              "O1",
				Origin:          "capture",
				Revision:        1,
				CapturedAt:      "2026-11-10T00:00:01Z",
				ActionableUntil: "2026-11-10T00:01:01Z",
				Quality: &protocol.Quality{
					State:      protocol.QualityStable,
					Samples:    3,
					StopReason: protocol.QualityStopStable,
				},
				Screen: &protocol.Screen{
					Width: 1080, Height: 1920, Rotation: 0,
				},
				Foreground: &protocol.Foreground{
					State:   "present",
					Package: "com.android.settings",
					TaskID:  rmfPtr(42),
					HasTask: true,
				},
				Tree: fullTree,
				Missing: []string{
					"optional",
				},
				Image: &protocol.ImageMeta{
					CapturedAt: "2026-11-10T00:00:01Z",
					Width:      360,
					Height:     640,
					MediaType:  "image/jpeg",
					ReadOnly:   true,
					Screen: &protocol.Screen{
						Width: 1080, Height: 1920, Rotation: 0,
					},
					Error:  imageError,
					Base64: "transport-only",
				},
				Error: &protocol.Error{
					Code:    protocol.CodeHierarchyFailed,
					Message: "部分观察失败",
					Details: map[string]any{"phase": "observation"},
				},
			},
			Inspection: &protocol.InspectionView{
				ObservationID: "O1",
				Origin:        "inspect",
				Revision:      1,
				CapturedAt:    "2026-11-10T00:00:01Z",
				Scope:         "tree",
				Tree:          rmfInspectionTree("n1"),
			},
			Shell: &protocol.ShellView{
				Termination: "exited",
				ExitCode:    rmfPtr(0),
				Output:      rmfPtr(""),
				Truncated:   rmfPtr(false),
				OutputError: &protocol.Error{
					Code:    protocol.CodeShellOutput,
					Message: "输出不完整",
					Details: map[string]any{"phase": "output"},
				},
			},
			Notification: &protocol.NotificationView{
				State: "saved",
				Error: &protocol.Error{
					Code:    protocol.CodeDeliveryUnavailable,
					Message: "控制端不可用",
					Details: map[string]any{"phase": "delivery"},
				},
			},
			Diagnosis: &protocol.DiagnosisView{
				Summary: "诊断",
				Issues: []protocol.DiagnosisIssue{
					{Phase: "capture", Code: "missing"},
				},
				TimingMS: map[string]int64{"capture": 7, "encode": 3},
				Timeline: []protocol.DiagnosisEvent{
					{At: "2026-11-10T00:00:03Z", Phase: "capture", Code: "complete"},
				},
				Coverage:  "request",
				Truncated: false,
				Operation: "P1",
				Outcome:   "executed",
			},
			Cleanup: &protocol.Cleanup{
				State:     protocol.CleanupPending,
				Execution: protocol.CleanupConfirmed,
				Tasks:     protocol.CleanupPending,
				Recents:   protocol.CleanupPending,
				Display:   protocol.CleanupPending,
				Error: &protocol.Error{
					Code:    protocol.CodeCleanupUnconfirmed,
					Message: "收尾未确认",
					Details: map[string]any{"phase": "cleanup"},
				},
			},
			Error: &protocol.Error{
				Code:    protocol.CodeActionUnknown,
				Message: "结果不确定",
				Details: map[string]any{
					"phase":                  "request",
					"nearest_actionable_ref": "n516",
					"safe_carrier_ref":       "n491",
					"safe_point":             map[string]any{"x": 0, "y": 0},
					"task_ids":               []int{0, 42},
				},
			},
			Recovery: &protocol.Recovery{
				Entry:      protocol.RecoveryEntryStatus,
				Reference:  protocol.RecoveryReferenceP,
				InputState: protocol.RecoveryInputEffective,
				Cause:      protocol.RecoveryCauseRunning,
			},
		},
	}

	delta := rmfFixture{
		name: "delta_and_ended_session",
		result: protocol.Result{
			OperationID: "P2",
			Outcome:     "executed",
			Session: &protocol.Session{
				Origin:    "P2",
				State:     "ended",
				Mode:      "virtual",
				Agent:     "agent",
				StartedAt: "2026-11-10T00:00:00Z",
				EndedAt:   "2026-11-10T00:05:00Z",
				Reason:    "client_end",
			},
			IncludeSession: true,
			Observation: &protocol.Observation{
				Status:          "partial",
				ID:              "O2",
				Origin:          "delta",
				Revision:        2,
				CapturedAt:      "2026-11-10T00:00:04Z",
				ActionableUntil: "2026-11-10T00:01:04Z",
				Foreground: &protocol.Foreground{
					State:  "unknown",
					Reason: "no_window",
				},
				Tree: &protocol.ReadTreeValue{Delta: rmfDelta("O1")},
			},
			Inspection: &protocol.InspectionView{
				ObservationID: "O2",
				Origin:        "inspect",
				Revision:      2,
				CapturedAt:    "2026-11-10T00:00:04Z",
				Scope:         "subtree",
				View:          protocol.ViewDetailed,
				Root:          rmfNode("n5", protocol.RoleButton, true),
			},
		},
	}

	query := rmfFixture{
		name: "inspection_query",
		result: protocol.Result{
			Outcome: "executed",
			Inspection: &protocol.InspectionView{
				ObservationID: "O3",
				Origin:        "inspect",
				Revision:      3,
				CapturedAt:    "2026-11-10T00:00:05Z",
				Scope:         "query",
				View:          protocol.ViewDetailed,
				Matches: []protocol.InspectionMatch{
					{
						Node: rmfNode("n6", protocol.RoleButton, true),
						Path: []string{"n1", "n6"},
					},
				},
				Total:     1,
				HasTotal:  true,
				Truncated: rmfPtr(false),
			},
		},
	}
	appFind := rmfFixture{
		name: "app_find",
		result: protocol.Result{
			OperationID: "P4",
			App: &protocol.AppView{
				Kind: protocol.AppKindFind,
				Items: []protocol.AppItem{
					{Name: "设置", Package: "com.android.settings"},
				},
				Total:      1,
				HasTotal:   true,
				Truncated:  rmfPtr(false),
				CapturedAt: "2026-11-10T00:00:06Z",
			},
		},
	}
	appList := rmfFixture{
		name: "app_list",
		result: protocol.Result{
			OperationID: "P5",
			App: &protocol.AppView{
				Kind:      protocol.AppKindList,
				DisplayID: rmfPtr(12),
				TaskItems: []protocol.AppTaskItem{
					{
						TaskID: 42, Package: "com.android.settings",
						Ownership: protocol.TaskOwnershipOwned,
					},
					{
						TaskID: 43, Package: "com.example.probe",
						Ownership: protocol.TaskOwnershipBorrowed, OriginDisplay: rmfPtr(0),
					},
				},
				Total:      2,
				HasTotal:   true,
				Truncated:  rmfPtr(false),
				CapturedAt: "2026-11-10T00:00:07Z",
				Foreground: &protocol.Foreground{
					State: "present", Package: "com.android.settings",
					TaskID: rmfPtr(42), HasTask: true,
				},
			},
		},
	}
	appListUnknownForeground := rmfFixture{
		name: "app_list_unknown_foreground",
		result: protocol.Result{
			OperationID: "P11",
			App: &protocol.AppView{
				Kind:       protocol.AppKindList,
				DisplayID:  rmfPtr(12),
				TaskItems:  []protocol.AppTaskItem{},
				Total:      0,
				HasTotal:   true,
				Truncated:  rmfPtr(false),
				CapturedAt: "2026-11-10T00:00:08Z",
				Foreground: &protocol.Foreground{State: "unknown", Reason: "window_read_failed"},
			},
		},
	}
	appOpen := rmfFixture{
		name: "app_open",
		result: protocol.Result{
			OperationID: "P6",
			Outcome:     "executed",
			App: &protocol.AppView{
				Kind: protocol.AppKindOpen, Package: "com.android.settings",
				TaskID: rmfPtr(42), DisplayID: rmfPtr(12),
				Ownership:      protocol.TaskOwnershipOwned,
				Reused:         rmfPtr(false),
				StatePreserved: rmfPtr(false), HasStatePreserved: true,
			},
		},
	}
	appClaim := rmfFixture{
		name: "app_claim",
		result: protocol.Result{
			OperationID: "P7",
			Outcome:     "executed",
			App: &protocol.AppView{
				Kind: protocol.AppKindClaim, Package: "com.example.probe",
				TaskID: rmfPtr(43), SourceDisplayID: rmfPtr(0), DisplayID: rmfPtr(12),
				Ownership:      protocol.TaskOwnershipBorrowed,
				StatePreserved: rmfPtr(true), HasStatePreserved: true,
			},
		},
	}
	appHandoff := rmfFixture{
		name: "app_handoff",
		result: protocol.Result{
			OperationID: "P8",
			Outcome:     "executed",
			App: &protocol.AppView{
				Kind: protocol.AppKindHandoff, Package: "com.example.probe",
				TaskID: rmfPtr(43), SourceDisplayID: rmfPtr(12), DisplayID: rmfPtr(0),
				Ownership:         protocol.TaskOwnershipShared,
				HasStatePreserved: true,
				ForegroundRestore: protocol.ForegroundRestoreUnconfirmed,
			},
		},
	}
	appClose := rmfFixture{
		name: "app_close",
		result: protocol.Result{
			OperationID: "P9",
			Outcome:     "uncertain",
			App: &protocol.AppView{
				Kind:      protocol.AppKindClose,
				DisplayID: rmfPtr(12),
				Tasks: []protocol.AppCloseTask{
					{TaskID: 42, Outcome: "executed", RecentsCleaned: rmfPtr(true), HasRecentsCleaned: true},
					{
						TaskID: 43, Outcome: "uncertain",
						RecentsCleaned: nil, HasRecentsCleaned: true,
						Error: &protocol.Error{
							Code:    protocol.CodeCleanupUnconfirmed,
							Message: "关闭结果未确认",
							Details: map[string]any{"phase": "close"},
						},
					},
				},
				HasTasks: true,
				Complete: rmfPtr(false),
			},
		},
	}
	appKill := rmfFixture{
		name: "app_kill",
		result: protocol.Result{
			OperationID: "P10",
			Outcome:     "executed",
			App: &protocol.AppView{
				Kind: protocol.AppKindKill, Package: "com.example.probe",
				SourceTaskID: rmfPtr(42), DisplayID: rmfPtr(12),
				TargetPIDs: []int{1234, 1235}, HasTargetPIDs: true,
				KilledPIDs: []int{1234, 1235}, HasKilledPIDs: true,
				RemainingPIDs: []int{}, HasRemainingPIDs: true,
				HasRestarted: true,
			},
		},
	}
	scrollNotObserved := rmfFixture{
		name: "scroll_effect_not_observed",
		result: protocol.Result{
			OperationID: "P12",
			Outcome:     "executed",
			Phase:       "completed",
			Effect:      &protocol.Effect{State: protocol.EffectNotObserved},
			Observation: &protocol.Observation{
				Status:          "complete",
				ID:              "O4",
				Origin:          "P12",
				Revision:        4,
				CapturedAt:      "2026-11-10T00:00:09Z",
				ActionableUntil: "2026-11-10T00:01:09Z",
				Quality: &protocol.Quality{
					State:      protocol.QualityStable,
					Samples:    5,
					StopReason: protocol.QualityStopMaxSamples,
				},
				Tree: rmfFullTreeValue("n1"),
			},
		},
	}
	scrollUnknownEffect := rmfFixture{
		name: "scroll_effect_unknown_unstable",
		result: protocol.Result{
			OperationID: "P13",
			Outcome:     "executed",
			Phase:       "completed",
			Effect:      &protocol.Effect{State: protocol.EffectUnknown},
			Observation: &protocol.Observation{
				Status:          "partial",
				ID:              "O5",
				Origin:          "P13",
				Revision:        5,
				CapturedAt:      "2026-11-10T00:00:10Z",
				ActionableUntil: "2026-11-10T00:01:10Z",
				Quality: &protocol.Quality{
					State:      protocol.QualityUnstable,
					Samples:    4,
					StopReason: protocol.QualityStopBudget,
				},
				Tree: rmfFullTreeValue("n1"),
			},
		},
	}
	notSampled := rmfFixture{
		name: "observation_quality_not_sampled",
		result: protocol.Result{
			OperationID: "P14",
			Outcome:     "executed",
			Observation: &protocol.Observation{
				Status:          "complete",
				ID:              "O6",
				Origin:          "observe",
				Revision:        6,
				CapturedAt:      "2026-11-10T00:00:11Z",
				ActionableUntil: "2026-11-10T00:01:11Z",
				Quality: &protocol.Quality{
					State:      protocol.QualityUnknown,
					Samples:    0,
					StopReason: protocol.QualityStopNotSampled,
				},
				Tree: rmfFullTreeValue("n1"),
			},
		},
	}
	return []rmfFixture{
		full, delta, query,
		appFind, appList, appListUnknownForeground, appOpen, appClaim, appHandoff, appClose, appKill,
		scrollNotObserved, scrollUnknownEffect, notSampled,
	}
}

// rmfFullTree builds one compact public tree. ReadTreeValue.Full is always a
// compact complete tree; inspection material is built by rmfInspectionTree.
func rmfFullTree(ref string) *protocol.ReadTree {
	return &protocol.ReadTree{
		Format: "full",
		Scope:  protocol.Scope,
		View:   protocol.ViewCompact,
		Root:   rmfNode(ref, protocol.RoleRoot, true),
	}
}

// rmfInspectionTree builds the detailed complete tree of the inspection tree
// scope: detailed view, no hidden geometry.
func rmfInspectionTree(ref string) *protocol.ReadTree {
	tree := rmfFullTree(ref)
	tree.View = protocol.ViewDetailed
	return tree
}

func rmfFullTreeValue(ref string) *protocol.ReadTreeValue {
	return &protocol.ReadTreeValue{Full: rmfFullTree(ref)}
}

func rmfDelta(base string) *protocol.ReadDelta {
	return &protocol.ReadDelta{
		Format: "delta",
		Base:   base,
		View:   protocol.ViewCompact,
		Set: map[string]protocol.ReadSetNode{
			"n1": rmfSetNode(),
			"n2": rmfSetNode(),
		},
		Add:    []*protocol.ReadNode{rmfNode("n4", protocol.RoleButton, true)},
		Remove: []string{"n9"},
		Children: map[string][]string{
			"n1": {"n2"},
		},
		Replace: map[string]*protocol.ReadNode{
			"n3": rmfNode("n3", protocol.RoleGroup, true),
		},
	}
}

func rmfSetNode() protocol.ReadSetNode {
	return protocol.ReadSetNode{
		Label:     protocol.Field[[]string]{Set: true, Value: []string{}},
		Actions:   protocol.Field[[]protocol.Action]{Set: true, Value: []protocol.Action{}},
		Bounds:    protocol.Field[protocol.ReadBounds]{Set: true, Value: protocol.KnownBounds(protocol.Bounds{Left: 1, Top: 2, Right: 3, Bottom: 4})},
		Enabled:   protocol.Field[*bool]{Set: true, Value: rmfPtr(false)},
		Focused:   protocol.Field[*bool]{Set: true, Value: nil},
		Selected:  protocol.Field[*bool]{Set: true, Value: rmfPtr(true)},
		Checked:   protocol.Field[*bool]{Set: true, Value: rmfPtr(false)},
		Value:     protocol.Field[*string]{Set: true, Value: rmfPtr("")},
		Hint:      protocol.Field[*string]{Set: true, Value: rmfPtr("")},
		Protected: protocol.Field[*bool]{Set: true, Value: nil},
		Axis:      protocol.Field[*protocol.Axis]{Set: true, Value: rmfPtr(protocol.AxisY)},
		Resource:  protocol.Field[*string]{Set: true, Value: rmfPtr("")},
	}
}

func rmfNode(ref string, role protocol.Role, withChildren bool) *protocol.ReadNode {
	node := &protocol.ReadNode{
		Ref:      ref,
		Role:     role,
		Bounds:   protocol.KnownBounds(protocol.Bounds{Left: 1, Top: 2, Right: 3, Bottom: 4}),
		Label:    []string{"label"},
		Actions:  []protocol.Action{protocol.ActionTap},
		Enabled:  nil,
		Focused:  nil,
		Selected: rmfPtr(true),
		// checked=false is the public default and is omitted; the fixture uses
		// a known true so the member appears in the canonical document.
		Checked:   rmfPtr(true),
		Value:     rmfPtr(""),
		Hint:      rmfPtr(""),
		Protected: nil,
		Axis:      protocol.AxisY,
		Resource:  rmfPtr(""),
		Context:   true,
	}
	if withChildren {
		node.Children = []*protocol.ReadNode{
			{
				Ref:      ref + "1",
				Role:     protocol.RoleText,
				Bounds:   protocol.KnownBounds(protocol.Bounds{Left: 5, Top: 6, Right: 7, Bottom: 8}),
				Label:    []string{"child"},
				Actions:  []protocol.Action{},
				Selected: rmfPtr(false),
				Checked:  rmfPtr(false),
			},
		}
	}
	return node
}

func rmfDeterministicResult(iteration int) *protocol.Result {
	keys := []string{"n3", "n1", "n2"}
	values := map[string]int64{"n3": 0, "n1": 1, "n2": 2}
	rotate := iteration % len(keys)
	details := make(map[string]any, len(keys))
	timing := make(map[string]int64, len(keys))
	set := make(map[string]protocol.ReadSetNode, len(keys))
	children := make(map[string][]string, len(keys))
	replace := make(map[string]*protocol.ReadNode, len(keys))
	for offset := range keys {
		key := keys[(offset+rotate)%len(keys)]
		details[key] = values[key]
		timing[key] = values[key]
		set[key] = rmfSetNode()
		children[key] = []string{"n1", "n2"}
		replace[key] = rmfNode(key, protocol.RoleButton, false)
	}
	details["safe_point"] = map[string]any{"x": 0, "y": 0}
	return &protocol.Result{
		OperationID: "P1",
		Error: &protocol.Error{
			Code:    protocol.CodeInternal,
			Message: "deterministic",
			Details: details,
		},
		Observation: &protocol.Observation{
			ID: "O1",
			Tree: &protocol.ReadTreeValue{Delta: &protocol.ReadDelta{
				Format:   "delta",
				Base:     "O0",
				View:     protocol.ViewCompact,
				Set:      set,
				Children: children,
				Replace:  replace,
			}},
		},
		Diagnosis: &protocol.DiagnosisView{
			Issues:   []protocol.DiagnosisIssue{},
			TimingMS: timing,
			Coverage: "request",
		},
	}
}

func rmfPtr[T any](value T) *T {
	return &value
}

func rmfResultBytes(t *testing.T, result *protocol.Result) []byte {
	t.Helper()
	if result == nil {
		return nil
	}
	value, err := jsonv2.Marshal(result, jsonv2.Deterministic(true))
	if err != nil {
		t.Fatalf("marshal result snapshot: %v", err)
	}
	return value
}

func rmfStructuredBytes(t *testing.T, value any) []byte {
	t.Helper()
	if value == nil {
		return nil
	}
	raw, err := jsonv2.Marshal(value, jsonv2.Deterministic(true))
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	return raw
}

func rmfDecodeCanonicalError(t *testing.T, raw []byte) (string, string, map[string]any) {
	t.Helper()
	var value map[string]any
	if err := jsonv2.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode canonical JSON: %v", err)
	}
	errorValue, ok := value["error"].(map[string]any)
	if !ok {
		t.Fatalf("canonical JSON has no error object: %s", raw)
	}
	code, _ := errorValue["code"].(string)
	message, _ := errorValue["message"].(string)
	details := map[string]any{}
	if rawDetails, ok := errorValue["details"].(map[string]any); ok {
		details = rawDetails
	}
	return code, message, details
}

func rmfCollectJSONPaths(t *testing.T, raw []byte, paths map[string]struct{}) {
	t.Helper()
	var value any
	if err := jsonv2.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode canonical fixture: %v", err)
	}
	var walk func(any, string)
	walk = func(current any, path string) {
		switch typed := current.(type) {
		case map[string]any:
			for name, child := range typed {
				childPath := rmfJoinPath(path, name)
				paths[childPath] = struct{}{}
				walk(child, childPath)
			}
		case []any:
			elementPath := path + "[*]"
			paths[elementPath] = struct{}{}
			for _, child := range typed {
				walk(child, elementPath)
			}
		}
	}
	walk(value, "")
}

func rmfJSONPathPresent(pattern string, paths map[string]struct{}) bool {
	want := strings.Split(pattern, ".")
	for path := range paths {
		if rmfPathSegmentsMatch(want, strings.Split(path, ".")) {
			return true
		}
	}
	return false
}

func rmfPathSegmentsMatch(pattern, actual []string) bool {
	for len(pattern) != 0 {
		if len(actual) == 0 {
			return false
		}
		if pattern[0] == actual[0] {
			pattern = pattern[1:]
			actual = actual[1:]
			continue
		}
		prefix := strings.TrimSuffix(pattern[0], "[*]")
		if prefix != pattern[0] && actual[0] == prefix && len(actual) >= 2 {
			pattern = pattern[1:]
			actual = actual[2:]
			continue
		}
		return false
	}
	return len(actual) == 0
}
