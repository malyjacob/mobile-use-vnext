package observation

import (
	"fmt"
	"math/bits"
	"sort"
	"strings"
	"unicode"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// Visual hint kinds. The kind is a controlled enum that the engine may record
// for delta de-duplication; the value is the short token written on the public
// observation line. Neither carries page text, coordinates or package details.
const (
	VisualKindLoading      = "loading"
	VisualKindImageHeavy   = "image_heavy"
	VisualKindMediaSurface = "media_surface"
	VisualKindSparseTree   = "sparse_tree"
)

// Visual hint thresholds. They are fixed compile-time constants: the hint never
// adds a request parameter, a device read or a second screenshot.
//
//	visualMinImagePercent      foreground image fragments below 5% are ignored
//	visualMaxBackdropPercent   full-screen backdrop images at or above 90% are ignored
//	visualBackdropControlLimit a picture holding this many smaller labeled or
//	                           interactive controls is treated as a background
//	visualMaxSampledRects      at most this many foreground pictures are unioned
//	visualHeavyUnionPercent    the union must cover at least this share
//	visualHeavyMaxTextRunes    a text-rich page is never "image_heavy"
//	visualSparseMaxTextRunes   a page with fewer readable runes is sparse
//	visualSparseMaxInteractive a page with fewer interactive controls is sparse
const (
	visualMinImagePercent           = 5
	visualMaxBackdropPercent        = 90
	visualBackdropControlLimit      = 3
	visualMaxSampledRects           = 64
	visualHeavyUnionPercent         = 45
	visualHeavyMaxTextRunes         = 150
	visualLoadingMaxTextRunes       = 80
	visualLoadingMaxInteractive     = 8
	visualUnstableSparseTextRunes   = 40
	visualUnstableSparseInteractive = 8
	visualMinMediaSurfacePercent    = 25
	visualMaxMediaRects             = 16
	visualMediaMaxTextRunes         = 80
	visualMediaMaxInteractive       = 10
	visualSparseMaxTextRunes        = 20
	visualSparseMaxInteractive      = 4
)

// visualRect is one screen-clipped rectangle with its area and center.
type visualRect struct {
	rect protocol.Bounds
	area int64
	cx   int
	cy   int
}

// visualStats is the bounded aggregate of one complete public tree.
type visualStats struct {
	textRunes   int
	interactive int
	images      []visualRect
	controls    []visualRect
}

// InferVisualHint classifies a complete public tree without raw capture or
// settle quality evidence.
func InferVisualHint(tree *protocol.Tree, foregroundState string) (string, string) {
	return ClassifyVisual(tree, nil, foregroundState, nil)
}

// ClassifyVisual returns a visual kind and its public token in priority order:
// loading, image_heavy(<pct>%), media_surface, or sparse_tree. It uses the
// complete public tree, optional raw capture, and optional settle quality.
func ClassifyVisual(tree *protocol.Tree, capture *device.Capture, foregroundState string, quality *protocol.Quality) (kind, value string) {
	if tree == nil || tree.Root == nil || tree.Root.Bounds == nil {
		return "", ""
	}
	screen := *tree.Root.Bounds
	screenArea, ok := boundsArea(screen)
	if !ok || screenArea <= 0 {
		return "", ""
	}
	projection := compactProjection{screen: screen, screenKnown: true, screenArea: screenArea}
	stats := collectVisualStats(tree.Root, projection)
	var hasProgressIndicator bool
	var mediaRects []visualRect
	if capture != nil {
		hasProgressIndicator, mediaRects = collectVisualCaptureStats(capture, projection)
	}
	if foregroundState != device.ForegroundNone && visualLoading(stats, hasProgressIndicator, quality) {
		return VisualKindLoading, VisualKindLoading
	}
	if kind, value := visualImageHeavy(stats, screenArea); kind != "" {
		return kind, value
	}
	if foregroundState == device.ForegroundNone {
		return "", ""
	}
	if stats.textRunes < visualMediaMaxTextRunes && stats.interactive < visualMediaMaxInteractive &&
		areaAtLeastPercent(visualMediaUnionArea(mediaRects), screenArea, visualMinMediaSurfacePercent) {
		return VisualKindMediaSurface, VisualKindMediaSurface
	}
	if stats.textRunes < visualSparseMaxTextRunes && stats.interactive < visualSparseMaxInteractive {
		return VisualKindSparseTree, VisualKindSparseTree
	}
	return "", ""
}

func visualLoading(stats visualStats, hasProgressIndicator bool, quality *protocol.Quality) bool {
	return (hasProgressIndicator && stats.textRunes < visualLoadingMaxTextRunes &&
		stats.interactive < visualLoadingMaxInteractive) ||
		(quality != nil && quality.State == protocol.QualityUnstable &&
			quality.StopReason != protocol.QualityStopCondition && stats.textRunes < visualUnstableSparseTextRunes &&
			stats.interactive < visualUnstableSparseInteractive)
}

// collectVisualCaptureStats finds class evidence in visible nodes of the target
// display. The complete public tree may omit these semantically empty nodes.
func collectVisualCaptureStats(capture *device.Capture, screen compactProjection) (bool, []visualRect) {
	var hasProgressIndicator bool
	var mediaRects []visualRect
	for i := range capture.Nodes {
		node := &capture.Nodes[i]
		if i == capture.Root || node.DisplayID != capture.Display.ID || !node.Visible {
			continue
		}
		rect, positive := compactRect(protocol.Bounds(node.Bounds), screen)
		if !positive {
			continue
		}
		if isProgressBarClass(node.ClassName) && rect.Width() > 4 && rect.Height() > 4 {
			hasProgressIndicator = true
		}
		if isMediaSurfaceClass(node.ClassName) {
			media := newVisualRect(rect)
			if !areaBelowPercent(media.area, screen.screenArea, visualMinImagePercent) {
				mediaRects = append(mediaRects, media)
			}
		}
	}
	return hasProgressIndicator, mediaRects
}

func isProgressBarClass(class string) bool {
	simple := class[strings.LastIndexByte(class, '.')+1:]
	return strings.HasSuffix(simple, "ProgressBar") && !strings.Contains(simple, "SeekBar")
}

func isMediaSurfaceClass(class string) bool {
	simple := class[strings.LastIndexByte(class, '.')+1:]
	return strings.HasSuffix(simple, "SurfaceView") || strings.HasSuffix(simple, "TextureView") ||
		strings.HasSuffix(simple, "VideoView") || strings.HasSuffix(simple, "PlayerView")
}

func visualMediaUnionArea(rects []visualRect) int64 {
	if len(rects) > visualMaxMediaRects {
		sort.SliceStable(rects, func(i, j int) bool {
			return rects[i].area > rects[j].area
		})
		rects = rects[:visualMaxMediaRects]
	}
	return unionRectArea(rects)
}

// visualImageHeavy applies the foreground-picture rule. It keeps image leaves
// that survive four precise filters, unions at most the largest 64 of them and
// reports the covered screen share only on a text-poor page.
func visualImageHeavy(stats visualStats, screenArea int64) (string, string) {
	candidates := make([]visualRect, 0, len(stats.images))
	for _, image := range stats.images {
		if areaBelowPercent(image.area, screenArea, visualMinImagePercent) {
			continue
		}
		if areaAtLeastPercent(image.area, screenArea, visualMaxBackdropPercent) {
			continue
		}
		if visualBackgroundControls(image, stats.controls) >= visualBackdropControlLimit {
			continue
		}
		candidates = append(candidates, image)
	}
	if len(candidates) == 0 {
		return "", ""
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].area > candidates[j].area
	})
	if len(candidates) > visualMaxSampledRects {
		candidates = candidates[:visualMaxSampledRects]
	}
	unionArea := unionRectArea(candidates)
	if !areaAtLeastPercent(unionArea, screenArea, visualHeavyUnionPercent) {
		return "", ""
	}
	if stats.textRunes >= visualHeavyMaxTextRunes {
		return "", ""
	}
	percent := roundedPercentOf(unionArea, screenArea)
	if percent < visualHeavyUnionPercent {
		percent = visualHeavyUnionPercent
	}
	if percent > 100 {
		percent = 100
	}
	return VisualKindImageHeavy, fmt.Sprintf("%s(%d%%)", VisualKindImageHeavy, percent)
}

