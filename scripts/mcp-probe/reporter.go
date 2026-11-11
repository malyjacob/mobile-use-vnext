package main

import (
	"fmt"
	"io"
	"strings"
)

// reporter prints one PASS or FAIL line per check and one final result line.
// Every value is reduced to a single token so a line can never carry a
// whitespace-separated secret, base64 data or tree text.
type reporter struct {
	out    io.Writer
	failed bool
}

func newReporter(out io.Writer) *reporter {
	return &reporter{out: out}
}

func (r *reporter) pass(name string, fields ...string) {
	r.write("PASS", name, "", fields)
}

func (r *reporter) fail(name, reason string, fields ...string) {
	r.failed = true
	r.write("FAIL", name, reason, fields)
}

// skip records an environment-dependent check that was deliberately not
// exercised. It is not a failure and it is not a PASS either: the operator can
// see that the fact was not verified in this run.
func (r *reporter) skip(name, reason string, fields ...string) {
	r.write("SKIP", name, reason, fields)
}

func (r *reporter) write(kind, name, reason string, fields []string) {
	line := kind + " " + token(name)
	if reason != "" {
		line += " reason=" + token(reason)
	}
	for _, raw := range fields {
		if raw == "" {
			continue
		}
		key, value, found := strings.Cut(raw, "=")
		if !found {
			line += " " + token(raw)
			continue
		}
		line += " " + token(key) + "=" + token(value)
	}
	fmt.Fprintln(r.out, line)
}

// finish prints the final result and returns the matching process exit code.
// BLOCKED takes precedence because it is only ever reported before device
// work, in place of the single admissibility check.
func (r *reporter) finish(blocked bool) int {
	switch {
	case blocked:
		fmt.Fprintln(r.out, "RESULT BLOCKED")
		return 2
	case r.failed:
		fmt.Fprintln(r.out, "RESULT FAIL")
		return 1
	default:
		fmt.Fprintln(r.out, "RESULT PASS")
		return 0
	}
}

// token keeps one value on one line and drops anything that does not belong to
// a controlled identifier, code or number.
func token(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if builder.Len() >= 96 {
			break
		}
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9':
			builder.WriteRune(character)
		case character == '-', character == '_', character == '.',
			character == '/', character == ':', character == ',',
			character == '+', character == '(':
			builder.WriteRune(character)
		default:
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "empty"
	}
	return builder.String()
}

// field renders one key=value pair; an absent value is reported explicitly.
func field(key, value string) string {
	if value == "" {
		value = "none"
	}
	return key + "=" + value
}
