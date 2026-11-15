package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/malyjacob/mobile-use-vnext/internal/eventlog"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// ------------------------------------------------------------- read-only fixtures

func readonlyStringPtr(value string) *string { return &value }

func readonlyTree(root *protocol.Node) *protocol.Tree {
	return &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root}
}

func readonlyNode(ref string, role protocol.Role, labels ...string) *protocol.Node {
	node := &protocol.Node{
		Ref: ref, Role: role,
		Bounds: &protocol.Bounds{Left: 0, Top: 0, Right: 10, Bottom: 10},
	}
	if len(labels) > 0 {
		node.Label = labels
	}
	return node
}

// attachObservation installs one retained observation fixture. A nil tree
// reproduces an evicted tree body with the header still present.
func attachObservation(t *testing.T, e *Engine, id, agent, status string, tree *protocol.Tree) {
	t.Helper()
	capturedAt := time.Now().Add(-time.Minute)
	obs := &observationState{
		ID: id, Origin: "Pfixture", Revision: 4, Agent: agent,
		CapturedAt: capturedAt, ActionableUntil: capturedAt.Add(time.Hour),
		Status: status,
	}
	if tree != nil {
		obs.Projection = &Projection{Tree: tree}
	}
	e.mu.Lock()
	e.observations[id] = obs
	e.mu.Unlock()
}

func inspectionMatchRefs(view *protocol.InspectionView) []string {
	refs := make([]string, 0, len(view.Matches))
	for _, match := range view.Matches {
		if match.Node != nil {
			refs = append(refs, match.Node.Ref)
		}
	}
	return refs
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func hasDiagnosisIssue(view *protocol.DiagnosisView, phase, code string) bool {
	for _, issue := range view.Issues {
		if issue.Phase == phase && issue.Code == code {
			return true
		}
	}
	return false
}

func parseDiagnosisTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("diagnosis time is not RFC3339: %v", err)
	}
	return parsed
}

func detailedTreeBytes(t *testing.T, root *protocol.Node) int {
	t.Helper()
	tree := observation.ProjectDetailedTree(readonlyTree(root))
	if tree == nil || tree.Validate() != nil {
		t.Fatalf("invalid detailed tree fixture")
	}
	encoded, err := protocol.JSON(tree)
	if err != nil {
		t.Fatalf("encode detailed tree: %v", err)
	}
	return len(encoded)
}

func detailedTreeWithSize(t *testing.T, size int) *protocol.Node {
	t.Helper()
	root := readonlyNode("n1", protocol.RoleRoot)
	child := readonlyNode("n2", protocol.RoleText, "x")
	root.Children = []*protocol.Node{child}
	oneByteLabel := detailedTreeBytes(t, root)
	if size <= oneByteLabel {
		t.Fatalf("detailed tree base is %d bytes, cannot reach %d", oneByteLabel, size)
	}
	child.Label[0] = strings.Repeat("x", 1+size-oneByteLabel)
	if got := detailedTreeBytes(t, root); got != size {
		t.Fatalf("detailed tree size = %d, want %d", got, size)
	}
	return root
}

func detailedSubtreeContentBytes(t *testing.T, node *protocol.Node) int {
	t.Helper()
	detailed := observation.ProjectDetailedNode(node)
	encoded, err := protocol.JSON(inspectionSubtreeContent{
		View: protocol.ViewDetailed, Root: detailed,
	})
	if err != nil {
		t.Fatalf("encode detailed subtree content: %v", err)
	}
	return len(encoded)
}

func detailedSubtreeWithSize(t *testing.T, size int) (*protocol.Node, string) {
	t.Helper()
	root := readonlyNode("n1", protocol.RoleRoot)
	child := readonlyNode("n2", protocol.RoleText, "x")
	root.Children = []*protocol.Node{child}
	oneByteLabel := detailedSubtreeContentBytes(t, child)
	if size <= oneByteLabel {
		t.Fatalf("detailed subtree base is %d bytes, cannot reach %d", oneByteLabel, size)
	}
	child.Label[0] = strings.Repeat("x", 1+size-oneByteLabel)
	if got := detailedSubtreeContentBytes(t, child); got != size {
		t.Fatalf("detailed subtree size = %d, want %d", got, size)
	}
	return root, child.Ref
}

// ------------------------------------------------------------- inspect

func TestInspectReturnsTreeAndSubtree(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	tree := currentTree(t, h.engine)

	result, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{ObservationID: start.Observation.ID})
	if apiErr != nil {
		t.Fatalf("inspect tree rejected: %v", apiErr)
	}
	if result.OperationID != "" || result.Outcome != "" {
		t.Fatalf("inspect must be read-only: %+v", result)
	}
	if result.IncludeSession || result.Session != nil || result.Cleanup != nil {
		t.Fatalf("inspect must carry no session/cleanup: %+v", result)
	}
	view := result.Inspection
	if view == nil || view.Scope != "tree" || view.Tree == nil || view.Tree.Root == nil {
		t.Fatalf("inspect tree view: %+v", view)
	}
	if view.ObservationID != start.Observation.ID || view.Revision != 1 || view.CapturedAt == "" {
		t.Fatalf("inspect header: %+v", view)
	}
	if view.Tree.Format != "full" || view.Tree.Scope != protocol.Scope {
		t.Fatalf("inspect tree header: %+v", view.Tree)
	}
	if view.Tree.Root.Ref != tree.Root.Ref {
		t.Fatalf("inspect tree root %q, want %q", view.Tree.Root.Ref, tree.Root.Ref)
	}

	button := findRole(tree.Root, protocol.RoleButton)
	if button == nil {
		t.Fatal("fixture button missing")
	}
	subtree, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: start.Observation.ID, Node: button.Ref, HasNode: true,
	})
	if apiErr != nil {
		t.Fatalf("inspect subtree rejected: %v", apiErr)
	}
	if subtree.Inspection.Scope != "subtree" || subtree.Inspection.Root == nil ||
		subtree.Inspection.Root.Ref != button.Ref {
		t.Fatalf("inspect subtree: %+v", subtree.Inspection)
	}

	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: start.Observation.ID, Node: "n404", HasNode: true,
	}); apiErr == nil || apiErr.Code != protocol.CodeNodeNotFound || apiErr.HTTPStatus != 409 {
		t.Fatalf("missing node must be a controlled 409: %+v", apiErr)
	}
}

