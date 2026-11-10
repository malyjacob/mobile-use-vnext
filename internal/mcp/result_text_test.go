package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

func decodeResultJSON(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	err := d.Decode(&v)
	return v, err
}

// Independent, test-only oracle. It reconstructs facts from the wire syntax;
// it never calls the encoder to recover omitted information.
func decodeResultText(t *testing.T, text string) (map[string]any, bool, error) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	bad := func() (map[string]any, bool, error) { return nil, false, fmt.Errorf("invalid result text") }
	if len(lines) < 2 || lines[len(lines)-1] != "end result" {
		return bad()
	}
	flag, ok := strings.CutPrefix(lines[0], "result v1 is_error=")
	if !ok || (flag != "true" && flag != "false") {
		return bad()
	}
	root := map[string]any{}
	seen := map[string]bool{}
	put := func(path string, v any) error {
		for old := range seen {
			if old == path || strings.HasPrefix(path, old+"/") || strings.HasPrefix(old, path+"/") {
				return fmt.Errorf("duplicate path %s", path)
			}
		}
		seen[path] = true
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
		for i, p := range parts {
			parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
		}
		var set func(any, []string) (any, error)
		set = func(node any, parts []string) (any, error) {
			if len(parts) == 0 {
				return v, nil
			}
			if node == nil {
				if _, err := strconv.Atoi(parts[0]); err == nil {
					node = []any{}
				} else {
					node = map[string]any{}
				}
			}
			switch n := node.(type) {
			case map[string]any:
				child, err := set(n[parts[0]], parts[1:])
				if err != nil {
					return nil, err
				}
				n[parts[0]] = child
				return n, nil
			case []any:
				i, err := strconv.Atoi(parts[0])
				if err != nil || i < 0 || i > len(n) {
					return nil, fmt.Errorf("invalid array index")
				}
				if i == len(n) {
					n = append(n, nil)
				}
				n[i], err = set(n[i], parts[1:])
				return n, err
			}
			return nil, fmt.Errorf("invalid pointer parent")
		}
		_, err := set(root, parts)
		return err
	}
	putJSON := func(path string, raw []byte) error {
		v, err := decodeResultJSON(raw)
		if err != nil {
			return err
		}
		return put(path, v)
	}
	for i := 1; i < len(lines)-1; i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "json v1 path=") {
			d := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, "json v1 path=")))
			var path string
			if d.Decode(&path) != nil || !strings.HasPrefix(path, "/") {
				return bad()
			}
			rest := strings.TrimPrefix(line, "json v1 path=")[d.InputOffset():]
			value, ok := strings.CutPrefix(rest, " value=")
			if !ok {
				return bad()
			}
			if err := putJSON(path, []byte(value)); err != nil {
				return nil, false, err
			}
			continue
		}
		if strings.HasPrefix(line, "tree v1 ") || strings.HasPrefix(line, "inspect v1 ") || strings.HasPrefix(line, "shell v1 ") || strings.HasPrefix(line, "apps v1 ") || strings.HasPrefix(line, "timeline v1 ") {
			kind := strings.Fields(line)[0]
			end := "end " + kind
			j := i + 1
			for j < len(lines)-1 && lines[j] != end {
				j++
			}
			if j == len(lines)-1 {
				return bad()
			}
			block := strings.Join(lines[i:j+1], "\n") + "\n"
			_, header := parseHeaderFields(t, line)
			switch kind {
			case "tree":
				o, _ := root["observation"].(map[string]any)
				if o["id"] != header["id"] {
					return bad()
				}
				var v any
				if header["format"] == "full" {
					v = parseFullTreeText(t, block)
				} else if header["format"] == "delta" {
					v = parseDeltaText(t, block)
				} else {
					return bad()
				}
				raw, err := protocol.JSON(v)
				if err != nil {
					return nil, false, err
				}
				if err = putJSON("/observation/tree", raw); err != nil {
					return nil, false, err
				}
			case "inspect":
				v := parseInspectionText(t, block)
				raw, err := protocol.JSON(v)
				if err != nil {
					return nil, false, err
				}
				obj, _ := decodeResultJSON(raw)
				for key, value := range obj.(map[string]any) {
					if key == "origin" {
						continue
					} // carried by inspection.origin
					if err := put("/inspection/"+key, value); err != nil {
						return nil, false, err
					}
				}
			case "shell":
				for _, k := range []string{"termination", "exit_code", "truncated"} {
					raw := header[k]
					if k == "termination" {
						raw = quoteResult(raw)
					}
					if err := putJSON("/shell/"+k, []byte(raw)); err != nil {
						return nil, false, err
					}
				}
				output := ""
				chunks := 0
				for _, row := range lines[i+1 : j] {
					if s, ok := strings.CutPrefix(row, "output "); ok {
						var v string
						if json.Unmarshal([]byte(s), &v) != nil {
							return bad()
						}
						output += v
						chunks++
					} else if s, ok := strings.CutPrefix(row, "output_error "); ok {
						if err := putJSON("/shell/output_error", []byte(s)); err != nil {
							return nil, false, err
						}
					} else {
						return bad()
					}
				}
				if header["output_bytes"] == "null" {
					if chunks != 0 {
						return bad()
					}
					if err := put("/shell/output", nil); err != nil {
						return nil, false, err
					}
				} else {
					n, err := strconv.Atoi(header["output_bytes"])
					if err != nil || n != len(output) || chunks == 0 {
						return bad()
					}
					if err := put("/shell/output", output); err != nil {
						return nil, false, err
					}
				}
			case "apps", "timeline":
				prefix, path, countKey := "item ", "/apps/items", "items"
				if kind == "timeline" {
					prefix, path, countKey = "event ", "/diagnosis/timeline", "events"
				}
				items := []any{}
				for _, row := range lines[i+1 : j] {
					s, ok := strings.CutPrefix(row, prefix)
					if !ok {
						return bad()
					}
					v, err := decodeResultJSON([]byte(s))
					if err != nil {
						return bad()
					}
					items = append(items, v)
				}
				n, err := strconv.Atoi(header[countKey])
				if err != nil || n != len(items) {
					return bad()
				}
				if err := put(path, items); err != nil {
					return nil, false, err
				}
				if kind == "apps" {
					for _, key := range []string{"total", "truncated", "captured_at"} {
						raw := header[key]
						if key == "captured_at" {
							raw = quoteResult(raw)
						}
						if err := putJSON("/apps/"+key, []byte(raw)); err != nil {
							return nil, false, err
						}
					}
				}
			default:
				return bad()
			}
			i = j
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		kind, known := resultFields[key]
		if !ok || !known {
			return bad()
		}
		var v any
		var err error
		if kind == fieldToken && !strings.HasPrefix(value, "\"") {
			v = value
		} else {
			v, err = decodeResultJSON([]byte(value))
			if err != nil {
				return bad()
			}
		}
		if err := put("/"+strings.ReplaceAll(key, ".", "/"), v); err != nil {
			return nil, false, err
		}
	}
	return root, flag == "true", nil
}

