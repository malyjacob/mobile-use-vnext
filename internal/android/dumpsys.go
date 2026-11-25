package android

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// Pattern set ported from the Python reference parsers. They intentionally stay
// narrow: an unparsed record is unknown evidence, never a guessed foreground.

// componentPattern matches `package/Class` activity components only.
const componentPattern = `[A-Za-z0-9_$.]+/[A-Za-z0-9_$.]+`

var (
	boundsPattern = regexp.MustCompile(`^\[(-?\d+),(-?\d+)\]\[(-?\d+),(-?\d+)\]$`)

	activityDisplayHeader = regexp.MustCompile(`(?m)^Display #(\d+) \(activities from top to bottom\):[ \t]*\r?$`)

	topResumed = regexp.MustCompile(
		`(?m)^\s*topResumedActivity=ActivityRecord\{[^\n]*?\s(` + componentPattern + `)\s+t(\d+)\}`,
	)

	visibleTask = regexp.MustCompile(
		`(?m)^  \* Task\{[^\n]*?#(\d+)[^\n]*?\bA=\d+:([A-Za-z0-9_.]+)[^\n]*?\bvisible=true\b`,
	)

	primaryFocus = regexp.MustCompile(
		`mCurrentFocus=Window\{\S+ u\d+ (` + componentPattern + `)\}`,
	)

	primaryDisplayHeader = regexp.MustCompile(`^  Display: mDisplayId=(\d+)(?:\s|$)`)

	realSizePattern = regexp.MustCompile(`\breal (\d+) x (\d+)`)

	rotationPattern = regexp.MustCompile(`\brotation (\d+), state ON,`)

	windowHeader = regexp.MustCompile(`^  Window #\d+ Window\{[^\r\n]*\}:[ \t]*\r?$`)

	windowDisplayID = regexp.MustCompile(`^[ \t]+mDisplayId=(\d+)\b`)

	windowOwner = regexp.MustCompile(`^[ \t]+mOwnerUid=\d+[^\r\n]*\bpackage=([^\s]+)`)

	windowVisible = regexp.MustCompile(`^[ \t]+isVisible=true[ \t]*\r?$`)
)

// unknownForeground is the honest "no evidence" record.
func unknownForeground(reason string) device.Foreground {
	return device.Foreground{State: device.ForegroundUnknown, Reason: reason}
}

// noneForeground is the confirmed "no application in front" record.
func noneForeground() device.Foreground {
	return device.Foreground{State: device.ForegroundNone}
}

// presentForeground keeps package and optional task id.
func presentForeground(pkg string, taskID *int) device.Foreground {
	return device.Foreground{State: device.ForegroundPresent, Package: pkg, TaskID: taskID}
}

// foregroundFromSources combines the primary window focus record with the
// activity manager's per-display records.
//
// Display 0 has an input focus window, so `dumpsys window displays` names the
// focused app even when the activity manager cannot; the activity dump supplies
// the task id only when both sources name the same component. A virtual display
// has no input focus, so only the activity dump is used.
func foregroundFromSources(displayID int, windowListing, activities string, haveWindow, haveActivities bool) device.Foreground {
	if displayID != 0 {
		if !haveActivities {
			return unknownForeground("activities_unreadable")
		}
		return parseActivityForeground(activities, displayID)
	}
	parsed := parsePrimaryFocus(windowListing)
	focus, focusKnown := parsed.record, parsed.known
	if !focusKnown {
		if !haveWindow {
			return unknownForeground("window_unreadable")
		}
		if !haveActivities {
			return unknownForeground("activities_unreadable")
		}
		// The window inventory was readable but carries no focus record: confirm
		// that the primary display has no foreground application.
		section, ok := activityDisplaySection(activities, 0)
		if !ok {
			return unknownForeground("display_section_missing")
		}
		if visibleTask.MatchString(section) {
			return unknownForeground("focus_missing")
		}
		return noneForeground()
	}
	if focus.component == "" {
		// mCurrentFocus is present but null: nothing owns input focus.
		return noneForeground()
	}
	pkg := packageOf(focus.component)
	if !haveActivities {
		return presentForeground(pkg, nil)
	}
	section, ok := activityDisplaySection(activities, 0)
	if !ok {
		return presentForeground(pkg, nil)
	}
	resumed := topResumed.FindStringSubmatch(section)
	if resumed == nil {
		return presentForeground(pkg, nil)
	}
	if normalizeComponent(resumed[1]) != normalizeComponent(focus.component) {
		// A different top resumed record owns the task; the id would be a guess.
		return presentForeground(pkg, nil)
	}
	taskID, err := strconv.Atoi(resumed[2])
	if err != nil {
		return presentForeground(pkg, nil)
	}
	return presentForeground(pkg, &taskID)
}

// focusRecord is the parsed mCurrentFocus window record.
type focusRecord struct {
	component string // "" when the record is null
}

