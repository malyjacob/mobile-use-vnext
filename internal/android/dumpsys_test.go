package android

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// The dumpsys samples below reproduce the measured shape of `dumpsys activity
// activities`, `dumpsys window displays` and `am stack list` with placeholder
// packages only.

const activityDump = `ACTIVITY MANAGER ACTIVITIES (dumpsys activity activities)
Display #0 (activities from top to bottom):
  * Task{c52b89b #1 type=home U=0 visible=true visibleRequested=true mode=fullscreen translucent=false sz=1}
    * Task{c9eda97 #2 type=home I=com.example.home/.Launcher U=0 rootTaskId=1 visible=true visibleRequested=true mode=fullscreen translucent=false sz=1}
      topResumedActivity=ActivityRecord{127309986 u0 com.example.home/.Launcher t2}
  * Task{a7fe978 #2478 type=standard A=10317:com.example.editor U=0 visible=false visibleRequested=false mode=fullscreen translucent=true sz=2}
Display #35 (activities from top to bottom):
  * Task{bb #2120 type=standard A=10262:com.example.calc U=0 visible=true visibleRequested=true}
    topResumedActivity=ActivityRecord{22 u0 com.example.calc/.CalcActivity t2120}
  * Task{cc #2121 type=standard A=10263:com.example.hidden U=0 visible=false visibleRequested=false}
Display #93 (activities from top to bottom):
  * Task{dd #3000 type=standard A=10264:com.example.other U=0 visible=true}
`

const windowDisplays = "WINDOW MANAGER DISPLAY CONTENTS (dumpsys window displays)\n" +
	"  Display: mDisplayId=0 (organized)\n" +
	"    init=1440x3200 600dpi base=1080x2400 450dpi cur=1080x2400\n" +
	"    mCurrentFocus=Window{61d0ce2 u0 com.example.home/.Launcher}\n" +
	"  Display: mDisplayId=35\n" +
	"    mCurrentFocus=null\n"

func TestParseActivityForegroundVirtualDisplay(t *testing.T) {
	info := parseActivityForeground(activityDump, 35)
	if info.State != device.ForegroundPresent || info.Package != "com.example.calc" {
		t.Fatalf("want present com.example.calc, got %+v", info)
	}
	if info.TaskID == nil || *info.TaskID != 2120 {
		t.Fatalf("task id=%v, want 2120", info.TaskID)
	}
}

func TestParseActivityForegroundFallsBackToVisibleTask(t *testing.T) {
	output := "\nDisplay #35 (activities from top to bottom):\n" +
		"  * Task{aa #2121 type=standard A=10263:com.example.hidden U=0 visible=false}\n" +
		"  * Task{bb #2120 type=standard A=10262:com.example.calc U=0 visible=true}\n"
	info := parseActivityForeground(output, 35)
	if info.State != device.ForegroundPresent || info.Package != "com.example.calc" {
		t.Fatalf("got %+v", info)
	}
	if info.TaskID == nil || *info.TaskID != 2120 {
		t.Fatalf("task id=%v", info.TaskID)
	}
}

func TestParseActivityForegroundMissingDisplayIsUnknown(t *testing.T) {
	info := parseActivityForeground(activityDump, 61)
	if info.State != device.ForegroundUnknown || info.Package != "" || info.TaskID != nil {
		t.Fatalf("missing section must be unknown, got %+v", info)
	}
}

func TestParseActivityForegroundEmptySectionIsNone(t *testing.T) {
	info := parseActivityForeground("\nDisplay #61 (activities from top to bottom):\n", 61)
	if info.State != device.ForegroundNone {
		t.Fatalf("an empty section is confirmed none, got %+v", info)
	}
}

func TestPrimaryForegroundUsesWindowFocus(t *testing.T) {
	// Without the activity dump the package is known but the task is not.
	info := foregroundFromSources(0, windowDisplays, "", true, false)
	if info.State != device.ForegroundPresent || info.Package != "com.example.home" || info.TaskID != nil {
		t.Fatalf("got %+v", info)
	}

	// The activity dump names the same component, so its task id is adopted.
	info = foregroundFromSources(0, windowDisplays, activityDump, true, true)
	if info.State != device.ForegroundPresent || info.TaskID == nil || *info.TaskID != 2 {
		t.Fatalf("agreeing sources must attach the task id: %+v", info)
	}

	// A different top resumed record leaves the task id unknown instead of guessing.
	other := "\nDisplay #0 (activities from top to bottom):\n" +
		"  * Task{zz #9 type=standard U=0 visible=true}\n" +
		"    topResumedActivity=ActivityRecord{1 u0 com.example.dialog/.Dialog t9}\n"
	info = foregroundFromSources(0, windowDisplays, other, true, true)
	if info.State != device.ForegroundPresent || info.Package != "com.example.home" || info.TaskID != nil {
		t.Fatalf("disagreeing sources must not attach a task id: %+v", info)
	}
}

