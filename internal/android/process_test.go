package android

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// These tests run the real transport against the stubbed adb process, so the
// exact argv shape, the root fallback and every receipt mapping are exercised
// without a device.

func TestPackageProcessesMainAndRemote(t *testing.T) {
	d := newStubDevice(t, "processes")
	got, err := d.PackageProcesses(t.Context(), "com.example.app")
	if err != nil {
		t.Fatalf("PackageProcesses: %v", err)
	}
	if !got.Known || got.ReadFailed {
		t.Fatalf("known=%v read_failed=%v", got.Known, got.ReadFailed)
	}
	if got.UserID != 10140 || got.BootID != testBootID {
		t.Fatalf("uid=%d boot=%q", got.UserID, got.BootID)
	}
	if len(got.Instances) != 2 {
		t.Fatalf("instances=%+v", got.Instances)
	}
	if got.Instances[0] != (device.ProcessInstance{
		PID: 101, StartTicks: 98765, Name: "com.example.app"}) {
		t.Fatalf("main instance=%+v", got.Instances[0])
	}
	if got.Instances[1] != (device.ProcessInstance{
		PID: 202, StartTicks: 98800, Name: "com.example.app:remote"}) {
		t.Fatalf("remote instance=%+v", got.Instances[1])
	}
}

func TestPackageProcessesEmptyIsKnown(t *testing.T) {
	d := newStubDevice(t, "processes-empty")
	got, err := d.PackageProcesses(t.Context(), "com.example.app")
	if err != nil {
		t.Fatalf("PackageProcesses: %v", err)
	}
	if !got.Known || got.ReadFailed {
		t.Fatalf("known=%v read_failed=%v", got.Known, got.ReadFailed)
	}
	if got.Instances == nil {
		t.Fatal("a proven absence carries an empty inventory, not a nil one")
	}
	if len(got.Instances) != 0 {
		t.Fatalf("instances=%+v", got.Instances)
	}
	if got.UserID != 10140 || got.BootID != testBootID {
		t.Fatalf("uid=%d boot=%q", got.UserID, got.BootID)
	}
}

func TestPackageProcessesReadFailuresAreUnknown(t *testing.T) {
	cases := []struct {
		name       string
		transcript string
	}{
		{"package uid unreadable", "processes-unreadable"},
		{"start ticks need an unavailable root", "processes-no-root"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newStubDevice(t, tc.transcript)
			got, err := d.PackageProcesses(t.Context(), "com.example.app")
			if err != nil {
				t.Fatalf("a failed read is a fact, not a transport error: %v", err)
			}
			if got.Known || !got.ReadFailed {
				t.Fatalf("known=%v read_failed=%v", got.Known, got.ReadFailed)
			}
			if len(got.Instances) != 0 {
				t.Fatalf("instances=%+v", got.Instances)
			}
		})
	}
}

func TestPackageProcessesRootFallback(t *testing.T) {
	d := newStubDevice(t, "processes-root")
	got, err := d.PackageProcesses(t.Context(), "com.example.app")
	if err != nil {
		t.Fatalf("PackageProcesses: %v", err)
	}
	if !got.Known || got.ReadFailed || len(got.Instances) != 2 {
		t.Fatalf("known=%v read_failed=%v instances=%+v",
			got.Known, got.ReadFailed, got.Instances)
	}
	if got.Instances[0].StartTicks != 98765 || got.Instances[1].StartTicks != 98800 {
		t.Fatalf("instances=%+v", got.Instances)
	}
}

