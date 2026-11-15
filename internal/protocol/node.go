package protocol

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
)

// Role is the public reading role of a node. It comes from source evidence and
// fixed projection rules, never from guessing business semantics.
type Role string

const (
	RoleRoot     Role = "root"
	RoleGroup    Role = "group"
	RoleText     Role = "text"
	RoleButton   Role = "button"
	RoleInput    Role = "input"
	RoleCheckbox Role = "checkbox"
	RoleRadio    Role = "radio"
	RoleSwitch   Role = "switch"
	RoleToggle   Role = "toggle"
	RoleScroll   Role = "scroll"
	RoleImage    Role = "image"
	RoleView     Role = "view"
)

// Valid reports whether r belongs to the fixed first-version role set.
func (r Role) Valid() bool {
	switch r {
	case RoleRoot, RoleGroup, RoleText, RoleButton, RoleInput,
		RoleCheckbox, RoleRadio, RoleSwitch, RoleToggle, RoleScroll,
		RoleImage, RoleView:
		return true
	}
	return false
}

// Action is one node-level capability known from source attributes. Screen
// level keys are not node actions.
type Action string

const (
	ActionTap       Action = "tap"
	ActionLongPress Action = "long_press"
	ActionText      Action = "text"
	ActionSwipe     Action = "swipe"
)

// Valid reports whether a belongs to the fixed first-version action set.
func (a Action) Valid() bool {
	switch a {
	case ActionTap, ActionLongPress, ActionText, ActionSwipe:
		return true
	}
	return false
}

// Axis is a known scroll axis. The zero value means unknown and is encoded as
// an omitted field.
type Axis string

const (
	AxisX    Axis = "x"
	AxisY    Axis = "y"
	AxisBoth Axis = "both"
)

// Valid reports whether a belongs to the fixed first-version axis set.
func (a Axis) Valid() bool {
	return a == AxisX || a == AxisY || a == AxisBoth
}

// Bounds is a half-open rectangle in target-display logical pixels.
type Bounds struct {
	Left, Top, Right, Bottom int
}

// Width returns the rectangle width.
func (b Bounds) Width() int { return b.Right - b.Left }

// Height returns the rectangle height.
func (b Bounds) Height() int { return b.Bottom - b.Top }

// Contains reports whether the point lies inside the half-open rectangle.
func (b Bounds) Contains(x, y int) bool {
	return x >= b.Left && x < b.Right && y >= b.Top && y < b.Bottom
}

// MarshalJSONTo encodes the rectangle as [left,top,right,bottom].
func (b Bounds) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginArray); err != nil {
		return err
	}
	for _, v := range []int{b.Left, b.Top, b.Right, b.Bottom} {
		if err := writeInt(enc, v); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndArray)
}

// UnmarshalJSONFrom decodes [left,top,right,bottom] with no extra members.
func (b *Bounds) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	var values []int
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	if len(values) != 4 {
		return fmt.Errorf("bounds must have exactly four integers")
	}
	if values[2] < values[0] || values[3] < values[1] {
		return fmt.Errorf("bounds must be a non-negative rectangle")
	}
	*b = Bounds{Left: values[0], Top: values[1], Right: values[2], Bottom: values[3]}
	return nil
}

// Node is the complete public tree node. Its encoding omits known defaults:
// enabled=true, focused/selected/protected/checked=false, empty collections,
// and absent value/hint/axis/resource/context. Unknown tri-state values are
// explicit null. On decode, an absent tri-state member restores its known
// default (enabled=true, the others false); explicit null remains unknown.
type Node struct {
	Ref       string
	Role      Role
	Bounds    *Bounds
	Label     []string
	Actions   []Action
	Enabled   *bool
	Focused   *bool
	Selected  *bool
	Checked   *bool
	Value     *string
	Hint      *string
	Protected *bool
	Axis      Axis
	Resource  *string
	Context   bool
	Children  []*Node
}

// Clone returns a deep copy; published trees are immutable.
func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	c := *n
	if n.Bounds != nil {
		b := *n.Bounds
		c.Bounds = &b
	}
	c.Label = append([]string(nil), n.Label...)
	c.Actions = append([]Action(nil), n.Actions...)
	c.Enabled = cloneBool(n.Enabled)
	c.Focused = cloneBool(n.Focused)
	c.Selected = cloneBool(n.Selected)
	c.Checked = cloneBool(n.Checked)
	c.Protected = cloneBool(n.Protected)
	c.Value = cloneString(n.Value)
	c.Hint = cloneString(n.Hint)
	c.Resource = cloneString(n.Resource)
	if n.Children != nil {
		c.Children = make([]*Node, len(n.Children))
		for i, child := range n.Children {
			c.Children[i] = child.Clone()
		}
	}
	return &c
}

