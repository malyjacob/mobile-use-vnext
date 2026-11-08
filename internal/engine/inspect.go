package engine

import (
	"strings"
	"time"
	"unicode"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// inspectionUnitBytes is the fixed local budget for one subtree or one query
// hit. It is service policy, not a per-call option: too large a unit is a
// controlled error instead of a silently cropped node.
const inspectionUnitBytes = 32 * 1024

// inspectionSource is the immutable reading material of one retained
// observation. It is copied out under the state lock; the tree it points at is
// immutable, so traversal and encoding happen without holding e.mu.
type inspectionSource struct {
	id         string
	origin     string
	revision   int
	capturedAt time.Time
	root       *protocol.Node
}

// inspectMatch is one complete matched subtree and its ancestor ref path.
type inspectMatch struct {
	node *protocol.Node
	path []string
}

// inspectMatchWire mirrors the public query hit shape so byte budgeting uses
// exactly the canonical JSON produced later by InspectionView.
type inspectMatchWire struct {
	Node *protocol.Node `json:"node"`
	Path []string       `json:"path"`
}

// Inspect returns historical reading material for one retained observation. It
// performs no device I/O, takes no work position, renews no lease, confirms no
// baseline and allocates no observation or operation.
func (e *Engine) Inspect(agent string, req *protocol.InspectRequest) (*protocol.Result, *protocol.APIError) {
	if req == nil {
		return nil, protocol.Invalid("inspect 请求缺失")
	}
	if req.HasNode && req.HasQuery {
		return nil, protocol.Invalid("node 与 query 不能同时出现")
	}
	if req.HasQuery && req.Query == "" {
		return nil, protocol.Invalid("query 不能为空")
	}
	source, apiErr := e.inspectionSource(agent, req.ObservationID)
	if apiErr != nil {
		return nil, apiErr
	}
	view := &protocol.InspectionView{
		ObservationID: source.id,
		Origin:        source.origin,
		Revision:      source.revision,
		CapturedAt:    formatTime(source.capturedAt),
	}
	switch {
	case req.HasQuery:
		if apiErr := inspectionQuery(view, source.root, req.Query); apiErr != nil {
			return nil, apiErr
		}
	case req.HasNode:
		if apiErr := inspectionSubtree(view, source.root, req.Node); apiErr != nil {
			return nil, apiErr
		}
	default:
		if apiErr := e.inspectionTree(view, source.root); apiErr != nil {
			return nil, apiErr
		}
	}
	return &protocol.Result{Inspection: view}, nil
}

// inspectionSource resolves one observation by id and ownership. Every status
// (ready/claimed/retired/expired) stays readable while its tree is retained.
func (e *Engine) inspectionSource(agent, observationID string) (*inspectionSource, *protocol.APIError) {
	e.mu.Lock()
	defer e.mu.Unlock()
	obs := e.observations[observationID]
	if obs == nil || obs.Agent != agent {
		return nil, protocol.NewError(409, protocol.CodeObservationGone, "原观察不可用")
	}
	tree := obs.tree()
	if tree == nil || tree.Root == nil {
		return nil, protocol.NewError(409, protocol.CodeContentUnavailable, "原观察内容已不可用")
	}
	return &inspectionSource{
		id: obs.ID, origin: obs.Origin, revision: obs.Revision,
		capturedAt: obs.CapturedAt, root: tree.Root,
	}, nil
}

// inspectionTree returns the complete public tree within the full-tree budget.
func (e *Engine) inspectionTree(view *protocol.InspectionView, root *protocol.Node) *protocol.APIError {
	tree := &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: root}
	encoded, err := protocol.JSON(tree)
	if err != nil {
		return protocol.Internal()
	}
	if len(encoded) > e.cfg.MaxFullTreeBytes {
		return inspectionTooLarge(e.cfg.MaxFullTreeBytes)
	}
	view.Scope = "tree"
	view.Tree = tree
	return nil
}

// inspectionSubtree returns one complete subtree rooted at an original public
// ref. A missing ref is a controlled not-found error, never a partial node.
func inspectionSubtree(view *protocol.InspectionView, root *protocol.Node, ref string) *protocol.APIError {
	node := findPublicNode(root, ref)
	if node == nil {
		return protocol.NewError(409, protocol.CodeNodeNotFound, "原节点不在该观察中")
	}
	encoded, err := protocol.JSON(node)
	if err != nil {
		return protocol.Internal()
	}
	if len(encoded) > inspectionUnitBytes {
		return inspectionTooLarge(inspectionUnitBytes)
	}
	view.Scope = "subtree"
	view.Root = node
	return nil
}