func TestPackageProcessesRejectsInvalidPackage(t *testing.T) {
	d := newStubDevice(t, "processes")
	got, err := d.PackageProcesses(t.Context(), "com.example app")
	if CodeOf(err) != CodeInvalidArgs {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	if got.Known || !got.ReadFailed {
		t.Fatalf("known=%v read_failed=%v", got.Known, got.ReadFailed)
	}
}

func TestKillProcessDispatchesExactInstance(t *testing.T) {
	d := newStubDevice(t, "processes")
	d.setShellV2(true)
	receipt, err := d.KillProcess(t.Context(), 101, 98765)
	if err != nil {
		t.Fatalf("KillProcess: %v", err)
	}
	if receipt != device.DispatchTerminated {
		t.Fatalf("receipt=%d", receipt)
	}
}

func TestKillProcessRefusesChangedInstance(t *testing.T) {
	d := newStubDevice(t, "processes-ticks-changed")
	d.setShellV2(true)
	receipt, err := d.KillProcess(t.Context(), 101, 98765)
	if receipt != device.DispatchNotDispatched {
		t.Fatalf("receipt=%d, a changed instance must never signal", receipt)
	}
	if CodeOf(err) != CodeAvailability {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
}

func TestKillProcessRefusesUnverifiedIdentity(t *testing.T) {
	d := newStubDevice(t, "processes-kill-no-root")
	d.setShellV2(true)
	receipt, err := d.KillProcess(t.Context(), 101, 98765)
	if receipt != device.DispatchNotDispatched {
		t.Fatalf("receipt=%d, an unverified identity must never signal", receipt)
	}
	if CodeOf(err) != CodeAvailability {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
}

// TestKillProcessUnconfirmedDispatch proves a rejected su never becomes a
// terminated receipt: nothing at the command boundary proved the signal landed.
func TestKillProcessUnconfirmedDispatch(t *testing.T) {
	d := newStubDevice(t, "processes-kill-denied")
	d.setShellV2(true)
	receipt, err := d.KillProcess(t.Context(), 101, 98765)
	if receipt != device.DispatchUnknown {
		t.Fatalf("receipt=%d", receipt)
	}
	if CodeOf(err) != CodeAvailability {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
}

func TestKillProcessRejectsInvalidIdentity(t *testing.T) {
	d := newStubDevice(t, "processes")
	d.setShellV2(true)
	for _, tc := range []struct {
		name  string
		pid   int
		ticks uint64
	}{
		{"zero pid", 0, 98765},
		{"negative pid", -1, 98765},
		{"missing start ticks", 101, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt, err := d.KillProcess(t.Context(), tc.pid, tc.ticks)
			if receipt != device.DispatchNotDispatched || CodeOf(err) != CodeInvalidArgs {
				t.Fatalf("receipt=%d code=%q err=%v", receipt, CodeOf(err), err)
			}
		})
	}
}

func TestMediaPlayback(t *testing.T) {
	cases := []struct {
		name       string
		transcript string
		want       device.MediaState
	}{
		{"playing", "media-playing", device.MediaState{Known: true, Playing: true}},
		{"stopped", "media-stopped", device.MediaState{Known: true}},
		{"unrecognized", "media-unknown", device.MediaState{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newStubDevice(t, tc.transcript)
			got, err := d.MediaPlayback(t.Context())
			if err != nil {
				t.Fatalf("MediaPlayback: %v", err)
			}
			if got != tc.want {
				t.Fatalf("state=%+v want=%+v", got, tc.want)
			}
		})
	}
}

func TestForegroundService(t *testing.T) {
	cases := []struct {
		name       string
		transcript string
		pkg        string
		want       device.ServiceState
	}{
		{"foreground", "fgs-hit", "com.example.app",
			device.ServiceState{Known: true, Foreground: true}},
		{"background", "fgs-none", "com.example.app", device.ServiceState{Known: true}},
		{"other package only", "fgs-hit", "com.example.other", device.ServiceState{Known: true}},
		{"unrecognized", "fgs-unknown", "com.example.app", device.ServiceState{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newStubDevice(t, tc.transcript)
			got, err := d.ForegroundService(t.Context(), tc.pkg)
			if err != nil {
				t.Fatalf("ForegroundService: %v", err)
			}
			if got != tc.want {
				t.Fatalf("state=%+v want=%+v", got, tc.want)
			}
		})
	}
}

func TestForegroundServiceRejectsInvalidPackage(t *testing.T) {
	d := newStubDevice(t, "fgs-hit")
	if _, err := d.ForegroundService(t.Context(), "com.example app"); CodeOf(err) != CodeInvalidArgs {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
}

// TestParseStartTicks keeps the comm/parenthesis handling honest: only field 22
// counts, and a malformed line is never a zero tick.
func TestParseStartTicks(t *testing.T) {
	cases := []struct {
		name  string
		stat  string
		ticks uint64
		ok    bool
	}{
		{
			name:  "plain comm",
			stat:  processStatFixture(101, "com.example.app", 98765),
			ticks: 98765,
			ok:    true,
		},
		{
			name:  "comm with spaces and parentheses",
			stat:  processStatFixture(101, "example app (work)", 55555),
			ticks: 55555,
			ok:    true,
		},
		{
			name: "truncated line",
			stat: "101 (com.example.app) S 1 101",
		},
		{
			name: "no parentheses",
			stat: "101 com.example.app S 1 101",
		},
		{
			name: "zero start ticks",
			stat: processStatFixture(101, "com.example.app", 0),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ticks, ok := parseStartTicks(tc.stat)
			if ok != tc.ok || ticks != tc.ticks {
				t.Fatalf("ticks=%d ok=%v want=%d/%v", ticks, ok, tc.ticks, tc.ok)
			}
		})
	}
}

// TestParseStartTicksOutputIgnoresMalformedRecords proves one unreadable PID is
// simply absent from the map; the caller decides that the read failed.
func TestParseStartTicksOutputIgnoresMalformedRecords(t *testing.T) {
	output := "stat " + processStatFixture(101, "com.example.app", 98765) + "\n" +
		"stat 202 -\n" +
		"stat 303 303 (truncated)\n" +
		"garbage\n"
	ticks := parseStartTicksOutput(output)
	if len(ticks) != 1 || ticks[101] != 98765 {
		t.Fatalf("ticks=%v", ticks)
	}
}

func TestParseProcessListing(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   []processRow
		ok     bool
	}{
		{
			name:   "typed rows",
			output: processListingFixture,
			want: []processRow{
				{pid: 1, uid: 0, name: "init"},
				{pid: 101, uid: 10140, name: "com.example.app"},
				{pid: 202, uid: 10140, name: "com.example.app:remote"},
				{pid: 303, uid: 10141, name: "com.example.other"},
			},
			ok: true,
		},
		{
			name:   "name keeps its spaces",
			output: processHeaderFixture + "101 10140 example app (work)\n",
			want:   []processRow{{pid: 101, uid: 10140, name: "example app (work)"}},
			ok:     true,
		},
		{
			name:   "header without rows is a proven empty table",
			output: processHeaderFixture,
			want:   []processRow{},
			ok:     true,
		},
		{
			name:   "missing header",
			output: "101 10140 com.example.app\n",
		},
		{
			name:   "unreadable uid column",
			output: processHeaderFixture + "101 u0_a140 com.example.app\n",
		},
		{
			name:   "partial table is not an inventory",
			output: processHeaderFixture + "101 10140 com.example.app\n?\n",
		},
		{
			name:   "empty output",
			output: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseProcessListing(tc.output)
			if ok != tc.ok {
				t.Fatalf("ok=%v want=%v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("rows=%+v want=%+v", got, tc.want)
			}
			for index := range tc.want {
				if got[index] != tc.want[index] {
					t.Fatalf("row %d=%+v want=%+v", index, got[index], tc.want[index])
				}
			}
		})
	}
}

func TestParsePackageUID(t *testing.T) {
	cases := []struct {
		name   string
		output string
		pkg    string
		uid    int
		ok     bool
	}{
		{
			name:   "exact record",
			output: "package:com.example.app uid:10140\npackage:com.example.other uid:10141\n",
			pkg:    "com.example.app",
			uid:    10140,
			ok:     true,
		},
		{
			name:   "trailing gids",
			output: "package:com.example.app uid:10140 gids:[3003,9997]\n",
			pkg:    "com.example.app",
			uid:    10140,
			ok:     true,
		},
		{
			name:   "substring package is not the target",
			output: "package:com.example.app.debug uid:10141\n",
			pkg:    "com.example.app",
		},
		{
			name:   "two uids are ambiguous",
			output: "package:com.example.app uid:10140\npackage:com.example.app uid:10141\n",
			pkg:    "com.example.app",
		},
		{
			name:   "system uid is not an application",
			output: "package:com.example.app uid:1000\n",
			pkg:    "com.example.app",
		},
		{
			name:   "no record",
			output: "package records unavailable\n",
			pkg:    "com.example.app",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uid, ok := parsePackageUID(tc.output, tc.pkg)
			if ok != tc.ok || uid != tc.uid {
				t.Fatalf("uid=%d ok=%v want=%d/%v", uid, ok, tc.uid, tc.ok)
			}
		})
	}
}

// TestParseForegroundServiceMatchesExactPackage proves a package filter substring
// never satisfies the service guard: a `.debug` record cannot justify a kill.
func TestParseForegroundServiceMatchesExactPackage(t *testing.T) {
	other := strings.ReplaceAll(serviceForegroundFixture(true), "com.example.app", "com.example.app.debug")
	state := parseForegroundService(other, "com.example.app")
	if !state.Known || state.Foreground {
		t.Fatalf("state=%+v", state)
	}
	state = parseForegroundService(other, "com.example.app.debug")
	if !state.Known || !state.Foreground {
		t.Fatalf("state=%+v", state)
	}
}

// TestParseForegroundServiceAcceptsForegroundNotification covers the equivalent
// marker: a live foreground notification id still counts as a foreground service.
func TestParseForegroundServiceAcceptsForegroundNotification(t *testing.T) {
	output := strings.ReplaceAll(serviceForegroundFixture(true), "isForeground=true", "isForeground=false")
	state := parseForegroundService(output, "com.example.app")
	if !state.Known || !state.Foreground {
		t.Fatalf("state=%+v", state)
	}
}

func TestParseMediaPlayback(t *testing.T) {
	if state := parseMediaPlayback(mediaPlayingFixture); state != (device.MediaState{Known: true, Playing: true}) {
		t.Fatalf("playing=%+v", state)
	}
	if state := parseMediaPlayback(mediaStoppedFixture); state != (device.MediaState{Known: true}) {
		t.Fatalf("stopped=%+v", state)
	}
	if state := parseMediaPlayback("unexpected dump\n"); state != (device.MediaState{}) {
		t.Fatalf("unknown=%+v", state)
	}
}
