package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/client"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ---------------------------------------------------------------- schema

// testAppSchemaShape pins the published app schema to the flat contract: one
// root object, exactly the nine catalogued members, kind required, and no
// conditional applicators at any depth. Branch admission is the strict
// server-side DecodeApp matrix, not a duplicated JSON-Schema matrix.
func testAppSchemaShape(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var schema struct {
		Type                 string                     `json:"type"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode app schema: %v", err)
	}
	if schema.Type != "object" {
		t.Fatalf("app schema type = %q, want object", schema.Type)
	}
	wantProperties := []string{
		"kind", "query", "reference", "scope", "app",
		"target", "restore_foreground", "allow_borrowed", "allow_stop_playback",
	}
	if len(schema.Properties) != len(wantProperties) {
		t.Fatalf("app properties = %d, want %d", len(schema.Properties), len(wantProperties))
	}
	for _, name := range wantProperties {
		if _, ok := schema.Properties[name]; !ok {
			t.Fatalf("app schema lacks property %q", name)
		}
	}
	if _, ok := schema.Properties["observation_id"]; ok {
		t.Fatal("app schema still publishes the retired observation_id member")
	}
	if !reflect.DeepEqual(schema.Required, []string{"kind"}) {
		t.Fatalf("app required = %v, want [kind]", schema.Required)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatalf("app additionalProperties = %v, want false", schema.AdditionalProperties)
	}
	requireNoConditionalApplicators(t, raw)
	var kind struct {
		Enum []string `json:"enum"`
	}
	if err := json.Unmarshal(schema.Properties["kind"], &kind); err != nil {
		t.Fatalf("kind schema: %v", err)
	}
	if !reflect.DeepEqual(kind.Enum, protocol.AppKinds) {
		t.Fatalf("kind enum = %v, want %v", kind.Enum, protocol.AppKinds)
	}
	var target struct {
		Type                 string                     `json:"type"`
		Properties           map[string]json.RawMessage `json:"properties"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	if err := json.Unmarshal(schema.Properties["target"], &target); err != nil {
		t.Fatalf("target schema: %v", err)
	}
	if target.Type != "object" || len(target.Properties) != 2 {
		t.Fatalf("target schema = %+v, want a flat two-member object", target)
	}
	if target.AdditionalProperties == nil || *target.AdditionalProperties {
		t.Fatalf("target additionalProperties = %v, want false", target.AdditionalProperties)
	}
	var taskID struct {
		Type    string `json:"type"`
		Minimum int    `json:"minimum"`
	}
	if err := json.Unmarshal(target.Properties["task_id"], &taskID); err != nil ||
		taskID.Type != "integer" || taskID.Minimum != 1 {
		t.Fatalf("target.task_id schema = %+v %v", taskID, err)
	}
	var app struct {
		Type    string `json:"type"`
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(target.Properties["app"], &app); err != nil ||
		app.Type != "string" || app.Pattern == "" {
		t.Fatalf("target.app schema = %+v %v", app, err)
	}
	for _, property := range []string{"restore_foreground", "allow_borrowed", "allow_stop_playback"} {
		var option struct {
			Type    string `json:"type"`
			Default *bool  `json:"default"`
		}
		if err := json.Unmarshal(schema.Properties[property], &option); err != nil {
			t.Fatalf("%s schema: %v", property, err)
		}
		if option.Type != "boolean" || option.Default == nil || *option.Default {
			t.Fatalf("%s schema = %+v, want boolean default false", property, option)
		}
	}
	var scope struct {
		Enum    []string `json:"enum"`
		Default string   `json:"default"`
	}
	if err := json.Unmarshal(schema.Properties["scope"], &scope); err != nil ||
		scope.Default != protocol.AppScopeSession || !reflect.DeepEqual(scope.Enum, []string{protocol.AppScopeSession, protocol.AppScopePrimary}) {
		t.Fatalf("scope schema = %+v %v", scope, err)
	}
}

// requireNoConditionalApplicators walks the whole schema and rejects every
// applicator that would encode branch admission as JSON Schema. The server-side
// strict decoder stays the only kind-matrix authority.
func requireNoConditionalApplicators(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode app schema tree: %v", err)
	}
	var walk func(node any, path string)
	walk = func(node any, path string) {
		switch typed := node.(type) {
		case map[string]any:
			for key, child := range typed {
				switch key {
				case "allOf", "if", "then", "else", "not", "oneOf":
					t.Fatalf("app schema publishes %q at %s", key, path)
				}
				walk(child, path+"."+key)
			}
		case []any:
			for _, child := range typed {
				walk(child, path+"[]")
			}
		}
	}
	walk(value, "$")
}

// ---------------------------------------------------------------- admission

func TestAppStrictAdmission(t *testing.T) {
	cases := []struct{ name, body string }{
		{"missing kind", `{}`},
		{"null kind", `{"kind":null}`},
		{"unknown kind", `{"kind":"launch"}`},
		{"kind case variant", `{"kind":"Find"}`},
		{"duplicate kind", `{"kind":"find","kind":"find"}`},
		{"trailing value", `{"kind":"find"} {"kind":"find"}`},
		{"find cross-branch reference", `{"kind":"find","reference":"O1"}`},
		{"find cross-branch target", `{"kind":"find","target":{"task_id":1}}`},
		{"find null query", `{"kind":"find","query":null}`},
		{"find wrong query type", `{"kind":"find","query":1}`},
		{"list missing reference", `{"kind":"list"}`},
		{"list cross-branch target", `{"kind":"list","reference":"O1","target":{"task_id":1}}`},
		{"list cross-branch query", `{"kind":"list","reference":"O1","query":"x"}`},
		{"list bad scope", `{"kind":"list","reference":"O1","scope":"virtual"}`},
		{"list null scope", `{"kind":"list","reference":"O1","scope":null}`},
		{"retired observation_id", `{"kind":"open","observation_id":"O1","target":{"app":"com.example"}}`},
		{"open missing target", `{"kind":"open","reference":"O1"}`},
		{"open top-level app", `{"kind":"open","reference":"O1","app":"com.example"}`},
		{"open cross-branch query", `{"kind":"open","reference":"O1","target":{"app":"com.example"},"query":"x"}`},
		{"open bad package", `{"kind":"open","reference":"O1","target":{"app":"not a package"}}`},
		{"claim missing target", `{"kind":"claim","reference":"O1"}`},
		{"claim null target", `{"kind":"claim","reference":"O1","target":null}`},
		{"claim empty target", `{"kind":"claim","reference":"O1","target":{}}`},
		{"claim target both", `{"kind":"claim","reference":"O1","target":{"task_id":1,"app":"com.example"}}`},
		{"claim target zero", `{"kind":"claim","reference":"O1","target":{"task_id":0}}`},
		{"claim target negative", `{"kind":"claim","reference":"O1","target":{"task_id":-1}}`},
		{"claim target string", `{"kind":"claim","reference":"O1","target":{"task_id":"1"}}`},
		{"claim target unknown member", `{"kind":"claim","reference":"O1","target":{"task_id":1,"pid":7}}`},
		{"claim cross-branch app", `{"kind":"claim","reference":"O1","target":{"task_id":1},"app":"com.example"}`},
		{"claim cross-branch restore", `{"kind":"claim","reference":"O1","target":{"task_id":1},"restore_foreground":true}`},
		{"handoff cross-branch allow", `{"kind":"handoff","reference":"O1","target":{"task_id":1},"allow_borrowed":true}`},
		{"close cross-branch restore", `{"kind":"close","reference":"O1","target":{"task_id":1},"restore_foreground":true}`},
		{"kill package target", `{"kind":"kill","reference":"O1","target":{"app":"com.example"}}`},
		{"kill cross-branch allow_borrowed", `{"kind":"kill","reference":"O1","target":{"task_id":1},"allow_borrowed":true}`},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			fake := newTestDeviceService(t)
			adapter := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.DiscardHandler)}
			execution := adapter.run(context.Background(), testDefinitionByName(t, "app"), []byte(testCase.body))
			if !execution.admission || execution.result == nil || execution.result.Error == nil ||
				execution.result.Error.Code != protocol.CodeInvalidRequest {
				t.Fatalf("%s was not rejected as invalid_request: %+v", testCase.name, execution.result)
			}
			if execution.result.OperationID != "" {
				t.Fatalf("%s allocated an operation ID", testCase.name)
			}
			if execution.result.App != nil {
				t.Fatalf("%s synthesized app metadata for an adapter rejection", testCase.name)
			}
			if fake.totalRequests() != 0 {
				t.Fatalf("%s called the service", testCase.name)
			}
			if !execution.isError("app") {
				t.Fatalf("%s reported success", testCase.name)
			}
		})
	}
}