func assertResultTextParity(t *testing.T, raw []byte, isError bool) string {
	t.Helper()
	text, err := encodeResultText(raw, isError)
	if err != nil {
		t.Fatal(err)
	}
	decoded, flag, err := decodeResultText(t, text)
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	want, err := decodeResultJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if flag != isError || !reflect.DeepEqual(want, decoded) {
		t.Fatalf("fact mismatch\nwant=%#v\ngot=%#v\n%s", want, decoded, text)
	}
	again, err := encodeResultText(raw, isError)
	if err != nil || text != again {
		t.Fatal("nondeterministic text")
	}
	return text
}

func TestTextCodecGolden(t *testing.T) {
	raw := []byte(`{"operation_id":"P1","outcome":"executed","shell":{"termination":"exited","exit_code":0,"output":"hello\r\n世界\n","truncated":false},"error":{"code":"example","message":"a=b\n\"quoted\""}}`)
	text := assertResultTextParity(t, raw, false)
	want := "result v1 is_error=false\noperation_id=P1\noutcome=executed\n\nshell v1 termination=exited exit_code=0 truncated=false output_bytes=14\noutput \"hello\\r\\n\"\noutput \"世界\\n\"\nend shell\nerror.code=example\nerror.message=\"a=b\\n\\\"quoted\\\"\"\nend result\n"
	if text != want {
		t.Fatalf("golden mismatch\n%s", text)
	}
}

