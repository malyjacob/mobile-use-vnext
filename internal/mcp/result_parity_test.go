package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/client"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// This first gate deliberately uses the existing both-mode entry point. A
// successful shell call must expose its output to a text-only consumer too.
func TestBothModeParity(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("shell", func(w http.ResponseWriter, r *http.Request, body string) {
		writeTestJSON(w, 200, `{"operation_id":"P1","outcome":"executed","shell":{"termination":"exited","exit_code":0,"output":"parity-output","truncated":false}}`)
	})
	a := &adapter{client: newTestDeviceClient(t, fake)}
	result := a.handle(context.Background(), testDefinitionByName(t, "shell"), []byte(`{"reference":"O1","command":"echo parity-output"}`))
	if !strings.Contains(testTextContent(t, result), "parity-output") {
		t.Fatal("text encoding lost shell.output present in structuredContent")
	}
}

// TestHTTPNestedErrorDetailsAcrossModes runs one real client HTTP decode and
// then the three MCP projections of that same execution. It guards the full
// service-result → client → canonical JSON path for static candidates.
func TestHTTPNestedErrorDetailsAcrossModes(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("act", func(w http.ResponseWriter, r *http.Request, body string) {
		writeTestJSON(w, http.StatusUnprocessableEntity,
			`{"operation_id":"P15","operation_state":"finished","outcome":"not_executed",`+
				`"error":{"code":"no_safe_trajectory","message":"无法生成安全滚动轨迹","details":`+
				`{"reason":"trajectory_blocked","scope":"target","scope_ref":"n56","candidates":[`+
				`{"ref":"n66","role":"scroll","actions":["swipe"],"bounds":[0,363,1080,2265],"relation":"child","executable":true},`+
				`{"ref":"n57","role":"scroll","actions":["swipe"],"bounds":[0,363,1080,2265],"relation":"child","executable":false}],`+
				`"blocking_refs":["n57","n58"]}},`+
				`"recovery":{"entry":"retry","reference":"O","input_state":"effective"}}`)
	})
	a := &adapter{client: newTestDeviceClient(t, fake)}
	execution := a.run(context.Background(), testDefinitionByName(t, "act"),
		[]byte(`{"reference":"O1","operation":{"type":"tap","target":{"ref":"n491"}}}`))
	if execution.result == nil || execution.result.Error == nil {
		t.Fatalf("HTTP nested failure was not projected: %#v", execution)
	}
	details := execution.result.Error.Details
	candidates, ok := details["candidates"].([]any)
	if !ok || len(candidates) != 2 {
		t.Fatalf("client lost candidates: %#v", details)
	}
	first, ok := candidates[0].(map[string]any)
	if !ok || first["ref"] != "n66" || first["executable"] != true {
		t.Fatalf("client lost first executable candidate: %#v", candidates[0])
	}
	bounds, ok := first["bounds"].([]any)
	if !ok || len(bounds) != 4 || bounds[0] != float64(0) || bounds[3] != float64(2265) {
		t.Fatalf("client lost candidate bounds: %#v", first["bounds"])
	}
	blocking, ok := details["blocking_refs"].([]any)
	if !ok || len(blocking) != 2 || blocking[0] != "n57" || blocking[1] != "n58" {
		t.Fatalf("client lost blocking_refs: %#v", details)
	}

	canonical, err := canonicalResult(execution)
	if err != nil {
		t.Fatalf("canonicalResult: %v", err)
	}
	want := expectedResultV4(t, canonical, true, execution.requestRef)
	for _, mode := range []ResultMode{ResultModeBoth, ResultModeText, ResultModeStructured} {
		out := projectExecution(execution, "act", mode)
		if !out.IsError {
			t.Fatalf("mode %v: nested failure was not an error", mode)
		}
		if mode == ResultModeStructured {
			if got := rmfStructuredBytes(t, out.StructuredContent); !bytes.Equal(got, canonical) {
				t.Fatalf("mode %v: structuredContent differs from canonical", mode)
			}
			diagnostic := testTextContent(t, out)
			requireTestContains(t, diagnostic, "error.code=no_safe_trajectory")
			requireTestContains(t, diagnostic, `"candidates":[`)
			requireTestContains(t, diagnostic, `"blocking_refs":["n57","n58"]`)
			requireTestContains(t, diagnostic, `"executable":true`)
			continue
		}
		text := testTextContent(t, out)
		decoded, flag, err := decodeResultText(t, text)
		if err != nil || !flag || !sameJSONValue(want, decoded) {
			t.Fatalf("mode %v text parity error=%v flag=%t", mode, err, flag)
		}
		requireTestContains(t, text, "no_safe_trajectory")
		requireTestContains(t, text, `"executable":true`)
	}
}

