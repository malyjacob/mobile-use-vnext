package android

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
)

// Package process, media-session and foreground-service primitives.
//
// They produce bounded read-only guard facts plus one exact `kill -9` dispatch.
// The engine owns ownership, guards, outcome and every exclusion; this file only
// reports what a device read or one command boundary proves.
//
// No package name, raw dump, PID, command or device text is ever logged. Events
// carry only whitelisted fields: a state token, a reason enum, a count and a
// duration.

const (
	// processReadFloor is the minimum bound of one process/media/service read.
	// The activity and package managers can be slow right after boot.
	processReadFloor = 10 * time.Second
	// applicationUIDMin is the first application UID; applicationUIDMax is the
	// first UID above the application range. A record outside the range is a
	// system or isolated identity this primitive never signals.
	applicationUIDMin = 10000
	applicationUIDMax = 100000
	// maxProcessInstances bounds one batched identity read. A package with more
	// live processes than this cannot be verified within one bounded command.
	maxProcessInstances = 128
	// startTimeFieldOffset is the index of /proc/<pid>/stat field 22 inside the
	// tail after the last ')': field 3 (state) starts at index 0.
	startTimeFieldOffset = 19
	// processNameTimeout is the bound of the exact identity re-read.
	processNameTimeout = 8 * time.Second
)

// processNamePattern accepts a package process name as printed by ps: the
// package itself or one of its `:suffix` processes.
var processNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]*(?::[A-Za-z0-9_.]+)?$`)

// psRowPattern matches one `ps -A -o PID,UID,NAME` row. The NAME column is the
// remainder of the line, so a comm that contains spaces is kept intact.
var psRowPattern = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+(\S.*)$`)

// psHeaderPattern matches the fixed column header of the listing.
var psHeaderPattern = regexp.MustCompile(`^\s*PID\s+UID\s+NAME\b`)

// uidColumnPattern reads the per-row UID token of `pm list packages -U`.
var uidColumnPattern = regexp.MustCompile(`(?:^|\s)uid:(\d+)(?:\s|$)`)

func (d *Device) processTimeout() time.Duration {
	if d == nil || d.timeout < processReadFloor {
		return processReadFloor
	}
	return d.timeout
}

// processRow is one parsed `ps` row.
type processRow struct {
	pid  int
	uid  int
	name string
}

// PackageProcesses enumerates the live processes of one package for one Android
// user: the main process and every `package:` child process. Known is true only
// when every required read completed; an empty known inventory is a proven
// absence, never a failed read.
//
// The command-side identity is resolved first: the package manager must prove the
// installed identity of that exact user (application id, installed user block and
// per-user full UID). The process table is then filtered by that full UID, so the
// same package under another Android user is never reported. The returned AppUID
// is the application id, not the full UID: the two differ for every user other
// than user 0.
//
// Start ticks come from /proc/<pid>/stat so the engine can later recognize the
// exact instance; PIDs the direct read cannot see are re-read once through the
// existing root path.
func (d *Device) PackageProcesses(ctx context.Context, pkg string, androidUserID int) (
	device.PackageProcesses, error,
) {
	result := device.PackageProcesses{ReadFailed: true, AndroidUserID: androidUserID}
	if d == nil {
		return result, errNilDevice
	}
	if !validPackageName(pkg) {
		return result, newErr(CodeInvalidArgs, "invalid package name", nil)
	}
	if androidUserID < 0 {
		return result, newErr(CodeInvalidArgs, "invalid Android user id", nil)
	}
	if err := contextError(ctx); err != nil {
		return result, err
	}
	started := time.Now()
	fail := func(reason string) (device.PackageProcesses, error) {
		d.logEvent(eventlog.Warn, "android.processes.read_failed",
			eventlog.Str("state", "unknown"),
			eventlog.Str("reason", reason),
			eventlog.Duration("ms", time.Since(started)))
		return result, nil
	}

	bootID, ok := d.readBootID(ctx)
	if !ok {
		return fail("boot_identity")
	}
	result.BootID = bootID
	identity, err := d.PackageIdentity(ctx, pkg, androidUserID)
	if err != nil {
		return fail("package_identity")
	}
	if !identity.Known || identity.AndroidUserID != androidUserID {
		return fail("package_identity")
	}
	fullUID := identity.AppUID
	result.AppUID = identity.AppID
	rows, ok := d.readProcessListing(ctx)
	if !ok {
		return fail("process_listing")
	}
	instances := make([]device.ProcessInstance, 0, 2)
	pids := make([]int, 0, 2)
	for _, row := range rows {
		if row.uid != fullUID {
			continue
		}
		if row.name != pkg && !strings.HasPrefix(row.name, pkg+":") {
			continue
		}
		instances = append(instances, device.ProcessInstance{PID: row.pid, Name: row.name})
		pids = append(pids, row.pid)
	}
	if len(instances) > 0 {
		sort.Ints(pids)
		ticks, ok := d.readStartTicks(ctx, pids)
		if !ok {
			return fail("start_ticks")
		}
		for index := range instances {
			value, present := ticks[instances[index].PID]
			if !present {
				return fail("start_ticks")
			}
			instances[index].StartTicks = value
		}
	}
	sort.SliceStable(instances, func(i, j int) bool { return instances[i].PID < instances[j].PID })
	result.Known = true
	result.ReadFailed = false
	result.Instances = instances
	d.logEvent(eventlog.Info, "android.processes.read",
		eventlog.Str("state", "known"),
		eventlog.Int("count", len(instances)),
		eventlog.Duration("ms", time.Since(started)))
	return result, nil
}