// visualBackgroundControls counts the smaller labeled or interactive controls
// whose center lies inside one picture. Reaching the fixed limit is enough to
// stop counting.
func visualBackgroundControls(image visualRect, controls []visualRect) int {
	count := 0
	for _, control := range controls {
		if control.area >= image.area {
			continue
		}
		if !image.rect.Contains(control.cx, control.cy) {
			continue
		}
		count++
		if count >= visualBackdropControlLimit {
			return count
		}
	}
	return count
}

// collectVisualStats walks the complete public tree once. The synthetic root
// contributes no readable content. Every other non-context node adds its readable
// runes that no ancestor composite label already carries, counts as one
// interactive control when it has an action and a positive visible rectangle, and
// is remembered as a candidate image leaf or as a labeled/interactive control.
func collectVisualStats(root *protocol.Node, projection compactProjection) visualStats {
	stats := visualStats{}
	if root == nil {
		return stats
	}
	for _, child := range root.Children {
		stats.walk(child, nil, projection)
	}
	return stats
}

// walk accumulates one subtree in source preorder.
func (s *visualStats) walk(n *protocol.Node, ancestors [][]string, projection compactProjection) {
	if n == nil {
		return
	}
	if !n.Context {
		for _, part := range n.Label {
			if compactPartCovered(part, ancestors) {
				continue
			}
			s.textRunes += countReadableRunes(part)
		}
		if n.Value != nil && !(n.Protected != nil && *n.Protected) {
			s.textRunes += countReadableRunes(*n.Value)
		}
		if n.Hint != nil {
			s.textRunes += countReadableRunes(*n.Hint)
		}
		if rect, positive := compactVisibleRect(n, projection); positive {
			if len(n.Actions) > 0 {
				s.interactive++
			}
			if visualImageLeaf(n) {
				s.images = append(s.images, newVisualRect(rect))
			} else if visualContentControl(n) {
				s.controls = append(s.controls, newVisualRect(rect))
			}
		}
	}
	childAncestors := ancestors
	if hasNonNilChild(n) && len(n.Label) > 0 {
		childAncestors = appendCompactAncestorLabel(ancestors, n.Label)
	}
	for _, child := range n.Children {
		s.walk(child, childAncestors, projection)
	}
}

