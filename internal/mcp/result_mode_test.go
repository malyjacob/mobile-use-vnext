package mcp

import (
	"net/http"
	"testing"
)

func TestResultModeParse(t *testing.T) {
	t.Parallel()

	valid := []struct {
		value string
		want  ResultMode
	}{
		{value: "both", want: ResultModeBoth},
		{value: "text", want: ResultModeText},
		{value: "structured", want: ResultModeStructured},
	}
	for _, test := range valid {
		test := test
		t.Run("valid/"+test.value, func(t *testing.T) {
			t.Parallel()
			got, err := ParseResultMode(test.value)
			if err != nil {
				t.Fatalf("ParseResultMode(%q): %v", test.value, err)
			}
			if got != test.want {
				t.Fatalf("ParseResultMode(%q) = %v, want %v", test.value, got, test.want)
			}
			if got.String() != test.value {
				t.Fatalf("ResultMode(%d).String() = %q, want %q", got, got.String(), test.value)
			}
		})
	}

	for _, value := range []string{"", "Both", "TEXT", "structured ", " text", "legacy", "both,text"} {
		value := value
		t.Run("invalid/"+value, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseResultMode(value); err == nil {
				t.Fatalf("ParseResultMode(%q) unexpectedly succeeded", value)
			}
		})
	}
}

func TestResultModeFromHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		values   []string
		fallback ResultMode
		want     ResultMode
		wantErr  bool
	}{
		{name: "absent uses default", fallback: ResultModeStructured, want: ResultModeStructured},
		{name: "both", values: []string{"both"}, fallback: ResultModeStructured, want: ResultModeBoth},
		{name: "text", values: []string{"text"}, fallback: ResultModeBoth, want: ResultModeText},
		{name: "structured", values: []string{"structured"}, fallback: ResultModeBoth, want: ResultModeStructured},
		{name: "empty", values: []string{""}, wantErr: true},
		{name: "multiple", values: []string{"text", "structured"}, wantErr: true},
		{name: "comma joined", values: []string{"text,structured"}, wantErr: true},
		{name: "leading whitespace", values: []string{" text"}, wantErr: true},
		{name: "trailing whitespace", values: []string{"text "}, wantErr: true},
		{name: "internal whitespace", values: []string{"te xt"}, wantErr: true},
		{name: "wrong case", values: []string{"Text"}, wantErr: true},
		{name: "invalid default", fallback: ResultMode(99), wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			header := make(http.Header)
			for _, value := range test.values {
				header.Add(ResultModeHeader, value)
			}
			got, err := ResultModeFromHeader(header, test.fallback)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ResultModeFromHeader() = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResultModeFromHeader(): %v", err)
			}
			if got != test.want {
				t.Fatalf("ResultModeFromHeader() = %v, want %v", got, test.want)
			}
		})
	}
}
