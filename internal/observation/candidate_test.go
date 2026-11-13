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