func TestAppEveryKindIsAdmitted(t *testing.T) {
	cases := []struct {
		name string
		body string
		kind string
	}{
		{"find", `{"kind":"find"}`, protocol.AppKindFind},
		{"find query", `{"kind":"find","query":"设置"}`, protocol.AppKindFind},
		{"list", `{"kind":"list","reference":"O1"}`, protocol.AppKindList},
		{"list primary filter", `{"kind":"list","reference":"O1","scope":"primary","app":"com.example"}`, protocol.AppKindList},
		{"open", `{"kind":"open","reference":"O1","target":{"app":"com.example"}}`, protocol.AppKindOpen},
		{"claim task", `{"kind":"claim","reference":"O1","target":{"task_id":43}}`, protocol.AppKindClaim},
		{"claim app", `{"kind":"claim","reference":"O1","target":{"app":"com.example"}}`, protocol.AppKindClaim},
		{"handoff", `{"kind":"handoff","reference":"O1","target":{"task_id":43},"restore_foreground":true}`, protocol.AppKindHandoff},
		{"close", `{"kind":"close","reference":"O1","target":{"task_id":43},"allow_borrowed":true}`, protocol.AppKindClose},
		{"kill", `{"kind":"kill","reference":"O1","target":{"task_id":43},"allow_stop_playback":true}`, protocol.AppKindKill},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			prepared, apiErr := prepareCall(nil, testDefinitionByName(t, "app"), []byte(testCase.body))
			if apiErr != nil {
				t.Fatalf("admission rejected %s: %s", testCase.body, apiErr.Code)
			}
			if prepared.readOnly {
				t.Fatal("app was admitted as read-only")
			}
			if prepared.kind != testCase.kind {
				t.Fatalf("prepared kind = %q, want %q", prepared.kind, testCase.kind)
			}
			expectedQuery := testCase.kind == protocol.AppKindFind || testCase.kind == protocol.AppKindList
			if prepared.appQuery() != expectedQuery {
				t.Fatalf("query classification mismatch for %q", testCase.kind)
			}
			if prepared.appMutation() == expectedQuery {
				t.Fatalf("mutation classification mismatch for %q", testCase.kind)
			}
			if prepared.endsOnCancel() == expectedQuery {
				t.Fatalf("cancel policy mismatch for %q", testCase.kind)
			}
		})
	}
}

