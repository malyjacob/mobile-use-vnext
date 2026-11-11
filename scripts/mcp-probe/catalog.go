package main

import (
	"crypto/sha256"
	"encoding/hex"
	jsonlegacy "encoding/json"
	"encoding/json/v2"
	"os"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpsource "github.com/malyjacob/mobile-use-vnext/internal/mcp"
)

// checkLiveCatalog proves that the live tools/list is the same catalog the
// source tree publishes. The MCP SDK is free to return the tools in a
// different order, so the check compares a name-keyed mapping: the name set
// must be exactly the 11 frozen tools (and never apps/open_app), then every
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
		if tool.Description != sourceTool.Description {
			return failf("catalog_description_mismatch", field("tool", wanted))
		}
		liveBytes, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return failf("catalog_schema_unusable", field("tool", wanted))
		}
		liveSchema, liveErr := decodeExactJSON(liveBytes)
		sourceSchema, sourceErr := decodeExactJSON(sourceTool.Schema)
		if liveErr != nil || sourceErr != nil || !semanticEqual(liveSchema, sourceSchema) {
			return failf("catalog_schema_mismatch", field("tool", wanted))
		}
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