func TestAgentResultLosslessParity(t *testing.T) {
	for _, fixture := range resultModeFixtures() {
		t.Run(fixture.tool+"/"+fixture.name, func(t *testing.T) {
			var r protocol.Result
			if err := jsonv2.Unmarshal([]byte(fixture.raw), &r); err != nil {
				t.Fatal(err)
			}
			e := &execution{result: &r, cancelled: fixture.cancelled, cancellation: "取消；不证明动作撤回", image: &client.Image{Data: []byte{1, 2, 3}, MediaType: "image/jpeg"}}
			a, err := canonicalResult(e)
			if err != nil {
				t.Fatal(err)
			}
			want, err := decodeResultJSON(a)
			if err != nil {
				t.Fatal(err)
			}
			var firstImages []*sdk.ImageContent
			var firstError bool
			for i, mode := range []ResultMode{ResultModeBoth, ResultModeText, ResultModeStructured} {
				out := projectExecution(e, fixture.tool, mode)
				if out.Content == nil {
					t.Fatal("nil content")
				}
				images := testImageContents(out)
				if len(images) != 1 {
					t.Fatal("image missing")
				}
				if i == 0 {
					firstImages = images
					firstError = out.IsError
				} else if !reflect.DeepEqual(images, firstImages) || out.IsError != firstError {
					t.Fatal("envelope parity")
				}
				if mode != ResultModeStructured {
					wantText := expectedResultV4(t, a, out.IsError)
					decoded, flag, err := decodeResultText(t, testTextContent(t, out))
					if err != nil || !sameJSONValue(wantText, decoded) || flag != out.IsError {
						t.Fatalf("text parity mode=%v err=%v\nwant=%#v\ngot=%#v\n%s", mode, err, wantText, decoded, testTextContent(t, out))
					}
				}
				if mode == ResultModeText {
					if out.StructuredContent != nil {
						t.Fatal("text mode emitted structuredContent")
					}
				} else {
					got, err := decodeResultJSON([]byte(testStructuredJSON(t, out)))
					if err != nil || !reflect.DeepEqual(want, got) {
						t.Fatal("structured parity")
					}
				}
				if mode == ResultModeStructured {
					for _, c := range out.Content {
						if text, ok := c.(*sdk.TextContent); ok {
							if !out.IsError || !strings.HasPrefix(text.Text, "diagnostic v1 is_error=true\n") || !strings.HasSuffix(text.Text, "\nend diagnostic\n") {
								t.Fatal("incorrect diagnostic")
							}
						}
					}
				}
			}
		})
	}
}

// TestEffectQualityModeParity proves text and both omit effect/quality from
// readable text while structured and the structured half of both retain the
// complete protocol.Result values, including samples=0 and not_sampled.
func TestEffectQualityModeParity(t *testing.T) {
	source := &protocol.Result{
		OperationID:     "P1",
		Outcome:         "executed",
		Phase:           "completed",
		Effect:          &protocol.Effect{State: protocol.EffectUnknown},
		TargetReference: "O1",
		Observation: &protocol.Observation{
			Status: "complete",
			ID:     "O2",
			Quality: &protocol.Quality{
				State: protocol.QualityUnknown, Samples: 0, StopReason: protocol.QualityStopNotSampled,
			},
		},
	}
	e := &execution{result: source}
	canonical, err := canonicalResult(e)
	if err != nil {
		t.Fatal(err)
	}
	want, err := decodeResultJSON(canonical)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []ResultMode{ResultModeBoth, ResultModeText, ResultModeStructured} {
		out := projectExecution(e, "act", mode)
		if out.IsError {
			t.Fatalf("mode %v: unexpected error", mode)
		}
		if mode == ResultModeStructured {
			got, err := decodeResultJSON([]byte(testStructuredJSON(t, out)))
			if err != nil || !reflect.DeepEqual(want, got) {
				t.Fatalf("structured parity err=%v got=%#v", err, got)
			}
			continue
		}
		if mode == ResultModeBoth {
			got, err := decodeResultJSON([]byte(testStructuredJSON(t, out)))
			if err != nil || !reflect.DeepEqual(want, got) {
				t.Fatalf("both structured parity err=%v got=%#v", err, got)
			}
		}
		text := testTextContent(t, out)
		wantText := expectedResultV4(t, canonical, false)
		decoded, flag, err := decodeResultText(t, text)
		if err != nil || flag || !sameJSONValue(wantText, decoded) {
			t.Fatalf("text parity mode=%v err=%v", mode, err)
		}
		requireTestNotContains(t, text, "effect=")
		requireTestNotContains(t, text, "quality=")
	}
}

