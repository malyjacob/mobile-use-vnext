package android

import (
	"strings"
	"testing"
)

// The identity fixtures reproduce the measured `dumpsys package` shape of this
// phone's Android 16: the record lives in the `Packages:` section, the app id is
// a record field, and the per-user install facts sit in a deeper-indented
// `User <id>:` block. Only placeholder packages appear.

func TestPackageIdentityReadsInstalledIdentity(t *testing.T) {
	d := newStubDevice(t, "package-identity")
	got, err := d.PackageIdentity(t.Context(), "com.example.app", 0)
	if err != nil {
		t.Fatalf("PackageIdentity: %v", err)
	}
	if !got.Known {
		t.Fatalf("identity not known: %+v", got)
	}
	if got.Package != "com.example.app" || got.AndroidUserID != 0 {
		t.Fatalf("package=%q user=%d", got.Package, got.AndroidUserID)
	}
	if got.AppID != 10388 || got.AppUID != 10388 {
		t.Fatalf("app id=%d app uid=%d", got.AppID, got.AppUID)
	}
	if got.FirstInstallTime != "2026-11-09 03:45:47" || got.SignatureHash != "fc909ae5" {
		t.Fatalf("install time=%q signature=%q", got.FirstInstallTime, got.SignatureHash)
	}
}

// TestPackageIdentityCrossUser proves the full uid is user-scoped: the same
// package has a different full uid under user 10 while the application id and
// the identity fields stay the same.
func TestPackageIdentityCrossUser(t *testing.T) {
	d := newStubDevice(t, "package-identity-user10")
	got, err := d.PackageIdentity(t.Context(), "com.example.app", 10)
	if err != nil {
		t.Fatalf("PackageIdentity: %v", err)
	}
	if !got.Known || got.AndroidUserID != 10 || got.AppID != 10317 || got.AppUID != 1010317 {
		t.Fatalf("identity=%+v", got)
	}
	// The user-10 block is the second user block of the record; its install time
	// must win over the user-0 one.
	if got.FirstInstallTime != "2026-11-10 10:10:10" {
		t.Fatalf("install time=%q", got.FirstInstallTime)
	}
	if got.SignatureHash != "fc909ae5" {
		t.Fatalf("signature=%q", got.SignatureHash)
	}
}

func TestPackageIdentityUnknownSources(t *testing.T) {
	cases := []struct {
		name       string
		transcript string
		user       int
	}{
		{"empty signature list", "package-identity-empty-signature", 0},
		{"package not installed for the user", "package-identity-not-installed", 0},
		{"ambiguous package record", "package-identity-ambiguous", 0},
		{"per-user uid does not match", "package-identity-uid-mismatch", 10},
		{"requested user has no installed record", "package-identity", 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newStubDevice(t, tc.transcript)
			got, err := d.PackageIdentity(t.Context(), "com.example.app", tc.user)
			if err != nil {
				t.Fatalf("an unreadable identity is a fact, not an error: %v", err)
			}
			if got.Known {
				t.Fatalf("identity must stay unknown: %+v", got)
			}
			if got.AndroidUserID != tc.user || got.AppID != 0 || got.AppUID != 0 ||
				got.SignatureHash != "" || got.FirstInstallTime != "" {
				t.Fatalf("a partial identity must not be assembled: %+v", got)
			}
		})
	}
}