func cloneBool(v *bool) *bool {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func cloneString(v *string) *string {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// MarshalJSONTo writes the complete Node with protocol default omission.
func (n *Node) MarshalJSONTo(enc *jsontext.Encoder) error {
	if n == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "ref"); err != nil {
		return err
	}
	if err := writeString(enc, n.Ref); err != nil {
		return err
	}
	if err := writeName(enc, "role"); err != nil {
		return err
	}
	if err := writeString(enc, string(n.Role)); err != nil {
		return err
	}
	if err := writeName(enc, "bounds"); err != nil {
		return err
	}
	if n.Bounds == nil {
		if err := writeNull(enc); err != nil {
			return err
		}
	} else if err := MarshalJSONTo(enc, n.Bounds); err != nil {
		return err
	}
	if len(n.Label) > 0 {
		if err := writeName(enc, "label"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, label := range n.Label {
			if err := writeString(enc, label); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
	}
	if len(n.Actions) > 0 {
		if err := writeName(enc, "actions"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, action := range n.Actions {
			if err := writeString(enc, string(action)); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
	}
	// enabled: default true; unknown is an explicit null.
	if n.Enabled == nil || !*n.Enabled {
		if err := writeName(enc, "enabled"); err != nil {
			return err
		}
		if n.Enabled == nil {
			if err := writeNull(enc); err != nil {
				return err
			}
		} else if err := writeBool(enc, false); err != nil {
			return err
		}
	}
	// focused/selected/protected: default false; unknown is an explicit null.
	for _, field := range []struct {
		name  string
		value *bool
	}{
		{"focused", n.Focused},
		{"selected", n.Selected},
		{"protected", n.Protected},
	} {
		if field.value != nil && !*field.value {
			continue
		}
		if err := writeName(enc, field.name); err != nil {
			return err
		}
		if field.value == nil {
			if err := writeNull(enc); err != nil {
				return err
			}
		} else if err := writeBool(enc, *field.value); err != nil {
			return err
		}
	}
	// checked: default false; true and unknown are explicit.
	if n.Checked == nil || *n.Checked {
		if err := writeName(enc, "checked"); err != nil {
			return err
		}
		if n.Checked == nil {
			if err := writeNull(enc); err != nil {
				return err
			}
		} else if err := writeBool(enc, true); err != nil {
			return err
		}
	}
	if n.Value != nil {
		if err := writeName(enc, "value"); err != nil {
			return err
		}
		if err := writeString(enc, *n.Value); err != nil {
			return err
		}
	}
	if n.Hint != nil {
		if err := writeName(enc, "hint"); err != nil {
			return err
		}
		if err := writeString(enc, *n.Hint); err != nil {
			return err
		}
	}
	if n.Axis != "" {
		if err := writeName(enc, "axis"); err != nil {
			return err
		}
		if err := writeString(enc, string(n.Axis)); err != nil {
			return err
		}
	}
	if n.Resource != nil {
		if err := writeName(enc, "resource"); err != nil {
			return err
		}
		if err := writeString(enc, *n.Resource); err != nil {
			return err
		}
	}
	if n.Context {
		if err := writeName(enc, "context"); err != nil {
			return err
		}
		if err := writeBool(enc, true); err != nil {
			return err
		}
	}
	if len(n.Children) > 0 {
		if err := writeName(enc, "children"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, child := range n.Children {
			if err := MarshalJSONTo(enc, child); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// nodeWire mirrors Node with raw values for fields whose presence and null
// distinction cannot be represented by Go zero values.
type nodeWire struct {
	Ref       string         `json:"ref"`
	Role      Role           `json:"role"`
	Bounds    *Bounds        `json:"bounds"`
	Label     []string       `json:"label"`
	Actions   []Action       `json:"actions"`
	Enabled   jsontext.Value `json:"enabled"`
	Focused   jsontext.Value `json:"focused"`
	Selected  jsontext.Value `json:"selected"`
	Checked   jsontext.Value `json:"checked"`
	Value     jsontext.Value `json:"value"`
	Hint      jsontext.Value `json:"hint"`
	Protected jsontext.Value `json:"protected"`
	Axis      jsontext.Value `json:"axis"`
	Resource  jsontext.Value `json:"resource"`
	Context   bool           `json:"context"`
	Children  []*Node        `json:"children"`
}

// UnmarshalJSONFrom decodes a complete Node and restores protocol defaults.
func (n *Node) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire nodeWire
	if err := json.UnmarshalDecode(dec, &wire, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	*n = Node{
		Ref:      wire.Ref,
		Role:     wire.Role,
		Bounds:   wire.Bounds,
		Label:    append([]string{}, wire.Label...),
		Actions:  append([]Action{}, wire.Actions...),
		Context:  wire.Context,
		Children: wire.Children,
	}
	if n.Ref == "" {
		return fmt.Errorf("node ref is required")
	}
	if !n.Role.Valid() {
		return fmt.Errorf("invalid node role %q", n.Role)
	}
	for _, action := range n.Actions {
		if !action.Valid() {
			return fmt.Errorf("invalid node action %q", action)
		}
	}
	enabled, err := decodeTriBool(wire.Enabled, true)
	if err != nil {
		return err
	}
	n.Enabled = enabled
	for _, field := range []struct {
		name string
		raw  jsontext.Value
		dst  **bool
	}{
		{"focused", wire.Focused, &n.Focused},
		{"selected", wire.Selected, &n.Selected},
		{"protected", wire.Protected, &n.Protected},
	} {
		value, err := decodeTriBool(field.raw, false)
		if err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
		*field.dst = value
	}
	checked, err := decodeTriBool(wire.Checked, false)
	if err != nil {
		return fmt.Errorf("checked: %w", err)
	}
	n.Checked = checked
	for _, field := range []struct {
		name string
		raw  jsontext.Value
		dst  **string
	}{
		{"value", wire.Value, &n.Value},
		{"hint", wire.Hint, &n.Hint},
		{"resource", wire.Resource, &n.Resource},
	} {
		value, err := decodeOptionalString(field.raw)
		if err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
		*field.dst = value
	}
	axis, err := decodeOptionalString(wire.Axis)
	if err != nil {
		return fmt.Errorf("axis: %w", err)
	}
	if axis != nil {
		n.Axis = Axis(*axis)
		if !n.Axis.Valid() {
			return fmt.Errorf("invalid axis %q", *axis)
		}
	}
	return nil
}

func decodeTriBool(raw jsontext.Value, defaultValue bool) (*bool, error) {
	if len(raw) == 0 {
		value := defaultValue
		return &value, nil
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func decodeOptionalString(raw jsontext.Value) (*string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

// Scope is the fixed public tree scope name.
const Scope = "reported_visible_with_context"

// Tree is a complete public tree.
type Tree struct {
	Format string
	Scope  string
	Root   *Node
}

// MarshalJSONTo encodes a full tree.
func (t *Tree) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if err := writeName(enc, "format"); err != nil {
		return err
	}
	if err := writeString(enc, "full"); err != nil {
		return err
	}
	if err := writeName(enc, "scope"); err != nil {
		return err
	}
	if err := writeString(enc, Scope); err != nil {
		return err
	}
	if err := writeName(enc, "root"); err != nil {
		return err
	}
	if err := MarshalJSONTo(enc, t.Root); err != nil {
		return err
	}
	return enc.WriteToken(jsontext.EndObject)
}

// UnmarshalJSONFrom decodes a full tree.
func (t *Tree) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	var wire struct {
		Format string `json:"format"`
		Scope  string `json:"scope"`
		Root   *Node  `json:"root"`
	}
	if err := json.Unmarshal(raw, &wire, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if wire.Format != "full" {
		return fmt.Errorf("tree format is not full")
	}
	if wire.Scope != Scope {
		return fmt.Errorf("unsupported tree scope %q", wire.Scope)
	}
	if wire.Root == nil || wire.Root.Role != RoleRoot {
		return fmt.Errorf("full tree requires a root node")
	}
	*t = Tree{Format: wire.Format, Scope: wire.Scope, Root: wire.Root}
	return nil
}

// Field is one explicit set value. An unset Field is the only representation
// of "not modified"; Set with a nil pointer means explicit JSON null.
type Field[T any] struct {
	Set   bool
	Value T
}

// IsZero implements the omitzero contract: an unset field is omitted, an
// explicit false/0/""/[]/null value is encoded.
func (f Field[T]) IsZero() bool { return !f.Set }

// MarshalJSONTo encodes the field's explicit value.
func (f Field[T]) MarshalJSONTo(enc *jsontext.Encoder) error {
	return MarshalJSONTo(enc, f.Value)
}

// UnmarshalJSONFrom records one explicit value, including explicit null.
func (f *Field[T]) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var value T
	if err := json.UnmarshalDecode(dec, &value); err != nil {
		return err
	}
	f.Set = true
	f.Value = value
	return nil
}

// SetNode is one delta set entry. Every emitted field is a complete new value;
// omission means "use the baseline value", never a zero-value default.
type SetNode struct {
	Label     Field[[]string] `json:"label,omitzero"`
	Actions   Field[[]Action] `json:"actions,omitzero"`
	Bounds    Field[*Bounds]  `json:"bounds,omitzero"`
	Enabled   Field[*bool]    `json:"enabled,omitzero"`
	Focused   Field[*bool]    `json:"focused,omitzero"`
	Selected  Field[*bool]    `json:"selected,omitzero"`
	Checked   Field[*bool]    `json:"checked,omitzero"`
	Value     Field[*string]  `json:"value,omitzero"`
	Hint      Field[*string]  `json:"hint,omitzero"`
	Protected Field[*bool]    `json:"protected,omitzero"`
	Axis      Field[*Axis]    `json:"axis,omitzero"`
	Resource  Field[*string]  `json:"resource,omitzero"`
}

// IsZero reports whether no field is set.
func (s SetNode) IsZero() bool {
	return !s.Label.Set && !s.Actions.Set && !s.Bounds.Set && !s.Enabled.Set &&
		!s.Focused.Set && !s.Selected.Set && !s.Checked.Set && !s.Value.Set &&
		!s.Hint.Set && !s.Protected.Set && !s.Axis.Set && !s.Resource.Set
}

// Delta is the fixed-baseline difference representation.
type Delta struct {
	Format   string              `json:"format"`
	Base     string              `json:"base"`
	Set      map[string]SetNode  `json:"set,omitzero"`
	Add      []*Node             `json:"add,omitzero"`
	Remove   []string            `json:"remove,omitzero"`
	Children map[string][]string `json:"children,omitzero"`
	Replace  map[string]*Node    `json:"replace,omitzero"`
}

// IsEmpty reports whether the delta contains no change blocks.
func (d *Delta) IsEmpty() bool {
	return len(d.Set) == 0 && len(d.Add) == 0 && len(d.Remove) == 0 &&
		len(d.Children) == 0 && len(d.Replace) == 0
}

// MarshalJSONTo validates and encodes a delta.
func (d *Delta) MarshalJSONTo(enc *jsontext.Encoder) error {
	if d.Format != "delta" {
		return fmt.Errorf("delta format is invalid")
	}
	if d.Base == "" {
		return fmt.Errorf("delta base is required")
	}
	type wire struct {
		Format   string              `json:"format"`
		Base     string              `json:"base"`
		Set      map[string]SetNode  `json:"set,omitzero"`
		Add      []*Node             `json:"add,omitzero"`
		Remove   []string            `json:"remove,omitzero"`
		Children map[string][]string `json:"children,omitzero"`
		Replace  map[string]*Node    `json:"replace,omitzero"`
	}
	return MarshalJSONTo(enc, wire{
		Format: d.Format, Base: d.Base, Set: d.Set, Add: d.Add,
		Remove: d.Remove, Children: d.Children, Replace: d.Replace,
	})
}

// UnmarshalJSONFrom decodes a delta with the fixed format name.
func (d *Delta) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	type wire struct {
		Format   string              `json:"format"`
		Base     string              `json:"base"`
		Set      map[string]SetNode  `json:"set"`
		Add      []*Node             `json:"add"`
		Remove   []string            `json:"remove"`
		Children map[string][]string `json:"children"`
		Replace  map[string]*Node    `json:"replace"`
	}
	var w wire
	if err := json.UnmarshalDecode(dec, &w, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if w.Format != "delta" {
		return fmt.Errorf("tree format is not delta")
	}
	if w.Base == "" {
		return fmt.Errorf("delta base is required")
	}
	*d = Delta{Format: w.Format, Base: w.Base, Set: w.Set, Add: w.Add,
		Remove: w.Remove, Children: w.Children, Replace: w.Replace}
	return nil
}

// TreeValue is the observation "tree" member: exactly one full tree or one
// delta object.
type TreeValue struct {
	Full  *Tree
	Delta *Delta
}

// MarshalJSONTo encodes the tree value.
func (t TreeValue) MarshalJSONTo(enc *jsontext.Encoder) error {
	switch {
	case t.Full != nil && t.Delta != nil:
		return fmt.Errorf("tree value cannot hold full and delta at once")
	case t.Full != nil:
		return MarshalJSONTo(enc, t.Full)
	case t.Delta != nil:
		return MarshalJSONTo(enc, t.Delta)
	default:
		return writeNull(enc)
	}
}

// UnmarshalJSONFrom decodes a full or delta tree value.
func (t *TreeValue) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	var probe struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	switch probe.Format {
	case "full":
		var tree Tree
		if err := json.Unmarshal(raw, &tree); err != nil {
			return err
		}
		*t = TreeValue{Full: &tree}
	case "delta":
		var delta Delta
		if err := json.Unmarshal(raw, &delta); err != nil {
			return err
		}
		*t = TreeValue{Delta: &delta}
	default:
		return fmt.Errorf("unknown tree format %q", probe.Format)
	}
	return nil
}