type resultModeFixture struct {
	tool, name, raw string
	cancelled       bool
}

func resultModeFixtures() []resultModeFixture {
	var fixtures []resultModeFixture
	add := func(tool, name, raw string) {
		fixtures = append(fixtures, resultModeFixture{tool: tool, name: name, raw: raw})
	}
	v3RoleTree := `{"format":"full","scope":"reported_visible_with_context","view":"compact","root":{` +
		`"ref":"n1","role":"root","bounds":[0,0,1080,2400],"children":[` +
		`{"ref":"n2","role":"view"},` +
		`{"chain":["n10","n11"],"role":"view","children":[` +
		`{"ref":"n12","role":"text","label":["child"]}]},` +
		`{"ref":"n3","role":"view","bounds":[5,5],"actions":["tap"]}]}}`
	legacyChainDelta := `{"format":"delta","base":"O1","view":"compact",` +
		`"add":[{"ref":"n20","role":"view","children":[` +
		`{"chain":["n21","n22"],"role":"view","children":[` +
		`{"ref":"n23","role":"button","label":["增加"],"actions":["tap"]}]}]}],` +
		`"replace":{"n30":{"ref":"n30","role":"group","children":[` +
		`{"chain":["n31","n32"],"role":"group","children":[` +
		`{"ref":"n33","role":"text","label":["替换"]}]}]}}}`
	add("status", "not_seen", `{"operation_state":"not_seen","can_start":true,"session":null}`)
	add("status", "closed", `{"request_closed":true,"can_start":false,"blocking_operation_id":"P1","blocking_code":"cleanup_pending"}`)
	add("start", "full", `{"operation_id":"P1","outcome":"executed","session":`+testActiveSessionJSON+`,"observation":{"status":"complete","id":"O1","tree":`+testFullTreeJSON+`}}`)
	add("start", "first_observation_failed", `{"operation_id":"P1","outcome":"executed","observation":{"status":"failed","error":{"code":"hierarchy_unavailable","message":"读取失败"}}}`)
	for _, tree := range []struct{ name, raw string }{{"full", testFullTreeJSON}, {"delta", testDeltaTreeJSON}} {
		add("observe", tree.name, `{"observation":{"status":"complete","id":"O2","tree":`+tree.raw+`}}`)
	}
	add("observe", "v3_role_text", `{"observation":{"status":"complete","id":"O2","tree":`+v3RoleTree+`}}`)
	add("observe", "legacy_v3_chain_delta", `{"observation":{"status":"complete","id":"O4","tree":`+legacyChainDelta+`}}`)
	add("observe", "missing", `{"observation":{"status":"partial","id":"O1","missing":["image"],"image":{"error":{"code":"image_unavailable","message":"缺图"}}}}`)
	add("observe", "read_only_image", `{"observation":{"status":"failed","image":{"read_only":true,"width":2,"height":3,"media_type":"image/jpeg","base64":"secret"}}}`)
	add("observe", "empty", `{}`)
	// Condition wait: a timed-out wait still publishes a frame and stays a
	// success; a condition hit carries both the frozen trigger and the matched
	// subset; a failed verification acquisition keeps the trigger and elapsed
	// time but never a match list.
	add("wait_for", "timeout", `{"operation_id":"P1","phase":"finished","observation":{"status":"complete","id":"O2","quality":{"state":"unknown","samples":0,"stop_reason":"not_sampled"}},"wait":{"trigger":"timeout","matched":["timeout"],"elapsed_ms":1200}}`)
	add("wait_for", "condition_stable", `{"operation_id":"P1","phase":"finished","observation":{"status":"complete","id":"O3","quality":{"state":"stable","samples":3,"stop_reason":"stable"}},"wait":{"trigger":"query","elapsed_ms":2400}}`)
	add("wait_for", "failed", `{"operation_id":"P1","phase":"failed","observation":{"status":"failed","missing":["tree"],"error":{"code":"hierarchy_unavailable","message":"读取失败"}},"wait":{"trigger":"absent_query","elapsed_ms":3000},"error":{"code":"hierarchy_unavailable","message":"读取失败"},"recovery":{"entry":"observe","reference":"P"}}`)
	for _, outcome := range []string{"executed", "not_executed", "uncertain"} {
		add("act", outcome, `{"operation_id":"P1","outcome":"`+outcome+`","error":{"code":"target_changed","message":"目标变化"},"recovery":{"entry":"observe","reference":"P"}}`)
	}
	add("act", "no_safe_trajectory_details", `{"operation_id":"P15","operation_state":"finished","outcome":"not_executed",`+
		`"error":{"code":"no_safe_trajectory","message":"无法生成安全滚动轨迹","details":`+
		`{"reason":"trajectory_blocked","scope":"target","scope_ref":"n56","candidates":[`+
		`{"ref":"n66","role":"scroll","actions":["swipe"],"bounds":[0,363,1080,2265],"relation":"child","executable":true},`+
		`{"ref":"n57","role":"scroll","actions":["swipe"],"bounds":[0,363,1080,2265],"relation":"child","executable":false}],`+
		`"blocking_refs":["n57","n58"]}},`+
		`"recovery":{"entry":"retry","reference":"O","input_state":"effective"}}`)
	// The static taxonomy carries empty action arrays, candidate maps and
	// bounded blocking refs; all three result modes must preserve them.
	add("act", "unsupported_empty_actions", `{"operation_id":"P16","operation_state":"finished","outcome":"not_executed",`+
		`"error":{"code":"unsupported_node_action","message":"目标节点不含点击、长按或文本输入能力","details":`+
		`{"reason":"target_missing_action","target_actions":[]}},`+
		`"recovery":{"entry":"retry","reference":"O","input_state":"effective"}}`)
	add("act", "no_safe_point_details", `{"operation_id":"P17","operation_state":"finished","outcome":"not_executed",`+
		`"error":{"code":"no_safe_point","message":"未能证明点击安全点","details":`+
		`{"reason":"mixed","scope":"target","scope_ref":"n2",`+
		`"candidates":[{"ref":"n3","role":"button","actions":["tap"],"bounds":[10,20,30,40],"relation":"overlapping","executable":true}],`+
		`"blocking_refs":["n4","n5"]}},`+
		`"recovery":{"entry":"retry","reference":"O","input_state":"effective"}}`)
	// A rejected revalidation names the nearest changed public ancestor in
	// error.details.changed_ref; the controlled diagnostic must keep it.
	add("act", "lineage_changed", `{"operation_id":"P19","operation_state":"finished","outcome":"not_executed",`+
		`"error":{"code":"target_changed","message":"目标祖先已变化","details":`+
		`{"reason":"lineage_changed","scope":"target","scope_ref":"n2","changed_ref":"n820"}},`+
		`"recovery":{"entry":"retry","reference":"O","input_state":"effective"}}`)
	add("act", "scroll_effect_quality", `{"operation_id":"P1","outcome":"executed","phase":"completed","effect":{"state":"observed"},"target_reference":"O1","observation":{"status":"complete","id":"O2","origin":"P1","revision":2,"quality":{"state":"unstable","samples":5,"stop_reason":"max_samples"},"tree":`+testFullTreeJSON+`}}`)
	add("act", "scroll_effect_unknown", `{"operation_id":"P1","outcome":"uncertain","effect":{"state":"unknown"},"observation":{"status":"complete","id":"O3","quality":{"state":"unknown","samples":0,"stop_reason":"not_sampled"}},"error":{"code":"action_unknown","message":"动作结果不确定"}}`)
	add("act", "error_image_v3_tree", `{"operation_id":"P1","outcome":"uncertain","observation":{"status":"complete","id":"O2","tree":`+v3RoleTree+`},"error":{"code":"action_unknown","message":"动作结果不确定"}}`)
	add("act", "text_input_submit_deferred", `{"operation_id":"P1","outcome":"uncertain","text_input":{"write":"executed","submit":"not_executed"}}`)
	add("act", "text_input_write_uncertain", `{"operation_id":"P1","outcome":"uncertain","text_input":{"write":"uncertain","submit":"not_executed"}}`)
	add("act", "text_input_not_dispatched", `{"operation_id":"P1","outcome":"uncertain","text_input":{"write":"not_executed","submit":"not_executed"}}`)
	add("act", "text_input_submit_uncertain", `{"operation_id":"P1","outcome":"uncertain","text_input":{"write":"executed","submit":"uncertain"}}`)
	// A partial text act confirms the write and never dispatched the submit: the
	// closed error code, the fixed write/submit snapshot, a fresh ready O and
	// the durable observe recovery coexist without any unknown dispatch.
	add("act", "text_submit_partial", `{"operation_id":"P18","outcome":"partial",`+
		`"text_input":{"write":"executed","submit":"not_executed"},`+
		`"error":{"code":"text_submit_not_executed","message":"写入已确认，提交未派发","details":`+
		`{"stage":"submit","reason":"submit_target_unconfirmed","candidate_count":0,`+
		`"dispatch":{"focus":"not_attempted","write":"executed","submit":"not_attempted"}}},`+
		`"observation":{"status":"complete","id":"O2","quality":{"state":"unknown","samples":0,"stop_reason":"not_sampled"}},`+
		`"recovery":{"entry":"observe","reference":"P","input_state":"retired"}}`)
	fixtures = append(fixtures, resultModeFixture{tool: "act", name: "executed_cancelled", raw: `{"outcome":"executed"}`, cancelled: true})
	// The inspection tree scope carries the detailed view inside the nested
	// complete tree; subtree and query declare it on the envelope.
	inspectionEnvelope := `"view":"detailed",`
	for _, scope := range []struct{ name, fields string }{
		{"tree", `"tree":` + testDetailedTreeJSON},
		{"subtree", inspectionEnvelope + `"root":{"ref":"n1","role":"root","bounds":null}`},
		{"query", inspectionEnvelope + `"matches":[{"node":{"ref":"n2","role":"text","bounds":null,"label":["a"]},"path":["n1"]}],"total":2,"truncated":true`},
		{"query", inspectionEnvelope + `"matches":[],"total":0,"truncated":false`},
	} {
		add("inspect", scope.name, `{"inspection":{"observation_id":"O1","origin":"P1","revision":1,"captured_at":"now","scope":"`+scope.name+`",`+scope.fields+`}}`)
	}
	for _, code := range []string{"inspection_too_large", "content_unavailable"} {
		add("inspect", code, `{"error":{"code":"`+code+`","message":"不可用"}}`)
	}
	for _, state := range []string{"active", "ended", "expired"} {
		add("keepalive", state, `{"outcome":"executed","session":{"origin":"P1","state":"`+state+`","mode":"virtual"}}`)
	}
	add("keepalive", "no_session", `{"error":{"code":"no_session","message":"无会话"}}`)
	for _, state := range []string{"pending", "unconfirmed", "complete"} {
		add("end", state, `{"outcome":"executed","cleanup":{"state":"`+state+`","execution":"confirmed","tasks":"pending","recents":"pending","display":"unconfirmed"}}`)
	}
	for _, items := range []string{`[]`, `[{"name":"设置","package":"com.android.settings"}]`} {
		add("app", "find_items", `{"operation_id":"P4","app":{"kind":"find","items":`+items+`,"total":2,"truncated":true,"captured_at":"now"}}`)
	}
	add("app", "find_error", `{"error":{"code":"app_unavailable","message":"不可用"}}`)
	add("app", "list", `{"operation_id":"P5","app":{"kind":"list","display_id":12,"items":[{"task_id":42,"package":"p","ownership":"owned"}],"total":1,"truncated":false,"captured_at":"now","foreground":{"state":"present","package":"p","task_id":42}}}`)
	add("app", "open", `{"operation_id":"P6","outcome":"executed","app":{"kind":"open","package":"p","task_id":42,"display_id":12,"ownership":"owned","reused":true,"state_preserved":false}}`)
	add("app", "claim", `{"operation_id":"P7","outcome":"executed","app":{"kind":"claim","package":"p","task_id":42,"source_display_id":0,"display_id":12,"ownership":"borrowed","state_preserved":true}}`)
	add("app", "handoff", `{"operation_id":"P8","outcome":"executed","app":{"kind":"handoff","package":"p","task_id":42,"source_display_id":12,"display_id":0,"ownership":"shared","state_preserved":null,"foreground_restore":"not_requested"}}`)
	add("app", "close_partial", `{"operation_id":"P9","outcome":"uncertain","app":{"kind":"close","display_id":12,"tasks":[{"task_id":42,"outcome":"executed","recents_cleaned":true},{"task_id":43,"outcome":"uncertain","recents_cleaned":null}],"complete":false},"error":{"code":"task_remove_unconfirmed","message":"部分任务的关闭结果未确认"}}`)
	add("app", "kill", `{"operation_id":"P10","outcome":"executed","app":{"kind":"kill","package":"p","source_task_id":42,"display_id":12,"target_pids":[1234,1235],"killed_pids":[1234,1235],"remaining_pids":[],"restarted":null}}`)
	for _, state := range []string{"queued", "unavailable"} {
		add("notify", state, `{"outcome":"executed","message_id":"M1","saved":true,"notification":{"state":"`+state+`"}}`)
	}
	for _, termination := range []string{"exited", "timeout", "cancelled", "not_started", "unknown"} {
		add("shell", termination, `{"outcome":"executed","shell":{"termination":"`+termination+`","exit_code":null,"output":null,"truncated":null,"output_error":{"code":"output_unavailable","message":"输出不可用"}}}`)
	}
	add("shell", "nonzero", `{"outcome":"executed","shell":{"termination":"exited","exit_code":1,"output":"a\r\nb\n","truncated":true}}`)
	for _, coverage := range []string{"own", "reference", "record_unavailable"} {
		add("diagnose", coverage, `{"diagnosis":{"summary":"诊断","issues":[{"phase":"p","code":"c"}],"timing_ms":{"p":9007199254740993},"timeline":[{"at":"now","phase":"p","code":"c"}],"coverage":"`+coverage+`","truncated":true}}`)
	}
	for _, tool := range toolCatalog {
		add(tool.name, "admission", `{"error":{"code":"invalid_request","message":"参数错误"},"recovery":{"entry":"retry","reference":"none"}}`)
	}
	return fixtures
}

