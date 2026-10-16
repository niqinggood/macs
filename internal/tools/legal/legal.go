package legal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mac/internal/configvalue"
	"mac/internal/textutil"
)

// ContractRiskTool analyzes contract clauses for risks.
type ContractRiskTool struct{}

func (t *ContractRiskTool) Name() string { return "contract_risk" }
func (t *ContractRiskTool) Description() string {
	return "Automated contract risk analysis. Scans clauses and flags: unlimited liability, unilateral termination, " +
		"vague obligations, IP assignment, non-compete scope, penalty clauses. Returns risk scores (Red/Yellow/Green)."
}
func (t *ContractRiskTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"contract_text": map[string]interface{}{
				"type": "string", "description": "Contract text or specific clauses to analyze",
			},
			"jurisdiction": map[string]interface{}{
				"type": "string", "description": "Legal jurisdiction: cn (China), us (USA), eu (European Union), common (common law)",
				"enum": []string{"cn", "us", "eu", "common"},
			},
		},
		"required": []string{"contract_text"},
	}
}

func (t *ContractRiskTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	text := configvalue.String(params, "contract_text", "")
	jurisdiction := configvalue.String(params, "jurisdiction", "cn")

	if text == "" {
		return "Error: contract_text is required", nil
	}

	risks := analyzeContractRisks(text, jurisdiction)

	var report strings.Builder
	report.WriteString("## Contract Risk Analysis\n\n")
	report.WriteString(fmt.Sprintf("**Jurisdiction**: %s\n\n", jurisdiction))

	// Summary
	highRisk := 0
	mediumRisk := 0
	for _, r := range risks {
		if r.Level == "🔴 HIGH" {
			highRisk++
		} else if r.Level == "🟡 MEDIUM" {
			mediumRisk++
		}
	}
	report.WriteString(fmt.Sprintf("**Risk Summary**: 🔴 %d High | 🟡 %d Medium | 🟢 %d Low\n\n", highRisk, mediumRisk, len(risks)-highRisk-mediumRisk))

	report.WriteString("| # | Level | Clause | Risk | Recommendation |\n")
	report.WriteString("|---|-------|--------|------|----------------|\n")
	for i, r := range risks {
		report.WriteString(fmt.Sprintf("| %d | %s | %s | %s | %s |\n",
			i+1, r.Level, r.Clause, r.Risk, r.Fix))
	}

	// Overall assessment
	report.WriteString("\n### Overall Assessment\n")
	if highRisk > 3 {
		report.WriteString("❌ **Do NOT sign** — Multiple high-risk clauses. Significant legal exposure.\n")
	} else if highRisk > 0 {
		report.WriteString("⚠️ **Negotiate** — Contains high-risk clauses. Request modifications before signing.\n")
	} else if mediumRisk > 3 {
		report.WriteString("⚠️ **Review** — Several medium-risk clauses. Consider negotiating key terms.\n")
	} else {
		report.WriteString("✅ **Generally acceptable** — Minor concerns only. Standard terms.\n")
	}

	return report.String(), nil
}

type RiskItem struct {
	Level  string
	Clause string
	Risk   string
	Fix    string
}

