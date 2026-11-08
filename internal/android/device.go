// Package android is the ADB transport, fixed uiautomator RPC client and
// artifact manager for one explicitly selected phone. It holds no session or
// action authority: callers decide what to observe and act on, this package
// only performs bounded device I/O and reports controlled facts.
package android

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
)

// Defaults mirror the pinned Python deployment so both implementations can
// share one device and one fixed artifact path.
const (
	defaultADBPath        = "adb"
	defaultU2Port         = 9008
	defaultRemoteJar      = "/data/local/tmp/u2.jar"
	defaultControlPackage = "com.mobileuse.vnext"
	defaultTimeout        = 8 * time.Second
	defaultMaxOutput      = 8 << 20
	// U2JarSHA256 is the pinned patched server artifact. Any other local bytes
	// are refused before the device is touched.
	U2JarSHA256 = "28361193de3d13303543c13bdc7268c22edecabfa4e68e6fbdfc35fffe1406a3"
	// u2ProcessPattern matches only this project's server process. It is used
	// verbatim with pkill -f so unrelated app_process jobs are never touched.
	u2ProcessPattern = "^app_process / com.wetest.uia2.Main$"
	u2MainClass      = "com.wetest.uia2.Main"
)

// Config selects the transport and the fixed artifacts.
type Config struct {
	AdbPath        string        // executable, default "adb"
	Serial         string        // explicit device serial; never auto-selected
	U2Port         int           // default 9008
	LocalJarPath   string        // artifact to verify and push
	RemoteJarPath  string        // default /data/local/tmp/u2.jar
	AppListDexPath string        // fixed app discovery helper to verify and push
	RemoteAppList  string        // default /data/local/tmp/mobile-use-applist.dex
	ControlPackage string        // default "com.mobileuse.vnext"
	Timeout        time.Duration // per-command timeout
}

// ShellResult is one bounded adb shell outcome. The payload is device data and
// must never be copied into logs or error text.
type ShellResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Device is one configured phone transport. It performs no automatic display
// selection and no retries.
type Device struct {
	adbPath           string
	serial            string
	u2Port            int
	localJarPath      string
	remoteJarPath     string
	appListPath       string
	remoteAppListPath string
	controlPackage    string
	timeout           time.Duration
	maxOutput         int

	log    *eventlog.Logger
	client *http.Client
	base   string
	// testBase overrides the RPC base URL for httptest fixtures only.
	testBase string
	// testEnv appends environment entries to adb invocations for process-level
	// tests with a stubbed adb; it is nil in production.
	testEnv []string

	// mu serializes server lifecycle operations. It is never held across a
	// caller-provided capture or input path.
	mu      sync.Mutex
	server  *serverProcess
	helperA struct {
		path string
		uid  int
	}
	helperFetchedAt time.Time
}

// New validates the configuration and returns a transport. It performs no I/O.
func New(cfg Config, log *eventlog.Logger) (*Device, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	adb := cfg.AdbPath
	if adb == "" {
		adb = defaultADBPath
	}
	if strings.ContainsAny(adb, "\r\n\x00") {
		return nil, newErr(CodeInvalidArgs, "invalid adb executable path", nil)
	}
	if cfg.Serial == "" {
		return nil, newErr(CodeInvalidArgs, "a device serial is required", nil)
	}
	if strings.ContainsAny(cfg.Serial, " \t\r\n\x00") || strings.HasPrefix(cfg.Serial, "-") {
		return nil, newErr(CodeInvalidArgs, "invalid device serial", nil)
	}
	port := cfg.U2Port
	if port == 0 {
		port = defaultU2Port
	}
	if port < 1 || port > 65535 {
		return nil, newErr(CodeInvalidArgs, "invalid u2 port", nil)
	}
	remote := cfg.RemoteJarPath
	if remote == "" {
		remote = defaultRemoteJar
	}
	if !strings.HasPrefix(remote, "/") || strings.ContainsAny(remote, " \t\r\n'\"`$;&|<>()") {
		return nil, newErr(CodeInvalidArgs, "invalid remote jar path", nil)
	}
	control := cfg.ControlPackage
	if control == "" {
		control = defaultControlPackage
	}
	if !validPackageName(control) {
		return nil, newErr(CodeInvalidArgs, "invalid control package name", nil)
	}
	remoteAppList := cfg.RemoteAppList
	if remoteAppList == "" {
		remoteAppList = defaultRemoteAppListDex
	}
	if !strings.HasPrefix(remoteAppList, "/") || strings.ContainsAny(remoteAppList, " \t\r\n'\"`$;&|<>()") {
		return nil, newErr(CodeInvalidArgs, "invalid remote app list path", nil)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	d := &Device{
		adbPath:           adb,
		serial:            cfg.Serial,
		u2Port:            port,
		localJarPath:      cfg.LocalJarPath,
		remoteJarPath:     remote,
		appListPath:       cfg.AppListDexPath,
		remoteAppListPath: remoteAppList,
		controlPackage:    control,
		timeout:           timeout,
		maxOutput:         defaultMaxOutput,
		log:               log,
		base:              fmt.Sprintf("http://127.0.0.1:%d", port),
	}
	d.client = &http.Client{Timeout: timeout}
	return d, nil
}

func validateConfig(cfg Config) error {
	if cfg.Timeout < 0 {
		return newErr(CodeInvalidArgs, "invalid timeout", nil)
	}
	return nil
}

func validPackageName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
		default:
			return false
		}
	}
	return true
}

