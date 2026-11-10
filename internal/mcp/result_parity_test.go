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
					decoded, flag, err := decodeResultText(t, testTextContent(t, out))
					if err != nil || !reflect.DeepEqual(want, decoded) || flag != out.IsError {
						t.Fatalf("text parity mode=%v err=%v\nwant=%#v\ngot=%#v\n%s", mode, err, want, decoded, testTextContent(t, out))
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

type resultModeFixture struct {
	tool, name, raw string
	cancelled       bool
}

func resultModeFixtures() []resultModeFixture {
	var fixtures []resultModeFixture
	add := func(tool, name, raw string) {
		fixtures = append(fixtures, resultModeFixture{tool: tool, name: name, raw: raw})
	}
	add("status", "not_seen", `{"operation_state":"not_seen","can_start":true,"session":null}`)
	add("status", "closed", `{"request_closed":true,"can_start":false,"blocking_operation_id":"P1","blocking_code":"cleanup_pending"}`)
	add("start", "full", `{"operation_id":"P1","outcome":"executed","session":`+testActiveSessionJSON+`,"observation":{"status":"complete","id":"O1","tree":`+testFullTreeJSON+`}}`)
	add("start", "first_observation_failed", `{"operation_id":"P1","outcome":"executed","observation":{"status":"failed","error":{"code":"hierarchy_unavailable","message":"读取失败"}}}`)
	for _, tree := range []struct{ name, raw string }{{"full", testFullTreeJSON}, {"delta", testDeltaTreeJSON}} {
		add("observe", tree.name, `{"observation":{"status":"complete","id":"O2","tree":`+tree.raw+`}}`)
	}
	add("observe", "missing", `{"observation":{"status":"partial","id":"O1","missing":["image"],"image":{"error":{"code":"image_unavailable","message":"缺图"}}}}`)
	add("observe", "read_only_image", `{"observation":{"status":"failed","image":{"read_only":true,"width":2,"height":3,"media_type":"image/jpeg","base64":"secret"}}}`)
	add("observe", "empty", `{}`)
	for _, outcome := range []string{"executed", "not_executed", "uncertain"} {
		add("act", outcome, `{"operation_id":"P1","outcome":"`+outcome+`","error":{"code":"target_changed","message":"目标变化"},"recovery":{"entry":"observe","reference":"P"}}`)
	}
	fixtures = append(fixtures, resultModeFixture{tool: "act", name: "executed_cancelled", raw: `{"outcome":"executed"}`, cancelled: true})
	for _, scope := range []struct{ name, fields string }{
		{"tree", `"tree":` + testFullTreeJSON},
		{"subtree", `"root":{"ref":"n1","role":"root","bounds":null}`},
		{"query", `"matches":[{"node":{"ref":"n2","role":"text","bounds":null,"label":["a"]},"path":["n1"]}],"total":2,"truncated":true`},
		{"query", `"matches":[],"total":0,"truncated":false`},
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
		add("apps", "items", `{"apps":{"items":`+items+`,"total":2,"truncated":true,"captured_at":"now"}}`)
	}
	add("apps", "error", `{"error":{"code":"apps_unavailable","message":"不可用"}}`)
	add("open_app", "reused", `{"outcome":"executed","app":{"package":"p","task_id":0,"display_id":0,"reused":true,"state_preserved":false}}`)
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

func TestStructuredMinimalErrorText(t *testing.T) {
	for _, tool := range []string{"observe", "act", "inspect", "apps", "status"} {
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
			requireTestContains(t, testTextContent(t, out), "error.code=invalid_request")
		}
	}
}

func TestNoContentInLogs(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("apps", func(w http.ResponseWriter, r *http.Request, body string) {
		writeTestJSON(w, 200, `{"apps":{"items":[{"name":"sensitive-app-name","package":"p"}],"total":1,"truncated":false,"captured_at":"now"}}`)
	})
	var buffer bytes.Buffer
	a := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.NewJSONHandler(&buffer, nil))}
	for _, mode := range []ResultMode{ResultModeBoth, ResultModeText, ResultModeStructured} {
		a.handle(context.Background(), testDefinitionByName(t, "apps"), []byte(`{}`), mode)
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

func TestCatalogFingerprintUnchanged(t *testing.T) {
	const baseline = "a42fc3247361d1dadadce49a28214053947cd8ecacc967f81ec756a81d7a325b"
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