func TestInspectQueryMatchesLabelValueHintResource(t *testing.T) {
	h := newHarness(t)
	root := readonlyNode("n1", protocol.RoleRoot)
	confirm := readonlyNode("n2", protocol.RoleButton, "确定")
	input := readonlyNode("n3", protocol.RoleInput, "姓名")
	input.Value = readonlyStringPtr("张三")
	input.Hint = readonlyStringPtr("请输入姓名")
	resource := readonlyNode("n4", protocol.RoleView)
	resource.Resource = readonlyStringPtr("com.example:id/确认")
	okButton := readonlyNode("n5", protocol.RoleText, "OK")
	german := readonlyNode("n6", protocol.RoleText, "Straße")
	root.Children = []*protocol.Node{confirm, input, resource, okButton, german}
	attachObservation(t, h.engine, "O901", "agent", ObservationRetired, readonlyTree(root))

	for _, tc := range []struct {
		query string
		refs  []string
	}{
		{"确", []string{"n2", "n4"}},
		{"姓名", []string{"n3"}},
		{"张", []string{"n3"}},
		{"ok", []string{"n5"}},
		{"strasse", []string{"n6"}},
		{"STRASSE", []string{"n6"}},
		{"没有任何命中", nil},
	} {
		result, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
			ObservationID: "O901", Query: tc.query, HasQuery: true,
		})
		if apiErr != nil {
			t.Fatalf("query %q rejected: %v", tc.query, apiErr)
		}
		view := result.Inspection
		if view.Scope != "query" || !view.HasTotal {
			t.Fatalf("query %q view: %+v", tc.query, view)
		}
		if view.Total != len(tc.refs) {
			t.Fatalf("query %q total %d, want %d", tc.query, view.Total, len(tc.refs))
		}
		if refs := inspectionMatchRefs(view); !equalStrings(refs, tc.refs) {
			t.Fatalf("query %q refs %v, want %v", tc.query, refs, tc.refs)
		}
		if view.Truncated == nil || *view.Truncated {
			t.Fatalf("query %q must report truncated=false", tc.query)
		}
		for _, match := range view.Matches {
			if len(match.Path) != 1 || match.Path[0] != "n1" {
				t.Fatalf("query %q path %v, want [n1]", tc.query, match.Path)
			}
		}
	}

	// Empty matches stay a JSON array, never null.
	result, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O901", Query: "没有", HasQuery: true,
	})
	if apiErr != nil {
		t.Fatalf("empty query result rejected: %v", apiErr)
	}
	if len(result.Inspection.Matches) != 0 || result.Inspection.Matches == nil {
		t.Fatalf("empty matches must be an empty array: %+v", result.Inspection)
	}
	encoded, err := protocol.JSON(result)
	if err != nil {
		t.Fatalf("encode result: %v", err)
	}
	if !strings.Contains(string(encoded), `"matches":[]`) {
		t.Fatalf("empty matches must encode as []")
	}
}

func TestInspectReadableForEveryRetainedStatus(t *testing.T) {
	h := newHarness(t)
	tree := readonlyTree(readonlyNode("n1", protocol.RoleRoot))
	for i, status := range []string{
		ObservationReady, ObservationClaimed, ObservationRetired, ObservationExpired,
	} {
		id := "O91" + string(rune('0'+i))
		attachObservation(t, h.engine, id, "agent", status, tree)
		result, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{ObservationID: id})
		if apiErr != nil {
			t.Fatalf("status %s rejected: %v", status, apiErr)
		}
		if result.Inspection == nil || result.Inspection.Scope != "tree" {
			t.Fatalf("status %s view: %+v", status, result.Inspection)
		}
	}
}

func TestInspectRejectsForeignMissingAndEvictedContent(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()

	if _, apiErr := h.engine.Inspect("other", &protocol.InspectRequest{
		ObservationID: start.Observation.ID,
	}); apiErr == nil || apiErr.Code != protocol.CodeObservationGone {
		t.Fatalf("foreign owner must be observation_not_found: %+v", apiErr)
	}
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O404",
	}); apiErr == nil || apiErr.Code != protocol.CodeObservationGone {
		t.Fatalf("missing observation must be observation_not_found: %+v", apiErr)
	}
	attachObservation(t, h.engine, "O902", "agent", ObservationRetired, nil)
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O902",
	}); apiErr == nil || apiErr.Code != protocol.CodeContentUnavailable ||
		apiErr.HTTPStatus != 409 {
		// inspect returns no operation, so its errors are access-layer errors:
		// they carry no recovery projection at all.
		t.Fatalf("evicted content must be content_unavailable/409: %+v", apiErr)
	}
}

func TestInspectBudgetsRejectOversizedUnits(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()

	h.engine.cfg.MaxFullTreeBytes = 64
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: start.Observation.ID,
	}); apiErr == nil || apiErr.Code != protocol.CodeInspectionTooLarge ||
		apiErr.Details["limit"] != 64 {
		t.Fatalf("full tree over budget: %+v", apiErr)
	}

	huge := readonlyNode("n1", protocol.RoleRoot)
	child := readonlyNode("n2", protocol.RoleText, strings.Repeat("宽", 20000))
	huge.Children = []*protocol.Node{child}
	attachObservation(t, h.engine, "O903", "agent", ObservationRetired, readonlyTree(huge))

	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O903", Node: "n2", HasNode: true,
	}); apiErr == nil || apiErr.Code != protocol.CodeInspectionTooLarge ||
		apiErr.Details["limit"] != h.engine.cfg.MaxInspectionUnitBytes {
		t.Fatalf("oversized subtree: %+v", apiErr)
	}
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O903", Query: "宽", HasQuery: true,
	}); apiErr == nil || apiErr.Code != protocol.CodeInspectionTooLarge {
		t.Fatalf("oversized query hit: %+v", apiErr)
	}
}