// readProcessListing reads the current process table. A listing with a record
// this parser cannot read is a failed read, not a partial inventory: a missing
// row would otherwise look like a proven absence.
func (d *Device) readProcessListing(ctx context.Context) ([]processRow, bool) {
	result, err := d.shellWith(ctx, d.processTimeout(), "ps -A -o PID,UID,NAME")
	if err != nil || result.ExitCode != 0 {
		return nil, false
	}
	return parseProcessListing(string(result.Stdout))
}

// parseProcessListing accepts only `PID UID NAME` rows under the fixed header.
func parseProcessListing(output string) ([]processRow, bool) {
	rows := make([]processRow, 0, 64)
	header := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !header {
			if psHeaderPattern.MatchString(line) {
				header = true
				continue
			}
			return nil, false
		}
		match := psRowPattern.FindStringSubmatch(line)
		if match == nil {
			return nil, false
		}
		pid, pidErr := strconv.Atoi(match[1])
		uid, uidErr := strconv.Atoi(match[2])
		name := strings.TrimSpace(match[3])
		if pidErr != nil || uidErr != nil || pid <= 0 || name == "" {
			return nil, false
		}
		rows = append(rows, processRow{pid: pid, uid: uid, name: name})
	}
	if !header {
		return nil, false
	}
	return rows, true
}

// readBootID reads the same identity source and validation as ExecutionIdentity
// without requiring the input transport to have negotiated shell v2: a bounded
// process read must still be able to name the boot it observed.
func (d *Device) readBootID(ctx context.Context) (string, bool) {
	result, err := d.shellWith(ctx, d.processTimeout(), "cat /proc/sys/kernel/random/boot_id")
	if err != nil || result.ExitCode != 0 {
		return "", false
	}
	bootID := strings.ToLower(strings.TrimSpace(string(result.Stdout)))
	if !bootIDPattern.MatchString(bootID) {
		return "", false
	}
	return bootID, true
}

// startTicksScript prints one `stat <pid> <raw /proc/<pid>/stat>` record per PID.
// The raw line is parsed on the host because the comm field may contain spaces
// and parentheses; only the numeric fields are ever used.
func startTicksScript(pids []int) string {
	var builder strings.Builder
	builder.WriteString("for p in")
	for _, pid := range pids {
		builder.WriteString(" ")
		builder.WriteString(strconv.Itoa(pid))
	}
	builder.WriteString("; do if [ -r /proc/$p/stat ]; then printf 'stat %s ' \"$p\"; " +
		"cat /proc/$p/stat; else printf 'stat %s -\\n' \"$p\"; fi; done")
	return builder.String()
}

// readTicksBatch runs the two-pass start-tick read and returns every record it
// could parse. ok is false only when no pass completed, so an absent PID means
// "the exact instance has no readable stat" rather than "the device was never
// read".
func (d *Device) readTicksBatch(ctx context.Context, pids []int) (map[int]uint64, bool) {
	if d == nil {
		return nil, false
	}
	if len(pids) == 0 {
		return map[int]uint64{}, true
	}
	if len(pids) > maxProcessInstances {
		return nil, false
	}
	ticks := make(map[int]uint64, len(pids))
	answered := false
	if output, ok := d.runProcScript(ctx, startTicksScript(pids), false); ok {
		answered = true
		for pid, value := range parseStartTicksOutput(output) {
			ticks[pid] = value
		}
	}
	if missing := missingPIDs(pids, ticks); len(missing) > 0 {
		if output, ok := d.runProcScript(ctx, startTicksScript(missing), true); ok {
			answered = true
			for pid, value := range parseStartTicksOutput(output) {
				ticks[pid] = value
			}
		}
	}
	if !answered {
		return nil, false
	}
	return ticks, true
}

