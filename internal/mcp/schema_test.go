package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/malyjacob/mobile-use-vnext/internal/protocol"
)

// compatibilityProtocolVersion is the newest handshake-era protocol version.
// A client pinned to it takes the legacy initialize path instead of
// server/discover, so the same server instructions must appear there too.
const compatibilityProtocolVersion = "2025-11-25"

// baselineDiscoveryBytes is the pre-#16 discovery payload measured on the
// baseline commit: instructions (0) plus the complete tools/list JSON. The
// deduplicated instructions and descriptions must stay strictly below it.
const baselineDiscoveryBytes = 36250

// TestTreeTextGuideStatesTriStateOmission pins the shared tree legend to the
// complete-node defaults and the different set semantics.
func TestTreeTextGuideStatesTriStateOmission(t *testing.T) {
	for _, required := range []string{
		"enabled=true` 省略",
		"enabled=false`/`enabled=null` 显式",
		"focused`/`selected`/`protected`/`checked` 的已知 `false` 省略",
		"`set` 行不同",
		"出现的状态字段一律保留显式 `false`/`true`/`null`",
	} {
		if !strings.Contains(treeTextGuide, required) {
			t.Errorf("tree guide lacks %q", required)
		}
	}
	if strings.Contains(treeTextGuide, "defaults {\"checked\":false}") {
		t.Error("tree guide retains the removed defaults declaration")
	}
}

// TestTreeTextGuideStatesCompactGeometryPolicy pins the observation-view
// geometry legend: action positions stay, actionless text and anonymous plain
// rectangles are hidden, and the remaining rectangles hide only when they
// repeat the nearest delivered ancestor or fall below one percent of the
// screen.
func TestTreeTextGuideStatesCompactGeometryPolicy(t *testing.T) {
	for _, required := range []string{
		"根和有动作的节点交付已知矩形",
		"无动作的 `text` 与匿名普通 view/group 省略矩形",
		"与父链上最近交付的矩形四边相同",
		"裁剪到屏幕后小于屏幕面积 1% 时省略",
		"详细补看交付原矩形",
	} {
		if !strings.Contains(treeTextGuide, required) {
			t.Errorf("tree guide lacks %q", required)
		}
	}
}

func TestTreeTextGuideStatesV3RoleRecovery(t *testing.T) {
	for _, required := range []string{
		"tree v3 format=full",
		"tree v3 format=delta",
		"inspect v3 scope=",
		"省略 `role`",
		"隐藏几何后直接是引号标签时默认 `text`",
		"或行尾默认 `view`",
		"显式角色覆盖默认",
		"`view` 必须显式写 `view`",
		"无标签或带几何列的 `text` 必须显式写 `text`",
		"`[n2 n3]`",
		"`chain [n2 n3] <role> ...`",
	} {
		if !strings.Contains(treeTextGuide, required) {
			t.Errorf("tree guide lacks %q", required)
		}
	}
	for _, retired := range []string{"tree v2", "inspect v2"} {
		if strings.Contains(treeTextGuide, retired) {
			t.Errorf("tree guide retains retired version %q", retired)
		}
	}
}

// TestServerInstructionsCarryCrossToolRules checks the shared workflow and the
// single complete tree legend.
func TestServerInstructionsCarryCrossToolRules(t *testing.T) {
	instructions := ServerInstructions()
	if instructions == "" {
		t.Fatal("ServerInstructions is empty")
	}
	if got := strings.Count(instructions, treeTextGuide); got != 1 {
		t.Fatalf("tree guide occurrences = %d, want 1", got)
	}
	for _, required := range []string{
		"start({})",
		"can_start=false",
		"recovery.entry=observe",
		"observation.actionable_until",
		"幂等",
		"五分钟",
		"两分钟",
		"不续租",
		"keepalive",
		"wait.trigger",
		"wait.matched",
		"observation.quality=unstable/unknown",
		"写入全文",
		"app.list",
		"diagnose",
		"notify 无 reference",
		"同一 base 的每条 delta",
		"不发送 ENTER",
		"press_enter",
		"text_input",
		"bubble",
		"统一用 reference",
		"not_executed",
		"uncertain",
		"executed",
		"blocking_operation_id",
		"start_operation_id",
		"target_reference",
		"device_busy",
		"pending cleanup",
		"no_safe_point",
		"最近交互祖先一次",
		"session.origin",
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("server instructions lack %q", required)
		}
	}
}

