package main

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"reflect"
	"sort"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// appFixture is one hand-written expected result. The expected A and the golden
// text are independent literals: neither is produced by marshaling the probe's
// own structs, so a shared missing field can never make both sides agree.
type appFixture struct {
	name     string
	expected string
	text     string
	isError  bool
	verify   func(t *testing.T, app *appView)
}

func fixtureObject(t *testing.T, fixture appFixture) map[string]any {
	t.Helper()
	object, err := exactObject([]byte(fixture.expected))
	if err != nil {
		t.Fatalf("%s: expected JSON: %v", fixture.name, err)
	}
	return object
}

func renderFixtureResult(fixture appFixture, mode string, object map[string]any) *sdk.CallToolResult {
	result := &sdk.CallToolResult{IsError: fixture.isError}
	switch mode {
	case resultModeText:
		result.Content = []sdk.Content{&sdk.TextContent{Text: fixture.text}}
	case resultModeStructured:
		result.StructuredContent = object
		if fixture.isError {
			result.Content = []sdk.Content{&sdk.TextContent{Text: fakeDiagnosticText(object)}}
		} else {
			result.Content = []sdk.Content{}
		}
	case resultModeBoth:
		result.Content = []sdk.Content{&sdk.TextContent{Text: fixture.text}}
		result.StructuredContent = object
	}
	return result
}