func analyzeContractRisks(text, jurisdiction string) []RiskItem {
	lower := strings.ToLower(text)
	var risks []RiskItem

	// Unlimited liability check
	if strings.Contains(lower, "unlimited liability") || strings.Contains(lower, "无限责任") ||
		strings.Contains(lower, "承担全部") || strings.Contains(lower, "所有损失") {
		risks = append(risks, RiskItem{
			"🔴 HIGH", "Liability", "Unlimited liability exposure",
			"Cap liability at contract value or 12 months of fees",
		})
	}

	// Unilateral termination
	if strings.Contains(lower, "at any time") || strings.Contains(lower, "随时终止") ||
		strings.Contains(lower, "sole discretion") || strings.Contains(lower, "单方解除") ||
		strings.Contains(lower, "无需承担任何赔偿") {
		risks = append(risks, RiskItem{
			"🔴 HIGH", "Termination", "Unilateral termination without cause or compensation",
			"Require mutual consent or 30-day notice with cause",
		})
	}

	// IP assignment
	if (strings.Contains(lower, "intellectual property") || strings.Contains(lower, "知识产权")) &&
		(strings.Contains(lower, "assign") || strings.Contains(lower, "transfer") || strings.Contains(lower, "归属") ||
			strings.Contains(lower, "all rights") || strings.Contains(lower, "所有权利")) {
		risks = append(risks, RiskItem{
			"🔴 HIGH", "IP Rights", "Broad IP assignment — may lose ownership of pre-existing IP",
			"Narrow scope to work product specifically created for this engagement",
		})
	}

	// Non-compete
	if strings.Contains(lower, "non-compete") || strings.Contains(lower, "竞业限制") ||
		strings.Contains(lower, "不得从事") || strings.Contains(lower, "shall not engage") {
		scope := "Broad"
		if strings.Contains(lower, "2 years") || strings.Contains(lower, "两年") {
			scope = "Excessive (2+ years)"
		}
		risks = append(risks, RiskItem{
			"🟡 MEDIUM", "Non-Compete", fmt.Sprintf("%s non-compete clause", scope),
			"Limit to 6-12 months, specific competitors, and geographic scope",
		})
	}

	// Vague obligations
	if strings.Contains(lower, "best efforts") || strings.Contains(lower, "尽最大努力") ||
		strings.Contains(lower, "reasonable endeavors") || strings.Contains(lower, "合理努力") {
		risks = append(risks, RiskItem{
			"🟡 MEDIUM", "Obligations", "Vague performance standard ('best efforts')",
			"Define specific deliverables, metrics, and timelines",
		})
	}

	// Penalty clauses
	if strings.Contains(lower, "penalty") || strings.Contains(lower, "违约金") ||
		strings.Contains(lower, "liquidated damages") || strings.Contains(lower, "罚金") {
		risks = append(risks, RiskItem{
			"🟡 MEDIUM", "Penalties", "Penalty/liquidated damages clause",
			"Ensure penalties are proportional to actual damages",
		})
	}

	// Auto-renewal
	if strings.Contains(lower, "auto-renew") || strings.Contains(lower, "自动续期") ||
		strings.Contains(lower, "automatically renew") {
		risks = append(risks, RiskItem{
			"🟢 LOW", "Term", "Auto-renewal clause",
			"Confirm renewal notice period (recommend 30-60 days)",
		})
	}

	// Governing law
	if strings.Contains(lower, "governing law") || strings.Contains(lower, "管辖") {
		risks = append(risks, RiskItem{
			"🟢 LOW", "Jurisdiction", "Check governing law and dispute resolution venue",
			"Prefer local jurisdiction or neutral arbitration",
		})
	}

	// Data privacy
	if strings.Contains(lower, "personal data") || strings.Contains(lower, "个人信息") ||
		strings.Contains(lower, "data protection") || strings.Contains(lower, "数据保护") {
		risks = append(risks, RiskItem{
			"🟡 MEDIUM", "Data Privacy", "Personal data handling obligations",
			"Ensure compliance with applicable data protection laws (GDPR/PIPL)",
		})
	}

	if len(risks) == 0 {
		risks = append(risks, RiskItem{
			"🟢 LOW", "General", "No high-risk patterns detected in automated scan",
			"Have a qualified lawyer review the full document",
		})
	}

	return risks
}

// ==================== legal_doc_gen tool ====================

type LegalDocTool struct {
	workspace string
}

func NewLegalDocTool(workspace string) *LegalDocTool {
	os.MkdirAll(filepath.Join(workspace, ".openkitty", "storage", "legal"), 0755)
	return &LegalDocTool{workspace: workspace}
}