// ---------------------------------------------------------------- 7 kinds x 3 modes

type appModeFixture struct {
	name      string
	arguments string
	service   string // one %q for the adapter-allocated operation ID
	isError   bool
	query     bool
	typed     []string
}

func appModeFixtures() []appModeFixture {
	return []appModeFixture{
		{
			name:      "find",
			arguments: `{"kind":"find"}`,
			query:     true,
			service: `{"operation_id":%q,"app":{"kind":"find",` +
				`"items":[{"name":"设置","package":"com.android.settings"}],` +
				`"total":1,"truncated":false,"captured_at":"2026-11-12T03:00:00+08:00"}}`,
			typed: []string{
				"app.kind=find",
				`app.items=[{"name":"设置","package":"com.android.settings"}]`,
				"app.total=1",
				"app.truncated=false",
				"app.captured_at=2026-11-12T03:00:00+08:00",
			},
		},
		{
			name:      "list",
			arguments: `{"kind":"list","reference":"O1"}`,
			query:     true,
			service: `{"operation_id":%q,"app":{"kind":"list","display_id":12,` +
				`"items":[{"task_id":42,"package":"com.example","ownership":"borrowed","origin_display":0}],` +
				`"total":1,"truncated":false,"captured_at":"2026-11-12T03:00:01+08:00",` +
				`"foreground":{"state":"present","package":"com.example","task_id":42}}}`,
			typed: []string{
				"app.kind=list",
				"app.display_id=12",
				`app.items=[{"task_id":42,"package":"com.example","ownership":"borrowed","origin_display":0}]`,
				`app.foreground={"state":"present","package":"com.example","task_id":42}`,
			},
		},
		{
			name:      "open",
			arguments: `{"kind":"open","reference":"O1","target":{"app":"com.example"}}`,
			service: `{"operation_id":%q,"outcome":"executed","app":{"kind":"open",` +
				`"package":"com.example","task_id":42,"display_id":12,"ownership":"owned",` +
				`"reused":false,"state_preserved":false}}`,
			typed: []string{
				"app.kind=open", `app.package="com.example"`, "app.task_id=42",
				"app.display_id=12", "app.ownership=owned", "app.reused=false",
				"app.state_preserved=false",
			},
		},
		{
			name:      "claim",
			arguments: `{"kind":"claim","reference":"O1","target":{"task_id":43}}`,
			service: `{"operation_id":%q,"outcome":"executed","app":{"kind":"claim",` +
				`"package":"com.example","task_id":43,"source_display_id":0,"display_id":12,` +
				`"ownership":"borrowed","state_preserved":true}}`,
			typed: []string{
				"app.kind=claim", "app.source_display_id=0",
				"app.display_id=12", "app.ownership=borrowed", "app.state_preserved=true",
			},
		},
		{
			name:      "handoff",
			arguments: `{"kind":"handoff","reference":"O1","target":{"task_id":43}}`,
			service: `{"operation_id":%q,"outcome":"executed","app":{"kind":"handoff",` +
				`"package":"com.example","task_id":43,"source_display_id":12,"display_id":0,` +
				`"ownership":"shared","state_preserved":null,"foreground_restore":"not_requested"}}`,
			typed: []string{
				"app.kind=handoff", "app.source_display_id=12", "app.display_id=0",
				"app.ownership=shared", "app.state_preserved=null",
				"app.foreground_restore=not_requested",
			},
		},
		{
			name:      "close",
			arguments: `{"kind":"close","reference":"O1","target":{"app":"com.example"}}`,
			service: `{"operation_id":%q,"outcome":"executed","app":{"kind":"close","display_id":12,` +
				`"tasks":[{"task_id":42,"outcome":"executed","recents_cleaned":true},` +
				`{"task_id":43,"outcome":"executed","recents_cleaned":false}],"complete":false}}`,
			typed: []string{
				"app.kind=close", "app.display_id=12",
				`app.tasks=[{"task_id":42,"outcome":"executed","recents_cleaned":true},{"task_id":43,"outcome":"executed","recents_cleaned":false}]`,
				"app.complete=false",
			},
		},
		{
			name:      "kill",
			arguments: `{"kind":"kill","reference":"O1","target":{"task_id":42}}`,
			service: `{"operation_id":%q,"outcome":"executed","app":{"kind":"kill",` +
				`"package":"com.example","source_task_id":42,"display_id":12,` +
				`"target_pids":[1234,1235],"killed_pids":[1234,1235],` +
				`"remaining_pids":[],"restarted":null}}`,
			typed: []string{
				"app.kind=kill", "app.source_task_id=42", "app.display_id=12",
				"app.target_pids=[1234,1235]", "app.killed_pids=[1234,1235]",
				"app.remaining_pids=[]", "app.restarted=null",
			},
		},
		{
			name:      "kill uncertain",
			arguments: `{"kind":"kill","reference":"O1","target":{"task_id":42}}`,
			service: `{"operation_id":%q,"outcome":"uncertain","app":{"kind":"kill",` +
				`"package":"com.example","source_task_id":42,"display_id":12,` +
				`"target_pids":[1234],"killed_pids":[1234],"restarted":null},` +
				`"error":{"code":"kill_unconfirmed","message":"进程终止未确认"}}`,
			isError: true,
			typed:   []string{"app.kind=kill", "app.restarted=null"},
		},
		{
			name:      "claim not executed",
			arguments: `{"kind":"claim","reference":"O1","target":{"task_id":43}}`,
			service: `{"operation_id":%q,"outcome":"not_executed","app":{"kind":"claim",` +
				`"package":"com.example","task_id":43,"source_display_id":0},` +
				`"error":{"code":"window_action_not_supported_on_primary_display","message":"主屏不支持该操作"}}`,
			isError: true,
			typed:   []string{"app.kind=claim", "app.source_display_id=0", "outcome=not_executed"},
		},
		{
			name:      "find read failure",
			arguments: `{"kind":"find"}`,
			query:     true,
			service: `{"operation_id":%q,"app":{"kind":"find","items":[],"total":0},` +
				`"error":{"code":"device_unavailable","message":"应用清单读取失败"}}`,
			isError: true,
			typed:   []string{"app.kind=find"},
		},
		{
			name:      "close partial",
			arguments: `{"kind":"close","reference":"O1","target":{"app":"com.example"}}`,
			service: `{"operation_id":%q,"outcome":"uncertain","app":{"kind":"close","display_id":12,` +
				`"tasks":[{"task_id":42,"outcome":"executed","recents_cleaned":true},` +
				`{"task_id":43,"outcome":"uncertain","recents_cleaned":false,` +
				`"error":{"code":"task_remove_unconfirmed","message":"关闭未确认"}}],"complete":false},` +
				`"error":{"code":"task_remove_unconfirmed","message":"部分任务的关闭结果未确认"}}`,
			isError: true,
			typed: []string{
				`app.tasks=[{"task_id":42,"outcome":"executed","recents_cleaned":true},{"task_id":43,"outcome":"uncertain","recents_cleaned":false,"error":{"code":"task_remove_unconfirmed","message":"关闭未确认"}}]`,
				"app.complete=false",
			},
		},
	}
}

