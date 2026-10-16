package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SecurityDimension represents a category of security risk.
type SecurityDimension string

const (
	DimFileAccess       SecurityDimension = "file_access"
	DimDangerousCommand SecurityDimension = "dangerous_command"
	DimNetwork          SecurityDimension = "network"
	DimProcess          SecurityDimension = "process"
	DimScreenInput      SecurityDimension = "screen_input"
	DimPayment          SecurityDimension = "payment"
	DimWebContent       SecurityDimension = "web_content"
)

// FindingSeverity indicates how dangerous a finding is.
type FindingSeverity string

const (
	SevInfo     FindingSeverity = "info"
	SevWarning  FindingSeverity = "warning"
	SevDanger   FindingSeverity = "danger"
	SevCritical FindingSeverity = "critical"
)

// SecurityFinding represents a single security issue found in a skill file.
type SecurityFinding struct {
	Dimension      SecurityDimension `json:"dimension"`
	Severity       FindingSeverity   `json:"severity"`
	RuleID         string            `json:"rule_id"`
	File           string            `json:"file"`
	Line           int               `json:"line,omitempty"`
	MatchedPattern string            `json:"matched_pattern"`
	Description    string            `json:"description"`
}

// SkillSecurityReport summarizes security scan results for a skill.
type SkillSecurityReport struct {
	ScannedAt        time.Time                 `json:"scanned_at"`
	SkillName        string                    `json:"skill_name"`
	RiskLevel        string                    `json:"risk_level"` // safe, low, medium, high, critical
	RiskScore        int                       `json:"risk_score"`
	Findings         []SecurityFinding         `json:"findings"`
	DimensionSummary map[SecurityDimension]int `json:"dimension_summary"`
	ScanDurationMs   int64                     `json:"scan_duration_ms"`
}

// SecurityRule defines a pattern to check for in skill files.
type SecurityRule struct {
	ID           SecurityDimension
	Severity     FindingSeverity
	FilePatterns []string // file extensions or names to check
	Patterns     []*regexp.Regexp
	Description  string
}

