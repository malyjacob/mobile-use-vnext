// Command toolcatalog prints the fixed MCP tool catalog fingerprint as one
// JSON object on stdout. Release manifests record this value and verification
// scripts recompute it, so this command must never print anything else.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	mcpserver "github.com/malyjacob/mobile-use-vnext/internal/mcp"
)

func main() {
	payload := struct {
		Count  int    `json:"count"`
		SHA256 string `json:"sha256"`
	}{
		Count:  mcpserver.ToolCount(),
		SHA256: mcpserver.CatalogFingerprint(),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, "toolcatalog: encode catalog fingerprint:", err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}