// focusParse is one parse of the primary window-focus section.
type focusParse struct {
	record focusRecord
	known  bool // the display 0 block was found and read
}

// parsePrimaryFocus returns the display 0 focus record. known is false when the
// window listing has no display 0 block at all.
//
// Only display 0's own block is read, so a virtual display's null focus can
// never be mistaken for the primary record.
func parsePrimaryFocus(listing string) focusParse {
	if strings.TrimSpace(listing) == "" {
		return focusParse{}
	}
	lines := strings.Split(listing, "\n")
	for index, line := range lines {
		header := primaryDisplayHeader.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if header == nil || header[1] != "0" {
			continue
		}
		for _, follow := range lines[index+1:] {
			follow = strings.TrimRight(follow, "\r")
			if primaryDisplayHeader.MatchString(follow) {
				break
			}
			if strings.Contains(follow, "mCurrentFocus=null") {
				return focusParse{known: true}
			}
			if match := primaryFocus.FindStringSubmatch(follow); match != nil {
				return focusParse{
					record: focusRecord{component: normalizeComponent(match[1])},
					known:  true,
				}
			}
		}
		return focusParse{known: true}
	}
	return focusParse{}
}

// parseActivityForeground reads one non-primary display's activity section.
// It prefers topResumedActivity and falls back to the first visible top-level
// task; a missing section or record is unknown evidence.
func parseActivityForeground(output string, displayID int) device.Foreground {
	section, ok := activityDisplaySection(output, displayID)
	if !ok {
		return unknownForeground("display_section_missing")
	}
	if resumed := topResumed.FindStringSubmatch(section); resumed != nil {
		taskID, err := strconv.Atoi(resumed[2])
		if err != nil {
			return unknownForeground("task_id_unparseable")
		}
		return presentForeground(packageOf(normalizeComponent(resumed[1])), &taskID)
	}
	if task := visibleTask.FindStringSubmatch(section); task != nil {
		taskID, err := strconv.Atoi(task[1])
		if err != nil {
			return unknownForeground("task_id_unparseable")
		}
		return presentForeground(task[2], &taskID)
	}
	return noneForeground()
}

// activityDisplaySection returns the text of one `Display #N` section.
func activityDisplaySection(output string, displayID int) (string, bool) {
	headers := activityDisplayHeader.FindAllStringSubmatchIndex(output, -1)
	for index, header := range headers {
		id, err := strconv.Atoi(output[header[2]:header[3]])
		if err != nil || id != displayID {
			continue
		}
		end := len(output)
		if index+1 < len(headers) {
			end = headers[index+1][0]
		}
		return output[header[1]:end], true
	}
	return "", false
}

// packageOf splits a normalized component.
func packageOf(component string) string {
	if index := strings.IndexByte(component, '/'); index >= 0 {
		return component[:index]
	}
	return ""
}

// normalizeComponent expands Android's package-relative Activity shorthand so
// the window and activity records can be compared exactly.
func normalizeComponent(component string) string {
	slash := strings.IndexByte(component, '/')
	if slash < 0 {
		return component
	}
	pkg, class := component[:slash], component[slash+1:]
	switch {
	case strings.HasPrefix(class, "."):
		class = pkg + class
	case !strings.Contains(class, "."):
		class = pkg + "." + class
	}
	return pkg + "/" + class
}

// rootTaskPrefix opens one `am stack list` root task.
var rootTaskPrefix = regexp.MustCompile(`^RootTask id=`)

// rootTaskID reads the root task id token of one header. A header whose id is
// missing or unparseable still opens a container; its leaf tasks then carry
// RootID=0 so the engine never moves a task under an unproven root.
var rootTaskID = regexp.MustCompile(`^RootTask id=(\d+)\b`)

// rootTaskDisplay reads the display id of one root task header.
var rootTaskDisplay = regexp.MustCompile(`\bdisplayId=(\d+)\b`)

// taskUserID reads the `userId=<n>` token a root or leaf line may carry.
var taskUserID = regexp.MustCompile(`\buserId=(\d+)\b`)

// leafTaskPrefix opens one leaf task line, including a line whose target cannot
// be parsed: the line still proves that the snapshot is incomplete.
var leafTaskPrefix = regexp.MustCompile(`^\s+taskId=`)

// leafTaskID reads the id token of one leaf task line and stops right after the
// separator, so the target can be read from the remainder of the line.
var leafTaskID = regexp.MustCompile(`^\s+taskId=(\d+):[ \t]?`)

// leafTaskUnknown matches an unknown leaf: the activity manager knows the
// container but not the activity or package behind it.
var leafTaskUnknown = regexp.MustCompile(`^unknown\b`)

// leafTaskComponent reads the `package/Class` target of one leaf task line.
var leafTaskComponent = regexp.MustCompile(`^(` + componentPattern + `)\b`)