func (t *LegalDocTool) Name() string { return "legal_doc_gen" }
func (t *LegalDocTool) Description() string {
	return "Generate standard legal documents: NDA (保密协议), Service Agreement (服务合同), " +
		"Employment Contract (劳动合同), Privacy Policy (隐私政策), Terms of Service (服务条款)."
}
func (t *LegalDocTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"doc_type": map[string]interface{}{
				"type": "string", "description": "nda, service_agreement, employment, privacy_policy, tos",
				"enum": []string{"nda", "service_agreement", "employment", "privacy_policy", "tos"},
			},
			"party_a": map[string]interface{}{
				"type": "string", "description": "Party A name (disclosing party / employer / company)",
			},
			"party_b": map[string]interface{}{
				"type": "string", "description": "Party B name (receiving party / employee / user)",
			},
			"effective_date": map[string]interface{}{
				"type": "string", "description": "Effective date (YYYY-MM-DD). Default: today",
			},
			"term_months": map[string]interface{}{
				"type": "integer", "description": "Contract term in months (default 12)",
			},
			"jurisdiction": map[string]interface{}{
				"type": "string", "description": "cn (China), us (USA), eu (EU)",
				"enum": []string{"cn", "us", "eu"},
			},
		},
		"required": []string{"doc_type", "party_a", "party_b"},
	}
}

func (t *LegalDocTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	docType := configvalue.String(params, "doc_type", "nda")
	partyA := configvalue.String(params, "party_a", "")
	partyB := configvalue.String(params, "party_b", "")
	effectiveDate := configvalue.String(params, "effective_date", time.Now().Format("2006-01-02"))
	termMonths := configvalue.Int(params, "term_months", 12)
	jurisdiction := configvalue.String(params, "jurisdiction", "cn")

	if partyA == "" || partyB == "" {
		return "Error: party_a and party_b are required", nil
	}

	doc := t.generateDocument(docType, partyA, partyB, effectiveDate, termMonths, jurisdiction)

	filename := fmt.Sprintf("%s_%s_%s.md", docType, partyA, time.Now().Format("20060102"))
	filePath := filepath.Join(t.workspace, ".openkitty", "storage", "legal", filename)
	os.WriteFile(filePath, []byte(doc), 0644)

	disclaimer := "⚠️ This is a template. Review by a qualified lawyer before use."
	return fmt.Sprintf("## %s Generated\n\n**File**: `%s`\n\n%s\n\n```markdown\n%s\n```",
		docTypeName(docType), filePath, disclaimer, textutil.Truncate(doc, 2000)), nil
}

