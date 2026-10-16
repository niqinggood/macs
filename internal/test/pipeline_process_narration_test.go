package app

import (
	"strings"
	"testing"
)

func TestCleanPipelineUserOutputRemovesDeliveryProcessPreamble(t *testing.T) {
	output := `我已有足够的框架来写 BP v0。下面直接按要求在聊天中交付，未明确要求文件，故不写文件。

# 商业计划书 v0

这是面向用户的正文。`
	cleaned := cleanPipelineUserOutput(output)
	if strings.Contains(cleaned, "我已有足够") ||
		strings.Contains(cleaned, "下面直接") ||
		strings.Contains(cleaned, "不写文件") {
		t.Fatalf("delivery process preamble remained: %q", cleaned)
	}
	if !strings.Contains(cleaned, "# 商业计划书 v0") ||
		!strings.Contains(cleaned, "这是面向用户的正文") {
		t.Fatalf("user-facing content was removed: %q", cleaned)
	}
}

func TestCleanPipelineUserOutputPreservesNumberedInteractionOptions(t *testing.T) {
	output := `请选择处理方式：
1. 直接分析当前材料。
2. 读取你接下来上传的材料，我会先核对再形成结论。
3. 两者结合，先检索再与你的材料交叉验证。
- 也可以先确认范围，我将只处理选定部分。`
	cleaned := cleanPipelineUserOutput(output)
	for _, want := range []string{
		"1. 直接分析当前材料。",
		"2. 读取你接下来上传的材料，我会先核对再形成结论。",
		"3. 两者结合，先检索再与你的材料交叉验证。",
		"- 也可以先确认范围，我将只处理选定部分。",
	} {
		if !strings.Contains(cleaned, want) {
			t.Fatalf("numbered user-facing option %q was removed: %q", want, cleaned)
		}
	}
}