func TestInspectDetailedTreeBudgetBoundary(t *testing.T) {
	const limit = 128 * 1024
	h := newHarness(t)
	root := detailedTreeWithSize(t, limit)
	attachObservation(t, h.engine, "O905", "agent", ObservationRetired, readonlyTree(root))

	result, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O905",
	})
	if apiErr != nil {
		t.Fatalf("inspect tree at its exact budget: %v", apiErr)
	}
	encoded, err := protocol.JSON(result.Inspection.Tree)
	if err != nil {
		t.Fatalf("encode accepted detailed tree: %v", err)
	}
	if len(encoded) != limit {
		t.Fatalf("accepted detailed tree = %d bytes, want %d", len(encoded), limit)
	}

	h.engine.cfg.MaxFullTreeBytes = limit - 1
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O905",
	}); apiErr == nil || apiErr.Code != protocol.CodeInspectionTooLarge ||
		apiErr.Details["limit"] != limit-1 {
		t.Fatalf("detailed tree at N-1: %+v", apiErr)
	}
}

func TestInspectSubtreeBudgetBoundary(t *testing.T) {
	const limit = 32 * 1024
	h := newHarness(t)
	root, ref := detailedSubtreeWithSize(t, limit)
	attachObservation(t, h.engine, "O906", "agent", ObservationRetired, readonlyTree(root))

	result, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O906", Node: ref, HasNode: true,
	})
	if apiErr != nil {
		t.Fatalf("inspect subtree at its exact budget: %v", apiErr)
	}
	encoded, err := protocol.JSON(inspectionSubtreeContent{
		View: protocol.ViewDetailed, Root: result.Inspection.Root,
	})
	if err != nil {
		t.Fatalf("encode accepted detailed subtree: %v", err)
	}
	if len(encoded) != limit {
		t.Fatalf("accepted detailed subtree = %d bytes, want %d", len(encoded), limit)
	}

	h.engine.cfg.MaxInspectionUnitBytes = limit - 1
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O906", Node: ref, HasNode: true,
	}); apiErr == nil || apiErr.Code != protocol.CodeInspectionTooLarge ||
		apiErr.Details["limit"] != limit-1 {
		t.Fatalf("detailed subtree at N-1: %+v", apiErr)
	}
}

func TestInspectQueryUnitBudgetBoundary(t *testing.T) {
	h := newHarness(t)
	root := readonlyNode("n1", protocol.RoleRoot)
	hit := readonlyNode("n2", protocol.RoleText, "needle")
	root.Children = []*protocol.Node{hit}
	attachObservation(t, h.engine, "O907", "agent", ObservationRetired, readonlyTree(root))

	encodedHit, err := protocol.JSON(inspectMatchWire{
		Node: observation.ProjectDetailedNode(hit),
		Path: []string{"n1"},
	})
	if err != nil {
		t.Fatalf("encode query hit: %v", err)
	}
	base, err := queryEmptyBytes(1, false)
	if err != nil {
		t.Fatalf("measure query content header: %v", err)
	}
	limit := base + len(encodedHit)
	h.engine.cfg.MaxInspectionUnitBytes = limit
	result, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O907", Query: "needle", HasQuery: true,
	})
	if apiErr != nil {
		t.Fatalf("query hit at its exact budget: %v", apiErr)
	}
	if len(result.Inspection.Matches) != 1 || result.Inspection.Truncated == nil ||
		*result.Inspection.Truncated {
		t.Fatalf("single-hit query at N: %+v", result.Inspection)
	}

	h.engine.cfg.MaxInspectionUnitBytes = limit - 1
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O907", Query: "needle", HasQuery: true,
	}); apiErr == nil || apiErr.Code != protocol.CodeInspectionTooLarge ||
		apiErr.Details["limit"] != limit-1 {
		t.Fatalf("query hit at N-1: %+v", apiErr)
	}
}

func TestInspectQueryPrefixBudgetBoundary(t *testing.T) {
	const total = 4
	h := newHarness(t)
	root := readonlyNode("n1", protocol.RoleRoot)
	for index := 0; index < total; index++ {
		label := "prefix" + strings.Repeat("x", 90+index)
		root.Children = append(root.Children, readonlyNode(
			"n"+string(rune('2'+index)), protocol.RoleText, label,
		))
	}
	attachObservation(t, h.engine, "O908", "agent", ObservationRetired, readonlyTree(root))

	base, err := queryEmptyBytes(total, true)
	if err != nil {
		t.Fatalf("measure query content header: %v", err)
	}
	unitBytes := func(index int) int {
		encoded, err := protocol.JSON(inspectMatchWire{
			Node: observation.ProjectDetailedNode(root.Children[index]),
			Path: []string{root.Ref},
		})
		if err != nil {
			t.Fatalf("encode query hit %d: %v", index, err)
		}
		return len(encoded)
	}
	first, second := unitBytes(0), unitBytes(1)
	limit := base + first + 1 + second
	h.engine.cfg.MaxInspectionUnitBytes = limit
	result, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O908", Query: "prefix", HasQuery: true,
	})
	if apiErr != nil {
		t.Fatalf("query prefix at its exact budget: %v", apiErr)
	}
	view := result.Inspection
	if view.Total != total || len(view.Matches) != 2 || view.Truncated == nil ||
		!*view.Truncated {
		t.Fatalf("query prefix at N: total=%d matches=%d truncated=%v",
			view.Total, len(view.Matches), view.Truncated)
	}
	if refs := inspectionMatchRefs(view); !equalStrings(refs, []string{"n2", "n3"}) {
		t.Fatalf("query prefix refs = %v, want [n2 n3]", refs)
	}

	h.engine.cfg.MaxInspectionUnitBytes = limit - 1
	result, apiErr = h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O908", Query: "prefix", HasQuery: true,
	})
	if apiErr != nil {
		t.Fatalf("query prefix at N-1: %v", apiErr)
	}
	view = result.Inspection
	if view.Total != total || len(view.Matches) != 1 || view.Truncated == nil ||
		!*view.Truncated {
		t.Fatalf("query prefix at N-1: total=%d matches=%d truncated=%v",
			view.Total, len(view.Matches), view.Truncated)
	}
	if refs := inspectionMatchRefs(view); !equalStrings(refs, []string{"n2"}) {
		t.Fatalf("query prefix refs at N-1 = %v, want [n2]", refs)
	}
}