func TestAppResultParityAcrossModes(t *testing.T) {
	for _, fixture := range appModeFixtures() {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			fake := newTestDeviceService(t)
			fake.setHandler("app", func(writer http.ResponseWriter, request *http.Request, body string) {
				writeTestJSON(writer, http.StatusOK, fmt.Sprintf(fixture.service, testOperationHeader(request)))
			})
			adapter := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.DiscardHandler)}
			for _, mode := range []ResultMode{ResultModeText, ResultModeStructured, ResultModeBoth} {
				mode := mode
				t.Run(mode.String(), func(t *testing.T) {
					// The expectation is hand-written from the public contract and
					// only the adapter-allocated P is substituted; it is never a
					// marshal of the projection under test.
					out := adapter.handle(context.Background(), testDefinitionByName(t, "app"), []byte(fixture.arguments), mode)
					requests := fake.requestsFor("app")
					if len(requests) == 0 {
						t.Fatal("app did not reach the service")
					}
					operation := requests[len(requests)-1].OperationID
					if !strings.HasPrefix(operation, "P") {
						t.Fatalf("app did not allocate a P: %q", operation)
					}
					expectedRaw := []byte(fmt.Sprintf(fixture.service, operation))
					expected, err := decodeResultJSON(expectedRaw)
					if err != nil {
						t.Fatal(err)
					}
					if out.IsError != fixture.isError {
						t.Fatalf("IsError = %v, want %v\n%s", out.IsError, fixture.isError, testTextContent(t, out))
					}
					if mode == ResultModeText {
						if out.StructuredContent != nil {
							t.Fatal("text mode emitted structuredContent")
						}
						decoded, flag, err := decodeResultText(t, testTextContent(t, out))
						if err != nil {
							t.Fatalf("decode text: %v\n%s", err, testTextContent(t, out))
						}
						if flag != fixture.isError || !reflect.DeepEqual(decoded, expected) {
							t.Fatalf("text facts differ from the hand-written expectation\nwant=%#v\ngot=%#v\n%s",
								expected, decoded, testTextContent(t, out))
						}
						if fixture.query {
							if _, ok := decoded["outcome"]; ok {
								t.Fatalf("query text carried an outcome: %s", testTextContent(t, out))
							}
						}
					} else {
						if out.StructuredContent == nil {
							t.Fatal("structured mode omitted structuredContent")
						}
						actual, err := decodeResultJSON([]byte(testStructuredJSON(t, out)))
						if err != nil || !reflect.DeepEqual(actual, expected) {
							t.Fatalf("structuredContent differs from the hand-written expectation err=%v\nwant=%#v\ngot=%#v",
								err, expected, actual)
						}
						if fixture.query {
							if _, ok := actual.(map[string]any)["outcome"]; ok {
								t.Fatal("query structuredContent carried an outcome")
							}
						}
						if mode == ResultModeBoth {
							decoded, flag, err := decodeResultText(t, testTextContent(t, out))
							if err != nil || flag != fixture.isError || !reflect.DeepEqual(decoded, expected) {
								t.Fatalf("both-mode text differs from structuredContent err=%v\nwant=%#v\ngot=%#v",
									err, expected, decoded)
							}
						}
					}
					if mode != ResultModeStructured {
						text := testTextContent(t, out)
						for _, want := range fixture.typed {
							if strings.HasSuffix(want, "=") {
								requireTestContains(t, text, want)
								continue
							}
							requireTestContains(t, text, want+"\n")
						}
						requireTestNotContains(t, text, `json v1 path="/app`)
					}
				})
			}
		})
	}
}

