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

// leafTask is one `taskId=N: package/Class` line under a root task.
var leafTask = regexp.MustCompile(`^\s+taskId=(\d+): (` + componentPattern + `)\b`)

// leafTaskVisible reads the trailing visible=true flag of a leaf task line.
var leafTaskVisible = regexp.MustCompile(`\bvisible=(true|false)\b`)

// parseTasks reads this phone's `am stack list` RootTaskInfo output. Unlike
// `dumpsys activity` it lists current containers only, so the same package can
// own an independent task on another display without being collapsed.
func parseTasks(listing string) []device.Task {
	tasks := []device.Task{}
	displayID := -1
	rootID := 0
	rootSeen := false
	for _, line := range strings.Split(listing, "\n") {
		if rootTaskPrefix.MatchString(line) {
			// Every header starts a new container, so the previous root id never
			// carries over to the next one.
			rootID = 0
			displayID = -1
			rootSeen = false
			if id := rootTaskID.FindStringSubmatch(line); id != nil {
				if parsed, err := strconv.Atoi(id[1]); err == nil && parsed > 0 {
					rootID = parsed
				}
			}
			if display := rootTaskDisplay.FindStringSubmatch(line); display != nil {
				if parsed, err := strconv.Atoi(display[1]); err == nil && parsed >= 0 {
					displayID = parsed
					rootSeen = true
				}
			}
			continue
		}
		task := leafTask.FindStringSubmatch(line)
		if task == nil || !rootSeen || displayID < 0 {
			continue
		}
		taskID, err := strconv.Atoi(task[1])
		if err != nil {
			continue
		}
		component := normalizeComponent(task[2])
		visible := leafTaskVisible.FindStringSubmatch(line)
		tasks = append(tasks, device.Task{
			TaskID:    taskID,
			RootID:    rootID,
			DisplayID: displayID,
			Package:   packageOf(component),
			Component: component,
			Visible:   visible != nil && visible[1] == "true",
		})
	}
	return tasks
}

// parseIntStrict parses a decimal integer with no surrounding text.
func parseIntStrict(text string) (int, bool) {
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, false
	}
	return value, true
}