// readStartTicks reads the start ticks of every requested PID; one unreadable
// PID fails the whole inventory. A PID without ticks is never a zero value.
func (d *Device) readStartTicks(ctx context.Context, pids []int) (map[int]uint64, bool) {
	ticks, ok := d.readTicksBatch(ctx, pids)
	if !ok {
		return nil, false
	}
	if len(missingPIDs(pids, ticks)) > 0 {
		return nil, false
	}
	return ticks, true
}

// missingPIDs returns the requested PIDs without a recorded start tick.
func missingPIDs(pids []int, ticks map[int]uint64) []int {
	missing := make([]int, 0, len(pids))
	for _, pid := range pids {
		if _, present := ticks[pid]; !present {
			missing = append(missing, pid)
		}
	}
	sort.Ints(missing)
	return missing
}

// runProcScript runs one fixed /proc read script directly or through the existing
// root path. Output stays device data and is never logged.
func (d *Device) runProcScript(ctx context.Context, script string, root bool) (string, bool) {
	if root {
		script = "su -c " + shellQuote(script)
	}
	result, err := d.shellWith(ctx, d.processTimeout(), script)
	if err != nil || result.ExitCode != 0 {
		return "", false
	}
	return string(result.Stdout), true
}

// parseStartTicksOutput reads the `stat <pid> <raw>` records of one batch.
func parseStartTicksOutput(output string) map[int]uint64 {
	ticks := make(map[int]uint64)
	for _, line := range strings.Split(output, "\n") {
		rest, found := strings.CutPrefix(strings.TrimRight(line, "\r"), "stat ")
		if !found {
			continue
		}
		pidText, statLine, found := strings.Cut(rest, " ")
		if !found {
			continue
		}
		pid, err := strconv.Atoi(pidText)
		if err != nil || pid <= 0 {
			continue
		}
		value, ok := parseStartTicks(statLine)
		if !ok {
			continue
		}
		ticks[pid] = value
	}
	return ticks
}

// parseStartTicks reads field 22 (starttime) of one /proc/<pid>/stat line. comm
// is enclosed in parentheses and may contain spaces, so the fields are counted
// from the last ')'; a malformed or truncated line is not a zero tick.
func parseStartTicks(statLine string) (uint64, bool) {
	closing := strings.LastIndexByte(statLine, ')')
	if closing < 0 {
		return 0, false
	}
	fields := strings.Fields(statLine[closing+1:])
	if len(fields) <= startTimeFieldOffset {
		return 0, false
	}
	ticks, err := strconv.ParseUint(fields[startTimeFieldOffset], 10, 64)
	if err != nil || ticks == 0 {
		return 0, false
	}
	return ticks, true
}

// processIdentityScript prints the comm and real UID of one PID as two lines.
// The comm is truncated by the kernel, so it is only used to prove that the exact
// instance is still an application process.
func processIdentityScript(pid int) string {
	path := "/proc/" + strconv.Itoa(pid) + "/status"
	return "if [ -r " + path + " ]; then " +
		"grep '^Name:' " + path + " | head -n 1 | cut -f2; " +
		"grep '^Uid:' " + path + " | head -n 1 | cut -f2; " +
		"else echo -; echo -; fi"
}

// readProcessIdentity re-reads the live comm and real UID of one PID.
func (d *Device) readProcessIdentity(ctx context.Context, pid int) (string, int, bool) {
	script := processIdentityScript(pid)
	output, ok := d.runProcScript(ctx, script, false)
	if !ok {
		output, ok = d.runProcScript(ctx, script, true)
	}
	if !ok {
		return "", 0, false
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return "", 0, false
	}
	name := strings.TrimSpace(lines[0])
	uid, err := strconv.Atoi(strings.TrimSpace(lines[1]))
	if err != nil || name == "" || name == "-" {
		return "", 0, false
	}
	return name, uid, true
}