// TestServerInstructionsKeepContractBoundary proves the instructions publish
// the reference unification and the branch-qualified defaults, while never
// naming the rejected alternative (allow_ancestor_fallback) or claiming a
// top-level default that would apply to coordinates or other actions.
func TestServerInstructionsKeepContractBoundary(t *testing.T) {
	instructions := ServerInstructions()
	for _, forbidden := range []string{
		"allow_ancestor_fallback",
	} {
		if strings.Contains(instructions, forbidden) {
			t.Errorf("server instructions publish forbidden wording %q", forbidden)
		}
	}
	if !strings.Contains(instructions, "observation.id") {
		t.Error("server instructions do not identify the observation id to reuse")
	}
	for _, required := range []string{
		"请求引用统一用 reference",
		"默认 bubble=true",
		"默认 press_enter=true",
		"operation.bubble=false",
		"operation.press_enter=false",
	} {
		if !strings.Contains(instructions, required) {
			t.Errorf("server instructions lack %q", required)
		}
	}
}

// TestToolDescriptionsExcludeTreeGuide proves the complete legend is published
// once, in the instructions, and never repeated in a tool description.
func TestToolDescriptionsExcludeTreeGuide(t *testing.T) {
	for _, definition := range toolCatalog {
		if definition.description == "" {
			t.Fatalf("%s: empty description", definition.name)
		}
		if strings.Contains(definition.description, treeTextGuide) {
			t.Errorf("%s: description repeats the tree guide", definition.name)
		}
	}
}

// TestToolDescriptionsAreCallerOriented asserts every description states its
// own purpose, key parameters/defaults, result or action boundary and minimal
// recovery, and that internal algorithms and the rejected ancestor-fallback
// name stay out.
func TestToolDescriptionsAreCallerOriented(t *testing.T) {
	required := map[string][]string{
		"start": {
			"请求体必须是空对象",
			"session.expires_at",
			"重放原 start 不重新获得五分钟",
			"可消费观察",
			"幂等/查询编号",
			"base full 加本次 delta",
		},
		"status": {
			"operation_state",
			"can_start",
			"blocking_operation_id",
			"不续租",
		},
		"snap": {
			"不读取节点树",
			"ImageContent",
			"不续租",
			"无图摘要",
		},
		"observe": {
			"reference 必填",
			"0–10000",
			"新 observation.id",
		},
		"wait_for": {
			"1..40",
			"stable=true",
			"连续 3 帧",
			"wait.trigger",
			"wait.matched",
			"35 秒",
		},
		"act": {
			"reference",
			"press_enter",
			"text_input",
			"bubble",
			"另发 ENTER",
			"invalid_target",
			"unsupported_node_action",
			"no_safe_point",
			"reason",
			"nearest_actionable_ref",
			"safe_point",
			"target_changed",
			"新 observation.id",
			"uncertain",
			"recovery.entry=observe",
			"operation.target={x,y}",
			"独立通过准入和重验",
		},
		"inspect": {
			"node 与 query 互斥",
			"content_unavailable",
			"inspection_too_large",
			"inspect v3",
		},
		"keepalive": {
			"1～120",
			"默认 2",
			"缩短",
			"session_ended",
			"reference_has_no_session",
			"session_expired",
			"status",
		},
		"end": {
			"cleanup",
			"pending",
			"不保证撤回",
			"recovery",
		},
		"app": {
			"find 无需会话",
			"scope=primary",
			"当前 ready O",
			"live owned",
			"outcome",
		},
		"notify": {
			"executed 只表示消息已保存",
			"notification.state",
			"无需活动会话",
			"无 reference 参数",
		},
		"shell": {
			"root=true",
			"exit_code",
			"observe",
			"status(P)",
			"finished",
			"blocking_operation_id",
			"blocking_code",
			"shell_pending",
		},
		"diagnose": {
			"coverage=record_unavailable",
			"status",
		},
	}
	if len(required) != len(toolCatalog) {
		t.Fatalf("description expectations cover %d tools, want %d", len(required), len(toolCatalog))
	}
	for _, definition := range toolCatalog {
		fragments, declared := required[definition.name]
		if !declared {
			t.Fatalf("%s: no description expectations", definition.name)
		}
		for _, fragment := range fragments {
			if !strings.Contains(definition.description, fragment) {
				t.Errorf("%s description lacks %q", definition.name, fragment)
			}
		}
		for _, forbidden := range []string{"allow_ancestor_fallback", "server instructions", "账本"} {
			if strings.Contains(definition.description, forbidden) {
				t.Errorf("%s description publishes A-phase-forbidden %q", definition.name, forbidden)
			}
		}
	}
}

