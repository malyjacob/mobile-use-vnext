package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// TestFinishUsesPhaseSpecificTextStatus proves a controlled tool error can keep
// the documented 422 static-text status even when the same wire code maps to
// 409 after claim. The result JSON is unchanged.
func TestFinishUsesPhaseSpecificTextStatus(t *testing.T) {
	recorder := httptest.NewRecorder()
	server := &Server{}
	result := &protocol.Result{
		OperationID: "Pstatic", Outcome: "not_executed",
		Error:      &protocol.Error{Code: protocol.CodeTextTargetUnknown, Message: "文字操作需要可确认的真实输入对象"},
		HTTPStatus: 422,
	}
	server.finish(recorder, result, nil)
	if recorder.Code != 422 {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	result.HTTPStatus = 0
	server.finish(recorder, result, nil)
	if recorder.Code != 409 {
		t.Fatalf("code-only status = %d, want 409", recorder.Code)
	}
}
