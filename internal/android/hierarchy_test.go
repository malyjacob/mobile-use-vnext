package android

import (
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/device"
)

// Fixture shapes are copied from the reference Python tests, with placeholder
// packages and labels only.

func TestParseHierarchyDisplayInheritanceAndFiltering(t *testing.T) {
	xml := `
	<hierarchy rotation="0">
	  <node class="android.widget.FrameLayout" package="com.example.app"
	        display-id="7" visible-to-user="true" bounds="[0,0][200,200]">
	    <node class="android.widget.Button" text="Duplicate"
	          resource-id="com.example.app:id/action" content-desc="Open"
	          package="com.example.app" clickable="true" focused="true"
	          visible-to-user="true" bounds="[10,10][190,50]" />
	    <node class="android.widget.FrameLayout" display-id="8"
	          visible-to-user="true" bounds="[0,0][200,200]">
	      <node class="android.widget.TextView" text="Overridden child"
	            package="com.example.app" visible-to-user="true"
	            bounds="[10,60][190,90]" />
	    </node>
	  </node>
	  <node class="android.widget.Button" text="Duplicate"
	        resource-id="com.example.app:id/action" content-desc="Open"
	        package="com.example.app" display-id="8"
	        visible-to-user="true" bounds="[10,10][190,50]" />
	  <node class="android.widget.TextView" text="Missing display"
	        package="com.example.app" visible-to-user="true"
	        bounds="[10,100][190,130]" />
	</hierarchy>`

	nodes, root, err := parseHierarchy([]byte(xml), 7, 200, 200)
	if err != nil {
		t.Fatalf("parseHierarchy: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d: %+v", len(nodes), nodes)
	}
	if root != 0 || nodes[0].Parent != -1 {
		t.Fatalf("root=%d parent=%d, want root 0 with parent -1", root, nodes[0].Parent)
	}
	button := nodes[1]
	if button.Text != "Duplicate" || button.ResourceID != "com.example.app:id/action" {
		t.Fatalf("unexpected button identity: %+v", button)
	}
	if button.Description != "Open" || !button.DescSet {
		t.Fatalf("description not preserved: %+v", button)
	}
	if button.Bounds != (device.Rect{Left: 10, Top: 10, Right: 190, Bottom: 50}) {
		t.Fatalf("bounds=%+v", button.Bounds)
	}
	if !button.Visible || !button.Clickable || !button.HasClickable || !button.HasFocused || !button.Focused {
		t.Fatalf("action state not preserved: %+v", button)
	}
	if button.Editable {
		t.Fatalf("a Button must not be editable")
	}
	if nodes[0].Children == nil || len(nodes[0].Children) != 1 || nodes[0].Children[0] != 1 {
		t.Fatalf("parent child link missing: %+v", nodes[0].Children)
	}

	// A missing display-id is not an implicit display zero. A valid dump with
	// no node on the requested display is an empty projection, not an error.
	emptyNodes, emptyRoot, err := parseHierarchy([]byte(xml), 0, 200, 200)
	if err != nil {
		t.Fatalf("empty projection returned an error: %v", err)
	}
	if len(emptyNodes) != 0 || emptyRoot != -1 {
		t.Fatalf("display 0 must not match nodes without display-id: nodes=%d root=%d",
			len(emptyNodes), emptyRoot)
	}
}

func TestParseHierarchyControlAppOnRequestedDisplay(t *testing.T) {
	xml := `
	<hierarchy rotation="0">
	  <node class="android.widget.FrameLayout" package="com.mobileuse.control"
	        display-id="0" visible-to-user="true" bounds="[0,0][200,200]">
	    <node class="android.widget.Button" package="com.mobileuse.control"
	          text="Stop" visible-to-user="true" bounds="[10,10][90,50]" />
	  </node>
	  <node class="android.widget.FrameLayout" package="com.example.app"
	        display-id="0" visible-to-user="true" bounds="[0,0][200,200]">
	    <node class="android.widget.Button" package="com.example.app"
	          text="Target" visible-to-user="true" bounds="[10,60][90,100]" />
	  </node>
	</hierarchy>`

	nodes, _, err := parseHierarchy([]byte(xml), 0, 200, 200)
	if err != nil {
		t.Fatalf("parseHierarchy: %v", err)
	}
	got := make([]string, len(nodes))
	for index, node := range nodes {
		got[index] = node.Text
	}
	if strings.Join(got, ",") != ",Stop,,Target" {
		t.Fatalf("texts=%q", got)
	}
}