func TestPackageIdentityRejectsInvalidArguments(t *testing.T) {
	d := newStubDevice(t, "package-identity")
	if _, err := d.PackageIdentity(t.Context(), "com.example app", 0); CodeOf(err) != CodeInvalidArgs {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	if _, err := d.PackageIdentity(t.Context(), "com.example.app", -1); CodeOf(err) != CodeInvalidArgs {
		t.Fatalf("code=%q err=%v", CodeOf(err), err)
	}
	var nilDevice *Device
	if _, err := nilDevice.PackageIdentity(t.Context(), "com.example.app", 0); CodeOf(err) != CodeInvalidArgs {
		t.Fatalf("nil device code=%q", CodeOf(err))
	}
}

// TestParsePackageRecord covers the section boundary, record count, user block,
// application id range and install-fact rules.
func TestParsePackageRecord(t *testing.T) {
	standard := dumpsysPackageFixture("com.example.app", 10388, "fc909ae5", nil)
	multiUser := dumpsysPackageFixture("com.example.app", 10317, "fc909ae5",
		[]fakeInstalledUser{
			{ID: 0, Installed: true, FirstInstall: "2026-08-04 19:53:47"},
			{ID: 10, Installed: true, FirstInstall: "2026-11-10 10:10:10"},
		})
	noAppID := strings.Replace(standard, "    appId=10388\n", "", 1)
	systemAppID := dumpsysPackageFixture("com.example.app", 1000, "fc909ae5", nil)
	noCurrentSignature := "Packages:\n" +
		"  Package [com.example.app] (f07413c):\n" +
		"    appId=10388\n" +
		"    signatures=PackageSignatures{5346d19 version:3, past signatures:[8f53736c]}\n" +
		"    User 0: ceDataInode=1 installed=true hidden=false\n" +
		"      firstInstallTime=2026-11-09 03:45:47\n"
	duplicateUsers := dumpsysPackageFixture("com.example.app", 10388, "fc909ae5",
		[]fakeInstalledUser{
			{ID: 0, Installed: true, FirstInstall: "2026-11-09 03:45:47"},
			{ID: 0, Installed: true, FirstInstall: "2026-11-09 03:45:47"},
		})
	multipleInstallTimes := strings.Replace(dumpsysPackageFixture("com.example.app", 10388,
		"fc909ae5", nil), "      uninstallReason=0\n",
		"      firstInstallTime=2027-01-01 00:00:00\n      uninstallReason=0\n", 1)
	emptyInstallTime := strings.Replace(standard,
		"      firstInstallTime=2026-11-09 03:45:47\n", "      firstInstallTime=\n", 1)
	hiddenOnly := "Packages:\n" +
		"Hidden system packages:\n" +
		"  Package [com.example.app] (f07413c):\n" +
		"    appId=10388\n"
	cases := []struct {
		name         string
		dump         string
		user         int
		ok           bool
		appID        int
		signature    string
		hasUser      bool
		installed    bool
		firstInstall string
	}{
		{name: "installed user", dump: standard, ok: true, appID: 10388,
			signature: "fc909ae5", hasUser: true, installed: true,
			firstInstall: "2026-11-09 03:45:47"},
		{name: "target user block wins", dump: multiUser, user: 10, ok: true,
			appID: 10317, signature: "fc909ae5", hasUser: true, installed: true,
			firstInstall: "2026-11-10 10:10:10"},
		{name: "user zero of a multi-user record", dump: multiUser, ok: true,
			appID: 10317, signature: "fc909ae5", hasUser: true, installed: true,
			firstInstall: "2026-08-04 19:53:47"},
		{name: "user not present", dump: multiUser, user: 99, ok: true,
			appID: 10317, signature: "fc909ae5"},
		{name: "no packages section", dump: "Queries:\n  com.example.app:\n    appId=10388\n",
			ok: false},
		{name: "record for another package is not the target",
			dump: dumpsysPackageFixture("com.example.other", 10388, "fc909ae5", nil), ok: false},
		{name: "app id outside the application range", dump: systemAppID, ok: true,
			signature: "fc909ae5", hasUser: true, installed: true,
			firstInstall: "2026-11-09 03:45:47"},
		{name: "missing app id", dump: noAppID, ok: true,
			signature: "fc909ae5", hasUser: true, installed: true,
			firstInstall: "2026-11-09 03:45:47"},
		{name: "only past signatures", dump: noCurrentSignature, ok: true, appID: 10388,
			hasUser: true, installed: true, firstInstall: "2026-11-09 03:45:47"},
		{name: "duplicate user blocks are ambiguous", dump: duplicateUsers, ok: true,
			appID: 10388, signature: "fc909ae5"},
		{name: "duplicate install times are ambiguous", dump: multipleInstallTimes,
			ok: true, appID: 10388, signature: "fc909ae5", hasUser: true, installed: true},
		{name: "empty install time is not a fact", dump: emptyInstallTime, ok: true,
			appID: 10388, signature: "fc909ae5", hasUser: true, installed: true},
		{name: "record only in a later section", dump: hiddenOnly, ok: false},
		{name: "empty dump", dump: "", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parsePackageRecord(tc.dump, "com.example.app", tc.user)
			if ok != tc.ok {
				t.Fatalf("ok=%v want=%v (record=%+v)", ok, tc.ok, got)
			}
			if !ok {
				if got != (packageIdentityDump{}) {
					t.Fatalf("a failed parse must not return partial evidence: %+v", got)
				}
				return
			}
			if got.appID != tc.appID || got.signatureHash != tc.signature ||
				got.hasUserBlock != tc.hasUser || got.userInstalled != tc.installed ||
				got.firstInstallTime != tc.firstInstall {
				t.Fatalf("record=%+v want appID=%d signature=%q user=%v installed=%v time=%q",
					got, tc.appID, tc.signature, tc.hasUser, tc.installed, tc.firstInstall)
			}
		})
	}
}