// appMatrixFixtures covers one normal response per app kind. Each golden text
// uses the declared typed lines for every known app member.
func appMatrixFixtures() []appFixture {
	return []appFixture{
		{
			name: "find",
			expected: `{"operation_id":"Pfind00000000000000000001","app":{"kind":"find",` +
				`"items":[{"name":"示例应用","package":"com.example.probe"}],` +
				`"total":1,"truncated":false,"captured_at":"2026-11-12T03:00:00+08:00"}}`,
			text: "result v3 is_error=false\n" +
				"operation_id=Pfind00000000000000000001\n" +
				"app.kind=find\n" +
				"app.items=[{\"name\":\"示例应用\",\"package\":\"com.example.probe\"}]\n" +
				"app.total=1\n" +
				"app.truncated=false\n" +
				"app.captured_at=2026-11-12T03:00:00+08:00\n" +
				"end result\n",
			verify: func(t *testing.T, app *appView) {
				if app == nil || app.Kind != "find" || !app.HasItems || len(app.Items) != 1 ||
					app.Items[0].Package != "com.example.probe" || !app.HasTotal || app.Total != 1 ||
					app.Truncated == nil || *app.Truncated || app.CapturedAt == "" {
					t.Fatalf("find facts = %+v", app)
				}
			},
		},
		{
			name: "list",
			expected: `{"operation_id":"Plist00000000000000000002","app":{"kind":"list",` +
				`"display_id":12,` +
				`"items":[{"task_id":42,"package":"com.example.probe","ownership":"owned"},` +
				`{"task_id":43,"package":"com.example.other","ownership":"borrowed","origin_display":0},` +
				`{"task_id":44,"package":"com.example.third","ownership":"shared"}],` +
				`"total":3,"truncated":false,"captured_at":"2026-11-12T03:00:01+08:00",` +
				`"foreground":{"state":"unknown","reason":"foreground_unavailable"}}}`,
			text: "result v3 is_error=false\n" +
				"operation_id=Plist00000000000000000002\n" +
				"app.kind=list\n" +
				"app.display_id=12\n" +
				"app.items=[{\"task_id\":42,\"package\":\"com.example.probe\",\"ownership\":\"owned\"}," +
				"{\"task_id\":43,\"package\":\"com.example.other\",\"ownership\":\"borrowed\",\"origin_display\":0}," +
				"{\"task_id\":44,\"package\":\"com.example.third\",\"ownership\":\"shared\"}]\n" +
				"app.total=3\n" +
				"app.truncated=false\n" +
				"app.captured_at=2026-11-12T03:00:01+08:00\n" +
				"app.foreground={\"state\":\"unknown\",\"reason\":\"foreground_unavailable\"}\n" +
				"end result\n",
			verify: func(t *testing.T, app *appView) {
				if app == nil || app.Kind != "list" || app.DisplayID == nil || *app.DisplayID != 12 ||
					!app.HasTaskItems || len(app.TaskItems) != 3 || !app.HasTotal || app.Total != 3 {
					t.Fatalf("list facts = %+v", app)
				}
				ownership := []string{app.TaskItems[0].Ownership, app.TaskItems[1].Ownership, app.TaskItems[2].Ownership}
				if !reflect.DeepEqual(ownership, []string{"owned", "borrowed", "shared"}) {
					t.Fatalf("ownership = %v", ownership)
				}
				if !app.TaskItems[1].HasOriginDisplay || app.TaskItems[1].OriginDisplay == nil ||
					*app.TaskItems[1].OriginDisplay != 0 {
					t.Fatalf("origin_display = %+v", app.TaskItems[1])
				}
				if app.Foreground == nil || app.Foreground.State != "unknown" ||
					app.Foreground.Reason != "foreground_unavailable" {
					t.Fatalf("foreground = %+v", app.Foreground)
				}
			},
		},
		{
			name: "open",
			expected: `{"operation_id":"Popen00000000000000000003","outcome":"executed","app":{"kind":"open",` +
				`"package":"com.example.probe","task_id":42,"display_id":12,"ownership":"owned",` +
				`"reused":true,"state_preserved":false}}`,
			text: "result v3 is_error=false\n" +
				"operation_id=Popen00000000000000000003\n" +
				"outcome=executed\n" +
				"app.kind=open\n" +
				"app.package=\"com.example.probe\"\n" +
				"app.task_id=42\n" +
				"app.display_id=12\n" +
				"app.ownership=owned\n" +
				"app.reused=true\n" +
				"app.state_preserved=false\n" +
				"end result\n",
			verify: func(t *testing.T, app *appView) {
				if app == nil || app.Kind != "open" || app.Package != "com.example.probe" ||
					app.TaskID == nil || *app.TaskID != 42 || app.DisplayID == nil || *app.DisplayID != 12 ||
					app.Ownership != "owned" || app.Reused == nil || !*app.Reused ||
					!app.HasStatePreserved || app.StatePreserved == nil || *app.StatePreserved {
					t.Fatalf("open facts = %+v", app)
				}
			},
		},
		{
			name: "claim",
			expected: `{"operation_id":"Pclaim00000000000000000004","outcome":"executed","app":{"kind":"claim",` +
				`"package":"com.example.probe","task_id":43,"source_display_id":0,"display_id":12,` +
				`"ownership":"borrowed","state_preserved":true}}`,
			text: "result v3 is_error=false\n" +
				"operation_id=Pclaim00000000000000000004\n" +
				"outcome=executed\n" +
				"app.kind=claim\n" +
				"app.package=\"com.example.probe\"\n" +
				"app.task_id=43\n" +
				"app.source_display_id=0\n" +
				"app.display_id=12\n" +
				"app.ownership=borrowed\n" +
				"app.state_preserved=true\n" +
				"end result\n",
			verify: func(t *testing.T, app *appView) {
				if app == nil || app.Kind != "claim" || app.SourceDisplayID == nil ||
					*app.SourceDisplayID != 0 || app.Ownership != "borrowed" ||
					!app.HasStatePreserved || app.StatePreserved == nil || !*app.StatePreserved {
					t.Fatalf("claim facts = %+v", app)
				}
			},
		},
		{
			name: "handoff",
			expected: `{"operation_id":"Phandoff0000000000000000005","outcome":"executed","app":{"kind":"handoff",` +
				`"package":"com.example.probe","task_id":43,"source_display_id":12,"display_id":0,` +
				`"ownership":"shared","state_preserved":null,"foreground_restore":"not_requested"}}`,
			text: "result v3 is_error=false\n" +
				"operation_id=Phandoff0000000000000000005\n" +
				"outcome=executed\n" +
				"app.kind=handoff\n" +
				"app.package=\"com.example.probe\"\n" +
				"app.task_id=43\n" +
				"app.source_display_id=12\n" +
				"app.display_id=0\n" +
				"app.ownership=shared\n" +
				"app.state_preserved=null\n" +
				"app.foreground_restore=not_requested\n" +
				"end result\n",
			verify: func(t *testing.T, app *appView) {
				// Explicit null must survive as "present and unknown", never as
				// an omitted member.
				if app == nil || app.Kind != "handoff" || !app.HasStatePreserved ||
					app.StatePreserved != nil || app.ForegroundRestore != "not_requested" ||
					app.Ownership != "shared" || app.DisplayID == nil || *app.DisplayID != 0 {
					t.Fatalf("handoff facts = %+v", app)
				}
			},
		},
		{
			name: "close_partial",
			expected: `{"operation_id":"P9","outcome":"uncertain","app":{"kind":"close","display_id":12,` +
				`"tasks":[{"task_id":42,"outcome":"executed","recents_cleaned":true},` +
				`{"task_id":43,"outcome":"uncertain","recents_cleaned":null}],` +
				`"complete":false},` +
				`"error":{"code":"task_remove_unconfirmed","message":"部分任务的关闭结果未确认，请先核验现状"},` +
				`"recovery":{"entry":"observe","reference":"P","input_state":"retired"}}`,
			text: "result v3 is_error=true\n" +
				"operation_id=P9\n" +
				"outcome=uncertain\n" +
				"app.kind=close\n" +
				"app.display_id=12\n" +
				"app.tasks=[{\"task_id\":42,\"outcome\":\"executed\",\"recents_cleaned\":true}," +
				"{\"task_id\":43,\"outcome\":\"uncertain\",\"recents_cleaned\":null}]\n" +
				"app.complete=false\n" +
				"error.code=task_remove_unconfirmed\n" +
				"error.message=\"部分任务的关闭结果未确认，请先核验现状\"\n" +
				"recovery.entry=observe\n" +
				"recovery.reference=P\n" +
				"recovery.input_state=retired\n" +
				"end result\n",
			isError: true,
			verify: func(t *testing.T, app *appView) {
				if app == nil || app.Kind != "close" || !app.HasTasks || len(app.Tasks) != 2 ||
					app.Complete == nil || *app.Complete {
					t.Fatalf("close facts = %+v", app)
				}
				if !app.Tasks[0].HasRecentsCleaned || app.Tasks[0].RecentsCleaned == nil ||
					!*app.Tasks[0].RecentsCleaned {
					t.Fatalf("recents true lost: %+v", app.Tasks[0])
				}
				if !app.Tasks[1].HasRecentsCleaned || app.Tasks[1].RecentsCleaned != nil {
					t.Fatalf("recents null lost: %+v", app.Tasks[1])
				}
			},
		},
		{
			name: "kill",
			expected: `{"operation_id":"Pkill00000000000000000006","outcome":"executed","app":{"kind":"kill",` +
				`"package":"com.example.probe","source_task_id":42,"display_id":12,` +
				`"target_pids":[1234,1235],"killed_pids":[1234,1235],"remaining_pids":[],"restarted":null}}`,
			text: "result v3 is_error=false\n" +
				"operation_id=Pkill00000000000000000006\n" +
				"outcome=executed\n" +
				"app.kind=kill\n" +
				"app.package=\"com.example.probe\"\n" +
				"app.source_task_id=42\n" +
				"app.display_id=12\n" +
				"app.target_pids=[1234,1235]\n" +
				"app.killed_pids=[1234,1235]\n" +
				"app.remaining_pids=[]\n" +
				"app.restarted=null\n" +
				"end result\n",
			verify: func(t *testing.T, app *appView) {
				if app == nil || app.Kind != "kill" || !app.HasTargetPIDs ||
					!reflect.DeepEqual(app.TargetPIDs, []int{1234, 1235}) ||
					!app.HasKilledPIDs || !reflect.DeepEqual(app.KilledPIDs, []int{1234, 1235}) {
					t.Fatalf("kill pids = %+v", app)
				}
				// Empty array and explicit null are different facts.
				if !app.HasRemainingPIDs || app.RemainingPIDs == nil || len(app.RemainingPIDs) != 0 {
					t.Fatalf("remaining_pids = %+v", app.RemainingPIDs)
				}
				if !app.HasRestarted || app.Restarted != nil {
					t.Fatalf("restarted = %+v", app.Restarted)
				}
			},
		},
	}
}

