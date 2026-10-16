package security

import (
	"encoding/json"
	"regexp"
	"strings"

	"mac/internal/toolcontract"
)

type FinancialChartDecision struct {
	ChartLike       bool
	FinanceLike     bool
	Synthetic       bool
	ScenarioAllowed bool
	Reason          string
}

var (
	financialChartCodePattern = regexp.MustCompile(`(?i)(?:\b(?:stock|equity|returns?|valuation|p/?e|p/?b|eps|ma(?:5|20|60)|ohlc|candlestick|share\s+price|closing\s+price)\b|股价|股票|收盘|行情|估值|市值|财务|涨跌|风险评分)`)
	chartOutputCodePattern    = regexp.MustCompile(`(?i)(?:savefig|matplotlib|pyplot|\.png\b|storage[\\/]charts|from\s+pptx|pptxgenjs)`)
	syntheticSeriesPattern    = regexp.MustCompile(`(?i)(?:np\.random|numpy\.random|random\.(?:seed|random|normal|uniform|gauss|randn)|default_rng|linspace|interp|cumsum|cumprod|believable\s+trajectory|plausible\s+trajectory|synthesi[sz]|synthetic|fabricat|reconstruct|伪造|合成行情|模拟股价)`)
	hardcodedRiskScorePattern = regexp.MustCompile(`(?i)(?:risk[_\s-]*scores?|风险评分|风险得分|scores?)\s*=\s*\[[\d\s,\.]+\]`)
	scenarioRequestPattern    = regexp.MustCompile(`(?i)(?:情景模拟|压力测试|蒙特卡洛|假设情景|scenario\s+simulation|stress\s+test|monte\s+carlo)`)
	visibleScenarioLabel      = regexp.MustCompile(`(?is)(?:title|suptitle|figtext|text|annotate)\s*\([^\)]*(?:情景模拟|非真实行情|scenario\s+simulation|not\s+actual\s+market\s+data)`)
	riskRubricPattern         = regexp.MustCompile(`(?i)(?:rubric|formula|weight|methodology|评分规则|评分依据|计算公式|权重)`)
)

func ClassifyFinancialChartCode(code, userInput string) FinancialChartDecision {
	decision := FinancialChartDecision{
		ChartLike:   chartOutputCodePattern.MatchString(code),
		FinanceLike: financialChartCodePattern.MatchString(code),
	}
	if !decision.ChartLike || !decision.FinanceLike {
		return decision
	}
	decision.Synthetic = syntheticSeriesPattern.MatchString(code) ||
		(hardcodedRiskScorePattern.MatchString(code) && !riskRubricPattern.MatchString(code))
	if !decision.Synthetic {
		return decision
	}
	decision.ScenarioAllowed = scenarioRequestPattern.MatchString(userInput) && visibleScenarioLabel.MatchString(code)
	if decision.ScenarioAllowed {
		return decision
	}
	decision.Reason = "financial chart code constructs synthetic/reconstructed market data or an unsupported numeric risk score"
	return decision
}

func FilterUnsafeFinancialChartCalls(userInput string, calls []toolcontract.Call) ([]toolcontract.Call, []string) {
	if len(calls) == 0 {
		return calls, nil
	}
	kept := make([]toolcontract.Call, 0, len(calls))
	var rejected []string
	for _, call := range calls {
		name := strings.ToLower(strings.TrimSpace(call.Name))
		if name != "ptc_execute" && name != "cli" && name != "persistent_shell" {
			kept = append(kept, call)
			continue
		}
		code := ""
		for _, key := range []string{"script", "command", "cmd"} {
			if value, ok := call.Arguments[key].(string); ok && strings.TrimSpace(value) != "" {
				code = value
				break
			}
		}
		if code == "" {
			encoded, _ := json.Marshal(call.Arguments)
			code = string(encoded)
		}
		decision := ClassifyFinancialChartCode(code, userInput)
		if decision.Synthetic && !decision.ScenarioAllowed {
			rejected = append(rejected, name+": "+decision.Reason)
			continue
		}
		kept = append(kept, call)
	}
	return kept, rejected
}
