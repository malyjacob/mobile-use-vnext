package protocol

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
)

// The read representation is the public delivery model of a tree. It is
// derived from the complete internal projection; the internal model is never
// published. The compact view can omit geometry and nodes. Retained nodes keep
// their original ref, role, content, capabilities and tri-state facts. Readers
// also accept historical chain definitions with consecutive logical refs.

// Public read views.
const (
	ViewCompact  = "compact"
	ViewDetailed = "detailed"
)

// GeometryKind is the public state of one node rectangle.
//
//	GeometryHidden  known geometry that this view does not deliver
//	GeometryUnknown the source did not provide usable geometry
//	GeometryKnown   a delivered rectangle
type GeometryKind uint8

const (
	GeometryHidden GeometryKind = iota
	GeometryUnknown
	GeometryKnown
)

// ReadBounds is the tri-state public geometry of one node.
type ReadBounds struct {
	Kind GeometryKind
	Rect Bounds
}

// KnownBounds builds a delivered rectangle.
func KnownBounds(rect Bounds) ReadBounds { return ReadBounds{Kind: GeometryKnown, Rect: rect} }

// UnknownBounds builds the "source did not provide geometry" state.
func UnknownBounds() ReadBounds { return ReadBounds{Kind: GeometryUnknown} }

// HiddenBounds builds the "known but not delivered by this view" state.
func HiddenBounds() ReadBounds { return ReadBounds{Kind: GeometryHidden} }

// Equal reports whether two public geometry states are identical.
func (b ReadBounds) Equal(other ReadBounds) bool {
	if b.Kind != other.Kind {
		return false
	}
	return b.Kind != GeometryKnown || b.Rect == other.Rect
}

// MarshalJSONTo encodes the modifying form: null for unknown, the controlled
// string "hidden" for hidden geometry and the rectangle array for known
// geometry. A complete node omits hidden geometry instead of writing it.
func (b ReadBounds) MarshalJSONTo(enc *jsontext.Encoder) error {
	switch b.Kind {
	case GeometryHidden:
		return writeString(enc, "hidden")
	case GeometryUnknown:
		return writeNull(enc)
	case GeometryKnown:
		return MarshalJSONTo(enc, &b.Rect)
	}
	return fmt.Errorf("invalid public geometry state")
}

// UnmarshalJSONFrom decodes null, the controlled string "hidden" or a
// rectangle array. Absence is never seen here: the caller decides whether a
// missing member means hidden or "not modified".
func (b *ReadBounds) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}
	return b.decodeValue(raw)
}

func (b *ReadBounds) decodeValue(raw jsontext.Value) error {
	switch string(raw) {
	case "null":
		*b = ReadBounds{Kind: GeometryUnknown}
		return nil
	case `"hidden"`:
		*b = ReadBounds{Kind: GeometryHidden}
		return nil
	}
	var rect Bounds
	if err := json.Unmarshal(raw, &rect); err != nil {
		return fmt.Errorf("bounds must be null, %q or [left,top,right,bottom]", "hidden")
	}
	*b = ReadBounds{Kind: GeometryKnown, Rect: rect}
	return nil
}

// ReadNode is one public node definition. Exactly one of Ref and Chain is
// present: an ordinary node carries its own ref, a chain definition carries
// the consecutive original refs it folds. A chain definition carries the shared
// fields of its members and the children of the last member.
type ReadNode struct {
	Ref       string
	Chain     []string
	Role      Role
	Bounds    ReadBounds
	Label     []string
	Actions   []Action
	Enabled   *bool
	Focused   *bool
	Selected  *bool
	Protected *bool
	Checked   *bool
	Value     *string
	Hint      *string
	Axis      Axis
	Resource  *string
	Context   bool
	Children  []*ReadNode
}

// IsChain reports whether the definition folds more than one ref.
func (n *ReadNode) IsChain() bool { return n != nil && len(n.Chain) > 0 }

// Refs returns the definition refs in chain order; an ordinary node yields its
// single ref.
func (n *ReadNode) Refs() []string {
	if n == nil {
		return nil
	}
	if len(n.Chain) > 0 {
		return n.Chain
	}
	return []string{n.Ref}
}

// Clone returns a deep copy; published read trees are immutable.
func (n *ReadNode) Clone() *ReadNode {
	if n == nil {
		return nil
	}
	c := *n
	c.Chain = append([]string(nil), n.Chain...)
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
		c.Children = make([]*ReadNode, len(n.Children))
		for i, child := range n.Children {
			c.Children[i] = child.Clone()
		}
	}
	return &c
}