// TestNestedErrorDetailsModeParity pins the same nested guidance across all
// three result modes. Text v2 must decode back to the canonical value,
// structuredContent must be the canonical value, and the structured diagnostic
// must retain the complete error.details object. The source Result is immutable.
func TestNestedErrorDetailsModeParity(t *testing.T) {
	source := &protocol.Result{
		OperationID:    "P15",
		OperationState: "finished",
		Outcome:        "not_executed",
		Error: &protocol.Error{
			Code:    protocol.CodeNoSafeTrajectory,
			Message: "无法生成安全滚动轨迹",
			Details: rmfNoSafeTrajectoryDetails(),
		},
	}
	before := rmfResultBytes(t, source)
	e := &execution{result: source}
	canonical, err := canonicalResult(e)
	if err != nil {
		t.Fatalf("canonicalResult: %v", err)
	}
	for _, mode := range []ResultMode{ResultModeBoth, ResultModeText, ResultModeStructured} {
		out := projectExecution(e, "act", mode)
		if !out.IsError {
			t.Fatalf("mode %v: nested error was not marked as an error", mode)
		}
		if mode == ResultModeText && out.StructuredContent != nil {
			t.Fatalf("mode %v: text mode carried structured content", mode)
		}
		if mode != ResultModeText {
			if got := rmfStructuredBytes(t, out.StructuredContent); !bytes.Equal(got, canonical) {
				t.Fatalf("mode %v: structuredContent differs from canonical\nwant %s\n got %s", mode, canonical, got)
			}
		}
		text := testTextContent(t, out)
		if mode == ResultModeStructured {
			requireTestContains(t, text, "diagnostic v1 is_error=true")
			requireTestContains(t, text, "error.code=no_safe_trajectory")
			requireTestContains(t, text, `"candidates":[`)
			requireTestContains(t, text, `"blocking_refs":["n57","n58"]`)
			requireTestContains(t, text, `"executable":true`)
			continue
		}
		wantText := expectedResultV4(t, canonical, true)
		decoded, flag, err := decodeResultText(t, text)
		if err != nil || !flag || !sameJSONValue(wantText, decoded) {
			t.Fatalf("mode %v text parity error=%v flag=%t\nwant=%#v\ngot=%#v\n%s",
				mode, err, flag, wantText, decoded, text)
		}
		requireTestContains(t, text, "no_safe_trajectory")
		requireTestContains(t, text, `"candidates":[`)
		requireTestContains(t, text, `"blocking_refs":["n57","n58"]`)
		requireTestContains(t, text, `"executable":true`)
	}
	if got := rmfResultBytes(t, source); !bytes.Equal(got, before) {
		t.Fatalf("mode projection mutated the source result\nbefore: %s\n after: %s", before, got)
	}
}

