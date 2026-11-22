package main

import (
	"crypto/sha256"
	"encoding/hex"
	jsonlegacy "encoding/json"
	"encoding/json/v2"
	"os"
	"strconv"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpsource "github.com/malyjacob/mobile-use-vnext/internal/mcp"
)

// checkLiveCatalog proves that the live tools/list is the same catalog the
// source tree publishes. The MCP SDK is free to return the tools in a
// different order, so the check compares a name-keyed mapping: the name set
// must be exactly the 13 frozen tools (and never apps/open_app), then every
// description and input schema is compared by meaning. The live schema has
// already been re-serialized by the SDK, so comparing its JSON bytes with the
// source fingerprint would be meaningless; only the source snapshot and
// fingerprint are compared byte-for-byte after the semantic equality gate.
func checkLiveCatalog(tools []*sdk.Tool) *checkError {
	// The merged-away names must fail even if the total count happens to match.
	for _, tool := range tools {
		if containsString(legacyToolNames, tool.Name) {
			return failf("legacy_tool_present", field("tool", tool.Name))
		}
	}
	if len(tools) != len(toolNames) {
		return failf("catalog_size_mismatch", field("count", strconv.Itoa(len(tools))))
	}
	live := make(map[string]*sdk.Tool, len(tools))
	for _, tool := range tools {
		if _, duplicate := live[tool.Name]; duplicate {
			return failf("catalog_name_duplicate", field("tool", tool.Name))
		}
		if !containsString(toolNames, tool.Name) {
			return failf("catalog_name_unexpected", field("tool", tool.Name))
		}
		live[tool.Name] = tool
	}
	for _, wanted := range toolNames {
		if live[wanted] == nil {
			return failf("catalog_name_missing", field("tool", wanted))
		}
	}

	snapshot := mcpsource.CatalogSnapshot()
	if mcpsource.ToolCount() != len(toolNames) {
		return failf("source_catalog_count_mismatch",
			field("count", strconv.Itoa(mcpsource.ToolCount())))
	}
	if len(snapshot) != len(toolNames) {
		return failf("source_catalog_size_mismatch",
			field("count", strconv.Itoa(len(snapshot))))
	}
	source := make(map[string]mcpsource.ToolSnapshot, len(snapshot))
	for _, tool := range snapshot {
		if _, duplicate := source[tool.Name]; duplicate {
			return failf("source_catalog_name_duplicate", field("tool", tool.Name))
		}
		source[tool.Name] = tool
	}
	for _, wanted := range toolNames {
		sourceTool, found := source[wanted]
		if !found {
			return failf("source_catalog_name_missing", field("tool", wanted))
		}
		tool := live[wanted]
		if containsHiddenResultTerm(tool.Description) {
			return failf("catalog_description_hidden_result_term", field("tool", wanted))
		}
		if tool.Description != sourceTool.Description {
			return failf("catalog_description_mismatch", field("tool", wanted))
		}
		liveBytes, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return failf("catalog_schema_unusable", field("tool", wanted))
		}
		if containsHiddenResultTerm(string(liveBytes)) {
			return failf("catalog_schema_hidden_result_term", field("tool", wanted))
		}
		liveSchema, liveErr := decodeExactJSON(liveBytes)
		sourceSchema, sourceErr := decodeExactJSON(sourceTool.Schema)
		if liveErr != nil || sourceErr != nil || !semanticEqual(liveSchema, sourceSchema) {
			return failf("catalog_schema_mismatch", field("tool", wanted))
		}
		readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
		if readOnly != sourceTool.ReadOnlyHint {
			return failf("read_only_annotation_mismatch", field("tool", wanted))
		}
	}
	return checkActScrollSchema(live["act"])
}

func containsHiddenResultTerm(description string) bool {
	folded := strings.ToLower(description)
	for _, term := range []string{"quality", "effect", "观察质量", "滚动效果"} {
		if strings.Contains(folded, term) {
			return true
		}
	}
	return false
}

// actSchemaProperty is the probe's explicit view of one act operation member.
// Only the members this check asserts are declared; everything else is ignored
// after the semantic source comparison has already proven the whole schema.
type actSchemaProperty struct {
	Enum       []string                     `json:"enum"`
	Minimum    *int                         `json:"minimum"`
	Maximum    *int                         `json:"maximum"`
	Properties map[string]actSchemaProperty `json:"properties"`
}

// actToolSchema is the top-level property view of the act input schema.
type actToolSchema struct {
	Properties map[string]actSchemaProperty `json:"properties"`
}

