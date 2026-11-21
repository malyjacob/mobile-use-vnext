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

func fixtureView(t *testing.T, fixture appFixture) resultView {
	t.Helper()
	var view resultView
	if err := jsonv2.Unmarshal([]byte(fixture.expected), &view); err != nil {
		t.Fatalf("%s: expected view: %v", fixture.name, err)
	}
	return view
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
// uses the result v4 single-line app head plus the formal collection blocks.
func appMatrixFixtures() []appFixture {
	return []appFixture{
		{
			name: "find",
			expected: `{"operation_id":"Pfind00000000000000000001","app":{"kind":"find",` +
				`"items":[{"name":"示例应用","package":"com.example.probe"}],` +
				`"total":1,"truncated":false,"captured_at":"2026-11-12T03:00:00Z"}}`,
			text: "result v4 ok op=Pfind00000000000000000001\n" +
				"app find total=1 truncated=false at=03:00:00\n" +
				"\n" +
				"app_items v1 kind=find count=1\n" +
				"com.example.probe \"示例应用\"\n" +
				"end app_items\n" +
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
				`"total":3,"truncated":false,"captured_at":"2026-11-12T03:00:01Z",` +
				`"foreground":{"state":"unknown","reason":"foreground_unavailable"}}}`,
			text: "result v4 ok op=Plist00000000000000000002\n" +
				"app list display=12 total=3 truncated=false fg=unknown(foreground_unavailable) at=03:00:01\n" +
				"\n" +
				"app_items v1 kind=list count=3\n" +
				"#42 com.example.probe owned\n" +
				"#43 com.example.other borrowed origin_display=0\n" +
				"#44 com.example.third shared\n" +
				"end app_items\n" +
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
			text: "result v4 ok op=Popen00000000000000000003\n" +
				"app open pkg=com.example.probe task=42 display=12 ownership=owned " +
				"reused=true state_preserved=false\n" +
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
			text: "result v4 ok op=Pclaim00000000000000000004\n" +
				"app claim pkg=com.example.probe task=43 src_display=0 display=12 " +
				"ownership=borrowed state_preserved=true\n" +
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
			text: "result v4 ok op=Phandoff0000000000000000005\n" +
				"app handoff pkg=com.example.probe task=43 src_display=12 display=0 " +
				"ownership=shared state_preserved=null fg_restore=not_requested\n" +
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
			text: "result v4 err op=P9 outcome=uncertain\n" +
				"app close display=12 complete=false\n" +
				"\n" +
				"app_tasks v1 count=2\n" +
				"#42 executed recents_cleaned=true\n" +
				"#43 uncertain recents_cleaned=null\n" +
				"end app_tasks\n" +
				"error task_remove_unconfirmed \"部分任务的关闭结果未确认，请先核验现状\"\n" +
				"recovery entry=observe ref=P input=retired\n" +
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
			text: "result v4 ok op=Pkill00000000000000000006\n" +
				"app kill pkg=com.example.probe src_task=42 display=12 " +
				"target_pids=[1234,1235] killed_pids=[1234,1235] remaining_pids=[] restarted=null\n" +
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
		view := fixtureView(t, fixture)
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
				if mode == resultModeStructured {
					if !semanticEqual(projected.object, object) {
						t.Fatalf("structured object differs from expected A")
					}
				}
				if projected.view.App == nil {
					t.Fatal("app projection missing")
				}
				fixture.verify(t, projected.view.App)
				if mode != resultModeStructured {
					decoded, decodeFailure := decodeResultText(fixture.text)
					if decodeFailure != nil {
						t.Fatalf("decode golden text: %s %v", decodeFailure.reason, decodeFailure.fields)
					}
					if failure := compareResultTextToStructured(decoded, object, view, ""); failure != nil {
						t.Fatalf("golden text does not restore expected A: %s %v",
							failure.reason, failure.fields)
					}
					checkTypedAppGolden(t, fixture.text)
				}
			})
		}
	}
}