// skillSecurityRules returns all security rules (adapted from LobsterAI).
func skillSecurityRules() []SecurityRule {
	mustCompile := func(patterns ...string) []*regexp.Regexp {
		var res []*regexp.Regexp
		for _, p := range patterns {
			res = append(res, regexp.MustCompile(p))
		}
		return res
	}

	return []SecurityRule{
		// ── File Access ──
		{ID: DimFileAccess, Severity: SevCritical, FilePatterns: []string{".sh", ".bash", ".js", ".ts", ".ps1", ".bat"}, Patterns: mustCompile(
			`\.ssh/(id_rsa|id_ed25519|id_ecdsa|id_dsa|authorized_keys|known_hosts|config)\b`,
			`cat\s+~?/?\.ssh/`, `readFileSync\(.*\.ssh`,
		), Description: "尝试读取SSH密钥文件"},
		{ID: DimFileAccess, Severity: SevCritical, FilePatterns: []string{".sh", ".js", ".ts", ".ps1"}, Patterns: mustCompile(
			`\.aws/(credentials|config)\b`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`,
		), Description: "尝试访问AWS凭证"},
		{ID: DimFileAccess, Severity: SevDanger, FilePatterns: []string{".sh", ".js", ".ts", ".ps1"}, Patterns: mustCompile(
			`\.npmrc\b`, `\.netrc\b`, `\.pypirc\b`, `\.docker/config\.json`, `\.kube/config`, `\.gnupg/`,
		), Description: "尝试访问令牌/凭证文件"},
		{ID: DimFileAccess, Severity: SevWarning, FilePatterns: []string{".sh", ".js", ".ts", ".ps1"}, Patterns: mustCompile(
			`process\.env\.(API_KEY|SECRET|TOKEN|PASSWORD|PRIVATE_KEY)`, `\$\{?(API_KEY|SECRET_KEY|AUTH_TOKEN|DB_PASSWORD)\}?`,
		), Description: "尝试读取环境变量中的密钥"},

		// ── Dangerous Commands ──
		{ID: DimDangerousCommand, Severity: SevDanger, FilePatterns: []string{".sh", ".bash", ".ps1", ".bat", ".cmd"}, Patterns: mustCompile(
			`\brm\s+-[a-zA-Z]*r[a-zA-Z]*f\b`, `\brm\s+--force\s+--recursive\b`, `Remove-Item\s+.*-Recurse\s+-Force`,
		), Description: "包含危险的递归删除命令"},
		{ID: DimDangerousCommand, Severity: SevCritical, FilePatterns: []string{".sh", ".bash", ".ps1", ".bat"}, Patterns: mustCompile(
			`\bmkfs\b`, `\bdd\s+.*of=/dev/`, `\bdiskpart\b`, `\bfdisk\b`,
		), Description: "包含磁盘格式化/破坏命令"},
		{ID: DimDangerousCommand, Severity: SevDanger, FilePatterns: []string{".sh", ".bash"}, Patterns: mustCompile(
			`\bsudo\b`, `\bsu\s+-\b`,
		), Description: "使用sudo提权"},
		{ID: DimDangerousCommand, Severity: SevCritical, FilePatterns: []string{".sh", ".bash", ".ps1", ".bat"}, Patterns: mustCompile(
			`\bchmod\s+777\b`, `\bchown\s+-R\s+root`, `/etc/passwd`, `/etc/shadow`, `\biptables\b`, `reg\s+add\b.*HKLM`,
		), Description: "尝试修改系统关键配置"},
		{ID: DimDangerousCommand, Severity: SevCritical, FilePatterns: []string{".sh", ".bash", ".bat"}, Patterns: mustCompile(
			`:\(\)\s*\{\s*:\|:\s*&\s*\}\s*;`,
		), Description: "包含fork炸弹"},

		// ── Network ──
		{ID: DimNetwork, Severity: SevDanger, FilePatterns: []string{".sh", ".bash"}, Patterns: mustCompile(
			`curl\s+.*-[a-zA-Z]*d\s+.*(\$HOME|\$\(cat|/etc/|\.ssh|passwd)`, `curl\s+.*--data.*(\$HOME|\.ssh|passwd)`,
		), Description: "尝试通过curl外传数据"},
		{ID: DimNetwork, Severity: SevDanger, FilePatterns: []string{".js", ".ts"}, Patterns: mustCompile(
			`https?://[^'"\s]*\.(tk|ml|ga|cf)/`, `ngrok\.io|serveo\.net|localtunnel\.me`, `pastebin\.com|paste\.ee`,
		), Description: "向外传数据到可疑域名"},
		{ID: DimNetwork, Severity: SevWarning, FilePatterns: []string{".sh", ".js", ".ts", ".ps1"}, Patterns: mustCompile(
			`discord\.com/api/webhooks/`, `hooks\.slack\.com`, `api\.telegram\.org/bot`,
		), Description: "通过webhook向外发送数据"},

		// ── Process ──
		{ID: DimProcess, Severity: SevCritical, FilePatterns: []string{".sh", ".bash", ".js", ".ts", ".ps1"}, Patterns: mustCompile(
			`/dev/tcp/`, `\bnc\s+.*-[a-zA-Z]*e\b`, `python\s+-c\s+['"]import\s+socket`,
			`New-Object\s+Net\.Sockets\.TcpClient`, `\bsocat\b.*exec:`,
		), Description: "包含反弹shell代码"},
		{ID: DimProcess, Severity: SevCritical, FilePatterns: []string{".sh", ".bash", ".js", ".ts"}, Patterns: mustCompile(
			`\bxmrig\b`, `\bcgminer\b`, `stratum\+tcp://`, `cryptonight|randomx`,
		), Description: "包含加密货币挖矿代码"},
		{ID: DimProcess, Severity: SevWarning, FilePatterns: []string{".sh", ".bash", ".ps1"}, Patterns: mustCompile(
			`\bnohup\b.*&\s*$`, `crontab\s+-[el]`, `Start-Process.*-WindowStyle\s+Hidden`,
		), Description: "尝试后台驻留进程"},

		// ── Screen / Input ──
		{ID: DimScreenInput, Severity: SevDanger, FilePatterns: []string{".sh", ".bash", ".js", ".ts", ".ps1"}, Patterns: mustCompile(
			`\bscreencapture\b`, `\bscrot\b`, `desktopCapturer`, `CopyFromScreen`,
		), Description: "尝试截取屏幕"},
		{ID: DimScreenInput, Severity: SevCritical, FilePatterns: []string{".js", ".ts", ".ps1"}, Patterns: mustCompile(
			`\bkeylogger\b`, `SetWindowsHookEx.*WH_KEYBOARD`, `GetAsyncKeyState`,
		), Description: "包含键盘记录代码"},
		{ID: DimScreenInput, Severity: SevWarning, FilePatterns: []string{".js", ".ts", ".sh", ".ps1"}, Patterns: mustCompile(
			`clipboard\.readText`, `\bpbpaste\b`, `Get-Clipboard`,
		), Description: "尝试读取剪贴板"},

		// ── Payment ──
		{ID: DimPayment, Severity: SevDanger, FilePatterns: []string{".js", ".ts", ".sh"}, Patterns: mustCompile(
			`stripe\.com/v1/charges`, `paypal\.com/v[12]/payments`, `api\.alipay\.com`, `api\.mch\.weixin\.qq\.com`,
		), Description: "包含支付API调用"},
		{ID: DimPayment, Severity: SevDanger, FilePatterns: []string{".js", ".ts", ".sh", ".ps1"}, Patterns: mustCompile(
			`\bweb3\.eth\.sendTransaction\b`, `\bsolana.*transfer\b`, `\bprivateKey\b.*\b0x[0-9a-f]{64}\b`,
		), Description: "包含加密货币交易代码"},

		// ── Web Content ──
		{ID: DimWebContent, Severity: SevWarning, FilePatterns: []string{".html", ".htm"}, Patterns: mustCompile(
			`<script[^>]*>[\s\S]*?(document\.cookie|localStorage|sessionStorage)`, `on\w+\s*=\s*["'].*?(eval|Function)`,
		), Description: "HTML中包含危险内联脚本"},
		{ID: DimWebContent, Severity: SevDanger, FilePatterns: []string{".svg"}, Patterns: mustCompile(
			`<script\b`, `on\w+\s*=`, `<foreignObject\b`, `javascript:`,
		), Description: "SVG中包含脚本代码"},
	}
}

// ScanSkillDir scans a skill directory for security issues.
func ScanSkillDir(skillDir string) (*SkillSecurityReport, error) {
	start := time.Now()
	rules := skillSecurityRules()

	skillName := filepath.Base(skillDir)
	report := &SkillSecurityReport{
		ScannedAt:        time.Now(),
		SkillName:        skillName,
		RiskLevel:        "safe",
		DimensionSummary: make(map[SecurityDimension]int),
	}

	err := filepath.Walk(skillDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		// Only scan relevant files
		if ext != ".sh" && ext != ".bash" && ext != ".js" && ext != ".ts" && ext != ".ps1" &&
			ext != ".bat" && ext != ".cmd" && ext != ".html" && ext != ".htm" && ext != ".svg" &&
			ext != ".md" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}
		content := string(data)

		// Check SKILL.md frontmatter for prompt injection
		if strings.HasSuffix(strings.ToLower(info.Name()), "skill.md") {
			checkPromptInjection(path, content, report)
		}

		for _, rule := range rules {
			// Check file pattern match
			fileMatch := false
			for _, fp := range rule.FilePatterns {
				if strings.TrimPrefix(fp, "**/*") == ext || fp == "**/"+strings.ToLower(info.Name()) {
					fileMatch = true
					break
				}
			}
			if !fileMatch {
				continue
			}

			for _, pattern := range rule.Patterns {
				if loc := pattern.FindStringIndex(content); loc != nil {
					lineNum := strings.Count(content[:loc[0]], "\n") + 1
					relPath, _ := filepath.Rel(skillDir, path)
					report.Findings = append(report.Findings, SecurityFinding{
						Dimension:      rule.ID,
						Severity:       rule.Severity,
						RuleID:         string(rule.ID),
						File:           relPath,
						Line:           lineNum,
						MatchedPattern: content[max(0, loc[0]-20):min(len(content), loc[1]+20)],
						Description:    rule.Description,
					})
					report.DimensionSummary[rule.ID]++
					report.RiskScore += severityScore(rule.Severity)
					break // one finding per rule per file
				}
			}
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("scan skill dir: %w", err)
	}

	report.RiskLevel = riskLevel(report.RiskScore)
	report.ScanDurationMs = time.Since(start).Milliseconds()
	return report, nil
}

// checkPromptInjection checks SKILL.md frontmatter for suspicious patterns.
func checkPromptInjection(path string, content string, report *SkillSecurityReport) {
	injectionPatterns := []struct {
		pattern     *regexp.Regexp
		description string
	}{
		{regexp.MustCompile(`(?i)(ignore|forget|disregard)\s+(all\s+)?(previous|prior|above|earlier)\s+(instructions?|rules?|guidelines?|constraints?)`), "尝试覆盖系统指令"},
		{regexp.MustCompile(`(?i)you\s+are\s+now\s+(a\s+)?(different|new|another)`), "尝试重新定义AI角色"},
		{regexp.MustCompile(`(?i)(system\s*(prompt|message|instruction).*:.*you\s+(are|must|should|will))`), "伪装成系统提示"},
		{regexp.MustCompile(`(?i)<\|im_start\|>|<\|im_end\|>|\[system\]\(#system\)`), "使用特殊标记注入"},
	}

	for _, inj := range injectionPatterns {
		if loc := inj.pattern.FindStringIndex(content); loc != nil {
			lineNum := strings.Count(content[:loc[0]], "\n") + 1
			relPath, _ := filepath.Rel(path, path) // stub, use dir
			_ = relPath
			report.Findings = append(report.Findings, SecurityFinding{
				Dimension:      "prompt_injection",
				Severity:       SevCritical,
				RuleID:         "prompt_injection.override",
				File:           filepath.Base(path),
				Line:           lineNum,
				MatchedPattern: content[max(0, loc[0]-20):min(len(content), loc[1]+20)],
				Description:    inj.description,
			})
			report.DimensionSummary["prompt_injection"]++
			report.RiskScore += 10
			break
		}
	}
}

func severityScore(s FindingSeverity) int {
	switch s {
	case SevInfo:
		return 1
	case SevWarning:
		return 3
	case SevDanger:
		return 7
	case SevCritical:
		return 10
	default:
		return 0
	}
}

func riskLevel(score int) string {
	switch {
	case score == 0:
		return "safe"
	case score <= 5:
		return "low"
	case score <= 15:
		return "medium"
	case score <= 30:
		return "high"
	default:
		return "critical"
	}
}

// HasBlockingFindings returns true if the report has critical issues that should block installation.
func (r *SkillSecurityReport) HasBlockingFindings() bool {
	for _, f := range r.Findings {
		if f.Severity == SevCritical {
			return true
		}
	}
	return false
}

// Summary returns a one-line summary of the security report.
func (r *SkillSecurityReport) Summary() string {
	if len(r.Findings) == 0 {
		return fmt.Sprintf("安全扫描通过 (%dms)", r.ScanDurationMs)
	}
	return fmt.Sprintf("发现 %d 个安全问题 (风险等级: %s, %dms)", len(r.Findings), r.RiskLevel, r.ScanDurationMs)
}

// VerboseSummary returns a multi-line detail for logging.
func (r *SkillSecurityReport) VerboseSummary() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== 技能安全扫描: %s ===\n", r.SkillName))
	sb.WriteString(fmt.Sprintf("风险等级: %s | 评分: %d | 发现: %d | 耗时: %dms\n",
		r.RiskLevel, r.RiskScore, len(r.Findings), r.ScanDurationMs))
	for _, f := range r.Findings {
		sb.WriteString(fmt.Sprintf("  [%s] %s:%d — %s\n", f.Severity, f.File, f.Line, f.Description))
	}
	return sb.String()
}
