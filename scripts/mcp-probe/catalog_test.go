package main

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpsource "github.com/malyjacob/mobile-use-vnext/internal/mcp"
)

func sourceCatalogTools(t *testing.T) []*sdk.Tool {
	t.Helper()
	snapshot := mcpsource.CatalogSnapshot()
	tools := make([]*sdk.Tool, 0, len(snapshot))
	for _, tool := range snapshot {
		entry := &sdk.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: jsontext.Value(bytes.Clone(tool.Schema)),
		}
		if tool.ReadOnlyHint {
			entry.Annotations = &sdk.ToolAnnotations{ReadOnlyHint: true}
		}
		tools = append(tools, entry)
	}
	return tools
}

func TestSourceCatalogIsFrozenThirteen(t *testing.T) {
	if count := mcpsource.ToolCount(); count != 13 || count != len(toolNames) {
		t.Fatalf("tool count = %d, probe = %d, want 13", count, len(toolNames))
	}
	snapshot := mcpsource.CatalogSnapshot()
	if len(snapshot) != len(toolNames) {
		t.Fatalf("snapshot length = %d", len(snapshot))
	}
	seen := make(map[string]bool, len(snapshot))
	for _, tool := range snapshot {
		if seen[tool.Name] {
			t.Fatalf("snapshot has duplicate tool %q", tool.Name)
		}
		seen[tool.Name] = true
		if !containsString(toolNames, tool.Name) {
			t.Fatalf("snapshot has unexpected tool %q", tool.Name)
		}
	}
	for _, wanted := range toolNames {
		if !seen[wanted] {
			t.Fatalf("snapshot is missing tool %q", wanted)
		}
	}
	for _, legacy := range legacyToolNames {
		if containsString(toolNames, legacy) {
			t.Fatalf("probe expectation still lists legacy tool %q", legacy)
		}
	}
}

