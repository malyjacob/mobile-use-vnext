package observation

import (
	"testing"
)

// A rejected acquisition must leave continuity untouched: the next published
// tree keeps the refs it would have had without the discarded candidate. This
// is the projection-side half of the engine's "no side effects on refusal"
// commit gate.

func candidateCapture() *capBuilder {
	c := newCap(1080, 2400)
	root := c.root()
	c.add(root, withText(withClass(visible(rect(10, 10, 500, 80)), "android.widget.TextView"), "title"))
	return c
}

func candidateCaptureWithNewChild() *capBuilder {
	c := candidateCapture()
	root := 0
	c.add(root, withText(withClass(visible(rect(10, 100, 500, 180)), "android.widget.TextView"), "extra"))
	return c
}

func TestCandidateDoesNotAdvanceContinuity(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, candidateCapture().capture())
	titleRef := first.Tree.Root.Children[0].Ref

	// A candidate that is never committed must not consume the next ref.
	discarded, commit, err := b.Candidate(candidateCaptureWithNewChild().capture())
	if err != nil {
		t.Fatalf("candidate: %v", err)
	}
	if discarded == nil || commit == nil {
		t.Fatal("candidate must return a snapshot and a commit function")
	}
	if b.Previous() != first {
		t.Fatal("a candidate must not publish itself")
	}

	// The next committed tree must be exactly what it would have been without
	// the discarded candidate: the same title ref and the next fresh number.
	after := buildOne(t, b, candidateCaptureWithNewChild().capture())
	if got := after.Tree.Root.Children[0].Ref; got != titleRef {
		t.Fatalf("discarded candidate shifted the ref of an unchanged node: %s -> %s",
			titleRef, got)
	}
	if got := after.Tree.Root.Children[1].Ref; got != "n3" {
		t.Fatalf("discarded candidate consumed a ref: new node ref %q, want n3", got)
	}
	// The discarded candidate allocated the same number it would have used, so
	// the committed tree is exactly the tree without the discarded attempt.
	if after.Tree.Root.Children[1].Ref != discarded.Tree.Root.Children[1].Ref {
		t.Fatal("the discarded and committed projection should agree on fresh numbering")
	}
}

func TestCandidateCommitPublishesExactState(t *testing.T) {
	b := NewBuilder()
	first := buildOne(t, b, candidateCapture().capture())
	second, commit, err := b.Candidate(candidateCaptureWithNewChild().capture())
	if err != nil {
		t.Fatalf("candidate: %v", err)
	}
	commit()
	if b.Previous() != second {
		t.Fatal("commit must publish exactly the candidate snapshot")
	}
	// The committed candidate keeps the ref numbering of the next observation.
	third := buildOne(t, b, candidateCaptureWithNewChild().capture())
	if third.Tree.Root.Children[0].Ref != first.Tree.Root.Children[0].Ref {
		t.Fatalf("committed continuity was not applied: %s",
			dump(third.Tree))
	}
	if third.Tree.Root.Children[1].Ref != second.Tree.Root.Children[1].Ref {
		t.Fatalf("committed candidate did not keep its ref: %s",
			dump(third.Tree))
	}
}

// candidateAgainstPageCapture builds a list page; candidateAgainstOtherPage
// builds a different page whose top-level text would be a sibling of the list.
func candidateAgainstPageCapture(marker string) *capBuilder {
	c := newCap(1080, 2400)
	root := c.root()
	row := c.add(root, withClass(visible(rect(0, 0, 1080, 200)), "android.widget.LinearLayout"))
	c.add(row, withText(withClass(visible(rect(10, 10, 500, 90)), "android.widget.TextView"), marker))
	return c
}

