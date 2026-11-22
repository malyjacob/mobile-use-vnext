package eventlog

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestSubmitDispatchAndContinuityKeysAreWhitelisted proves the renamed text
// submission dispatch fact and the two new continuity facts survive the single
// field whitelist, while the retired enter_dispatch key is dropped.
func TestSubmitDispatchAndContinuityKeysAreWhitelisted(t *testing.T) {
	var out bytes.Buffer
	logger := New(&out, "svc")
	logger.Event(Info, "engine.text.finished",
		Str("focus_dispatch", "dispatched"),
		Str("write_dispatch", "dispatched"),
		Str("submit_dispatch", "not_attempted"),
		Str("outcome", "partial"),
		Str("stage", "submit"),
		Str("reason", "submit_target_unconfirmed"),
		Int("count", 0),
		Int("tier", 4),
		Str("baseline_pool", "historical"),
	)
	logger.Event(Info, "engine.text.finished", Str("enter_dispatch", "dispatched"))
	logger.Close()

	recorded := out.String()
	for _, want := range []string{
		`submit_dispatch="not_attempted"`,
		`focus_dispatch="dispatched"`,
		`write_dispatch="dispatched"`,
		`outcome="partial"`,
		`stage="submit"`,
		`reason="submit_target_unconfirmed"`,
		`count=0`,
		`tier=4`,
		`baseline_pool="historical"`,
		`service_instance="svc"`,
	} {
		if !strings.Contains(recorded, want) {
			t.Fatalf("recorded events missing %s:\n%s", want, recorded)
		}
	}
	if strings.Contains(recorded, "enter_dispatch") {
		t.Fatalf("retired enter_dispatch key survived the whitelist:\n%s", recorded)
	}
}

// TestWhitelistDropsUncontrolledContentKeys is the reverse control of the
// single field whitelist: no ref, changed_ref, input text, command, output or
// tree key can ever reach a log line even when a caller passes one.
func TestWhitelistDropsUncontrolledContentKeys(t *testing.T) {
	forbidden := []string{
		"changed_ref", "ref", "scope_ref", "ancestor_ref", "blocking_refs",
		"text", "value", "readback", "command", "output", "tree", "delta",
		"message", "detail", "error_text", "candidates", "user",
	}
	var out bytes.Buffer
	logger := New(&out, "")
	fields := make([]Field, 0, len(forbidden))
	for _, key := range forbidden {
		fields = append(fields, Str(key, "secret-value"))
	}
	logger.Event(Info, "act.target_changed", fields...)
	logger.Close()

	recorded := out.String()
	for _, key := range forbidden {
		if strings.Contains(recorded, key+"=") {
			t.Fatalf("forbidden key %q reached the log:\n%s", key, recorded)
		}
	}
	if strings.Contains(recorded, "secret-value") {
		t.Fatalf("free-text value reached the log:\n%s", recorded)
	}
}

// TestFieldFormattingBoxesValuesIntoOneLine proves values stay single-line,
// bounded and type-stable, so a hostile or accidental value cannot forge a
// second event line.
func TestFieldFormattingBoxesValuesIntoOneLine(t *testing.T) {
	var out bytes.Buffer
	logger := New(&out, "")
	logger.Event(Warn, "engine.capture", Bool("proven", true), Int("coverage", 42),
		Duration("ms", 1500*time.Millisecond), Str("phase", "a\nb\r\"c"),
		Str("reason", strings.Repeat("x", 400)), Field{Key: "state", Value: 1.5})
	logger.Close()

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("event line count = %d, want 1:\n%s", len(lines), out.String())
	}
	line := lines[0]
	for _, want := range []string{`proven=true`, `coverage=42`, `ms=1500`, `state=?`} {
		if !strings.Contains(line, want) {
			t.Fatalf("line missing %s:\n%s", want, line)
		}
	}
	if strings.Contains(line, "\n") || strings.Contains(line, "\r") {
		t.Fatalf("line kept a line break: %q", line)
	}
	if strings.Count(line, `"`) != 4 {
		t.Fatalf("quotes were not neutralized: %q", line)
	}
	if !strings.Contains(line, `reason="`+strings.Repeat("x", 128)+`"`) {
		t.Fatalf("value was not truncated to the defensive bound: %q", line)
	}
}

// TestEventNameSanitization proves an unvetted event name cannot inject
// separators or fields into the line.
func TestEventNameSanitization(t *testing.T) {
	var out bytes.Buffer
	logger := New(&out, "")
	logger.Event(Info, "engine.text finished=oops\nnext", Int("count", 1))
	logger.Close()
	line := strings.TrimSuffix(out.String(), "\n")
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("event name injected a line break: %q", line)
	}
	if !strings.Contains(line, "engine.text_finished_oops_next") {
		t.Fatalf("event name was not sanitized: %q", line)
	}
	if strings.Count(line, "count=1") != 1 {
		t.Fatalf("event name fabricated a field: %q", line)
	}
}

// TestBoundedQueueDropsInsteadOfBlocking proves the sink never blocks a STOP or
// device boundary when the writer is stuck.
func TestBoundedQueueDropsInsteadOfBlocking(t *testing.T) {
	gate := make(chan struct{})
	sink := &gatedWriter{gate: gate}
	logger := New(sink, "")
	for i := 0; i < 4096; i++ {
		logger.Event(Info, "engine.capture", Int("count", i))
	}
	if logger.Lost() == 0 {
		t.Fatal("a blocked writer did not drop any event; the queue is unbounded")
	}
	close(gate)
	logger.Close()
}

type gatedWriter struct {
	gate chan struct{}
	buf  bytes.Buffer
}

func (w *gatedWriter) Write(p []byte) (int, error) {
	<-w.gate
	return w.buf.Write(p)
}

// TestNilLoggerIsSafe proves the zero logger is usable on teardown paths.
func TestNilLoggerIsSafe(t *testing.T) {
	var logger *Logger
	logger.Event(Error, "engine.stop", Str("reason", "client_end"))
	logger.Close()
	if got := logger.Lost(); got != 0 {
		t.Fatalf("nil logger lost = %d", got)
	}
}