func (t *LegalDocTool) generateDocument(docType, partyA, partyB, date string, term int, jurisdiction string) string {
	var sb strings.Builder

	switch docType {
	case "nda":
		sb.WriteString(fmt.Sprintf(`# 保密协议 (Non-Disclosure Agreement)

**甲方 (Disclosing Party)**: %s
**乙方 (Receiving Party)**: %s
**生效日期**: %s

## 1. 定义
"保密信息"指甲方向乙方披露的、与甲方业务相关的所有非公开信息。

## 2. 保密义务
乙方承诺对保密信息严格保密，不得向任何第三方披露。

## 3. 使用限制
保密信息仅用于双方合作的商业目的，不得用于其他用途。

## 4. 保密期限
本协议有效期内及终止后 **3年** 内持续有效。

## 5. 例外情况
以下信息不受保密义务约束：
- 已公开的信息
- 乙方在披露前已知悉的信息
- 法律要求披露的信息

## 6. 违约责任
违约方应赔偿守约方因此遭受的全部实际损失。

## 7. 管辖法律
本协议适用%s法律。

**甲方签字**: ___________  日期: ___________
**乙方签字**: ___________  日期: ___________
`, partyA, partyB, date, jurisdictionName(jurisdiction)))

	case "service_agreement":
		sb.WriteString(fmt.Sprintf(`# 服务合同 (Service Agreement)

**委托方**: %s
**服务方**: %s
**合同期限**: %s 起 %d 个月

## 1. 服务内容
服务方按照附件A的服务清单提供相关服务。

## 2. 服务费用
总费用及支付方式详见附件B。

## 3. 验收标准
委托方应在收到交付物后 **5个工作日** 内完成验收。

## 4. 知识产权
服务过程中产生的知识产权归 **委托方** 所有。

## 5. 违约责任
任何一方违约，应赔偿对方直接损失，上限为合同总金额。

## 6. 终止条款
任一方可提前 **30天** 书面通知终止本合同。

## 7. 争议解决
双方协商解决；协商不成的，提交%s仲裁。

**甲方**: ___________  **乙方**: ___________
日期: ___________  日期: ___________
`, partyA, partyB, date, term, jurisdictionName(jurisdiction)))

	case "employment":
		sb.WriteString(fmt.Sprintf(`# 劳动合同 (Employment Contract)

**用人单位**: %s
**员工**: %s
**入职日期**: %s

## 1. 工作岗位
员工担任 **[职位]** ，工作地点为 **[地点]**。

## 2. 工作时间
标准工时制，每周工作不超过40小时。

## 3. 薪酬待遇
月薪人民币 **[金额]** 元，每月 **[日期]** 发放。

## 4. 试用期
试用期 **[3-6]** 个月，试用期工资为正式工资的80%%。

## 5. 社会保险
用人单位依法为员工缴纳社会保险和住房公积金。

## 6. 保密与竞业限制
员工在职期间及离职后 **[期限]** 内负有保密义务。

## 7. 合同解除
双方均可依法解除本合同，需提前 **30天** 书面通知。

**用人单位 (盖章)**: ___________  日期: ___________
**员工签字**: ___________  日期: ___________
`, partyA, partyB, date))

	case "privacy_policy":
		sb.WriteString(fmt.Sprintf(`# 隐私政策 (Privacy Policy)

**最后更新**: %s
**运营方**: %s

## 1. 信息收集
我们收集的个人信息包括：
- 账户信息：姓名、邮箱、手机号
- 使用数据：访问日志、操作记录
- 设备信息：IP地址、浏览器类型

## 2. 信息使用
我们使用收集的信息用于：
- 提供和改进服务
- 客户支持和沟通
- 法律合规要求

## 3. 信息共享
我们不会向第三方出售您的个人信息。仅在以下情况共享：
- 经您明确同意
- 法律要求
- 与服务提供商合作（受保密协议约束）

## 4. 数据安全
我们采取行业标准的安全措施保护您的数据。

## 5. 您的权利
您有权：访问、更正、删除您的个人信息；撤回同意；数据可携带。

## 6. Cookie政策
我们使用必要的Cookie来维持服务运行。

## 7. 联系我们
如有隐私问题，请联系：%s

`, date, partyA, partyB))

	case "tos":
		sb.WriteString(fmt.Sprintf(`# 服务条款 (Terms of Service)

**最后更新**: %s

## 1. 接受条款
使用%s的服务即表示您同意本条款。

## 2. 服务描述
我们提供 **[服务描述]**。

## 3. 用户义务
用户承诺：不违反法律法规；不侵犯他人权利；不滥用服务。

## 4. 知识产权
平台内容的知识产权归%s所有。用户保留其发布内容的所有权。

## 5. 免责声明
服务按"现状"提供，我们不保证服务不中断或无错误。

## 6. 责任限制
在法律允许的范围内，我们的责任限于您过去12个月支付的服务费用。

## 7. 终止
我们保留在违反条款时终止服务的权利。

## 8. 适用法律
本条款受%s法律管辖。

**@%s** — 保留所有权利
`, date, partyA, partyA, jurisdictionName(jurisdiction), partyA))
	}

	return sb.String()
}

func docTypeName(t string) string {
	switch t {
	case "nda":
		return "保密协议 (NDA)"
	case "service_agreement":
		return "服务合同"
	case "employment":
		return "劳动合同"
	case "privacy_policy":
		return "隐私政策"
	case "tos":
		return "服务条款"
	}
	return t
}

func jurisdictionName(j string) string {
	switch j {
	case "cn":
		return "中华人民共和国"
	case "us":
		return "美国"
	case "eu":
		return "欧盟"
	}
	return j
}

// ==================== compliance_check tool ====================

type ComplianceCheckTool struct{}

func (t *ComplianceCheckTool) Name() string { return "compliance_check" }
func (t *ComplianceCheckTool) Description() string {
	return "Generate compliance checklists for common regulations: GDPR, PIPL (个人信息保护法), " +
		"CSL (网络安全法), DSL (数据安全法), SOC2, ISO27001."
}
func (t *ComplianceCheckTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"regulation": map[string]interface{}{
				"type": "string", "description": "gdpr, pipl, csl, dsl, soc2, iso27001",
				"enum": []string{"gdpr", "pipl", "csl", "dsl", "soc2", "iso27001"},
			},
		},
		"required": []string{"regulation"},
	}
}

