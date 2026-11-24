package engine

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

type baselineSelectionCandidate struct {
	cost      int
	jsonBytes int
	textBytes int
	delta     *protocol.ReadDelta
}

type baselineSelectionNode struct {
	id        string
	label     string
	bounds    device.Rect
	clickable bool
	disabled  bool
}

type baselineSelectionContext struct {
	id      string
	childID string
	label   string
	bounds  device.Rect
}

func baselineSelectionNodes(count int) []baselineSelectionNode {
	nodes := make([]baselineSelectionNode, count)
	for index := range nodes {
		top := 120 + index*170
		nodes[index] = baselineSelectionNode{
			id:        fmt.Sprintf("baseline-item-%02d", index),
			label:     fmt.Sprintf("item-%02d", index),
			bounds:    device.Rect{Left: 40, Top: top, Right: 480, Bottom: top + 120},
			clickable: true,
		}
	}
	return nodes
}

func baselineSelectionNodeCapture(display device.Display,
	nodes []baselineSelectionNode) device.Capture {
	return baselineSelectionContextCapture(display, nodes, nil)
}

func baselineSelectionContextCapture(display device.Display,
	nodes []baselineSelectionNode, contexts []baselineSelectionContext) device.Capture {
	specs := make([]rawNodeSpec, 1, len(nodes)+1+2*len(contexts))
	specs[0] = rawNodeSpec{
		Parent: -1,
		Bounds: screenRect(),
		Class:  "android.widget.FrameLayout",
	}
	for _, node := range nodes {
		specs = append(specs, rawNodeSpec{
			Parent:    0,
			Bounds:    node.bounds,
			Class:     "android.widget.Button",
			Text:      node.label,
			Clickable: node.clickable,
			Disabled:  node.disabled,
		})
	}
	for _, context := range contexts {
		contextParent := len(specs)
		specs = append(specs, rawNodeSpec{
			Parent: 0,
			Bounds: context.bounds,
			Class:  "android.widget.FrameLayout",
			Hidden: true,
		})
		specs = append(specs, rawNodeSpec{
			Parent: contextParent,
			Bounds: context.bounds,
			Class:  "android.widget.TextView",
			Text:   context.label,
		})
	}
	capture := buildCapture(display, specs)
	for index, node := range nodes {
		capture.Nodes[index+1].ResourceID = node.id
		capture.Nodes[index+1].ResourceSet = true
	}
	for index, context := range contexts {
		contextIndex := 1 + len(nodes) + index*2
		capture.Nodes[contextIndex].ResourceID = context.id
		capture.Nodes[contextIndex].ResourceSet = true
		capture.Nodes[contextIndex+1].ResourceID = context.childID
		capture.Nodes[contextIndex+1].ResourceSet = true
	}
	return capture
}

func baselineSelectionNodeStep(nodes []baselineSelectionNode) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return baselineSelectionNodeCapture(display, nodes), nil
	}
}

func baselineSelectionContextStep(nodes []baselineSelectionNode,
	contexts []baselineSelectionContext) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return baselineSelectionContextCapture(display, nodes, contexts), nil
	}
}

func baselineSelectionPage(labels []string, bounds []device.Rect) []rawNodeSpec {
	specs := make([]rawNodeSpec, 1, len(labels)+1)
	specs[0] = rawNodeSpec{
		Parent: -1,
		Bounds: screenRect(),
		Class:  "android.widget.FrameLayout",
	}
	for index, label := range labels {
		specs = append(specs, rawNodeSpec{
			Parent:    0,
			Bounds:    bounds[index],
			Class:     "android.widget.Button",
			Text:      label,
			Clickable: true,
		})
	}
	return specs
}

func baselineSelectionDefaultLabels(count int) []string {
	labels := make([]string, count)
	for index := range labels {
		labels[index] = fmt.Sprintf("item-%02d", index)
	}
	return labels
}

func baselineSelectionDefaultBounds(count int) []device.Rect {
	bounds := make([]device.Rect, count)
	for index := range bounds {
		top := 120 + index*170
		bounds[index] = device.Rect{
			Left: 40, Top: top, Right: 480, Bottom: top + 120,
		}
	}
	return bounds
}

func baselineSelectionCapture(display device.Display, labels []string,
	bounds []device.Rect) device.Capture {
	capture := buildCapture(display, baselineSelectionPage(labels, bounds))
	for index := 1; index < len(capture.Nodes); index++ {
		capture.Nodes[index].ResourceID = fmt.Sprintf("baseline-item-%02d", index-1)
		capture.Nodes[index].ResourceSet = true
	}
	return capture
}

func baselineSelectionStep(labels []string, bounds []device.Rect) captureStep {
	return func(_ *fakeDevice, display device.Display) (device.Capture, error) {
		return baselineSelectionCapture(display, labels, bounds), nil
	}
}

