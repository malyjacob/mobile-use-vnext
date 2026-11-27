package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
)

// TestDiscoverySchemaStructureBaseline freezes the pre-slimming catalog after
// stripping only Schema descriptions. Array order and read-only hints are part
// of the digest, as are defaults and every other Schema keyword.
func TestDiscoverySchemaStructureBaseline(t *testing.T) {
	type toolStructure struct {
		Name         string
		Schema       map[string]any
		ReadOnlyHint bool
	}
	snapshot := CatalogSnapshot()
	structure := make([]toolStructure, 0, len(snapshot))
	for _, tool := range snapshot {
		var schema map[string]any
		if err := json.Unmarshal(tool.Schema, &schema); err != nil {
			t.Fatalf("%s: decode schema: %v", tool.Name, err)
		}
		if _, exists := schema["description"]; exists {
			t.Errorf("%s: root schema repeats the tool description", tool.Name)
		}
		stripDiscoverySchemaDescriptions(schema)
		structure = append(structure, toolStructure{tool.Name, schema, tool.ReadOnlyHint})
	}
	wire, err := json.Marshal(structure)
	if err != nil {
		t.Fatal(err)
	}
	const baseline = "e0e0cdbbf44c159a31f6b200ee83a98c40b1d101d6d3a40fb5a043baf3265a37"
	if got := fmt.Sprintf("%x", sha256.Sum256(wire)); got != baseline {
		t.Fatalf("catalog structure digest = %s, want %s", got, baseline)
	}
}

func stripDiscoverySchemaDescriptions(schema map[string]any) {
	delete(schema, "description")
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, child := range properties {
			if subschema, ok := child.(map[string]any); ok {
				stripDiscoverySchemaDescriptions(subschema)
			}
		}
	}
	if branches, ok := schema["oneOf"].([]any); ok {
		for _, branch := range branches {
			if subschema, ok := branch.(map[string]any); ok {
				stripDiscoverySchemaDescriptions(subschema)
			}
		}
	}
	if excluded, ok := schema["not"].(map[string]any); ok {
		stripDiscoverySchemaDescriptions(excluded)
	}
}