func TestStructuredMinimalErrorText(t *testing.T) {
	for _, tool := range []string{"observe", "act", "inspect", "app", "status"} {
		out := projectExecution(&execution{result: &protocol.Result{OperationID: "P1", OperationState: "request_closed", Observation: &protocol.Observation{Status: "failed"}}}, tool, ResultModeStructured)
		if out.IsError {
			text := testTextContent(t, out)
			requireTestContains(t, text, "operation_id=P1")
			requireTestContains(t, text, "observation.status=failed")
			requireTestContains(t, text, "diagnostic v1 is_error=true\n")
			requireTestContains(t, text, "\nend diagnostic\n")
		}
	}
	out := projectExecution(&execution{result: &protocol.Result{CanStart: new(true)}}, "status", ResultModeStructured)
	if out.IsError || len(out.Content) != 0 || out.Content == nil {
		t.Fatal("structured success content must be []")
	}
	encoded, err := json.Marshal(out)
	if err != nil || !strings.Contains(string(encoded), `"content":[]`) {
		t.Fatalf("wire content: %s %v", encoded, err)
	}
}

func TestAdmissionParity(t *testing.T) {
	for _, mode := range []ResultMode{ResultModeBoth, ResultModeText, ResultModeStructured} {
		for _, args := range []string{`{}`, `{"reference":"O1","X":1}`, `{"reference":null}`, `{"reference":1}`} {
			fake := newTestDeviceService(t)
			a := &adapter{client: newTestDeviceClient(t, fake)}
			out := a.handle(context.Background(), testDefinitionByName(t, "observe"), []byte(args), mode)
			if !out.IsError || fake.totalRequests() != 0 {
				t.Fatal("admission invoked service")
			}
			if mode == ResultModeStructured {
				requireTestContains(t, testTextContent(t, out), "error.code=invalid_request")
			} else {
				requireTestContains(t, testTextContent(t, out), "error invalid_request")
			}
		}
	}
}