func measureBaselineSelectionCandidate(
	t *testing.T,
	h *harness,
	target device.Capture,
	previewObservationID string,
	baseline *baselineState,
) baselineSelectionCandidate {
	t.Helper()
	targetCopy := target
	projection, _, err := h.engine.projector.CandidateAgainst(
		baseline.Projection, &targetCopy)
	if err != nil || projection == nil || projection.Tree == nil ||
		projection.Capture == nil {
		t.Fatalf("candidate against %s failed: %v", baseline.ObservationID, err)
	}
	compact, _, cost, _, apiErr := h.engine.publicCandidate(*projection.Capture, projection)
	if apiErr != nil {
		t.Fatalf("public candidate against %s failed: %v", baseline.ObservationID, apiErr)
	}
	delta, textBytes, ok := h.engine.treeTextBaselineDelta(
		previewObservationID, baseline, compact)
	if !ok {
		t.Fatalf("candidate against %s did not pass the tree-v5 gate", baseline.ObservationID)
	}
	encoded, err := json.Marshal(delta)
	if err != nil {
		t.Fatalf("encode JSON delta against %s: %v", baseline.ObservationID, err)
	}
	return baselineSelectionCandidate{
		cost: cost, jsonBytes: len(encoded), textBytes: textBytes,
		delta: delta,
	}
}

func baselineSelectionDeltaJSON(t *testing.T, candidate baselineSelectionCandidate) string {
	t.Helper()
	encoded, err := json.Marshal(candidate.delta)
	if err != nil {
		t.Fatalf("encode candidate diagnostic: %v", err)
	}
	return string(encoded)
}

func baselineSelectionCurrentBaseline(t *testing.T, h *harness) *baselineState {
	t.Helper()
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	current := h.engine.current
	if current == nil || !current.Full || current.Compact == nil ||
		current.Projection == nil {
		t.Fatalf("current observation is not a reusable full baseline: %+v", current)
	}
	return &baselineState{
		ObservationID:     current.ID,
		Origin:            current.Origin,
		Revision:          current.Revision,
		CapturedAt:        current.CapturedAt,
		DisplayID:         current.DisplayID,
		DisplayGeneration: current.DisplayGeneration,
		Tree:              current.Compact,
		Projection:        current.Projection,
	}
}

// TestBaselineSelectionUsesTextOrderWhenJSONOrderIsOpposite drives Start and
// Observe through the real publication path. Candidate A has fewer JSON bytes,
// while candidate B has fewer tree-v5 bytes and must become delta.base.
func TestBaselineSelectionUsesTextOrderWhenJSONOrderIsOpposite(t *testing.T) {
	h := newHarness(t)
	const count = 12

	targetNodes := baselineSelectionNodes(count)
	// The long label must stay passive: the compact projection truncates long
	// actionable labels, which would shrink candidate A's delta below candidate
	// B's context-only add and invert the fixture's intended byte order.
	targetNodes[2].clickable = false
	targetNodes[2].label = strings.Repeat("x", 740)
	targetContexts := []baselineSelectionContext{
		{
			id: "baseline-context-00", childID: "baseline-context-child-00",
			label:  "context-text",
			bounds: device.Rect{Left: 80, Top: 2140, Right: 1000, Bottom: 2240},
		},
		{
			id: "baseline-context-01", childID: "baseline-context-child-01",
			label:  "context-text-01",
			bounds: device.Rect{Left: 80, Top: 2141, Right: 1000, Bottom: 2241},
		},
		{
			id: "baseline-context-02", childID: "baseline-context-child-02",
			label:  "context-text-02",
			bounds: device.Rect{Left: 80, Top: 2142, Right: 1000, Bottom: 2242},
		},
		{
			id: "baseline-context-03", childID: "baseline-context-child-03",
			label:  "context-text-03",
			bounds: device.Rect{Left: 80, Top: 2143, Right: 1000, Bottom: 2243},
		},
	}
	stepTarget := baselineSelectionContextStep(targetNodes, targetContexts)

	nodesA := append([]baselineSelectionNode(nil), targetNodes...)
	nodesA[2].label = "older-a"
	stepA := baselineSelectionContextStep(nodesA, targetContexts)

	stepB := baselineSelectionNodeStep(targetNodes)

	h.device.scriptCaptures(stepA)
	start := h.startSession()
	baseAID := start.Observation.ID

	h.device.scriptCaptures(stepB)
	baseB := baselineObserveRequest(t, h, "PselectionOppositeB", &protocol.ObserveRequest{
		Reference: baseAID,
		Full:      true,
	})
	requireFullTree(t, baseB, 2)

	targetCapture := baselineSelectionContextCapture(
		h.engineSessionDisplay(t), targetNodes, targetContexts)
	h.engine.mu.Lock()
	baseA := baselineWithID(h.engine.baselines, baseAID)
	previewID := h.engine.previewObservationIDLocked()
	h.engine.mu.Unlock()
	if baseA == nil {
		t.Fatal("page A was not retained as a confirmed baseline")
	}
	baseBState := baselineSelectionCurrentBaseline(t, h)
	if baseBState.ObservationID != baseB.ID {
		t.Fatalf("current baseline = %q, want %q", baseBState.ObservationID, baseB.ID)
	}
	candidateA := measureBaselineSelectionCandidate(
		t, h, targetCapture, previewID, baseA)
	candidateB := measureBaselineSelectionCandidate(
		t, h, targetCapture, previewID, baseBState)
	if candidateA.jsonBytes >= candidateB.jsonBytes {
		t.Fatalf("fixture JSON order is not A < B: A(json=%d text=%d) B(json=%d text=%d)\nA=%s\nB=%s",
			candidateA.jsonBytes, candidateA.textBytes,
			candidateB.jsonBytes, candidateB.textBytes,
			baselineSelectionDeltaJSON(t, candidateA),
			baselineSelectionDeltaJSON(t, candidateB))
	}
	if candidateA.textBytes <= candidateB.textBytes {
		t.Fatalf("fixture tree-v5 order is not B < A: A(json=%d text=%d) B(json=%d text=%d)\nA=%s\nB=%s",
			candidateA.jsonBytes, candidateA.textBytes,
			candidateB.jsonBytes, candidateB.textBytes,
			baselineSelectionDeltaJSON(t, candidateA),
			baselineSelectionDeltaJSON(t, candidateB))
	}

	h.device.scriptCaptures(stepTarget)
	published := baselineObserve(t, h, "PselectionOppositeTarget", baseB.ID)
	requireBaselineDelta(t, published, 3, baseB.ID)
}