func TestPrimaryForegroundNullFocusIsNone(t *testing.T) {
	listing := "  Display: mDisplayId=0\n    mCurrentFocus=null\n"
	if info := foregroundFromSources(0, listing, "", true, false); info.State != device.ForegroundNone {
		t.Fatalf("null focus must be none, got %+v", info)
	}
}

func TestPrimaryForegroundUnreadableIsUnknown(t *testing.T) {
	if info := foregroundFromSources(0, "", "", false, false); info.State != device.ForegroundUnknown {
		t.Fatalf("unreadable window dump must be unknown, got %+v", info)
	}
	// A readable window dump without any display block is also unknown.
	if info := foregroundFromSources(0, "WINDOW MANAGER DISPLAY CONTENTS\n", "", true, true); info.State != device.ForegroundUnknown {
		t.Fatalf("missing display block must be unknown, got %+v", info)
	}
}

// TestPrimaryForegroundIgnoresVirtualNullFocus proves a virtual display's null
// focus is never read as the primary record.
func TestPrimaryForegroundIgnoresVirtualNullFocus(t *testing.T) {
	listing := "  Display: mDisplayId=0\n" +
		"    mCurrentFocus=Window{abc u0 com.example.home/.Launcher}\n" +
		"  Display: mDisplayId=35\n" +
		"    mCurrentFocus=null\n"
	info := foregroundFromSources(0, listing, "", true, false)
	if info.State != device.ForegroundPresent || info.Package != "com.example.home" {
		t.Fatalf("got %+v", info)
	}
}

func TestNormalizeComponent(t *testing.T) {
	cases := map[string]string{
		"com.example/.Main":       "com.example/com.example.Main",
		"com.example/Main":        "com.example/com.example.Main",
		"com.example/com.ui.Main": "com.example/com.ui.Main",
	}
	for input, want := range cases {
		if got := normalizeComponent(input); got != want {
			t.Fatalf("normalizeComponent(%q)=%q, want %q", input, got, want)
		}
	}
}

const stackListing = `RootTask id=42 bounds=[0,0][1080,2400] displayId=0 userId=0
 configuration={1.0 460mcc1mnc [en_US] ldltr sw384dp w384dp h853dp 450dpi}
  taskId=42: com.example/com.example.Main bounds=[0,0][1080,2400] userId=0 visible=true topActivity=ComponentInfo{com.example/.Main}

RootTask id=43 bounds=[0,0][720,1280] displayId=61 userId=0
  taskId=43: com.example/.Main bounds=[0,0][720,1280] userId=0 visible=false topActivity=ComponentInfo{com.example/com.example.ui.Main}
  taskId=4: unknown bounds=[0,0][1080,2400] userId=0 visible=false
`

func TestParseTasksKeepsDistinctInstances(t *testing.T) {
	tasks := parseTasks(stackListing)
	if len(tasks) != 2 {
		t.Fatalf("want 2 current tasks, got %d: %+v", len(tasks), tasks)
	}
	if tasks[0].TaskID != 42 || tasks[0].RootID != 42 ||
		tasks[0].DisplayID != 0 || tasks[0].Package != "com.example" {
		t.Fatalf("first task=%+v", tasks[0])
	}
	if !tasks[0].Visible || tasks[0].Component != "com.example/com.example.Main" {
		t.Fatalf("first task=%+v", tasks[0])
	}
	if tasks[1].TaskID != 43 || tasks[1].RootID != 43 ||
		tasks[1].DisplayID != 61 || tasks[1].Visible {
		t.Fatalf("second task=%+v", tasks[1])
	}
	if tasks[1].Component != "com.example/com.example.Main" {
		t.Fatalf("component=%q", tasks[1].Component)
	}
}

