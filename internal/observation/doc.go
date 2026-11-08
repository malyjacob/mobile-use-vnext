// Package observation builds the public observation tree from one raw device
// capture, keeps conservative ref continuity across captures of one session,
// and computes/replays the fixed-baseline delta format.
//
// The package is pure: it performs no I/O, holds no device state and touches
// no other package's data. The engine owns sessions and commit authority; this
// package only receives a complete capture and returns an immutable snapshot,
// or two trees and returns a delta.
//
// # Projection
//
// One capture produces one complete public tree with a synthetic root (role
// root, bounds = target display rectangle, known enabled=true and
// focused/selected/protected=false). The public scope is
// protocol.Scope (reported_visible_with_context): nodes reported visible in the
// target display, plus their necessary ancestors. An ancestor that is not
// visible but has a visible or already-kept descendant is published as a
// context node (role group, context=true, bounds=nil, no actions, no label).
// Nodes of other displays are never published, and neither are semantically
// empty leaves (no label, description, value, known state, action capability
// or capability flag).
//
// This first implementation does not fold or collapse containers: every node
// that has kept children or semantic evidence is published, so the public tree
// is a faithful projection of the source hierarchy. The design permits folding
// single-child layout shells and absorbing a unique title leaf into its
// control; that is deliberately left out here because folding changes the
// meaning of a public unit (and therefore its ref) and needs a stable rule set
// before it can preserve refs conservatively.
//
// # Refs and continuity
//
// Refs are n1, n2, ... allocated monotonically per Builder (one session/scope).
// A ref is never reused for a different target; a full rebuild does not force
// renumbering. Build matches the new tree against the Builder's previous
// snapshot one-to-one, in parent-first passes:
//
//  1. same matched parent and identical identity evidence (role, class,
//     package, resource id presence and value, label signature, protection);
//  2. same matched parent and the same label-free container shape (a scroll
//     area or layout row), when the siblings are unique in both trees, or, for
//     several identical siblings, when the parent keeps the same number of
//     them and the ordinal therefore says nothing moved;
//  3. exactly one unit with the same identity evidence anywhere in both trees;
//  4. same matched parent and the same input anchor (class plus resource id or
//     description) for input nodes, which keeps an input's ref through value
//     edits.
//
// Identity evidence excludes input value, checked/focused/selected and bounds,
// so typing, ticking and scrolling never break identity. A unit identified by
// its resource id keeps its ref when its readable text changes; without a
// resource id the label is part of its identity. A role change always yields a
// new ref. Anything ambiguous or newly appeared gets a new ref; refs of nodes
// that left the tree are never resurrected.
//
// # Delta
//
// Diff computes T = apply(base, delta) for a confirmed full baseline. It emits
// set, add, remove, children and replace blocks, verifies the result with
// Apply and Equal, and returns ErrNotRepresentable when no legal delta exists.
// Apply is the atomic interpreter of the delta format (design §8.1).
//
// Generated forms:
//   - set for every changed field of a surviving ref, always with the complete
//     new value (including false, "", [] and an explicit null);
//   - add for the highest new subtree roots, each attached by an explicit
//     children entry;
//   - remove for the highest vanishing baseline roots (a children entry is only
//     emitted when something else about the list also changed);
//   - children with the complete current child list for surviving parents;
//   - replace for a self-contained baseline region when the complete current
//     subtree is cheaper than patching it node by node. A region is chosen
//     top-down, covers its whole subtree and is never nested in another block.
//
// A ref may not change role, may not move to a different parent, and may not
// move inside an added subtree; those pairs return ErrNotRepresentable so the
// engine can request a full tree instead.
package observation
