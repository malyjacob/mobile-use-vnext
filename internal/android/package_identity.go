package android

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
)

// Package-manager identity primitives.
//
// `am force-stop --user <id> <pkg>` takes an Android user id, so before any
// package is stopped the adapter must prove three separate package-manager facts:
// which application id the package carries, whether that package is installed for
// the exact user, and which full Linux uid that user's UID list reports. Only a
// record that satisfies all three is returned as known. Anything unreadable or
// ambiguous stays unknown; a partial identity is never assembled.
//
// The adapter reports these as bounded facts only. It does not decide that a
// package may be stopped, and it never logs a package name, raw dump, signature,
// install time, UID or device text: events carry a state token and a reason enum.

const (
	// androidUserUIDRange is the Android per-user UID stride: a full Linux UID is
	// androidUserID*androidUserUIDRange + appID. It is the same 100000 boundary
	// that separates the application UID range from the system UID range.
	androidUserUIDRange = 100000

	// maxSignatureHashLength bounds one accepted signature hash token.
	maxSignatureHashLength = 128
)

var (
	// packageSectionHeader opens the single top-level package inventory section.
	// Every later top-level section ends it, so a record is never taken from
	// `Queries:`, `Dexopt state:` or a hidden-package section.
	packageSectionHeader = regexp.MustCompile(`^Packages:[ \t]*$`)

	// packageRecordHeader opens one `  Package [<name>] (<hex>):` record.
	packageRecordHeader = regexp.MustCompile(`^  Package \[([^\]]+)\] \(`)

	// packageAppID reads the application id of one package record.
	packageAppID = regexp.MustCompile(`^[ \t]+appId=(\d+)[ \t]*$`)

	// packageSignaturesLine opens the signature record of one package record.
	packageSignaturesLine = regexp.MustCompile(`^[ \t]+signatures=`)

	// packageCurrentSignatures reads the current signature list of a signature
	// record. The record prints it before the past-signature list of the same
	// line, and parseCurrentSignature cuts the line before that list.
	packageCurrentSignatures = regexp.MustCompile(`signatures:\[([^\]]*)\]`)

	// packageUserHeader opens one `User <id>:` block of a package record.
	packageUserHeader = regexp.MustCompile(`^([ \t]{2,})User (\d+):(.*)$`)

	// packageUserInstalled reads the installed flag of one user block header.
	packageUserInstalled = regexp.MustCompile(`(?:^|[ \t])installed=(true|false)(?:[ \t]|$)`)

	// packageFirstInstallTime reads the install timestamp line of one user block.
	packageFirstInstallTime = regexp.MustCompile(`^[ \t]+firstInstallTime=(.*)$`)

	// signatureHashPattern accepts one printed signature hash token. Android
	// prints an abbreviated digest; the exact width is not part of the contract,
	// so the token is kept opaque and only its shape is checked.
	signatureHashPattern = regexp.MustCompile(`^[0-9a-fA-F]+$`)
)

// packageIdentityDump is one parsed `dumpsys package` record for one package and
// one Android user. A zero field means the dump did not prove that fact; appID is
// zero unless exactly one in-range appId line was read.
type packageIdentityDump struct {
	appID            int
	signatureHash    string
	hasUserBlock     bool
	userInstalled    bool
	firstInstallTime string
}

