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
// focused/selected/protected/checked=false). The public scope is
// protocol.Scope (reported_visible_with_context): nodes reported visible in the
// target display, plus their necessary ancestors. An ancestor that is not
// visible but has a visible or already-kept descendant is published as a
// context node (role group, context=true, bounds=nil, no actions, no label).
// Nodes of other displays are never published, and neither are semantically
// empty leaves (no readable content, non-default state, interaction
// capability or intrinsic leaf role).
//
// Published descendants attach to the nearest published ancestor in source
// preorder, even across wrappers from another display. Source and ContextSource
// form a disjoint, complete public-ref index; raw facts remain unchanged.
// Compact reading may hide known geometry, omit tiny unlabeled image leaves,
// remove anonymous containers left empty by those omissions, and bypass hidden
// anonymous singleton wrappers. Retained nodes keep their logical fields,
// ordering and refs. New compact output uses ordinary definitions only. Readers
// continue to expand historical chain definitions, assigning indented children
// to the chain tail.
//
// # Refs and continuity
//
// Refs are n1, n2, ... allocated monotonically per Builder (one session/scope).
// A ref is never reused for a different target; a full rebuild does not force
// renumbering. Build matches the new tree against the Builder's previous
// snapshot one-to-one through five deterministic stages:
//
//  1. unique anonymous structure under already matched parents;
//  2. six local/global content and identity passes;
//  3. container matching from equal, conflict-free descendant-pair sets;
//  4. local recovery below newly anchored parents, alternating with stage 3;
//  5. one zero-shift ordinal pass with an independent fixed point, followed
//     only by local recovery.
//
// Identity evidence excludes input value, checked/focused/selected and bounds,
// so those changes alone do not break reading identity. Non-input labels remain
// part of identity alongside resource ids. Local resource-only fallback can
// preserve a ref through text edits only when both sides are unique below a
// matched parent. Global uniqueness uses complete trees, including matched
// nodes. Ambiguity or budget exhaustion allocates new refs; incomplete batches
// are discarded. Candidate/Preview leave continuity and its high-water mark
// unchanged until an accepted candidate commits. Old snapshots stay immutable.
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
//   - add for closed new subtrees and empty assembly roots leading to
//     survivors, each attached by an explicit children entry;
//   - remove for the highest vanishing baseline roots (a children entry is only
//     emitted when something else about the list also changed);
//   - children with complete final child lists for surviving parents and empty
//     top-level add assembly roots;
//   - replace for a self-contained baseline region when the complete current
//     subtree is cheaper than patching it node by node. A region is chosen
//     top-down, covers its whole subtree and is never nested in another block.
//
// The five blocks declare one final graph atomically. Surviving refs may move
// across parents, including into added assembly roots. Remove roots disappear
// while descendants may survive through final reachable claims. Replace roots
// may move, but surviving descendants inside closed definitions keep baseline
// parents. Cycles, duplicate parents, orphaned nodes, unreachable declarations,
// changed roots and surviving role changes are rejected. Node and Read share
// the planner and applier; Read additionally expands historical chains, emits
// ordinary definitions, and checks canonical wire equality. All five bools
// retain strict tri-state equality; set preserves explicit null and default
// values.
package observation