// ---------------------------------------------------------------- IsError

func TestAppIsErrorByKindAndOutcome(t *testing.T) {
	app := func(kind string) *protocol.Result {
		return &protocol.Result{App: &protocol.AppView{Kind: kind}}
	}
	cases := []struct {
		name   string
		result *protocol.Result
		want   bool
	}{
		{"find collection", &protocol.Result{App: &protocol.AppView{Kind: protocol.AppKindFind, Items: []protocol.AppItem{}}}, false},
		{"find no collection no summary", app(protocol.AppKindFind), true},
		{"find read error", &protocol.Result{App: &protocol.AppView{Kind: protocol.AppKindFind}, Error: &protocol.Error{Code: "device_unavailable"}}, true},
		{"find summary", &protocol.Result{OperationState: "finished", App: &protocol.AppView{Kind: protocol.AppKindFind}}, false},
		{"list collection", &protocol.Result{App: &protocol.AppView{Kind: protocol.AppKindList, TaskItems: []protocol.AppTaskItem{}}}, false},
		{"list no collection no summary", app(protocol.AppKindList), true},
		{"list summary", &protocol.Result{OperationState: "finished", App: &protocol.AppView{Kind: protocol.AppKindList}}, false},
		{"open executed", &protocol.Result{Outcome: "executed", App: app(protocol.AppKindOpen).App}, false},
		{"open uncertain", &protocol.Result{Outcome: "uncertain", App: app(protocol.AppKindOpen).App}, true},
		{"open not executed", &protocol.Result{Outcome: "not_executed", App: app(protocol.AppKindOpen).App}, true},
		{"claim executed", &protocol.Result{Outcome: "executed", App: app(protocol.AppKindClaim).App}, false},
		{"handoff executed", &protocol.Result{Outcome: "executed", App: app(protocol.AppKindHandoff).App}, false},
		{"handoff foreground restore unconfirmed", &protocol.Result{Outcome: "executed", App: &protocol.AppView{
			Kind: protocol.AppKindHandoff, ForegroundRestore: protocol.ForegroundRestoreUnconfirmed,
		}}, false},
		{"close executed recents unconfirmed", &protocol.Result{Outcome: "executed", App: &protocol.AppView{Kind: protocol.AppKindClose, Complete: rmfPtr(false)}}, false},
		{"open executed post observation failed", &protocol.Result{
			Outcome: "executed", App: app(protocol.AppKindOpen).App,
			Observation: &protocol.Observation{Status: "failed", Error: &protocol.Error{Code: protocol.CodeHierarchyFailed}},
		}, false},
		{"kill executed", &protocol.Result{Outcome: "executed", App: app(protocol.AppKindKill).App}, false},
		{"kill uncertain", &protocol.Result{Outcome: "uncertain", App: app(protocol.AppKindKill).App}, true},
		{"missing app", &protocol.Result{Error: &protocol.Error{Code: "device_unavailable"}}, true},
		{"nil result", nil, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			execution := &execution{result: testCase.result}
			if got := execution.isError("app"); got != testCase.want {
				t.Fatalf("isError = %v, want %v", got, testCase.want)
			}
		})
	}
}

// A cancelled query is always an error even when the service already delivered
// a complete collection.
func TestAppCancelledQueryKnownResultStaysError(t *testing.T) {
	execution := &execution{
		result: &protocol.Result{App: &protocol.AppView{
			Kind: protocol.AppKindFind, Items: []protocol.AppItem{},
		}},
	}
	if execution.isError("app") {
		t.Fatal("uncancelled empty find should not be an error")
	}
	execution.cancelled = true
	if !projectExecution(execution, "app", ResultModeBoth).IsError {
		t.Fatal("projection dropped the cancellation IsError override")
	}
}

// ---------------------------------------------------------------- cancellation