// checkActScrollSchema asserts the live act tool declares the contract-10
// scroll surface: operation.type includes scroll, all four scroll members are
// published, direction is exactly up/down/left/right and duration_ms is bounded
// by 50/2000. The whole-schema semantic equality and the 13-tool count are
// proven by the caller before this focused check runs.
func checkActScrollSchema(tool *sdk.Tool) *checkError {
	if tool == nil {
		return failf("act_tool_missing")
	}
	encoded, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return failf("act_schema_unusable")
	}
	var schema actToolSchema
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return failf("act_schema_unusable")
	}
	operation, declared := schema.Properties["operation"]
	if !declared {
		return failf("act_operation_missing")
	}
	actionType, declared := operation.Properties["type"]
	if !declared {
		return failf("act_type_missing")
	}
	if !containsString(actionType.Enum, "scroll") {
		return failf("act_type_scroll_missing")
	}
	for _, member := range []string{"container", "direction", "amount", "duration_ms"} {
		if _, present := operation.Properties[member]; !present {
			return failf("act_scroll_member_missing", field("member", member))
		}
	}
	if !sameStringSet(operation.Properties["direction"].Enum,
		[]string{"up", "down", "left", "right"}) {
		return failf("act_direction_enum_mismatch")
	}
	duration := operation.Properties["duration_ms"]
	if duration.Minimum == nil || duration.Maximum == nil ||
		*duration.Minimum != 50 || *duration.Maximum != 2000 {
		return failf("act_duration_bounds_mismatch",
			field("minimum", integerString(duration.Minimum)),
			field("maximum", integerString(duration.Maximum)))
	}
	return nil
}

// catalogProjection mirrors the source catalog fingerprint document: tool count
// plus ordered names, descriptions and schemas, in that member order. It is
// reproduced here so the probe can prove that CatalogSnapshot and
// CatalogFingerprint describe the same source catalog instead of trusting a
// count alone.
type catalogProjection struct {
	Count int                     `json:"count"`
	Tools []catalogToolProjection `json:"tools"`
}

type catalogToolProjection struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Schema      jsonlegacy.RawMessage `json:"schema"`
}

// expectedCatalogFingerprint recomputes the source fingerprint from the pure
// source snapshot. It uses the v1 encoder because that is the fixed algorithm
// in internal/mcp; the probe does not invent a second fingerprint scheme.
func expectedCatalogFingerprint() (string, error) {
	snapshot := mcpsource.CatalogSnapshot()
	projection := catalogProjection{
		Count: len(snapshot),
		Tools: make([]catalogToolProjection, 0, len(snapshot)),
	}
	for _, tool := range snapshot {
		projection.Tools = append(projection.Tools, catalogToolProjection{
			Name:        tool.Name,
			Description: tool.Description,
			Schema:      jsonlegacy.RawMessage(tool.Schema),
		})
	}
	canonical, err := jsonlegacy.Marshal(projection)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func isLowerHex64(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		switch {
		case character >= '0' && character <= '9',
			character >= 'a' && character <= 'f':
		default:
			return false
		}
	}
	return true
}

// catalogManifest is the shared fragment of both release manifests.
type catalogManifest struct {
	ToolCatalog struct {
		Count  int    `json:"count"`
		SHA256 string `json:"sha256"`
	} `json:"tool_catalog"`
}

func readCatalogManifest(path string) (catalogManifest, error) {
	var manifest catalogManifest
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	if err := jsonlegacy.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

// catalogIdentityCheck verifies the source fingerprint and, when the operator
// supplies manifests, that every manifest publishes the same count and hash. A
// live catalog that only passes a count check cannot reach this point.
func (p *probe) catalogIdentityCheck() bool {
	fingerprint := mcpsource.CatalogFingerprint()
	if !isLowerHex64(fingerprint) {
		p.reporter.fail("catalog_fingerprint", "fingerprint_shape_invalid")
		return false
	}
	recomputed, err := expectedCatalogFingerprint()
	if err != nil {
		p.reporter.fail("catalog_fingerprint", "fingerprint_recompute_failed")
		return false
	}
	if recomputed != fingerprint {
		p.reporter.fail("catalog_fingerprint", "fingerprint_snapshot_mismatch")
		return false
	}
	for _, path := range p.manifests {
		manifest, err := readCatalogManifest(path)
		if err != nil {
			p.reporter.fail("catalog_manifest", "manifest_unreadable", field("file", path))
			return false
		}
		if manifest.ToolCatalog.Count != len(toolNames) {
			p.reporter.fail("catalog_manifest", "manifest_count_mismatch",
				field("file", path), field("count", strconv.Itoa(manifest.ToolCatalog.Count)))
			return false
		}
		if manifest.ToolCatalog.SHA256 != fingerprint {
			p.reporter.fail("catalog_manifest", "manifest_fingerprint_mismatch", field("file", path))
			return false
		}
		p.reporter.pass("catalog_manifest",
			field("file", path), field("count", strconv.Itoa(manifest.ToolCatalog.Count)),
			"sha256=match")
	}
	p.reporter.pass("catalog_fingerprint",
		field("sha256", fingerprint), field("tools", strconv.Itoa(len(toolNames))))
	return true
}