// TestLiveToolDescriptionsClarifyWaitAndTextRecovery checks the descriptions
// and instructions actually returned by live discovery, not only the source
// catalog.
func TestLiveToolDescriptionsClarifyWaitAndTextRecovery(t *testing.T) {
	fake := newTestDeviceService(t)
	session := connectTestInMemory(t, newTestMCPServer(t, fake))
	ctx, cancel := testCallContext(t)
	defer cancel()

	catalog, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	descriptions := make(map[string]string, len(catalog.Tools))
	for _, tool := range catalog.Tools {
		descriptions[tool.Name] = tool.Description
	}

	const textRecoveryTrigger = "已认领 text 的整体 outcome=uncertain"
	required := map[string][]string{
		"observe": {
			"wait_ms",
			"毫秒",
			"等待公开树条件应选 wait_for",
		},
		"wait_for": {
			"轮询当前会话的公开树条件",
			"任一条件成立",
			"stable=true",
			"连续 3 帧",
			"发布新 observation.id 时调用成功",
			"wait.trigger=timeout",
			"wait.matched 包含终帧命中的条件",
			"超时时还包含 timeout",
			"最终未发布新 O",
		},
		"act": {
			textRecoveryTrigger,
			"仅已派发聚焦",
			"recovery.entry=observe",
			"recovery.reference=P",
			"直接 observe 核对当前框值/页面",
			"原活动会话的具体归属 P",
			"session.origin",
			"属于该会话的 operation_id",
			"避免原样重发",
		},
	}
	for name, fragments := range required {
		t.Run(name, func(t *testing.T) {
			description, ok := descriptions[name]
			if !ok {
				t.Fatalf("%s missing from live tools/list", name)
			}
			for _, fragment := range fragments {
				if !strings.Contains(description, fragment) {
					t.Errorf("live %s description lacks %q", name, fragment)
				}
			}
		})
	}

	initialize := session.InitializeResult()
	if initialize == nil {
		t.Fatal("initialize result missing")
	}
	for _, fragment := range []string{
		textRecoveryTrigger,
		"直接 observe 核对当前框值/页面",
		"recovery.entry=observe",
		"recovery.reference=P",
		"通常 uncertain 先用 status",
	} {
		if !strings.Contains(initialize.Instructions, fragment) {
			t.Errorf("live server instructions lack %q", fragment)
		}
	}
}

// TestActDescriptionKeepsCallerRecovery removes the internal landing and focus
// binding algorithm from the public text while keeping the actionable recovery
// contract and the explicit no-ENTER boundary.
func TestActDescriptionKeepsCallerRecovery(t *testing.T) {
	description := testDefinitionByName(t, "act").description
	for _, forbidden := range []string{
		"default_center",
		"carrier_node",
		"center_span",
		"exact → near",
		"shifted",
		"safe_carrier_ref",
		"occluding_ref",
		"Tier",
		"落点",
		"绑定",
		"同 Display",
	} {
		if strings.Contains(description, forbidden) {
			t.Errorf("act description retains internal algorithm wording %q", forbidden)
		}
	}
	for _, required := range []string{
		"全文写入",
		"另发 ENTER",
		"press_enter=false",
		"text_input",
		"bubble=true",
		"用新 O 和新 P 重试",
		"uncertain",
		"避免原样重发",
	} {
		if !strings.Contains(description, required) {
			t.Errorf("act description lacks caller recovery %q", required)
		}
	}
}

// TestKeepaliveDescriptionKeepsCallerContract removes the internal renewal
// comparison and history dedup wording while keeping the range, default,
// shortening behaviour and error recovery.
func TestKeepaliveDescriptionKeepsCallerContract(t *testing.T) {
	description := testDefinitionByName(t, "keepalive").description
	for _, forbidden := range []string{
		"max(现有截止时间",
		"历史去重",
		"同一可识别 P 重试",
		"P 记录仍有界保留",
		"账本",
	} {
		if strings.Contains(description, forbidden) {
			t.Errorf("keepalive description retains internal wording %q", forbidden)
		}
	}
	for _, required := range []string{
		"1～120",
		"默认 2",
		"缩短",
		"session_ended",
		"reference_has_no_session",
		"session_expired",
	} {
		if !strings.Contains(description, required) {
			t.Errorf("keepalive description lacks %q", required)
		}
	}
}

func TestLiveQuerySchemaMinimum(t *testing.T) {
	session := connectTestInMemory(t, newTestMCPServer(t, newTestDeviceService(t)))
	ctx, cancel := testCallContext(t)
	defer cancel()
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	expected := map[string][]string{"inspect": {"query"}, "wait_for": {"query", "absent_query"}}
	matched := 0
	for _, tool := range list.Tools {
		fields, ok := expected[tool.Name]
		if !ok {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				MinLength int `json:"minLength"`
				MaxLength int `json:"maxLength"`
			} `json:"properties"`
		}
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		for _, field := range fields {
			got := schema.Properties[field]
			if got.MinLength != 1 || got.MaxLength != protocol.MaxQueryRunes {
				t.Errorf("%s.%s: minLength=%d maxLength=%d", tool.Name, field, got.MinLength, got.MaxLength)
			}
			matched++
		}
	}
	if matched != 3 {
		t.Errorf("checked %d live query fields, want 3", matched)
	}
}