func TestCancellationAppQueryDoesNotEnd(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("app", fake.blockTestUntilStopped)
	adapter := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.DiscardHandler)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultChannel := make(chan *execution, 1)
	go func() {
		resultChannel <- adapter.run(ctx, testDefinitionByName(t, "app"), []byte(`{"kind":"find"}`))
	}()
	appRequests := fake.waitFor(t, "app", 1, mcpTestTimeout)
	operation := appRequests[0].OperationID
	cancel()

	var execution *execution
	select {
	case execution = <-resultChannel:
	case <-time.After(mcpTestTimeout):
		t.Fatal("cancelled app query did not return")
	}
	if fake.requestCount("end") != 0 {
		t.Fatal("cancelled app query issued an end")
	}
	if !execution.cancelled || !execution.isError("app") {
		t.Fatal("cancelled query was not an error")
	}
	if execution.result.OperationID != operation {
		t.Fatalf("operation_id = %q, want %q", execution.result.OperationID, operation)
	}
	if execution.result.Outcome != "" {
		t.Fatalf("cancelled query synthesized outcome %q", execution.result.Outcome)
	}
	if execution.result.Recovery != nil {
		t.Fatalf("cancelled query kept recovery %#v", execution.result.Recovery)
	}
	if execution.result.App == nil || execution.result.App.Kind != protocol.AppKindFind ||
		execution.result.App.Items != nil || execution.result.App.TaskItems != nil {
		t.Fatalf("query call metadata = %#v", execution.result.App)
	}
	if execution.result.Error == nil || execution.result.Error.Code != "call_cancelled" ||
		execution.result.Error.Message != cancellationQueryMessage {
		t.Fatalf("cancellation error = %#v", execution.result.Error)
	}
	text := testTextContent(t, projectExecution(execution, "app", ResultModeBoth))
	for _, want := range []string{
		"app.kind=find",
		"error.code=call_cancelled",
		"查询已取消；未发起 end",
		`"mcp.cancelled":true`,
		`"mcp.cancellation":"查询已取消；未发起 end"`,
	} {
		requireTestContains(t, text, want)
	}
	for _, unwanted := range []string{"outcome=", "recovery.entry=", "app.items", "app.total"} {
		requireTestNotContains(t, text, unwanted)
	}
}

// A known service result keeps its business fields and its original error, but
// a cancelled query must not copy the original recovery or invent an outcome.
func TestCancellationAppQueryKeepsKnownFacts(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("end", testEndHandler())
	adapter := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.DiscardHandler)}
	ready := make(chan struct{})
	release := make(chan struct{})
	var source *protocol.Result
	call := &preparedCall{
		kind: protocol.AppKindList,
		run: func(ctx context.Context, operationID string) (*client.Response, error) {
			close(ready)
			<-release
			source = &protocol.Result{
				OperationID: operationID,
				Outcome:     "not_executed",
				App: &protocol.AppView{
					Kind:      protocol.AppKindList,
					DisplayID: rmfPtr(12),
					TaskItems: []protocol.AppTaskItem{
						{TaskID: 42, Package: "com.example", Ownership: protocol.TaskOwnershipOwned},
					},
					Total: 1, HasTotal: true, Truncated: rmfPtr(false),
					CapturedAt: "2026-11-12T03:00:00+08:00",
				},
				Error: &protocol.Error{
					Code: "device_unavailable", Message: "任务清单读取失败",
					Details: map[string]any{"phase": "inventory"},
				},
				Recovery: &protocol.Recovery{Entry: protocol.RecoveryEntryRetry, Reference: protocol.RecoveryReferenceNone},
			}
			return &client.Response{Result: source}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	resultChannel := make(chan *execution, 1)
	go func() { resultChannel <- adapter.invoke(ctx, call, "P42") }()
	<-ready
	cancel()
	close(release)
	var execution *execution
	select {
	case execution = <-resultChannel:
	case <-time.After(mcpTestTimeout):
		t.Fatal("cancelled list did not return")
	}
	if fake.requestCount("end") != 0 {
		t.Fatal("cancelled list issued an end")
	}
	if execution.result.Error == nil || execution.result.Error.Code != "device_unavailable" ||
		execution.result.Error.Message != "任务清单读取失败" {
		t.Fatalf("known service error was replaced: %#v", execution.result.Error)
	}
	if len(execution.result.App.TaskItems) != 1 || execution.result.App.TaskItems[0].TaskID != 42 {
		t.Fatalf("known collection was lost: %#v", execution.result.App)
	}
	if execution.result.Outcome != "" {
		t.Fatalf("cancelled query kept outcome %q", execution.result.Outcome)
	}
	canonical, err := canonicalResult(execution)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(canonical, &value); err != nil {
		t.Fatal(err)
	}
	if _, ok := value["outcome"]; ok {
		t.Fatalf("canonical cancelled query kept outcome: %s", canonical)
	}
	if _, ok := value["recovery"]; ok {
		t.Fatalf("canonical cancelled query kept recovery: %s", canonical)
	}
	details := value["error"].(map[string]any)["details"].(map[string]any)
	if details["phase"] != "inventory" || details["mcp.cancelled"] != true ||
		details["mcp.cancellation"] != cancellationQueryMessage {
		t.Fatalf("cancellation details = %#v", details)
	}
	items := value["app"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("canonical collection = %#v", items)
	}
	diagnostic := testTextContent(t, projectExecution(execution, "app", ResultModeStructured))
	requireTestContains(t, diagnostic, "app.kind=list\n")
	requireTestContains(t, diagnostic, `"mcp.cancellation"`)
	requireTestNotContains(t, diagnostic, "app.items")
	requireTestNotContains(t, diagnostic, "app.display_id")
	if source == nil || source.Outcome != "not_executed" || source.Recovery == nil ||
		source.Error == nil || source.Error.Code != "device_unavailable" {
		t.Fatalf("cancellation mutated the service result: %#v", source)
	}
}

func TestCancellationAppMutationKeepsBoundedEnd(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("app", fake.blockTestUntilStopped)
	fake.setHandler("end", testEndHandler())
	adapter := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.DiscardHandler)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultChannel := make(chan *execution, 1)
	go func() {
		resultChannel <- adapter.run(ctx, testDefinitionByName(t, "app"),
			[]byte(`{"kind":"close","reference":"O1","target":{"task_id":42}}`))
	}()
	appRequests := fake.waitFor(t, "app", 1, mcpTestTimeout)
	operation := appRequests[0].OperationID
	cancel()
	var execution *execution
	select {
	case execution = <-resultChannel:
	case <-time.After(mcpTestTimeout):
		t.Fatal("cancelled close did not return")
	}
	endRequests := fake.waitFor(t, "end", 1, mcpTestTimeout)
	if !strings.Contains(endRequests[0].Body, fmt.Sprintf("%q", operation)) {
		t.Fatalf("end body %s does not reference %s", endRequests[0].Body, operation)
	}
	if !execution.cancelled || !execution.isError("app") {
		t.Fatal("cancelled mutation must be an error")
	}
	if execution.result.App == nil || execution.result.App.Kind != protocol.AppKindClose ||
		execution.result.App.Tasks != nil {
		t.Fatalf("mutation call metadata = %#v", execution.result.App)
	}
	if execution.result.Error == nil || execution.result.Error.Code != "call_cancelled" ||
		!strings.Contains(execution.result.Error.Message, "有界 end("+operation+")") {
		t.Fatalf("cancellation error = %#v", execution.result.Error)
	}
	if execution.result.Session == nil || execution.result.Session.State != "ended" {
		t.Fatalf("end session facts were not merged: %#v", execution.result.Session)
	}
	if execution.result.Cleanup == nil || execution.result.Cleanup.State != protocol.CleanupPending {
		t.Fatalf("end cleanup facts were not merged: %#v", execution.result.Cleanup)
	}
	text := testTextContent(t, projectExecution(execution, "app", ResultModeBoth))
	for _, want := range []string{"app.kind=close", "有界 end", "不证明", `"mcp.cancelled":true`} {
		requireTestContains(t, text, want)
	}
	requireTestNotContains(t, text, cancellationQueryMessage)
}

