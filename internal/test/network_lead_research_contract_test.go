package app

import (
	"strings"
	"testing"
)

func TestValidateLeadResearchStageOutput(t *testing.T) {
	exec := &PipelineExecution{PipelineName: "lead-research"}
	tests := []struct {
		name   string
		output string
	}{
		{
			name: "产品画像与SWOT",
			output: `产品维度表
证据账本
SWOT
优势 / 劣势 / 机会 / 威胁
竞品与替代方案
负面 ICP
实体分类
PRODUCT_PROFILE_STATUS: READY`,
		},
		{
			name: "搜索策略",
			output: `搜索假设
关键词矩阵
负面关键词
多源渠道计划
接纳与淘汰规则
证据有效性标准
SEARCH_PLAN_STATUS: READY`,
		},
		{
			name: "多源搜索与核验",
			output: `检索执行记录
实体分类表
已核验潜客表
排除对象表
证据有效性审计
RESEARCH_STATUS: DEGRADED`,
		},
		{
			name: "报告整理",
			output: `产品结论与 SWOT
检索关键词
已核验潜客优先级表
排除对象表
证据有效性审计
下一轮检索计划`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateLeadResearchStageOutput(exec, &PipelineStep{Name: tt.name}, tt.output); err != nil {
				t.Fatalf("valid output rejected: %v", err)
			}
		})
	}
}

func TestValidateLeadResearchStageOutputRejectsSkippedPlanning(t *testing.T) {
	exec := &PipelineExecution{PipelineName: "lead-research"}
	err := validateLeadResearchStageOutput(exec, &PipelineStep{Name: "搜索策略"}, "直接开始搜索上海 AI 公司")
	if err == nil {
		t.Fatal("expected missing search plan contract to fail")
	}
	for _, want := range []string{"搜索策略", "SEARCH_PLAN_STATUS: READY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

func TestValidateLeadResearchStageOutputRejectsIncompleteSWOT(t *testing.T) {
	exec := &PipelineExecution{PipelineName: "lead-research"}
	err := validateLeadResearchStageOutput(exec, &PipelineStep{Name: "产品画像与SWOT"}, `产品画像
优势
机会
PRODUCT_PROFILE_STATUS: READY`)
	if err == nil {
		t.Fatal("expected incomplete SWOT to fail")
	}
	for _, want := range []string{"劣势", "威胁", "证据账本", "负面ICP"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

func TestValidateLeadResearchStageOutputRejectsRuntimeEvidence(t *testing.T) {
	exec := &PipelineExecution{PipelineName: "lead-research"}
	output := `产品维度表
证据账本
SWOT
优势 / 劣势 / 机会 / 威胁
竞品与替代方案
负面 ICP
实体分类
证明材料：目录包含 openKitty.exe 和 find_customer.db
PRODUCT_PROFILE_STATUS: READY`
	err := validateLeadResearchStageOutput(exec, &PipelineStep{Name: "产品画像与SWOT"}, output)
	if err == nil || !strings.Contains(err.Error(), "inadmissible runtime evidence") {
		t.Fatalf("runtime evidence was not rejected: %v", err)
	}
}

func TestValidateLeadResearchStageOutputAllowsProductClarificationStop(t *testing.T) {
	exec := &PipelineExecution{PipelineName: "lead-research"}
	output := `已确认内容：用户指定目标产品为 OpenKitty。
冲突/疑问：目录只出现 openKitty.exe 和另一个产品，无法确定哪套能力属于 OpenKitty；这些文件未作为产品证据。
请补充：OpenKitty 对应的项目目录或产品说明文件。

INPUT_STATUS: MISSING
<!--PIPELINE_STOP-->`
	if err := validateLeadResearchStageOutput(exec, &PipelineStep{Name: "产品画像与SWOT"}, output); err != nil {
		t.Fatalf("valid product clarification stop rejected: %v", err)
	}
}

func TestValidateLeadResearchStageOutputAllowsExplicitRuntimeExclusion(t *testing.T) {
	exec := &PipelineExecution{PipelineName: "lead-research"}
	output := `产品维度表
证据账本
SWOT
优势 / 劣势 / 机会 / 威胁
竞品与替代方案
负面 ICP
实体分类
排除说明：openKitty.exe 和 find_customer.db 未作为产品证据。
PRODUCT_PROFILE_STATUS: READY`
	if err := validateLeadResearchStageOutput(exec, &PipelineStep{Name: "产品画像与SWOT"}, output); err != nil {
		t.Fatalf("explicit runtime evidence exclusion rejected: %v", err)
	}
}

func TestValidateLeadResearchStageOutputRejectsUnverifiedBrandSubstitution(t *testing.T) {
	exec := &PipelineExecution{
		PipelineName: "lead-research",
		RuntimePipeline: &Pipeline{
			AIEmployeeOriginalTask: "请为 OpenKitty 寻找上海潜在客户；OpenKitty 是可私有部署的 AgentOS。",
		},
		Steps: []StepResult{{
			Name: "产品画像与SWOT",
			ToolCalls: []ToolCallRecord{{
				ToolName: "web_search",
				Status:   "success",
				Result:   "搜索摘要提到了 Nuwax Agent OS，但没有明确绑定 OpenKitty。",
			}},
		}},
	}
	output := `产品维度表
证据账本
OpenKitty = Nuwax Agent OS 的开源/社区品牌面。
SWOT
优势 / 劣势 / 机会 / 威胁
竞品与替代方案
负面 ICP
实体分类
PRODUCT_PROFILE_STATUS: READY`
	err := validateLeadResearchStageOutput(exec, &PipelineStep{Name: "产品画像与SWOT"}, output)
	if err == nil || !strings.Contains(err.Error(), "unverified product identity") {
		t.Fatalf("unverified brand substitution was accepted: %v", err)
	}
}

func TestValidateLeadResearchStageOutputAllowsDirectFirstPartyIdentityEvidence(t *testing.T) {
	exec := &PipelineExecution{
		PipelineName: "lead-research",
		RuntimePipeline: &Pipeline{
			AIEmployeeOriginalTask: "请为 OpenKitty 寻找上海潜在客户。",
		},
		Steps: []StepResult{{
			Name: "产品画像与SWOT",
			ToolCalls: []ToolCallRecord{{
				ToolName: "web_fetch",
				Status:   "success",
				Result:   "官方产品页：OpenKitty 是 Nuwax 的开源品牌面。",
			}},
		}},
	}
	output := `产品维度表
证据账本
OpenKitty 是 Nuwax 的开源品牌面。
SWOT
优势 / 劣势 / 机会 / 威胁
竞品与替代方案
负面 ICP
实体分类
PRODUCT_PROFILE_STATUS: READY`
	if err := validateLeadResearchStageOutput(exec, &PipelineStep{Name: "产品画像与SWOT"}, output); err != nil {
		t.Fatalf("direct first-party identity evidence was rejected: %v", err)
	}
}