// TestParsePackageRecordIgnoresQueriesSection proves the repeating `Queries:`
// section can never supply an app id for a record that lacks one.
func TestParsePackageRecordIgnoresQueriesSection(t *testing.T) {
	dump := strings.Replace(dumpsysPackageFixture("com.example.app", 10388, "fc909ae5", nil),
		"    appId=10388\n", "", 1)
	got, ok := parsePackageRecord(dump, "com.example.app", 0)
	if !ok {
		t.Fatalf("the Packages record must still be found: %+v", got)
	}
	if got.appID != 0 {
		t.Fatalf("app id leaked from the Queries section: %+v", got)
	}
}

// TestPackageIdentityTracksReinstall proves a reinstall surfaces every compared
// field (application id, first install time, signature hash) as a different
// identity, so the engine can revoke a stale close eligibility.
func TestPackageIdentityTracksReinstall(t *testing.T) {
	before, ok := parsePackageRecord(dumpsysPackageFixture("com.example.app", 10317,
		"8f53736c", []fakeInstalledUser{{ID: 0, Installed: true,
			FirstInstall: "2026-08-04 19:53:47"}}), "com.example.app", 0)
	if !ok {
		t.Fatal("the first identity must parse")
	}
	after, ok := parsePackageRecord(dumpsysPackageFixture("com.example.app", 10317,
		"fc909ae5", []fakeInstalledUser{{ID: 0, Installed: true,
			FirstInstall: "2026-11-25 21:05:03"}}), "com.example.app", 0)
	if !ok {
		t.Fatal("the second identity must parse")
	}
	if before.appID != after.appID {
		t.Fatalf("app id changed unexpectedly: %d/%d", before.appID, after.appID)
	}
	if before.firstInstallTime == after.firstInstallTime {
		t.Fatal("a reinstall must surface a new first install time")
	}
	if before.signatureHash == after.signatureHash {
		t.Fatal("a reinstall must surface a new signature hash")
	}
}

