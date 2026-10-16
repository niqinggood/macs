package risk

import (
	"crypto/sha256"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const (
	CategorySexualContent      = "sexual_content"
	CategoryPoliticalExtremism = "political_extremism"
	CategoryIllegalInducement  = "illegal_inducement"
)

type Decision struct {
	Flagged       bool
	Category      string
	RuleID        string
	PublicMessage string
}

// AdvisoryLogger is the narrow logging contract used by gateway risk audits.
type AdvisoryLogger interface {
	Printf(format string, values ...interface{})
}

// AssessAndLogUserContent classifies a request and records only a short hash
// when it is flagged, so raw user content is never copied into diagnostic logs.
func AssessAndLogUserContent(logger AdvisoryLogger, source, text string) Decision {
	decision := AssessUserContent(text)
	if !decision.Flagged || logger == nil {
		return decision
	}
	digest := sha256.Sum256([]byte(text))
	logger.Printf(
		"[RiskControl] advisory source=%s category=%s rule=%s content_sha256=%x",
		strings.TrimSpace(source), decision.Category, decision.RuleID, digest[:8],
	)
	return decision
}

var gatewayRiskRequestSignals = []string{
	"帮我", "替我", "给我", "教我", "告诉我怎么", "如何实施", "怎么实施",
	"生成", "创作", "编写", "写一", "制作", "设计", "提供", "推荐", "寻找",
	"搜索", "下载", "分享", "传播", "发布", "推广", "招募", "策划", "优化",
	"实施", "搭建", "模仿", "扮演", "写出",
	"helpme", "howto", "generate", "create", "write", "build", "design",
	"provide", "recommend", "find", "download", "share", "promote", "recruit",
}

var gatewayRiskSexualMarkers = []string{
	"色情", "淫秽", "成人视频", "黄色网站", "黄色内容", "裸聊",
	"露骨性爱", "性描写", "情色小说", "色情小说", "成人视频下载",
	"porn", "pornographic", "explicitsex", "adultvideo", "eroticstory",
}

var gatewayRiskMinorSexualMarkers = []string{
	"儿童色情", "未成年色情", "未成年裸照", "未成年裸聊", "幼女色情",
	"儿童性剥削", "未成年人性剥削", "childporn", "csam",
	"childsexualabusematerial", "minorsexualcontent",
}

var gatewayRiskPoliticalMarkers = []string{
	"暴力推翻政府", "武装推翻政府", "颠覆国家政权", "煽动颠覆", "分裂国家",
	"煽动分裂", "组织武装暴动", "鼓动暴乱", "宣传恐怖主义", "传播恐怖主义",
	"宣传极端主义", "传播极端主义", "煽动民族仇恨", "招募恐怖组织",
	"violentoverthrow", "armeduprising", "terroristpropaganda",
	"extremistpropaganda", "inciteethnichatred", "recruitterrorists",
}

var gatewayRiskIllegalInducementMarkers = []string{
	"骗取验证码", "骗取密码", "盗取账号", "诱导转账", "诈骗话术", "诈骗脚本",
	"冒充客服骗钱", "冒充公检法", "钓鱼网站", "钓鱼页面", "刷单返利诈骗",
	"裸聊诈骗", "杀猪盘", "赌博引流", "网赌引流", "赌博拉人", "招募赌客",
	"毒品引流", "毒品交易", "招募下线", "非法集资话术", "传销话术",
	"规避风控", "绕过审核", "躲避监管", "绕过反诈", "诱导未成年人",
	"phishingsite", "phishingpage", "stealverificationcode", "stealpassword",
	"inducetransfer", "scamscript", "gamblingpromotion", "drugtrafficking",
	"bypassmoderation", "evaderiskcontrol",
}

var gatewayRiskDefensiveQuestionSignals = []string{
	"如何识别", "怎么识别", "如何防范", "怎么防范", "如何举报", "怎么举报",
	"如何拦截", "怎么拦截", "如何审核", "怎么审核", "如何核实", "怎么核实",
	"如何核验", "怎么核验", "风险识别", "风险治理",
	"howtodetect", "howtoprevent", "howtoreport", "howtoblock", "howtomoderate",
}

var gatewayRiskBenignDeliverableSignals = []string{
	"反诈宣传", "反诈骗宣传", "安全科普", "性健康科普", "未成年人保护",
	"内容审核规则", "审核规则", "风控规则", "检测规则", "拦截规则",
	"举报材料", "法律分析", "合规分析", "新闻分析", "新闻核验", "历史研究",
	"危害分析", "风险治理方案", "警示教育", "辟谣材料",
	"fraudprevention", "safetyeducation", "contentmoderationrule",
	"riskcontrolrule", "complianceanalysis", "newsvetting", "historicalresearch",
}

var gatewayRiskClearlyMaliciousSignals = []string{
	"规避风控", "绕过审核", "躲避监管", "绕过反诈", "逃避检测",
	"骗钱", "非法获利", "招募下线", "招募赌客", "招募恐怖组织",
	"bypassmoderation", "evaderiskcontrol", "evadedetection", "stealmoney",
}

var gatewayRiskShortSlogans = []string{
	"暴力推翻政府", "武装推翻政府", "组织武装暴动", "煽动分裂国家",
	"宣传恐怖主义", "煽动民族仇恨",
}

func normalizeGatewayRiskText(text string) string {
	text = strings.ToLower(strings.TrimSpace(norm.NFKC.String(text)))
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) ||
			unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, text)
}