// TestParseTasksRootID covers the root header forms: a normal header names the
// root, an unreadable root id keeps RootID=0 so the task is never moved, and a
// leaf line outside any root header is not a task at all.
func TestParseTasksRootID(t *testing.T) {
	cases := []struct {
		name    string
		listing string
		want    []device.Task
	}{
		{
			name: "root header",
			listing: "RootTask id=42 bounds=[0,0][1080,2400] displayId=0 userId=0\n" +
				"  taskId=42: com.example/com.example.Main visible=true\n",
			want: []device.Task{{TaskID: 42, RootID: 42, DisplayID: 0,
				Package: "com.example", Component: "com.example/com.example.Main", Visible: true}},
		},
		{
			name: "unreadable root id stays zero",
			listing: "RootTask id=? bounds=[0,0][720,1280] displayId=61 userId=0\n" +
				"  taskId=9: com.example/.Main visible=true\n",
			want: []device.Task{{TaskID: 9, RootID: 0, DisplayID: 61,
				Package: "com.example", Component: "com.example/com.example.Main", Visible: true}},
		},
		{
			name:    "leaf outside a root header",
			listing: "  taskId=7: com.example/.Main visible=true\n",
			want:    []device.Task{},
		},
		{
			name: "root id never carries over",
			listing: "RootTask id=42 bounds=[0,0][1080,2400] displayId=0 userId=0\n" +
				"  taskId=42: com.example/com.example.Main visible=true\n" +
				"RootTask id=? bounds=[0,0][720,1280] displayId=61 userId=0\n" +
				"  taskId=9: com.example/.Main visible=true\n",
			want: []device.Task{
				{TaskID: 42, RootID: 42, DisplayID: 0,
					Package: "com.example", Component: "com.example/com.example.Main", Visible: true},
				{TaskID: 9, RootID: 0, DisplayID: 61,
					Package: "com.example", Component: "com.example/com.example.Main", Visible: true},
			},
		},
		{
			name: "unknown package leaf is skipped",
			listing: "RootTask id=42 bounds=[0,0][1080,2400] displayId=0 userId=0\n" +
				"  taskId=4: unknown bounds=[0,0][1080,2400] userId=0 visible=false\n",
			want: []device.Task{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTasks(tc.listing)
			if len(got) != len(tc.want) {
				t.Fatalf("tasks=%+v want=%+v", got, tc.want)
			}
			for index := range tc.want {
				if got[index] != tc.want[index] {
					t.Fatalf("task %d=%+v want=%+v", index, got[index], tc.want[index])
				}
			}
		})
	}
}

func TestParseTasksIgnoresRecentsAndUnknownLeaves(t *testing.T) {
	output := "ACTIVITY MANAGER RECENT TASKS (dumpsys activity recents)\n" +
		"  Recent #1: Task{aa #77 type=standard}\n" +
		"RootTask id=1 bounds=[0,0][1080,2400] displayId=0 userId=0\n" +
		"  taskId=4: unknown bounds=[0,0][1080,2400] userId=0 visible=false\n"
	if tasks := parseTasks(output); len(tasks) != 0 {
		t.Fatalf("historical recents must not become tasks: %+v", tasks)
	}
	if tasks := parseTasks(""); len(tasks) != 0 {
		t.Fatalf("empty listing: %+v", tasks)
	}
}

func TestParseDisplayIDs(t *testing.T) {
	listing := "Display 4630946540531149187 (HWC display 0): port=131 pnpId=QCM displayName=\"\"\n" +
		"Display 1152921504722368738 (Virtual display): displayName=\"MobileUseAgent\"\n" +
		"Display 22 (Virtual display): displayName=\"scrcpy\"\n"
	ids := parseDisplayIDs(listing)
	primary, ok := ids.primaryID()
	if !ok || primary != 4630946540531149187 {
		t.Fatalf("primary=%d ok=%v", primary, ok)
	}
	virtual, ok := ids.virtualID()
	if !ok || virtual != 1152921504722368738 {
		t.Fatalf("virtual=%d ok=%v", virtual, ok)
	}

	// A second matching virtual layer is ambiguous and never guessed.
	ambiguous := parseDisplayIDs(listing +
		"Display 33 (Virtual display): displayName=\"MobileUseAgent\"\n")
	if _, ok := ambiguous.virtualID(); ok {
		t.Fatal("two virtual layers must not resolve to one id")
	}
	// No HWC display 0 entry means the primary is not present; id 0 is never a
	// fallback SurfaceFlinger id.
	if _, ok := parseDisplayIDs("").primaryID(); ok {
		t.Fatal("empty listing must not resolve a primary id")
	}
}

