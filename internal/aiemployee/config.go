// Package aiemployee contains digital-employee configuration and domain policy.
package aiemployee

import (
	"fmt"
	"strings"
)

type Config struct {
	ID               string            `json:"id" yaml:"id"`
	Name             string            `json:"name" yaml:"name"`
	AtName           string            `json:"at_name" yaml:"at_name"`
	Note             string            `json:"note,omitempty" yaml:"note,omitempty"`
	Role             string            `json:"role" yaml:"role"`
	SystemMessage    string            `json:"system_message,omitempty" yaml:"system_message,omitempty"`
	Pipeline         string            `json:"pipeline" yaml:"pipeline"`
	Direct           *DirectConfig     `json:"direct,omitempty" yaml:"direct,omitempty"`
	Supervisor       string            `json:"supervisor" yaml:"supervisor"`
	Channel          string            `json:"channel" yaml:"channel"`
	ChatID           string            `json:"chat_id,omitempty" yaml:"chat_id,omitempty"`
	Tier             string            `json:"tier,omitempty" yaml:"tier,omitempty"`
	ModelBinding     string            `json:"model_binding,omitempty" yaml:"model_binding,omitempty"`
	RAGKBSlugs       []string          `json:"rag_kb_slugs,omitempty" yaml:"rag_kb_slugs,omitempty"`
	RAGKBIDs         []uint            `json:"rag_kb_ids,omitempty" yaml:"rag_kb_ids,omitempty"`
	RAGMultiTurn     bool              `json:"rag_multi_turn,omitempty" yaml:"rag_multi_turn,omitempty"`
	TenantID         string            `json:"tenant_id,omitempty" yaml:"tenant_id,omitempty"`
	Template         string            `json:"template,omitempty" yaml:"template,omitempty"`
	WorkingHours     string            `json:"working_hours,omitempty" yaml:"working_hours,omitempty"`
	CronExpr         string            `json:"cron_expr,omitempty" yaml:"cron_expr,omitempty"`
	PipelineTemplate string            `json:"pipeline_template,omitempty" yaml:"pipeline_template,omitempty"`
	APITokens        map[string]string `json:"api_tokens,omitempty" yaml:"api_tokens,omitempty"`
	MentionAliases   []string          `json:"mention_aliases,omitempty" yaml:"mention_aliases,omitempty"`
	RoutingExamples  []string          `json:"routing_examples,omitempty" yaml:"routing_examples,omitempty"`
	Hidden           bool              `json:"hidden,omitempty" yaml:"hidden,omitempty"`
	InternalOnly     bool              `json:"internal_only,omitempty" yaml:"internal_only,omitempty"`
}

type DirectConfig struct {
	Prompt         string   `json:"prompt" yaml:"prompt"`
	Tools          []string `json:"tools" yaml:"tools"`
	RequiredSkills []string `json:"required_skills,omitempty" yaml:"required_skills,omitempty"`
	MaxIter        int      `json:"max_iter,omitempty" yaml:"max_iter,omitempty"`
	ArtifactPolicy string   `json:"artifact_policy,omitempty" yaml:"artifact_policy,omitempty"`
}

type SupervisorConfig struct {
	Identity             string `json:"identity" yaml:"identity"`
	Fallback             string `json:"fallback" yaml:"fallback"`
	AutoRouteAIEmployees bool   `json:"auto_route_ai_employees,omitempty" yaml:"auto_route_ai_employees,omitempty"`
}

func (cfg *Config) AEKind() string {
	if cfg == nil || cfg.Direct != nil || cfg.Pipeline == "" {
		return "single"
	}
	if strings.Contains(cfg.Pipeline, "router") {
		return "router"
	}
	return "single"
}

func (cfg *Config) GetRoutingExamples() []string {
	if cfg == nil || cfg.InternalOnly {
		return nil
	}
	if len(cfg.RoutingExamples) > 0 {
		return cfg.RoutingExamples
	}
	return autoGenerateExamples(cfg.Name, cfg.Role)
}

func autoGenerateExamples(name, role string) []string {
	keywords := strings.Split(role, "/")
	var examples []string
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		switch {
		case strings.Contains(keyword, "周报") || strings.Contains(keyword, "站会"):
			examples = append(examples, "帮我写本周的工作周报", "整理一下今天的站会纪要")
		case strings.Contains(keyword, "PR") || strings.Contains(keyword, "代码审查"):
			examples = append(examples, "帮我review一下这个PR", "检查一下最近的提交代码")
		case strings.Contains(keyword, "发版") || strings.Contains(keyword, "release"):
			examples = append(examples, "准备发布1.2.0版本", "生成这次发布的changelog")
		case strings.Contains(keyword, "投资") || strings.Contains(keyword, "分析"):
			examples = append(examples, "分析一下这支股票的走势", "帮我评估这个投资标的")
		case strings.Contains(keyword, "合同") || strings.Contains(keyword, "法务") || strings.Contains(keyword, "法律"):
			examples = append(examples, "帮我审查这份服务合同", "这个条款有没有法律风险")
		case strings.Contains(keyword, "公众号") || strings.Contains(keyword, "文章"):
			examples = append(examples, "写一篇关于AI应用的公众号文章", "帮我把这篇文章排版成HTML")
		case strings.Contains(keyword, "市场") || strings.Contains(keyword, "竞品"):
			examples = append(examples, "分析一下我们的竞争对手", "帮我做一份市场调研报告")
		case strings.Contains(keyword, "数据") || strings.Contains(keyword, "分析"):
			examples = append(examples, "分析这个CSV文件的销售趋势", "帮我把这些数据做成图表")
		case strings.Contains(keyword, "客服") || strings.Contains(keyword, "售后"):
			examples = append(examples, "我的快递什么时候到", "这个商品有问题要退货", "帮我查一下订单")
		case strings.Contains(keyword, "售前") || strings.Contains(keyword, "商品"):
			examples = append(examples, "这个多少钱", "这款有货吗", "推荐一款适合我的产品")
		case strings.Contains(keyword, "物流") || strings.Contains(keyword, "快递"):
			examples = append(examples, "我的快递到哪了", "快递怎么还没到")
		case strings.Contains(keyword, "退款") || strings.Contains(keyword, "退货"):
			examples = append(examples, "我要退款退货", "收到的商品有问题")
		case strings.Contains(keyword, "获客") || strings.Contains(keyword, "线索"):
			examples = append(examples, "帮我找几个做SaaS的潜在客户", "分析一下这个行业的客户画像")
		case strings.Contains(keyword, "翻译"):
			examples = append(examples, "帮我把这份文件翻译成英文", "翻译这段技术文档")
		case strings.Contains(keyword, "开发") || strings.Contains(keyword, "编码"):
			examples = append(examples, "帮我实现一个用户登录功能", "修复这个空指针异常")
		}
	}
	if len(examples) == 0 {
		examples = append(examples, fmt.Sprintf("帮我处理%s相关的任务", name))
	}
	if len(examples) > 3 {
		examples = examples[:3]
	}
	return examples
}