// compactVisibleRect returns the positive visible part of one node rectangle.
func compactVisibleRect(n *protocol.Node, projection compactProjection) (protocol.Bounds, bool) {
	if n == nil || n.Bounds == nil {
		return protocol.Bounds{}, false
	}
	return compactRect(*n.Bounds, projection)
}

// visualImageLeaf is the A3c predicate on the complete public tree: an image
// leaf without readable content, actions or non-default state.
func visualImageLeaf(n *protocol.Node) bool {
	if n == nil || n.Context || n.Role != protocol.RoleImage || hasNonNilChild(n) {
		return false
	}
	if len(n.Actions) != 0 || len(n.Label) != 0 {
		return false
	}
	if n.Value != nil || n.Hint != nil || n.Resource != nil || n.Axis != "" {
		return false
	}
	return visualKnownDefaultState(n)
}

// visualContentControl reports whether one node carries content a reader can use:
// a readable label, a non-empty value or at least one action.
func visualContentControl(n *protocol.Node) bool {
	if n == nil || n.Context {
		return false
	}
	if len(n.Label) > 0 || len(n.Actions) > 0 {
		return true
	}
	return n.Value != nil && *n.Value != ""
}

// visualKnownDefaultState reports whether all five public state flags are known
// and at their default: enabled true, focused/selected/protected/checked false.
func visualKnownDefaultState(n *protocol.Node) bool {
	if n == nil || n.Enabled == nil || !*n.Enabled {
		return false
	}
	for _, flag := range []*bool{n.Focused, n.Selected, n.Protected, n.Checked} {
		if flag == nil || *flag {
			return false
		}
	}
	return true
}