func TestParsePrimaryDisplayInfo(t *testing.T) {
	sample := "Display Devices:\n" +
		"  mDisplayId=0\n" +
		"    mBaseDisplayInfo=DisplayInfo{\"built-in\", displayId 0, real 1440 x 3200, rotation 0, state ON, type INTERNAL}\n" +
		"    mOverrideDisplayInfo=DisplayInfo{\"built-in\", displayId 0, real 1080 x 2400, app 1080 x 2400, density 450, rotation 0, state ON, type INTERNAL}\n"
	info, ok := parsePrimaryDisplayInfo(sample)
	if !ok || info.width != 1080 || info.height != 2400 || info.rotation != 0 {
		t.Fatalf("info=%+v ok=%v", info, ok)
	}
	if !displayIDPresent(sample, 0) {
		t.Fatal("display 0 must be present in the dump")
	}
	if displayIDPresent(sample, 61) {
		t.Fatal("display 61 must not be present")
	}
	baseOnly := "    mBaseDisplayInfo=DisplayInfo{\"built-in\", displayId 0, real 1440 x 3200, rotation 0, state ON}\n"
	info, ok = parsePrimaryDisplayInfo(baseOnly)
	if !ok || info.width != 1440 || info.height != 3200 {
		t.Fatalf("base info=%+v ok=%v", info, ok)
	}
	sleeping := "    mOverrideDisplayInfo=DisplayInfo{\"built-in\", displayId 0, real 1080 x 2400, rotation 0, state OFF}\n"
	if _, ok := parsePrimaryDisplayInfo(sleeping); ok {
		t.Fatal("a display that is not ON must not be available")
	}
	if _, ok := parsePrimaryDisplayInfo(""); ok {
		t.Fatal("empty dump must not resolve")
	}
}

func TestParseWMSizeAndDensity(t *testing.T) {
	size, ok := parseWMSize("Physical size: 1440x3200\nOverride size: 1080x2400\n")
	if !ok || size.width != 1080 || size.height != 2400 {
		t.Fatalf("size=%+v ok=%v", size, ok)
	}
	size, ok = parseWMSize("Physical size: 1440x3200\n")
	if !ok || size.width != 1440 || size.height != 3200 {
		t.Fatalf("physical fallback=%+v ok=%v", size, ok)
	}
	if _, ok := parseWMSize("garbage\n"); ok {
		t.Fatal("unparseable wm size must fail")
	}
	density, ok := parseWMDensity("Physical density: 600\nOverride density: 450\n")
	if !ok || density != 450 {
		t.Fatalf("density=%d ok=%v", density, ok)
	}
	density, ok = parseWMDensity("Physical density: 600\n")
	if !ok || density != 600 {
		t.Fatalf("physical density=%d ok=%v", density, ok)
	}
	if _, ok := parseWMDensity(""); ok {
		t.Fatal("unparseable wm density must fail")
	}
}

func TestParseControlIdentity(t *testing.T) {
	paths := "package:/data/app/~~abc==/com.mobileuse.control-xyz==/base.apk\n"
	packages := "package:com.mobileuse.control.debug uid:10499\npackage:com.mobileuse.control uid:10354\n"
	apk, uid, err := parseControlIdentity(paths, packages, "com.mobileuse.control")
	if err != nil || apk != "/data/app/~~abc==/com.mobileuse.control-xyz==/base.apk" || uid != 10354 {
		t.Fatalf("apk=%q uid=%d err=%v", apk, uid, err)
	}
	if _, _, err := parseControlIdentity("package:/data/app/x/split.apk\n", packages, "com.mobileuse.control"); err == nil {
		t.Fatal("missing base.apk must be rejected")
	}
	if _, _, err := parseControlIdentity(paths, "package:com.mobileuse.control uid:1000\n", "com.mobileuse.control"); err == nil {
		t.Fatal("a system UID must be rejected")
	}
	if _, _, err := parseControlIdentity(paths, packages+"package:com.mobileuse.control uid:10355\n", "com.mobileuse.control"); err == nil {
		t.Fatal("two UIDs must be rejected")
	}
	if _, _, err := parseControlIdentity(paths, "package:com.mobileuse.control.debug uid:10354\n", "com.mobileuse.control"); err == nil {
		t.Fatal("a substring package must not satisfy the identity filter")
	}
	if _, _, err := parseControlIdentity(paths, "package:"+strings.TrimSpace("")+" uid:x\n", "com.mobileuse.control"); err == nil {
		t.Fatal("a malformed uid must be rejected")
	}
}
