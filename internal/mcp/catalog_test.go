package mcp

import (
	"encoding/hex"
	"testing"
)

func TestCatalogFingerprint(t *testing.T) {
	if count := ToolCount(); count != 12 {
		t.Fatalf("ToolCount() = %d, want 12", count)
	}
	fingerprint := CatalogFingerprint()
	if len(fingerprint) != 64 {
		t.Fatalf("fingerprint length = %d, want 64", len(fingerprint))
	}
	if _, err := hex.DecodeString(fingerprint); err != nil {
		t.Fatalf("fingerprint is not hex: %v", err)
	}
	if again := CatalogFingerprint(); again != fingerprint {
		t.Fatalf("fingerprint is not deterministic: %s != %s", again, fingerprint)
	}
}
