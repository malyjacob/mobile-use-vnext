package engine

import "github.com/malyjacob/mobile-use-vnext/internal/device"

// rawIdentity is the complete source identity of one node. It deliberately
// excludes the snapshot index, so the same node can be found again in a newer
// capture by content alone.
type rawIdentity struct {
	Package, Class, ResourceID, Text, Description string
	Bounds                                        device.Rect
	Clickable, LongClickable, Scrollable          bool
	Editable, Password                            bool
	HasEnabled, Enabled                           bool
	HasChecked, Checked                           bool
	HasSelected, Selected                         bool
	HasFocused, Focused                           bool
}

// identityOf is the strict source identity used by settle sampling and the
// coordinate-target path. Ref action revalidation uses structureKey and its
// bounded Tier 1-3 matcher instead; relaxed ref geometry and transparent
// wrappers do not relax settle equality or coordinate authorization.
func identityOf(node *device.RawNode) rawIdentity {
	return rawIdentity{
		Package: node.Package, Class: node.ClassName, ResourceID: node.ResourceID,
		Text: node.Text, Description: node.Description, Bounds: node.Bounds,
		Clickable: node.Clickable, LongClickable: node.LongClickable,
		Scrollable: node.Scrollable, Editable: node.Editable, Password: node.Password,
		HasEnabled: node.HasEnabled, Enabled: node.Enabled,
		HasChecked: node.HasChecked, Checked: node.Checked,
		HasSelected: node.HasSelected, Selected: node.Selected,
		HasFocused: node.HasFocused, Focused: node.Focused,
	}
}
