package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/client"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const snapTestImageBase64 = "77u/77u/77u/AAECAwQ="

func snapTestImageBytes(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(snapTestImageBase64)
	if err != nil {
		t.Fatalf("decode test image: %v", err)
	}
	return data
}

func snapSuccessBody(operationID string) string {
	return fmt.Sprintf(`{"operation_id":%q,"image":{"captured_at":"2026-11-18T00:00:00Z",`+
		`"width":540,"height":1200,"media_type":"image/jpeg","read_only":true,`+
		`"screen":{"width":1080,"height":2400,"rotation":0},"base64":%q}}`,
		operationID, snapTestImageBase64)
}

func TestSnapToolPublishesOnlyReadOnlyAnnotation(t *testing.T) {
	fake := newTestDeviceService(t)
	session := connectTestInMemory(t, newTestMCPServer(t, fake))
	ctx, cancel := testCallContext(t)
	defer cancel()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	found := false
	for _, tool := range list.Tools {
		if tool.Name == "snap" {
			found = true
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Fatalf("snap annotations = %+v", tool.Annotations)
			}
			continue
		}
		if tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
			t.Fatalf("%s publishes an unexpected read-only annotation", tool.Name)
		}
	}
	if !found {
		t.Fatal("snap is missing from tools/list")
	}
}

func TestSnapProjectsOneImageInEveryMode(t *testing.T) {
	for _, mode := range []ResultMode{ResultModeBoth, ResultModeText, ResultModeStructured} {
		t.Run(mode.String(), func(t *testing.T) {
			fake := newTestDeviceService(t)
			fake.setHandler("snap", func(writer http.ResponseWriter, request *http.Request, body string) {
				writeTestJSON(writer, http.StatusOK, snapSuccessBody(testOperationHeader(request)))
			})
			adapter := &adapter{
				client:            newTestDeviceClient(t, fake),
				logger:            slog.New(slog.DiscardHandler),
				defaultResultMode: mode,
			}
			execution := adapter.run(context.Background(), testDefinitionByName(t, "snap"),
				[]byte(`{"reference":"O1"}`))
			if execution.isError("snap") {
				t.Fatalf("snap projected an error: %+v", execution.result)
			}
			requests := fake.requestsFor("snap")
			if len(requests) != 1 {
				t.Fatalf("snap requests = %d, want 1", len(requests))
			}
			if requests[0].Body != `{"reference":"O1"}` {
				t.Fatalf("snap body = %q", requests[0].Body)
			}
			if requests[0].OperationID == "" || !strings.HasPrefix(requests[0].OperationID, "P") {
				t.Fatal("snap did not send a pre-allocated P")
			}
			if fake.requestCount("end") != 0 {
				t.Fatal("a successful snap issued an end")
			}
			if execution.image == nil || !bytes.Equal(execution.image.Data, snapTestImageBytes(t)) ||
				execution.image.MediaType != "image/jpeg" {
				t.Fatalf("adapter image = %+v", execution.image)
			}
			out := projectExecution(execution, "snap", mode)
			if out.IsError {
				t.Fatal("snap success was projected as an error")
			}
			images := testImageContents(out)
			if len(images) != 1 {
				t.Fatalf("ImageContent count = %d, want 1", len(images))
			}
			if images[0].MIMEType != "image/jpeg" ||
				!bytes.Equal(images[0].Data, snapTestImageBytes(t)) {
				t.Fatal("ImageContent does not carry the decoded JPEG")
			}
			if mode != ResultModeStructured {
				text := testTextContent(t, out)
				requireTestContains(t, text, "is_error=false")
				requireTestContains(t, text, `image={"captured_at":"2026-11-18T00:00:00Z"`)
				requireTestContains(t, text, `"width":540`)
				requireTestContains(t, text, `"height":1200`)
				requireTestContains(t, text, `"media_type":"image/jpeg"`)
				requireTestContains(t, text, `"read_only":true`)
				requireTestNotContains(t, text, snapTestImageBase64)
			}
			if mode != ResultModeText {
				structured := testStructuredObject(t, out)
				image, ok := structured["image"].(map[string]any)
				if !ok {
					t.Fatalf("structured image missing: %v", structured)
				}
				if image["captured_at"] != "2026-11-18T00:00:00Z" ||
					image["width"] != float64(540) || image["height"] != float64(1200) ||
					image["media_type"] != "image/jpeg" || image["read_only"] != true {
					t.Fatalf("structured image = %v", image)
				}
				if _, leaked := image["base64"]; leaked {
					t.Fatal("structured image retained the base64 carrier")
				}
			}
		})
	}
}