func (t *ComplianceCheckTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	regulation := configvalue.String(params, "regulation", "gdpr")

	checklist := getComplianceChecklist(regulation)

	var report strings.Builder
	report.WriteString(fmt.Sprintf("## Compliance Checklist: %s\n\n", strings.ToUpper(regulation)))
	report.WriteString("| # | Check Item | Status | Priority |\n")
	report.WriteString("|---|------------|--------|----------|\n")
	for i, item := range checklist {
		report.WriteString(fmt.Sprintf("| %d | %s | ⬜ | %s |\n", i+1, item.Item, item.Priority))
	}
	return report.String(), nil
}

type ChecklistItem struct {
	Item     string
	Priority string
}

func getComplianceChecklist(regulation string) []ChecklistItem {
	switch regulation {
	case "gdpr":
		return []ChecklistItem{
			{"Appoint Data Protection Officer (DPO)", "HIGH"},
			{"Maintain Records of Processing Activities (ROPA)", "HIGH"},
			{"Implement Data Protection Impact Assessment (DPIA)", "HIGH"},
			{"Establish lawful basis for all data processing", "HIGH"},
			{"Implement data subject rights (access, rectification, erasure, portability)", "HIGH"},
			{"Obtain explicit consent for data collection", "HIGH"},
			{"Implement data breach notification procedure (72 hours)", "HIGH"},
			{"Sign Data Processing Agreements (DPA) with vendors", "MEDIUM"},
			{"Implement Privacy by Design and Default", "MEDIUM"},
			{"Conduct regular data protection training", "MEDIUM"},
			{"Cross-border data transfer safeguards (SCCs/BCRs)", "MEDIUM"},
			{"Cookie consent mechanism", "LOW"},
		}
	case "pipl":
		return []ChecklistItem{
			{"Appoint Personal Information Protection Officer", "HIGH"},
			{"Establish personal information processing rules", "HIGH"},
			{"Obtain individual consent for data collection", "HIGH"},
			{"Conduct Personal Information Protection Impact Assessment", "HIGH"},
			{"Implement data subject rights (inquiry, correction, deletion)", "HIGH"},
			{"Establish data breach notification mechanism", "HIGH"},
			{"Sign data processing agreements with vendors", "HIGH"},
			{"Personal information localization for CIIO", "MEDIUM"},
			{"Cross-border transfer security assessment", "MEDIUM"},
			{"Implement data classification and grading", "MEDIUM"},
			{"Regular compliance audits", "MEDIUM"},
		}
	case "csl":
		return []ChecklistItem{
			{"Implement network security protection measures", "HIGH"},
			{"Establish security incident reporting (to CAC)", "HIGH"},
			{"Conduct security assessments for critical equipment", "HIGH"},
			{"Implement real-name authentication for users", "HIGH"},
			{"Personal information protection measures", "HIGH"},
			{"Network security training for personnel", "MEDIUM"},
			{"Data classification and backup", "MEDIUM"},
			{"Network security monitoring and auditing", "MEDIUM"},
			{"Emergency response plan", "MEDIUM"},
		}
	case "dsl":
		return []ChecklistItem{
			{"Establish data classification and grading system", "HIGH"},
			{"Implement data security protection measures", "HIGH"},
			{"Conduct risk assessment for important data processing", "HIGH"},
			{"Cross-border data transfer security assessment for important data", "HIGH"},
			{"Appoint data security officer", "HIGH"},
			{"Data security training", "MEDIUM"},
			{"Data security incident reporting", "HIGH"},
			{"Regular data security audits", "MEDIUM"},
		}
	case "soc2":
		return []ChecklistItem{
			{"Define and document security policies", "HIGH"},
			{"Implement access control and authentication", "HIGH"},
			{"Monitor system operations and security events", "HIGH"},
			{"Implement change management process", "HIGH"},
			{"Conduct risk assessment", "HIGH"},
			{"Vendor risk management", "MEDIUM"},
			{"Business continuity and disaster recovery plan", "MEDIUM"},
			{"Data encryption at rest and in transit", "HIGH"},
			{"Regular penetration testing", "MEDIUM"},
		}
	case "iso27001":
		return []ChecklistItem{
			{"Define Information Security Management System (ISMS) scope", "HIGH"},
			{"Information security policy and objectives", "HIGH"},
			{"Risk assessment and treatment plan", "HIGH"},
			{"Statement of Applicability (SoA)", "HIGH"},
			{"Asset inventory and classification", "HIGH"},
			{"Access control policy", "HIGH"},
			{"Cryptography policy", "HIGH"},
			{"Incident management procedure", "MEDIUM"},
			{"Business continuity plan", "MEDIUM"},
			{"Internal audit program", "MEDIUM"},
			{"Management review", "MEDIUM"},
		}
	}
	return nil
}