// MarshalJSONTo writes one complete read node in the fixed public column order.
func (n *ReadNode) MarshalJSONTo(enc *jsontext.Encoder) error {
	if n == nil {
		return writeNull(enc)
	}
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if len(n.Chain) > 0 {
		if n.Ref != "" {
			return fmt.Errorf("read node cannot carry ref and chain at once")
		}
		if err := writeName(enc, "chain"); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, ref := range n.Chain {
			if err := writeString(enc, ref); err != nil {
				return err
			}
		}
		if err := enc.WriteToken(jsontext.EndArray); err != nil {
			return err
		}
	} else {
		if n.Ref == "" {
			return fmt.Errorf("read node requires ref or chain")
		}
		if err := writeName(enc, "ref"); err != nil {
			return err
		}
		if err := writeString(enc, n.Ref); err != nil {
			return err
		}
	}
	if err := writeName(enc, "role"); err != nil {
		return err
	}
	if err := writeString(enc, string(n.Role)); err != nil {
		return err
	}
	// bounds: hidden geometry is omitted, unknown is an explicit null and
	// known geometry keeps the rectangle array.
	if n.Bounds.Kind != GeometryHidden {
		if err := writeName(enc, "bounds"); err != nil {
			return err
		}
		if err := MarshalJSONTo(enc, n.Bounds); err != nil {
			return err
		}
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
	if n.Checked == nil {
		if err := writeName(enc, "checked"); err != nil {
			return err
		}
		if err := writeNull(enc); err != nil {
			return err
		}
	} else if *n.Checked {
		if err := writeName(enc, "checked"); err != nil {
			return err
		}
		if err := writeBool(enc, true); err != nil {
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

// readNodeWire mirrors ReadNode with raw values for the members whose presence
// and null distinction the Go zero value cannot express.
type readNodeWire struct {
	Ref       jsontext.Value `json:"ref"`
	Chain     []string       `json:"chain"`
	Role      Role           `json:"role"`
	Bounds    jsontext.Value `json:"bounds"`
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
	Children  []*ReadNode    `json:"children"`
}

// UnmarshalJSONFrom decodes one complete read node. A missing bounds member
// means hidden geometry; null means unknown.
func (n *ReadNode) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire readNodeWire
	if err := json.UnmarshalDecode(dec, &wire, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	*n = ReadNode{
		Role:     wire.Role,
		Label:    append([]string{}, wire.Label...),
		Actions:  append([]Action{}, wire.Actions...),
		Context:  wire.Context,
		Children: wire.Children,
	}
	hasRef := len(wire.Ref) > 0 && string(wire.Ref) != "null"
	hasChain := len(wire.Chain) > 0
	switch {
	case hasRef && hasChain:
		return fmt.Errorf("read node cannot carry ref and chain at once")
	case hasRef:
		if err := json.Unmarshal(wire.Ref, &n.Ref); err != nil {
			return fmt.Errorf("read node ref must be a string")
		}
	case hasChain:
		if len(wire.Chain) < 2 {
			return fmt.Errorf("chain requires at least two refs")
		}
		n.Chain = append([]string{}, wire.Chain...)
	default:
		return fmt.Errorf("read node requires ref or chain")
	}
	for _, ref := range n.Refs() {
		if !ValidPublicRef(ref) {
			return fmt.Errorf("invalid public ref %q", ref)
		}
	}
	if !n.Role.Valid() {
		return fmt.Errorf("invalid node role %q", n.Role)
	}
	if len(wire.Bounds) == 0 {
		n.Bounds = ReadBounds{Kind: GeometryHidden}
	} else if err := n.Bounds.decodeValue(wire.Bounds); err != nil {
		return err
	}
	for _, action := range n.Actions {
		if !action.Valid() {
			return fmt.Errorf("invalid node action %q", action)
		}
	}
	enabled, err := decodeTriBool(wire.Enabled, true)
	if err != nil {
		return fmt.Errorf("enabled: %w", err)
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

// ValidPublicRef reports whether ref is one of the fixed public n-prefixed
// refs: "n" followed by a non-zero digit and further digits.
func ValidPublicRef(ref string) bool {
	if len(ref) < 2 || ref[0] != 'n' || ref[1] < '1' || ref[1] > '9' {
		return false
	}
	for i := 1; i < len(ref); i++ {
		if ref[i] < '0' || ref[i] > '9' {
			return false
		}
	}
	return true
}

// ReadTree is a complete public tree in one view.
type ReadTree struct {
	Format string
	Scope  string
	View   string
	Root   *ReadNode
}

// MarshalJSONTo encodes a complete read tree.
func (t *ReadTree) MarshalJSONTo(enc *jsontext.Encoder) error {
	if t == nil {
		return writeNull(enc)
	}
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
	if err := writeName(enc, "view"); err != nil {
		return err
	}
	if err := writeString(enc, t.View); err != nil {
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

// UnmarshalJSONFrom decodes a complete read tree and validates its refs and
// view.
func (t *ReadTree) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var wire struct {
		Format string    `json:"format"`
		Scope  string    `json:"scope"`
		View   string    `json:"view"`
		Root   *ReadNode `json:"root"`
	}
	if err := json.UnmarshalDecode(dec, &wire, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if wire.Format != "full" {
		return fmt.Errorf("tree format is not full")
	}
	if wire.Scope != Scope {
		return fmt.Errorf("unsupported tree scope %q", wire.Scope)
	}
	if wire.View != ViewCompact && wire.View != ViewDetailed {
		return fmt.Errorf("unsupported tree view %q", wire.View)
	}
	if wire.Root == nil || wire.Root.IsChain() || wire.Root.Role != RoleRoot {
		return fmt.Errorf("full tree requires a root node")
	}
	*t = ReadTree{Format: wire.Format, Scope: wire.Scope, View: wire.View, Root: wire.Root}
	return t.Validate()
}

// Validate checks global ref uniqueness, the view geometry rules and the fixed
// node shapes of one complete read tree.
func (t *ReadTree) Validate() error {
	if t == nil || t.Root == nil {
		return fmt.Errorf("read tree requires a root")
	}
	seen := make(map[string]bool)
	var walk func(n *ReadNode, isRoot bool) error
	walk = func(n *ReadNode, isRoot bool) error {
		if n == nil {
			return fmt.Errorf("read tree holds a nil node")
		}
		if isRoot && (n.IsChain() || n.Role != RoleRoot) {
			return fmt.Errorf("read tree root must be one ordinary root node")
		}
		for _, ref := range n.Refs() {
			if seen[ref] {
				return fmt.Errorf("duplicate public ref %q", ref)
			}
			seen[ref] = true
		}
		if n.Bounds.Kind == GeometryHidden && t.View == ViewDetailed {
			return fmt.Errorf("detailed view must not hide geometry")
		}
		for _, child := range n.Children {
			if err := walk(child, false); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(t.Root, true)
}

// ReadSetNode is one delta set entry of the read representation. Every emitted
// member is a complete new value; omission means "keep the baseline value".
type ReadSetNode struct {
	Label     Field[[]string]   `json:"label,omitzero"`
	Actions   Field[[]Action]   `json:"actions,omitzero"`
	Bounds    Field[ReadBounds] `json:"bounds,omitzero"`
	Enabled   Field[*bool]      `json:"enabled,omitzero"`
	Focused   Field[*bool]      `json:"focused,omitzero"`
	Selected  Field[*bool]      `json:"selected,omitzero"`
	Checked   Field[*bool]      `json:"checked,omitzero"`
	Value     Field[*string]    `json:"value,omitzero"`
	Hint      Field[*string]    `json:"hint,omitzero"`
	Protected Field[*bool]      `json:"protected,omitzero"`
	Axis      Field[*Axis]      `json:"axis,omitzero"`
	Resource  Field[*string]    `json:"resource,omitzero"`
}

// IsZero reports whether no member is set.
func (s ReadSetNode) IsZero() bool {
	return !s.Label.Set && !s.Actions.Set && !s.Bounds.Set && !s.Enabled.Set &&
		!s.Focused.Set && !s.Selected.Set && !s.Checked.Set && !s.Value.Set &&
		!s.Hint.Set && !s.Protected.Set && !s.Axis.Set && !s.Resource.Set
}

// ReadDelta is the fixed-baseline difference between two read trees.
type ReadDelta struct {
	Format   string                 `json:"format"`
	Base     string                 `json:"base"`
	View     string                 `json:"view"`
	Set      map[string]ReadSetNode `json:"set,omitzero"`
	Add      []*ReadNode            `json:"add,omitzero"`
	Remove   []string               `json:"remove,omitzero"`
	Children map[string][]string    `json:"children,omitzero"`
	Replace  map[string]*ReadNode   `json:"replace,omitzero"`
}

// IsEmpty reports whether the delta declares no change block.
func (d *ReadDelta) IsEmpty() bool {
	return len(d.Set) == 0 && len(d.Add) == 0 && len(d.Remove) == 0 &&
		len(d.Children) == 0 && len(d.Replace) == 0
}

// Validate checks the fixed view, the definition anchors and every ref.
func (d *ReadDelta) Validate() error {
	if d == nil {
		return fmt.Errorf("delta is missing")
	}
	if d.Format != "delta" {
		return fmt.Errorf("delta format is invalid")
	}
	if d.Base == "" {
		return fmt.Errorf("delta base is required")
	}
	if d.View != ViewCompact {
		return fmt.Errorf("delta view must be compact")
	}
	for ref, set := range d.Set {
		if !ValidPublicRef(ref) {
			return fmt.Errorf("invalid set ref %q", ref)
		}
		if set.IsZero() {
			return fmt.Errorf("set %q declares no field", ref)
		}
	}
	for _, node := range d.Add {
		if node == nil || node.IsChain() || !ValidPublicRef(node.Ref) {
			return fmt.Errorf("add requires an ordinary definition root")
		}
	}
	for _, ref := range d.Remove {
		if !ValidPublicRef(ref) {
			return fmt.Errorf("invalid remove ref %q", ref)
		}
	}
	for ref, children := range d.Children {
		if !ValidPublicRef(ref) {
			return fmt.Errorf("invalid children ref %q", ref)
		}
		for _, child := range children {
			if !ValidPublicRef(child) {
				return fmt.Errorf("invalid child ref %q", child)
			}
		}
	}
	for ref, node := range d.Replace {
		if node == nil || node.IsChain() || node.Ref != ref {
			return fmt.Errorf("replace %q must define that ordinary ref", ref)
		}
	}
	return nil
}

// MarshalJSONTo validates and encodes a read delta.
func (d *ReadDelta) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := d.Validate(); err != nil {
		return err
	}
	type wire struct {
		Format   string                 `json:"format"`
		Base     string                 `json:"base"`
		View     string                 `json:"view"`
		Set      map[string]ReadSetNode `json:"set,omitzero"`
		Add      []*ReadNode            `json:"add,omitzero"`
		Remove   []string               `json:"remove,omitzero"`
		Children map[string][]string    `json:"children,omitzero"`
		Replace  map[string]*ReadNode   `json:"replace,omitzero"`
	}
	return MarshalJSONTo(enc, wire{
		Format: d.Format, Base: d.Base, View: d.View,
		Set: d.Set, Add: d.Add, Remove: d.Remove, Children: d.Children, Replace: d.Replace,
	})
}

// UnmarshalJSONFrom decodes a read delta with its fixed format and view.
func (d *ReadDelta) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	type wire struct {
		Format   string                 `json:"format"`
		Base     string                 `json:"base"`
		View     string                 `json:"view"`
		Set      map[string]ReadSetNode `json:"set"`
		Add      []*ReadNode            `json:"add"`
		Remove   []string               `json:"remove"`
		Children map[string][]string    `json:"children"`
		Replace  map[string]*ReadNode   `json:"replace"`
	}
	var w wire
	if err := json.UnmarshalDecode(dec, &w, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	*d = ReadDelta{Format: w.Format, Base: w.Base, View: w.View, Set: w.Set,
		Add: w.Add, Remove: w.Remove, Children: w.Children, Replace: w.Replace}
	return d.Validate()
}

// ReadTreeValue is the observation "tree" member: exactly one compact full
// tree or one compact delta object.
type ReadTreeValue struct {
	Full  *ReadTree
	Delta *ReadDelta
}

// MarshalJSONTo encodes the tree value.
func (t ReadTreeValue) MarshalJSONTo(enc *jsontext.Encoder) error {
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

// UnmarshalJSONFrom decodes a full or delta read tree value.
func (t *ReadTreeValue) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
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
		var tree ReadTree
		if err := json.Unmarshal(raw, &tree); err != nil {
			return err
		}
		*t = ReadTreeValue{Full: &tree}
	case "delta":
		var delta ReadDelta
		if err := json.Unmarshal(raw, &delta); err != nil {
			return err
		}
		*t = ReadTreeValue{Delta: &delta}
	default:
		return fmt.Errorf("unknown tree format %q", probe.Format)
	}
	return nil
}

// IsZero implements the omitzero contract for a read set member. It is unused
// on the wire but keeps the type usable as an optional member in tests.
func (s ReadSetNode) IsEmptyValue() bool { return s.IsZero() }
