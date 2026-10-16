package security

import (
	"log"
	"regexp"
	"strings"
)

// 借鉴hermes Promptware/Brainworm防御: 三个卡点检测prompt注入攻击
// 参考: arxiv 2601.09625

// InjectionSeverity 注入严重程度
type InjectionSeverity int

const (
	SevNone InjectionSeverity = iota // 无风险
	SevLow                           // 可疑模式
	SevHigh                          // 明确攻击
)

// InjectionResult 检测结果
type InjectionResult struct {
	Severity   InjectionSeverity
	Pattern    string // 匹配到的模式
	Redacted   string // 脱敏后的内容
	Confidence float64
}

// PromptInjectionDetector 注入检测器
type PromptInjectionDetector struct {
	patterns []injectionPattern
}

type injectionPattern struct {
	re         *regexp.Regexp
	severity   InjectionSeverity
	confidence float64
	label      string
}

// hasZeroWidthChars 检测零宽字符（高级注入常用手法）
func HasZeroWidthChars(s string) bool {
	for index, r := range s {
		// U+FEFF is the decoded form of a legal UTF-8 BOM only when it is the
		// first code point. The same character anywhere else remains suspicious.
		if index == 0 && r == 0xFEFF {
			continue
		}
		if (r >= 0x200B && r <= 0x200F) || // 零宽空格系列
			(r >= 0x202A && r <= 0x202E) || // 双向文本控制
			(r >= 0x2060 && r <= 0x2064) || // 单词连接符
			r == 0xFEFF { // 非文件开头的 BOM/零宽不换行空格
			return true
		}
	}
	return false
}

// removeZeroWidthChars 移除零宽字符
func RemoveZeroWidthChars(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 0x200B && r <= 0x200F) ||
			(r >= 0x202A && r <= 0x202E) ||
			(r >= 0x2060 && r <= 0x2064) ||
			r == 0xFEFF {
			return -1
		}
		return r
	}, s)
}

func NewPromptInjectionDetector() *PromptInjectionDetector {
	d := &PromptInjectionDetector{}

	// 借鉴hermes threat-patterns: 注入模式库
	highPatterns := []struct {
		pattern string
		label   string
	}{
		// 经典指令覆盖攻击
		{`(?i)(ignore|forget|disregard)\s+(all\s+)?(previous|prior|above|earlier)\s+(instructions?|messages?|context|prompts?)`, "指令覆盖"},
		{`(?i)(you\s+are\s+now|you\s+must\s+now|your\s+new\s+(role|task|job|identity))`, "角色篡改"},
		{`(?i)(system\s*:\s*(new|override|important|urgent|critical))`, "系统消息注入"},
		{`(?i)DAN\s+(mode|mode\s+enabled|mode\s+activated)`, "DAN模式注入"},
		{`(?i)(do\s+not\s+(follow|obey|listen|execute))`, "反指令注入"},
		{`(?i)(IMPORTANT|URGENT|CRITICAL)\s*:\s*(override|ignore|skip|bypass)`, "大写强调覆盖"},
		{`(?i)(as\s+an?\s+AI\s+(assistant|language\s+model),\s*(you|I)\s+(must|should|will))`, "AI身份操控"},
		{`(?i)(prioritize|execute|perform)\s+(this|following|below|above)\s+(over|before|instead)`, "优先级篡改"},
		{`(?i)you\s+are\s+(no\s+longer|not)\s+(an?\s+)?(AI|assistant|helper)`, "身份否定"},
		{`(?i)(输出|打印|显示|回复)\s*[：:]\s*(我|你).*(是|现在|必须|应该)`, "中文指令注入"},
	}

	for _, p := range highPatterns {
		d.patterns = append(d.patterns, injectionPattern{
			re: regexp.MustCompile(p.pattern), severity: SevHigh, confidence: 0.9, label: p.label,
		})
	}

	// 可疑模式（中风险）
	lowPatterns := []struct {
		pattern string
		label   string
	}{
		{`(?i)<\|im_start\|>|<\|im_end\|>|<\|\w+\|>`, "角色标记注入"},
		{`(?i)<instruction>|</instruction>|<directive>|</directive>`, "XML指令注入"},
		{`(?i)\[system\]|\[assistant\]|\[user\]`, "方括号角色注入"},
		{`(?i)1gn0r3|0v3rr1d3|b4ckd00r`, "leet编码注入"},
		{`(?i)(forget\s+(everything|all)|reset\s+context)`, "上下文重置"},
		{`(?i)you\s+will\s+(respond|reply|answer)\s+(exactly|precisely|only)\s+with`, "输出操控"},
		{`(?i)(secret|hidden)\s+(instruction|command|directive)`, "隐藏指令声明"},
	}

	for _, p := range lowPatterns {
		d.patterns = append(d.patterns, injectionPattern{
			re: regexp.MustCompile(p.pattern), severity: SevLow, confidence: 0.6, label: p.label,
		})
	}

	return d
}