func TestAppResultModesMatrix(t *testing.T) {
	for _, fixture := range appMatrixFixtures() {
		fixture := fixture
		object := fixtureObject(t, fixture)
		for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
			mode := mode
			t.Run(fixture.name+"/"+mode, func(t *testing.T) {
				projected, failure := project(renderFixtureResult(fixture, mode, object), mode)
				if failure != nil {
					t.Fatalf("project: %s %v", failure.reason, failure.fields)
				}
				if projected.result.IsError != fixture.isError {
					t.Fatalf("is_error = %v, want %v", projected.result.IsError, fixture.isError)
				}
				if !semanticEqual(projected.object, object) {
					t.Fatalf("decoded object differs from expected A")
				}
				if projected.view.App == nil {
					t.Fatal("app projection missing")
				}
				fixture.verify(t, projected.view.App)
				if mode != resultModeStructured {
					decoded, decodeFailure := decodeResultText(fixture.text)
					if decodeFailure != nil {
						t.Fatalf("decode golden text: %s", decodeFailure.reason)
					}
					if !semanticEqual(decoded.object, object) {
						t.Fatal("golden text does not restore expected A")
					}
					checkTypedAppGolden(t, fixture.text)
				}
			})
		}
	}
}

// checkTypedAppGolden is the known-field gate: an app member that the contract
// classifies must not arrive through the json v1 fallback. It also proves the
// typed line is actually present, so a pure round trip cannot hide a dropped
// classification.
func checkTypedAppGolden(t *testing.T, text string) {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, `json v1 path="/app/`) {
			for name := range appTextFields {
				if strings.HasPrefix(line, `json v1 path="/app/`+name) {
					t.Fatalf("known app field %q used json v1 fallback", name)
				}
			}
		}
		if strings.HasPrefix(line, "app.") {
			key, _, found := strings.Cut(strings.TrimPrefix(line, "app."), "=")
			if !found {
				t.Fatalf("app typed line has no value: %q", line)
			}
			if _, known := appTextFields[key]; !known {
				t.Fatalf("unknown app typed field %q", key)
			}
		}
	}
}