// checkTypedAppGolden is the known-field gate: a known app member must not use
// the json v1 fallback, and every key on the app head must be a declared v4
// alias.
func checkTypedAppGolden(t *testing.T, text string) {
	t.Helper()
	aliases := map[string]bool{
		"pkg": true, "task": true, "src_task": true, "src_display": true,
		"display": true, "ownership": true, "reused": true, "state_preserved": true,
		"fg_restore": true, "complete": true, "target_pids": true, "killed_pids": true,
		"remaining_pids": true, "restarted": true, "total": true, "truncated": true,
		"fg": true, "at": true,
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, `json v1 path="/app/`) {
			for name := range appTextFields {
				if strings.HasPrefix(line, `json v1 path="/app/`+name) {
					t.Fatalf("known app field %q used json v1 fallback", name)
				}
			}
		}
		if !strings.HasPrefix(line, "app ") {
			continue
		}
		for index, token := range strings.Fields(line) {
			if index < 2 {
				continue
			}
			key, _, found := strings.Cut(token, "=")
			if !found {
				t.Fatalf("app head token has no value: %q", token)
			}
			if !aliases[key] {
				t.Fatalf("unknown app head member %q", key)
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
				`"total":0,"truncated":false,"captured_at":"2026-11-12T03:00:02Z"}}`,
			text: "result v4 ok op=Pfindempty00000000000007\n" +
				"app find total=0 truncated=false at=03:00:02\n" +
				"\n" +
				"app_items v1 kind=find count=0\n" +
				"end app_items\n" +
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
			text: "result v4 err op=Pfindfail0000000000000008\n" +
				"app find\n" +
				"error device_unavailable \"应用清单读取失败\"\n" +
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
			text: "result v4 err op=Plistfail0000000000000009\n" +
				"app list display=12\n" +
				"error device_unavailable \"任务清单读取失败\"\n" +
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
			text: "result v4 ok op=Popen20000000000000000010\n" +
				"app open pkg=com.example.probe task=44 display=12 ownership=owned " +
				"reused=false state_preserved=null\n" +
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
			text: "result v4 err op=Pclaimamb0000000000000011 outcome=not_executed\n" +
				"app claim pkg=com.example.probe\n" +
				"error task_target_ambiguous \"目标任务不唯一\"\n" +
				"error.details={\"task_ids\":[42,43]}\n" +
				"recovery entry=retry ref=O input=effective\n" +
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
			text: "result v4 ok op=Pclose2000000000000000012\n" +
				"app close display=12 complete=true\n" +
				"\n" +
				"app_tasks v1 count=1\n" +
				"#42 executed recents_cleaned=true\n" +
				"end app_tasks\n" +
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
			text: "result v4 err op=Pkill20000000000000000013 outcome=uncertain\n" +
				"app kill pkg=com.example.probe src_task=42 display=12 " +
				"target_pids=[1234] killed_pids=[] restarted=null\n" +
				"error kill_unconfirmed \"进程终止未确认\"\n" +
				"recovery entry=observe ref=P input=retired\n" +
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
			text: "result v4 err\n" +
				"error invalid_request \"不支持该成员\"\n" +
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
			text: "result v4 err op=Pcancel000000000000000014 state=finished phase=query_cancelled\n" +
				"app list\n" +
				"error call_cancelled \"查询已取消\"\n" +
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
		view := fixtureView(t, fixture)
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
				if mode == resultModeStructured && !semanticEqual(projected.object, object) {
					t.Fatal("structured object differs from expected A")
				}
				fixture.verify(t, projected.view.App)
				if mode == resultModeText {
					decoded, decodeFailure := decodeResultText(fixture.text)
					if decodeFailure != nil {
						t.Fatalf("decode golden: %s %v", decodeFailure.reason, decodeFailure.fields)
					}
					if failure := compareResultTextToStructured(decoded, object, view, ""); failure != nil {
						t.Fatalf("golden mismatch: %s %v", failure.reason, failure.fields)
					}
				}
			})
		}
	}
}