// PackageIdentity reads the installed identity of one package for one Android
// user. Known is true only when one package record names the exact package, its
// application id is in the application range, the target user block is installed
// with a readable first install time, the signature list has a current hash and
// the per-user UID list cross-checks that identity.
//
// A read failure, a missing user, an ambiguous record and a mismatching UID are
// all unknown facts rather than errors; the caller decides what an unknown
// identity forbids.
func (d *Device) PackageIdentity(ctx context.Context, pkg string, androidUserID int) (
	device.PackageIdentity, error,
) {
	result := device.PackageIdentity{Package: pkg, AndroidUserID: androidUserID}
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
	fail := func(reason string) (device.PackageIdentity, error) {
		d.logEvent(eventlog.Warn, "android.package.identity",
			eventlog.Str("state", "unknown"),
			eventlog.Str("reason", reason),
			eventlog.Duration("ms", time.Since(started)))
		return result, nil
	}

	dump, ok := d.readPackageDump(ctx, pkg)
	if !ok {
		return fail("package_dump")
	}
	record, ok := parsePackageRecord(dump, pkg, androidUserID)
	if !ok {
		return fail("package_record")
	}
	if record.appID < applicationUIDMin || record.appID >= applicationUIDMax {
		return fail("app_id")
	}
	if !record.hasUserBlock || !record.userInstalled {
		return fail("user_installed")
	}
	if record.firstInstallTime == "" {
		return fail("first_install_time")
	}
	if record.signatureHash == "" {
		return fail("signatures")
	}
	fullUID, ok := d.packageFullUID(ctx, pkg, androidUserID, record.appID)
	if !ok {
		return fail("package_uid")
	}
	result.Known = true
	result.AppID = record.appID
	result.AppUID = fullUID
	result.FirstInstallTime = record.firstInstallTime
	result.SignatureHash = record.signatureHash
	d.logEvent(eventlog.Info, "android.package.identity",
		eventlog.Str("state", "known"),
		eventlog.Duration("ms", time.Since(started)))
	return result, nil
}

// readPackageDump reads the package manager dump of one exact package. The
// output is device data and is never logged or returned through an error.
func (d *Device) readPackageDump(ctx context.Context, pkg string) (string, bool) {
	result, err := d.shellWith(ctx, d.processTimeout(), "dumpsys package "+shellQuote(pkg))
	if err != nil || result.ExitCode != 0 {
		return "", false
	}
	return string(result.Stdout), true
}

// packageFullUID resolves the full Linux UID of one installed package for one
// Android user. The `cmd package` form is preferred; the `pm` wrapper is the
// fallback. A record is accepted only when exactly one line names the exact
// package and its uid cross-checks the user and the application id the dump
// reported.
func (d *Device) packageFullUID(ctx context.Context, pkg string, androidUserID, appID int) (int, bool) {
	for _, command := range []string{
		"cmd package list packages -U --user " + strconv.Itoa(androidUserID) + " " + shellQuote(pkg),
		"pm list packages -U --user " + strconv.Itoa(androidUserID) + " " + shellQuote(pkg),
	} {
		result, err := d.shellWith(ctx, d.processTimeout(), command)
		if err != nil || result.ExitCode != 0 {
			continue
		}
		if uid, ok := parsePackageFullUID(string(result.Stdout), pkg, androidUserID, appID); ok {
			return uid, true
		}
	}
	return 0, false
}

// parsePackageFullUID reads the full UID of one exact package record for one
// Android user. A substring package (for example a `.debug` variant), a second
// record for the same package, a malformed or missing uid token and any
// arithmetic mismatch with the expected user and application id are all refused:
// the uid is the command-side identity, so it is never guessed.
func parsePackageFullUID(output, pkg string, androidUserID, appID int) (int, bool) {
	prefix := "package:" + pkg
	records := make([]string, 0, 1)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimPrefix(line, prefix)
		if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
			// A longer package name that merely starts with the target.
			continue
		}
		records = append(records, rest)
	}
	if len(records) != 1 {
		return 0, false
	}
	match := uidColumnPattern.FindStringSubmatch(records[0])
	if match == nil {
		return 0, false
	}
	fullUID, err := strconv.Atoi(match[1])
	if err != nil || fullUID <= 0 {
		return 0, false
	}
	if fullUID/androidUserUIDRange != androidUserID ||
		fullUID%androidUserUIDRange != appID ||
		appID < applicationUIDMin || appID >= applicationUIDMax {
		return 0, false
	}
	return fullUID, true
}

