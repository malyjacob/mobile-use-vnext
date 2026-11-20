package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("second JSON value")
		}
		return nil, fmt.Errorf("trailing JSON: %w", err)
	}
	return v, nil
}

// Independent, test-only oracle. It reconstructs facts from the wire syntax;
// it never calls the encoder to recover omitted information.
func decodeResultText(t *testing.T, text string) (map[string]any, bool, error) {
	t.Helper()
	if text == "" || strings.ContainsRune(text, '\r') ||
		!strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\n\n") {
		return nil, false, fmt.Errorf("result text must be non-empty LF text with one trailer")
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	bad := func() (map[string]any, bool, error) { return nil, false, fmt.Errorf("invalid result text") }
	if len(lines) < 2 || lines[len(lines)-1] != "end result" {
		return bad()
	}
	flag, ok := strings.CutPrefix(lines[0], "result v3 is_error=")
	if !ok || (flag != "true" && flag != "false") {
		return bad()
	}
	root := map[string]any{}
	seen := map[string]bool{}
	put := func(path string, v any) error {
		parts, err := decodeJSONPointer(path)
		if err != nil {
			return err
		}
		for old := range seen {
			if old == path || strings.HasPrefix(path, old+"/") || strings.HasPrefix(old, path+"/") {
				return fmt.Errorf("duplicate path %s", path)
			}
		}
		seen[path] = true
		var set func(any, []string) (any, error)
		set = func(node any, parts []string) (any, error) {
			if len(parts) == 0 {
				return v, nil
			}
			if node == nil {
				node = map[string]any{}
			}
			n, ok := node.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid pointer parent")
			}
			child, err := set(n[parts[0]], parts[1:])
			if err != nil {
				return nil, err
			}
			n[parts[0]] = child
			return n, nil
		}
		_, err = set(root, parts)
		return err
	}
	putJSON := func(path string, raw []byte) error {
		v, err := decodeResultJSON(raw)
		if err != nil {
			return err
		}
		return put(path, v)
	}
	groupOpen := false
	groupFields := 0
	groupObservationID := ""
	closeGroup := func() error {
		if groupOpen && groupFields == 0 {
			return fmt.Errorf("observation group is empty")
		}
		groupOpen = false
		groupFields = 0
		return nil
	}
	putObservationField := func(relative string, value any) error {
		if relative == "" || strings.HasPrefix(relative, ".") || strings.HasSuffix(relative, ".") ||
			strings.Contains(relative, "..") {
			return fmt.Errorf("malformed observation subpath %q", relative)
		}
		path := "/observation/" + strings.ReplaceAll(relative, ".", "/")
		if err := put(path, value); err != nil {
			return err
		}
		if relative == "id" {
			groupObservationID, _ = value.(string)
		}
		groupFields++
		return nil
	}
	previousBlank := false
	for i := 1; i < len(lines)-1; i++ {
		line := lines[i]
		if line == "" {
			if err := closeGroup(); err != nil {
				return nil, false, err
			}
			if previousBlank {
				return nil, false, fmt.Errorf("repeated blank line")
			}
			previousBlank = true
			continue
		}
		previousBlank = false
		if strings.HasSuffix(line, " ") || strings.ContainsAny(line, "\t\r") {
			return nil, false, fmt.Errorf("line has non-canonical trailing or control whitespace")
		}
		if strings.HasPrefix(line, " ") {
			if !groupOpen {
				return nil, false, fmt.Errorf("indented line outside observation group")
			}
			key, value, ok := strings.Cut(strings.TrimPrefix(line, " "), "=")
			if !ok || key == "" || value == "" {
				return nil, false, fmt.Errorf("malformed observation group field")
			}
			kind, known := resultFields["observation."+key]
			if !known {
				return nil, false, fmt.Errorf("unknown observation field %q", key)
			}
			decoded, err := decodeResultFieldValue(value, kind)
			if err != nil {
				return nil, false, err
			}
			if err := putObservationField(key, decoded); err != nil {
				return nil, false, err
			}
			continue
		}
		if line == "observation" {
			if err := closeGroup(); err != nil {
				return nil, false, err
			}
			groupOpen = true
			continue
		}
		if strings.HasPrefix(line, "json v1 path=") {
			d := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, "json v1 path=")))
			var path string
			if err := closeGroup(); err != nil {
				return nil, false, err
			}
			if d.Decode(&path) != nil || !strings.HasPrefix(path, "/") {
				return bad()
			}
			rest := strings.TrimPrefix(line, "json v1 path=")[d.InputOffset():]
			value, ok := strings.CutPrefix(rest, " value=")
			if !ok || value == "" {
				return bad()
			}
			if err := putJSON(path, []byte(value)); err != nil {
				return nil, false, err
			}
			continue
		}
		if strings.HasPrefix(line, "tree ") || strings.HasPrefix(line, "inspect v3 ") ||
			strings.HasPrefix(line, "shell v1 ") || strings.HasPrefix(line, "timeline v1 ") {
			if err := closeGroup(); err != nil {
				return nil, false, err
			}
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
			switch kind {
			case "tree":
				if !strings.HasPrefix(line, "tree v4 ") {
					return nil, false, fmt.Errorf("ordinary tree is not tree v4")
				}
				if groupObservationID == "" {
					return nil, false, fmt.Errorf("tree block has no observation group id")
				}
				id := groupObservationID
				var v any
				if strings.HasPrefix(line, "tree v4 full ") {
					tree, err := parseFullTreeTextValue(block, id)
					if err != nil {
						return nil, false, err
					}
					v = tree
				} else if strings.HasPrefix(line, "tree v4 delta ") {
					delta, err := parseDeltaTextValue(block, id)
					if err != nil {
						return nil, false, err
					}
					v = delta
				} else {
					return nil, false, fmt.Errorf("unknown tree v4 format")
				}
				raw, err := protocol.JSON(v)
				if err != nil {
					return nil, false, err
				}
				if err = putJSON("/observation/tree", raw); err != nil {
					return nil, false, err
				}
			case "inspect":
				v, err := parseInspectionTextValue(block)
				if err != nil {
					return nil, false, err
				}
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
				_, header, order, err := parseHeaderFieldsValue(line)
				if err != nil {
					return nil, false, err
				}
				if !reflect.DeepEqual(order, []string{"termination", "exit_code", "truncated", "output_bytes"}) {
					return nil, false, fmt.Errorf("shell header fields are invalid")
				}
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
			case "timeline":
				_, header, order, err := parseHeaderFieldsValue(line)
				if err != nil {
					return nil, false, err
				}
				if !reflect.DeepEqual(order, []string{"events"}) {
					return nil, false, fmt.Errorf("timeline header fields are invalid")
				}
				items := []any{}
				for _, row := range lines[i+1 : j] {
					s, ok := strings.CutPrefix(row, "event ")
					if !ok {
						return bad()
					}
					v, err := decodeResultJSON([]byte(s))
					if err != nil {
						return bad()
					}
					items = append(items, v)
				}
				n, err := strconv.Atoi(header["events"])
				if err != nil || n != len(items) {
					return bad()
				}
				if err := put("/diagnosis/timeline", items); err != nil {
					return nil, false, err
				}
			default:
				return bad()
			}
			i = j
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		kind, known := resultFields[key]
		if !ok || !known || key == "" || value == "" {
			return bad()
		}
		if strings.HasPrefix(key, "observation.") {
			return nil, false, fmt.Errorf("observation field outside its group")
		}
		if err := closeGroup(); err != nil {
			return nil, false, err
		}
		v, err := decodeResultFieldValue(value, kind)
		if err != nil {
			return nil, false, err
		}
		if err := put("/"+strings.ReplaceAll(key, ".", "/"), v); err != nil {
			return nil, false, err
		}
	}
	if err := closeGroup(); err != nil {
		return nil, false, err
	}
	return root, flag == "true", nil
}

