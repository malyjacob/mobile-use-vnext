package main

import (
	"strconv"
	"testing"
)

// geometryColumnsFullFrameNodes wraps one compact tree body in a legal result v4
// frame with the declared logical node count, so a grammar case needs no live
// adapter.
func geometryColumnsFullFrameNodes(nodes int, body string) string {
	return "result v4 ok\nobs O4821_1\n\ntree v5 full nodes=" +
		strconv.Itoa(nodes) + "\n\n" + body + "\nend tree\nend result\n"
}

// geometryColumnsFullFrame wraps one two-node compact tree body.
func geometryColumnsFullFrame(body string) string {
	return geometryColumnsFullFrameNodes(2, body)
}

// TestAnonymousChainShorthandDecodes is the positive gate for the historical
// bare `[n2 n3]` anonymous chain: the reader decodes it in a compact tree even
// though the canonical v5 encoder never emits it and the live smoke gate still
// rejects every chain in product output.
func TestAnonymousChainShorthandDecodes(t *testing.T) {
	full := geometryColumnsFullFrameNodes(3, "n1 root 0,0,100,100\n [n2 n3]")
	if _, failure := decodeResultText(full); failure != nil {
		t.Fatalf("bare shorthand full tree rejected: %s %v", failure.reason, failure.fields)
	}
	delta := "result v4 ok\nobs O4821_2\n\ntree v5 delta base=O4821_1 changes=1\n\n" +
		"+ n20\n [n21 n22]\nend tree\nend result\n"
	if _, failure := decodeResultText(delta); failure != nil {
		t.Fatalf("bare shorthand delta subtree rejected: %s %v", failure.reason, failure.fields)
	}
	rejected := []struct {
		name string
		text string
	}{
		{
			name: "shorthand as delta definition root",
			text: "result v4 ok\nobs O4821_2\n\ntree v5 delta base=O4821_1 changes=1\n\n" +
				"[n21 n22]\nend tree\nend result\n",
		},
		{
			name: "shorthand with an extra column",
			text: geometryColumnsFullFrameNodes(3, "n1 root 0,0,100,100\n [n2 n3] view"),
		},
		{
			name: "shorthand with one ref",
			text: geometryColumnsFullFrameNodes(2, "n1 root 0,0,100,100\n [n2]"),
		},
	}
	for _, testCase := range rejected {
		if _, failure := decodeResultText(testCase.text); failure == nil {
			t.Fatalf("%s accepted", testCase.name)
		}
	}
}

// TestGeometryColumnDoesNotSwallowCommaAttributes is the regression gate for the
// geometry column scanner: only an integer list (or the "-"/"@" marker) may be
// read as geometry, so a comma inside a label, value, hint or resource string
// stays an attribute value instead of being rejected as a malformed coordinate.
func TestGeometryColumnDoesNotSwallowCommaAttributes(t *testing.T) {
	root := "n1 root 0,0,100,100\n"
	accepted := []struct {
		name string
		body string
	}{
		{name: "value with one comma", body: root + " n2 text value=\"a,b\""},
		{name: "value with three commas", body: root + " n2 text value=\"a,b,c,d\""},
		{name: "value and hint with commas", body: root + " n2 input value=\"a,b\" hint=\"c,d\""},
		{name: "label with commas", body: root + " n2 text \"a,b,c,d\""},
		{name: "resource with one comma", body: root + " n2 tap resource=\"a,b\""},
		{name: "resource with three commas", body: root + " n2 tap resource=\"a,b,c,d\""},
	}
	for _, testCase := range accepted {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(geometryColumnsFullFrame(testCase.body)); failure != nil {
				t.Fatalf("legal frame rejected: %s %v", failure.reason, failure.fields)
			}
		})
	}

	rejected := []struct {
		name string
		body string
	}{
		{name: "rectangle on a non-container", body: root + " n2 group 1,2,3,4"},
		{name: "point on root", body: "n1 root 1,2\n n2 group 1,2"},
		{name: "three coordinates", body: root + " n2 group 1,2,3"},
		{name: "trailing comma", body: root + " n2 group 1,2,"},
	}
	for _, testCase := range rejected {
		t.Run(testCase.name, func(t *testing.T) {
			if _, failure := decodeResultText(geometryColumnsFullFrame(testCase.body)); failure == nil {
				t.Fatalf("malformed geometry accepted: %q", testCase.body)
			}
		})
	}
}