func TestTextCodecPresenceAndEscaping(t *testing.T) {
	for _, raw := range []string{
		`{"error":{"code":"example","message":"hello v1 world"}}`,
		`{"operation_id":"0","outcome":"null","phase":"true","observation":{"status":"complete","id":"123"}}`,
		`{"session":null,"can_start":false,"saved":true,"observation":{"status":"partial","id":"","revision":0,"missing":[],"image":null},"error":{"code":"bad = token","message":"\r\n\t\u0000 end result","details":{"a":null,"b":false,"c":0,"d":"","e":[],"large":9007199254740993}}}`,
		`{"shell":{"termination":"unknown","exit_code":null,"output":null,"truncated":null}}`,
		`{"shell":{"termination":"exited","exit_code":0,"output":"","truncated":false,"future/key":{"~":0}}}`,
		`{"apps":{"items":[],"total":0,"truncated":false,"captured_at":"now"},"diagnosis":{"timeline":[],"summary":""}}`,
		`{"future/~key":{"x":[null,false,0,"",[]]},"session":{},"observation":{"status":"complete","tree":{"format":"full","scope":"","root":null}}}`,
	} {
		t.Run(raw[:min(len(raw), 40)], func(t *testing.T) { assertResultTextParity(t, []byte(raw), true) })
	}
}

func TestFutureFieldFallback(t *testing.T) {
	for _, raw := range []string{
		`{"observation":{"status":"complete","id":"O1","tree":{"format":"full","scope":"reported_visible_with_context","root":{"ref":"n1","role":"root","bounds":null,"future":true}}}}`,
		`{"inspection":{"observation_id":"O1","origin":"P1","revision":1,"captured_at":"now","scope":"tree","future":0}}`,
		`{"apps":{"items":[{"name":"N","package":"P","future":false}],"total":1,"truncated":false,"captured_at":"now","future":[]},"diagnosis":{"timeline":[{"at":"now","phase":"p","future":null}],"future/x":{}}}`,
	} {
		assertResultTextParity(t, []byte(raw), false)
	}
}