// KillProcess sends one exact `kill -9` to a verified application process.
//
// The live instance is revalidated first: its start ticks must still equal the
// recorded identity (a reused PID has a different start time) and the live record
// must still be an application process with a package-shaped comm. The frozen
// signature carries only PID and start ticks, so the package/UID binding itself
// belongs to the enumeration that produced the PID; this primitive refuses rather
// than guessing. A refused identity or an unprovable read never signals.
func (d *Device) KillProcess(ctx context.Context, pid int, startTicks uint64) (
	device.DispatchReceipt, error,
) {
	if d == nil {
		return device.DispatchNotDispatched, errNilDevice
	}
	if pid <= 0 || startTicks == 0 {
		return device.DispatchNotDispatched,
			newErr(CodeInvalidArgs, "invalid process identity", nil)
	}
	if err := contextError(ctx); err != nil {
		return device.DispatchNotDispatched, err
	}
	started := time.Now()
	refuse := func(reason, message string) (device.DispatchReceipt, error) {
		d.logEvent(eventlog.Warn, "android.process.kill_refused",
			eventlog.Str("state", "refused"),
			eventlog.Str("reason", reason),
			eventlog.Duration("ms", time.Since(started)))
		return device.DispatchNotDispatched, newErr(CodeAvailability, message, nil)
	}

	ticks, ok := d.readTicksBatch(ctx, []int{pid})
	if !ok {
		return refuse("identity_unreadable", "the process identity could not be revalidated")
	}
	live, present := ticks[pid]
	if !present {
		return refuse("instance_gone", "the process instance is gone")
	}
	if live != startTicks {
		return refuse("identity_changed", "the process identity changed")
	}
	name, uid, ok := d.readProcessIdentity(ctx, pid)
	if !ok {
		return refuse("identity_unreadable", "the process identity could not be revalidated")
	}
	if uid < applicationUIDMin || uid >= applicationUIDMax || !processNamePattern.MatchString(name) {
		return refuse("not_application", "the process is not an application process")
	}
	shell, receipt, err := d.runShellV2(ctx, processNameTimeout,
		"su -c "+shellQuote("kill -9 "+strconv.Itoa(pid)))
	if err != nil {
		d.logKill(receipt, started)
		return receipt, err
	}
	if receipt != device.DispatchTerminated || shell.ExitCode != 0 {
		// The command boundary never proved the signal was delivered.
		d.logKill(device.DispatchUnknown, started)
		return device.DispatchUnknown,
			newErr(CodeAvailability, "the process termination was not confirmed", nil)
	}
	d.logKill(receipt, started)
	return receipt, nil
}

// logKill records the one-shot kill boundary with whitelisted fields only.
func (d *Device) logKill(receipt device.DispatchReceipt, started time.Time) {
	d.logEvent(eventlog.Info, "android.process.kill",
		eventlog.Str("state", receiptName(receipt)),
		eventlog.Duration("ms", time.Since(started)))
}

// mediaSessionHeaders are the fixed header tokens of a readable
// `dumpsys media_session` dump.
var mediaSessionHeaders = []string{
	"MEDIA SESSION SERVICE", "MediaSessionService", "MediaSessionStack", "Sessions Stack",
}

// playingStatePattern matches a PLAYING PlaybackState record (state 3).
var playingStatePattern = regexp.MustCompile(
	`(?i)PlaybackState\s*\{\s*state\s*=\s*(?:PLAYING\b|3\b)`,
)

// MediaPlayback reads the device-wide playback fact used by the kill guard.
// Known is true only when the dump is recognizable; Playing is true only when a
// session record is actually PLAYING.
func (d *Device) MediaPlayback(ctx context.Context) (device.MediaState, error) {
	if d == nil {
		return device.MediaState{}, errNilDevice
	}
	if err := contextError(ctx); err != nil {
		return device.MediaState{}, err
	}
	started := time.Now()
	text, ok, err := d.readCommand(ctx, "dumpsys media_session", d.processTimeout())
	if err != nil || !ok {
		d.logEvent(eventlog.Debug, "android.media.read",
			eventlog.Str("state", "unknown"),
			eventlog.Duration("ms", time.Since(started)))
		return device.MediaState{}, nil
	}
	state := parseMediaPlayback(text)
	d.logEvent(eventlog.Debug, "android.media.read",
		eventlog.Str("state", mediaStateToken(state)),
		eventlog.Duration("ms", time.Since(started)))
	return state, nil
}

