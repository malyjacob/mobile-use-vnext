package mcp

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestCatalogFingerprint(t *testing.T) {
	if count := ToolCount(); count != 13 {
		t.Fatalf("ToolCount() = %d, want 13", count)
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

// TestCatalogSnapshotCarriesResultV4Discovery pins the published discovery
// surface to the result v4 envelope and the compact obs/app/recovery lines, and
// proves the retired result v3 envelope and observation.screen.display_id path
// are gone from both the instructions and every tool description.
func TestCatalogSnapshotCarriesResultV4Discovery(t *testing.T) {
	snapshot := CatalogSnapshot()
	if len(snapshot) != ToolCount() || len(snapshot) != 13 {
		t.Fatalf("catalog snapshot covers %d tools, want %d", len(snapshot), ToolCount())
	}

	instructions := ServerInstructions()
	for _, required := range []string{
		"`result v4 <ok|err> [op=…] [state=…] [outcome=…] [phase=…]`",
		"`obs [<O>] [status=…] [fg=…] [at=HH:MM:SS] [screen=<W>x<H>[@<display_id>]] [rot=<1|2|3>] [missing=…]`",
		"`app_items v1 kind=find|list count=<n>`",
		"`app_tasks v1 count=<n>`",
		"`recovery entry=<entry> ref=<精确 O/P 编号|O|P|none> [input=…] [cause=…]`",
		"`structured` 保留完整审计字段",
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("server instructions lack %q", required)
		}
	}
	for _, retired := range []string{
		"result v3",
		"inspection.origin",
		"observation.screen.display_id",
		"observation.actionable_until",
		"连续 `observation.*` 字段",
	} {
		if strings.Contains(instructions, retired) {
			t.Errorf("server instructions retain retired wording %q", retired)
		}
	}

	for _, tool := range snapshot {
		if tool.Description == "" {
			t.Errorf("%s: empty catalog description", tool.Name)
		}
		if len(tool.Schema) == 0 {
			t.Errorf("%s: empty catalog schema", tool.Name)
		}
		for _, retired := range []string{"result v3", "observation.screen.display_id", "actionable_until"} {
			if strings.Contains(tool.Description, retired) {
				t.Errorf("%s: description retains retired wording %q", tool.Name, retired)
			}
		}
	}
}