// logEvent records one whitelisted event. Callers pass only safe fields.
func (d *Device) logEvent(level eventlog.Level, name string, fields ...eventlog.Field) {
	if d == nil || d.log == nil {
		return
	}
	d.log.Event(level, name, fields...)
}

// adbArgs builds the fixed argv prefix: adb -s <serial> <verb> ...
func (d *Device) adbArgs(verb string, rest ...string) []string {
	args := make([]string, 0, 4+len(rest))
	args = append(args, "-s", d.serial, verb)
	return append(args, rest...)
}

// exePath resolves the adb executable once so a missing binary is reported as a
// controlled availability error instead of a path-dependent exec failure.
func (d *Device) exePath() (string, error) {
	if d == nil {
		return "", errNilDevice
	}
	if strings.ContainsRune(d.adbPath, '/') || strings.ContainsRune(d.adbPath, '\\') {
		return d.adbPath, nil
	}
	path, err := exec.LookPath(d.adbPath)
	if err != nil {
		return "", errADBMissing
	}
	return path, nil
}

// command creates one context-bounded exec.Cmd. A context without a deadline
// gets the configured per-command timeout.
func (d *Device) command(ctx context.Context, exe string, args ...string) (*exec.Cmd, context.CancelFunc) {
	if _, ok := ctx.Deadline(); !ok {
		ctx, cancel := context.WithTimeout(ctx, d.timeout)
		return exec.CommandContext(ctx, exe, args...), cancel
	}
	var noop context.CancelFunc = func() {}
	return exec.CommandContext(ctx, exe, args...), noop
}

// boundedBuffer keeps the first maxOutput bytes and reports overflow.
type boundedBuffer struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := b.limit - b.buf.Len(); room < n {
		if room > 0 {
			b.buf.Write(p[:room])
		} else if n > 0 {
			b.overflow = true
		}
		if room < n {
			b.overflow = true
		}
		return n, nil
	}
	b.buf.Write(p)
	return n, nil
}

func (b *boundedBuffer) Bytes() []byte { return b.buf.Bytes() }

// run executes one bounded process and returns stdout, stderr and exit code.
// stderr is kept only for the caller's evidence and never for errors or logs.
func (d *Device) run(ctx context.Context, timeout time.Duration, exe string, args ...string) (ShellResult, error) {
	return d.runInput(ctx, timeout, nil, exe, args...)
}

// runInput executes one bounded process with optional stdin bytes. Content
// passed here is never logged and never appears in the argv.
func (d *Device) runInput(ctx context.Context, timeout time.Duration, input []byte, exe string, args ...string) (ShellResult, error) {
	var result ShellResult
	if err := contextError(ctx); err != nil {
		return result, err
	}
	if timeout <= 0 {
		timeout = d.timeout
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	outBuf := &boundedBuffer{limit: d.maxOutput}
	errBuf := &boundedBuffer{limit: 64 << 10}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	cmd.WaitDelay = time.Second
	if len(d.testEnv) > 0 {
		cmd.Env = append(os.Environ(), d.testEnv...)
	}
	err := cmd.Run()
	result.Stdout = outBuf.Bytes()
	result.Stderr = errBuf.Bytes()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, contextError(ctx)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			result.ExitCode = code
			if (code == 1 || code == 2) && len(result.Stdout) == 0 &&
				len(result.Stderr) > 0 && isADBFailureStderr(result.Stderr) {
				// adb itself reports device selection and transport failures with
				// status 1 or 2. Device stdout is never inspected for text and no
				// byte of it is retained in the error.
				return result, newErr(CodeTransport, "the adb command failed", err)
			}
			return result, nil
		}
		return result, execFailure(err, false)
	}
	return result, nil
}