func TestParseCurrentSignature(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{
			name: "single current signature",
			line: "    signatures=PackageSignatures{1f5ff68 version:2, signatures:[8f53736c], past signatures:[]}",
			want: "8f53736c",
		},
		{
			name: "first of two current signatures",
			line: "    signatures=PackageSignatures{8867b62 version:3, signatures:[fc909ae5, 6c02dd51], past signatures:[]}",
			want: "fc909ae5",
		},
		{
			name: "empty current list",
			line: "    signatures=PackageSignatures{5346d19 version:3, signatures:[], past signatures:[8f53736c]}",
		},
		{
			name: "only past signatures",
			line: "    signatures=PackageSignatures{5346d19 version:3, past signatures:[8f53736c]}",
		},
		{
			name: "non-hash token",
			line: "    signatures=PackageSignatures{5346d19 version:3, signatures:[not-a-hash], past signatures:[]}",
		},
		{
			name: "missing list",
			line: "    signatures=PackageSignatures{5346d19 version:3}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseCurrentSignature(tc.line); got != tc.want {
				t.Fatalf("signature=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestParsePackageFullUID(t *testing.T) {
	cases := []struct {
		name   string
		output string
		pkg    string
		user   int
		appID  int
		uid    int
		ok     bool
	}{
		{
			name:   "user zero",
			output: "package:com.example.app uid:10388\n",
			pkg:    "com.example.app", user: 0, appID: 10388, uid: 10388, ok: true,
		},
		{
			name:   "user ten",
			output: "package:com.example.app uid:1010317\n",
			pkg:    "com.example.app", user: 10, appID: 10317, uid: 1010317, ok: true,
		},
		{
			name:   "trailing gids",
			output: "package:com.example.app uid:1010317 gids:[3003,9997]\n",
			pkg:    "com.example.app", user: 10, appID: 10317, uid: 1010317, ok: true,
		},
		{
			name:   "user zero uid for a user ten request",
			output: "package:com.example.app uid:10317\n",
			pkg:    "com.example.app", user: 10, appID: 10317,
		},
		{
			name:   "application id mismatch",
			output: "package:com.example.app uid:1010399\n",
			pkg:    "com.example.app", user: 10, appID: 10317,
		},
		{
			name:   "substring package is not the target",
			output: "package:com.example.app.debug uid:1010317\n",
			pkg:    "com.example.app", user: 10, appID: 10317,
		},
		{
			name: "two records are ambiguous",
			output: "package:com.example.app uid:1010317\n" +
				"package:com.example.app uid:1010317\n",
			pkg: "com.example.app", user: 10, appID: 10317,
		},
		{
			name:   "missing uid column",
			output: "package:com.example.app\n",
			pkg:    "com.example.app", user: 0, appID: 10388,
		},
		{
			name:   "malformed uid",
			output: "package:com.example.app uid:x\n",
			pkg:    "com.example.app", user: 0, appID: 10388,
		},
		{
			name:   "system uid",
			output: "package:com.example.app uid:1000\n",
			pkg:    "com.example.app", user: 0, appID: 1000,
		},
		{
			name:   "no record",
			output: "package records unavailable\n",
			pkg:    "com.example.app", user: 0, appID: 10388,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uid, ok := parsePackageFullUID(tc.output, tc.pkg, tc.user, tc.appID)
			if ok != tc.ok || uid != tc.uid {
				t.Fatalf("uid=%d ok=%v want=%d/%v", uid, ok, tc.uid, tc.ok)
			}
		})
	}
}

// TestPackageProcessIdentityIsUserScoped guards the field separation the engine
// relies on: the application id and the full uid are different values for every
// user other than user 0.
func TestPackageProcessIdentityIsUserScoped(t *testing.T) {
	identity, ok := parsePackageRecord(dumpsysPackageFixture("com.example.app", 10317,
		"fc909ae5", []fakeInstalledUser{{ID: 10, Installed: true,
			FirstInstall: "2026-11-10 10:10:10"}}), "com.example.app", 10)
	if !ok || identity.appID != 10317 {
		t.Fatalf("record=%+v ok=%v", identity, ok)
	}
	fullUID, ok := parsePackageFullUID("package:com.example.app uid:1010317\n",
		"com.example.app", 10, identity.appID)
	if !ok || fullUID != 1010317 {
		t.Fatalf("full uid=%d ok=%v", fullUID, ok)
	}
	if fullUID%androidUserUIDRange != 10317 {
		t.Fatalf("the application id must be the full uid modulo the per-user range: %d", fullUID)
	}
}
