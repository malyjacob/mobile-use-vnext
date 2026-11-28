package engine

import (
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
	"github.com/malyjacob/mobile-use-vnext/internal/observation"
	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

const actRebindTestNoTarget = "no target"

func TestPlanActRebind(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*actRebindRequest)
	}{
		{"coordinate text", func(req *actRebindRequest) { req.Kind, req.TargetRef = "text", "" }},
		{"coordinate tap", func(req *actRebindRequest) { req.TargetRef = "" }},
		{"scroll", func(req *actRebindRequest) { req.Kind = "scroll" }},
		{"capability changed", func(req *actRebindRequest) { req.Reason = targetChangedReasonCapabilityChanged }},
		{"already blocked", func(req *actRebindRequest) { req.Reason = targetChangedReasonScopeBlocked }},
		{"no original plan", func(req *actRebindRequest) { req.OrigPlan = nil }},
		{"bubble ancestor", func(req *actRebindRequest) { req.OrigPlan.bubbleAncestorRaw = req.OrigRaw }},
		{"colocated", func(req *actRebindRequest) { req.OrigPlan.colocatedRaw = req.OrigRaw }},
		{"delegate", func(req *actRebindRequest) { req.OrigPlan.delegateRaw = req.OrigRaw }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := actRebindRequest{Kind: "tap", Reason: targetChangedReasonTargetMissing,
				TargetRef: "n2", OrigPlan: &tapPlan{}, OrigRaw: &device.RawNode{}}
			tc.mutate(&req)
			decision, apiErr := planActRebind(req)
			if apiErr != nil || decision != (actRebindDecision{}) {
				t.Fatalf("gate: decision=%+v err=%v", decision, apiErr)
			}
		})
	}
}