func gatewayRiskContainsAny(normalized string, candidates []string) bool {
	if normalized == "" {
		return false
	}
	for _, candidate := range candidates {
		if candidate = normalizeGatewayRiskText(candidate); candidate != "" &&
			strings.Contains(normalized, candidate) {
			return true
		}
	}
	return false
}

func gatewayRiskContainsAll(normalized string, groups ...[]string) bool {
	for _, group := range groups {
		if !gatewayRiskContainsAny(normalized, group) {
			return false
		}
	}
	return true
}

func gatewayRiskHasBenignContext(normalized string) bool {
	if gatewayRiskContainsAny(normalized, gatewayRiskClearlyMaliciousSignals) {
		return false
	}
	if gatewayRiskContainsAny(normalized, gatewayRiskBenignDeliverableSignals) {
		return true
	}
	if gatewayRiskContainsAny(normalized, gatewayRiskDefensiveQuestionSignals) {
		return true
	}
	analysisSignals := []string{"分析", "研究", "讨论", "核验", "评价", "解读", "analyze", "research", "discuss"}
	safetySubjects := []string{"危害", "影响", "历史", "法律", "治理", "案例", "新闻", "伦理", "风险", "harm", "impact", "history", "law", "governance", "news", "risk"}
	return gatewayRiskContainsAll(normalized, analysisSignals, safetySubjects)
}

func AssessUserContent(text string) Decision {
	normalized := normalizeGatewayRiskText(text)
	if normalized == "" {
		return Decision{}
	}
	benignContext := gatewayRiskHasBenignContext(normalized)
	hasRequest := gatewayRiskContainsAny(normalized, gatewayRiskRequestSignals)

	if gatewayRiskContainsAny(normalized, gatewayRiskMinorSexualMarkers) &&
		hasRequest && !benignContext {
		return Decision{
			Flagged: true, Category: CategorySexualContent,
			RuleID:        "sexual_minor_exploitation_request",
			PublicMessage: "该请求涉及平台不支持的未成年人性剥削或色情内容。可以改为未成年人保护、举报、内容审核或安全科普方向。",
		}
	}
	if gatewayRiskContainsAny(normalized, gatewayRiskSexualMarkers) &&
		hasRequest && !benignContext {
		return Decision{
			Flagged: true, Category: CategorySexualContent,
			RuleID:        "sexual_explicit_generation_or_access",
			PublicMessage: "该请求涉及平台不支持的色情露骨内容生成、获取或传播。可以改为性健康科普、风险识别或内容审核方向。",
		}
	}
	if gatewayRiskContainsAny(normalized, gatewayRiskPoliticalMarkers) &&
		hasRequest && !benignContext {
		return Decision{
			Flagged: true, Category: CategoryPoliticalExtremism,
			RuleID:        "political_extremism_enablement",
			PublicMessage: "该请求涉及平台不支持的暴力颠覆、分裂、仇恨煽动或极端主义宣传。可以改为新闻核验、历史研究、法律分析或风险治理方向。",
		}
	}
	if len([]rune(normalized)) <= 28 &&
		gatewayRiskContainsAny(normalized, gatewayRiskShortSlogans) &&
		!benignContext {
		return Decision{
			Flagged: true, Category: CategoryPoliticalExtremism,
			RuleID:        "political_extremism_short_slogan",
			PublicMessage: "该请求涉及平台不支持的暴力颠覆、分裂、仇恨煽动或极端主义宣传。可以改为新闻核验、历史研究、法律分析或风险治理方向。",
		}
	}
	if gatewayRiskContainsAny(normalized, gatewayRiskIllegalInducementMarkers) &&
		hasRequest && !benignContext {
		return Decision{
			Flagged: true, Category: CategoryIllegalInducement,
			RuleID:        "illegal_inducement_enablement",
			PublicMessage: "该请求涉及平台不支持的诈骗、诱导转账、赌博或毒品引流、规避审核等行为。可以改为反诈、风控、举报或合规治理方向。",
		}
	}
	return Decision{}
}