func TestNoContentInLogs(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("app", func(w http.ResponseWriter, r *http.Request, body string) {
		writeTestJSON(w, 200, `{"operation_id":"P1","app":{"kind":"find","items":[{"name":"sensitive-app-name","package":"p"}],"total":1,"truncated":false,"captured_at":"now"}}`)
	})
	var buffer bytes.Buffer
	a := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.NewJSONHandler(&buffer, nil))}
	for _, mode := range []ResultMode{ResultModeBoth, ResultModeText, ResultModeStructured} {
		a.handle(context.Background(), testDefinitionByName(t, "app"), []byte(`{"kind":"find"}`), mode)
	}
	for _, line := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil {
			t.Fatal("log json")
		}
		for key := range record {
			switch key {
			case "time", "level", "msg", "tool", "duration_ms", "is_error", "code", "result_mode":
			default:
				t.Fatalf("unapproved log key %s", key)
			}
		}
	}
	if strings.Contains(buffer.String(), "sensitive-app-name") {
		t.Fatal("content in log")
	}
}

type resultModeTestTransport struct{ mode string }

func (rt resultModeTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer mode-test-token")
	if rt.mode != "" {
		clone.Header.Set(ResultModeHeader, rt.mode)
	}
	return http.DefaultTransport.RoundTrip(clone)
}