func TestTextCodecUnrepresentableTreeMetadata(t *testing.T) {
	for _, raw := range []string{
		`{"observation":{"status":"complete","id":"中文","tree":{"format":"full","scope":"reported_visible_with_context","root":{"ref":"n1","role":"root","bounds":null}}}}`,
		`{"observation":{"status":"complete","id":"O1","tree":{"format":"delta","base":"O0","replace":{"n1":{"ref":"n2","role":"text","bounds":null}}}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			text, err := encodeResultText([]byte(raw), false)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(text, "\ntree v1 ") {
				t.Fatal("unrepresentable metadata entered readable block")
			}
			assertResultTextParity(t, []byte(raw), false)
		})
	}
}

func TestResultJSONComparisonPreservesLargeIntegers(t *testing.T) {
	if sameResultJSON([]byte(`{"revision":9007199254740992}`), []byte(`{"revision":9007199254740993}`)) {
		t.Fatal("typed round-trip guard rounded different integers to the same float")
	}
	if !sameResultJSON([]byte(`{"a":0,"revision":9007199254740993}`), []byte(`{"revision":9007199254740993,"a":0}`)) {
		t.Fatal("member order affected semantic comparison")
	}
}

func TestTextCodecTrailer(t *testing.T) {
	for _, text := range []string{"result v1 is_error=false\n", "result v1 is_error=false\noperation_id=P1\noperation_id=P2\nend result\n", "result v1 is_error=false\nresult v1 is_error=false\nend result\n", "diagnostic v1 is_error=true\nend diagnostic\n"} {
		if _, _, err := decodeResultText(t, text); err == nil {
			t.Fatalf("accepted malformed %q", text)
		}
	}
}

func TestTextCodecBlockInterleaving(t *testing.T) {
	raw := []byte(`{"session":{"origin":"P1","state":"active","mode":"virtual"},"apps":{"items":[],"total":0,"truncated":false,"captured_at":"now"},"diagnosis":{"timeline":[{"at":"now"}]},"error":{"code":"e","message":"m"}}`)
	text := assertResultTextParity(t, raw, true)
	for _, part := range []string{"session.mode=virtual\n\napps v1", "end apps\n\ntimeline v1", "end timeline\nerror.code=e", "error.message=\"m\"\nend result"} {
		requireTestContains(t, text, part)
	}
	text = assertResultTextParity(t, []byte(`{"shell":{"termination":"exited","exit_code":0,"output":"x","truncated":false}}`), false)
	requireTestContains(t, text, "is_error=false\n\nshell v1")
}

func TestTextCodecLargeContent(t *testing.T) {
	for _, c := range []struct {
		name  string
		bytes int
		raw   func(string) string
	}{
		{"shell", 64 * 1024, func(s string) string {
			return `{"shell":{"termination":"exited","exit_code":0,"output":` + quoteResult(s) + `,"truncated":false}}`
		}},
		{"apps", 32 * 1024, func(s string) string {
			return `{"apps":{"items":[{"name":` + quoteResult(s) + `,"package":"p"}],"total":1,"truncated":false,"captured_at":"now"}}`
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw := []byte(c.raw(strings.Repeat("x", c.bytes)))
			text := assertResultTextParity(t, raw, false)
			t.Logf("canonical_bytes=%d text_bytes=%d both_bytes=%d", len(raw), len(text), len(raw)+len(text))
		})
	}
}

func TestTextCodecMetadataPaths(t *testing.T) {
	for _, fixture := range resultModeFixtures() {
		if fixture.tool == "inspect" || fixture.tool == "observe" {
			var r protocol.Result
			if err := jsonv2ForTextTest([]byte(fixture.raw), &r); err != nil {
				t.Fatal(err)
			}
			raw, err := canonicalResult(&execution{result: &r})
			if err != nil {
				t.Fatal(err)
			}
			text := assertResultTextParity(t, raw, false)
			if strings.Contains(text, "tree v1 format=delta") && strings.Contains(text, "observation.tree.scope=") {
				t.Fatal("derived delta scope became fact")
			}
		}
	}
}

func jsonv2ForTextTest(raw []byte, r *protocol.Result) error {
	// Protocol response decoding is strict and presence-aware.
	return protocol.Decode(raw, r)
}

func TestTextCodecServiceLimitFixtures(t *testing.T) {
	for _, c := range []struct {
		name, scope string
		limit       int
	}{
		{"observation", "", 128 * 1024}, {"inspection-tree", "tree", 128 * 1024}, {"inspection-subtree", "subtree", 32 * 1024}, {"inspection-query", "query", 32 * 1024},
	} {
		t.Run(c.name, func(t *testing.T) {
			node := testNode("n1", protocol.RoleRoot, nil)
			node.Label = []string{strings.Repeat("x", c.limit-1024)}
			r := &protocol.Result{}
			if c.scope == "" {
				r.Observation = &protocol.Observation{Status: "complete", ID: "O1", Tree: &protocol.TreeValue{Full: &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: node}}}
			} else {
				view := &protocol.InspectionView{Origin: "P1", ObservationID: "O1", Revision: 1, CapturedAt: "now", Scope: c.scope}
				r.Inspection = view
				switch c.scope {
				case "tree":
					view.Tree = &protocol.Tree{Format: "full", Scope: protocol.Scope, Root: node}
				case "subtree":
					view.Root = node
				case "query":
					view.Matches = []protocol.InspectionMatch{{Node: node, Path: []string{}}}
					view.HasTotal = true
					view.Total = 1
					view.Truncated = new(false)
				}
			}
			raw, err := canonicalResult(&execution{result: r})
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) > c.limit || len(raw) < c.limit-2048 {
				t.Fatal("fixture is not near service limit")
			}
			text := assertResultTextParity(t, raw, false)
			if !strings.Contains(text, "\nend tree\n") && !strings.Contains(text, "\nend inspect\n") {
				t.Fatal("large tree fell back instead of readable block")
			}
			t.Logf("canonical_bytes=%d text_bytes=%d both_bytes=%d", len(raw), len(text), len(raw)+len(text))
		})
	}
}
