package android

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// These tests run the real os/exec transport against the stubbed adb process so
// argv shape, exit handling, parsing and the read-only paths are exercised
// without a device.

func newStubDevice(t *testing.T, transcript string) *Device {
	t.Helper()
	d, err := New(Config{
		AdbPath: fakeADB(t, transcript),
		Serial:  "127.0.0.1:5555",
		Timeout: 10 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.testEnv = envFor(transcript)
	return d
}

// envFor returns the stub sentinel for one transcript.
func envFor(transcript string) []string {
	return []string{fakeSentinelEnv + "=" + fakeDigestOf(transcript)}
}

func TestPrepareAndIdentity(t *testing.T) {
	d := newStubDevice(t, "probe")
	ctx := t.Context()
	if err := d.Prepare(ctx); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	model, version, sdk, width, height, density, err := d.Identity(ctx)
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if model != "stub-model" || version != "16" || sdk != 36 {
		t.Fatalf("identity=%q %q %d", model, version, sdk)
	}
	if width != 1080 || height != 2400 || density != 450 {
		t.Fatalf("metrics=%dx%d @%d", width, height, density)
	}
}

func TestProbeDisplayPrimaryAndVirtual(t *testing.T) {
	d := newStubDevice(t, "probe")
	ctx := t.Context()
	primary, err := d.ProbeDisplay(ctx, 0)
	if err != nil {
		t.Fatalf("ProbeDisplay(0): %v", err)
	}
	if !primary.Exists || primary.SFID != 4630946540531149187 {
		t.Fatalf("primary=%+v", primary)
	}
	virtual, err := d.ProbeDisplay(ctx, 61)
	if err != nil {
		t.Fatalf("ProbeDisplay(61): %v", err)
	}
	if !virtual.Exists || virtual.SFID != 1152921504722368738 {
		t.Fatalf("virtual=%+v", virtual)
	}
	// A display with no SurfaceFlinger layer never falls back to id 0.
	absent, err := d.ProbeDisplay(ctx, 62)
	if err != nil {
		t.Fatalf("ProbeDisplay(62): %v", err)
	}
	if absent.Exists || absent.SFID != 0 {
		t.Fatalf("absent=%+v", absent)
	}
}

func TestCaptureIsReadOnlyAndFiltered(t *testing.T) {
	d := newStubDevice(t, "capture")
	// The hierarchy source is served by an in-process RPC endpoint.
	server := newCaptureServer(t, captureXML)
	defer server.Close()
	d.testBase = server.URL

	display := device.Display{ID: 0, SFID: 4630946540531149187, Width: 1080, Height: 2400, Density: 450, Generation: "g1"}
	capture, err := d.Capture(t.Context(), display)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(capture.Nodes) != 2 {
		t.Fatalf("nodes=%d, want 2 (the display 61 node is filtered)", len(capture.Nodes))
	}
	field := capture.Nodes[1]
	if field.Text != "Query" || !field.Editable || !field.Focused || !field.Visible {
		t.Fatalf("field=%+v", field)
	}
	if capture.Root != 0 || capture.Display.Generation != "g1" {
		t.Fatalf("capture=%+v", capture)
	}
	if capture.Foreground.State != device.ForegroundPresent || capture.Foreground.Package != "com.example.home" {
		t.Fatalf("foreground=%+v", capture.Foreground)
	}
	if capture.Foreground.TaskID == nil || *capture.Foreground.TaskID != 2 {
		t.Fatalf("task id=%v", capture.Foreground.TaskID)
	}
	if capture.CapturedAt.IsZero() {
		t.Fatal("CapturedAt missing")
	}

	// The control package on a virtual display fails the isolation invariant;
	// the capture is rejected instead of filtering the control nodes out.
	server.SetXML(`<hierarchy rotation="0"><node class="android.widget.Button" package="` +
		d.controlPackage + `" display-id="61" visible-to-user="true" bounds="[0,0][10,10]" /></hierarchy>`)
	if _, err := d.Capture(t.Context(), device.Display{ID: 61, Width: 1080, Height: 2400}); CodeOf(err) != CodeDisplayAbsent {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
}

func TestTaskListUsesCurrentTasks(t *testing.T) {
	d := newStubDevice(t, "capture")
	tasks, err := d.TaskList(t.Context())
	if err != nil {
		t.Fatalf("TaskList: %v", err)
	}
	if len(tasks) != 1 || tasks[0].TaskID != 42 || tasks[0].DisplayID != 0 || !tasks[0].Visible {
		t.Fatalf("tasks=%+v", tasks)
	}
}

func TestScreenshotReturnsPNG(t *testing.T) {
	d := newStubDevice(t, "screenshot")
	raw, err := d.Screenshot(t.Context(), 4630946540531149187)
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if !strings.HasPrefix(string(raw), "\x89PNG\r\n\x1a\n") {
		t.Fatalf("not a PNG: %q", raw)
	}
}

func TestInputArgvOnTheWire(t *testing.T) {
	cases := []struct {
		command string
		argv    string
	}{
		{"tap", "input -d 61 tap 100 200"},
		{"long_press", "input -d 61 swipe 100 200 100 200 700"},
		{"swipe", "input -d 61 swipe 10 20 30 40 300"},
		{"key", "input -d 61 keyevent BACK"},
	}
	for _, testCase := range cases {
		d := newStubDevice(t, "input")
		record := filepath.Join(t.TempDir(), "argv.txt")
		d.testEnv = append(d.testEnv, "MOBILE_USE_FAKE_ADB_ARGV="+record)
		ctx := t.Context()
		var err error
		switch testCase.command {
		case "tap":
			err = d.Tap(ctx, 61, 100, 200)
		case "long_press":
			err = d.LongPress(ctx, 61, 100, 200)
		case "swipe":
			err = d.Swipe(ctx, 61, 10, 20, 30, 40)
		case "key":
			err = d.Key(ctx, 61, "BACK")
		}
		if err != nil {
			t.Fatalf("%s: %v", testCase.command, err)
		}
		raw, readErr := os.ReadFile(record)
		if readErr != nil {
			t.Fatalf("%s: argv: %v", testCase.command, readErr)
		}
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		want := []string{"-s", "127.0.0.1:5555", "shell", testCase.argv}
		if strings.Join(lines, "|") != strings.Join(want, "|") {
			t.Fatalf("%s: argv=%q want %q", testCase.command, lines, want)
		}
	}
}

func TestShellPassesOneCommandArgument(t *testing.T) {
	d := newStubDevice(t, "argv-record")
	record := filepath.Join(t.TempDir(), "argv.txt")
	d.testEnv = append(d.testEnv, "MOBILE_USE_FAKE_ADB_ARGV="+record)
	if _, err := d.Shell(context.Background(), "input -d 4 tap 1 2"); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("argv: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	want := []string{"-s", "127.0.0.1:5555", "shell", "input -d 4 tap 1 2"}
	if strings.Join(lines, " ") != strings.Join(want, " ") {
		t.Fatalf("argv=%q want %q", lines, want)
	}
}

func TestShellReportsExitCode(t *testing.T) {
	d := newStubDevice(t, "exit3")
	result, err := d.Shell(t.Context(), "true")
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if result.ExitCode != 3 || strings.TrimSpace(string(result.Stdout)) != "out" {
		t.Fatalf("result=%+v", result)
	}
	if _, err := d.shellChecked(t.Context(), "true"); CodeOf(err) != CodeExitCode {
		t.Fatalf("checked code=%q", CodeOf(err))
	}
}

func TestShellTimeoutIsControlled(t *testing.T) {
	d := newStubDevice(t, "hang")
	ctx, cancel := context.WithTimeout(t.Context(), 700*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := d.shellWith(ctx, 700*time.Millisecond, "true")
	if CodeOf(err) != CodeCancelled {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("timeout not enforced: %v", elapsed)
	}
}

func TestShellMissingADBIsControlled(t *testing.T) {
	d, err := New(Config{AdbPath: filepath.Join(t.TempDir(), "nope"), Serial: "s"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := d.Shell(t.Context(), "true"); CodeOf(err) != CodeAvailability {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	name := "definitely-not-adb-xyz" + exeSuffix()
	missing, err := New(Config{AdbPath: name, Serial: "s"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := missing.Shell(t.Context(), "true"); CodeOf(err) != CodeAvailability {
		t.Fatalf("code=%q", CodeOf(err))
	}
}

func TestRemoteHashParsing(t *testing.T) {
	d := newStubDevice(t, "remote-hash")
	if got := d.remoteHash(t.Context()); got != U2JarSHA256 {
		t.Fatalf("remote hash=%q", got)
	}
	d.remoteJarPath = "/data/local/tmp/missing.jar"
	if got := d.remoteHash(t.Context()); got != "" {
		t.Fatalf("unexpected hash=%q", got)
	}
}

func TestEnsureU2RejectsUnpinnedArtifactBeforeDevice(t *testing.T) {
	d := newStubDevice(t, "probe")
	dir := t.TempDir()
	jar := filepath.Join(dir, "u2.jar")
	if err := os.WriteFile(jar, []byte("not the pinned artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.localJarPath = jar
	if err := d.EnsureU2(t.Context()); CodeOf(err) != CodeArtifact {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
}

func TestStartDisplayHelperRejectsNonUUIDBeforeShell(t *testing.T) {
	d := newStubDevice(t, "probe")
	record := filepath.Join(t.TempDir(), "argv.txt")
	d.testEnv = append(d.testEnv, "MOBILE_USE_FAKE_ADB_ARGV="+record)
	err := d.StartDisplayHelper(t.Context(), "not-a-uuid; touch /data/local/tmp/x", "00000000-0000-0000-0000-000000000002")
	if CodeOf(err) != CodeInvalidArgs {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	if _, statErr := os.Stat(record); statErr == nil {
		t.Fatal("a rejected helper identity must not run a shell command")
	}
}

func TestVerifyLocalJarHash(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "u2.jar")
	if err := os.WriteFile(good, []byte("not the pinned artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyLocalJar(good); CodeOf(err) != CodeArtifact {
		t.Fatalf("unpinned artifact code=%q", CodeOf(err))
	}
	if _, err := verifyLocalJar(filepath.Join(dir, "missing.jar")); CodeOf(err) != CodeArtifact {
		t.Fatalf("missing artifact code=%q", CodeOf(err))
	}
	if _, err := verifyLocalJar(""); CodeOf(err) != CodeArtifact {
		t.Fatalf("empty path code=%q", CodeOf(err))
	}
	if _, err := verifyLocalJar(dir); CodeOf(err) != CodeArtifact {
		t.Fatalf("directory code=%q", CodeOf(err))
	}
	sum, err := hashFile(good)
	if err != nil || len(sum) != 64 || sum != strings.ToLower(sum) {
		t.Fatalf("hashFile=%q err=%v", sum, err)
	}
	if sum == U2JarSHA256 {
		t.Fatal("the fixture must not match the pinned hash")
	}
	if U2JarSHA256 != "28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3" {
		t.Fatalf("pinned hash changed: %q", U2JarSHA256)
	}
}