// isADBFailureStderr recognises adb's own diagnostics.
func isADBFailureStderr(stderr []byte) bool {
	lower := strings.ToLower(string(stderr))
	for _, marker := range []string{
		"device '", "not found", "no devices", "error:", "cannot connect",
		"adb: ", "unauthorized", "offline",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// Execute runs the whole argv (already including the executable). It exists for
// tests; normal callers use Shell or Screenshot.
func (d *Device) Execute(ctx context.Context, timeout time.Duration, exe string, args ...string) (ShellResult, error) {
	if d == nil {
		return ShellResult{}, errNilDevice
	}
	return d.run(ctx, timeout, exe, args...)
}

// Shell runs one adb shell command with a bounded timeout and returns its
// result. The command is passed as exactly one argv element, which is how adb
// shell forwards it to the device shell; the host never uses sh -c.
func (d *Device) Shell(ctx context.Context, command string) (ShellResult, error) {
	return d.shellWith(ctx, d.timeout, command)
}

func (d *Device) shellWith(ctx context.Context, timeout time.Duration, command string) (ShellResult, error) {
	if d == nil {
		return ShellResult{}, errNilDevice
	}
	if command == "" {
		return ShellResult{}, newErr(CodeInvalidArgs, "empty shell command", nil)
	}
	exe, err := d.exePath()
	if err != nil {
		return ShellResult{}, err
	}
	return d.run(ctx, timeout, exe, d.adbArgs("shell", command)...)
}

// shellChecked runs one shell command and reports a non-zero device status as a
// controlled exit error.
func (d *Device) shellChecked(ctx context.Context, command string) (ShellResult, error) {
	result, err := d.Shell(ctx, command)
	if err != nil {
		return result, err
	}
	if result.ExitCode != 0 {
		return result, exitDetail(result.ExitCode)
	}
	return result, nil
}

// Prepare verifies the ADB transport and reads static identity. It does not
// start the u2 server.
func (d *Device) Prepare(ctx context.Context) error {
	if d == nil {
		return errNilDevice
	}
	started := time.Now()
	exe, err := d.exePath()
	if err != nil {
		return err
	}
	result, err := d.run(ctx, d.timeout, exe, d.adbArgs("get-state")...)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 || firstField(result.Stdout) != "device" {
		return newErr(CodeAvailability, "the selected device is not connected", nil)
	}
	if _, _, _, _, _, _, err := d.Identity(ctx); err != nil {
		return err
	}
	d.logEvent(eventlog.Info, "android.transport.prepared",
		eventlog.Str("state", "device"),
		eventlog.Duration("ms", time.Since(started)))
	return nil
}

// Identity returns model/Android version/SDK and effective main display metrics.
func (d *Device) Identity(ctx context.Context) (model string, androidVersion string, sdk int, width, height, density int, err error) {
	if d == nil {
		return "", "", 0, 0, 0, 0, errNilDevice
	}
	var props [3]string
	for index, prop := range []string{"ro.product.model", "ro.build.version.release", "ro.build.version.sdk"} {
		result, runErr := d.shellChecked(ctx, "getprop "+prop)
		if runErr != nil {
			return "", "", 0, 0, 0, 0, runErr
		}
		props[index] = strings.TrimSpace(string(result.Stdout))
	}
	sdk, ok := parseIntStrict(props[2])
	if !ok || sdk <= 0 {
		return "", "", 0, 0, 0, 0, newErr(CodeAvailability, "the device SDK level is unreadable", nil)
	}
	if props[0] == "" || props[1] == "" {
		return "", "", 0, 0, 0, 0, newErr(CodeAvailability, "the device identity is unreadable", nil)
	}
	width, height, density, err = d.LogicalSize(ctx)
	if err != nil {
		return "", "", 0, 0, 0, 0, err
	}
	return props[0], props[1], sdk, width, height, density, nil
}

// LogicalSize reads the effective logical size and density used by input.
func (d *Device) LogicalSize(ctx context.Context) (width, height, density int, err error) {
	if d == nil {
		return 0, 0, 0, errNilDevice
	}
	sizeResult, err := d.shellChecked(ctx, "wm size")
	if err != nil {
		return 0, 0, 0, err
	}
	densityResult, err := d.shellChecked(ctx, "wm density")
	if err != nil {
		return 0, 0, 0, err
	}
	size, ok := parseWMSize(string(sizeResult.Stdout))
	if !ok {
		return 0, 0, 0, newErr(CodeAvailability, "the display size is unreadable", nil)
	}
	dpi, ok := parseWMDensity(string(densityResult.Stdout))
	if !ok {
		return 0, 0, 0, newErr(CodeAvailability, "the display density is unreadable", nil)
	}
	return size.width, size.height, dpi, nil
}

type size struct{ width, height int }

// parseWMSize prefers the last Override line and falls back to the last
// Physical line, which is the coordinate space input commands use.
func parseWMSize(output string) (size, bool) {
	var override, physical []size
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		var rest string
		switch {
		case strings.HasPrefix(line, "Override size:"):
			rest = strings.TrimSpace(strings.TrimPrefix(line, "Override size:"))
		case strings.HasPrefix(line, "Physical size:"):
			rest = strings.TrimSpace(strings.TrimPrefix(line, "Physical size:"))
		default:
			continue
		}
		w, h, ok := parseWxH(rest)
		if !ok {
			continue
		}
		if strings.HasPrefix(line, "Override") {
			override = append(override, size{w, h})
		} else {
			physical = append(physical, size{w, h})
		}
	}
	if len(override) > 0 {
		return override[len(override)-1], true
	}
	if len(physical) > 0 {
		return physical[len(physical)-1], true
	}
	return size{}, false
}

// parseWMDensity prefers the last Override density over Physical density.
func parseWMDensity(output string) (int, bool) {
	var override, physical int
	hasOverride, hasPhysical := false, false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Override density:"):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "Override density:"))
			if value, ok := parseIntStrict(rest); ok && value > 0 {
				override, hasOverride = value, true
			}
		case strings.HasPrefix(line, "Physical density:"):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "Physical density:"))
			if value, ok := parseIntStrict(rest); ok && value > 0 {
				physical, hasPhysical = value, true
			}
		}
	}
	if hasOverride {
		return override, true
	}
	if hasPhysical {
		return physical, true
	}
	return 0, false
}