func TestActRebind(t *testing.T) {
	t.Run("text parent", func(t *testing.T) {
		for _, tc := range []struct {
			name, reason string
			mutate       func(*device.Capture)
		}{
			{name: "keyboard shrinks parent and moves input"},
			{name: "two inputs", reason: targetChangedReasonTargetAmbiguous, mutate: func(fresh *device.Capture) {
				second := fresh.Nodes[2]
				second.Bounds = device.Rect{Left: 130, Top: 305, Right: 400, Bottom: 365}
				second.RawBounds = second.Bounds
				actRebindTestAppend(fresh, 1, second)
			}},
			{name: "duplicate raw parent", reason: actRebindTestNoTarget, mutate: func(fresh *device.Capture) {
				duplicate := fresh.Nodes[1]
				duplicate.Children = nil
				duplicate.Visible = false
				actRebindTestAppend(fresh, 0, duplicate)
			}},
			{name: "duplicate public role with empty resource", reason: actRebindTestNoTarget, mutate: func(fresh *device.Capture) {
				duplicate := transparentNode(0, 0, 0, fresh.Display.ID,
					device.Rect{Left: 650, Top: 400, Right: 950, Bottom: 500})
				duplicate.Text = "Another region"
				duplicate.ResourceID = "com.example:id/another_region"
				actRebindTestAppend(fresh, 0, duplicate)
			}},
			{name: "public resource mismatch", reason: actRebindTestNoTarget, mutate: func(fresh *device.Capture) {
				fresh.Nodes[1].Clickable = true
			}},
			{name: "public role mismatch", reason: actRebindTestNoTarget, mutate: func(fresh *device.Capture) {
				fresh.Nodes[1].Clickable = true
				fresh.Nodes[1].Text = "Editor"
			}},
			{name: "outside old parent span", reason: actRebindTestNoTarget, mutate: func(fresh *device.Capture) {
				for i := 1; i <= 2; i++ {
					bounds := &fresh.Nodes[i].Bounds
					bounds.Left += 540
					bounds.Right += 540
					fresh.Nodes[i].RawBounds = *bounds
				}
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				display := targetProofDisplay()
				oldParent := transparentNode(1, 0, 1, display.ID,
					device.Rect{Left: 70, Top: 200, Right: 600, Bottom: 850}, 2)
				oldParent.ResourceID = "com.example:id/editor_panel"
				oldInput := revalEditText(2, 1, 2, display.ID,
					device.Rect{Left: 110, Top: 680, Right: 500, Bottom: 790}, "", "", "", false)
				original := revalCapture(display, revalRoot(display.ID, 1), oldParent, oldInput)
				freshParent, freshInput := oldParent, oldInput
				freshParent.Bounds = device.Rect{Left: 100, Top: 205, Right: 450, Bottom: 375}
				freshInput.Bounds = device.Rect{Left: 115, Top: 220, Right: 440, Bottom: 290}
				freshParent.RawBounds, freshInput.RawBounds = freshParent.Bounds, freshInput.Bounds
				fresh := revalCapture(display, revalRoot(display.ID, 1), freshParent, freshInput)
				if tc.mutate != nil {
					tc.mutate(&fresh)
				}
				req := actRebindTestRequest(t, "text", &original, 2, &fresh)
				req.Reason = targetChangedReasonLineageChanged
				actRebindTestDecision(t, req, actRebindText, tc.reason, 2)
			})
		}
	})

	t.Run("text parent projection", func(t *testing.T) {
		for _, tc := range []struct {
			name, reason string
			keepParent   bool
		}{
			{name: "mapped empty parent forbids fallback", reason: actRebindTestNoTarget, keepParent: true},
			{name: "unmapped parent permits fallback"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				display := targetProofDisplay()
				parent := transparentNode(1, 0, 1, display.ID,
					device.Rect{Left: 70, Top: 150, Right: 600, Bottom: 850}, 2)
				parent.ResourceID = "com.example:id/editor_panel"
				parent.Clickable = tc.keepParent
				oldInput := revalEditText(2, 1, 2, display.ID,
					device.Rect{Left: 110, Top: 680, Right: 500, Bottom: 790}, "", "Search archive", "", false)
				original := revalCapture(display, revalRoot(display.ID, 1), parent, oldInput)
				parent.Children = nil
				fresh := revalCapture(display, revalRoot(display.ID, 1), parent)
				oldInput.Bounds = device.Rect{Left: 110, Top: 250, Right: 500, Bottom: 360}
				oldInput.RawBounds = oldInput.Bounds
				actRebindTestAppend(&fresh, 0, oldInput)
				req := actRebindTestRequest(t, "text", &original, 2, &fresh)
				sawParent := false
				for _, index := range req.FreshView.source {
					if index == 1 {
						sawParent = true
					}
				}
				if sawParent != tc.keepParent {
					t.Fatalf("fresh parent projection presence = %v, want %v", sawParent, tc.keepParent)
				}
				actRebindTestDecision(t, req, actRebindText, tc.reason, 2)
			})
		}
	})

	t.Run("ambiguous inner parent yields to unique outer parent", func(t *testing.T) {
		display := targetProofDisplay()
		makeCapture := func(inputTop int) device.Capture {
			outer := transparentNode(1, 0, 1, display.ID,
				device.Rect{Left: 70, Top: 150, Right: 650, Bottom: 850}, 2)
			outer.ResourceID, outer.Clickable = "com.example:id/outer_editor", true
			inner := transparentNode(2, 1, 2, display.ID,
				device.Rect{Left: 95, Top: 180, Right: 600, Bottom: 810}, 3)
			inner.ResourceID = "com.example:id/inner_editor"
			input := revalEditText(3, 2, 3, display.ID,
				device.Rect{Left: 110, Top: inputTop, Right: 500, Bottom: inputTop + 100}, "", "", "", false)
			return revalCapture(display, revalRoot(display.ID, 1), outer, inner, input)
		}
		original, fresh := makeCapture(680), makeCapture(250)
		duplicate := transparentNode(0, 0, 0, display.ID,
			device.Rect{Left: 670, Top: 180, Right: 920, Bottom: 450})
		duplicate.Text = "Another region"
		actRebindTestAppend(&original, 0, duplicate)
		// Move the fresh input out of the inner wrapper and keep the wrapper
		// published. Choosing it would leave no eligible input.
		fresh.Nodes[2].Children, fresh.Nodes[2].Text = nil, "Inner editor"
		fresh.Nodes[2].Bounds.Top = 430
		fresh.Nodes[2].RawBounds = fresh.Nodes[2].Bounds
		fresh.Nodes[1].Children = []int{2, 3}
		fresh.Nodes[3].Parent, fresh.Nodes[3].Depth = 1, 2
		actRebindTestDecision(t, actRebindTestRequest(t, "text", &original, 3, &fresh), actRebindText, "", 3)
	})

	t.Run("text parent with two original fields", func(t *testing.T) {
		for _, tc := range []struct {
			name, description, reason string
			otherOriginallyDisabled   bool
			otherOriginallyHidden     bool
			otherPublicRoleOnly       bool
			moveOutsideParent         bool
		}{
			{name: "only the other field remains", description: "Notes", reason: actRebindTestNoTarget},
			{name: "only the previously disabled field remains", description: "Notes", otherOriginallyDisabled: true, reason: actRebindTestNoTarget},
			{name: "previously hidden raw editor becomes the only visible field", description: "Notes", otherOriginallyHidden: true, reason: actRebindTestNoTarget},
			{name: "other field is identifiable only by the public input role", description: "Notes", otherPublicRoleOnly: true, reason: actRebindTestNoTarget},
			{name: "same field without static evidence", reason: actRebindTestNoTarget},
			{name: "same field with unique description uses A2", description: "Search archive"},
			{name: "same field with unique description uses A2 beside a disabled field", description: "Search archive", otherOriginallyDisabled: true, moveOutsideParent: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				display := targetProofDisplay()
				parent := transparentNode(1, 0, 1, display.ID,
					device.Rect{Left: 70, Top: 150, Right: 600, Bottom: 850}, 2, 3)
				parent.ResourceID, parent.Text = "com.example:id/editor_panel", "Editor panel"
				original := revalCapture(display, revalRoot(display.ID, 1), parent,
					revalEditText(2, 1, 2, display.ID,
						device.Rect{Left: 110, Top: 680, Right: 500, Bottom: 790}, "", "Search archive", "", false),
					revalEditText(3, 1, 2, display.ID,
						device.Rect{Left: 110, Top: 540, Right: 500, Bottom: 650}, "", "Notes", "", false))
				original.Nodes[3].Enabled = !tc.otherOriginallyDisabled
				if tc.otherOriginallyHidden {
					original.Nodes[3].Visible = false
					original.Nodes[3].Bounds = device.Rect{}
				}
				parent.Children = []int{2}
				fresh := revalCapture(display, revalRoot(display.ID, 1), parent,
					revalEditText(2, 1, 2, display.ID,
						device.Rect{Left: 110, Top: 250, Right: 500, Bottom: 360}, "", tc.description, "", false))
				if tc.moveOutsideParent {
					// Keep the parent mapped, but put the input outside its subtree
					// so successful recovery requires the static description path.
					fresh.Nodes[0].Children = []int{1, 2}
					fresh.Nodes[1].Children = nil
					fresh.Nodes[1].Bounds.Top = 420
					fresh.Nodes[1].RawBounds = fresh.Nodes[1].Bounds
					fresh.Nodes[2].Parent, fresh.Nodes[2].Depth = 0, 1
				}
				req := actRebindTestRequest(t, "text", &original, 2, &fresh)
				if tc.otherPublicRoleOnly {
					// Isolate the public-role branch using a synthetic disagreement
					// between the projected role and the raw editable heuristic.
					req.Obs.Projection.Capture.Nodes[3].Editable = false
				}
				actRebindTestDecision(t, req, actRebindText, tc.reason, 2)
			})
		}
	})

	t.Run("text static label", func(t *testing.T) {
		const label = "Search archive"
		for _, tc := range []struct {
			name, reason string
			mutate       func(*device.Capture, *device.Capture)
		}{
			{name: "direct unique description"},
			{name: "shared editable value does not identify the field", reason: actRebindTestNoTarget, mutate: func(old, fresh *device.Capture) {
				old.Nodes[1].Text, fresh.Nodes[1].Text = "Draft message", "Draft message"
				old.Nodes[1].Description, fresh.Nodes[1].Description = "", "Notes"
			}},
			{name: "editable value cannot match a description", reason: actRebindTestNoTarget, mutate: func(_, fresh *device.Capture) {
				fresh.Nodes[1].Text, fresh.Nodes[1].Description = label, "Notes"
			}},
			{name: "two independently anchored fields", reason: targetChangedReasonTargetAmbiguous, mutate: func(old, fresh *device.Capture) {
				const secondLabel = "Filter archive"
				actRebindTestAppend(old, 1, actRebindTestLabel(secondLabel, old.Nodes[1].Bounds))
				second := fresh.Nodes[1]
				second.Description = secondLabel
				second.Bounds.Top += 90
				second.Bounds.Bottom += 90
				second.RawBounds = second.Bounds
				actRebindTestAppend(fresh, 0, second)
			}},
			{name: "direct passive placeholder", mutate: func(old, fresh *device.Capture) {
				for _, capture := range []*device.Capture{old, fresh} {
					input := &capture.Nodes[1]
					input.Description = ""
					actRebindTestAppend(capture, 1, actRebindTestLabel(label, input.Bounds))
				}
			}},
			{name: "passive placeholder outside the field", reason: actRebindTestNoTarget, mutate: func(old, fresh *device.Capture) {
				for _, capture := range []*device.Capture{old, fresh} {
					capture.Nodes[1].Description = ""
					actRebindTestAppend(capture, 1, actRebindTestLabel(label,
						device.Rect{Left: 650, Top: 250, Right: 800, Bottom: 280}))
				}
			}},
			{name: "password and invisible duplicates ignored", mutate: func(old, fresh *device.Capture) {
				for _, capture := range []*device.Capture{old, fresh} {
					duplicate := actRebindTestLabel(label, device.Rect{Left: 650, Top: 250, Right: 800, Bottom: 280})
					duplicate.HasPassword, duplicate.Password = true, true
					actRebindTestAppend(capture, 0, duplicate)
					duplicate.Visible, duplicate.Password = false, false
					actRebindTestAppend(capture, 0, duplicate)
				}
			}},
			{name: "duplicate in original raw root", reason: actRebindTestNoTarget, mutate: func(old, _ *device.Capture) {
				old.Nodes[0].Text = label
			}},
			{name: "two matching attributes on one raw node", reason: actRebindTestNoTarget, mutate: func(_, fresh *device.Capture) {
				fresh.Nodes[1].Text = label
			}},
			{name: "contains a digit", reason: actRebindTestNoTarget, mutate: func(old, fresh *device.Capture) {
				old.Nodes[1].Description, fresh.Nodes[1].Description = "Search archive ２", "Search archive ２"
			}},
			{name: "area outside ratio", reason: actRebindTestNoTarget, mutate: func(_, fresh *device.Capture) {
				fresh.Nodes[1].Bounds.Bottom = fresh.Nodes[1].Bounds.Top + 3
				fresh.Nodes[1].RawBounds = fresh.Nodes[1].Bounds
			}},
			{name: "outside horizontal span", reason: actRebindTestNoTarget, mutate: func(_, fresh *device.Capture) {
				fresh.Nodes[1].Bounds.Left, fresh.Nodes[1].Bounds.Right = 640, 950
				fresh.Nodes[1].RawBounds = fresh.Nodes[1].Bounds
			}},
			{name: "occluded", reason: targetChangedReasonScopeBlocked, mutate: func(_, fresh *device.Capture) {
				actRebindTestAppend(fresh, 0, revalButton(0, 0, 0, fresh.Display.ID, fresh.Nodes[1].Bounds, "Overlay", "", ""))
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				display := targetProofDisplay()
				original := revalCapture(display, revalRoot(display.ID, 1), revalEditText(1, 0, 1, display.ID,
					device.Rect{Left: 100, Top: 560, Right: 430, Bottom: 630}, "", label, "", false))
				fresh := revalCapture(display, revalRoot(display.ID, 1), revalEditText(1, 0, 1, display.ID,
					device.Rect{Left: 120, Top: 210, Right: 440, Bottom: 285}, "", label, "", false))
				if tc.mutate != nil {
					tc.mutate(&original, &fresh)
				}
				actRebindTestDecision(t, actRebindTestRequest(t, "text", &original, 1, &fresh), actRebindText, tc.reason, 1)
			})
		}
	})

	t.Run("container identity excludes editable values", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			staticLabel bool
			duplicate   bool
		}{
			{name: "unique editable value alone"},
			{name: "repeated editable value alone", duplicate: true},
			{name: "unique static label with repeated editable value", staticLabel: true, duplicate: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				const draft = "Private draft"
				original, fresh := actRebindTestCards()
				for _, capture := range []*device.Capture{&original, &fresh} {
					if !tc.staticLabel {
						capture.Nodes[1].Text, capture.Nodes[3].Text = "", ""
					}
					top := capture.Nodes[1].Bounds.Top
					input := revalEditText(0, 0, 0, capture.Display.ID,
						device.Rect{Left: 110, Top: top + 65, Right: 190, Bottom: top + 85}, draft, "", "", false)
					actRebindTestAppend(capture, 1, input)
					if tc.duplicate {
						input.Bounds = device.Rect{Left: 650, Top: 350, Right: 840, Bottom: 430}
						input.RawBounds = input.Bounds
						actRebindTestAppend(capture, 0, input)
					}
				}
				var strategy string
				if tc.staticLabel {
					strategy = actRebindContainer
				}
				actRebindTestDecision(t, actRebindTestRequest(t, "tap", &original, 1, &fresh), strategy, "", 1)
			})
		}
	})

	t.Run("container", func(t *testing.T) {
		for _, tc := range []struct {
			name, kind, reason string
			noAttempt          bool
			mutate             func(*device.Capture, *device.Capture)
		}{
			{name: "two thirds using self text and numeric badge", kind: "tap"},
			{name: "long press", kind: "long_press"},
			{name: "fresh editor value cannot carry a static container anchor", kind: "tap", reason: actRebindTestNoTarget, mutate: func(old, fresh *device.Capture) {
				const anchor = "Saved search"
				old.Nodes[1].Text, fresh.Nodes[1].Text = "", ""
				old.Nodes[3].Text = anchor
				fresh.Nodes[3] = revalEditText(3, 1, 2, fresh.Display.ID, fresh.Nodes[3].Bounds, anchor, "", "", false)
			}},
			{name: "repeated purchase buttons", kind: "tap", reason: targetChangedReasonTargetAmbiguous, mutate: func(old, fresh *device.Capture) {
				for _, capture := range []*device.Capture{old, fresh} {
					duplicate := capture.Nodes[1]
					duplicate.Children = nil
					duplicate.Bounds = device.Rect{Left: 90, Top: 850, Right: 450, Bottom: 1020}
					duplicate.RawBounds = duplicate.Bounds
					actRebindTestAppend(capture, 0, duplicate)
				}
			}},
			{name: "only numeric and whitespace", kind: "tap", noAttempt: true, mutate: func(old, fresh *device.Capture) {
				for _, capture := range []*device.Capture{old, fresh} {
					capture.Nodes[1].Text, capture.Nodes[3].Text = "17", " \t "
				}
			}},
			{name: "blocked by floating window", kind: "tap", reason: targetChangedReasonScopeBlocked, mutate: func(_, fresh *device.Capture) {
				actRebindTestAppend(fresh, 0, revalButton(0, 0, 0, fresh.Display.ID, fresh.Nodes[1].Bounds, "Overlay", "", ""))
			}},
			{name: "second candidate shares only nested anchor", kind: "tap", reason: targetChangedReasonTargetAmbiguous, mutate: func(old, fresh *device.Capture) {
				old.Nodes[1].Text = ""
				old.Nodes[3].Text = "购买"
				fresh.Nodes[1].Text = ""
				fresh.Nodes[3].Text = "购买"
				card := fresh.Nodes[1]
				card.Children = []int{2, 3}
				card.Parent = 1
				card.Index = len(fresh.Nodes)
				fresh.Nodes[1].Children = []int{card.Index}
				for _, child := range card.Children {
					fresh.Nodes[child].Parent = card.Index
					fresh.Nodes[child].Depth++
				}
				card.Depth++
				fresh.Nodes = append(fresh.Nodes, card)
			}},
			{name: "area outside ratio", kind: "tap", reason: actRebindTestNoTarget, mutate: func(_, fresh *device.Capture) {
				fresh.Nodes[1].Bounds.Bottom = fresh.Nodes[1].Bounds.Top + 12
				fresh.Nodes[1].RawBounds = fresh.Nodes[1].Bounds
			}},
			{name: "horizontal overlap below half", kind: "tap", reason: actRebindTestNoTarget, mutate: func(_, fresh *device.Capture) {
				fresh.Nodes[1].Bounds.Left, fresh.Nodes[1].Bounds.Right = 420, 770
				fresh.Nodes[1].RawBounds = fresh.Nodes[1].Bounds
			}},
			{name: "full screen fallback", kind: "tap", reason: actRebindTestNoTarget, mutate: func(old, fresh *device.Capture) {
				old.Nodes[1].Bounds = device.Rect{Right: old.Display.Width, Bottom: old.Display.Height / 2}
				old.Nodes[1].RawBounds = old.Nodes[1].Bounds
				fresh.Nodes[1].Bounds, fresh.Nodes[1].RawBounds = fresh.Display.Bounds(), fresh.Display.Bounds()
			}},
			{name: "overlap below two thirds", kind: "tap", reason: actRebindTestNoTarget, mutate: func(_, fresh *device.Capture) {
				fresh.Nodes[3].Text = "New status"
			}},
			{name: "unrelated own label", kind: "tap", reason: actRebindTestNoTarget, mutate: func(old, fresh *device.Capture) {
				old.Nodes[3].Text = "Card contents"
				fresh.Nodes[3].Text = "Card contents"
				fresh.Nodes[1].Text = "Remove"
				actRebindTestAppend(fresh, 1, actRebindTestLabel("购买", fresh.Nodes[2].Bounds))
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				original, fresh := actRebindTestCards()
				if tc.mutate != nil {
					tc.mutate(&original, &fresh)
				}
				strategy := actRebindContainer
				if tc.noAttempt {
					strategy = ""
				}
				actRebindTestDecision(t, actRebindTestRequest(t, tc.kind, &original, 1, &fresh), strategy, tc.reason, 1)
			})
		}
	})
}