// Scan 扫描内容，返回检测结果
func (d *PromptInjectionDetector) Scan(content string, source string) *InjectionResult {
	result := &InjectionResult{Severity: SevNone, Redacted: content}

	// 1. 检测零宽字符（高度可疑，几乎肯定是攻击）
	if HasZeroWidthChars(content) {
		result.Severity = SevHigh
		result.Confidence = 0.95
		result.Pattern = "零宽字符注入"
		result.Redacted = d.redactSuspicious(content, nil)
		return result
	}

	// 2. 扫描正则模式库
	for _, p := range d.patterns {
		if matches := p.re.FindAllString(content, -1); len(matches) > 0 {
			if p.severity > result.Severity {
				result.Severity = p.severity
				result.Pattern = p.label + ": " + strings.Join(matches, ", ")
				result.Confidence = p.confidence
			}
		}
	}

	// 3. 额外启发式：内容中出现大量role切换标记（类似conversation格式的注入）
	if result.Severity == SevNone {
		if d.hasConversationStructureInjection(content) {
			result.Severity = SevLow
			result.Confidence = 0.5
			result.Pattern = "对话结构注入"
		}
	}

	if result.Severity > SevNone {
		result.Redacted = d.redactSuspicious(content, d.patterns)
	}

	return result
}

// hasConversationStructureInjection 检测伪造的对话格式注入
func (d *PromptInjectionDetector) hasConversationStructureInjection(content string) bool {
	// 检测是否在非system上下文中出现 "User:" / "Assistant:" / "System:" 模式
	patterns := []string{"Human:", "User:", "Assistant:", "System:", "AI:"}
	count := 0
	for _, p := range patterns {
		count += strings.Count(content, p)
	}
	return count >= 3 // 出现3次以上角色标记 → 可疑
}

// redactSuspicious 脱敏可疑内容
func (d *PromptInjectionDetector) redactSuspicious(content string, patterns []injectionPattern) string {
	result := content
	// 移除零宽字符
	result = RemoveZeroWidthChars(result)
	// 截断长度，防止注入内容过大
	if len(result) > 50000 {
		result = result[:50000] + "\n[...内容过长已截断...]"
	}
	return result
}

// ScanToolOutput 卡点1: 工具输出（借鉴hermes 3 chokepoints）
func ScanToolOutput(detector *PromptInjectionDetector, toolName, content string) string {
	if detector == nil {
		return content // 未初始化，透传
	}
	result := detector.Scan(content, "tool:"+toolName)
	if result.Severity == SevHigh {
		securityLog("工具输出检测到注入攻击: tool=%s pattern=%s confidence=%.0f%%",
			toolName, result.Pattern, result.Confidence*100)
		// 高危：用脱敏版本替代
		return "[⚠️ 检测到潜在注入攻击，内容已脱敏]\n" + result.Redacted
	}
	if result.Severity == SevLow {
		securityLog(" 工具输出可疑模式: tool=%s pattern=%s", toolName, result.Pattern)
		// 低危：附加警告标记
		return "[⚠️ 内容含可疑模式] " + result.Redacted
	}
	return content
}

// ScanMemoryRecall 卡点2: 记忆召回
func ScanMemoryRecall(detector *PromptInjectionDetector, eventID, content string) (string, bool) {
	if detector == nil {
		return content, true
	}
	result := detector.Scan(content, "memory:"+eventID)
	if result.Severity >= SevLow {
		securityLog(" 记忆召回检测到注入: event=%s pattern=%s", eventID, result.Pattern)
		if result.Severity == SevHigh {
			return result.Redacted, false // 丢弃该记忆
		}
		return result.Redacted, true // 保留但脱敏
	}
	return content, true
}

// ScanSkillContent 卡点3: 技能加载
func ScanSkillContent(detector *PromptInjectionDetector, skillName string, content string) (string, bool) {
	if detector == nil {
		return content, true
	}
	result := detector.Scan(content, "skill:"+skillName)
	if result.Severity == SevHigh {
		securityLog(" 技能内容检测到注入攻击: skill=%s pattern=%s", skillName, result.Pattern)
		return "", false // 拒绝加载
	}
	if result.Severity == SevLow {
		securityLog(" 技能内容可疑: skill=%s pattern=%s", skillName, result.Pattern)
		// 低危：允许加载但记录
	}
	return content, true
}

// securityLog 安全日志
func securityLog(format string, args ...interface{}) {
	log.Printf("[SECURITY] "+format, args...)
}

// RuleCount reports the number of compiled detection patterns.
func (d *PromptInjectionDetector) RuleCount() int {
	if d == nil {
		return 0
	}
	return len(d.patterns)
}