// TestLegacyChainDefinitionsKeepOptionalGeometry pins the historical chain
// syntax the reader still accepts: the geometry column stays optional, so a
// hidden anonymous chain and a delivered point/rectangle chain both parse. A
// chain still has to spell its own geometry, so "@" stays invalid there. The
// live smoke gate that rejects any chain in a product block is unchanged.
func TestLegacyChainDefinitionsKeepOptionalGeometry(t *testing.T) {
	accepted := []string{
		"result v4 ok\nobs O4821_2\n\ntree v5 delta base=O4821_1 changes=2\n\n" +
			"+ n20\n chain [n21 n22] view\n  n23 button 10,10 \"增加\"\n" +
			"+ n30\n chain [n31 n32] view 5,5\n  n33 text 5,5 \"分组\"\n" +
			"end tree\nend result\n",
		"result v4 ok\nobs O4821_2\n\ntree v5 delta base=O4821_1 changes=1\n\n" +
			"+ n40\n chain [n41 n42] group 3,4\n  n43 text 3,4 \"子\"\n" +
			"end tree\nend result\n",
	}
	for index, text := range accepted {
		if _, failure := decodeResultText(text); failure != nil {
			t.Fatalf("legacy chain %d rejected: %s %v", index, failure.reason, failure.fields)
		}
	}
	rejected := []string{
		// A chain may not abbreviate its geometry with @.
		"result v4 ok\nobs O4821_2\n\ntree v5 delta base=O4821_1 changes=1\n\n" +
			"+ n50 group 0,0,10,10\n chain [n51 n52] view @\nend tree\nend result\n",
		// A chain still obeys the compact role binding: scroll is a rectangle.
		"result v4 ok\nobs O4821_2\n\ntree v5 delta base=O4821_1 changes=1\n\n" +
			"+ n60\n chain [n61 n62] scroll 5,5\nend tree\nend result\n",
	}
	for index, text := range rejected {
		if _, failure := decodeResultText(text); failure == nil {
			t.Fatalf("malformed chain %d accepted", index)
		}
	}
}

// TestSetBoundsShapeAcceptsBothArities pins the delta set bounds column: a
// carried point is [x,y], a carried rectangle is [l,t,r,b], and the two
// non-delivered forms stay hidden/null.
func TestSetBoundsShapeAcceptsBothArities(t *testing.T) {
	accepted := []string{"[1,2]", "[-1,-2]", "[1,2,3,4]", "hidden", "null"}
	for _, value := range accepted {
		t.Run("accept "+value, func(t *testing.T) {
			text := "result v4 ok\nobs O4821_2\n\ntree v5 delta base=O4821_1 changes=1\n\n" +
				"~ n2 bounds=" + value + "\nend tree\nend result\n"
			if _, failure := decodeResultText(text); failure != nil {
				t.Fatalf("valid set bounds rejected: %s %v", failure.reason, failure.fields)
			}
		})
	}
	rejected := []string{"[1]", "[1,2,3]", "[1,2,3,4,5]", "[]", "[1,2", "1,2", "unknown"}
	for _, value := range rejected {
		t.Run("reject "+value, func(t *testing.T) {
			text := "result v4 ok\nobs O4821_2\n\ntree v5 delta base=O4821_1 changes=1\n\n" +
				"~ n2 bounds=" + value + "\nend tree\nend result\n"
			if _, failure := decodeResultText(text); failure == nil {
				t.Fatalf("malformed set bounds accepted: %q", value)
			}
		})
	}
}