// TestSnapDuplicateSummaryIsNotSuccess pins the MCP success rule: a replay
// without an image is a normal controllable summary, but not a snap success.
func TestSnapDuplicateSummaryIsNotSuccess(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("snap", func(writer http.ResponseWriter, request *http.Request, body string) {
		writeTestJSON(writer, http.StatusOK,
			`{"operation_id":"P7","operation_state":"finished","phase":"finished"}`)
	})
	adapter := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.DiscardHandler)}
	execution := adapter.run(context.Background(), testDefinitionByName(t, "snap"),
		[]byte(`{"reference":"O1"}`))
	if !execution.isError("snap") {
		t.Fatal("a duplicate summary without an image counted as success")
	}
	if len(testImageContents(projectExecution(execution, "snap", ResultModeBoth))) != 0 {
		t.Fatal("a duplicate summary delivered an image")
	}
}

func TestSnapCancellationClearsImageAndSkipsEnd(t *testing.T) {
	fake := newTestDeviceService(t)
	fake.setHandler("snap", fake.blockTestUntilStopped)
	adapter := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.DiscardHandler)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultChannel := make(chan *execution, 1)
	go func() {
		resultChannel <- adapter.run(ctx, testDefinitionByName(t, "snap"),
			[]byte(`{"reference":"O1"}`))
	}()
	snapRequests := fake.waitFor(t, "snap", 1, mcpTestTimeout)
	cancel()
	var execution *execution
	select {
	case execution = <-resultChannel:
	case <-time.After(mcpTestTimeout):
		t.Fatal("cancelled snap did not return")
	}
	if fake.requestCount("end") != 0 {
		t.Fatal("cancelled snap issued an end")
	}
	if len(fake.requestsFor("snap")) != 1 {
		t.Fatal("cancelled snap was dispatched more than once")
	}
	if execution.result.OperationID != snapRequests[0].OperationID {
		t.Fatalf("operation_id = %q, want %q",
			execution.result.OperationID, snapRequests[0].OperationID)
	}
	if execution.result.Error == nil || execution.result.Error.Code != "call_cancelled" {
		t.Fatalf("cancellation error = %#v", execution.result.Error)
	}
	if execution.result.Image != nil || execution.image != nil {
		t.Fatal("cancelled snap retained image material")
	}
	out := projectExecution(execution, "snap", ResultModeBoth)
	if len(testImageContents(out)) != 0 {
		t.Fatal("cancelled snap published an ImageContent")
	}
	text := testTextContent(t, out)
	requireTestContains(t, text, "call_cancelled")
	requireTestContains(t, text, "不交付图片")
	requireTestNotContains(t, text, snapTestImageBase64)
}

// TestSnapCancellationDropsKnownCommitResult covers the race where the service
// already committed and returned an image before the adapter entered its
// cancellation branch: the image must still be discarded.
func TestSnapCancellationDropsKnownCommitResult(t *testing.T) {
	fake := newTestDeviceService(t)
	adapter := &adapter{client: newTestDeviceClient(t, fake), logger: slog.New(slog.DiscardHandler)}
	call := &preparedCall{snap: true}
	known := callOutcome{response: &client.Response{
		Result: &protocol.Result{
			OperationID: "P7",
			Image: &protocol.ImageMeta{
				CapturedAt: "2026-11-18T00:00:00Z", Width: 540, Height: 1200,
				MediaType: "image/jpeg", ReadOnly: true,
			},
		},
		Image: &client.Image{Data: []byte{1, 2, 3}, MediaType: "image/jpeg"},
	}}
	execution := adapter.cancelled(call, "P7", nil, known, true)
	if fake.requestCount("end") != 0 {
		t.Fatal("cancelled snap issued an end")
	}
	if execution.image != nil || execution.result.Image != nil {
		t.Fatal("cancelled snap kept the committed image")
	}
	if execution.result.Error == nil || execution.result.Error.Code != "call_cancelled" {
		t.Fatalf("cancellation error = %#v", execution.result.Error)
	}
	out := projectExecution(execution, "snap", ResultModeBoth)
	if len(testImageContents(out)) != 0 {
		t.Fatal("cancelled snap published an ImageContent")
	}
}

// TestSnapStructuredSchemaIsClosed pins the one admitted member and the read
// path of the deployed tool definition.
func TestSnapStructuredSchemaIsClosed(t *testing.T) {
	definition := testDefinitionByName(t, "snap")
	var schema struct {
		Type                 string                     `json:"type"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(definition.schema, &schema); err != nil {
		t.Fatalf("snap schema: %v", err)
	}
	if schema.Type != "object" || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatalf("snap schema shape = %+v", schema)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "reference" {
		t.Fatalf("snap required = %v", schema.Required)
	}
	if len(schema.Properties) != 1 || schema.Properties["reference"] == nil {
		t.Fatalf("snap properties = %v", schema.Properties)
	}
	if !definition.snap || definition.readOnly {
		t.Fatalf("snap definition = snap=%v readOnly=%v", definition.snap, definition.readOnly)
	}
}