func TestParseHierarchyClippingVisibilityAndOffScreen(t *testing.T) {
	xml := `
	<hierarchy rotation="0">
	  <node class="android.widget.EditText" package="com.example.app"
	        display-id="7" text="Partially visible" visible-to-user="true"
	        password="true" bounds="[-10,20][40,60]" />
	  <node class="android.widget.Button" package="com.example.app"
	        display-id="7" text="Hidden" visible-to-user="false"
	        bounds="[20,20][80,60]" />
	  <node class="android.widget.Button" package="com.example.app"
	        display-id="7" text="Outside" visible-to-user="true"
	        bounds="[100,20][150,60]" />
	  <node class="android.widget.Button" package="com.example.app"
	        display-id="7" text="No bounds" visible-to-user="true" />
	</hierarchy>`

	nodes, _, err := parseHierarchy([]byte(xml), 7, 100, 100)
	if err != nil {
		t.Fatalf("parseHierarchy: %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("all display nodes must be kept, got %d", len(nodes))
	}
	partial := nodes[0]
	if !partial.Visible {
		t.Fatalf("partially visible node must be visible")
	}
	if partial.Bounds != (device.Rect{Left: 0, Top: 20, Right: 40, Bottom: 60}) {
		t.Fatalf("clipped bounds=%+v", partial.Bounds)
	}
	if !partial.Editable || !partial.HasPassword || !partial.Password {
		t.Fatalf("EditText hint/password not preserved: %+v", partial)
	}
	hidden := nodes[1]
	if hidden.Visible {
		t.Fatalf("visible-to-user=false must not be visible")
	}
	if hidden.Bounds != (device.Rect{Left: 20, Top: 20, Right: 80, Bottom: 60}) {
		t.Fatalf("on-screen but invisible node keeps clipped bounds: %+v", hidden.Bounds)
	}
	outside := nodes[2]
	if outside.Visible || outside.Bounds != (device.Rect{}) {
		t.Fatalf("off-screen node must be invisible with a zero rect: %+v", outside)
	}
	if outside.RawBounds != (device.Rect{Left: 100, Top: 20, Right: 150, Bottom: 60}) {
		t.Fatalf("raw bounds must be preserved: %+v", outside.RawBounds)
	}
	unbounded := nodes[3]
	if unbounded.Visible || unbounded.Bounds != (device.Rect{}) {
		t.Fatalf("absent bounds must be off-screen: %+v", unbounded)
	}
}

func TestParseHierarchyMalformedBoundsNeverFabricate(t *testing.T) {
	for _, raw := range []string{
		"", "garbage", "[0,0][100,100] trailing", "[0,0]-[100,100]", "[0,0]",
		"[a,b][c,d]", "[0, 0][100, 100]",
	} {
		xml := `<hierarchy rotation="0"><node class="android.widget.TextView" ` +
			`package="com.example.app" display-id="7" visible-to-user="true" bounds="` + raw + `" /></hierarchy>`
		nodes, _, err := parseHierarchy([]byte(xml), 7, 100, 100)
		if err != nil {
			t.Fatalf("bounds %q: unexpected error %v", raw, err)
		}
		if len(nodes) != 1 {
			t.Fatalf("bounds %q: want one node", raw)
		}
		if nodes[0].Visible || nodes[0].Bounds != (device.Rect{}) {
			t.Fatalf("bounds %q: must be off-screen, got %+v", raw, nodes[0])
		}
	}
}

func TestParseHierarchyAttributePresence(t *testing.T) {
	xml := `
	<hierarchy rotation="0">
	  <node class="android.widget.FrameLayout" package="com.example.app"
	        display-id="7" visible-to-user="true" bounds="[0,0][300,400]">
	    <node class="androidx.recyclerview.widget.RecyclerView"
	          package="com.example.app" visible-to-user="true" scrollable="true"
	          bounds="[0,0][300,300]">
	      <node class="android.widget.CheckBox" package="com.example.app"
	            text="Sync" visible-to-user="true" checkable="true"
	            checked="true" selected="true" enabled="false" long-clickable="true"
	            focusable="true" bounds="[10,10][290,60]" />
	    </node>
	  </node>
	</hierarchy>`

	nodes, _, err := parseHierarchy([]byte(xml), 7, 300, 400)
	if err != nil {
		t.Fatalf("parseHierarchy: %v", err)
	}
	if len(nodes) != 3 {
		t.Fatalf("want 3 nodes, got %d", len(nodes))
	}
	scroll := nodes[1]
	checked := nodes[2]
	if !scroll.HasScrollable || !scroll.Scrollable {
		t.Fatalf("scrollable not preserved: %+v", scroll)
	}
	if checked.Parent != 1 || scroll.Parent != 0 || nodes[0].Parent != -1 {
		t.Fatalf("parent links: %d %d %d", nodes[0].Parent, scroll.Parent, checked.Parent)
	}
	if !checked.HasChecked || !checked.Checked || !checked.HasSelected || !checked.Selected {
		t.Fatalf("checked/selected: %+v", checked)
	}
	if !checked.HasEnabled || checked.Enabled {
		t.Fatalf("enabled=false must be present and false: %+v", checked)
	}
	if !checked.HasLongClick || !checked.LongClickable {
		t.Fatalf("long-clickable: %+v", checked)
	}
	// Attributes the source did not report stay absent, not defaulted.
	if checked.HasPassword || checked.HasFocused || checked.HasClickable || checked.HasScrollable {
		t.Fatalf("missing attributes must not be claimed: %+v", checked)
	}
	// Text was reported; description and resource-id were not.
	if !checked.TextSet || checked.Text != "Sync" {
		t.Fatalf("text presence: %+v", checked)
	}
	if checked.DescSet || checked.ResourceSet {
		t.Fatalf("absent description/resource must stay absent: %+v", checked)
	}
}

func TestParseHierarchyMalformedXMLIsControlled(t *testing.T) {
	for _, xml := range []string{
		`<hierarchy><node package="com.example.app">`,
		`<hierarchy rotation="0"></node></hierarchy>`,
		``,
		`not xml at all`,
	} {
		if _, _, err := parseHierarchy([]byte(xml), 7, 200, 200); err == nil {
			t.Fatalf("input %q must fail", xml)
		} else if CodeOf(err) != CodeHierarchy {
			t.Fatalf("input %q: code=%q", xml, CodeOf(err))
		}
	}
}

func TestParseHierarchyNonBMPText(t *testing.T) {
	const text = "中文+BMP✨+非BMP🌸🚀"
	xml := `<hierarchy rotation="0"><node class="android.widget.TextView" package="com.example.app" ` +
		`display-id="7" text="` + text + `" visible-to-user="true" bounds="[0,0][200,50]" /></hierarchy>`
	nodes, _, err := parseHierarchy([]byte(xml), 7, 200, 100)
	if err != nil {
		t.Fatalf("parseHierarchy: %v", err)
	}
	if nodes[0].Text != text {
		t.Fatalf("text=%q", nodes[0].Text)
	}
}