func TestHTTPModesSelectProjectionPerCall(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("status", func(w http.ResponseWriter, r *http.Request, body string) {
		if len(r.Header.Values(ResultModeHeader)) != 0 {
			t.Error("adapter mode forwarded to device service")
		}
		writeTestJSON(w, 200, `{"operation_state":"not_seen","can_start":true}`)
	})
	server, err := NewServer(Options{Client: newTestDeviceClient(t, fake), DefaultResultMode: ResultModeText})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := httptest.NewServer(NewHTTPHandler(server, HTTPConfig{Token: "mode-test-token"}))
	defer endpoint.Close()
	for _, mode := range []string{"both", "text", "structured", "", "both"} {
		session := connectTestStreamable(t, endpoint.URL, resultModeTestTransport{mode: mode})
		out := callTestTool(t, session, "status", map[string]any{})
		if out.IsError {
			t.Fatal("status query failed")
		}
		if mode == "text" || mode == "" {
			if out.StructuredContent != nil {
				t.Fatal("per-call mode not selected")
			}
		} else if out.StructuredContent == nil {
			t.Fatal("structured missing")
		}
		if mode == "structured" {
			if len(out.Content) != 0 || out.Content == nil {
				t.Fatal("structured content must be []")
			}
		} else {
			decoded, flag, err := decodeResultText(t, testTextContent(t, out))
			if err != nil || flag || decoded["operation_state"] != "not_seen" {
				t.Fatal("text facts")
			}
		}
		_ = session.Close()
	}
}

func TestCatalogFingerprintBaseline(t *testing.T) {
	// The catalog has thirteen tools. This baseline covers the caller-oriented
	// descriptions after result v4/tree v5 local guidance was added to start,
	// observe, wait_for, act, app and inspect, including content-stability
	// guidance and omission of the two structured evidence objects. It also
	// covers app kill stating that a virtual-display foreground task is
	// terminated together with its task window container, while the same app
	// still having a task on the primary or another display refuses, the
	// act(text) auto_submit/submit_ref contract with outcome=partial, and the
	// four-tier scroll amount with scroll axis-fallback guidance. Contract 30
	// adds observation change and hint guidance to the act and snap descriptions.
	const baseline = "25a09a66f54e728b1c9fc67e6898514c6ac7438b9976528ece7e36bd4f5eab43"
	if got := CatalogFingerprint(); got != baseline {
		t.Fatalf("catalog changed: %s", got)
	}
	fake := newTestDeviceService(t)
	session := connectTestInMemory(t, newTestMCPServer(t, fake))
	ctx, cancel := testCallContext(t)
	defer cancel()
	catalog, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range catalog.Tools {
		if tool.OutputSchema != nil {
			t.Fatalf("outputSchema published for %s", tool.Name)
		}
	}
}

