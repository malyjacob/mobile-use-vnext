package android

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// pinnedAppListPath returns the checked-in helper artifact used by tests.
func pinnedAppListPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "third_party", "android-applist", "applist.dex")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("checked-in app list artifact is missing: %v", err)
	}
	return path
}

func TestAppListReadsPinnedHelper(t *testing.T) {
	d := newStubDevice(t, "applist")
	d.appListPath = pinnedAppListPath(t)
	d.remoteAppListPath = defaultRemoteAppListDex

	entries, err := d.AppList(context.Background())
	if err != nil {
		t.Fatalf("AppList: %v", err)
	}
	want := []device.AppEntry{
		{Name: "Example", Package: "com.example.two"},
		{Name: "示例", Package: "com.example.one"},
	}
	if len(entries) != len(want) {
		t.Fatalf("entries=%v", entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, entries[i], want[i])
		}
	}
}

func TestEnsureAppListRejectsWrongLocalArtifact(t *testing.T) {
	d := newStubDevice(t, "applist")
	path := filepath.Join(t.TempDir(), "applist.dex")
	if err := os.WriteFile(path, []byte("not the pinned dex"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.appListPath = path
	if err := d.EnsureAppList(context.Background()); err == nil {
		t.Fatal("expected a hash rejection")
	}
}

func TestEnsureAppListRequiresConfiguration(t *testing.T) {
	d := newStubDevice(t, "applist")
	d.appListPath = ""
	if err := d.EnsureAppList(context.Background()); err == nil {
		t.Fatal("expected a configuration rejection")
	}
}

func TestParseAppListValidation(t *testing.T) {
	label := base64.StdEncoding.EncodeToString([]byte("标签"))
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"empty", "", true},
		{"normal", "com.example\t" + label + "\n", true},
		{"duplicate collapsed", "com.example\t" + label + "\ncom.example\t" + label + "\n", true},
		{"missing tab", "com.example\n", false},
		{"bad package", "com..example\t" + label + "\n", false},
		{"bad base64", "com.example\t!!!\n", false},
		{"invalid utf8", "com.example\t" + base64.StdEncoding.EncodeToString([]byte{0xff}) + "\n", false},
		{"crlf", "com.example\t" + label + "\r\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := parseAppList([]byte(tc.in))
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected rejection, got %v", entries)
			}
			if tc.name == "duplicate collapsed" && len(entries) != 1 {
				t.Fatalf("duplicate was not collapsed: %v", entries)
			}
			if tc.name == "crlf" && (len(entries) != 1 || entries[0].name != "标签") {
				t.Fatalf("crlf label was not parsed: %v", entries)
			}
		})
	}
}

func TestParseAppListOrderIsFoldedLabelThenPackage(t *testing.T) {
	lines := []string{
		"com.z\t" + base64.StdEncoding.EncodeToString([]byte("beta")),
		"com.a\t" + base64.StdEncoding.EncodeToString([]byte("Alpha")),
		"com.b\t" + base64.StdEncoding.EncodeToString([]byte("Alpha")),
	}
	entries, err := parseAppList([]byte(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.pkg)
	}
	want := []string{"com.a", "com.b", "com.z"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order=%v want=%v", got, want)
		}
	}
}