func parseWxH(text string) (int, int, bool) {
	parts := strings.SplitN(text, "x", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, okW := parseIntStrict(strings.TrimSpace(parts[0]))
	h, okH := parseIntStrict(strings.TrimSpace(parts[1]))
	if !okW || !okH || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// maxScreenshotBytes bounds one source screenshot read. The encoded output is
// separately bounded; this cap only protects the process from a runaway write.
const maxScreenshotBytes = 24 << 20

// CaptureImage reads raw PNG bytes for one SurfaceFlinger display id. The layer
// id is an unsigned 64-bit value (it exceeds int64 on this device). CapturedAt
// is the start of the device read, so later encode/delivery work cannot make
// the source look newer than it is.
func (d *Device) CaptureImage(ctx context.Context, sfid uint64) (device.ImageSource, error) {
	if d == nil {
		return device.ImageSource{}, errNilDevice
	}
	if sfid == 0 {
		return device.ImageSource{}, newErr(CodeInvalidArgs, "invalid display surface id", nil)
	}
	exe, err := d.exePath()
	if err != nil {
		return device.ImageSource{}, err
	}
	started := time.Now()
	result, err := d.run(ctx, d.screenshotTimeout(), exe,
		d.adbArgs("exec-out", "screencap", "-p", "-d", fmt.Sprintf("%d", sfid))...)
	if err != nil {
		return device.ImageSource{}, err
	}
	if result.ExitCode != 0 {
		return device.ImageSource{}, exitDetail(result.ExitCode)
	}
	if len(result.Stdout) > maxScreenshotBytes {
		return device.ImageSource{}, newErr(CodeOutputTooBig, "the screenshot exceeds the source limit", nil)
	}
	if !bytes.HasPrefix(result.Stdout, []byte("\x89PNG\r\n\x1a\n")) {
		return device.ImageSource{}, newErr(CodeAvailability, "the screenshot is not a PNG image", nil)
	}
	return device.ImageSource{Data: result.Stdout, CapturedAt: started}, nil
}

func (d *Device) screenshotTimeout() time.Duration {
	if d.timeout < 10*time.Second {
		return 10 * time.Second
	}
	return d.timeout
}

// ProbeDisplay resolves whether the logical display still exists and its
// SurfaceFlinger layer id. It never falls back to display 0.
func (d *Device) ProbeDisplay(ctx context.Context, displayID int) (device.DisplayState, error) {
	if d == nil {
		return device.DisplayState{}, errNilDevice
	}
	if displayID < 0 {
		return device.DisplayState{}, newErr(CodeInvalidArgs, "invalid display id", nil)
	}
	listing, err := d.shellChecked(ctx, "dumpsys SurfaceFlinger --display-id")
	if err != nil {
		return device.DisplayState{}, err
	}
	sfids := parseDisplayIDs(string(listing.Stdout))
	var state device.DisplayState
	if displayID == 0 {
		sfid, ok := sfids.primaryID()
		if !ok {
			return device.DisplayState{}, nil
		}
		logical, err := d.shellChecked(ctx, "dumpsys display")
		if err != nil {
			return device.DisplayState{}, err
		}
		if _, ok := parsePrimaryDisplayInfo(string(logical.Stdout)); !ok {
			return device.DisplayState{}, nil
		}
		state.Exists = true
		state.SFID = sfid
		return state, nil
	}
	logical, err := d.shellChecked(ctx, "dumpsys display")
	if err != nil {
		return device.DisplayState{}, err
	}
	text := string(logical.Stdout)
	if !displayIDPresent(text, displayID) {
		if !displayIDMentioned(text) {
			return device.DisplayState{}, newErr(CodeDisplayUnknown,
				"the display state is unreadable", nil)
		}
		// The logical display is definitively not in the display list. A
		// SurfaceFlinger layer may still belong to another managed display.
		return device.DisplayState{Exists: false}, nil
	}
	sfid, ok := sfids.virtualID()
	if !ok {
		if len(sfids.virtual) > 1 {
			return device.DisplayState{}, newErr(CodeDisplayUnknown,
				"more than one managed display surface is present", nil)
		}
		return device.DisplayState{}, newErr(CodeDisplayUnknown,
			"the managed display surface is not listed yet", nil)
	}
	state.Exists = true
	state.SFID = sfid
	return state, nil
}

// displayIDs holds the SurfaceFlinger ids grouped by display kind.
type displayIDs struct {
	primary []uint64
	virtual []uint64
}

// primaryID returns the single primary SurfaceFlinger id, if unambiguous.
func (ids displayIDs) primaryID() (uint64, bool) { return single(ids.primary) }

// virtualID returns the single virtual SurfaceFlinger id, if unambiguous.
func (ids displayIDs) virtualID() (uint64, bool) { return single(ids.virtual) }

func single(values []uint64) (uint64, bool) {
	if len(values) != 1 {
		return 0, false
	}
	return values[0], true
}

// parseDisplayIDs reads `dumpsys SurfaceFlinger --display-id`. The primary is
// the HWC display 0 entry; a virtual display is either the HWC entry flagged as
// a virtual display or a "Virtual display" layer reporting MobileUseAgent.
func parseDisplayIDs(output string) displayIDs {
	var (
		ids      displayIDs
		primary  []uint64
		virtuals []uint64
	)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Display ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		id, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "Display "+fields[1]))
		switch {
		case strings.HasPrefix(rest, "(HWC display 0)"):
			primary = append(primary, id)
		case strings.Contains(rest, "displayName=\"MobileUseAgent\""):
			virtuals = append(virtuals, id)
		}
	}
	ids.primary = primary
	ids.virtual = virtuals
	return ids
}