// ==================== legal_qa tool ====================

type LegalQATool struct{}

func (t *LegalQATool) Name() string { return "legal_qa" }
func (t *LegalQATool) Description() string {
	return "AI legal assistant for common scenarios. Ask legal questions about: contracts, company law, employment, " +
		"IP protection, data privacy, compliance, dispute resolution. Provides practical guidance with legal references."
}
func (t *LegalQATool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"scenario": map[string]interface{}{
				"type": "string", "description": "contract (合同), employment (劳动), ip (知识产权), data (数据隐私), company (公司法), dispute (纠纷)",
				"enum": []string{"contract", "employment", "ip", "data", "company", "dispute"},
			},
			"question": map[string]interface{}{
				"type": "string", "description": "Your legal question in plain language",
			},
			"jurisdiction": map[string]interface{}{
				"type": "string", "description": "cn (中国法), us (美国法), eu (欧盟法)",
				"enum": []string{"cn", "us", "eu"},
			},
		},
		"required": []string{"scenario", "question"},
	}
}

func (t *LegalQATool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	scenario := configvalue.String(params, "scenario", "contract")
	question := configvalue.String(params, "question", "")
	jurisdiction := configvalue.String(params, "jurisdiction", "cn")

	if question == "" {
		return "Error: question is required", nil
	}

	// Generate an LLM-ready system prompt for this legal scenario
	sysPrompt := t.buildLegalSystemPrompt(scenario, jurisdiction)

	var report strings.Builder
	report.WriteString("## Legal AI Assistant\n\n")
	report.WriteString(fmt.Sprintf("**Scenario**: %s | **Jurisdiction**: %s\n\n", scenario, jurisdiction))
	report.WriteString(fmt.Sprintf("**Question**: %s\n\n", question))
	report.WriteString("### AI System Prompt for Legal Advisory\n")
	report.WriteString("To get a detailed answer, send this system prompt to the LLM with your question:\n\n")
	report.WriteString("```\n")
	report.WriteString(sysPrompt)
	report.WriteString("\n```\n\n")

	// Quick knowledge base lookup
	kb := t.lookupLegalKB(scenario, question, jurisdiction)
	if kb != "" {
		report.WriteString("### Quick Reference\n")
		report.WriteString(kb)
	}

	return report.String(), nil
}

func (t *LegalQATool) buildLegalSystemPrompt(scenario, jurisdiction string) string {
	base := fmt.Sprintf("You are a professional legal advisor specializing in %s law. ", jurisdictionName(jurisdiction))
	base += "Provide practical, actionable legal guidance. Always include: "
	base += "1) Relevant legal principles 2) Practical recommendations 3) Risk assessment 4) Next steps. "

	switch scenario {
	case "contract":
		base += "You review and advise on contract matters: formation, performance, breach, remedies, standard clauses, negotiation strategy."
	case "employment":
		base += "You advise on employment law: hiring, termination, discrimination, wages, benefits, workplace safety, non-compete."
	case "ip":
		base += "You advise on intellectual property: patents, trademarks, copyright, trade secrets, licensing, infringement."
	case "data":
		base += "You advise on data protection: privacy laws, data security, breach notification, cross-border transfer, user rights."
	case "company":
		base += "You advise on corporate law: incorporation, governance, shareholder rights, M&A, compliance, director duties."
	case "dispute":
		base += "You advise on dispute resolution: litigation, arbitration, mediation, settlement negotiation, enforcement."
	}

	base += " IMPORTANT: Include a disclaimer that this is informational, not legal advice. Recommend consulting a qualified lawyer."
	return base
}

