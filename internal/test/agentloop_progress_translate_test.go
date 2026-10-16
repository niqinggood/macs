package app

import (
	"encoding/json"
	"strings"
	"testing"

	"mac/internal/eventbus"
)

const toolFailureSummaryLimit = 800

func translateProgressForTest(thought, thoughtType string) string {
	return eventbus.TranslateProgressMessage(thought, thoughtType, func(value string) string {
		return Clean(StripToolCalls(value))
	}, toolFailureSummaryLimit)
}

func TestTranslateProgressMessageKeepsLongCLICommandDetail(t *testing.T) {
	marker := "OPENKITTY_PRESENTATION_ENTRYPOINT"
	command := `cd "C:\Users\niqin\.openkitty\runtime\builtin\presentations\` +
		strings.Repeat(`nested-workspace\`, 12) + marker + `" && powershell -File run.ps1`
	argsJSON, err := json.Marshal(map[string]interface{}{"command": command})
	if err != nil {
		t.Fatal(err)
	}

	message := translateProgressForTest("will use cli: "+string(argsJSON), "use_tool")
	if !strings.Contains(message, marker) {
		t.Fatalf("CLI progress truncated the useful path too early: %q", message)
	}
}

func TestTranslateProgressMessageKeepsLongToolFailureDetail(t *testing.T) {
	marker := "ROOT_CAUSE_CONNECTION_RESET"
	failure := "social_trending failed: 执行失败: " + strings.Repeat("上游请求失败；", 35) +
		`fetch: Get "https://www.v2ex.com/api/topics/hot.json": ` + marker

	message := translateProgressForTest(failure, "tool_error")
	if !strings.Contains(message, marker) {
		t.Fatalf("tool failure progress truncated the useful root cause too early: %q", message)
	}
}

func TestCompactToolFailureKeepsUsefulDetailBeyondLegacyLimit(t *testing.T) {
	marker := "ROOT_CAUSE_DNS_TIMEOUT"
	failure := "执行失败: " + strings.Repeat("网络错误；", 60) + marker

	if compacted := translateProgressForTest(failure, "tool_error"); !strings.Contains(compacted, marker) {
		t.Fatalf("compacted failure lost useful detail too early: %q", compacted)
	}
}