func TestAllToolResultFixturesParity(t *testing.T) {
	for _, fixture := range rmfResultFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			raw, err := canonicalResult(&execution{result: &fixture.result})
			if err != nil {
				t.Fatal(err)
			}
			assertResultTextParity(t, raw, false)
		})
	}
}

func TestCancellationWithSimultaneousServiceResult(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(fmt.Sprint("readOnly=", readOnly), func(t *testing.T) {
			fake := newTestDeviceService(t)
			fake.setHandler("end", testEndHandler())
			a := &adapter{client: newTestDeviceClient(t, fake)}
			for i := 0; i < 20; i++ {
				ctx, cancel := context.WithCancel(context.Background())
				call := &preparedCall{readOnly: readOnly, run: func(context.Context, string) (*client.Response, error) {
					cancel()
					return &client.Response{Result: &protocol.Result{OperationID: "P1", Outcome: "executed"}}, nil
				}}
				e := a.invoke(ctx, call, "P1")
				cancel()
				if !e.cancelled {
					t.Fatal("cancellation lost when service result was already available")
				}
				if e.result.Outcome != "executed" {
					t.Fatal("known execution fact changed")
				}
				if out := projectExecution(e, "act", ResultModeBoth); !out.IsError {
					t.Fatal("cancel override missing")
				}
			}
			want := 20
			if readOnly {
				want = 0
			}
			if got := fake.requestCount("end"); got != want {
				t.Fatalf("end count=%d want=%d", got, want)
			}
		})
	}
}

// TestHTTPTextErrorDetailsAcrossModes proves the text detail group survives the
// full HTTP → client → MCP structured/text path, including a nested dispatch
// object and an explicit candidate_count=0.
func TestHTTPTextErrorDetailsAcrossModes(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("act", func(w http.ResponseWriter, r *http.Request, body string) {
		writeTestJSON(w, http.StatusConflict,
			`{"operation_id":"P16","operation_state":"finished","outcome":"not_executed",`+
				`"error":{"code":"text_target_uncertain","message":"目标输入对象无法唯一确认","details":`+
				`{"stage":"write","reason":"no_unique_target","candidate_count":0,`+
				`"dispatch":{"focus":"not_attempted","write":"not_dispatched","submit":"not_attempted"}}},`+
				`"recovery":{"entry":"observe","reference":"P","input_state":"retired"}}`)
	})
	a := &adapter{client: newTestDeviceClient(t, fake)}
	execution := a.run(context.Background(), testDefinitionByName(t, "act"),
		[]byte(`{"reference":"O1","operation":{"type":"text","target":{"ref":"n1"},"text":"x","auto_submit":false}}`))
	if execution.result == nil || execution.result.Error == nil {
		t.Fatalf("HTTP text failure was not projected: %#v", execution)
	}
	details := execution.result.Error.Details
	if details["stage"] != "write" || details["reason"] != "no_unique_target" || details["candidate_count"] != float64(0) {
		t.Fatalf("client lost text details: %#v", details)
	}
	dispatch, ok := details["dispatch"].(map[string]any)
	if !ok || dispatch["focus"] != "not_attempted" || dispatch["write"] != "not_dispatched" ||
		dispatch["submit"] != "not_attempted" {
		t.Fatalf("client lost nested dispatch: %#v", details["dispatch"])
	}

	canonical, err := canonicalResult(execution)
	if err != nil {
		t.Fatalf("canonicalResult: %v", err)
	}
	want := expectedResultV4(t, canonical, true, execution.requestRef)
	for _, mode := range []ResultMode{ResultModeBoth, ResultModeText, ResultModeStructured} {
		out := projectExecution(execution, "act", mode)
		if !out.IsError {
			t.Fatalf("mode %v: text failure was not an error", mode)
		}
		if mode == ResultModeStructured {
			if got := rmfStructuredBytes(t, out.StructuredContent); !bytes.Equal(got, canonical) {
				t.Fatalf("mode %v: structuredContent differs from canonical", mode)
			}
			diagnostic := testTextContent(t, out)
			requireTestContains(t, diagnostic, `"candidate_count":0`)
			requireTestContains(t, diagnostic, `"dispatch":{"focus":"not_attempted","submit":"not_attempted","write":"not_dispatched"}`)
			continue
		}
		decoded, flag, err := decodeResultText(t, testTextContent(t, out))
		if err != nil || !flag || !sameJSONValue(want, decoded) {
			t.Fatalf("mode %v text parity error=%v flag=%t", mode, err, flag)
		}
	}
}