// TestAppErrorAndEdgeFixtures covers the failure, empty, null and ambiguous
// branches the matrix needs without inventing a response for an inapplicable
// kind.
func TestAppErrorAndEdgeFixtures(t *testing.T) {
	fixtures := []appFixture{
		{
			name: "find_empty",
			expected: `{"operation_id":"Pfindempty00000000000007","app":{"kind":"find","items":[],` +
				`"total":0,"truncated":false,"captured_at":"2026-11-12T03:00:02+08:00"}}`,
			text: "result v3 is_error=false\n" +
				"operation_id=Pfindempty00000000000007\n" +
				"app.kind=find\n" +
				"app.items=[]\n" +
				"app.total=0\n" +
				"app.truncated=false\n" +
				"app.captured_at=2026-11-12T03:00:02+08:00\n" +
				"end result\n",
			verify: func(t *testing.T, app *appView) {
				if app == nil || !app.HasItems || app.Items == nil || len(app.Items) != 0 {
					t.Fatalf("empty items = %+v", app)
				}
			},
		},
		{
			name: "find_failure",
			expected: `{"operation_id":"Pfindfail0000000000000008","app":{"kind":"find"},` +
				`"error":{"code":"device_unavailable","message":"应用清单读取失败"}}`,
			text: "result v3 is_error=true\n" +
				"operation_id=Pfindfail0000000000000008\n" +
				"app.kind=find\n" +
				"error.code=device_unavailable\n" +
				"error.message=\"应用清单读取失败\"\n" +
				"end result\n",
			isError: true,
			verify: func(t *testing.T, app *appView) {
				if app == nil || app.Kind != "find" || app.HasItems || app.HasTotal {
					t.Fatalf("failed find facts = %+v", app)
				}
			},
		},
		{
			name: "list_failure",
			expected: `{"operation_id":"Plistfail0000000000000009","app":{"kind":"list","display_id":12},` +
				`"error":{"code":"device_unavailable","message":"任务清单读取失败"}}`,
			text: "result v3 is_error=true\n" +
				"operation_id=Plistfail0000000000000009\n" +
				"app.kind=list\n" +
				"app.display_id=12\n" +
				"error.code=device_unavailable\n" +
				"error.message=\"任务清单读取失败\"\n" +
				"end result\n",
			isError: true,
			verify: func(t *testing.T, app *appView) {
				if app == nil || app.Kind != "list" || app.HasTaskItems || app.Foreground != nil {
					t.Fatalf("failed list facts = %+v", app)
				}
			},
		},
		{
			name: "open_state_preserved_null",
			expected: `{"operation_id":"Popen20000000000000000010","outcome":"executed","app":{"kind":"open",` +
				`"package":"com.example.probe","task_id":44,"display_id":12,"ownership":"owned",` +
				`"reused":false,"state_preserved":null}}`,
			text: "result v3 is_error=false\n" +
				"operation_id=Popen20000000000000000010\n" +
				"outcome=executed\n" +
				"app.kind=open\n" +
				"app.package=\"com.example.probe\"\n" +
				"app.task_id=44\n" +
				"app.display_id=12\n" +
				"app.ownership=owned\n" +
				"app.reused=false\n" +
				"app.state_preserved=null\n" +
				"end result\n",
			verify: func(t *testing.T, app *appView) {
				if app == nil || !app.HasStatePreserved || app.StatePreserved != nil ||
					app.Reused == nil || *app.Reused {
					t.Fatalf("open null facts = %+v", app)
				}
			},
		},
		{
			name: "claim_ambiguous",
			expected: `{"operation_id":"Pclaimamb0000000000000011","outcome":"not_executed",` +
				`"app":{"kind":"claim","package":"com.example.probe"},` +
				`"error":{"code":"task_target_ambiguous","message":"目标任务不唯一",` +
				`"details":{"task_ids":[42,43]}},` +
				`"recovery":{"entry":"retry","reference":"O","input_state":"effective"}}`,
			text: "result v3 is_error=true\n" +
				"operation_id=Pclaimamb0000000000000011\n" +
				"outcome=not_executed\n" +
				"app.kind=claim\n" +
				"app.package=\"com.example.probe\"\n" +
				"error.code=task_target_ambiguous\n" +
				"error.message=\"目标任务不唯一\"\n" +
				"error.details={\"task_ids\":[42,43]}\n" +
				"recovery.entry=retry\n" +
				"recovery.reference=O\n" +
				"recovery.input_state=effective\n" +
				"end result\n",
			isError: true,
			verify: func(t *testing.T, app *appView) {
				if app == nil || app.Kind != "claim" {
					t.Fatalf("claim app = %+v", app)
				}
			},
		},
		{
			name: "close_complete",
			expected: `{"operation_id":"Pclose2000000000000000012","outcome":"executed",` +
				`"app":{"kind":"close","display_id":12,` +
				`"tasks":[{"task_id":42,"outcome":"executed","recents_cleaned":true}],` +
				`"complete":true}}`,
			text: "result v3 is_error=false\n" +
				"operation_id=Pclose2000000000000000012\n" +
				"outcome=executed\n" +
				"app.kind=close\n" +
				"app.display_id=12\n" +
				"app.tasks=[{\"task_id\":42,\"outcome\":\"executed\",\"recents_cleaned\":true}]\n" +
				"app.complete=true\n" +
				"end result\n",
			verify: func(t *testing.T, app *appView) {
				if app == nil || app.Complete == nil || !*app.Complete || len(app.Tasks) != 1 {
					t.Fatalf("complete facts = %+v", app)
				}
			},
		},
		{
			name: "kill_unconfirmed",
			expected: `{"operation_id":"Pkill20000000000000000013","outcome":"uncertain",` +
				`"app":{"kind":"kill","package":"com.example.probe","source_task_id":42,` +
				`"display_id":12,"target_pids":[1234],"killed_pids":[],"restarted":null},` +
				`"error":{"code":"kill_unconfirmed","message":"进程终止未确认"},` +
				`"recovery":{"entry":"observe","reference":"P","input_state":"retired"}}`,
			text: "result v3 is_error=true\n" +
				"operation_id=Pkill20000000000000000013\n" +
				"outcome=uncertain\n" +
				"app.kind=kill\n" +
				"app.package=\"com.example.probe\"\n" +
				"app.source_task_id=42\n" +
				"app.display_id=12\n" +
				"app.target_pids=[1234]\n" +
				"app.killed_pids=[]\n" +
				"app.restarted=null\n" +
				"error.code=kill_unconfirmed\n" +
				"error.message=\"进程终止未确认\"\n" +
				"recovery.entry=observe\n" +
				"recovery.reference=P\n" +
				"recovery.input_state=retired\n" +
				"end result\n",
			isError: true,
			verify: func(t *testing.T, app *appView) {
				if app == nil || app.HasRemainingPIDs || !app.HasRestarted || app.Restarted != nil ||
					!app.HasKilledPIDs || app.KilledPIDs == nil || len(app.KilledPIDs) != 0 {
					t.Fatalf("unconfirmed kill facts = %+v", app)
				}
			},
		},
		{
			name:     "admission_rejection",
			expected: `{"error":{"code":"invalid_request","message":"不支持该成员"}}`,
			text: "result v3 is_error=true\n" +
				"error.code=invalid_request\n" +
				"error.message=\"不支持该成员\"\n" +
				"end result\n",
			isError: true,
			verify: func(_ *testing.T, app *appView) {
				if app != nil {
					t.Fatalf("admission rejection must not publish app facts")
				}
			},
		},
		{
			name: "query_cancelled",
			expected: `{"operation_id":"Pcancel000000000000000014","operation_state":"finished",` +
				`"phase":"query_cancelled","app":{"kind":"list"},` +
				`"error":{"code":"call_cancelled","message":"查询已取消",` +
				`"details":{"mcp.cancelled":true,"mcp.cancellation":"查询已取消；未发起 end"}}}`,
			text: "result v3 is_error=true\n" +
				"operation_id=Pcancel000000000000000014\n" +
				"operation_state=finished\n" +
				"phase=query_cancelled\n" +
				"app.kind=list\n" +
				"error.code=call_cancelled\n" +
				"error.message=\"查询已取消\"\n" +
				"error.details={\"mcp.cancelled\":true,\"mcp.cancellation\":\"查询已取消；未发起 end\"}\n" +
				"end result\n",
			isError: true,
			verify: func(t *testing.T, app *appView) {
				if app == nil || app.Kind != "list" || app.HasTaskItems || app.HasTotal {
					t.Fatalf("cancelled query facts = %+v", app)
				}
			},
		},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		object := fixtureObject(t, fixture)
		for _, mode := range []string{resultModeBoth, resultModeText, resultModeStructured} {
			mode := mode
			t.Run(fixture.name+"/"+mode, func(t *testing.T) {
				projected, failure := project(renderFixtureResult(fixture, mode, object), mode)
				if failure != nil {
					t.Fatalf("project: %s %v", failure.reason, failure.fields)
				}
				if projected.result.IsError != fixture.isError {
					t.Fatalf("is_error = %v, want %v", projected.result.IsError, fixture.isError)
				}
				if !semanticEqual(projected.object, object) {
					t.Fatal("decoded object differs from expected A")
				}
				fixture.verify(t, projected.view.App)
			})
		}
	}
}