func actRebindTestCards() (device.Capture, device.Capture) {
	display := targetProofDisplay()
	capture := func(top int, badge, extra string) device.Capture {
		cardBounds := device.Rect{Left: 90, Top: top, Right: 500, Bottom: top + 200}
		card := revalButton(1, 0, 1, display.ID, cardBounds, "购买", "", "")
		out := revalCapture(display, revalRoot(display.ID, 1), card)
		actRebindTestAppend(&out, 1, actRebindTestLabel(badge,
			device.Rect{Left: 110, Top: top + 20, Right: 165, Bottom: top + 40}))
		actRebindTestAppend(&out, 1, actRebindTestLabel(extra,
			device.Rect{Left: 190, Top: top + 40, Right: 250, Bottom: top + 60}))
		return out
	}
	return capture(440, "3赞", "Old status"), capture(220, "4赞", "")
}

func actRebindTestLabel(text string, bounds device.Rect) device.RawNode {
	node := transparentNode(0, 0, 0, 0, bounds)
	node.ClassName, node.Text = "android.widget.TextView", text
	return node
}

func actRebindTestAppend(capture *device.Capture, parent int, node device.RawNode) {
	node.Index, node.Parent = len(capture.Nodes), parent
	node.Depth, node.DisplayID = capture.Nodes[parent].Depth+1, capture.Display.ID
	capture.Nodes[parent].Children = append(capture.Nodes[parent].Children, node.Index)
	capture.Nodes = append(capture.Nodes, node)
}

