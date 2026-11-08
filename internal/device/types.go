// Package device holds internal device facts shared by the android adapter and
// the engine. It holds no runtime state and performs no I/O.
package device

import "time"

// Rect is a half-open rectangle in target-display logical pixels.
type Rect struct {
	Left, Top, Right, Bottom int
}

// Width returns the rectangle width.
func (r Rect) Width() int { return r.Right - r.Left }

// Height returns the rectangle height.
func (r Rect) Height() int { return r.Bottom - r.Top }

// Empty reports whether the rectangle has no area.
func (r Rect) Empty() bool { return r.Right <= r.Left || r.Bottom <= r.Top }

// Intersect returns the intersection with other.
func (r Rect) Intersect(other Rect) Rect {
	out := Rect{
		Left:   max(r.Left, other.Left),
		Top:    max(r.Top, other.Top),
		Right:  min(r.Right, other.Right),
		Bottom: min(r.Bottom, other.Bottom),
	}
	if out.Empty() {
		return Rect{}
	}
	return out
}

// Contains reports whether the point lies in the half-open rectangle.
func (r Rect) Contains(x, y int) bool {
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

// Center returns the integer center point.
func (r Rect) Center() (int, int) { return (r.Left + r.Right) / 2, (r.Top + r.Bottom) / 2 }

// Display is one dynamic display identity. ID is the Android logical display
// id; SFID is the SurfaceFlinger layer id used by screencap. They are not
// interchangeable and are never treated as constants.
type Display struct {
	ID         int
	SFID       uint64
	Width      int
	Height     int
	Rotation   int
	Density    int
	Generation string
}

// Bounds returns the display rectangle.
func (d Display) Bounds() Rect {
	return Rect{Left: 0, Top: 0, Right: d.Width, Bottom: d.Height}
}

// Foreground states.
const (
	ForegroundPresent = "present"
	ForegroundNone    = "none"
	ForegroundUnknown = "unknown"
)

// Foreground is the target display foreground record.
type Foreground struct {
	State   string
	Package string
	TaskID  *int
	Reason  string
}

// RawNode is one source node with enough evidence for public projection. The
// Has* flags record whether the source actually provided the attribute; the
// projection must not turn a missing attribute into a known default.
type RawNode struct {
	Index       int
	Parent      int // -1 for the traversal root
	Children    []int
	Depth       int
	DisplayID   int
	RawBounds   Rect
	Bounds      Rect // clipped to the display; zero when fully off-screen
	Visible     bool // visible-to-user attribute and intersects the display
	Text        string
	Description string
	ResourceID  string
	ClassName   string
	Package     string

	TextSet       bool
	DescSet       bool
	ResourceSet   bool
	HasEnabled    bool
	Enabled       bool
	HasChecked    bool
	Checked       bool
	HasSelected   bool
	Selected      bool
	HasFocused    bool
	Focused       bool
	HasClickable  bool
	Clickable     bool
	HasLongClick  bool
	LongClickable bool
	HasScrollable bool
	Scrollable    bool
	HasCheckable  bool
	Checkable     bool
	HasPassword   bool
	Password      bool

	// Editable is a class-name heuristic used only as a projection hint. Text
	// input revalidates editability through live device RPCs.
	Editable bool
}

// Capture is one immutable read-only acquisition of a target display.
type Capture struct {
	Display    Display
	Nodes      []RawNode
	Root       int
	Foreground Foreground
	CapturedAt time.Time
}

// TextTarget is the display-bound structural identity used by the patched
// uiautomator text RPCs.
type TextTarget struct {
	DisplayID   int
	Package     string
	ResourceID  string
	ClassName   string
	Description string
	Bounds      Rect
	Width       int
	Height      int
}

// TextResult is one patched display-bound text RPC result.
type TextResult struct {
	CandidateCount int
	Value          *string
	Issued         bool
	Accepted       bool
}

// DisplayState is the read-only display existence fact. SurfaceFlinger layer
// ids exceed int64 on this device, so they are carried as unsigned 64-bit.
type DisplayState struct {
	Exists bool
	SFID   uint64
}

// InputKind is one dispatched input primitive.
type InputKind string

const (
	InputTap       InputKind = "tap"
	InputLongPress InputKind = "long_press"
	InputSwipe     InputKind = "swipe"
	InputKey       InputKind = "key"
)

// Input is one device input primitive. It is dispatched only after the engine
// has claimed the observation and revalidated the live target.
type Input struct {
	Kind      InputKind
	DisplayID int
	X, Y      int
	ToX, ToY  int
	Key       string
}

// Task is one foreground task fact from the activity dump.
type Task struct {
	TaskID    int
	DisplayID int
	Package   string
	Component string
	Visible   bool
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