// newVisualRect derives the area and half-open center of one positive rectangle.
func newVisualRect(rect protocol.Bounds) visualRect {
	area, _ := boundsArea(rect)
	cx, cy := compactCenter(rect)
	return visualRect{rect: rect, area: area, cx: cx, cy: cy}
}

// countReadableRunes counts the code points of one readable string that are not
// whitespace.
func countReadableRunes(s string) int {
	count := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			count++
		}
	}
	return count
}

// unionRectArea returns the exact number of screen pixels covered by the union of
// the given half-open rectangles. The plane is split at every rectangle edge;
// inside one strip the covered height is the merged length of the covering
// rectangles, so overlapping pictures are counted once and no pixel is estimated.
func unionRectArea(rects []visualRect) int64 {
	if len(rects) == 0 {
		return 0
	}
	columns := make([]int, 0, 2*len(rects))
	for _, item := range rects {
		columns = append(columns, item.rect.Left, item.rect.Right)
	}
	sort.Ints(columns)
	type span struct{ top, bottom int }
	spans := make([]span, 0, len(rects))
	var total int64
	for i := 0; i+1 < len(columns); i++ {
		left, right := columns[i], columns[i+1]
		width := right - left
		if width <= 0 {
			continue
		}
		spans = spans[:0]
		for _, item := range rects {
			if item.rect.Left <= left && right <= item.rect.Right {
				spans = append(spans, span{top: item.rect.Top, bottom: item.rect.Bottom})
			}
		}
		if len(spans) == 0 {
			continue
		}
		sort.Slice(spans, func(a, b int) bool { return spans[a].top < spans[b].top })
		covered := int64(0)
		top, bottom := spans[0].top, spans[0].bottom
		for _, item := range spans[1:] {
			if item.top > bottom {
				covered += int64(bottom - top)
				top, bottom = item.top, item.bottom
				continue
			}
			if item.bottom > bottom {
				bottom = item.bottom
			}
		}
		covered += int64(bottom - top)
		total += covered * int64(width)
	}
	return total
}

// areaBelowPercent reports whether area*100 is strictly below
// screenArea*percent, exactly and without overflowing.
func areaBelowPercent(area, screenArea int64, percent uint64) bool {
	return !mulAtLeast(area, 100, screenArea, percent)
}

// areaAtLeastPercent reports whether area*100 is at least screenArea*percent,
// exactly and without overflowing.
func areaAtLeastPercent(area, screenArea int64, percent uint64) bool {
	return mulAtLeast(area, 100, screenArea, percent)
}

// mulAtLeast compares a*aFactor with b*bFactor as unsigned 128-bit products. Both
// areas are non-negative, so the conversion is exact.
func mulAtLeast(a int64, aFactor uint64, b int64, bFactor uint64) bool {
	highA, lowA := bits.Mul64(uint64(a), aFactor)
	highB, lowB := bits.Mul64(uint64(b), bFactor)
	if highA != highB {
		return highA > highB
	}
	return lowA >= lowB
}

// roundedPercentOf returns round(100*part/whole) as an integer. The numerator is
// built as a 128-bit product and divided exactly, so no intermediate overflows
// even for screens whose pixel area is near the int64 limit.
func roundedPercentOf(part, whole int64) int {
	if whole <= 0 {
		return 0
	}
	if part < 0 {
		part = 0
	}
	if part > whole {
		part = whole
	}
	high, low := bits.Mul64(uint64(part), 100)
	half := uint64(whole / 2)
	low += half
	if low < half {
		high++
	}
	quotient, _ := bits.Div64(high, low, uint64(whole))
	return int(quotient)
}