// TestAppTypedFieldGates holds the reverse gates: known app fields may not use
// the fallback, unknown fields may not use the app head, and a dropped nested
// close fact must break an equality assertion.
func TestAppTypedFieldGates(t *testing.T) {
	t.Run("known field via fallback", func(t *testing.T) {
		text := "result v4 err\n" +
			"app close\n" +
			"json v1 path=\"/app/tasks\" value=[]\n" +
			"end result\n"
		if _, failure := decodeResultText(text); failure == nil ||
			failure.reason != "text_result_app_field_typed_required" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("unknown field via app head", func(t *testing.T) {
		text := "result v4 ok\n" +
			"app find future=1\n" +
			"end result\n"
		if _, failure := decodeResultText(text); failure == nil ||
			failure.reason != "text_result_app_invalid" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("unknown field via fallback preserved", func(t *testing.T) {
		text := "result v4 ok\n" +
			"app find\n" +
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
	t.Run("nested item fallback inside block preserved", func(t *testing.T) {
		text := "result v4 ok\n" +
			"app find total=1 truncated=false at=03:00:00\n" +
			"\n" +
			"app_items v1 kind=find count=1\n" +
			"json v1 path=\"/app/items/0/future\" value={\"value\":1}\n" +
			"com.example.probe \"示例应用\"\n" +
			"end app_items\n" +
			"end result\n"
		decoded, failure := decodeResultText(text)
		if failure != nil {
			t.Fatalf("decode: %s %v", failure.reason, failure.fields)
		}
		value, ok := lookupPath(decoded.object, []string{"app", "items", "0", "future", "value"})
		number, integer := exactInteger(value)
		if !ok || !integer || number != 1 {
			t.Fatalf("nested fallback lost: %#v, %v", value, ok)
		}
		if decoded.view.App == nil || len(decoded.view.App.Items) != 1 {
			t.Fatalf("items = %+v", decoded.view.App)
		}
	})
	t.Run("missing restarted leaves no fact", func(t *testing.T) {
		text := "result v4 ok\n" +
			"app kill pkg=com.example.probe src_task=42 display=12 " +
			"target_pids=[1234] killed_pids=[1234] remaining_pids=[]\n" +
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
		fixture := appMatrixFixtures()[5]
		decoded, failure := decodeResultText(fixture.text)
		if failure != nil {
			t.Fatalf("decode: %s", failure.reason)
		}
		expected := fixtureObject(t, fixture)
		tasks, _ := lookupPath(expected, []string{"app", "tasks", "1"})
		taskObject, _ := tasks.(map[string]any)
		delete(taskObject, "recents_cleaned")
		view := fixtureView(t, fixture)
		if failure := compareResultTextToStructured(decoded, expected, view, ""); failure == nil {
			t.Fatal("dropping nested recents_cleaned was not detected")
		}
	})
}

// TestAppGoldenCoversEveryTypedField asserts every declared app member appears
// at least once in the golden fixtures' expected objects.
func TestAppGoldenCoversEveryTypedField(t *testing.T) {
	covered := make(map[string]bool)
	for _, fixture := range appMatrixFixtures() {
		object := fixtureObject(t, fixture)
		app, _ := object["app"].(map[string]any)
		for key := range app {
			covered[key] = true
		}
	}
	for name := range appTextFields {
		if !covered[name] {
			t.Fatalf("golden fixtures do not cover app.%s", name)
		}
	}
}

// TestClaimAmbiguityFacts asserts the structured recovery facts of the
// ambiguous-target branch independently of the generic round trip.
func TestClaimAmbiguityFacts(t *testing.T) {
	text := "result v4 err op=Pclaimamb0000000000000011 outcome=not_executed\n" +
		"app claim pkg=com.example.probe\n" +
		"error task_target_ambiguous \"目标任务不唯一\"\n" +
		"error.details={\"task_ids\":[42,43]}\n" +
		"recovery entry=retry ref=O input=effective\n" +
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
		structured := fixtureObject(t, fixture)
		if failure := compareResultTextToStructured(decoded, structured, fixtureView(t, fixture), ""); failure != nil {
			t.Fatalf("%s: text and structured facts differ: %s %v",
				fixture.name, failure.reason, failure.fields)
		}
	}
}
