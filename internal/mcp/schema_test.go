package mcp

import (
	"strings"
	"testing"
)

// TestActDescriptionDescribesDynamicRevalidation pins the public act guidance
// to the S5 semantics: static planning is followed by a fresh-tree proof, safe
// reselection is bounded to the same target scope, scroll trajectories may be
// re-planned inside the same container, and text focus binding is layered.
// The checks are semantic fragments, not a frozen prose snapshot.
func TestActDescriptionDescribesDynamicRevalidation(t *testing.T) {
	definition := testDefinitionByName(t, "act")
	description := definition.description

	for _, required := range []string{
		"default_center",
		"carrier_node",
		"center_span",
		"认领后",
		"当前树",
		"原静态点仍可证明时保留",
		"原 carrier 仍匹配且安全时沿用并在其内重算",
		"目标双向唯一且有独立强内容身份",
		"同一 target scope 内安全解绑并重选 carrier/点",
		"不向上寻找可点击祖先",
		"不穿过 blocker",
		"重算后的点",
		"422 unsupported_node_action",
		"occluding_ref",
		"nearest_actionable_ref",
		"safe_point",
		"safe_carrier_ref",
		"当前容器相交",
		"轴向连续",
		"离轴边缘稳定",
		"当前安全区间与中心带",
		"重新规划整条可证轨迹",
		"新起点/终点",
		"text 的 ref 目标与 tap 一样先在当前树确认目标并重算焦点 tap 点",
		"exact → near → shifted",
		"同 Display、同前台 task 上下文",
		"完整来源字段与 presence",
		"稳定 anchor",
		"覆盖旧点击点且与旧矩形足够交叠",
		"无 anchor 时只按双向唯一的旧点击点/矩形交叠绑定",
		"shifted 只放宽纵向位置",
		"全屏唯一同类型获焦",
		"fresh bounds",
		"target_changed",
		"用该 O 加新 P 重试",
		"uncertain",
		"不重开已结束的 tap",
		"observation.quality",
	} {
		if !strings.Contains(description, required) {
			t.Errorf("act description lacks %q", required)
		}
	}

	for _, stale := range []string{
		"认领后只重验该固定点",
		"不重新规划",
		"固定轨迹",
	} {
		if strings.Contains(description, stale) {
			t.Errorf("act description retains stale wording %q", stale)
		}
	}
}

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