// inspectionQuery matches the literal query over label/value/hint/resource and
// returns complete matched subtrees in original tree order. Only a prefix of
// complete units is returned when the budget forces truncation.
func inspectionQuery(view *protocol.InspectionView, root *protocol.Node, query string) *protocol.APIError {
	matches := collectInspectMatches(root, foldText(query))
	sizes := make([]int, len(matches))
	for i, match := range matches {
		encoded, err := protocol.JSON(inspectMatchWire{
			Node: match.node,
			Path: append([]string{}, match.path...),
		})
		if err != nil {
			return protocol.Internal()
		}
		if len(encoded) > inspectionUnitBytes {
			return inspectionTooLarge(inspectionUnitBytes)
		}
		sizes[i] = len(encoded)
	}
	included, used := 0, 0
	for i := range matches {
		if used+sizes[i] > inspectionUnitBytes {
			break
		}
		used += sizes[i]
		included++
	}
	truncated := included < len(matches)
	view.Scope = "query"
	view.Matches = make([]protocol.InspectionMatch, 0, included)
	for _, match := range matches[:included] {
		view.Matches = append(view.Matches, protocol.InspectionMatch{
			Node: match.node,
			Path: append([]string{}, match.path...),
		})
	}
	view.Total = len(matches)
	view.HasTotal = true
	view.Truncated = &truncated
	return nil
}

// collectInspectMatches walks the tree in preorder, so matches keep original
// tree order. The path is root..parent of the matched node.
func collectInspectMatches(root *protocol.Node, foldedQuery string) []inspectMatch {
	var matches []inspectMatch
	var walk func(node *protocol.Node, path []string)
	walk = func(node *protocol.Node, path []string) {
		if node == nil {
			return
		}
		if nodeMatchesQuery(node, foldedQuery) {
			matches = append(matches, inspectMatch{node: node, path: append([]string{}, path...)})
		}
		for _, child := range node.Children {
			walk(child, append(path, node.Ref))
		}
	}
	walk(root, nil)
	return matches
}

// nodeMatchesQuery applies one literal casefolded substring test per field.
func nodeMatchesQuery(node *protocol.Node, foldedQuery string) bool {
	for _, label := range node.Label {
		if strings.Contains(foldText(label), foldedQuery) {
			return true
		}
	}
	for _, field := range []*string{node.Value, node.Hint, node.Resource} {
		if field != nil && strings.Contains(foldText(*field), foldedQuery) {
			return true
		}
	}
	return false
}

// inspectionTooLarge reports one required unit that cannot fit its budget.
func inspectionTooLarge(limit int) *protocol.APIError {
	return protocol.NewError(409, protocol.CodeInspectionTooLarge, "所需内容超过补看预算").
		WithDetail("limit", limit)
}

// foldExpansions covers the common full-casefold expansions that simple case
// folding cannot express (Latin, ligatures and the Armenian ligatures). The
// replacement text is already in the lowercase form produced by foldRune.
var foldExpansions = map[rune]string{
	'ß': "ss", 'ẞ': "ss", 'İ': "i\u0307",
	'ŉ': "\u02bcn", 'ǰ': "j\u030c",
	'ẖ': "h\u0331", 'ẗ': "t\u0308", 'ẘ': "w\u030a", 'ẙ': "y\u030a",
	'ẚ': "a\u02be",
	'ﬀ': "ff", 'ﬁ': "fi", 'ﬂ': "fl", 'ﬃ': "ffi", 'ﬄ': "ffl",
	'ﬅ': "st", 'ﬆ': "st",
	'ﬓ': "մն", 'ﬔ': "մե", 'ﬕ': "մի", 'ﬖ': "վն", 'ﬗ': "մխ",
}

// foldText returns the Unicode case-folded form used by literal queries. Simple
// folding is canonicalized to the smallest rune of its fold orbit and lowered,
// and the common multi-rune full-fold expansions are applied explicitly. This
// is still a literal substring test: no regular expression or selector
// language.
func foldText(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if expansion, ok := foldExpansions[r]; ok {
			b.WriteString(expansion)
			continue
		}
		b.WriteRune(foldRune(r))
	}
	return b.String()
}

// foldRune returns the lowercase representative of r under simple case
// folding. Both sides of a comparison use the same representative, so the
// choice of representative does not change matching, and expansion text can be
// written in the same lowercase form.
func foldRune(r rune) rune {
	minimum := r
	for folded := unicode.SimpleFold(r); folded != r; folded = unicode.SimpleFold(folded) {
		if folded < minimum {
			minimum = folded
		}
	}
	return unicode.ToLower(minimum)
}