// primaryDisplayInfo is the effective logical display info of display 0.
type primaryDisplayInfo struct {
	width, height, rotation int
	awake                   bool
}

// parsePrimaryDisplayInfo reads the mOverrideDisplayInfo line, falling back to
// mBaseDisplayInfo when the override record is absent.
func parsePrimaryDisplayInfo(output string) (primaryDisplayInfo, bool) {
	var info primaryDisplayInfo
	line := ""
	for _, candidate := range strings.Split(output, "\n") {
		if strings.Contains(candidate, "mOverrideDisplayInfo=DisplayInfo{") &&
			strings.Contains(candidate, "displayId 0,") {
			line = candidate
			break
		}
	}
	if line == "" {
		for _, candidate := range strings.Split(output, "\n") {
			if strings.Contains(candidate, "mBaseDisplayInfo=DisplayInfo{") &&
				strings.Contains(candidate, "displayId 0,") {
				line = candidate
				break
			}
		}
	}
	if line == "" {
		return info, false
	}
	index := strings.Index(line, "displayId 0,")
	if index < 0 {
		return info, false
	}
	line = line[index:]
	width, height, rotation, awake := parseDisplayInfoFields(line)
	if width <= 0 || height <= 0 {
		return info, false
	}
	if !awake {
		return info, false
	}
	info.width, info.height, info.rotation, info.awake = width, height, rotation, awake
	return info, true
}