func decodeJSONPointer(path string) ([]string, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("JSON pointer %q is not absolute", path)
	}
	rawParts := strings.Split(path[1:], "/")
	parts := make([]string, len(rawParts))
	for index, raw := range rawParts {
		var decoded strings.Builder
		for cursor := 0; cursor < len(raw); cursor++ {
			if raw[cursor] != '~' {
				decoded.WriteByte(raw[cursor])
				continue
			}
			if cursor+1 >= len(raw) {
				return nil, fmt.Errorf("JSON pointer has an incomplete escape")
			}
			cursor++
			switch raw[cursor] {
			case '0':
				decoded.WriteByte('~')
			case '1':
				decoded.WriteByte('/')
			default:
				return nil, fmt.Errorf("JSON pointer has an invalid escape")
			}
		}
		parts[index] = decoded.String()
	}
	return parts, nil
}

func decodeResultFieldValue(value string, kind resultFieldKind) (any, error) {
	if kind == fieldBlock {
		return nil, fmt.Errorf("block field cannot use a scalar line")
	}
	if kind == fieldToken && !strings.HasPrefix(value, "\"") {
		if !resultToken(value) {
			return nil, fmt.Errorf("invalid bare token %q", value)
		}
		return value, nil
	}
	v, err := decodeResultJSON([]byte(value))
	if err != nil {
		return nil, err
	}
	if kind == fieldToken {
		if _, ok := v.(string); !ok {
			return nil, fmt.Errorf("token field is not a JSON string")
		}
	}
	if kind == fieldString {
		if _, ok := v.(string); !ok {
			return nil, fmt.Errorf("string field is not a JSON string")
		}
	}
	if kind == fieldLiteral {
		switch v.(type) {
		case nil, bool, json.Number:
		default:
			return nil, fmt.Errorf("literal field has a non-literal JSON value")
		}
	}
	if kind == fieldObject {
		switch v.(type) {
		case nil, map[string]any:
		default:
			return nil, fmt.Errorf("object field is not a JSON object or null")
		}
	}
	return v, nil
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

func TestResultDecoderV3V4Fixture(t *testing.T) {
	text := `result v3 is_error=false
operation_id=P1
observation
 status=complete
 id=Oabc_72
 origin=P1
 revision=2
 quality.state=stable
 quality.samples=3
 quality.stop_reason=stable

tree v4 full nodes=2

n1 root 0,0,1080,2400
 n2 button @ "确定"
end tree
end result
`
	decoded, isError, err := decodeResultText(t, text)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if isError {
		t.Fatal("is_error decoded as true")
	}
	want, err := decodeResultJSON([]byte(`{
		"operation_id":"P1",
		"observation":{
			"status":"complete",
			"id":"Oabc_72",
			"origin":"P1",
			"revision":2,
			"quality":{"state":"stable","samples":3,"stop_reason":"stable"},
			"tree":{
				"format":"full",
				"scope":"reported_visible_with_context",
				"view":"compact",
				"root":{
					"ref":"n1","role":"root","bounds":[0,0,1080,2400],
					"children":[{
						"ref":"n2","role":"button","bounds":[0,0,1080,2400],
						"label":["确定"],"actions":["tap"]
					}]
				}
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, decoded) {
		t.Fatalf("decoded facts mismatch\nwant=%#v\ngot=%#v", want, decoded)
	}
	deltaText := `result v3 is_error=false
observation
 id=Oabc_76

tree v4 delta base=_72 changes=0

end tree
end result
`
	delta, _, err := decodeResultText(t, deltaText)
	if err != nil {
		t.Fatalf("decode delta baseline: %v", err)
	}
	mutations := []struct {
		name     string
		before   string
		after    string
		baseline map[string]any
	}{
		{
			name: "label", before: ` n2 button @ "确定"`,
			after: ` n2 button @ "取消"`, baseline: want.(map[string]any),
		},
		{
			name: "default-action", before: ` n2 button @ "确定"`,
			after: ` n2 button @ "确定" !tap`, baseline: want.(map[string]any),
		},
		{
			name: "rectangle", before: ` n2 button @ "确定"`,
			after: ` n2 button 0,0,1,1 "确定"`, baseline: want.(map[string]any),
		},
		{
			name: "short-base", before: `base=_72`, after: `base=_73`,
			baseline: delta,
		},
	}
	for _, mutation := range mutations {
		t.Run("detect-"+mutation.name, func(t *testing.T) {
			source := text
			if mutation.name == "short-base" {
				source = deltaText
			}
			if strings.Count(source, mutation.before) != 1 {
				t.Fatalf("fixture must contain exactly one mutation point %q", mutation.before)
			}
			changedText := strings.Replace(source, mutation.before, mutation.after, 1)
			changed, _, err := decodeResultText(t, changedText)
			if err != nil {
				t.Fatalf("legal mutation was not decoded: %v", err)
			}
			if reflect.DeepEqual(mutation.baseline, changed) {
				t.Fatalf("complete fact comparison missed %s mutation", mutation.name)
			}
		})
	}
}

func TestResultDecoderV3ObservationGroupsAndTreeBinding(t *testing.T) {
	text := `result v3 is_error=true
observation
 status=partial
 id=Oabc_76
 quality.state=unknown
operation_id=P9
observation
 quality.samples=0
 quality.stop_reason=not_sampled

tree v4 delta base=_72 changes=1

~ n2 label=["late"]
end tree
end result
`
	decoded, isError, err := decodeResultText(t, text)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !isError {
		t.Fatal("is_error decoded as false")
	}
	observation := decoded["observation"].(map[string]any)
	quality := observation["quality"].(map[string]any)
	if observation["status"] != "partial" || observation["id"] != "Oabc_76" {
		t.Fatalf("observation group facts = %#v", observation)
	}
	if quality["state"] != "unknown" || quality["samples"] != json.Number("0") ||
		quality["stop_reason"] != "not_sampled" {
		t.Fatalf("quality group facts = %#v", quality)
	}
	tree := observation["tree"].(map[string]any)
	if tree["format"] != "delta" || tree["base"] != "Oabc_72" || tree["view"] != "compact" {
		t.Fatalf("tree header facts = %#v", tree)
	}
}

func TestResultDecoderV3RejectsAmbiguousOrMalformedText(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"old-version", "result v2 is_error=false\nend result\n"},
		{"crlf", "result v3 is_error=false\r\nend result\r\n"},
		{"missing-final-lf", "result v3 is_error=false\nend result"},
		{"extra-final-lf", "result v3 is_error=false\nend result\n\n"},
		{"empty-group", "result v3 is_error=false\nobservation\nend result\n"},
		{"old-flat-observation", "result v3 is_error=false\nobservation.id=O1\nend result\n"},
		{"bare-token-with-space", "result v3 is_error=false\noperation_id=P 1\nend result\n"},
		{"line-trailing-space", "result v3 is_error=false\noperation_id=P1 \nend result\n"},
		{"unknown-group-field", "result v3 is_error=false\nobservation\n future=true\nend result\n"},
		{"duplicate-group-field", "result v3 is_error=false\nobservation\n id=O1\n id=O2\nend result\n"},
		{"group-ancestor-conflict", "result v3 is_error=false\nobservation\n quality={\"state\":\"stable\"}\n quality.state=stable\nend result\n"},
		{"cross-group-conflict", "result v3 is_error=false\nobservation\n quality.state=stable\noperation_id=P1\nobservation\n quality.state=unstable\nend result\n"},
		{"group-trailing-json-bytes", "result v3 is_error=false\nobservation\n revision=2x\nend result\n"},
		{"tree-without-group-id", "result v3 is_error=false\nobservation\n status=complete\n\ntree v4 full nodes=1\n\nn1 root 0,0,1,1\nend tree\nend result\n"},
		{"tree-with-non-observation-id", "result v3 is_error=false\nobservation\n id=abc\n\ntree v4 full nodes=1\n\nn1 root 0,0,1,1\nend tree\nend result\n"},
		{"v3-ordinary-tree", "result v3 is_error=false\nobservation\n id=O1\n\ntree v3 format=full id=O1\nend tree\nend result\n"},
		{"duplicate-tree", "result v3 is_error=false\nobservation\n id=O1\n\ntree v4 full nodes=1\n\nn1 root 0,0,1,1\nend tree\ntree v4 full nodes=1\n\nn1 root 0,0,1,1\nend tree\nend result\n"},
		{"bad-pointer-escape", "result v3 is_error=false\njson v1 path=\"/future~2key\" value=1\nend result\n"},
		{"pointer-ancestor-conflict", "result v3 is_error=false\njson v1 path=\"/future\" value={}\njson v1 path=\"/future/key\" value=1\nend result\n"},
		{"second-json-value", "result v3 is_error=false\njson v1 path=\"/future\" value=1 2\nend result\n"},
		{"pointer-trailing-json-bytes", "result v3 is_error=false\njson v1 path=\"/future\" value=1x\nend result\n"},
		{"repeated-blank", "result v3 is_error=false\n\n\noperation_id=P1\nend result\n"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, _, err := decodeResultText(t, testCase.text); err == nil {
				t.Fatalf("malformed text was accepted:\n%s", testCase.text)
			}
		})
	}
}

func TestResultDecoderExpandsAndValidatesShortBase(t *testing.T) {
	valid := func(id, base string) string {
		return "result v3 is_error=false\nobservation\n id=" + id +
			"\n\ntree v4 delta base=" + base + " changes=0\n\nend tree\nend result\n"
	}
	decoded, _, err := decodeResultText(t, valid("Oabc_76", "_72"))
	if err != nil {
		t.Fatalf("valid short base: %v", err)
	}
	tree := decoded["observation"].(map[string]any)["tree"].(map[string]any)
	if tree["base"] != "Oabc_72" {
		t.Fatalf("short base expanded to %#v", tree["base"])
	}
	decoded, _, err = decodeResultText(t, valid("Oabc_76", "Odef_72"))
	if err != nil {
		t.Fatalf("valid foreign full base: %v", err)
	}
	tree = decoded["observation"].(map[string]any)["tree"].(map[string]any)
	if tree["base"] != "Odef_72" {
		t.Fatalf("full base changed to %#v", tree["base"])
	}
	const maxUint64 = "18446744073709551615"
	decoded, _, err = decodeResultText(t, valid("Oabc_76", "_"+maxUint64))
	if err != nil {
		t.Fatalf("max uint64 short base: %v", err)
	}
	tree = decoded["observation"].(map[string]any)["tree"].(map[string]any)
	if tree["base"] != "Oabc_"+maxUint64 {
		t.Fatalf("max uint64 base expanded to %#v", tree["base"])
	}
	if _, _, err := decodeResultText(t, valid("Oabc_"+maxUint64, "_1")); err != nil {
		t.Fatalf("max uint64 current sequence: %v", err)
	}
	for _, base := range []string{
		"_0",
		"_072",
		"_",
		"_x",
		"_18446744073709551616",
		"base=bad",
	} {
		t.Run(base, func(t *testing.T) {
			if _, _, err := decodeResultText(t, valid("Oabc_76", base)); err == nil {
				t.Fatalf("short base %q was accepted", base)
			}
		})
	}
	for _, id := range []string{"O_76", "Oabc76", "Oabc_18446744073709551616", "中文_76"} {
		t.Run(id, func(t *testing.T) {
			if _, _, err := decodeResultText(t, valid(id, "_72")); err == nil {
				t.Fatalf("short base with current id %q was accepted", id)
			}
		})
	}
}

func TestResultDecoderKeepsNumericFallbackKeyAsObject(t *testing.T) {
	text := `result v3 is_error=false
json v1 path="/observation/quality/0" value=1
end result
`
	decoded, _, err := decodeResultText(t, text)
	if err != nil {
		t.Fatalf("decode numeric fallback key: %v", err)
	}
	observation, ok := decoded["observation"].(map[string]any)
	if !ok {
		t.Fatalf("observation is not an object: %#v", decoded["observation"])
	}
	quality, ok := observation["quality"].(map[string]any)
	if !ok {
		t.Fatalf("quality is not an object: %#v", observation["quality"])
	}
	if quality["0"] != json.Number("1") {
		t.Fatalf("numeric path key was not retained as an object key: %#v", quality)
	}
}

// TestTextCodecDeliveredObservationOnError pins the contract-6 combination: an
// error result may carry the observation a rejected act delivered, and the tree
// block is projected together with is_error=true.
func TestTextCodecDeliveredObservationOnError(t *testing.T) {
	raw := []byte(`{"operation_id":"P7","operation_state":"finished","outcome":"not_executed","phase":"revalidate",` +
		`"observation":{"status":"complete","id":"O2","origin":"P1","revision":2,"tree":{"format":"full",` +
		`"scope":"reported_visible_with_context","view":"compact",` +
		`"root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}},` +
		`"error":{"code":"target_changed","message":"目标元素已变化，本次未派发"},` +
		`"recovery":{"entry":"retry","reference":"O","input_state":"effective"}}`)
	text := assertResultTextParity(t, raw, true)
	for _, want := range []string{
		"result v3 is_error=true",
		"\nobservation\n status=complete\n id=O2\n",
		"tree v4 full nodes=1",
		"recovery.entry=retry",
		"recovery.reference=O",
		"end result",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text lacks %q:\n%s", want, text)
		}
	}
}

func TestTextCodecGolden(t *testing.T) {
	raw := []byte(`{"operation_id":"P1","outcome":"executed","shell":{"termination":"exited","exit_code":0,"output":"hello\r\n世界\n","truncated":false},"error":{"code":"example","message":"a=b\n\"quoted\""}}`)
	text := assertResultTextParity(t, raw, false)
	want := "result v3 is_error=false\noperation_id=P1\noutcome=executed\n\nshell v1 termination=exited exit_code=0 truncated=false output_bytes=14\noutput \"hello\\r\\n\"\noutput \"世界\\n\"\nend shell\nerror.code=example\nerror.message=\"a=b\\n\\\"quoted\\\"\"\nend result\n"
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
		`{"app":{"kind":"find","items":[],"total":0,"truncated":false,"captured_at":"now"},"diagnosis":{"timeline":[],"summary":""}}`,
		`{"future/~key":{"x":[null,false,0,"",[]]},"session":{},"observation":{"status":"complete","tree":{"format":"full","scope":"","root":null}}}`,
	} {
		t.Run(raw[:min(len(raw), 40)], func(t *testing.T) { assertResultTextParity(t, []byte(raw), true) })
	}
}

func TestFutureFieldFallback(t *testing.T) {
	for _, raw := range []string{
		`{"observation":{"status":"complete","id":"O1","tree":{"format":"full","scope":"reported_visible_with_context","view":"compact","root":{"ref":"n1","role":"root","bounds":null,"future":true}}}}`,
		`{"inspection":{"observation_id":"O1","origin":"P1","revision":1,"captured_at":"now","scope":"tree","future":0}}`,
		`{"app":{"kind":"find","items":[{"name":"N","package":"P","future":false}],"total":1,"truncated":false,"captured_at":"now","future":[]},"diagnosis":{"timeline":[{"at":"now","phase":"p","future":null}],"future/x":{}}}`,
	} {
		assertResultTextParity(t, []byte(raw), false)
	}
}

func TestTextCodecUnrepresentableTreeMetadata(t *testing.T) {
	for _, raw := range []string{
		`{"observation":{"status":"complete","id":"中文","tree":{"format":"full","scope":"reported_visible_with_context","view":"compact","root":{"ref":"n1","role":"root","bounds":null}}}}`,
		`{"observation":{"status":"complete","id":"O1","tree":{"format":"delta","base":"O0","view":"compact","replace":{"n1":{"ref":"n2","role":"text","bounds":null}}}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			text, err := encodeResultText([]byte(raw), false)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(text, "\ntree v4 ") {
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

// TestTextCodecEffectAndQuality pins the readable projection of the two
// contract-10 facts. The ordered writer already follows wire order: effect is
// emitted between phase and target_reference, and observation.quality between
// actionable_until and screen.
func TestTextCodecEffectAndQuality(t *testing.T) {
	raw := []byte(`{"operation_id":"P1","outcome":"executed","phase":"completed",` +
		`"effect":{"state":"unknown"},"target_reference":"O1",` +
		`"observation":{"status":"complete","id":"O2","origin":"P1","revision":2,` +
		`"captured_at":"2026-11-10T00:00:00Z","actionable_until":"2026-11-10T00:01:00Z",` +
		`"quality":{"state":"unknown","samples":0,"stop_reason":"not_sampled"},` +
		`"screen":{"width":1080,"height":2400,"rotation":0},` +
		`"tree":{"format":"full","scope":"reported_visible_with_context",` +
		`"view":"compact",` +
		`"root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}}}`)
	text := assertResultTextParity(t, raw, false)
	requireTestContains(t, text, "\neffect.state=unknown\n")
	for _, line := range []string{
		"quality.state=unknown",
		"quality.samples=0",
		"quality.stop_reason=not_sampled",
	} {
		requireTestContains(t, text, "\n "+line+"\n")
	}
	ordered := []string{
		"\nphase=completed\n",
		"\neffect.state=unknown\n",
		"\ntarget_reference=O1\n",
		"\n actionable_until=2026-11-10T00:01:00Z\n",
		"\n quality.state=unknown\n",
		"\n quality.stop_reason=not_sampled\n",
		"\n screen=",
	}
	last := -1
	for _, fragment := range ordered {
		index := strings.Index(text, fragment)
		if index <= last {
			t.Fatalf("readable projection order broke at %q:\n%s", fragment, text)
		}
		last = index
	}
}

// TestTextCodecQualityAndEffectStates proves every declared enum value is
// emitted as the same readable token, including the explicit samples=0 fact.
func TestTextCodecQualityAndEffectStates(t *testing.T) {
	for _, quality := range []struct {
		state   string
		samples int
		stop    string
	}{
		{protocol.QualityStable, 3, protocol.QualityStopStable},
		{protocol.QualityUnstable, 5, protocol.QualityStopMaxSamples},
		{protocol.QualityUnknown, 1, protocol.QualityStopUnreadable},
		{protocol.QualityUnknown, 0, protocol.QualityStopNotSampled},
	} {
		raw := []byte(fmt.Sprintf(
			`{"observation":{"status":"complete","id":"O1","quality":{`+
				`"state":%q,"samples":%d,"stop_reason":%q}}}`,
			quality.state, quality.samples, quality.stop))
		text := assertResultTextParity(t, raw, false)
		requireTestContains(t, text, " quality.state="+quality.state+"\n")
		requireTestContains(t, text, fmt.Sprintf(" quality.samples=%d\n", quality.samples))
		requireTestContains(t, text, " quality.stop_reason="+quality.stop+"\n")
	}
	for _, state := range []string{protocol.EffectObserved, protocol.EffectNotObserved, protocol.EffectUnknown} {
		raw := []byte(fmt.Sprintf(`{"effect":{"state":%q}}`, state))
		text := assertResultTextParity(t, raw, false)
		requireTestContains(t, text, "effect.state="+state+"\n")
	}
}

// TestDiagnosticDoesNotProjectQuality pins the existing diagnostic whitelist:
// diagnostic v1 carries only observation.status/id, so the observation quality
// object never becomes a diagnostic fact even when the result is an error.
func TestDiagnosticDoesNotProjectQuality(t *testing.T) {
	raw := []byte(`{"operation_id":"P1","outcome":"not_executed","phase":"revalidate",` +
		`"observation":{"status":"complete","id":"O2",` +
		`"quality":{"state":"unknown","samples":0,"stop_reason":"not_sampled"},` +
		`"tree":{"format":"full","scope":"reported_visible_with_context",` +
		`"root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}},` +
		`"error":{"code":"target_changed","message":"目标变化"}}`)
	text, err := encodeResultDiagnostic(raw)
	if err != nil {
		t.Fatal(err)
	}
	requireTestContains(t, text, "diagnostic v1 is_error=true\n")
	requireTestContains(t, text, "observation.status=complete\n")
	requireTestContains(t, text, "error.code=target_changed\n")
	requireTestNotContains(t, text, "quality")
}

func TestTextCodecTrailer(t *testing.T) {
	for _, text := range []string{"result v3 is_error=false\n", "result v3 is_error=false\noperation_id=P1\noperation_id=P2\nend result\n", "result v3 is_error=false\nresult v3 is_error=false\nend result\n", "diagnostic v1 is_error=true\nend diagnostic\n"} {
		if _, _, err := decodeResultText(t, text); err == nil {
			t.Fatalf("accepted malformed %q", text)
		}
	}
}

func TestTextCodecBlockInterleaving(t *testing.T) {
	raw := []byte(`{"session":{"origin":"P1","state":"active","mode":"virtual"},"app":{"kind":"find","items":[],"total":0,"truncated":false,"captured_at":"now"},"diagnosis":{"timeline":[{"at":"now"}]},"error":{"code":"e","message":"m"}}`)
	text := assertResultTextParity(t, raw, true)
	for _, part := range []string{"session.mode=virtual\napp.kind=find", "app.items=[]", "end timeline\nerror.code=e", "error.message=\"m\"\nend result"} {
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
		{"app", 32 * 1024, func(s string) string {
			return `{"app":{"kind":"find","items":[{"name":` + quoteResult(s) + `,"package":"p"}],"total":1,"truncated":false,"captured_at":"now"}}`
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
			if strings.Contains(text, "tree v4 delta ") && strings.Contains(text, "observation.tree.scope=") {
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
			node := testNode("n1", protocol.RoleRoot, protocol.UnknownBounds())
			node.Label = []string{strings.Repeat("x", c.limit-1024)}
			r := &protocol.Result{}
			if c.scope == "" {
				r.Observation = &protocol.Observation{Status: "complete", ID: "O1", Tree: &protocol.ReadTreeValue{Full: &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewCompact, Root: node}}}
			} else {
				view := &protocol.InspectionView{Origin: "P1", ObservationID: "O1", Revision: 1, CapturedAt: "now", Scope: c.scope}
				r.Inspection = view
				switch c.scope {
				case "tree":
					view.Tree = &protocol.ReadTree{Format: "full", Scope: protocol.Scope, View: protocol.ViewDetailed, Root: node}
				case "subtree":
					view.View = protocol.ViewDetailed
					view.Root = node
				case "query":
					view.View = protocol.ViewDetailed
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