// ---------------------------------------------------------------- transport metadata

func TestAppTransportFailureKeepsKindOnly(t *testing.T) {
	cases := []struct {
		name string
		body string
		kind string
	}{
		{"close", `{"kind":"close","reference":"O1","target":{"task_id":42}}`, protocol.AppKindClose},
		{"find", `{"kind":"find"}`, protocol.AppKindFind},
		{"list", `{"kind":"list","reference":"O1"}`, protocol.AppKindList},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			fake := newTestDeviceService(t)
			fake.setHandler("app", func(writer http.ResponseWriter, request *http.Request, body string) {
				writer.WriteHeader(http.StatusBadGateway)
			})
			adapter := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.DiscardHandler)}
			execution := adapter.run(context.Background(), testDefinitionByName(t, "app"), []byte(testCase.body))
			if execution.result.App == nil || execution.result.App.Kind != testCase.kind {
				t.Fatalf("transport failure did not keep kind metadata: %#v", execution.result.App)
			}
			if execution.result.App.Tasks != nil || execution.result.App.Complete != nil ||
				execution.result.App.DisplayID != nil || execution.result.App.TaskID != nil ||
				execution.result.App.Items != nil || execution.result.App.TaskItems != nil {
				t.Fatalf("adapter invented device facts: %#v", execution.result.App)
			}
			if execution.result.Error == nil || !execution.isError("app") {
				t.Fatal("transport failure was not an error")
			}
			canonical, err := canonicalResult(execution)
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(canonical, &value); err != nil {
				t.Fatal(err)
			}
			appValue, ok := value["app"].(map[string]any)
			if !ok || len(appValue) != 1 || appValue["kind"] != testCase.kind {
				t.Fatalf("adapter call metadata = %#v, want only kind", value["app"])
			}
		})
	}
}

// ---------------------------------------------------------------- repeated P summary