func TestInspectQueryTruncatesOnlyCompleteUnits(t *testing.T) {
	h := newHarness(t)
	root := readonlyNode("n1", protocol.RoleRoot)
	for i := 0; i < 400; i++ {
		root.Children = append(root.Children, readonlyNode(
			"n"+string(rune('a'+i%26))+string(rune('a'+i/26)),
			protocol.RoleText, "命中"+strings.Repeat("x", 120),
		))
	}
	attachObservation(t, h.engine, "O904", "agent", ObservationRetired, readonlyTree(root))

	result, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: "O904", Query: "命中", HasQuery: true,
	})
	if apiErr != nil {
		t.Fatalf("truncating query rejected: %v", apiErr)
	}
	view := result.Inspection
	if view.Total != 400 {
		t.Fatalf("total matches %d, want 400", view.Total)
	}
	if view.Truncated == nil || !*view.Truncated {
		t.Fatalf("expected truncated=true")
	}
	if len(view.Matches) == 0 || len(view.Matches) >= view.Total {
		t.Fatalf("expected a non-empty prefix, got %d of %d", len(view.Matches), view.Total)
	}
	base, err := queryEmptyBytes(view.Total, *view.Truncated)
	if err != nil {
		t.Fatalf("measure query response header: %v", err)
	}
	fullResponseBytes := base
	used := 0
	for index, match := range view.Matches {
		if match.Node == nil || len(match.Node.Label) != 1 ||
			!strings.HasPrefix(match.Node.Label[0], "命中") {
			t.Fatalf("truncation returned an incomplete unit")
		}
		encoded, err := protocol.JSON(inspectMatchWire{
			Node: match.Node, Path: append([]string{}, match.Path...),
		})
		if err != nil {
			t.Fatalf("encode unit: %v", err)
		}
		used += len(encoded)
		if index > 0 {
			fullResponseBytes++
		}
		fullResponseBytes += len(encoded)
	}
	if used > h.engine.cfg.MaxInspectionUnitBytes {
		t.Fatalf("returned units use %d bytes, budget %d", used, h.engine.cfg.MaxInspectionUnitBytes)
	}
	if fullResponseBytes > h.engine.cfg.MaxInspectionUnitBytes {
		t.Fatalf("complete query response uses %d bytes, budget %d",
			fullResponseBytes, h.engine.cfg.MaxInspectionUnitBytes)
	}
}

func TestInspectDoesNotChangeAuthority(t *testing.T) {
	h := newHarnessWithClock(t, false)
	start := h.startSession()
	expires := time.Now().Add(30 * time.Second)
	h.engine.mu.Lock()
	h.engine.session.ExpiresAt = expires
	obs := h.engine.observations[start.Observation.ID]
	until := obs.ActionableUntil
	operations := len(h.engine.operations)
	nextOperation := h.engine.nextOperation
	nextObservation := h.engine.nextObservation
	h.engine.mu.Unlock()

	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: start.Observation.ID,
	}); apiErr != nil {
		t.Fatalf("inspect tree: %v", apiErr)
	}
	if _, apiErr := h.engine.Inspect("agent", &protocol.InspectRequest{
		ObservationID: start.Observation.ID, Query: "ok", HasQuery: true,
	}); apiErr != nil {
		t.Fatalf("inspect query: %v", apiErr)
	}

	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if !h.engine.session.ExpiresAt.Equal(expires) {
		t.Fatalf("inspect renewed the lease")
	}
	if h.engine.observations[start.Observation.ID].Status != ObservationReady {
		t.Fatalf("inspect changed observation status")
	}
	if !h.engine.observations[start.Observation.ID].ActionableUntil.Equal(until) {
		t.Fatalf("inspect changed actionable_until")
	}
	if len(h.engine.operations) != operations || h.engine.nextOperation != nextOperation ||
		h.engine.nextObservation != nextObservation {
		t.Fatalf("inspect allocated a new operation or observation")
	}
	if h.engine.work != nil || h.engine.baseline != nil {
		t.Fatalf("inspect took work or acknowledged a baseline")
	}
}

// ------------------------------------------------------------- keepalive