// TestDiscoveryByteBudget measures the live in-memory SDK discovery the same way
// the baseline was measured and proves the deduplicated text stays below it.
// Sub-items explain where the bytes went; they are not separate thresholds.
func TestDiscoveryByteBudget(t *testing.T) {
	fake := newTestDeviceService(t)
	session := connectTestInMemory(t, newTestMCPServer(t, fake))

	initialize := session.InitializeResult()
	if initialize == nil {
		t.Fatal("initialize result missing")
	}
	instructions := initialize.Instructions

	ctx, cancel := testCallContext(t)
	defer cancel()
	catalog, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	toolsJSON, err := json.Marshal(catalog.Tools)
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	descriptions := 0
	schemas := 0
	for _, tool := range catalog.Tools {
		descriptions += len(tool.Description)
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal schema: %v", tool.Name, err)
		}
		schemas += len(encoded)
		if tool.Name == "act" || tool.Name == "app" {
			t.Logf("%s discovery bytes: description=%d schema=%d", tool.Name, len(tool.Description), len(encoded))
		}
	}
	total := len(instructions) + len(toolsJSON)
	t.Logf("discovery bytes: instructions=%d tools_list=%d descriptions=%d schemas=%d total=%d baseline=%d",
		len(instructions), len(toolsJSON), descriptions, schemas, total, baselineDiscoveryBytes)
	if total >= baselineDiscoveryBytes {
		t.Fatalf("discovery total = %d, want < %d", total, baselineDiscoveryBytes)
	}

	if got := strings.Count(instructions, treeTextGuide); got != 1 {
		t.Fatalf("live instructions contain the tree guide %d times, want 1", got)
	}
	if len(catalog.Tools) != len(toolCatalog) {
		t.Fatalf("live tools = %d, want %d", len(catalog.Tools), len(toolCatalog))
	}
	// The SDK publishes tools sorted by name. Compare that deterministic order
	// with the source catalog names so a rename, add or removal fails here too.
	wantNames := make([]string, 0, len(toolCatalog))
	for _, definition := range toolCatalog {
		wantNames = append(wantNames, definition.name)
	}
	sort.Strings(wantNames)
	for index, tool := range catalog.Tools {
		if tool.Name != wantNames[index] {
			t.Fatalf("live tool %d = %q, want %q", index, tool.Name, wantNames[index])
		}
		source := testDefinitionByName(t, tool.Name)
		if tool.Description != source.description {
			t.Fatalf("%s: live description differs from the source catalog", tool.Name)
		}
		if tool.Description == "" {
			t.Fatalf("%s: empty live description", tool.Name)
		}
		if strings.Contains(tool.Description, treeTextGuide) {
			t.Fatalf("%s: live description repeats the tree guide", tool.Name)
		}
	}
}

// TestServerInstructionsDiscovery proves the live server/discover result carries
// the source instructions unchanged.
func TestServerInstructionsDiscovery(t *testing.T) {
	fake := newTestDeviceService(t)
	session := connectTestInMemory(t, newTestMCPServer(t, fake))
	result := session.InitializeResult()
	if result == nil {
		t.Fatal("initialize result missing")
	}
	if result.Instructions == "" {
		t.Fatal("server/discover instructions are empty")
	}
	if result.Instructions != ServerInstructions() {
		t.Fatal("live discovery instructions differ from the source accessor")
	}
}

// TestServerInstructionsCompatibilityInitialize proves the legacy initialize
// handshake returns the same instructions as server/discover.
func TestServerInstructionsCompatibilityInitialize(t *testing.T) {
	fake := newTestDeviceService(t)
	server := newTestMCPServer(t, fake)
	session := connectTestInMemoryWithOptions(t, server, &sdk.ClientSessionOptions{
		ProtocolVersion: compatibilityProtocolVersion,
	})
	result := session.InitializeResult()
	if result == nil {
		t.Fatal("initialize result missing")
	}
	if result.ProtocolVersion != compatibilityProtocolVersion {
		t.Fatalf("negotiated protocol = %q, want %q", result.ProtocolVersion, compatibilityProtocolVersion)
	}
	if result.Instructions == "" {
		t.Fatal("compatibility initialize instructions are empty")
	}
	if result.Instructions != ServerInstructions() {
		t.Fatal("compatibility initialize instructions differ from the source accessor")
	}
}

// connectTestInMemoryWithOptions is connectTestInMemory with explicit client
// session options, used to force the compatibility initialize handshake.
func connectTestInMemoryWithOptions(t *testing.T, server *sdk.Server, options *sdk.ClientSessionOptions) *sdk.ClientSession {
	t.Helper()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, serverTransport) }()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "1"}, nil).
		Connect(ctx, clientTransport, options)
	if err != nil {
		cancel()
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("server did not stop")
		}
	})
	return session
}