// mediaStateToken renders one media fact as a controlled token.
func mediaStateToken(state device.MediaState) string {
	switch {
	case !state.Known:
		return "unknown"
	case state.Playing:
		return "known_playing"
	default:
		return "known_idle"
	}
}

// parseMediaPlayback keeps an unrecognized dump unknown instead of reporting a
// silent device.
func parseMediaPlayback(output string) device.MediaState {
	recognized := false
	for _, header := range mediaSessionHeaders {
		if strings.Contains(output, header) {
			recognized = true
			break
		}
	}
	if !recognized {
		return device.MediaState{}
	}
	return device.MediaState{Known: true, Playing: playingStatePattern.MatchString(output)}
}

// serviceRecordStart opens one `dumpsys activity services` ServiceRecord.
var serviceRecordStart = regexp.MustCompile(`(?m)^\s*\*?\s*ServiceRecord\{`)

// serviceRecordPackage reads the explicit package of one ServiceRecord.
var serviceRecordPackage = regexp.MustCompile(`(?m)^\s*packageName=(\S+)\s*$`)

// serviceRecordComponent reads the component of one ServiceRecord header.
var serviceRecordComponent = regexp.MustCompile(
	`ServiceRecord\{[^\n]*?\s([A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)+)/[A-Za-z0-9_.$]+[^\n]*\}`,
)

// foregroundServicePattern matches a record marked as a foreground service: the
// explicit flag or a live foreground notification id.
var foregroundServicePattern = regexp.MustCompile(
	`\bisForeground=true\b|\bforegroundId=[1-9][0-9]*\b`,
)

// ServiceDumpHeaders are the fixed header tokens of a readable
// `dumpsys activity services` dump.
var serviceDumpHeaders = []string{
	"ACTIVITY MANAGER SERVICES", "active services:", "ServiceRecord{",
}

// ForegroundService reads whether the exact package currently runs a foreground
// service. A readable dump without a match is a proven absence; an unreadable or
// unrecognized dump stays unknown.
func (d *Device) ForegroundService(ctx context.Context, pkg string) (device.ServiceState, error) {
	if d == nil {
		return device.ServiceState{}, errNilDevice
	}
	if !validPackageName(pkg) {
		return device.ServiceState{}, newErr(CodeInvalidArgs, "invalid package name", nil)
	}
	if err := contextError(ctx); err != nil {
		return device.ServiceState{}, err
	}
	started := time.Now()
	text, ok, err := d.readCommand(ctx, "dumpsys activity services", d.processTimeout())
	if err != nil || !ok {
		d.logEvent(eventlog.Debug, "android.service.read",
			eventlog.Str("state", "unknown"),
			eventlog.Duration("ms", time.Since(started)))
		return device.ServiceState{}, nil
	}
	state := parseForegroundService(text, pkg)
	d.logEvent(eventlog.Debug, "android.service.read",
		eventlog.Str("state", serviceStateToken(state)),
		eventlog.Duration("ms", time.Since(started)))
	return state, nil
}

// serviceStateToken renders one service fact as a controlled token.
func serviceStateToken(state device.ServiceState) string {
	switch {
	case !state.Known:
		return "unknown"
	case state.Foreground:
		return "known_foreground"
	default:
		return "known_background"
	}
}

// parseForegroundService reads the matching ServiceRecord block of one package.
func parseForegroundService(output, pkg string) device.ServiceState {
	recognized := false
	for _, header := range serviceDumpHeaders {
		if strings.Contains(output, header) {
			recognized = true
			break
		}
	}
	if !recognized {
		return device.ServiceState{}
	}
	starts := serviceRecordStart.FindAllStringIndex(output, -1)
	for index, start := range starts {
		end := len(output)
		if index+1 < len(starts) {
			end = starts[index+1][0]
		}
		record := output[start[0]:end]
		if servicePackageOf(record) != pkg {
			continue
		}
		if foregroundServicePattern.MatchString(record) {
			return device.ServiceState{Known: true, Foreground: true}
		}
	}
	return device.ServiceState{Known: true}
}

// servicePackageOf reads the package of one ServiceRecord block, preferring the
// explicit packageName line over the header component.
func servicePackageOf(record string) string {
	if match := serviceRecordPackage.FindStringSubmatch(record); match != nil {
		return match[1]
	}
	if match := serviceRecordComponent.FindStringSubmatch(record); match != nil {
		return match[1]
	}
	return ""
}