// CandidateAgainst(prev, cap) is the read-only probe of a historical baseline:
// it must project cap against prev exactly, publish nothing, consume no ref
// numbers and leave the current continuity state untouched.
func TestCandidateAgainstUsesExplicitBaselineWithoutMovingHighWater(t *testing.T) {
	b := NewBuilder()
	pageA := buildOne(t, b, candidateAgainstPageCapture("列表页").capture())
	_ = buildOne(t, b, candidateAgainstPageCapture("详情页").capture())

	// The historical baseline still names the list page's refs even though the
	// builder's previous snapshot is now the other page.
	candidate, commit, err := b.CandidateAgainst(pageA, candidateAgainstPageCapture("列表页").capture())
	if err != nil {
		t.Fatalf("CandidateAgainst: %v", err)
	}
	if candidate == nil || commit == nil {
		t.Fatal("CandidateAgainst must return a snapshot and a commit function")
	}
	if pageA.Tree.Root.Ref != candidate.Tree.Root.Ref ||
		pageA.Tree.Root.Children[0].Ref != candidate.Tree.Root.Children[0].Ref ||
		pageA.Tree.Root.Children[0].Children[0].Ref != candidate.Tree.Root.Children[0].Children[0].Ref {
		t.Fatalf("candidate did not continue the explicit baseline:\nwant %s\n got %s",
			dump(pageA.Tree), dump(candidate.Tree))
	}

	// A discarded probe is invisible: a later Build must allocate exactly the
	// refs it would have allocated without the probe.
	plain := NewBuilder()
	_ = buildOne(t, plain, candidateAgainstPageCapture("列表页").capture())
	_ = buildOne(t, plain, candidateAgainstPageCapture("详情页").capture())
	withoutProbe := buildOne(t, plain, candidateAgainstPageCapture("列表页").capture())

	afterProbe := buildOne(t, b, candidateAgainstPageCapture("列表页").capture())
	if len(withoutProbe.Refs) != len(afterProbe.Refs) {
		t.Fatalf("probe changed continuity: refs %v vs %v", withoutProbe.Refs, afterProbe.Refs)
	}
	for i := range withoutProbe.Refs {
		if withoutProbe.Refs[i] != afterProbe.Refs[i] {
			t.Fatalf("discarded CandidateAgainst consumed a ref at %d: %s vs %s",
				i, withoutProbe.Refs[i], afterProbe.Refs[i])
		}
	}
}

func TestCandidateAgainstCommitPublishesProbeState(t *testing.T) {
	b := NewBuilder()
	pageA := buildOne(t, b, candidateAgainstPageCapture("列表页").capture())
	_ = buildOne(t, b, candidateAgainstPageCapture("详情页").capture())

	candidate, commit, err := b.CandidateAgainst(pageA, candidateAgainstPageCapture("列表页").capture())
	if err != nil {
		t.Fatalf("CandidateAgainst: %v", err)
	}
	if b.Previous() == candidate {
		t.Fatal("CandidateAgainst must not publish before commit")
	}
	commit()
	if b.Previous() != candidate {
		t.Fatal("commit must publish exactly the probe snapshot")
	}
	next := buildOne(t, b, candidateAgainstPageCapture("列表页").capture())
	if next.Tree.Root.Children[0].Ref != candidate.Tree.Root.Children[0].Ref {
		t.Fatalf("committed probe was not applied as continuity: %s",
			dump(next.Tree))
	}
}

func TestCandidateAgainstNilBaselineStartsFreshAboveHighWater(t *testing.T) {
	b := NewBuilder()
	seeded := buildOne(t, b, candidateCapture().capture())
	nextNumber := len(seeded.Refs) + 1
	candidate, _, err := b.CandidateAgainst(nil, candidateCapture().capture())
	if err != nil {
		t.Fatalf("CandidateAgainst(nil): %v", err)
	}
	if candidate.Tree.Root.Ref != formatRef(nextNumber) {
		t.Fatalf("fresh projection must start at the current high-water mark %s, got %s",
			formatRef(nextNumber), candidate.Tree.Root.Ref)
	}
	live := map[string]bool{}
	for _, ref := range seeded.Refs {
		live[ref] = true
	}
	for _, ref := range candidate.Refs {
		if live[ref] {
			t.Fatalf("nil-baseline candidate reused a live ref %s", ref)
		}
	}
}