// TestBaselineSelectionFallsBackWhenShortestHasNoRetentionPlan uses the real
// Observe commit path. Candidate A has the shortest tree-v5 delta but cannot
// fit even after pool pressure. Candidate B, which can evict backup A and fit,
// must be published instead of a full fallback.
func TestBaselineSelectionFallsBackWhenShortestHasNoRetentionPlan(t *testing.T) {
	h := newHarness(t)
	const count = 12

	targetLabels := baselineSelectionDefaultLabels(count)
	targetBounds := baselineSelectionDefaultBounds(count)

	labelsA := append([]string(nil), targetLabels...)
	labelsA[5] = "shortest-source"
	stepA := baselineSelectionStep(labelsA, targetBounds)

	labelsB := append([]string(nil), targetLabels...)
	boundsB := append([]device.Rect(nil), targetBounds...)
	for _, index := range []int{2, 6, 9} {
		boundsB[index].Left += 120
		boundsB[index].Right += 120
	}
	stepB := baselineSelectionStep(labelsB, boundsB)
	stepTarget := baselineSelectionStep(targetLabels, targetBounds)

	h.device.scriptCaptures(stepA)
	start := h.startSession()
	baseAID := start.Observation.ID
	h.device.scriptCaptures(stepB)
	baseB := baselineObserveRequest(t, h, "PselectionRetentionB", &protocol.ObserveRequest{
		Reference: baseAID,
		Full:      true,
	})
	requireFullTree(t, baseB, 2)

	targetCapture := baselineSelectionCapture(
		h.engineSessionDisplay(t), targetLabels, targetBounds)
	h.engine.mu.Lock()
	baseA := baselineWithID(h.engine.baselines, baseAID)
	previewID := h.engine.previewObservationIDLocked()
	baseBSnapshotCost := h.engine.current.Cost
	h.engine.mu.Unlock()
	if baseA == nil {
		t.Fatal("page A was not retained as a confirmed baseline")
	}
	baseBState := baselineSelectionCurrentBaseline(t, h)
	candidateA := measureBaselineSelectionCandidate(
		t, h, targetCapture, previewID, baseA)
	candidateB := measureBaselineSelectionCandidate(
		t, h, targetCapture, previewID, baseBState)
	if candidateA.textBytes >= candidateB.textBytes {
		t.Fatalf("fixture text order is not A < B: A=%d B=%d\nA=%s\nB=%s",
			candidateA.textBytes, candidateB.textBytes,
			baselineSelectionDeltaJSON(t, candidateA),
			baselineSelectionDeltaJSON(t, candidateB))
	}

	h.engine.mu.Lock()
	baseASnapshotCost := h.engine.observations[baseAID].Cost
	h.engine.mu.Unlock()
	limit := baseBSnapshotCost + candidateB.cost
	rejectAThreshold := baseASnapshotCost + baseBSnapshotCost + candidateA.cost - 1
	if limit > rejectAThreshold {
		t.Fatalf("fixture cannot both fit B and reject A: fit-B=%d reject-A=%d",
			limit, rejectAThreshold)
	}
	h.engine.cfg.MaxRetainedSnapshotCostBytes = limit

	h.device.scriptCaptures(stepTarget)
	published := baselineObserve(t, h, "PselectionRetentionTarget", baseB.ID)
	requireBaselineDelta(t, published, 3, baseB.ID)

	h.engine.mu.Lock()
	_, baseARetained := h.engine.observations[baseAID]
	poolHasA := baselineWithID(h.engine.baselines, baseAID) != nil
	h.engine.mu.Unlock()
	if baseARetained || poolHasA {
		t.Fatalf("retention fallback did not release backup A: retained=%t pooled=%t",
			baseARetained, poolHasA)
	}
}

func (h *harness) engineSessionDisplay(t *testing.T) device.Display {
	t.Helper()
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if h.engine.session == nil {
		t.Fatal("fixture has no active session")
	}
	return h.engine.session.Display
}