func actRebindTestRequest(t *testing.T, kind string, old *device.Capture, index int, fresh *device.Capture) actRebindRequest {
	t.Helper()
	projector := &testProjector{builder: observation.NewBuilder()}
	projection, err := projector.Build(old)
	if err != nil {
		t.Fatal(err)
	}
	original := projection.Capture
	oldView, apiErr := buildTapPublicView(projection.Tree, original, projection.Source)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	ref := projectionPublicRefForRaw(projection, index)
	if ref == "" {
		t.Fatalf("original target %d was not projected", index)
	}
	preview, err := projector.Preview(fresh)
	if err != nil {
		t.Fatal(err)
	}
	freshView, apiErr := buildTapPublicView(preview.Tree, fresh, preview.Source)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	obs := &observationState{Projection: projection, Screen: old.Display, DisplayID: old.Display.ID}
	x, y := original.Nodes[index].Bounds.Center()
	return actRebindRequest{Kind: kind, Reason: targetChangedReasonTargetMissing, TargetRef: ref,
		Obs: obs, OrigView: oldView, OrigRaw: &original.Nodes[index],
		OrigPlan: &tapPlan{x: x, y: y, targetRaw: &original.Nodes[index], targetRef: ref},
		Fresh:    fresh, FreshView: freshView}
}

