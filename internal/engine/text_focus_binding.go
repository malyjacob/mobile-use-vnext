package engine

import (
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

var textFocusPollDelays = []time.Duration{60 * time.Millisecond, 120 * time.Millisecond}

const (
	textFocusRecheckMaxElapsed = 1200 * time.Millisecond
	textFocusMaxAttempts       = 3
	maxParentAnchorDepth       = 8
)

// expandedTextGeometry permits vertical movement and growth while still
// requiring the new field to cover at least half of the old horizontal span.
func expandedTextGeometry(oldRect, freshRect device.Rect) bool {
	oldWidth, oldHeight := oldRect.Width(), oldRect.Height()
	freshWidth, freshHeight := freshRect.Width(), freshRect.Height()
	if oldWidth <= 0 || oldHeight <= 0 || freshWidth <= 0 || freshHeight <= 0 {
		return false
	}
	if freshWidth < oldWidth-max(8, oldWidth/5) || freshHeight < oldHeight-max(8, oldHeight/5) {
		return false
	}
	interW := min(oldRect.Right, freshRect.Right) - max(oldRect.Left, freshRect.Left)
	return interW > 0 && interW*2 >= oldWidth
}

// isRawWindowRoot recognizes both single-window roots and the real window
// roots immediately below a synthetic display root, as in lineageL1.
func isRawWindowRoot(capture *device.Capture, index int) bool {
	if capture == nil || index < 0 || index >= len(capture.Nodes) {
		return true
	}
	node := &capture.Nodes[index]
	return index == capture.Root || node.Parent == -1 || isSyntheticDisplayRoot(node) ||
		(node.Parent >= 0 && node.Parent < len(capture.Nodes) && isSyntheticDisplayRoot(&capture.Nodes[node.Parent]))
}

func textParentAnchorBoundary(node *device.RawNode) bool {
	return node.Editable || (node.HasScrollable && node.Scrollable)
}

func nearestParentResourceAnchor(capture *device.Capture, wantIdx int) *device.RawNode {
	want := &capture.Nodes[wantIdx]
	seen := map[int]struct{}{wantIdx: {}}
	parent := want.Parent
	for range maxParentAnchorDepth {
		if isRawWindowRoot(capture, parent) {
			break
		}
		if _, visited := seen[parent]; visited {
			break
		}
		seen[parent] = struct{}{}
		ancestor := &capture.Nodes[parent]
		if textParentAnchorBoundary(ancestor) {
			break
		}
		if ancestor.DisplayID == want.DisplayID && ancestor.Package == want.Package &&
			ancestor.ResourceSet && ancestor.ResourceID != "" {
			return ancestor
		}
		parent = ancestor.Parent
	}
	return nil
}

func uniqueTextParentAnchor(capture *device.Capture, anchor *device.RawNode) int {
	match := -1
	for index := range capture.Nodes {
		node := &capture.Nodes[index]
		if node.DisplayID != anchor.DisplayID || node.Package != anchor.Package ||
			node.ClassName != anchor.ClassName || !node.ResourceSet || node.ResourceID != anchor.ResourceID ||
			isRawWindowRoot(capture, index) || textParentAnchorBoundary(node) {
			continue
		}
		if match >= 0 {
			return -1
		}
		match = index
	}
	return match
}

// uniqueTextDescendant checks the editable class before any focus or source
// filtering. An editable descendant can match, but its children are off limits.
func uniqueTextDescendant(capture *device.Capture, parent int, want *device.RawNode) int {
	if parent < 0 {
		return -1
	}
	seen := map[int]struct{}{parent: {}}
	match := -1
	var visit func(index, depth int) bool
	visit = func(index, depth int) bool {
		if index < 0 || index >= len(capture.Nodes) {
			return false
		}
		if _, visited := seen[index]; visited {
			return false
		}
		seen[index] = struct{}{}
		node := &capture.Nodes[index]
		if node.Editable && node.DisplayID == want.DisplayID && node.Package == want.Package &&
			node.ClassName == want.ClassName {
			if match >= 0 {
				return false
			}
			match = index
		}
		if depth == maxParentAnchorDepth || textParentAnchorBoundary(node) {
			return true
		}
		for _, child := range node.Children {
			if !visit(child, depth+1) {
				return false
			}
		}
		return true
	}
	for _, child := range capture.Nodes[parent].Children {
		if !visit(child, 1) {
			return -1
		}
	}
	return match
}

func bindFocusedTextByParentAnchor(original, fresh *device.Capture, wantIdx int, want *device.RawNode) (textBinding, bool) {
	anchor := nearestParentResourceAnchor(original, wantIdx)
	if anchor == nil {
		return textBinding{}, false
	}
	oldParent := uniqueTextParentAnchor(original, anchor)
	if uniqueTextDescendant(original, oldParent, want) != wantIdx {
		return textBinding{}, false
	}
	freshParent := uniqueTextParentAnchor(fresh, anchor)
	candIdx := uniqueTextDescendant(fresh, freshParent, want)
	if candIdx < 0 {
		return textBinding{}, false
	}
	cand := &fresh.Nodes[candIdx]
	if !sameTextSourcePresence(want, cand) || !textCandidateSafe(cand) ||
		!rectValidOnScreen(cand.Bounds, fresh.Display.Bounds()) ||
		(!sameHorizontalBand(want.Bounds, cand.Bounds) && !expandedTextGeometry(want.Bounds, cand.Bounds)) {
		return textBinding{}, false
	}
	for i := range fresh.Nodes {
		node := &fresh.Nodes[i]
		if i != candIdx && node.DisplayID == want.DisplayID && textCandidateSafe(node) {
			return textBinding{}, false
		}
	}
	return textBinding{raw: cand, mode: textParentAnchor, capture: fresh}, true
}

func (e *Engine) logTextFocus(operationID string, mode textBindingMode, attempt int, elapsed time.Duration, apiErr *protocol.APIError) {
	outcome, reason := "proven", ""
	if apiErr != nil {
		mode = "none"
		outcome, reason = OutcomeUncertain, textReasonFocusUnconfirmed
		if apiErr.Code == protocol.CodeStopped {
			outcome, reason = "stopped", textReasonAuthorizationRevoked
		}
	}
	fields := []eventlog.Field{
		eventlog.Str("operation_id", operationID),
		eventlog.Str("mode", string(mode)),
		eventlog.Int("attempt", attempt),
		eventlog.Str("outcome", outcome),
		eventlog.Duration("ms", elapsed),
	}
	if reason != "" {
		fields = append(fields, eventlog.Str("reason", reason))
	}
	e.log.Event(eventlog.Debug, "engine.text.focus", fields...)
}