// parseDisplayInfoFields extracts real/app size, rotation and state ON.
func parseDisplayInfoFields(text string) (width, height, rotation int, awake bool) {
	for _, match := range realSizePattern.FindAllStringSubmatch(text, -1) {
		w, okW := parseIntStrict(match[1])
		h, okH := parseIntStrict(match[2])
		if okW && okH && w > 0 && h > 0 {
			width, height = w, h
			break
		}
	}
	if match := rotationPattern.FindStringSubmatch(text); match != nil {
		if value, ok := parseIntStrict(match[1]); ok {
			rotation = value
		}
	}
	awake = strings.Contains(text, "state ON,") || strings.Contains(text, "state ON}")
	return width, height, rotation, awake
}

// displayIDPresent reports whether the logical dump names the display id.
func displayIDPresent(output string, displayID int) bool {
	needles := []string{
		fmt.Sprintf("mDisplayId=%d\n", displayID),
		fmt.Sprintf("mDisplayId=%d ", displayID),
		fmt.Sprintf("displayId %d,", displayID),
		fmt.Sprintf("mDisplayId %d,", displayID),
	}
	for _, needle := range needles {
		if strings.Contains(output, needle) {
			return true
		}
	}
	return false
}

// displayIDMentioned reports whether the display dump contains at least one
// display record, so "target absent" can be distinguished from an unreadable
// or truncated dump.
func displayIDMentioned(output string) bool {
	return strings.Contains(output, "mDisplayId=") || strings.Contains(output, "displayId ")
}