func TestAppQuerySummaryKeepsKindWithoutOutcome(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("app", func(writer http.ResponseWriter, request *http.Request, body string) {
		writeTestJSON(writer, http.StatusOK, `{"operation_id":"P1","operation_state":"finished","phase":"query_cancelled","app":{"kind":"list"}}`)
	})
	adapter := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.DiscardHandler)}
	for _, mode := range []ResultMode{ResultModeText, ResultModeStructured, ResultModeBoth} {
		execution := adapter.run(context.Background(), testDefinitionByName(t, "app"), []byte(`{"kind":"list","reference":"P1"}`))
		if execution.isError("app") {
			t.Fatalf("%s: no-error query summary became an error", mode)
		}
		out := projectExecution(execution, "app", mode)
		if out.IsError {
			t.Fatalf("%s: projection marked the summary as an error", mode)
		}
		if mode != ResultModeStructured {
			text := testTextContent(t, out)
			for _, want := range []string{"operation_state=finished", "phase=query_cancelled", "app.kind=list"} {
				requireTestContains(t, text, want)
			}
			for _, unwanted := range []string{"outcome=", "app.items", "app.total"} {
				requireTestNotContains(t, text, unwanted)
			}
		}
		if mode != ResultModeText {
			var structuredValue map[string]any
			if err := json.Unmarshal([]byte(testStructuredJSON(t, out)), &structuredValue); err != nil {
				t.Fatal(err)
			}
			appValue, ok := structuredValue["app"].(map[string]any)
			if !ok || len(appValue) != 1 || appValue["kind"] != protocol.AppKindList {
				t.Fatalf("%s: query summary fabricated app facts: %#v", mode, structuredValue["app"])
			}
		}
	}
}

// ---------------------------------------------------------------- text classification

func appExpectedFieldKinds() map[string]resultFieldKind {
	return map[string]resultFieldKind{
		"app.kind":               fieldToken,
		"app.ownership":          fieldToken,
		"app.foreground_restore": fieldToken,
		"app.captured_at":        fieldToken,
		"app.package":            fieldString,
		"app.task_id":            fieldLiteral,
		"app.source_task_id":     fieldLiteral,
		"app.display_id":         fieldLiteral,
		"app.source_display_id":  fieldLiteral,
		"app.total":              fieldLiteral,
		"app.reused":             fieldLiteral,
		"app.state_preserved":    fieldLiteral,
		"app.complete":           fieldLiteral,
		"app.truncated":          fieldLiteral,
		"app.restarted":          fieldLiteral,
		"app.items":              fieldJSON,
		"app.tasks":              fieldJSON,
		"app.foreground":         fieldJSON,
		"app.target_pids":        fieldJSON,
		"app.killed_pids":        fieldJSON,
		"app.remaining_pids":     fieldJSON,
	}
}

// appFieldClassificationProblem is the known-field gate: every declared app
// path must exist with its exact classification. The reverse-control test
// removes app.tasks and expects this gate to fail.
func appFieldClassificationProblem(table map[string]resultFieldKind) string {
	for path, want := range appExpectedFieldKinds() {
		got, ok := table[path]
		if !ok {
			return path + " is not classified"
		}
		if got != want {
			return fmt.Sprintf("%s is %s, want %s", path, rmfFieldKindName(got), rmfFieldKindName(want))
		}
	}
	return ""
}

func TestAppFieldClassification(t *testing.T) {
	if problem := appFieldClassificationProblem(resultFields); problem != "" {
		t.Fatalf("app field classification: %s", problem)
	}
	text, err := encodeResultText([]byte(`{"app":{"kind":"close","display_id":12,`+
		`"tasks":[{"task_id":42,"outcome":"executed","recents_cleaned":true}],"complete":false}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	requireTestContains(t, text, `app.tasks=[{"task_id":42,"outcome":"executed","recents_cleaned":true}]`)
	requireTestNotContains(t, text, `json v1 path="/app/tasks"`)

	// Reverse control: deleting the fieldJSON classification must break the
	// known-field gate rather than silently falling back to json v1.
	if problem := func() string {
		table := make(map[string]resultFieldKind, len(resultFields))
		for path, kind := range resultFields {
			table[path] = kind
		}
		delete(table, "app.tasks")
		return appFieldClassificationProblem(table)
	}(); !strings.Contains(problem, "app.tasks") {
		t.Fatalf("known-field gate did not detect the removed classification: %q", problem)
	}
}

// ---------------------------------------------------------------- diagnostic whitelist

func TestAppDiagnosticOnlyCarriesKind(t *testing.T) {
	raw := []byte(`{"operation_id":"P9","outcome":"uncertain","app":{"kind":"close","display_id":12,` +
		`"tasks":[{"task_id":42,"outcome":"executed","recents_cleaned":true}],"complete":false},` +
		`"error":{"code":"task_remove_unconfirmed","message":"关闭未确认"},` +
		`"recovery":{"entry":"observe","reference":"P","input_state":"retired"}}`)
	text, err := encodeResultDiagnostic(raw)
	if err != nil {
		t.Fatal(err)
	}
	requireTestContains(t, text, "diagnostic v1 is_error=true\n")
	requireTestContains(t, text, "app.kind=close\n")
	requireTestContains(t, text, "operation_id=P9\n")
	requireTestContains(t, text, "outcome=uncertain\n")
	requireTestContains(t, text, "error.code=task_remove_unconfirmed\n")
	requireTestContains(t, text, "recovery.entry=observe\n")
	requireTestNotContains(t, text, "app.display_id")
	requireTestNotContains(t, text, "app.tasks")
	requireTestNotContains(t, text, "app.complete")
}