func (t *LegalQATool) lookupLegalKB(scenario, question, jurisdiction string) string {
	lower := strings.ToLower(question)
	var items []string

	switch scenario {
	case "contract":
		if strings.Contains(lower, "签字") || strings.Contains(lower, "sign") {
			items = append(items, "合同经双方签字盖章后生效。电子签名与手写签名具有同等法律效力。")
		}
		if strings.Contains(lower, "违约") || strings.Contains(lower, "breach") {
			items = append(items, "违约救济：实际履行、损害赔偿、合同解除。违约金过高可请求法院调减。")
		}
		if strings.Contains(lower, "无效") || strings.Contains(lower, "valid") {
			items = append(items, "合同无效情形：欺诈胁迫、恶意串通、合法形式掩盖非法目的、损害公共利益、违反强制性规定。")
		}
	case "employment":
		if strings.Contains(lower, "开除") || strings.Contains(lower, "解雇") || strings.Contains(lower, "fire") {
			items = append(items, "用人单位单方解除需有法定事由。违法解除需支付2倍经济补偿金作为赔偿。")
		}
		if strings.Contains(lower, "加班") || strings.Contains(lower, "overtime") {
			items = append(items, "加班费标准：工作日150%，休息日200%，法定节假日300%。每月加班不超过36小时。")
		}
		if strings.Contains(lower, "社保") || strings.Contains(lower, "social") {
			items = append(items, "用人单位必须为员工缴纳五险一金：养老、医疗、失业、工伤、生育保险 + 住房公积金。")
		}
	case "ip":
		if strings.Contains(lower, "专利") || strings.Contains(lower, "patent") {
			items = append(items, "专利类型：发明(20年)、实用新型(10年)、外观设计(15年)。需具备新颖性、创造性、实用性。")
		}
		if strings.Contains(lower, "商标") || strings.Contains(lower, "trademark") {
			items = append(items, "商标注册有效期为10年，到期可续展。未经注册的商标保护力度较弱。")
		}
		if strings.Contains(lower, "著作权") || strings.Contains(lower, "copyright") {
			items = append(items, "著作权自动产生，无需登记。法人作品保护期为发表后50年。")
		}
	case "data":
		if strings.Contains(lower, "用户数据") || strings.Contains(lower, "个人信息") {
			items = append(items, "收集个人信息需遵循：合法正当必要原则、知情同意、目的限制、最小化收集。")
		}
		if strings.Contains(lower, "泄露") || strings.Contains(lower, "breach") {
			items = append(items, "数据泄露需在72小时内通知监管机构。可能面临年收入4%的罚款(GDPR)。")
		}
	case "company":
		if strings.Contains(lower, "注册") || strings.Contains(lower, "成立") {
			items = append(items, "公司注册需要：名称核准、章程制定、注册资本认缴、工商登记、税务登记。")
		}
		if strings.Contains(lower, "股东") || strings.Contains(lower, "shareholder") {
			items = append(items, "股东权利：资产收益、参与决策、选择管理者、知情权、诉讼权。")
		}
	case "dispute":
		if strings.Contains(lower, "起诉") || strings.Contains(lower, "sue") {
			items = append(items, "诉讼时效一般为3年。需准备：起诉状、证据材料、主体资格证明。")
		}
		if strings.Contains(lower, "仲裁") || strings.Contains(lower, "arbitration") {
			items = append(items, "仲裁需双方有仲裁协议。一裁终局，效率高于诉讼。")
		}
	}

	if len(items) == 0 {
		return ""
	}

	result := ""
	for _, item := range items {
		result += fmt.Sprintf("- %s\n", item)
	}
	result += "\n⚠️ 以上为一般法律常识，具体情况请咨询专业律师。\n"
	return result
}