// leafTaskVisible reads the trailing visible=true flag of a leaf task line.
var leafTaskVisible = regexp.MustCompile(`\bvisible=(true|false)\b`)

// taskRoot is the current root segment while one listing is parsed. display is
// only meaningful while seen is true; a root whose display cannot be read never
// makes its leaves a trustworthy inventory.
type taskRoot struct {
	id      int
	display int
	seen    bool
	hasUser bool
	user    int
}

// parseTaskSnapshot reads this phone's `am stack list` RootTaskInfo output.
// Unlike `dumpsys activity` it lists current containers only, so the same
// package can own an independent task on another display without being
// collapsed.
//
// Every task line carries its own user on this device; a root segment may carry
// one too. The leaf value wins, a root value fills a missing leaf value, and a
// disagreement keeps the task with HasUserID=false because neither value is
// proven. Unknown leaves are retained with an empty package: they carry no
// activity identity, but their task id is still a real container.
//
// Complete is false when a task line cannot be structurally parsed or when a
// leaf has no containing root segment with a readable display id. Such a task is
// not guessed into the inventory; the incompleteness fact is what tells the
// caller the enumeration cannot be trusted.
func parseTaskSnapshot(listing string) device.TaskSnapshot {
	snapshot := device.TaskSnapshot{Complete: true, Tasks: []device.Task{}}
	var root taskRoot
	for _, raw := range strings.Split(listing, "\n") {
		line := strings.TrimRight(raw, "\r")
		if rootTaskPrefix.MatchString(line) {
			// Every header starts a new container, so the previous root never
			// carries over to the next one.
			root = taskRoot{display: -1}
			if id := rootTaskID.FindStringSubmatch(line); id != nil {
				if parsed, err := strconv.Atoi(id[1]); err == nil && parsed > 0 {
					root.id = parsed
				}
			}
			if display := rootTaskDisplay.FindStringSubmatch(line); display != nil {
				if parsed, err := strconv.Atoi(display[1]); err == nil && parsed >= 0 {
					root.display, root.seen = parsed, true
				}
			}
			root.user, root.hasUser = parseUserToken(line)
			continue
		}
		if !leafTaskPrefix.MatchString(line) {
			continue
		}
		id := leafTaskID.FindStringSubmatch(line)
		if id == nil {
			snapshot.Complete = false
			continue
		}
		taskID, err := strconv.Atoi(id[1])
		if err != nil || taskID <= 0 {
			snapshot.Complete = false
			continue
		}
		if !root.seen || root.display < 0 {
			snapshot.Complete = false
			continue
		}
		task := device.Task{TaskID: taskID, RootID: root.id, DisplayID: root.display}
		target := strings.TrimLeft(line[len(id[0]):], " \t")
		switch {
		case leafTaskUnknown.MatchString(target):
			// An unknown leaf keeps its task identity and nothing else.
		case leafTaskComponent.MatchString(target):
			component := normalizeComponent(leafTaskComponent.FindString(target))
			task.Package, task.Component = packageOf(component), component
		default:
			// A target that is neither `unknown` nor a component cannot be
			// attributed, so the whole enumeration is incomplete.
			snapshot.Complete = false
		}
		if visible := leafTaskVisible.FindStringSubmatch(line); visible != nil {
			task.Visible = visible[1] == "true"
		}
		leafUser, leafHasUser := parseUserToken(line)
		switch {
		case leafHasUser && root.hasUser:
			task.UserID = leafUser
			task.HasUserID = leafUser == root.user
		case leafHasUser:
			task.UserID, task.HasUserID = leafUser, true
		case root.hasUser:
			task.UserID, task.HasUserID = root.user, true
		}
		snapshot.Tasks = append(snapshot.Tasks, task)
	}
	return snapshot
}

// parseTasks returns the packaged leaf tasks of one listing. It keeps the
// historical TaskList contract: unknown leaves stay in the snapshot but are not
// exposed as tasks.
func parseTasks(listing string) []device.Task {
	return packagedTasks(parseTaskSnapshot(listing).Tasks)
}

// packagedTasks filters the attributable tasks out of one snapshot.
func packagedTasks(tasks []device.Task) []device.Task {
	out := make([]device.Task, 0, len(tasks))
	for _, task := range tasks {
		if task.Package == "" {
			continue
		}
		out = append(out, task)
	}
	return out
}

// parseUserToken reads the `userId=<n>` token of one root or leaf line. Only a
// non-negative decimal value counts; anything else is missing evidence.
func parseUserToken(line string) (int, bool) {
	match := taskUserID.FindStringSubmatch(line)
	if match == nil {
		return 0, false
	}
	value, err := strconv.Atoi(match[1])
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

// parseIntStrict parses a decimal integer with no surrounding text.
func parseIntStrict(text string) (int, bool) {
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, false
	}
	return value, true
}