// TestAppTypedFieldGates holds the reverse gates from the proposal: known app
// fields may not use the fallback, unknown fields may not use typed lines, and a
// dropped nested close fact must break an equality assertion.
func TestAppTypedFieldGates(t *testing.T) {
	t.Run("known field via fallback", func(t *testing.T) {
		text := "result v3 is_error=true\n" +
			"app.kind=close\n" +
			"json v1 path=\"/app/tasks\" value=[]\n" +
			"end result\n"
		if _, failure := decodeResultText(text); failure == nil ||
			failure.reason != "text_result_app_field_typed_required" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("unknown field via typed line", func(t *testing.T) {
		text := "result v3 is_error=false\n" +
			"app.kind=find\n" +
			"app.future=1\n" +
			"end result\n"
		if _, failure := decodeResultText(text); failure == nil ||
			failure.reason != "text_result_app_field_unknown" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("unknown field via fallback preserved", func(t *testing.T) {
		text := "result v3 is_error=false\n" +
			"app.kind=find\n" +
			"json v1 path=\"/app/future\" value={\"value\":1}\n" +
			"end result\n"
		decoded, failure := decodeResultText(text)
		if failure != nil {
			t.Fatalf("decode: %s", failure.reason)
		}
		value, ok := lookupPath(decoded.object, []string{"app", "future", "value"})
		number, integer := exactInteger(value)
		if !ok || !integer || number != 1 {
			t.Fatalf("unknown field lost: %#v", value)
		}
	})
	t.Run("missing restarted leaves no fact", func(t *testing.T) {
		text := "result v3 is_error=false\n" +
			"operation_id=P1\n" +
			"outcome=executed\n" +
			"app.kind=kill\n" +
			"app.package=\"com.example.probe\"\n" +
			"app.source_task_id=42\n" +
			"app.display_id=12\n" +
			"app.target_pids=[1234]\n" +
			"app.killed_pids=[1234]\n" +
			"app.remaining_pids=[]\n" +
			"end result\n"
		decoded, failure := decodeResultText(text)
		if failure != nil {
			t.Fatalf("decode: %s", failure.reason)
		}
		if decoded.view.App == nil || decoded.view.App.HasRestarted {
			t.Fatal("omitted restarted must not become a fact")
		}
	})
	t.Run("dropped nested recents breaks equality", func(t *testing.T) {
		decoded, failure := decodeResultText(appMatrixFixtures()[5].text)
		if failure != nil {
			t.Fatalf("decode: %s", failure.reason)
		}
		expected := fixtureObject(t, appMatrixFixtures()[5])
		tasks, _ := lookupPath(expected, []string{"app", "tasks", "1"})
		taskObject, _ := tasks.(map[string]any)
		delete(taskObject, "recents_cleaned")
		if semanticEqual(decoded.object, expected) {
			t.Fatal("dropping nested recents_cleaned was not detected")
		}
	})
}

// TestAppGoldenCoversEveryTypedField asserts every declared app typed field
// appears at least once in the golden fixtures.
func TestAppGoldenCoversEveryTypedField(t *testing.T) {
	all := ""
	for _, fixture := range appMatrixFixtures() {
		all += fixture.text
	}
	for name := range appTextFields {
		if !strings.Contains(all, "app."+name+"=") {
			t.Fatalf("golden fixtures do not cover app.%s", name)
		}
	}
}

// TestClaimAmbiguityFacts asserts the structured recovery facts of the
// ambiguous-target branch independently of the generic round trip.
func TestClaimAmbiguityFacts(t *testing.T) {
	// The ambiguity fixture is the hand-written golden text for this branch.
	text := "result v3 is_error=true\n" +
		"operation_id=Pclaimamb0000000000000011\n" +
		"outcome=not_executed\n" +
		"app.kind=claim\n" +
		"app.package=\"com.example.probe\"\n" +
		"error.code=task_target_ambiguous\n" +
		"error.message=\"目标任务不唯一\"\n" +
		"error.details={\"task_ids\":[42,43]}\n" +
		"recovery.entry=retry\n" +
		"recovery.reference=O\n" +
		"recovery.input_state=effective\n" +
		"end result\n"
	decoded, failure := decodeResultText(text)
	if failure != nil {
		t.Fatalf("decode: %s", failure.reason)
	}
	view := decoded.view
	if !decoded.isError || view.Error == nil || view.Error.Code != "task_target_ambiguous" {
		t.Fatalf("error facts = %+v", view.Error)
	}
	taskIDs, failure := detailInts(view.Error.Details, "task_ids")
	if failure != nil {
		t.Fatalf("task_ids: %s", failure.reason)
	}
	if !sort.IntsAreSorted(taskIDs) || !reflect.DeepEqual(taskIDs, []int{42, 43}) {
		t.Fatalf("task_ids = %v, want sorted [42 43]", taskIDs)
	}
	if view.Recovery == nil || view.Recovery.Entry != "retry" ||
		view.Recovery.Reference != "O" || view.Recovery.InputState != "effective" {
		t.Fatalf("recovery = %+v", view.Recovery)
	}
	if view.Outcome != "not_executed" || view.App == nil || view.App.Kind != "claim" {
		t.Fatalf("claim facts = %+v", view)
	}
}

func detailInts(details map[string]jsontext.Value, name string) ([]int, *checkError) {
	raw, found := details[name]
	if !found {
		return nil, failf("detail_missing", field("name", name))
	}
	var values []int
	if err := jsonv2.Unmarshal(raw, &values); err != nil {
		return nil, failf("detail_invalid", field("name", name))
	}
	return values, nil
}

// TestAppStructuredAndTextAreFieldEqual compares the text side with the
// structured side independently of project's both-mode gate.
func TestAppStructuredAndTextAreFieldEqual(t *testing.T) {
	for _, fixture := range appMatrixFixtures() {
		decoded, failure := decodeResultText(fixture.text)
		if failure != nil {
			t.Fatalf("%s: %s", fixture.name, failure.reason)
		}
		structured, err := exactObject([]byte(fixture.expected))
		if err != nil {
			t.Fatalf("%s: %v", fixture.name, err)
		}
		if !semanticEqual(decoded.object, structured) {
			t.Fatalf("%s: text and structured facts differ", fixture.name)
		}
	}
}