func actRebindTestDecision(t *testing.T, req actRebindRequest, strategy, reason string, index int) {
	t.Helper()
	decision, apiErr := planActRebind(req)
	if req.OrigView.sourceValidated || req.FreshView.sourceValidated {
		t.Fatal("planner mutated a caller-owned view")
	}
	if decision.Attempted != (strategy != "") || decision.Strategy != strategy {
		t.Fatalf("attempt: decision=%+v err=%v, want strategy=%q", decision, apiErr, strategy)
	}
	if reason != "" && reason != actRebindTestNoTarget {
		if apiErr == nil || apiErr.Code != protocol.CodeTargetChanged || apiErr.Details["reason"] != reason {
			t.Fatalf("error = %#v, want target_changed/%s", apiErr, reason)
		}
	} else if apiErr != nil {
		t.Fatalf("unexpected error: %#v", apiErr)
	}
	if reason != "" || strategy == "" {
		if decision.Frame != nil || decision.Target != nil || decision.Raw != nil || decision.RecoveredRef != "" {
			t.Fatalf("failed rebind yielded a target: %+v", decision)
		}
		return
	}
	frame := decision.Frame
	raw := &req.Fresh.Nodes[index]
	if frame == nil || frame.capture != req.Fresh || frame.raw != raw || frame.tier != 4 || frame.strategy == "" ||
		decision.Raw != raw || decision.Target == nil || decision.RecoveredRef != decision.Target.Ref ||
		req.FreshView.byRef[decision.RecoveredRef] != decision.Target || !raw.Bounds.Contains(frame.x, frame.y) {
		t.Fatalf("fresh target/point not proven: decision=%+v frame=%+v", decision, frame)
	}
	if frame.x == req.OrigPlan.x && frame.y == req.OrigPlan.y {
		t.Fatal("rebind reused the old point")
	}
}
