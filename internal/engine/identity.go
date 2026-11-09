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

// identityOf is the single identity function of this service. Live
// revalidation compares it to decide target_changed, and the settle criterion
// digests exactly the same value: a frame pair that differs nowhere in this
// identity cannot fail revalidation for content reasons. Keeping one function
// is what makes that guarantee hold as the fields evolve.
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