func TestCheckLiveCatalog(t *testing.T) {
	t.Run("source catalog passes", func(t *testing.T) {
		if failure := checkLiveCatalog(sourceCatalogTools(t)); failure != nil {
			t.Fatalf("source catalog rejected: %s %v", failure.reason, failure.fields)
		}
	})
	t.Run("semantic schema order", func(t *testing.T) {
		tools := sourceCatalogTools(t)
		var schema map[string]any
		if err := json.Unmarshal([]byte(mcpsource.CatalogSnapshot()[0].Schema), &schema); err != nil {
			t.Fatal(err)
		}
		tools[0].InputSchema = schema
		if failure := checkLiveCatalog(tools); failure != nil {
			t.Fatalf("re-serialized schema rejected: %s", failure.reason)
		}
	})
	t.Run("legacy tool present", func(t *testing.T) {
		tools := sourceCatalogTools(t)
		tools[7].Name = "apps"
		if failure := checkLiveCatalog(tools); failure == nil || failure.reason != "legacy_tool_present" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("count mismatch", func(t *testing.T) {
		tools := sourceCatalogTools(t)
		if failure := checkLiveCatalog(tools[:len(tools)-1]); failure == nil ||
			failure.reason != "catalog_size_mismatch" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("name order is irrelevant", func(t *testing.T) {
		tools := sourceCatalogTools(t)
		// The SDK may return a different order than the source catalog, including
		// a fully sorted one. The live/source comparison must not depend on it.
		sort.SliceStable(tools, func(left, right int) bool {
			return tools[left].Name < tools[right].Name
		})
		if failure := checkLiveCatalog(tools); failure != nil {
			t.Fatalf("sorted tools/list rejected: %s", failure.reason)
		}
		reverse := func(values []*sdk.Tool) {
			for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
				values[left], values[right] = values[right], values[left]
			}
		}
		reverse(tools)
		if failure := checkLiveCatalog(tools); failure != nil {
			t.Fatalf("reversed tools/list rejected: %s", failure.reason)
		}
	})
	t.Run("missing name", func(t *testing.T) {
		tools := sourceCatalogTools(t)
		tools[0].Name = "act"
		if failure := checkLiveCatalog(tools); failure == nil ||
			failure.reason != "catalog_name_duplicate" && failure.reason != "catalog_name_missing" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("unexpected name", func(t *testing.T) {
		tools := sourceCatalogTools(t)
		tools[0].Name = "legacy_extra"
		if failure := checkLiveCatalog(tools); failure == nil ||
			failure.reason != "catalog_name_unexpected" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("description mismatch", func(t *testing.T) {
		tools := sourceCatalogTools(t)
		tools[2].Description += " drift"
		if failure := checkLiveCatalog(tools); failure == nil ||
			failure.reason != "catalog_description_mismatch" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("schema mismatch", func(t *testing.T) {
		tools := sourceCatalogTools(t)
		var schema map[string]any
		if err := json.Unmarshal([]byte(mcpsource.CatalogSnapshot()[3].Schema), &schema); err != nil {
			t.Fatal(err)
		}
		schema["additionalProperties"] = true
		tools[3].InputSchema = schema
		if failure := checkLiveCatalog(tools); failure == nil ||
			failure.reason != "catalog_schema_mismatch" {
			t.Fatalf("failure = %v", failure)
		}
	})
}

func TestCheckLiveCatalogReadOnlyHints(t *testing.T) {
	for _, test := range []struct {
		name        string
		annotations *sdk.ToolAnnotations
	}{
		{name: "status"},
		{name: "act", annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tools := sourceCatalogTools(t)
			for _, tool := range tools {
				if tool.Name == test.name {
					tool.Annotations = test.annotations
				}
			}
			if failure := checkLiveCatalog(tools); failure == nil ||
				failure.reason != "read_only_annotation_mismatch" ||
				len(failure.fields) != 1 || failure.fields[0] != field("tool", test.name) {
				t.Fatalf("failure = %v, want read-only annotation mismatch for %s", failure, test.name)
			}
		})
	}
}

func actToolFrom(t *testing.T, tools []*sdk.Tool) *sdk.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == "act" {
			return tool
		}
	}
	t.Fatal("act tool missing")
	return nil
}

// actOperationSchema decodes the act schema into mutable JSON so a reverse
// control can remove or change one member without touching the source catalog.
func actOperationSchema(t *testing.T, tool *sdk.Tool) (map[string]any, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("act schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatalf("act schema: %v", err)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("act schema properties missing")
	}
	operation, ok := properties["operation"].(map[string]any)
	if !ok {
		t.Fatal("act operation missing")
	}
	operationProperties, ok := operation["properties"].(map[string]any)
	if !ok {
		t.Fatal("act operation properties missing")
	}
	return schema, operationProperties
}

// TestCheckActScrollSchema pins the contract-10 act surface the probe requires
// from the live tool: scroll declared, all scroll members present, exact
// direction enum and duration bounds. Each subtest is a reverse control that
// removes or changes one member and expects the matching distinct reason.
func TestCheckActScrollSchema(t *testing.T) {
	t.Run("source act schema passes", func(t *testing.T) {
		if failure := checkActScrollSchema(actToolFrom(t, sourceCatalogTools(t))); failure != nil {
			t.Fatalf("source act schema rejected: %s %v", failure.reason, failure.fields)
		}
	})
	t.Run("tool count is frozen thirteen", func(t *testing.T) {
		if count := mcpsource.ToolCount(); count != 13 || count != len(toolNames) {
			t.Fatalf("tool count = %d, probe = %d, want 13", count, len(toolNames))
		}
	})
	t.Run("scroll enum missing", func(t *testing.T) {
		act := actToolFrom(t, sourceCatalogTools(t))
		schema, operation := actOperationSchema(t, act)
		actionType, ok := operation["type"].(map[string]any)
		if !ok {
			t.Fatal("operation.type missing")
		}
		enum, ok := actionType["enum"].([]any)
		if !ok {
			t.Fatal("operation.type enum missing")
		}
		kept := make([]any, 0, len(enum))
		for _, member := range enum {
			if member != "scroll" {
				kept = append(kept, member)
			}
		}
		actionType["enum"] = kept
		act.InputSchema = schema
		if failure := checkActScrollSchema(act); failure == nil ||
			failure.reason != "act_type_scroll_missing" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("scroll member missing", func(t *testing.T) {
		act := actToolFrom(t, sourceCatalogTools(t))
		schema, operation := actOperationSchema(t, act)
		delete(operation, "duration_ms")
		act.InputSchema = schema
		if failure := checkActScrollSchema(act); failure == nil ||
			failure.reason != "act_scroll_member_missing" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("direction enum mismatch", func(t *testing.T) {
		act := actToolFrom(t, sourceCatalogTools(t))
		schema, operation := actOperationSchema(t, act)
		direction, ok := operation["direction"].(map[string]any)
		if !ok {
			t.Fatal("operation.direction missing")
		}
		direction["enum"] = []any{"up", "down"}
		act.InputSchema = schema
		if failure := checkActScrollSchema(act); failure == nil ||
			failure.reason != "act_direction_enum_mismatch" {
			t.Fatalf("failure = %v", failure)
		}
	})
	t.Run("duration bounds mismatch", func(t *testing.T) {
		act := actToolFrom(t, sourceCatalogTools(t))
		schema, operation := actOperationSchema(t, act)
		duration, ok := operation["duration_ms"].(map[string]any)
		if !ok {
			t.Fatal("operation.duration_ms missing")
		}
		duration["maximum"] = 2001
		act.InputSchema = schema
		if failure := checkActScrollSchema(act); failure == nil ||
			failure.reason != "act_duration_bounds_mismatch" {
			t.Fatalf("failure = %v", failure)
		}
	})
}

func TestCatalogFingerprint(t *testing.T) {
	fingerprint := mcpsource.CatalogFingerprint()
	if !isLowerHex64(fingerprint) {
		t.Fatalf("fingerprint = %q", fingerprint)
	}
	recomputed, err := expectedCatalogFingerprint()
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if recomputed != fingerprint {
		t.Fatalf("recomputed = %s, source = %s", recomputed, fingerprint)
	}

	var output bytes.Buffer
	runner := &probe{reporter: newReporter(&output)}
	if !runner.catalogIdentityCheck() {
		t.Fatalf("identity check failed:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "PASS catalog_fingerprint") {
		t.Fatalf("missing catalog fingerprint line:\n%s", output.String())
	}
}

func writeCatalogManifest(t *testing.T, count int, hash string) string {
	t.Helper()
	body := `{"format":"test","tool_catalog":{"count":` + strconv.Itoa(count) +
		`,"sha256":"` + hash + `"}}`
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func TestCatalogManifestIdentity(t *testing.T) {
	fingerprint := mcpsource.CatalogFingerprint()
	good := writeCatalogManifest(t, 13, fingerprint)

	var output bytes.Buffer
	runner := &probe{reporter: newReporter(&output), manifests: []string{good}}
	if !runner.catalogIdentityCheck() {
		t.Fatalf("good manifest rejected:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "PASS catalog_manifest") {
		t.Fatalf("missing manifest pass:\n%s", output.String())
	}

	badCount := writeCatalogManifest(t, 12, fingerprint)
	output.Reset()
	runner = &probe{reporter: newReporter(&output), manifests: []string{badCount}}
	if runner.catalogIdentityCheck() {
		t.Fatalf("count mismatch accepted:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "manifest_count_mismatch") {
		t.Fatalf("missing count failure:\n%s", output.String())
	}

	badHash := writeCatalogManifest(t, 13, strings.Repeat("0", 64))
	output.Reset()
	runner = &probe{reporter: newReporter(&output), manifests: []string{badHash}}
	if runner.catalogIdentityCheck() {
		t.Fatalf("hash mismatch accepted:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "manifest_fingerprint_mismatch") {
		t.Fatalf("missing hash failure:\n%s", output.String())
	}

	output.Reset()
	runner = &probe{reporter: newReporter(&output), manifests: []string{filepath.Join(t.TempDir(), "missing.json")}}
	if runner.catalogIdentityCheck() {
		t.Fatalf("missing manifest accepted:\n%s", output.String())
	}
}
