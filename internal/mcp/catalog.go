package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ToolCount returns the number of tools in the fixed public catalog.
func ToolCount() int {
	return len(toolCatalog)
}

// catalogProjection is the canonical JSON document hashed by
// CatalogFingerprint. Field order and toolCatalog order define the bytes, and
// encoding/json compacts each embedded schema document so source formatting
// whitespace never changes the fingerprint.
type catalogProjection struct {
	Count int                     `json:"count"`
	Tools []catalogToolProjection `json:"tools"`
}

type catalogToolProjection struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// CatalogFingerprint returns the lowercase hex SHA-256 of the canonical JSON
// projection of the fixed tool catalog: tool count plus ordered names,
// descriptions and input schemas. It is deterministic for one source tree and
// is recorded in release manifests so a running adapter can be compared with
// the build that produced it.
func CatalogFingerprint() string {
	projection := catalogProjection{
		Count: len(toolCatalog),
		Tools: make([]catalogToolProjection, 0, len(toolCatalog)),
	}
	for _, definition := range toolCatalog {
		projection.Tools = append(projection.Tools, catalogToolProjection{
			Name:        definition.name,
			Description: definition.description,
			Schema:      definition.schema,
		})
	}
	canonical, err := json.Marshal(projection)
	if err != nil {
		// Every schema is a compile-time constant, so a marshal failure means
		// the catalog itself is invalid and no fingerprint may be published.
		panic("mcp: tool catalog fingerprint: " + err.Error())
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}
