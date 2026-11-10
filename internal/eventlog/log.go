// Package eventlog provides the bounded, whitelisted event sink. Business
// boundaries record facts once; the sink never carries user content, trees,
// commands, credentials or raw exception text.
package eventlog

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Level is the event severity.
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

func (l Level) String() string {
	switch l {
	case Debug:
		return "DEBUG"
	case Warn:
		return "WARN"
	case Error:
		return "ERROR"
	}
	return "INFO"
}

// Field is one whitelisted attribute.
type Field struct {
	Key   string
	Value any
}

// Str returns a string attribute.
func Str(key, value string) Field { return Field{Key: key, Value: value} }

// Int returns an integer attribute.
func Int(key string, value int) Field { return Field{Key: key, Value: value} }

// Int64 returns a 64-bit integer attribute.
func Int64(key string, value int64) Field { return Field{Key: key, Value: value} }

// Bool returns a boolean attribute.
func Bool(key string, value bool) Field { return Field{Key: key, Value: value} }

// Duration returns a millisecond attribute; the key must be whitelisted.
func Duration(key string, value time.Duration) Field {
	return Field{Key: key, Value: value.Milliseconds()}
}

// allowedKeys is the single field whitelist. Adding a key requires deciding
// whether it can carry user content; identifiers, enums, booleans, counts and
// durations are safe, free text is not.
var allowedKeys = map[string]struct{}{
	"agent": {}, "phase": {}, "outcome": {}, "code": {}, "reason": {},
	"operation_id": {}, "observation_id": {}, "origin": {}, "session": {},
	"service_instance": {}, "app_instance": {}, "display_id": {},
	"display_generation": {}, "command_id": {}, "revision": {}, "base": {},
	"kind": {}, "mode": {}, "state": {}, "action": {}, "role": {},
	"count": {}, "bytes": {}, "ms": {}, "limit": {}, "available": {},
	"connected": {}, "stop_ready": {}, "can_start": {}, "queued": {},
	"dropped": {}, "request_id": {}, "closure": {}, "cleanup": {},
	"delivery": {}, "attempt": {}, "generation": {}, "version": {},
	// Managed shell execution facts. They are enums, booleans and counters;
	// commands, output text and paths are never whitelisted.
	"root": {}, "termination": {}, "exit_code": {}, "truncated": {},
	// Settle facts. They are a frame count and a completion flag, never tree
	// content.
	"samples": {}, "stable": {},
	"execution": {}, "tasks": {}, "recents": {}, "display": {},
	// Coordinate point proof facts. The resolution state and completion flag
	// describe one pure-CPU decision; points, refs and labels are never
	// whitelisted.
	"proven": {},
}

// Logger is a bounded, drop-counting event sink.
type Logger struct {
	serviceInstance string
	out             io.Writer
	ch              chan string
	done            chan struct{}
	closeOnce       sync.Once
	lost            atomic.Int64
	closed          atomic.Bool
}

// New starts a logger writing to out. The queue is bounded; a slow sink drops
// events instead of blocking a STOP or device path.
func New(out io.Writer, serviceInstance string) *Logger {
	l := &Logger{
		serviceInstance: serviceInstance,
		out:             out,
		ch:              make(chan string, 1024),
		done:            make(chan struct{}),
	}
	go l.run()
	return l
}

func (l *Logger) run() {
	defer close(l.done)
	for line := range l.ch {
		if l.out != nil {
			_, _ = io.WriteString(l.out, line)
		}
	}
}

// Event records one event. It never blocks on the sink.
func (l *Logger) Event(level Level, name string, fields ...Field) {
	if l == nil || l.closed.Load() {
		return
	}
	var b strings.Builder
	b.WriteString(time.Now().UTC().Format(time.RFC3339Nano))
	b.WriteString(" ")
	b.WriteString(level.String())
	b.WriteString(" ")
	b.WriteString(sanitize(name))
	if l.serviceInstance != "" {
		b.WriteString(" service_instance=")
		b.WriteString(quote(l.serviceInstance))
	}
	for _, field := range fields {
		if _, ok := allowedKeys[field.Key]; !ok {
			continue
		}
		b.WriteString(" ")
		b.WriteString(field.Key)
		b.WriteString("=")
		b.WriteString(format(field.Value))
	}
	b.WriteString("\n")
	select {
	case l.ch <- b.String():
	default:
		l.lost.Add(1)
	}
}

// Close flushes accepted events and stops the writer.
func (l *Logger) Close() {
	if l == nil {
		return
	}
	l.closeOnce.Do(func() {
		l.closed.Store(true)
		close(l.ch)
		<-l.done
	})
}

// Lost returns the number of dropped events.
func (l *Logger) Lost() int64 {
	if l == nil {
		return 0
	}
	return l.lost.Load()
}

func format(v any) string {
	switch value := v.(type) {
	case string:
		return quote(value)
	case bool:
		if value {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", value)
	case int64:
		return fmt.Sprintf("%d", value)
	case time.Duration:
		return fmt.Sprintf("%d", value.Milliseconds())
	default:
		return "?"
	}
}

// quote keeps values single-line and bounded; whitelisted values are IDs and
// enums, so truncation is only a defensive bound.
func quote(value string) string {
	if len(value) > 128 {
		value = value[:128]
	}
	value = strings.ReplaceAll(value, "\n", "_")
	value = strings.ReplaceAll(value, "\r", "_")
	value = strings.ReplaceAll(value, `"`, "_")
	return `"` + value + `"`
}

func sanitize(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