func TestKeepaliveRenewsSessionOnly(t *testing.T) {
	h := newHarnessWithClock(t, false)
	start := h.startSession()
	before := time.Now().Add(5 * time.Second)
	h.engine.mu.Lock()
	h.engine.session.ExpiresAt = before
	obs := h.engine.observations[start.Observation.ID]
	until := obs.ActionableUntil
	h.engine.mu.Unlock()

	result, apiErr := h.engine.Keepalive("agent", "PK1", &protocol.KeepaliveRequest{
		Reference: start.Observation.ID,
	})
	if apiErr != nil {
		t.Fatalf("keepalive rejected: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted || !result.IncludeSession || result.Session == nil {
		t.Fatalf("keepalive result: %+v", result)
	}
	if result.Session.State != SessionActive || result.Session.ExpiresAt == "" {
		t.Fatalf("keepalive session view: %+v", result.Session)
	}
	expires, err := time.Parse(time.RFC3339Nano, result.Session.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at is not RFC3339: %v", err)
	}
	if !expires.After(before) {
		t.Fatalf("keepalive did not extend the lease")
	}

	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if !h.engine.session.ExpiresAt.Equal(expires) {
		t.Fatalf("result expires_at does not match the stored lease")
	}
	if !obs.ActionableUntil.Equal(until) {
		t.Fatalf("keepalive changed observation actionable_until")
	}
	if obs.Status != ObservationReady {
		t.Fatalf("keepalive changed observation status: %s", obs.Status)
	}
	op := h.engine.operations["PK1"]
	if op == nil || op.State != OperationFinished || op.Phase != "renewed" ||
		op.Outcome != OutcomeExecuted {
		t.Fatalf("keepalive operation: %+v", op)
	}
}

func TestKeepaliveDuplicateDoesNotExtendAgain(t *testing.T) {
	h := newHarnessWithClock(t, false)
	start := h.startSession()
	if _, apiErr := h.engine.Keepalive("agent", "PKdup", &protocol.KeepaliveRequest{
		Reference: start.Observation.ID,
	}); apiErr != nil {
		t.Fatalf("first keepalive rejected: %v", apiErr)
	}
	frozen := time.Now().Add(30 * time.Second)
	h.engine.mu.Lock()
	h.engine.session.ExpiresAt = frozen
	h.engine.mu.Unlock()

	duplicate, apiErr := h.engine.Keepalive("agent", "PKdup", &protocol.KeepaliveRequest{
		Reference: start.OperationID,
	})
	if apiErr != nil {
		t.Fatalf("duplicate keepalive rejected: %v", apiErr)
	}
	if duplicate.OperationID != "PKdup" || duplicate.OperationState != OperationFinished ||
		duplicate.Outcome != OutcomeExecuted {
		t.Fatalf("duplicate view: %+v", duplicate)
	}
	if duplicate.Session == nil {
		t.Fatalf("duplicate view must keep the session fact")
	}
	expires, err := time.Parse(time.RFC3339Nano, duplicate.Session.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at is not RFC3339: %v", err)
	}
	if !expires.Equal(frozen) {
		t.Fatalf("duplicate keepalive extended the lease")
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if !h.engine.session.ExpiresAt.Equal(frozen) {
		t.Fatalf("duplicate keepalive mutated the lease")
	}
}

func TestKeepaliveWhileWorkInProgress(t *testing.T) {
	h := newHarnessWithClock(t, false)
	start := h.startSession()
	h.engine.mu.Lock()
	work := &workState{
		OperationID: "Pblock", Agent: "agent", Kind: WorkObserve, Phase: "capture",
		SessionEpoch: h.engine.session.Epoch, Start: time.Now(),
		Ctx: context.Background(), Cancel: func() {},
	}
	h.engine.work = work
	h.engine.session.ExpiresAt = time.Now().Add(3 * time.Second)
	h.engine.mu.Unlock()
	defer func() {
		h.engine.mu.Lock()
		if h.engine.work == work {
			h.engine.work = nil
		}
		h.engine.mu.Unlock()
	}()

	result, apiErr := h.engine.Keepalive("agent", "PKbusy", &protocol.KeepaliveRequest{
		Reference: start.Observation.ID,
	})
	if apiErr != nil {
		t.Fatalf("keepalive must not require the work position: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted {
		t.Fatalf("keepalive result: %+v", result)
	}
	if !h.engine.IsBusy() {
		t.Fatalf("keepalive released the ordinary work position")
	}
}

func TestKeepaliveRejectsForeignUnknownAndNoSession(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()

	result, apiErr := h.engine.Keepalive("other", "", &protocol.KeepaliveRequest{
		Reference: start.Observation.ID,
	})
	if apiErr != nil || result.Error == nil || result.Error.Code != protocol.CodeObservationGone {
		t.Fatalf("foreign O: apiErr=%v result=%+v", apiErr, result)
	}
	result, apiErr = h.engine.Keepalive("other", "", &protocol.KeepaliveRequest{
		Reference: start.OperationID,
	})
	if apiErr != nil || result.Error == nil || result.Error.Code != protocol.CodeOperationNotFound {
		t.Fatalf("foreign P: apiErr=%v result=%+v", apiErr, result)
	}
	result, apiErr = h.engine.Keepalive("agent", "", &protocol.KeepaliveRequest{
		Reference: "Pnever",
	})
	if apiErr != nil || result.Error == nil || result.Error.Code != protocol.CodeOperationUnknown {
		t.Fatalf("unknown P: apiErr=%v result=%+v", apiErr, result)
	}

	// A bare engine has no session at all: both O and P references report the
	// controlled no-session fact instead of inventing a lease.
	bare := New(DefaultConfig("instance-test", "agent"), nil, newFakeDevice(),
		&fakeStore{}, &testProjector{builder: observation.NewBuilder()}, testDiffer{})
	attachObservation(t, bare, "O920", "agent", ObservationRetired,
		readonlyTree(readonlyNode("n1", protocol.RoleRoot)))
	bare.mu.Lock()
	bare.operations["P920"] = &operationState{
		ID: "P920", Agent: "agent", Kind: "notify",
		State: OperationFinished, Outcome: OutcomeExecuted,
	}
	bare.mu.Unlock()
	for _, reference := range []string{"O920", "P920"} {
		res, apiErr := bare.Keepalive("agent", "", &protocol.KeepaliveRequest{Reference: reference})
		if apiErr != nil {
			t.Fatalf("no-session keepalive %s: %v", reference, apiErr)
		}
		if res.Error == nil || res.Error.Code != protocol.CodeReferenceNoSession {
			t.Fatalf("no-session keepalive %s: %+v", reference, res)
		}
		requireRecovery(t, res, protocol.RecoveryEntryStatus, protocol.RecoveryReferenceP, "", "")
	}
}

func TestKeepaliveRejectsEndedAndExpiredSession(t *testing.T) {
	t.Run("ended", func(t *testing.T) {
		h := newHarness(t)
		start := h.startSession()
		if _, apiErr := h.engine.End("agent", "", start.OperationID); apiErr != nil {
			t.Fatalf("end rejected: %v", apiErr)
		}
		result, apiErr := h.engine.Keepalive("agent", "", &protocol.KeepaliveRequest{
			Reference: start.OperationID,
		})
		if apiErr != nil {
			t.Fatalf("keepalive transport error: %v", apiErr)
		}
		if result.Error == nil || result.Error.Code != protocol.CodeSessionEnded {
			t.Fatalf("ended session keepalive: %+v", result)
		}
		requireRecovery(t, result, protocol.RecoveryEntryEnd, protocol.RecoveryReferenceP, "", "")
		if result.Session == nil || result.Session.State != SessionEnded {
			t.Fatalf("ended session view: %+v", result.Session)
		}
	})

	t.Run("expired", func(t *testing.T) {
		h := newHarnessWithClock(t, false)
		start := h.startSession()
		h.engine.mu.Lock()
		h.engine.session.ExpiresAt = time.Now().Add(-time.Second)
		h.engine.mu.Unlock()
		result, apiErr := h.engine.Keepalive("agent", "", &protocol.KeepaliveRequest{
			Reference: start.Observation.ID,
		})
		if apiErr != nil {
			t.Fatalf("keepalive transport error: %v", apiErr)
		}
		if result.Error == nil || result.Error.Code != protocol.CodeSessionExpired {
			t.Fatalf("expired session keepalive: %+v", result)
		}
	})
}

func TestKeepaliveRejectedDuringShutdown(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	h.engine.Shutdown(ctx)
	_, apiErr := h.engine.Keepalive("agent", "", &protocol.KeepaliveRequest{
		Reference: start.Observation.ID,
	})
	if apiErr == nil || apiErr.Code != protocol.CodeDeviceUnavailable || apiErr.HTTPStatus != 503 {
		t.Fatalf("shutdown keepalive: %+v", apiErr)
	}
}

// ------------------------------------------------------------- diagnose

func TestDiagnoseOwnCleanState(t *testing.T) {
	h := newHarness(t)
	result, apiErr := h.engine.Diagnose("agent", &protocol.DiagnoseRequest{})
	if apiErr != nil {
		t.Fatalf("diagnose rejected: %v", apiErr)
	}
	if result.OperationID != "" || result.Error != nil {
		t.Fatalf("diagnose must not fabricate an operation or error: %+v", result)
	}
	view := result.Diagnosis
	if view == nil || view.Coverage != "own_records" || view.Summary != "未记录到近期问题" {
		t.Fatalf("clean diagnosis: %+v", view)
	}
	if len(view.Issues) != 0 || len(view.Timeline) != 0 || view.Truncated {
		t.Fatalf("clean diagnosis carried entries: %+v", view)
	}
	if _, ok := view.TimingMS["since_start"]; !ok {
		t.Fatalf("clean diagnosis is missing since_start: %+v", view.TimingMS)
	}
}

func TestDiagnoseReportsOwnRecentFailure(t *testing.T) {
	h := newHarness(t)
	h.startSession()
	failed, apiErr := h.engine.Observe(context.Background(), "agent", "Pbad",
		&protocol.ObserveRequest{Reference: "Pmissing"})
	if apiErr != nil {
		t.Fatalf("observe transport error: %v", apiErr)
	}
	if failed.Error == nil || failed.Error.Code != protocol.CodeOperationUnknown {
		t.Fatalf("fixture failure: %+v", failed)
	}

	result, apiErr := h.engine.Diagnose("agent", &protocol.DiagnoseRequest{})
	if apiErr != nil {
		t.Fatalf("diagnose rejected: %v", apiErr)
	}
	view := result.Diagnosis
	if view.Coverage != "own_records" || view.Summary != "记录到近期问题" {
		t.Fatalf("own diagnosis: %+v", view)
	}
	if !hasDiagnosisIssue(view, "admission", protocol.CodeOperationUnknown) {
		t.Fatalf("failure issue missing: %+v", view.Issues)
	}
	if len(view.Timeline) == 0 {
		t.Fatalf("failure timeline missing")
	}
	for _, key := range []string{"since_start"} {
		if _, ok := view.TimingMS[key]; !ok {
			t.Fatalf("timing %s missing: %+v", key, view.TimingMS)
		}
	}

	// Other agents never see those records.
	other, apiErr := h.engine.Diagnose("other", &protocol.DiagnoseRequest{})
	if apiErr != nil {
		t.Fatalf("foreign diagnose rejected: %v", apiErr)
	}
	if other.Diagnosis.Coverage != "own_records" || hasDiagnosisIssue(other.Diagnosis, "admission", protocol.CodeOperationUnknown) {
		t.Fatalf("foreign diagnose leaked records: %+v", other.Diagnosis)
	}
}

func TestDiagnoseReferenceCoverage(t *testing.T) {
	h := newHarness(t)
	start := h.startSession()

	failed, apiErr := h.engine.Start(context.Background(), "agent", "Pstart2")
	if apiErr != nil {
		t.Fatalf("start transport error: %v", apiErr)
	}
	if failed.Outcome != OutcomeNotExecuted {
		t.Fatalf("fixture start should be rejected: %+v", failed)
	}

	byOperation, apiErr := h.engine.Diagnose("agent", &protocol.DiagnoseRequest{
		Reference: "Pstart2", HasRef: true,
	})
	if apiErr != nil {
		t.Fatalf("diagnose operation rejected: %v", apiErr)
	}
	view := byOperation.Diagnosis
	if view.Coverage != "original_record" || view.Operation != "Pstart2" ||
		view.Outcome != OutcomeNotExecuted {
		t.Fatalf("operation diagnosis: %+v", view)
	}
	if !hasDiagnosisIssue(view, "admission", protocol.CodeSessionExists) {
		t.Fatalf("operation issue missing: %+v", view.Issues)
	}
	if _, ok := view.TimingMS["operation"]; !ok {
		t.Fatalf("operation timing missing: %+v", view.TimingMS)
	}
	if view.Summary != "原操作记录存在未完成问题" {
		t.Fatalf("operation summary: %q", view.Summary)
	}

	byObservation, apiErr := h.engine.Diagnose("agent", &protocol.DiagnoseRequest{
		Reference: start.Observation.ID, HasRef: true,
	})
	if apiErr != nil {
		t.Fatalf("diagnose observation rejected: %v", apiErr)
	}
	if byObservation.Diagnosis.Coverage != "original_record" ||
		byObservation.Diagnosis.Operation != "" ||
		byObservation.Diagnosis.Summary != "原观察记录可用" {
		t.Fatalf("observation diagnosis: %+v", byObservation.Diagnosis)
	}

	unknown, apiErr := h.engine.Diagnose("agent", &protocol.DiagnoseRequest{
		Reference: "Pnever", HasRef: true,
	})
	if apiErr != nil {
		t.Fatalf("unknown diagnose rejected: %v", apiErr)
	}
	if unknown.Diagnosis.Coverage != "record_unavailable" ||
		unknown.Diagnosis.Summary != "未找到可用的原记录" ||
		len(unknown.Diagnosis.Issues) != 0 || unknown.Diagnosis.Truncated {
		t.Fatalf("unknown diagnosis: %+v", unknown.Diagnosis)
	}

	foreign, apiErr := h.engine.Diagnose("other", &protocol.DiagnoseRequest{
		Reference: start.OperationID, HasRef: true,
	})
	if apiErr != nil {
		t.Fatalf("foreign diagnose rejected: %v", apiErr)
	}
	if foreign.Diagnosis.Coverage != "record_unavailable" ||
		foreign.Diagnosis.Operation != "" {
		t.Fatalf("foreign diagnosis leaked records: %+v", foreign.Diagnosis)
	}
}

func TestDiagnoseBudgetDropsOldestAndFlagsTruncation(t *testing.T) {
	view := &protocol.DiagnosisView{
		Summary:  "记录到近期问题",
		Coverage: "own_records",
		TimingMS: map[string]int64{"since_start": 1},
	}
	for i := 0; i < 12; i++ {
		view.Issues = append(view.Issues, protocol.DiagnosisIssue{
			Phase: "admission", Code: protocol.CodeDeviceBusy,
		})
		view.Timeline = append(view.Timeline, protocol.DiagnosisEvent{
			At: "2026-11-09T00:00:00Z", Phase: "observe", Code: protocol.CodeDeviceBusy,
		})
	}
	trimDiagnosisBudget(view, 300)
	encoded, err := protocol.JSON(view)
	if err != nil {
		t.Fatalf("encode diagnosis: %v", err)
	}
	if len(encoded) > 300 {
		t.Fatalf("diagnosis uses %d bytes, budget 300", len(encoded))
	}
	if !view.Truncated {
		t.Fatalf("trimmed diagnosis must report truncated=true")
	}
	if len(view.Timeline) != 0 {
		t.Fatalf("oldest timeline events must be dropped first: %d left", len(view.Timeline))
	}
	if len(view.Issues) == 0 || len(view.Issues) >= 12 {
		t.Fatalf("issues were not bounded: %d", len(view.Issues))
	}

	small := &protocol.DiagnosisView{Summary: "原记录可用", Coverage: "own_records"}
	trimDiagnosisBudget(small, maxDiagnosisBytes)
	if small.Truncated {
		t.Fatalf("a small diagnosis must not be truncated")
	}
}

func TestDiagnoseTimelineIsOrdered(t *testing.T) {
	h := newHarness(t)
	h.startSession()
	for i := 0; i < 3; i++ {
		if _, apiErr := h.engine.Observe(context.Background(), "agent",
			"Pf"+string(rune('a'+i)), &protocol.ObserveRequest{Reference: "Pmissing"}); apiErr != nil {
			t.Fatalf("fixture observe transport error: %v", apiErr)
		}
	}
	result, apiErr := h.engine.Diagnose("agent", &protocol.DiagnoseRequest{})
	if apiErr != nil {
		t.Fatalf("diagnose rejected: %v", apiErr)
	}
	previous := time.Time{}
	for i, event := range result.Diagnosis.Timeline {
		at := parseDiagnosisTime(t, event.At)
		if i > 0 && at.Before(previous) {
			t.Fatalf("timeline is not oldest-first at %d", i)
		}
		previous = at
	}
}

// ------------------------------------------------------------- notify

func TestNotifySavesWithoutSession(t *testing.T) {
	eng := New(DefaultConfig("instance-test", "agent"), nil, newFakeDevice(),
		&fakeStore{}, &testProjector{builder: observation.NewBuilder()}, testDiffer{})
	result, apiErr := eng.Notify("agent", "", &protocol.NotifyRequest{Text: "第一条"})
	if apiErr != nil {
		t.Fatalf("notify rejected: %v", apiErr)
	}
	if result.Outcome != OutcomeExecuted || result.MessageID != "M1" ||
		result.Saved == nil || !*result.Saved {
		t.Fatalf("notify result: %+v", result)
	}
	if result.Notification == nil || result.Notification.State != "pending" ||
		result.Notification.Error != nil {
		t.Fatalf("notify notification branch: %+v", result.Notification)
	}
	if result.Session != nil || result.IncludeSession || result.Cleanup != nil {
		t.Fatalf("notify must not attach a session or cleanup")
	}
	second, apiErr := eng.Notify("agent", "", &protocol.NotifyRequest{Text: "第二条"})
	if apiErr != nil {
		t.Fatalf("second notify rejected: %v", apiErr)
	}
	if second.MessageID != "M2" {
		t.Fatalf("message ids must be monotonic, got %q", second.MessageID)
	}
	eng.mu.Lock()
	count := len(eng.messages)
	eng.mu.Unlock()
	if count != 2 {
		t.Fatalf("stored messages %d, want 2", count)
	}
}

func TestNotifyDuplicatePSavesOnce(t *testing.T) {
	eng := New(DefaultConfig("instance-test", "agent"), nil, newFakeDevice(),
		&fakeStore{}, &testProjector{builder: observation.NewBuilder()}, testDiffer{})
	first, apiErr := eng.Notify("agent", "PNdup", &protocol.NotifyRequest{Text: "第一条"})
	if apiErr != nil {
		t.Fatalf("first notify rejected: %v", apiErr)
	}
	if first.MessageID != "M1" {
		t.Fatalf("first message id %q", first.MessageID)
	}
	duplicate, apiErr := eng.Notify("agent", "PNdup", &protocol.NotifyRequest{Text: "第二条"})
	if apiErr != nil {
		t.Fatalf("duplicate notify rejected: %v", apiErr)
	}
	if duplicate.OperationID != "PNdup" || duplicate.OperationState != OperationFinished ||
		duplicate.Outcome != OutcomeExecuted {
		t.Fatalf("duplicate view: %+v", duplicate)
	}
	eng.mu.Lock()
	count := len(eng.messages)
	storedID := ""
	if count == 1 {
		storedID = eng.messages[0].ID
	}
	eng.mu.Unlock()
	if count != 1 || storedID != "M1" {
		t.Fatalf("duplicate P saved a second message: count=%d id=%q", count, storedID)
	}
}

func TestNotifyCapacityStaysBounded(t *testing.T) {
	eng := New(DefaultConfig("instance-test", "agent"), nil, newFakeDevice(),
		&fakeStore{}, &testProjector{builder: observation.NewBuilder()}, testDiffer{})
	for i := 0; i < 300; i++ {
		if _, apiErr := eng.Notify("agent", "", &protocol.NotifyRequest{Text: "x"}); apiErr != nil {
			t.Fatalf("notify %d rejected: %v", i, apiErr)
		}
	}
	eng.mu.Lock()
	count := len(eng.messages)
	firstID := eng.messages[0].ID
	lastID := eng.messages[count-1].ID
	eng.mu.Unlock()
	if count != maxStoredMessages {
		t.Fatalf("message count %d, want %d", count, maxStoredMessages)
	}
	if firstID != "M45" || lastID != "M300" {
		t.Fatalf("eviction kept %q..%q", firstID, lastID)
	}

	// Repeated large messages must hit the byte bound, not only the count bound.
	large := New(DefaultConfig("instance-test", "agent"), nil, newFakeDevice(),
		&fakeStore{}, &testProjector{builder: observation.NewBuilder()}, testDiffer{})
	long := strings.Repeat("字", protocol.MaxTextRunes)
	for i := 0; i < 90; i++ {
		if _, apiErr := large.Notify("agent", "", &protocol.NotifyRequest{Text: long}); apiErr != nil {
			t.Fatalf("large notify %d rejected: %v", i, apiErr)
		}
	}
	large.mu.Lock()
	count = len(large.messages)
	bytes := large.messageBytes
	lastID = large.messages[count-1].ID
	large.mu.Unlock()
	if bytes > maxStoredMessageBytes || count >= 90 {
		t.Fatalf("byte bound not enforced: count=%d bytes=%d", count, bytes)
	}
	if lastID != "M90" {
		t.Fatalf("last message id %q, want M90", lastID)
	}
}

func TestNotifyRejectsEmptyAndOversizedText(t *testing.T) {
	eng := New(DefaultConfig("instance-test", "agent"), nil, newFakeDevice(),
		&fakeStore{}, &testProjector{builder: observation.NewBuilder()}, testDiffer{})
	empty, apiErr := eng.Notify("agent", "", &protocol.NotifyRequest{Text: ""})
	if apiErr != nil {
		t.Fatalf("empty notify transport error: %v", apiErr)
	}
	if empty.Error == nil || empty.Error.Code != protocol.CodeInvalidRequest ||
		empty.Outcome != OutcomeNotExecuted {
		t.Fatalf("empty text result: %+v", empty)
	}
	overlong, apiErr := eng.Notify("agent", "", &protocol.NotifyRequest{
		Text: strings.Repeat("x", protocol.MaxTextRunes+1),
	})
	if apiErr != nil {
		t.Fatalf("overlong notify transport error: %v", apiErr)
	}
	if overlong.Error == nil || overlong.Error.Code != protocol.CodeInvalidRequest {
		t.Fatalf("overlong text result: %+v", overlong)
	}
	eng.mu.Lock()
	count := len(eng.messages)
	eng.mu.Unlock()
	if count != 0 {
		t.Fatalf("invalid text stored %d messages", count)
	}
}

func TestNotifyPendingIsIndependentOfAppState(t *testing.T) {
	h := newHarness(t)
	h.engine.mu.Lock()
	h.engine.app.Notifications = false
	h.engine.mu.Unlock()
	disabled, apiErr := h.engine.Notify("agent", "", &protocol.NotifyRequest{Text: "提醒"})
	if apiErr != nil {
		t.Fatalf("disabled notify rejected: %v", apiErr)
	}
	if disabled.Saved == nil || !*disabled.Saved || disabled.Notification == nil ||
		disabled.Notification.State != "pending" || disabled.Notification.Error != nil {
		t.Fatalf("disabled app must not change the saved/queued fact: %+v", disabled)
	}

	h.engine.mu.Lock()
	h.engine.app.Notifications = true
	h.engine.app.LastSync = time.Now().Add(-time.Minute)
	h.engine.mu.Unlock()
	stale, apiErr := h.engine.Notify("agent", "", &protocol.NotifyRequest{Text: "提醒"})
	if apiErr != nil {
		t.Fatalf("stale notify rejected: %v", apiErr)
	}
	if stale.Saved == nil || !*stale.Saved || stale.Notification == nil ||
		stale.Notification.State != "pending" || stale.Notification.Error != nil {
		t.Fatalf("stale app must not change the saved/queued fact: %+v", stale)
	}
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer strings.Builder
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestNotifyNeverLogsMessageText(t *testing.T) {
	out := &lockedBuffer{}
	logger := eventlog.New(out, "instance-test")
	eng := New(DefaultConfig("instance-test", "agent"), logger, newFakeDevice(),
		&fakeStore{}, &testProjector{builder: observation.NewBuilder()}, testDiffer{})
	secret := "敏感消息-不要出现在日志"
	result, apiErr := eng.Notify("agent", "", &protocol.NotifyRequest{Text: secret})
	if apiErr != nil {
		t.Fatalf("notify rejected: %v", apiErr)
	}
	if result.Error != nil {
		t.Fatalf("notify returned an unexpected error: %+v", result.Error)
	}
	if result.Notification == nil || result.Notification.State != "pending" ||
		result.Notification.Error != nil {
		t.Fatalf("notification branch must only state the queued fact: %+v", result.Notification)
	}
	logger.Close()
	logged := out.String()
	if logged == "" {
		t.Fatalf("expected a bounded notify event")
	}
	if strings.Contains(logged, secret) || strings.Contains(logged, "敏感消息") {
		t.Fatalf("log leaked the message text")
	}
	if !strings.Contains(logged, "notify.saved") {
		t.Fatalf("bounded notify event missing")
	}
	eng.mu.Lock()
	count := len(eng.messages)
	eng.mu.Unlock()
	if count != 1 {
		t.Fatalf("stored messages %d, want 1", count)
	}
}
