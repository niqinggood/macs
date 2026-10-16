package app

import (
	"testing"

	"mac/internal/security"
)

var classifyFinancialChartCode = security.ClassifyFinancialChartCode
var filterUnsafeFinancialChartCalls = security.FilterUnsafeFinancialChartCalls

func TestFinancialChartGuardRejectsSyntheticMarketSeriesAcrossExecutionTools(t *testing.T) {
	code := `
import numpy as np
import matplotlib.pyplot as plt
# synthesize a believable trajectory consistent with observed points
np.random.seed(42)
close = 12.2 + np.random.normal(0, 0.25, 120).cumsum()
plt.title("600596 收盘价与 MA20")
plt.savefig(".openkitty/storage/charts/price.png")`
	for _, name := range []string{"ptc_execute", "cli", "persistent_shell"} {
		args := map[string]interface{}{"script": code}
		if name != "ptc_execute" {
			args = map[string]interface{}{"command": code}
		}
		kept, rejected := filterUnsafeFinancialChartCalls("做得精美一些", []ToolCall{{Name: name, Arguments: args}})
		if len(kept) != 0 || len(rejected) != 1 {
			t.Fatalf("%s guard result kept=%d rejected=%v", name, len(kept), rejected)
		}
	}
}

func TestFinancialChartGuardAllowsNonFinancialRandomPlot(t *testing.T) {
	code := `import numpy as np; import matplotlib.pyplot as plt; plt.scatter(np.random.normal(size=20), np.random.normal(size=20)); plt.savefig("clusters.png")`
	decision := classifyFinancialChartCode(code, "画一张聚类诊断图")
	if decision.FinanceLike || decision.Reason != "" {
		t.Fatalf("non-financial plot was blocked: %#v", decision)
	}
}

func TestFinancialChartGuardAllowsExactAggregateBars(t *testing.T) {
	code := `returns = [-5.34, -11.96, 3.79]; plt.bar(["5日","20日","60日"], returns); plt.title("真实区间涨跌幅"); plt.savefig("returns.png")`
	decision := classifyFinancialChartCode(code, "用现有真实数据制作图表")
	if decision.Synthetic || decision.Reason != "" {
		t.Fatalf("exact aggregate chart was blocked: %#v", decision)
	}
}

func TestFinancialChartGuardScenarioRequiresUserRequestAndVisibleLabel(t *testing.T) {
	base := `import numpy as np; import matplotlib.pyplot as plt; prices=np.random.normal(size=100).cumsum(); plt.savefig("stock.png")`
	if got := classifyFinancialChartCode(base, "做压力测试"); got.ScenarioAllowed || got.Reason == "" {
		t.Fatalf("unlabelled scenario should be blocked: %#v", got)
	}
	labeled := base + `; plt.title("情景模拟·非真实行情")`
	if got := classifyFinancialChartCode(labeled, "请做压力测试情景模拟"); !got.ScenarioAllowed || got.Reason != "" {
		t.Fatalf("explicit labelled scenario should pass: %#v", got)
	}
}

func TestFinancialChartGuardRejectsUnsupportedRiskScores(t *testing.T) {
	code := `risks=["估值","周期"]; scores=[4,3]; plt.barh(risks,scores); plt.title("股票风险评分"); plt.savefig("risk.png")`
	got := classifyFinancialChartCode(code, "做得精美一些")
	if !got.Synthetic || got.Reason == "" {
		t.Fatalf("unsupported numeric risk score should be blocked: %#v", got)
	}
}