// parsePackageRecord reads the one package record of the exact package and the
// one user block of the target user out of a `dumpsys package` dump.
//
// Only the `Packages:` top-level section is read: the next top-level section
// ends it, so `Queries:` and the remaining service state can never supply a
// record. Exactly one record header may name the package; zero and several are
// both refused. ok is false only when that record cannot be located.
func parsePackageRecord(dump, pkg string, androidUserID int) (packageIdentityDump, bool) {
	lines := strings.Split(dump, "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], "\r")
	}
	start := -1
	for index, line := range lines {
		if packageSectionHeader.MatchString(line) {
			start = index
			break
		}
	}
	if start < 0 {
		return packageIdentityDump{}, false
	}
	end := len(lines)
	for index := start + 1; index < len(lines); index++ {
		if endsTopLevelSection(lines[index]) {
			end = index
			break
		}
	}
	header := -1
	headers := 0
	for index := start + 1; index < end; index++ {
		match := packageRecordHeader.FindStringSubmatch(lines[index])
		if match == nil || match[1] != pkg {
			continue
		}
		headers++
		if header < 0 {
			header = index
		}
	}
	if headers != 1 {
		return packageIdentityDump{}, false
	}
	recordEnd := end
	for index := header + 1; index < end; index++ {
		if packageRecordHeader.MatchString(lines[index]) {
			recordEnd = index
			break
		}
	}
	var record packageIdentityDump
	appIDs := 0
	appID := 0
	signatureLines := 0
	for index := header + 1; index < recordEnd; index++ {
		line := lines[index]
		if match := packageAppID.FindStringSubmatch(line); match != nil {
			appIDs++
			if value, err := strconv.Atoi(match[1]); err == nil {
				appID = value
			}
		}
		if packageSignaturesLine.MatchString(line) {
			signatureLines++
			if signatureLines == 1 {
				record.signatureHash = parseCurrentSignature(line)
			}
		}
	}
	if appIDs == 1 && appID >= applicationUIDMin && appID < applicationUIDMax {
		record.appID = appID
	}
	if signatureLines != 1 {
		record.signatureHash = ""
	}
	record.hasUserBlock, record.userInstalled, record.firstInstallTime =
		parsePackageUserBlock(lines, header, recordEnd, androidUserID)
	return record, true
}

// endsTopLevelSection reports whether one line starts the next top-level dump
// section. Section bodies are indented on this device, so an unindented
// non-empty line always ends the package inventory.
func endsTopLevelSection(line string) bool {
	if line == "" {
		return false
	}
	return line[0] != ' ' && line[0] != '\t'
}

// parseCurrentSignature reads the first non-empty hash of the current signature
// list. The past-signature list later on the same line has the same
// `signatures:[...]` shape, so the line is cut before it: a record that only
// carries past signatures proves no current signature.
func parseCurrentSignature(line string) string {
	if index := strings.Index(line, "past signatures"); index >= 0 {
		line = line[:index]
	}
	match := packageCurrentSignatures.FindStringSubmatch(line)
	if match == nil {
		return ""
	}
	for _, token := range strings.Split(match[1], ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if len(token) > maxSignatureHashLength || !signatureHashPattern.MatchString(token) {
			return ""
		}
		return token
	}
	return ""
}

// parsePackageUserBlock reads the target user block: exactly one header for that
// user, installed=true on it, and exactly one non-empty first install time among
// its deeper-indented lines. Several headers for the same user are ambiguous and
// therefore not proven.
func parsePackageUserBlock(lines []string, header, recordEnd, androidUserID int) (bool, bool, string) {
	block := -1
	blocks := 0
	for index := header + 1; index < recordEnd; index++ {
		match := packageUserHeader.FindStringSubmatch(lines[index])
		if match == nil {
			continue
		}
		user, err := strconv.Atoi(match[2])
		if err != nil || user != androidUserID {
			continue
		}
		blocks++
		if block < 0 {
			block = index
		}
	}
	if blocks != 1 {
		return false, false, ""
	}
	userLine := lines[block]
	match := packageUserHeader.FindStringSubmatch(userLine)
	indent := len(match[1])
	installedMatch := packageUserInstalled.FindStringSubmatch(match[3])
	installed := installedMatch != nil && installedMatch[1] == "true"
	time := ""
	times := 0
	for index := block + 1; index < recordEnd; index++ {
		line := lines[index]
		if line == "" {
			continue
		}
		if packageUserHeader.MatchString(line) || lineIndent(line) <= indent {
			break
		}
		stamp := packageFirstInstallTime.FindStringSubmatch(line)
		if stamp == nil {
			continue
		}
		value := strings.TrimSpace(stamp[1])
		if value == "" {
			continue
		}
		times++
		if times == 1 {
			time = value
		}
	}
	if times != 1 {
		time = ""
	}
	return true, installed, time
}

// lineIndent returns the leading whitespace width of one dump line.
func lineIndent(line string) int {
	indent := 0
	for indent < len(line) && (line[indent] == ' ' || line[indent] == '\t') {
		indent++
	}
	return indent
}