// Capture reads one target display's hierarchy, foreground and tasks and
// returns an immutable capture. It never publishes or modifies state.
func (d *Device) Capture(ctx context.Context, display device.Display) (device.Capture, error) {
	if d == nil {
		return device.Capture{}, errNilDevice
	}
	if display.ID < 0 || display.Width <= 0 || display.Height <= 0 {
		return device.Capture{}, newErr(CodeInvalidArgs, "invalid target display", nil)
	}
	started := time.Now()
	params := []any{true, maxHierarchyDepth}
	raw, err := d.rpc(ctx, "dumpWindowHierarchy", params, d.captureTimeout())
	if err != nil {
		return device.Capture{}, err
	}
	xml, ok := raw.(string)
	if !ok || xml == "" {
		return device.Capture{}, newErr(CodeHierarchy, "the hierarchy response is unusable", nil)
	}
	nodes, root, err := parseHierarchy([]byte(xml), display.ID, display.Width, display.Height)
	if err != nil {
		d.logEvent(eventlog.Warn, "android.capture.failed",
			eventlog.Int("display_id", display.ID),
			eventlog.Str("reason", "hierarchy"),
			eventlog.Duration("ms", time.Since(started)))
		return device.Capture{}, err
	}
	if len(nodes) == 0 {
		// A valid dump with no window on the target display is a legal empty
		// screen. Publish one synthetic display root; never invent descendants.
		bounds := device.Rect{Left: 0, Top: 0, Right: display.Width, Bottom: display.Height}
		nodes = []device.RawNode{{
			Index: 0, Parent: -1, DisplayID: display.ID, Visible: true,
			RawBounds: bounds, Bounds: bounds,
			HasEnabled: true, Enabled: true,
		}}
		root = 0
	}
	if display.ID != 0 && containsPackage(nodes, d.controlPackage) {
		// The control App must never be reachable through a virtual display tree;
		// an isolation violation fails the capture instead of being filtered.
		d.logEvent(eventlog.Warn, "android.capture.failed",
			eventlog.Int("display_id", display.ID),
			eventlog.Str("reason", "control_ui"))
		return device.Capture{}, newErr(CodeDisplayAbsent, "the control UI was captured on the virtual display", nil)
	}
	foreground, ferr := d.Foreground(ctx, display.ID)
	if ferr != nil {
		// Foreground is descriptive evidence; an unreadable source is unknown, not
		// a failed capture.
		foreground = unknownForeground("foreground_unreadable")
	}
	capture := device.Capture{
		Display:    display,
		Nodes:      stampCapture(nodes),
		Root:       root,
		Foreground: foreground,
		// CapturedAt is the start of the hierarchy read (T), not the end of
		// the dumpsys/foreground work, so freshness and image pairing use the
		// earliest actual source time.
		CapturedAt: started,
	}
	d.logEvent(eventlog.Debug, "android.capture.done",
		eventlog.Int("display_id", display.ID),
		eventlog.Int("count", len(nodes)),
		eventlog.Duration("ms", time.Since(started)))
	return capture, nil
}

// stampCapture replaces every child slice with a copy so later callers cannot
// mutate the source node records through aliasing.
func stampCapture(nodes []device.RawNode) []device.RawNode {
	if len(nodes) == 0 {
		return nodes
	}
	for index := range nodes {
		if len(nodes[index].Children) > 0 {
			children := make([]int, len(nodes[index].Children))
			copy(children, nodes[index].Children)
			nodes[index].Children = children
		}
	}
	return nodes
}

// containsPackage reports whether any node belongs to the package.
func containsPackage(nodes []device.RawNode, pkg string) bool {
	for index := range nodes {
		if nodes[index].Package == pkg {
			return true
		}
	}
	return false
}

// captureTimeout allows a slow first hierarchy dump after server start.
func (d *Device) captureTimeout() time.Duration {
	if d.timeout < 15*time.Second {
		return 15 * time.Second
	}
	return d.timeout
}

// hashFile returns the lowercase hex SHA-256 of one local file.
func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", newErr(CodeArtifact, "the local artifact could not be read", err)
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", newErr(CodeArtifact, "the local artifact could not be read", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// artifactDigestUnderTest lets unit tests substitute a known digest; it is empty
// in production, where U2JarSHA256 is always the expected value.
var artifactDigestUnderTest string

// expectedArtifactDigest returns the digest a local artifact must carry.
func expectedArtifactDigest() string {
	if artifactDigestUnderTest != "" {
		return artifactDigestUnderTest
	}
	return U2JarSHA256
}

// sha256Hex returns the lowercase hex SHA-256 of data.
func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// verifyLocalJar checks the local artifact against the pinned hash.
func verifyLocalJar(path string) (string, error) {
	if path == "" {
		return "", newErr(CodeArtifact, "no local u2 artifact is configured", nil)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", newErr(CodeArtifact, "the local u2 artifact is missing", err)
	}
	sum, err := hashFile(path)
	if err != nil {
		return "", err
	}
	if sum != expectedArtifactDigest() {
		return "", newErr(CodeArtifact, "the local u2 artifact has an unexpected SHA-256", nil)
	}
	return sum, nil
}

// remoteHash reads the device copy's SHA-256, returning "" when it is absent or
// unreadable; a missing remote file is expected before the first push.
func (d *Device) remoteHash(ctx context.Context) string {
	result, err := d.Shell(ctx, "sha256sum "+d.remoteJarPath)
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	sum := firstField(result.Stdout)
	if !isHex64(sum) {
		return ""
	}
	return strings.ToLower(sum)
}

func isHex64(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// firstField returns the first whitespace-separated token of the bytes.
func firstField(data []byte) string {
	for _, field := range bytes.Fields(data) {
		return string(field)
	}
	return ""
}
