package app

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"mac/internal/agentloop"
	"mac/internal/permissionpolicy"
	"mac/internal/projectctx"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mac/internal/portablepath"
	securitycore "mac/internal/security"
	"mac/internal/textutil"
	"mac/internal/toolregistry"
)

// presentationProductionState is deliberately turn-local. An LLMAgent can be
// reused by unrelated tasks, so presentation routing and QA evidence must never
// leak through the global invoked-skill cache or an agent-wide boolean.
type presentationProductionState struct {
	mu sync.Mutex

	Expected                      bool
	IntakeRequired                bool
	StrictQA                      bool
	PerSlideVisionRequired        bool
	PerSlideCustomizationRequired bool
	ArtDirectorActivated          bool
	ActivatedSkill                string
	RuntimeDir                    string
	Route                         string
	BasePPTHandoffAllowed         bool
	TaskSpecificBuilderRequired   bool
	PPTMasterAllowed              bool
	Level2BuildFailures           int
	PPTMasterBuildFailures        int
	PPTMasterUnavailable          bool
	Substantive                   bool
	SourceContext                 string
	SourceAuthorizationContext    string
	ReferenceRequired             bool
	ReferencePath                 string
	ReferenceRendered             bool
	ReferenceRenderTarget         string
	ReferenceSlideCount           int
	ReferenceInspectedImages      map[string]bool
	ReferenceVisualInspected      bool
	Scaffolded                    bool
	BuilderRead                   bool
	BuilderCustomized             bool
	BuilderPath                   string
	DeckCreated                   bool
	DeckPath                      string
	RequestedMinimumSlides        int
	RequestedMaximumSlides        int
	DeckSlideCount                int
	AuditPassed                   bool
	AuditEvidence                 string
	Rendered                      bool
	RenderEvidence                string
	RenderTarget                  string
	RenderedSlideCount            int
	VisualInspectedImages         map[string]bool
	FinalVisualInspected          bool
	VisualCoverageComplete        bool
	AestheticQAPassed             bool
	AestheticQAEvidence           string
	PerSlideVisionOptOut          bool
	VisualEvidence                string
	VisualDefectsDetected         bool
	VisualDefectEvidence          string
	NativeQAAttempted             bool
	SelfAuditFailed               bool
	SelfAuditEvidence             string
	StartedAt                     time.Time
	CandidatePaths                map[string]struct{}
	PPTPolishContract             bool
	ContractDeckPath              string
	ContractWorkDir               string
	ContractPreviewDir            string
	BoundedRevision               bool
	RevisionMode                  string
	RevisionBuildLimit            int
	RevisionBuildCount            int
	RevisionMutationCount         int
	RequiredBodyFontSize          float64
	RequiredTitleFontSize         float64
	SourceDirty                   bool
	AuditFailed                   bool
	AuditFailureMutationCount     int
	PPTPolishExecution            *PipelineExecution
	PPTPolishRequest              string
}

type presentationProductionStateContextKey struct{}

var (
	presentationFilePattern             = regexp.MustCompile(`(?i)(?:"[^"\r\n]*?\.pptx"|'[^'\r\n]*?\.pptx'|(?:[a-z]:[\\/])?[^"'\s\r\n,;<>，。；：]+(?:[\\/][^"'\s\r\n,;<>，。；：]+)*\.pptx\b)`)
	presentationImagePattern            = regexp.MustCompile(`(?i)(?:"[^"\r\n]*?\.(?:png|jpe?g|webp)"|'[^'\r\n]*?\.(?:png|jpe?g|webp)'|(?:[a-z]:[\\/])?[^"'\s\r\n,;<>，。；：]+(?:[\\/][^"'\s\r\n,;<>，。；：]+)*\.(?:png|jpe?g|webp)\b)`)
	presentationRenderOutputFlagPattern = regexp.MustCompile(`(?i)(?:-outputdir|--outdir|--output-dir|--output|output_dir[=:]|outdir[=:])\s*(?:"([^"]+)"|'([^']+)'|([^\s,;}]+))`)
	presentationBuilderFlagPattern      = regexp.MustCompile(`(?i)--builder\s*(?:"([^"]+)"|'([^']+)'|([^\s,;}]+))`)
	presentationSpecInputFlagPattern    = regexp.MustCompile(`(?i)--spec\s*(?:"([^"]+)"|'([^']+)'|([^\s,;}]+))`)
	presentationJSONSlideCountPattern   = regexp.MustCompile(`(?i)"(?:slide_count|rendered_slides|page_count)"\s*:\s*(\d+)`)
	presentationInspectedCountPattern   = regexp.MustCompile(`(?i)"(?:slides_inspected|pages_inspected|inspected_slide_count)"\s*:\s*(\d+)`)
	presentationTextSlideCountPattern   = regexp.MustCompile(`(?i)(?:rendered|exported|generated|converted|inspected|checked)\s+(\d+)\s+(?:slides?|pages?)|(?:共|已渲染|已导出|已检查)?\s*(\d+)\s*(?:张|页)(?:幻灯片|页面|预览图)`)
	presentationNegatedDefectPattern    = regexp.MustCompile(`(?i)(?:(?:未发现|没有发现|未检测到|没有检测到|不存在|无明显|无)(?:任何)?\s*(?:裁切|重叠|遮挡|溢出|乱码|空白页|占位符)(?:问题)?|(?:no|without)\s+(?:clipping|overlap|occlusion|overflow|garbled text|blank pages?|placeholders?)(?:\s+(?:was\s+)?(?:found|detected))?)`)
	presentationPositiveDefectPattern   = regexp.MustCompile(`(?i)(?:(?:发现|存在|检测到|出现).{0,32}(?:裁切|重叠|遮挡|溢出|乱码|空白页|占位符)|(?:found|detected|observed|unresolved).{0,64}(?:clipping|overlap|occlusion|overflow|garbled text|blank pages?|placeholders?))`)
	presentationStrictRenderQAPattern   = regexp.MustCompile(`(?i)(?:(?:逐页|每一页|每页|全页|全部页面|所有页面).{0,16}(?:渲染|视觉|像素|vision).{0,32}(?:检查|验收|核验|质检|qa)|(?:render|inspect|review|validate).{0,24}(?:every|all).{0,12}(?:slide|page))`)
	presentationRequestedPagePattern    = regexp.MustCompile(`(?i)(\d{1,3})\s*(?:页(?:\s*(?:pptx?|幻灯片|演示文稿|deck))?|slides?|pages?)`)
	presentationMainAppendixPattern     = regexp.MustCompile(`(?i)(\d{1,3})\s*页?.{0,24}(?:允许|可(?:有|加|含)?|最多|另有|外加|plus|with|allow(?:ed)?).{0,16}(\d{1,3})\s*页?\s*(?:附录|appendix)`)
	presentationPageRangePattern        = regexp.MustCompile(`(?i)(\d{1,3})\s*(?:[-–—~～至到]|to)\s*(\d{1,3})\s*(?:页|slides?|pages?)`)
	presentationPageMaximumPattern      = regexp.MustCompile(`(?i)(?:最多|至多|不超过|不多于|上限|maximum|max(?:imum)?(?:\s+of)?|no\s+more\s+than|up\s+to)\D{0,12}(\d{1,3})\s*(?:页|slides?|pages?)|(?:(\d{1,3})\s*(?:页|slides?|pages?))\s*(?:以内|以下|封顶|or\s+fewer|at\s+most)`)
	presentationPageMinimumOnlyPattern  = regexp.MustCompile(`(?i)(?:至少|不少于|不低于|minimum|min(?:imum)?(?:\s+of)?|at\s+least)\D{0,12}\d{1,3}\s*(?:页|slides?|pages?)`)
	presentationExitCodePattern         = regexp.MustCompile(`(?i)\b(?:exit|build_exit)\s*=\s*(\d+)\b`)
	presentationContractVisionPattern   = regexp.MustCompile(`(?i)^(?:contact_sheet\.jpg|slide_\d{2}\.png)$`)
	presentationEvidenceIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	presentationTechnicalTokenPattern   = regexp.MustCompile(`\b[A-Z][A-Z0-9+.-]{1,15}\b`)
	pythonInlinePattern                 = regexp.MustCompile(`(?i)(?:^|\s)(?:python|py)(?:\.exe)?(?:\s+[^\r\n]*?)?\s-c(?:\s|$)`)
	emptyPresentationCtor               = regexp.MustCompile(`(?i)\bpresentation\s*\(\s*\)`)
	presentationDirectGeometryCall      = regexp.MustCompile(`(?i)\.shapes\.(add_shape|add_textbox|add_picture|add_connector|add_table|add_chart|add_movie|add_ole_object)\s*\(`)
	presentationExplicitEMUConversion   = regexp.MustCompile(`(?i)^(?:inches|emu|cm|mm|_i|int|to_emu|as_emu)\s*\(`)
	presentationProductionNegation      = regexp.MustCompile(`(?i)(?:(?:不要|无需|不需要|不用|不必|禁止|不得)(?:再|真的|实际|现在|本次|在本次)?(?:帮我|替我|给我)?(?:生成|制作|创建|做|导出|转换|修改|编辑|重做|美化)(?:一份|一个|这个|该|真实的|真正的)?\s*(?:pptx?|powerpoint|幻灯片|演示文稿|路演稿|deck)|(?:do\s+not|don't|without)\s+(?:actually\s+)?(?:generate|create|build|make|produce|export|edit|revise)\s+(?:a\s+|the\s+|this\s+)?(?:pptx?|powerpoint|presentation|deck))`)
	presentationReferenceNegation       = regexp.MustCompile(`(?i)(?:(?:不能|不要|不可|不得|不应|禁止|无需|不需要)(?:再|拿|用|使用|把|将|以)?\s*(?:任何|这个|该|旧的|之前的)?\s*(?:参考(?:稿|文件|deck)?|模板|风格|reference|template)(?:\s*(?:deck|pptx?|powerpoint|演示文稿))?|(?:do\s+not|don't|without)\s+(?:use|take|treat)?\s*(?:the\s+|a\s+|any\s+)?(?:reference|template|reference\s+deck))`)
)

func withPresentationProductionState(ctx context.Context, input string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if presentationProductionStateFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, presentationProductionStateContextKey{}, newPresentationProductionState(ctx, input))
}

func newPresentationProductionState(ctx context.Context, input string) *presentationProductionState {
	historyPath := recentPresentationHistoryPath(ctx)
	followup := historyPath != "" && isPresentationFollowupRequest(input)
	baseCreationRequested := isPresentationCreationRequest(input) || (followup && isPresentationModificationFollowup(input))
	existingDeckQA := isExistingPresentationQARequest(input) || (followup && !baseCreationRequested)
	creationRequested := baseCreationRequested && !existingDeckQA
	intakeRequired := creationRequested && presentationExplicitIntakeFirst(input)
	if !creationRequested && presentationIntakePendingFromHistory(ctx) && presentationIntakeAnswer(input) {
		creationRequested = true
	}
	sourceContext := presentationEvidenceContext(ctx, input)
	basePPTHandoffAllowed := presentationAllowsBasePPTHandoff(input)
	substantive := (creationRequested || existingDeckQA) && !basePPTHandoffAllowed
	perSlideCustomizationRequired := creationRequested && substantive
	taskSpecificBuilderRequired := presentationRequiresTaskSpecificBuilder(input)
	strictQA := isStrictPresentationQARequest(input) ||
		(substantive && !presentationExplicitlyDisablesStrictQA(input))
	perSlideVisionOptOut := presentationExplicitlyDisablesPerSlideVision(input)
	pptMasterAllowed := presentationAllowsPPTMaster(sourceContext)
	route := "level2-task-builder"
	if creationRequested && substantive && pptMasterAllowed && !taskSpecificBuilderRequired {
		route = "level3-ppt-master"
	}
	state := &presentationProductionState{
		Expected:                      (creationRequested || existingDeckQA) && !intakeRequired,
		IntakeRequired:                intakeRequired,
		StrictQA:                      strictQA,
		PerSlideVisionRequired:        presentationRequiresPerSlideVision(input),
		PerSlideCustomizationRequired: perSlideCustomizationRequired,
		Route:                         route,
		BasePPTHandoffAllowed:         basePPTHandoffAllowed,
		TaskSpecificBuilderRequired:   taskSpecificBuilderRequired,
		PPTMasterAllowed:              pptMasterAllowed,
		Substantive:                   substantive,
		SourceContext:                 sourceContext,
		SourceAuthorizationContext:    sourceContext,
		RequestedMinimumSlides:        requestedPresentationMinimumSlides(input),
		RequestedMaximumSlides:        requestedPresentationMaximumSlides(input),
		ReferenceRequired:             presentationReferenceRequested(input),
		ReferenceInspectedImages:      make(map[string]bool),
		VisualInspectedImages:         make(map[string]bool),
		PerSlideVisionOptOut:          perSlideVisionOptOut,
		StartedAt:                     time.Now(),
		CandidatePaths:                make(map[string]struct{}),
	}
	state.RequiredBodyFontSize, state.RequiredTitleFontSize = presentationRequestedAuditFontFloors(input)
	if substantive {
		// These are production defaults, not optional style suggestions. Compact
		// labels and source footers are excluded by audit_pptx.py, while actual
		// audience copy and slide titles must remain readable in slideshow view.
		if state.RequiredBodyFontSize <= 0 {
			state.RequiredBodyFontSize = 16
		}
		if state.RequiredTitleFontSize <= 0 {
			state.RequiredTitleFontSize = 35
		}
	}
	if state.ReferenceRequired {
		state.ReferencePath = presentationReferencePath(input)
	}
	if existingDeckQA {
		state.Route = "qa-existing-deck"
		state.DeckCreated = true
		state.DeckPath = existingPresentationPath(input)
		if state.DeckPath == "" {
			state.DeckPath = historyPath
		}
	}
	return state
}

func presentationRequestedAuditFontFloors(input string) (body, title float64) {
	args := pptxAuditArgumentsForRequest(input)
	for index := 0; index+1 < len(args); index += 2 {
		value, err := strconv.ParseFloat(strings.TrimSpace(args[index+1]), 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(args[index]) {
		case "--min-body-font-size":
			body = value
		case "--min-title-font-size":
			title = value
		}
	}
	return body, title
}

func presentationExplicitIntakeFirst(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if !presentationContainsAny(lower,
		"先确认", "先问", "先询问", "确认后再", "确认清楚再", "确认完再",
		"ask me first", "confirm first", "before you start", "before creating",
	) {
		return false
	}
	dimensions := 0
	for _, markers := range [][]string{
		{"受众", "听众", "汇报对象", "audience"},
		{"用途", "场景", "会议", "目的", "use", "purpose"},
		{"核心观点", "核心结论", "主张", "观点", "key message", "core message", "thesis"},
	} {
		if presentationContainsAny(lower, markers...) {
			dimensions++
		}
	}
	return dimensions >= 2
}

func presentationIntakePendingFromHistory(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	history, _ := ctx.Value("history_messages").([]map[string]interface{})
	if len(history) == 0 {
		return false
	}
	start := len(history) - 8
	if start < 0 {
		start = 0
	}
	assistantAsked := false
	for i := len(history) - 1; i >= start; i-- {
		role, _ := history[i]["role"].(string)
		text := presentationHistoryMessageText(history[i]["content"])
		if strings.EqualFold(strings.TrimSpace(role), "assistant") && !assistantAsked {
			lower := strings.ToLower(text)
			assistantAsked = presentationContainsAny(lower, "受众", "audience") &&
				presentationContainsAny(lower, "用途", "目的", "场景", "purpose", "use") &&
				presentationContainsAny(lower, "核心观点", "核心结论", "key message", "core message", "thesis")
			continue
		}
		if strings.EqualFold(strings.TrimSpace(role), "user") && assistantAsked {
			return isPresentationCreationRequest(text) && presentationExplicitIntakeFirst(text)
		}
	}
	return false
}

func presentationIntakeAnswer(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if len([]rune(lower)) < 2 {
		return false
	}
	return !presentationContainsAny(lower,
		"取消", "不用做", "先不做", "不要做", "停止", "算了",
		"cancel", "stop", "never mind", "do not create",
	)
}

func presentationProductionStateFromContext(ctx context.Context) *presentationProductionState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(presentationProductionStateContextKey{}).(*presentationProductionState)
	return state
}

func isPresentationCreationRequest(input string) bool {
	lower := strings.ToLower(input)
	if !presentationContainsAny(lower, "ppt", "pptx", "powerpoint", "幻灯片", "演示文稿", "路演稿", "deck") {
		return false
	}
	// A current-turn user boundary such as "only give options; do not start
	// producing" overrides presentation nouns and production verbs elsewhere
	// in the request. Later turns are classified independently, so an explicit
	// "start now" follow-up enables production without carrying stale state.
	if permissionpolicy.DeriveUserToolPolicy(input).ForbidFileMutations {
		return false
	}
	if presentationProductionNegation.MatchString(lower) {
		return false
	}
	action := presentationContainsAny(lower,
		"生成", "制作", "创建", "做一个", "做一份", "做份", "做成", "继续做", "接着做", "整理成", "整理为", "你做", "重做", "重制", "改造", "润色", "美化", "修改", "编辑", "设计",
		"帮我做", "请做", "给我做", "替我做", "转成", "转换", "导出",
		"generate", "create", "build", "make", "continue making", "continue building", "produce", "redesign", "polish", "edit", "revise", "convert", "export",
	)
	if !action {
		return false
	}
	// Questions, tutorials and failure diagnosis may mention an action such as
	// “制作 PPT” without asking this turn to create or modify a deck.
	if presentationContainsAny(lower, "怎么做", "如何做", "怎么制作", "如何制作", "制作方法", "制作教程", "是否支持", "是否能", "能否", "可否", "会不会做", "能不能生成", "有没有能力", "能力是否", "can you", "are you able", "do you support", "原理", "报错", "错误日志", "失败原因", "算bug", "算 bug") &&
		!presentationContainsAny(lower, "帮我", "请生成", "请制作", "请创建", "给我", "替我", "直接生成", "重新生成", "重做", "修复后生成", "修复并生成") {
		return false
	}
	return true
}

func isPresentationFollowupRequest(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	return presentationContainsAny(lower,
		"继续", "接着", "上一份", "上一个", "刚才", "之前的", "那个ppt", "那个 ppt", "再改", "修改一下", "再验收", "验收一下",
		"换成", "改成", "风格", "配色", "颜色", "深色", "浅色", "黑底", "白底", "金线",
		"交付", "发布", "发送", "发出去", "resume", "continue", "previous deck", "last deck",
		"change the style", "restyle", "color palette", "dark theme", "light theme",
	)
}

func isPresentationModificationFollowup(input string) bool {
	lower := strings.ToLower(input)
	return presentationContainsAny(lower,
		"修改", "再改", "重做", "重制", "润色", "美化", "优化", "调整", "修复", "继续做", "接着做",
		"换成", "改成", "风格", "配色", "颜色", "深色", "浅色", "黑底", "白底", "金线", "商务", "科技感", "设计感",
		"edit", "revise", "redesign", "polish", "fix", "continue building", "change the style", "restyle", "color palette", "dark theme", "light theme",
	)
}

func recentPresentationHistoryPath(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	history, _ := ctx.Value("history_messages").([]map[string]interface{})
	checked := 0
	for i := len(history) - 1; i >= 0 && checked < 12; i-- {
		checked++
		if path := firstPresentationPath(presentationHistoryMessageText(history[i]["content"])); path != "" {
			return path
		}
	}
	return ""
}

func presentationHistoryMessageText(content interface{}) string {
	switch value := content.(type) {
	case string:
		return value
	case []map[string]interface{}:
		var parts []string
		for _, item := range value {
			if text, _ := item["text"].(string); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case []interface{}:
		var parts []string
		for _, raw := range value {
			item, _ := raw.(map[string]interface{})
			if text, _ := item["text"].(string); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

// presentationEvidenceContext keeps only recent user-authored instructions.
// A short follow-up such as "start writing" must inherit the truth boundary
// established earlier in the conversation (for example, that customer counts
// are not yet measured), without treating prior assistant prose as evidence.
func presentationEvidenceContext(ctx context.Context, input string) string {
	parts := []string{strings.TrimSpace(input)}
	if ctx == nil {
		return strings.TrimSpace(input)
	}
	history, _ := ctx.Value("history_messages").([]map[string]interface{})
	checked := 0
	for index := len(history) - 1; index >= 0 && checked < 32; index-- {
		role := strings.ToLower(strings.TrimSpace(fmt.Sprint(history[index]["role"])))
		if role != "user" {
			continue
		}
		checked++
		text := strings.TrimSpace(presentationHistoryMessageText(history[index]["content"]))
		if text == "" || text == strings.TrimSpace(input) {
			continue
		}
		if len(text) > 12000 {
			text = text[:12000]
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n")
}

func isExistingPresentationQARequest(input string) bool {
	if !presentationFilePattern.MatchString(input) {
		return false
	}
	lower := strings.ToLower(input)
	qaOrEdit := presentationContainsAny(lower,
		"验收", "检查", "审计", "审核", "验证", "质检", "视觉检查", "交付", "发布", "发送", "发到", "同步到", "继续上次",
		"修改", "修复", "调整", "润色", "美化", "最小修复",
		"validate", "inspect", "audit", "review", "quality check", "deliver", "publish", "send", "resume", "edit", "fix", "repair",
	)
	if !qaOrEdit {
		return false
	}
	if !isPresentationCreationRequest(input) {
		return true
	}
	// A request to inspect or minimally repair an existing deck remains an
	// existing-artifact workflow even though words such as "修复" also look like
	// production verbs. Only an explicit from-scratch/rebuild instruction should
	// move it into the new-deck Level-2 route.
	if presentationContainsAny(lower,
		"从零重做", "彻底重做", "重新制作一份", "重新生成一份", "全新制作", "另做一份", "重建整份",
		"rebuild from scratch", "create a new deck", "make a new presentation", "full redesign",
	) {
		return false
	}
	explicitExistingTarget := presentationContainsAny(lower,
		"既有", "已有", "现有", "原稿", "原文件", "源文件", "随任务提供", "这份ppt", "这份 ppt", "当前ppt", "当前 ppt",
		"不要直接重做", "不要重做", "保留原内容", "最小修复", "修复前后",
		"existing", "current deck", "source deck", "original deck", "minimal repair",
	)
	if explicitExistingTarget {
		return true
	}
	// A new-deck brief commonly says to audit the generated deck and repair any
	// clipping or overlap. Those downstream QA clauses do not turn the requested
	// output into an already-existing presentation. Treat explicit create/make
	// language as authoritative unless the user also identified an existing
	// source deck above.
	if presentationContainsAny(lower,
		"生成", "制作", "创建", "做一个", "做一份", "做份", "做成", "整理成", "整理为",
		"generate", "create", "build", "make", "produce",
	) {
		return false
	}
	return presentationContainsAny(lower,
		"修复", "修改", "调整", "润色", "美化",
		"repair", "edit", "fix", "revise", "polish",
	)
}

func presentationExplicitlyDisablesStrictQA(input string) bool {
	lower := strings.ToLower(input)
	return presentationContainsAny(lower,
		"不要严格验收", "无需严格验收", "不需要严格验收", "不用严格验收", "不要求逐页", "不用逐页", "不要求像素级", "无需像素级", "非严格验收",
		"no strict qa", "skip strict qa", "strict qa not required", "without strict validation", "skip strict validation",
	)
}

func isStrictPresentationQARequest(input string) bool {
	lower := strings.ToLower(input)
	if presentationExplicitlyDisablesStrictQA(input) {
		return false
	}
	return presentationStrictRenderQAPattern.MatchString(lower) || presentationContainsAny(lower,
		"严格验收", "强制验收", "必须通过验收", "逐页验收", "逐页视觉检查", "逐页视觉 qa", "逐页调用 vision", "每一页分别调用 vision", "每页分别调用 vision", "像素级验收",
		"全页验收", "全页检查", "全量渲染", "必须渲染全部", "渲染全部幻灯片", "渲染全部页面", "包含全部页面的联系表",
		"确保无重叠", "确保无裁切", "必须无重叠", "必须无裁切", "发现裁切", "发现重叠",
		"strict qa", "strict validation", "pixel-perfect", "must pass qa",
	)
}

func presentationExplicitlyDisablesPerSlideVision(input string) bool {
	lower := strings.ToLower(input)
	return presentationContainsAny(lower,
		"不要求逐页", "不用逐页", "无需逐页", "不需要逐页",
		"per-slide vision not required", "without per-slide vision",
	)
}

func presentationRequiresPerSlideVision(input string) bool {
	lower := strings.ToLower(input)
	if presentationExplicitlyDisablesPerSlideVision(input) {
		return false
	}
	return presentationContainsAny(lower,
		"逐页调用 vision", "逐页分别调用 vision", "每一页分别调用 vision", "每页分别调用 vision",
		"每张分别调用 vision", "逐张调用 vision", "one vision call per slide", "vision on each slide",
		"call vision separately for each slide", "call vision on every slide",
	) || (strings.Contains(lower, "slide_01") && strings.Contains(lower, "vision"))
}

func requestedPresentationMinimumSlides(input string) int {
	if match := presentationMainAppendixPattern.FindStringSubmatch(input); len(match) == 3 {
		if count, err := strconv.Atoi(match[1]); err == nil && count > 0 {
			return count
		}
	}
	if match := presentationPageRangePattern.FindStringSubmatch(input); len(match) == 3 {
		left, leftErr := strconv.Atoi(match[1])
		right, rightErr := strconv.Atoi(match[2])
		if leftErr == nil && rightErr == nil && left > 0 && right > 0 {
			return min(left, right)
		}
	}
	if presentationPageMaximumPattern.MatchString(input) && !presentationPageMinimumOnlyPattern.MatchString(input) {
		return 0
	}
	minimum := 0
	for _, match := range presentationRequestedPagePattern.FindAllStringSubmatch(input, -1) {
		if len(match) < 2 {
			continue
		}
		count, err := strconv.Atoi(match[1])
		if err == nil && count > minimum {
			minimum = count
		}
	}
	return minimum
}

func requestedPresentationMaximumSlides(input string) int {
	if match := presentationMainAppendixPattern.FindStringSubmatch(input); len(match) == 3 {
		mainPages, mainErr := strconv.Atoi(match[1])
		appendixPages, appendixErr := strconv.Atoi(match[2])
		if mainErr == nil && appendixErr == nil && mainPages > 0 && appendixPages >= 0 {
			return mainPages + appendixPages
		}
	}
	if match := presentationPageRangePattern.FindStringSubmatch(input); len(match) == 3 {
		left, leftErr := strconv.Atoi(match[1])
		right, rightErr := strconv.Atoi(match[2])
		if leftErr == nil && rightErr == nil && left > 0 && right > 0 {
			return max(left, right)
		}
	}
	if match := presentationPageMaximumPattern.FindStringSubmatch(input); len(match) >= 3 {
		for _, raw := range match[1:] {
			if count, err := strconv.Atoi(raw); err == nil && count > 0 {
				return count
			}
		}
	}
	pageMentions := presentationRequestedPagePattern.FindAllStringSubmatch(input, -1)
	if len(pageMentions) == 1 && !presentationPageMinimumOnlyPattern.MatchString(input) {
		if count, err := strconv.Atoi(pageMentions[0][1]); err == nil && count > 0 {
			return count
		}
	}
	return 0
}

func presentationAllowsBasePPTHandoff(input string) bool {
	lower := strings.ToLower(input)
	if presentationContainsAny(lower, "base-ppt", "base ppt", "generator: base-ppt", "generator=base-ppt") {
		return true
	}
	if presentationContainsAny(lower,
		"漂亮", "精美", "美观", "好看", "设计感", "美感", "高级", "大气", "专业设计",
		"对外", "正式汇报", "客户", "路演", "发布会", "品牌", "董事会", "管理层", "投资人",
		"beautiful", "polished", "highly polished", "visually polished", "aesthetic", "design-led", "premium", "showcase",
		"client-facing", "customer-facing", "external-facing", "executive", "board", "investor", "investor-facing", "pitch deck",
	) {
		return false
	}
	speedOrDraft := presentationContainsAny(lower,
		"快速", "尽快", "粗糙草稿", "占位", "设计不重要", "不用美化", "无需美化",
		"quick", "quickly", "fast", "rough draft", "placeholder", "placeholder filling",
		"design quality is unimportant", "design does not matter", "no need to polish", "no visual polish",
	)
	internalOrFixed := presentationContainsAny(lower,
		"内部", "固定结构", "固定格式", "固定模板", "固定栏目", "重复生成", "周期性", "周报", "日报", "月报", "模板填充",
		"internal", "internal-only", "fixed schema", "fixed structure", "fixed format", "fixed template", "template filling",
		"recurring", "recurring report", "weekly report", "daily report", "monthly report",
	)
	return speedOrDraft || internalOrFixed
}

func presentationRequiresTaskSpecificBuilder(input string) bool {
	lower := strings.ToLower(input)
	return presentationContainsAny(lower,
		"必须走任务专属构建器", "必须使用任务专属构建器", "必须走任务专用构建器", "必须使用任务专用构建器",
		"只能走任务专属构建器", "只能使用任务专属构建器", "不得降级", "禁止降级",
		"must use the task-specific builder", "must use a task-specific builder", "task-specific builder only", "no fallback",
	)
}

func presentationAllowsPPTMaster(input string) bool {
	lower := strings.ToLower(input)
	return presentationContainsAny(lower,
		"ppt-master", "pptmaster", "高端", "高级定制", "大气", "豪华", "电影感", "发布会", "品牌发布",
		"漂亮", "精美", "美观", "好看", "设计感", "美感", "专业设计",
		"董事会", "投资人", "融资路演", "融资bp", "融资 bp", "商业计划书", "募资材料", "客户提案", "销售提案", "对外路演", "作品展示",
		"beautiful", "polished", "aesthetic", "design-led", "premium", "luxurious", "cinematic", "showcase", "launch deck", "brand deck", "board deck",
		"investor deck", "investor-facing", "pitch book", "business plan", "client pitch", "sales deck", "executive showcase",
	)
}

// presentationDisasterFallbackReady keeps base-ppt as a genuine last resort.
// Explicit fast/internal/fixed-schema requests may choose it directly. For a
// substantive deck, two real production attempts on the editable task route
// must fail first; when a premium route is suitable, that route must also be
// objectively unavailable or fail its final export twice. Command guesses,
// probes, source writes, and validation snippets do not consume this budget.
func presentationDisasterFallbackReady(
	explicitBaseAllowed bool,
	taskSpecificBuilderRequired bool,
	level2BuildFailures int,
	pptMasterAllowed bool,
	pptMasterBuildFailures int,
	pptMasterUnavailable bool,
) bool {
	if explicitBaseAllowed {
		return true
	}
	if taskSpecificBuilderRequired || level2BuildFailures < 2 {
		return false
	}
	if !pptMasterAllowed {
		return true
	}
	return pptMasterUnavailable || pptMasterBuildFailures >= 2
}

func presentationReferenceRequested(input string) bool {
	_, ok := presentationReferenceCandidate(input)
	return ok
}

func presentationReferencePath(input string) string {
	path, _ := presentationReferenceCandidate(input)
	return path
}

func presentationReferenceCandidate(input string) (string, bool) {
	input = presentationReferenceNegation.ReplaceAllString(input, " ")
	matches := presentationFilePattern.FindAllStringIndex(input, -1)
	if len(matches) == 0 {
		return "", false
	}
	clean := func(match []int) string {
		return strings.Trim(strings.TrimSpace(input[match[0]:match[1]]), "\"'`.,;，。；）)]}")
	}
	referenceBefore := []string{"参考", "借鉴", "按照", "照着", "沿用", "继承", "作为模板", "用作模板", "风格参考", "版式参考", "reference", "template", "based on"}
	referenceAfter := []string{"作为参考", "用作参考", "做参考", "是参考稿", "为参考稿", "作为模板", "用作模板", "是模板", "为模板"}
	styleAfter := []string{"的风格", "的版式", "style reference", "as a style", "as the template"}
	outputBefore := []string{"输出", "保存", "生成", "制作", "创建", "产出", "交付", "导出", "写入", "文件名", "命名为", "另存为", "output", "save", "generate", "create", "build", "export", "filename", "file name"}
	bestIndex, bestScore := 0, -1<<30
	for index, match := range matches {
		before := strings.ToLower(input[:match[0]])
		after := strings.ToLower(input[match[1]:])
		score := 0
		for _, marker := range referenceBefore {
			if position := strings.LastIndex(before, marker); position >= 0 {
				distance := len(before) - position - len(marker)
				if distance <= 160 {
					score += 1200 - distance*4
				}
			}
		}
		for _, marker := range referenceAfter {
			if position := strings.Index(after, marker); position >= 0 && position <= 32 {
				score += 1100 - position*4
			}
		}
		for _, marker := range styleAfter {
			if position := strings.Index(after, marker); position >= 0 && position <= 24 {
				score += 800 - position*4
			}
		}
		for _, marker := range outputBefore {
			if position := strings.LastIndex(before, marker); position >= 0 {
				distance := len(before) - position - len(marker)
				if distance <= 120 {
					score -= 1200 - distance*3
				}
			}
		}
		if score > bestScore {
			bestIndex, bestScore = index, score
		}
	}
	if bestScore < 450 {
		return "", false
	}
	return clean(matches[bestIndex]), true
}

func existingPresentationPath(input string) string {
	matches := presentationFilePattern.FindAllStringIndex(input, -1)
	if len(matches) == 0 {
		return ""
	}
	clean := func(match []int) string {
		return strings.Trim(strings.TrimSpace(input[match[0]:match[1]]), "\"'`.,;，。；）)]}")
	}
	if len(matches) == 1 {
		return clean(matches[0])
	}
	inputMarkers := []string{"检查", "验收", "修复", "修改", "既有", "已有", "现有", "原稿", "原文件", "源文件", "输入", "随任务提供", "测试夹具", "inspect", "audit", "repair", "edit", "source", "input"}
	outputMarkers := []string{"输出", "保存", "生成", "制作", "创建", "产出", "交付", "导出", "写入", "output", "save", "generate", "create", "build", "export"}
	bestIndex, bestScore := 0, -1<<30
	for index, match := range matches {
		path := strings.ToLower(filepath.ToSlash(clean(match)))
		before := strings.ToLower(input[:match[0]])
		score := 0
		if strings.Contains(path, "fixtures/") || strings.Contains(path, "/repo/") {
			score += 1400
		}
		for _, marker := range inputMarkers {
			if position := strings.LastIndex(before, marker); position >= 0 {
				distance := len(before) - position - len(marker)
				if distance <= 140 {
					score += 1000 - distance*4
				}
			}
		}
		for _, marker := range outputMarkers {
			if position := strings.LastIndex(before, marker); position >= 0 {
				distance := len(before) - position - len(marker)
				if distance <= 100 {
					score -= 900 - distance*4
				}
			}
		}
		if score > bestScore {
			bestIndex, bestScore = index, score
		}
	}
	return clean(matches[bestIndex])
}

func presentationContainsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func presentationToolPayload(toolCall ToolCall) string {
	if toolCall.Arguments == nil {
		return ""
	}
	data, err := json.Marshal(toolCall.Arguments)
	if err != nil {
		return fmt.Sprintf("%v", toolCall.Arguments)
	}
	return string(data)
}

func normalizedPresentationSkillName(toolCall ToolCall) string {
	if toolCall.Name != "use_skill" {
		return ""
	}
	name, _ := toolCall.Arguments["skill_name"].(string)
	return strings.ToLower(strings.TrimSpace(name))
}

func mentionsPresentationScaffoldAction(payload string) bool {
	return strings.Contains(strings.ToLower(payload), "scaffold_task_pptx.py")
}

func isPresentationScaffoldAction(toolCall ToolCall, payload string) bool {
	lower := strings.ToLower(payload)
	if !mentionsPresentationScaffoldAction(lower) {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	if !presentationContainsAny(name, "cli", "persistent_shell", "shell_analyze", "shell", "command") {
		return false
	}
	normalized := strings.ReplaceAll(lower, `\`, "/")
	if strings.Contains(normalized, ".openkitty/storage/scaffold_task_pptx.py") {
		return false
	}
	return strings.Contains(lower, "--builder") && strings.Contains(lower, "--output")
}

func isCounterfeitPresentationScaffoldAction(toolCall ToolCall, payload string) bool {
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	if presentationContainsAny(name, "write_file", "edit_file", "multiedit") {
		// File mutation payloads contain both the target path and the source text.
		// A legitimate generated builder may retain a comment saying it came from
		// scaffold_task_pptx.py; scanning the entire payload turns that harmless
		// provenance note into a false counterfeit-scaffold violation.  For file
		// tools, only the file being mutated determines whether the action is an
		// attempt to create a workspace-local scaffold.
		target := filepath.ToSlash(strings.ToLower(strings.TrimSpace(firstStringArg(
			toolCall.Arguments, "path", "file_path", "file"))))
		return strings.EqualFold(filepath.Base(target), "scaffold_task_pptx.py")
	}
	if !mentionsPresentationScaffoldAction(payload) || isPresentationScaffoldAction(toolCall, payload) {
		return false
	}
	return presentationContainsAny(name,
		"cli", "persistent_shell", "shell_analyze", "shell", "command", "ptc_execute",
	)
}

func isCounterfeitPresentationQAHelperAction(toolCall ToolCall, payload string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.ReplaceAll(payload, `\`, "/")))
	if isPPTPolishCanonicalAuditAction(lower) || isPPTPolishCanonicalRenderAction(lower) || isTrustedLocalPresentationVisualQA(toolCall) {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	if presentationContainsAny(name, "write_file", "edit_file", "multiedit", "multi_edit", "json_patch") {
		base := strings.ToLower(filepath.Base(filepath.FromSlash(presentationMutationPath(toolCall))))
		suspiciousName := presentationContainsAny(base, "render", "raster", "preview", "thumbnail", "contact_sheet", "audit", "probe") &&
			presentationContainsAny(base, ".py", ".js", ".mjs", ".ts", ".ps1")
		suspiciousBody := presentationContainsAny(lower, "pptx", "powerpoint", "presentation", "slide") &&
			presentationContainsAny(lower, "pillow", "from pil", "imagedraw", "image.new(", "python-pptx", "contact_sheet", "audit_receipt", "render_receipt")
		return suspiciousName && suspiciousBody
	}
	if !presentationContainsAny(name, "cli", "persistent_shell", "shell", "command", "ptc_execute") {
		return false
	}
	return presentationContainsAny(lower,
		"render_runner.py", "audit_runner.py", "probe_deck.py", "preview_runner.py", "thumbnail_runner.py",
	)
}

func isPresentationAuditAction(payload string) bool {
	lower := strings.ToLower(payload)
	return presentationContainsAny(lower,
		"audit_pptx.py",
		"slides_test.py",
		"validate_pptx",
		"validate-pptx",
		"pptx_validator",
		"pptx-validator",
		"pptx_audit",
		"pptx-audit",
	)
}

func isPresentationAestheticQAAction(payload string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.ReplaceAll(payload, `\\`, "/")))
	return strings.Contains(lower, "validate_aesthetic_qa.py")
}

func isPresentationCanonicalAestheticQAAction(payload string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.ReplaceAll(payload, `\\`, "/")))
	return strings.Contains(lower, "/runtime/builtin/presentation/") &&
		strings.Contains(lower, "/scripts/validate_aesthetic_qa.py")
}

func hasPresentationAestheticQAEvidence(payload, result string) bool {
	if !isPresentationCanonicalAestheticQAAction(payload) || presentationOutputHasFailedExit(result) {
		return false
	}
	if presentationEvidenceMarker(result,
		`"aesthetic_qa_passed":false`, `"aesthetic_qa_passed": false`,
		"aesthetic qa failed", "aesthetic_qa_failed",
	) {
		return false
	}
	return presentationEvidenceMarker(result,
		`"aesthetic_qa_passed":true`, `"aesthetic_qa_passed": true`,
	)
}

func isPresentationRenderAction(payload string) bool {
	lower := strings.ToLower(payload)
	// A Pillow/python-pptx wireframe only draws approximate shape bounds. It is
	// useful for debugging, but it is not evidence of how PowerPoint renders the
	// deck and must never satisfy the real-page rendering receipt.
	if presentationContainsAny(lower, "from pil", "import pillow", "imagedraw", "image.new(") {
		return false
	}
	if presentationContainsAny(lower,
		"render_pptx.ps1",
		"render_pptx.py",
		"render_slides.py",
		"render_presentation.mjs",
	) {
		return true
	}
	if !strings.Contains(lower, ".pptx") {
		return false
	}
	if presentationContainsAny(lower, "soffice", "libreoffice", "unoconv") {
		return presentationContainsAny(lower, "--convert-to", "convert-to", "--output", "--outdir")
	}
	if strings.Contains(lower, "powerpoint.application") {
		return presentationContainsAny(lower, ".export(", ".saveas(", "exportasfixedformat")
	}
	if strings.Contains(lower, "artifact-tool") {
		return presentationContainsAny(lower, "render", "raster", "thumbnail")
	}
	return false
}

func isPresentationVisionAction(toolCall ToolCall) bool {
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	if !presentationContainsAny(name, "vision", "view_image", "image_view", "image_inspect", "inspect_image", "analyze_image") {
		return false
	}
	return len(presentationVisualImagePaths(toolCall)) > 0
}

func isPresentationProductionSkill(skillName, result string) bool {
	lower := strings.ToLower(strings.TrimSpace(skillName + " " + result))
	return presentationContainsAny(lower, "ppt", "powerpoint", "presentation", "slide", "deck", "幻灯片", "演示文稿", "路演稿")
}

func presentationEvidenceMarker(result string, markers ...string) bool {
	lower := strings.ToLower(strings.TrimSpace(result))
	for _, marker := range markers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func presentationOutputHasFailedExit(result string) bool {
	for _, match := range presentationExitCodePattern.FindAllStringSubmatch(result, -1) {
		if len(match) == 2 && strings.TrimSpace(match[1]) != "0" {
			return true
		}
	}
	lower := strings.ToLower(presentationToolOutputPayload(result))
	return presentationContainsAny(lower,
		"traceback (most recent call last)",
		"command not found", "not found command", "no such file or directory",
		`"ok":false`, `"success":false`, `"status":"failed"`, `"status": "failed"`,
	)
}

func hasPresentationStructureEvidence(payload, result string) bool {
	if presentationOutputHasFailedExit(result) || presentationEvidenceMarker(result,
		"audit failed",
		"structure validation failed",
		`"pptx_structure_valid":false`,
		`"presentation_structure_valid":false`,
		"pptx is corrupt",
		"pptx 已损坏",
	) {
		return false
	}
	return isPresentationAuditAction(payload) || presentationEvidenceMarker(result,
		"pptx_structure_ok",
		"presentation_structure_ok",
		`"pptx_structure_valid":true`,
		`"presentation_structure_valid":true`,
		"zip_open=ok",
	)
}

func hasPresentationRenderEvidence(payload, result string) bool {
	if presentationOutputHasFailedExit(result) || presentationEvidenceMarker(result,
		"libreoffice not found",
		"cannot render slides",
		"failed to render presentation",
		"rendering requires desktop powerpoint",
		"artifact-tool package to point to",
		`"presentation_rendered":false`,
		`"render_ok":false`,
		"无法渲染",
		"渲染失败",
	) {
		return false
	}
	return isPresentationRenderAction(payload)
}

func hasPresentationRenderEvidenceForTool(toolCall ToolCall, payload, result string) bool {
	if hasPresentationRenderEvidence(payload, result) {
		return true
	}
	if presentationEvidenceMarker(result,
		"libreoffice not found",
		"cannot render slides",
		"failed to render presentation",
		"rendering requires desktop powerpoint",
		`"presentation_rendered":false`,
		`"render_ok":false`,
		"无法渲染",
		"渲染失败",
	) {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	// Result markers are accepted only from a dedicated renderer whose call
	// also targets a PPTX. Generic cli/ptc scripts can print arbitrary markers;
	// that must not let a Pillow wireframe impersonate native PowerPoint pixels.
	if presentationContainsAny(name, "cli", "ptc_execute", "persistent_shell", "shell", "read_file", "write_file", "edit_file") ||
		!presentationContainsAny(name, "render", "renderer", "raster", "thumbnail") ||
		!presentationContainsAny(name, "ppt", "powerpoint", "presentation", "slide", "deck") ||
		firstPresentationPath(payload) == "" {
		return false
	}
	return presentationEvidenceMarker(result,
		"pptx_render_ok",
		"presentation_render_ok",
		`"presentation_rendered":true`,
		`"render_fidelity":"native"`,
	)
}

func hasPresentationVisualEvidence(toolCall ToolCall, result string) bool {
	if presentationEvidenceMarker(result,
		"use the vision tool to analyze",
		"unable to analyze the image",
		"cannot analyze the image",
		`"presentation_visual_inspected":false`,
		"无法分析图像",
		"无法查看图像",
	) {
		return false
	}
	if presentationEvidenceMarker(result,
		"pptx_visual_qa_ok",
		"presentation_visual_qa_ok",
		`"presentation_visual_inspected":true`,
	) {
		return isPresentationVisionAction(toolCall)
	}
	if !isPresentationVisionAction(toolCall) {
		return false
	}
	// read_file-style placeholder text proves only that a path exists. A visual
	// capability must actually expose pixels to the model.
	lower := strings.ToLower(result)
	return !presentationContainsAny(lower, "请使用 vision", "请调用 vision")
}

func isTrustedLocalPresentationVisualQA(toolCall ToolCall) bool {
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	if presentationContainsAny(name, "presentation_renderer", "ppt_renderer", "slide_renderer") {
		return true
	}
	if name != "cli" && name != "persistent_shell" {
		return false
	}
	payload := strings.ToLower(presentationToolPayload(toolCall))
	// JSON marshaling escapes every Windows separator. Collapse that escaped
	// representation before checking the trusted materialized-runtime path.
	payload = strings.ReplaceAll(payload, `\\`, `/`)
	payload = filepath.ToSlash(payload)
	return strings.Contains(payload, "/runtime/builtin/presentation/") &&
		presentationContainsAny(payload, "/scripts/render_pptx.ps1", "/scripts/render_pptx.py")
}

func presentationSelfAuditObservation(toolCall ToolCall, result string) (observed, failed bool, evidence string) {
	if !strings.EqualFold(strings.TrimSpace(toolCall.Name), "read_file") {
		return false, false, ""
	}
	path := strings.ToLower(filepath.ToSlash(firstStringArg(toolCall.Arguments, "path", "file_path", "file")))
	base := filepath.Base(path)
	if !presentationContainsAny(base, "ppt_qa", "presentation_qa", "ppt-audit", "ppt_audit", "presentation-audit", "presentation_audit", "验收", "审计") {
		return false, false, ""
	}
	lower := strings.ToLower(result)
	if presentationContainsAny(lower,
		"判定：❌", "总体：⚠️", "有项目待修", "仍有项目待修", "不可交付", "验收失败",
		`"passed":false`, `"ok":false`, `"status":"failed"`, "qa failed", "audit failed", "not ready for delivery",
	) {
		return true, true, path
	}
	if presentationContainsAny(lower,
		"总体：✅", "全部通过，可交付", "验收通过", `"passed":true`, `"status":"passed"`, "qa passed", "audit passed",
	) {
		return true, false, path
	}
	return false, false, ""
}

func containsDirectPresentationCreationCode(payload string) bool {
	lower := strings.ToLower(payload)
	if strings.Contains(lower, "pptxgenjs") || strings.Contains(lower, "new pptxgenjs") {
		return true
	}
	if emptyPresentationCtor.MatchString(payload) {
		return true
	}
	if presentationContainsAny(lower, "prs.save(", "presentation.save(", ".writefile(", ".write_file(") && strings.Contains(lower, "ppt") {
		return true
	}
	return strings.Contains(lower, ".pptx") && presentationContainsAny(lower, "zipfile", "ppt/presentation.xml", "[content_types].xml")
}

func presentationShellBuilderObservation(toolCall ToolCall, action string) bool {
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	if !presentationContainsAny(name, "cli", "persistent_shell", "shell_analyze", "shell", "command") {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(action))
	if len(fields) == 0 {
		return false
	}
	command := strings.ToLower(filepath.Base(filepath.ToSlash(strings.Trim(fields[0], "'\""))))
	readOnly := presentationContainsAny(command,
		"cat", "wc", "ls", "dir", "head", "tail", "stat", "file", "test", "find", "grep", "rg",
		"sha256sum", "md5sum", "get-content", "get-item", "select-string",
	)
	if !readOnly {
		return false
	}
	for _, token := range fields[1:] {
		token = strings.ToLower(filepath.Base(filepath.ToSlash(strings.Trim(token, "'\"();"))))
		switch token {
		case "python", "python3", "node", "deno", "bun", "powershell", "pwsh", "bash", "sh":
			return false
		}
	}
	return true
}

func isPresentationBuilderAction(toolCall ToolCall, payload string) bool {
	lower := strings.ToLower(payload)
	if isPresentationScaffoldAction(toolCall, lower) || isPresentationAuditAction(lower) ||
		isPresentationAestheticQAAction(lower) || isPresentationRenderAction(lower) {
		return false
	}
	if toolCall.Name == "convert" {
		return strings.Contains(lower, "ppt")
	}
	if containsDirectPresentationCreationCode(lower) {
		return true
	}
	if presentationContainsAny(lower, "build_deck.py", "build_ppt", "generate_ppt", "generate_deck", "ppt-master", "pptmaster") {
		if presentationShellBuilderObservation(toolCall, lower) {
			return false
		}
		return true
	}
	if presentationContainsAny(toolCall.Name, "cli", "persistent_shell") && strings.Contains(lower, ".pptx") && strings.Contains(lower, ".py") {
		return !presentationContainsAny(lower, "inspect", "extract", "inventory", "analy", "preview", "thumbnail")
	}
	return false
}

func isPresentationQAArtifactPath(path string) bool {
	base := strings.ToLower(filepath.Base(filepath.FromSlash(strings.TrimSpace(path))))
	return presentationContainsAny(base,
		"ppt_qa", "ppt-qa", "presentation_qa", "presentation-qa",
		"ppt_audit", "ppt-audit", "presentation_audit", "presentation-audit",
		"aesthetic_qa", "aesthetic-qa",
		"repair_log", "repair-log",
	)
}

func isPresentationAestheticQAArtifactWrite(toolCall ToolCall) bool {
	if !isPrematurePresentationQAArtifactWrite(toolCall) {
		return false
	}
	path := firstStringArg(toolCall.Arguments, "path", "file_path", "file", "target", "output", "output_path")
	base := strings.ToLower(filepath.Base(filepath.FromSlash(strings.TrimSpace(path))))
	return presentationContainsAny(base, "aesthetic_qa", "aesthetic-qa")
}

func isPrematurePresentationQAArtifactWrite(toolCall ToolCall) bool {
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	if name != "write_file" && name != "edit_file" && name != "multiedit" && name != "json_patch" {
		return false
	}
	path := firstStringArg(toolCall.Arguments, "path", "file_path", "file", "target", "output", "output_path")
	return isPresentationQAArtifactPath(path)
}

func isPresentationStableSpecBuilderAction(toolCall ToolCall, payload string) bool {
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	if name != "cli" && name != "persistent_shell" {
		return false
	}
	lower := strings.ToLower(payload)
	return strings.Contains(lower, "build_task_pptx.py") &&
		strings.Contains(lower, "--spec") && strings.Contains(lower, "--output")
}

func isPresentationBuilderExecutionAction(toolCall ToolCall, payload string) bool {
	if !isPresentationBuilderAction(toolCall, payload) || isPresentationBuilderSourceWrite(toolCall, payload) {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	return toolCall.Name == "convert" || presentationContainsAny(name, "cli", "persistent_shell", "shell", "command")
}

func isPresentationBuilderSourceWrite(toolCall ToolCall, payload string) bool {
	if toolCall.Name != "write_file" && toolCall.Name != "edit_file" && toolCall.Name != "multiedit" {
		return false
	}
	if len(presentationBuilderSourceFragments(toolCall)) > 0 {
		return true
	}
	path := strings.ToLower(firstStringArg(toolCall.Arguments, "path", "file_path", "file"))
	if presentationContainsAny(path, "build_deck.py", "build_ppt", "generate_ppt", "generate_deck") {
		return true
	}
	return (strings.HasSuffix(path, ".py") || strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".ts")) && containsDirectPresentationCreationCode(payload)
}

func isPresentationBuilderSourcePath(path, source string) bool {
	path = strings.ToLower(strings.TrimSpace(filepath.ToSlash(path)))
	if presentationContainsAny(path, "build_deck.py", "build_ppt", "generate_ppt", "generate_deck") {
		return true
	}
	return (strings.HasSuffix(path, ".py") || strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".ts")) &&
		containsDirectPresentationCreationCode(source)
}

// presentationBuilderSourceFragments returns only replacement content that is
// about to be persisted. In particular, edit_file's old_string can contain the
// invalid code that the edit is fixing, so it must not be inspected as if it
// were the new source. Multi-file edits are filtered per target path.
func presentationBuilderSourceFragments(toolCall ToolCall) []string {
	switch strings.ToLower(strings.TrimSpace(toolCall.Name)) {
	case "write_file":
		path := firstStringArg(toolCall.Arguments, "path", "file_path", "file")
		content := firstStringArg(toolCall.Arguments, "content", "text")
		if content != "" && isPresentationBuilderSourcePath(path, content) {
			return []string{content}
		}
	case "edit_file":
		path := firstStringArg(toolCall.Arguments, "path", "file_path", "file")
		content := firstStringArg(toolCall.Arguments, "new_string", "new_content", "content")
		if content != "" && isPresentationBuilderSourcePath(path, content) {
			return []string{content}
		}
	case "multiedit":
		rawEdits, _ := toolCall.Arguments["edits"].([]interface{})
		fragments := make([]string, 0, len(rawEdits))
		for _, raw := range rawEdits {
			edit, _ := raw.(map[string]interface{})
			path := firstStringArg(edit, "path", "file_path", "file")
			content := firstStringArg(edit, "new_string", "new_content", "content")
			if content != "" && isPresentationBuilderSourcePath(path, content) {
				fragments = append(fragments, content)
			}
		}
		return fragments
	}
	return nil
}

func presentationBuilderMutationPaths(toolCall ToolCall) []string {
	switch strings.ToLower(strings.TrimSpace(toolCall.Name)) {
	case "write_file", "edit_file":
		if path := firstStringArg(toolCall.Arguments, "path", "file_path", "file"); path != "" {
			return []string{path}
		}
	case "multi_edit", "multiedit":
		rawEdits, _ := toolCall.Arguments["edits"].([]interface{})
		paths := make([]string, 0, len(rawEdits))
		for _, raw := range rawEdits {
			edit, _ := raw.(map[string]interface{})
			if path := firstStringArg(edit, "path", "file_path", "file"); path != "" {
				paths = append(paths, path)
			}
		}
		return paths
	}
	return nil
}

func presentationBuilderMutationTargets(toolCall ToolCall, builderPath string) bool {
	if strings.TrimSpace(builderPath) == "" || !isPresentationBuilderSourceWrite(toolCall, presentationToolPayload(toolCall)) {
		return false
	}
	for _, path := range presentationBuilderMutationPaths(toolCall) {
		if samePresentationPath(path, builderPath) {
			return true
		}
	}
	return false
}

func presentationCallBody(source string, openParen int) (string, bool) {
	if openParen < 0 || openParen >= len(source) || source[openParen] != '(' {
		return "", false
	}
	depth := 0
	var quote byte
	triple := false
	escaped := false
	for index := openParen; index < len(source); index++ {
		character := source[index]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if triple {
				if character == quote && index+2 < len(source) && source[index+1] == quote && source[index+2] == quote {
					quote = 0
					triple = false
					index += 2
				}
				continue
			}
			if character == quote {
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			if index+2 < len(source) && source[index+1] == character && source[index+2] == character {
				triple = true
				index += 2
			}
			continue
		}
		switch character {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return source[openParen+1 : index], true
			}
		}
	}
	return "", false
}

func splitPresentationCallArguments(body string) []string {
	var arguments []string
	start := 0
	parenDepth, bracketDepth, braceDepth := 0, 0, 0
	var quote byte
	triple := false
	escaped := false
	for index := 0; index < len(body); index++ {
		character := body[index]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if triple {
				if character == quote && index+2 < len(body) && body[index+1] == quote && body[index+2] == quote {
					quote = 0
					triple = false
					index += 2
				}
				continue
			}
			if character == quote {
				quote = 0
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote = character
			if index+2 < len(body) && body[index+1] == character && body[index+2] == character {
				triple = true
				index += 2
			}
			continue
		}
		switch character {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
		case '[':
			bracketDepth++
		case ']':
			bracketDepth--
		case '{':
			braceDepth++
		case '}':
			braceDepth--
		case ',':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				arguments = append(arguments, strings.TrimSpace(body[start:index]))
				start = index + 1
			}
		}
	}
	arguments = append(arguments, strings.TrimSpace(body[start:]))
	return arguments
}

func presentationGeometryArgumentIndexes(method string, argumentCount int) []int {
	var first, last int
	switch strings.ToLower(method) {
	case "add_textbox":
		first, last = 0, 3
	case "add_shape", "add_picture", "add_connector", "add_chart", "add_movie":
		first, last = 1, 4
	case "add_table", "add_ole_object":
		first, last = 2, 5
	default:
		return nil
	}
	if first >= argumentCount {
		return nil
	}
	if last >= argumentCount {
		last = argumentCount - 1
	}
	indexes := make([]int, 0, last-first+1)
	for index := first; index <= last; index++ {
		indexes = append(indexes, index)
	}
	return indexes
}

func presentationGeometryArgumentExplicitlyConverted(argument string) bool {
	argument = strings.TrimSpace(argument)
	if equals := strings.Index(argument, "="); equals >= 0 {
		argument = strings.TrimSpace(argument[equals+1:])
	}
	for len(argument) >= 2 && argument[0] == '(' && argument[len(argument)-1] == ')' {
		argument = strings.TrimSpace(argument[1 : len(argument)-1])
	}
	return strings.EqualFold(argument, "none") || presentationExplicitEMUConversion.MatchString(argument)
}

func presentationGeometryArgumentIsCoordinate(method string, index int, argument string, argumentCount int) bool {
	if equals := strings.Index(argument, "="); equals >= 0 {
		keyword := strings.ToLower(strings.TrimSpace(argument[:equals]))
		switch keyword {
		case "left", "top", "width", "height", "x", "y", "cx", "cy", "begin_x", "begin_y", "end_x", "end_y":
			return true
		default:
			return false
		}
	}
	for _, geometryIndex := range presentationGeometryArgumentIndexes(method, argumentCount) {
		if index == geometryIndex {
			return true
		}
	}
	return false
}

func unsafePresentationGeometrySource(toolCall ToolCall) (string, string, bool) {
	for _, source := range presentationBuilderSourceFragments(toolCall) {
		matches := presentationDirectGeometryCall.FindAllStringSubmatchIndex(source, -1)
		for _, match := range matches {
			if len(match) < 4 {
				continue
			}
			method := source[match[2]:match[3]]
			body, ok := presentationCallBody(source, match[1]-1)
			if !ok {
				continue
			}
			arguments := splitPresentationCallArguments(body)
			for index, argument := range arguments {
				if presentationGeometryArgumentIsCoordinate(method, index, argument, len(arguments)) &&
					!presentationGeometryArgumentExplicitlyConverted(argument) {
					return method, argument, true
				}
			}
		}
	}
	return "", "", false
}

func isPresentationBuilderRead(toolCall ToolCall) bool {
	if toolCall.Name != "read_file" {
		return false
	}
	path := strings.ToLower(firstStringArg(toolCall.Arguments, "path", "file_path", "file"))
	return strings.HasSuffix(filepath.ToSlash(path), "/build_deck.py") ||
		strings.EqualFold(filepath.Base(path), "build_deck.py")
}

// presentationBoundedBuilderReread permits a small recovery read after the
// required full builder read. Long presentation turns can compact the model
// context after that checkpoint; denying every later range read leaves
// edit_file without a refreshable anchor and creates an avoidable retry loop.
func presentationBoundedBuilderReread(toolCall ToolCall) bool {
	if !isPresentationBuilderRead(toolCall) {
		return false
	}
	const maxLines = 160
	rangeSpec := strings.TrimSpace(firstStringArg(toolCall.Arguments, "lines_range", "line_range"))
	if rangeSpec != "" {
		lower := strings.ToLower(rangeSpec)
		for _, prefix := range []string{"head:", "tail:"} {
			if strings.HasPrefix(lower, prefix) {
				count, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(lower, prefix)))
				return err == nil && count > 0 && count <= maxLines
			}
		}
		parts := strings.Split(lower, "-")
		if len(parts) != 2 {
			return false
		}
		start, startErr := strconv.Atoi(strings.TrimSpace(parts[0]))
		end, endErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		return startErr == nil && endErr == nil && start > 0 && end >= start && end-start+1 <= maxLines
	}
	start, startOK := presentationPositiveIntArg(toolCall.Arguments["start_line"])
	end, endOK := presentationPositiveIntArg(toolCall.Arguments["end_line"])
	return startOK && endOK && end >= start && end-start+1 <= maxLines
}

func presentationPositiveIntArg(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, typed > 0
	case int64:
		return int(typed), typed > 0
	case float64:
		integer := int(typed)
		return integer, typed == float64(integer) && integer > 0
	case string:
		integer, err := strconv.Atoi(strings.TrimSpace(typed))
		return integer, err == nil && integer > 0
	default:
		return 0, false
	}
}

func isPresentationDesignHelperRead(toolCall ToolCall) bool {
	if toolCall.Name != "read_file" {
		return false
	}
	path := strings.ToLower(firstStringArg(toolCall.Arguments, "path", "file_path", "file"))
	return strings.EqualFold(filepath.Base(filepath.ToSlash(path)), "pptx_design.py")
}

func isPresentationInlineExecution(toolCall ToolCall, payload string) bool {
	lower := strings.ToLower(payload)
	if toolCall.Name == "ptc_execute" {
		return containsDirectPresentationCreationCode(lower) || isPresentationBuilderAction(toolCall, lower) || mentionsPresentationScaffoldAction(lower)
	}
	if toolCall.Name == "cli" || toolCall.Name == "persistent_shell" {
		return pythonInlinePattern.MatchString(lower) && (strings.Contains(lower, "ppt") || strings.Contains(lower, "presentation"))
	}
	return false
}

func presentationGateBlockedResult(toolCall ToolCall, reason string) (ToolResult, error, bool) {
	err := fmt.Errorf("%s", reason)
	return ToolResult{
		ToolCallID: toolCall.ID,
		Name:       toolCall.Name,
		Content:    "[BLOCKED: PRESENTATION PRODUCTION GATE] " + reason,
	}, err, true
}

func presentationSkillCheckpointResult(toolCall ToolCall, state *presentationProductionState) (ToolResult, error, bool) {
	if state == nil {
		return presentationGateBlockedResult(toolCall, "The presentation production skill is already active for this turn; continue from its existing workflow checkpoint.")
	}
	state.mu.Lock()
	skillName := strings.TrimSpace(state.ActivatedSkill)
	runtimeDir := strings.TrimSpace(state.RuntimeDir)
	scaffolded := state.Scaffolded
	builderRead := state.BuilderRead
	builderCustomized := state.BuilderCustomized
	builderPath := strings.TrimSpace(state.BuilderPath)
	state.mu.Unlock()
	if skillName == "" {
		skillName = "ppt-art-director"
	}
	lines := []string{
		"[HOST PRESENTATION RECOVERY CHECKPOINT]",
		"status: succeeded",
		"The host already activated " + skillName + " for this turn. This is an idempotent checkpoint lookup; the Skill was not reloaded.",
	}
	if runtimeDir != "" {
		lines = append(lines, "Runtime Directory: "+runtimeDir)
	}
	lines = append(lines, fmt.Sprintf(
		"Verified checkpoints: scaffolded=%t, builder_read=%t, builder_customized=%t.",
		scaffolded,
		builderRead,
		builderCustomized,
	))
	if builderPath != "" {
		lines = append(lines, "Existing task builder: "+builderPath)
	}
	switch {
	case scaffolded && builderRead && builderCustomized:
		lines = append(lines, "Do not call use_skill or scaffold again. Continue from the latest gate error: correct the deterministic fact_model generator when facts disagree, then execute the existing task builder as a no-argument Python script.")
	case scaffolded && builderRead:
		lines = append(lines, "Do not call use_skill or scaffold again. Continue with bounded anchored edits in TASK PAGE COMPOSITIONS, then execute the existing task builder.")
	case scaffolded:
		lines = append(lines, "Do not call use_skill or scaffold again. Read the existing task builder once, then continue the Level 2 workflow.")
	default:
		lines = append(lines, "Do not call use_skill again. Continue with the Runtime Directory above and run scaffold_task_pptx.py as one standalone command.")
	}
	return ToolResult{
		ToolCallID:         toolCall.ID,
		Name:               toolCall.Name,
		Content:            strings.Join(lines, "\n"),
		ExecutionSucceeded: toolExecutionOutcome(true),
	}, nil, true
}

func presentationPathWithinContract(path, directory string) bool {
	pathValues := presentationPathVariants(path)
	directoryValues := presentationPathVariants(directory)
	if len(pathValues) == 0 || len(directoryValues) == 0 {
		return false
	}
	for _, pathValue := range pathValues {
		for _, directoryValue := range directoryValues {
			if pathValue == directoryValue || strings.HasPrefix(pathValue, directoryValue+"/") || strings.Contains(pathValue, "/"+directoryValue+"/") || strings.HasSuffix(pathValue, "/"+directoryValue) {
				return true
			}
		}
	}
	return false
}

func presentationPayloadContainsContractPath(payload, path string) bool {
	normalizedPayload := strings.ToLower(filepath.ToSlash(strings.ReplaceAll(payload, `\\`, "/")))
	normalizedPath := normalizedPresentationPath(path)
	return normalizedPath != "" && strings.Contains(normalizedPayload, normalizedPath)
}

func presentationMutationPath(toolCall ToolCall) string {
	switch strings.ToLower(strings.TrimSpace(toolCall.Name)) {
	case "write_file", "edit_file", "json_patch", "multi_edit", "multiedit":
		return firstStringArg(toolCall.Arguments, "path", "file_path", "file")
	default:
		return ""
	}
}

func isPresentationFactModelDirectMutation(toolCall ToolCall) bool {
	path := strings.ToLower(filepath.Base(filepath.ToSlash(presentationMutationPath(toolCall))))
	return path == "fact_model.json"
}

func isPPTPolishCanonicalAuditAction(payload string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.ReplaceAll(payload, `\\`, "/")))
	return strings.Contains(lower, "/runtime/builtin/presentation/") &&
		strings.Contains(lower, "/scripts/audit_pptx.py")
}

func isPPTPolishCanonicalRenderAction(payload string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.ReplaceAll(payload, `\\`, "/")))
	return strings.Contains(lower, "/runtime/builtin/presentation/") &&
		presentationContainsAny(lower, "/scripts/render_pptx.py", "/scripts/render_pptx.ps1")
}

func presentationProductionCommandUsesExitMaskingWrapper(action string) bool {
	action = strings.TrimSpace(action)
	return strings.ContainsAny(action, "|;\r\n")
}

func presentationAuditUsesUnsupportedInputFlag(action string) bool {
	return regexp.MustCompile(`(?i)(?:^|\s)--input(?:\s|=)`).MatchString(strings.TrimSpace(action))
}

func presentationCommandNumericFlag(action, flag string) (float64, bool) {
	pattern := regexp.MustCompile(`(?i)(?:^|\s)` + regexp.QuoteMeta(flag) + `(?:\s+|=)(\d+(?:\.\d+)?)\b`)
	match := pattern.FindStringSubmatch(action)
	if len(match) != 2 {
		return 0, false
	}
	value, err := strconv.ParseFloat(match[1], 64)
	return value, err == nil
}

func presentationAuditFontFloorIssue(action string, requiredBody, requiredTitle float64) string {
	for _, requirement := range []struct {
		flag  string
		label string
		floor float64
	}{
		{flag: "--min-body-font-size", label: "正文", floor: requiredBody},
		{flag: "--min-title-font-size", label: "标题", floor: requiredTitle},
	} {
		if requirement.floor <= 0 {
			continue
		}
		actual, ok := presentationCommandNumericFlag(action, requirement.flag)
		if !ok || actual < requirement.floor {
			return fmt.Sprintf(
				"用户要求%s字号至少 %gpt；标准审计命令必须显式携带 %s %g，且不得用缺省或更低阈值重新审计来掩盖失败。",
				requirement.label,
				requirement.floor,
				requirement.flag,
				requirement.floor,
			)
		}
	}
	return ""
}

func pptPolishShellMutatesContractFiles(toolCall ToolCall, payload, workDir, previewDir string) bool {
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	if !presentationContainsAny(name, "cli", "persistent_shell", "shell", "command", "ptc_execute") {
		return false
	}
	if !presentationPayloadContainsContractPath(payload, workDir) && !presentationPayloadContainsContractPath(payload, previewDir) {
		return false
	}
	lower := strings.ToLower(payload)
	lower = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&").Replace(lower)
	return presentationContainsAny(lower,
		"python3 <<", "python3 - <<", "python <<", "python - <<", "python3 -c", "python -c",
		"rm -", "mv ", "cp ", "install ", "sed -i", "perl -pi", "tee ", "touch ",
		"pdftoppm", "libreoffice", "soffice", "unoconv", "from pil", "image.new(",
	)
}

func pptPolishContractToolIssue(
	toolCall ToolCall,
	payload, deckPath, workDir, previewDir, builderPath string,
	scaffolded, boundedRevision bool,
	revisionMode string,
	revisionBuildLimit, revisionBuildCount, revisionMutationCount int,
	requiredBodyFontSize, requiredTitleFontSize float64,
	sourceDirty, auditFailed bool,
	auditFailureMutationCount int,
	auditPassed, rendered bool,
	renderTarget string,
) string {
	action := permissionpolicy.ExtractAction(toolCall.Arguments)
	builderAction := isPresentationBuilderAction(toolCall, action)
	auditAction := isPresentationAuditAction(action)
	renderAction := isPresentationRenderAction(action)
	mutationPath := presentationMutationPath(toolCall)
	if mutationPath != "" && !presentationPathWithinContract(mutationPath, workDir) && !samePresentationPath(mutationPath, deckPath) {
		return fmt.Sprintf("PPT 本次运行只允许修改任务目录 %s 或精确最终文件 %s；禁止写入共享、运行时资产或其他目录：%s。", workDir, deckPath, mutationPath)
	}
	if (builderAction || auditAction || renderAction) && presentationProductionCommandUsesExitMaskingWrapper(action) {
		return "PPT 构建、审计和渲染必须作为单个直接命令执行；禁止使用管道、分号或多行包装命令掩盖真实退出状态。"
	}
	if pptPolishShellMutatesContractFiles(toolCall, action, workDir, previewDir) &&
		!isPPTPolishCanonicalAuditAction(action) &&
		!isPPTPolishCanonicalRenderAction(action) {
		return "PPT 契约禁止用 shell、内联 Python、LibreOffice、pdftoppm 或 PIL 直接改写规格和 preview；内容修正只用 edit_file/write_file，审计与渲染只用内置标准脚本。"
	}
	if scaffolded && isPresentationBuilderRead(toolCall) {
		readPath := firstStringArg(toolCall.Arguments, "path", "file_path", "file")
		allowedBuilder := builderPath
		if strings.TrimSpace(allowedBuilder) == "" {
			allowedBuilder = filepath.ToSlash(filepath.Join(workDir, "build_deck.py"))
		}
		if !samePresentationPath(readPath, allowedBuilder) {
			return fmt.Sprintf("脚手架已创建任务专用 builder；只能读取 %s，禁止改读运行时模板或共享 builder：%s。", allowedBuilder, readPath)
		}
	}
	if boundedRevision && mutationPath != "" && revisionBuildCount >= revisionBuildLimit {
		if revisionMode == "freshness" {
			return "本轮是 freshness-only 复核：最终 PPTX、deck_spec.json 和 builder 已冻结，禁止任何编辑；只允许审计、精确重渲染和逐页 vision。"
		}
		return fmt.Sprintf("本次有界返工已经完成允许的 %d 次重建，最终字节已冻结；禁止再次编辑或改写规格。请只审计、渲染精确最终 PPTX 并完成逐页 vision。", revisionBuildLimit)
	}
	if builderAction {
		if !presentationPayloadContainsContractPath(action, workDir) {
			return fmt.Sprintf("PPT 构建只能运行本次任务目录 %s 中的 builder；禁止切换到运行时资产、共享目录或临时副本。", workDir)
		}
		if boundedRevision && revisionMode != "freshness" && revisionMutationCount == 0 {
			return "本次内容返工尚未发生受控编辑；先用 edit_file 或 write_file 修正本次任务的 deck_spec.json/builder，再运行唯一一次 builder。禁止用 shell 内联脚本代替编辑工具。"
		}
		if boundedRevision && revisionBuildCount >= revisionBuildLimit {
			if revisionMode == "freshness" {
				return "本轮是 freshness-only 复核，最终 PPTX 字节必须保持不变；禁止运行 builder，只需对现有精确文件重新审计、渲染和逐页检查。"
			}
			return fmt.Sprintf("本次有界返工最多重建 %d 次且额度已用完；禁止再次运行 builder。请冻结现有 PPTX 并完成最终审计、渲染与逐页 vision。", revisionBuildLimit)
		}
		if auditFailed && revisionMutationCount <= auditFailureMutationCount {
			return "上一轮严格审计已经失败；必须先用受控编辑修正 deck_spec.json 或任务 builder，再重新构建，不能直接重跑 builder 或审计。"
		}
	}
	if boundedRevision && revisionMode != "freshness" && revisionBuildCount < revisionBuildLimit &&
		(auditAction || renderAction || isPresentationVisionAction(toolCall)) {
		return fmt.Sprintf("本次内容返工尚未完成成功重建（%d/%d）；旧 PPTX、旧预览和旧 vision 不能证明修正生效。先修复规格并成功运行本次任务 builder，然后才能审计、渲染和逐页检查。", revisionBuildCount, revisionBuildLimit)
	}
	if auditAction {
		if !isPPTPolishCanonicalAuditAction(action) {
			return "PPT 结构审计只能运行本次运行时内置 scripts/audit_pptx.py，禁止自定义或旁路审计命令。"
		}
		if presentationAuditUsesUnsupportedInputFlag(action) {
			return "audit_pptx.py 不支持 --input；请把精确最终 PPTX 路径作为脚本后的第一个位置参数，再携带要求的字号阈值。"
		}
		if sourceDirty {
			return "PPT 源规格或任务 builder 已在上次构建后修改；必须先直接运行本次任务 builder 生成新字节，再执行严格审计。"
		}
		if auditFailed {
			return "上一轮严格审计已经失败；必须先受控编辑并成功重新构建，禁止用缺省参数或重复审计覆盖失败记录。"
		}
		if issue := presentationAuditFontFloorIssue(action, requiredBodyFontSize, requiredTitleFontSize); issue != "" {
			return issue
		}
		auditedDeck := presentationToolDeckPath(toolCall, payload, "")
		if auditedDeck == "" || !samePresentationPath(auditedDeck, deckPath) {
			return fmt.Sprintf("PPT 审计必须针对精确最终文件 %s，不能审计其他副本：%s。", deckPath, auditedDeck)
		}
	}
	if renderAction {
		if !isPPTPolishCanonicalRenderAction(action) {
			return "PPT 最终渲染只能运行本次运行时内置 scripts/render_pptx.py 或 render_pptx.ps1；禁止直接调用 LibreOffice、soffice 或自制转换链。"
		}
		if sourceDirty {
			return "PPT 源规格或任务 builder 已修改但尚未重新构建；旧 PPTX 和旧审计不能用于渲染交付。"
		}
		if !auditPassed {
			return "最终 PPTX 尚未通过标准结构审计；必须先对精确最终文件运行 audit_pptx.py，审计成功后才能渲染。"
		}
		renderedDeck := presentationToolDeckPath(toolCall, payload, "")
		if renderedDeck == "" || !samePresentationPath(renderedDeck, deckPath) {
			return fmt.Sprintf("PPT 渲染必须读取精确最终文件 %s，不能渲染其他副本：%s。", deckPath, renderedDeck)
		}
		renderTarget := presentationRenderTarget(toolCall, payload, "")
		if renderTarget == "" || !samePresentationPath(renderTarget, previewDir) {
			return fmt.Sprintf("PPT 渲染输出只能写入本次运行的精确 preview：%s；禁止 tenant-root、storage-root、共享或临时渲染目录：%s。", previewDir, renderTarget)
		}
	}
	if isPresentationVisionAction(toolCall) {
		if sourceDirty {
			return "PPT 源规格或任务 builder 已修改但尚未重新构建；旧 preview 和旧 vision 证据已经失效。"
		}
		if !rendered || !samePresentationPath(renderTarget, previewDir) {
			return "最终 PPTX 尚未通过标准渲染写入本次精确 preview；旧图片不能作为证据。先运行内置 render_pptx，再检查 contact_sheet.jpg 和每个 slide_NN.png。"
		}
		for _, imagePath := range presentationVisualImagePaths(toolCall) {
			if !presentationPathWithinContract(imagePath, previewDir) {
				return fmt.Sprintf("PPT vision 只能检查本次运行 preview %s 下的 contact_sheet 和 slide_NN 图片；禁止共享、旧任务或临时图片：%s。", previewDir, imagePath)
			}
			if !presentationContractVisionPattern.MatchString(filepath.Base(normalizedPresentationPath(imagePath))) {
				return fmt.Sprintf("PPT vision 文件名必须是标准 contact_sheet.jpg 或 slide_NN.png；禁止自制联系表、连字符页名或其他图片：%s。", imagePath)
			}
		}
	}
	return ""
}

func presentationJSONSpecSlideCount(content string) (int, bool) {
	var spec struct {
		Slides []json.RawMessage `json:"slides"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &spec); err != nil || spec.Slides == nil {
		return 0, false
	}
	return len(spec.Slides), true
}

func presentationFlagPath(pattern *regexp.Regexp, action string) string {
	match := pattern.FindStringSubmatch(action)
	if len(match) == 0 {
		return ""
	}
	for _, value := range match[1:] {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func presentationScaffoldBuilderPath(toolCall ToolCall) string {
	if !isPresentationScaffoldAction(toolCall, presentationToolPayload(toolCall)) {
		return ""
	}
	return presentationFlagPath(presentationBuilderFlagPattern, permissionpolicy.ExtractAction(toolCall.Arguments))
}

func presentationScaffoldReceiptBuilderPath(result string) string {
	payload := presentationToolOutputPayload(result)
	if payload == "" {
		return ""
	}
	var receipt struct {
		Builder string `json:"builder"`
	}
	if err := json.Unmarshal([]byte(payload), &receipt); err != nil {
		return ""
	}
	return strings.TrimSpace(receipt.Builder)
}

func presentationScaffoldActionStandalone(toolCall ToolCall) bool {
	action := strings.ToLower(strings.TrimSpace(permissionpolicy.ExtractAction(toolCall.Arguments)))
	if action == "" {
		return false
	}
	return !presentationContainsAny(action, "&&", "||", ";", "|", "cmd.exe", "powershell")
}

func presentationScaffoldArtifactsExist(ctx context.Context, builderPaths ...string) bool {
	workspace := strings.TrimSpace(projectctx.ResolvedWorkspace(ctx))
	if workspace == "" {
		// Some unit and compatibility contexts do not expose a resolved
		// workspace. Preserve the historical receipt-based behavior there.
		return true
	}
	for _, builderPath := range builderPaths {
		for _, candidate := range presentationCandidateAbsolutePaths(workspace, builderPath) {
			candidate, err := filepath.Abs(candidate)
			if err != nil || !projectctx.ContainsPath(workspace, candidate) || projectctx.EnsureResolvedPathInside(workspace, candidate) != nil {
				continue
			}
			if info, err := os.Stat(candidate); err != nil || info.IsDir() || info.Size() == 0 {
				continue
			}
			dir := filepath.Dir(candidate)
			complete := true
			for _, sibling := range []string{"deck_spec.json", "visual_plan.json", "pptx_design.py"} {
				info, err := os.Stat(filepath.Join(dir, sibling))
				if err != nil || info.IsDir() || info.Size() == 0 {
					complete = false
					break
				}
			}
			if complete {
				return true
			}
		}
	}
	return false
}

func presentationBuilderSpecPath(toolCall ToolCall) string {
	if !isPresentationStableSpecBuilderAction(toolCall, presentationToolPayload(toolCall)) {
		return ""
	}
	return presentationFlagPath(presentationSpecInputFlagPattern, permissionpolicy.ExtractAction(toolCall.Arguments))
}

func presentationSpecSlideCountFromWorkspace(ctx context.Context, specPath string) (int, bool) {
	workspace := strings.TrimSpace(projectctx.ResolvedWorkspace(ctx))
	if workspace == "" || strings.TrimSpace(specPath) == "" {
		return 0, false
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return 0, false
	}
	target := strings.TrimSpace(specPath)
	if !filepath.IsAbs(target) {
		target = filepath.Join(workspace, target)
	}
	target, err = filepath.Abs(target)
	if err != nil || !projectctx.ContainsPath(workspace, target) || projectctx.EnsureResolvedPathInside(workspace, target) != nil {
		return 0, false
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return 0, false
	}
	return presentationJSONSpecSlideCount(string(content))
}

func presentationSpecPageBoundIssue(ctx context.Context, toolCall ToolCall, minimum, maximum int) string {
	count, found := 0, false
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	if name == "write_file" {
		path := strings.ToLower(firstStringArg(toolCall.Arguments, "path", "file_path", "file"))
		content, _ := toolCall.Arguments["content"].(string)
		if strings.HasSuffix(path, ".json") {
			count, found = presentationJSONSpecSlideCount(content)
			// A draft may grow later, but it must never begin above a hard
			// maximum that the final deck cannot legally exceed.
			if found && maximum > 0 && count > maximum {
				return fmt.Sprintf("演示规格包含 %d 页，超过用户明确允许的最多 %d 页；请先合并或删除页面，再写入规格。封面也计入总页数。", count, maximum)
			}
		}
		return ""
	}
	if specPath := presentationBuilderSpecPath(toolCall); specPath != "" {
		count, found = presentationSpecSlideCountFromWorkspace(ctx, specPath)
	}
	if !found {
		return ""
	}
	if maximum > 0 && count > maximum {
		return fmt.Sprintf("演示规格包含 %d 页，超过用户明确允许的最多 %d 页；在运行 builder 前先修正规格，封面也计入总页数。", count, maximum)
	}
	if minimum > 0 && count < minimum {
		return fmt.Sprintf("演示规格仅包含 %d 页，少于用户明确要求的至少 %d 页；在运行 builder 前先补齐规格。", count, minimum)
	}
	return ""
}

func presentationSourceBoundSpecPath(ctx context.Context, toolCall ToolCall, builderPath string) (string, error) {
	workspace := strings.TrimSpace(projectctx.ResolvedWorkspace(ctx))
	if workspace == "" {
		return "", fmt.Errorf("workspace is unavailable")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	raw := presentationBuilderSpecPath(toolCall)
	if raw == "" && strings.TrimSpace(builderPath) != "" {
		raw = filepath.Join(filepath.Dir(filepath.FromSlash(builderPath)), "deck_spec.json")
	}
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("task-local deck_spec.json is not associated with this builder")
	}
	target := filepath.FromSlash(strings.TrimSpace(raw))
	if !filepath.IsAbs(target) {
		target = filepath.Join(workspace, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if !projectctx.ContainsPath(workspace, target) || projectctx.EnsureResolvedPathInside(workspace, target) != nil {
		return "", fmt.Errorf("deck_spec.json is outside the current workspace: %s", target)
	}
	return target, nil
}

func presentationFactModelNumber(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	default:
		return 0, false
	}
}

func presentationFactModelCount(model map[string]interface{}, keys ...string) (float64, bool) {
	for _, key := range keys {
		value, exists := model[key]
		if !exists {
			continue
		}
		if number, ok := presentationFactModelNumber(value); ok {
			return number, true
		}
		if values, ok := value.([]interface{}); ok {
			return float64(len(values)), true
		}
	}
	return 0, false
}

func presentationFactModelObject(model map[string]interface{}, keys ...string) (map[string]interface{}, bool) {
	for _, key := range keys {
		if value, ok := model[key].(map[string]interface{}); ok {
			return value, true
		}
	}
	return nil, false
}

func presentationFactModelArray(model map[string]interface{}, keys ...string) ([]interface{}, bool) {
	for _, key := range keys {
		if value, ok := model[key].([]interface{}); ok {
			return value, true
		}
	}
	return nil, false
}

func presentationFactModelText(value interface{}) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func presentationFactModelRowID(row map[string]interface{}) string {
	for _, key := range []string{"order_id", "id", "row_id"} {
		if value := presentationFactModelText(row[key]); value != "" {
			return value
		}
	}
	return ""
}

func presentationFactModelTraceRows(model map[string]interface{}, expected float64, label string, keys ...string) ([]interface{}, error) {
	rows, ok := presentationFactModelArray(model, keys...)
	if !ok {
		return nil, fmt.Errorf("%s must be an explicit array of traceable rows, not only a count", label)
	}
	if float64(len(rows)) != expected {
		return nil, fmt.Errorf("%s length %d disagrees with count %.0f", label, len(rows), expected)
	}
	for index, value := range rows {
		row, ok := value.(map[string]interface{})
		if !ok || presentationFactModelRowID(row) == "" {
			return nil, fmt.Errorf("%s[%d] must contain order_id/id", label, index)
		}
		if label == "excluded_rows" && presentationFactModelText(row["reason"]) == "" && presentationFactModelText(row["detail"]) == "" && presentationFactModelText(row["rule"]) == "" {
			return nil, fmt.Errorf("%s[%d] must contain a source-grounded reason", label, index)
		}
		if label == "duplicate_pairs" {
			_, keptOK := row["kept_raw"].(map[string]interface{})
			_, discardedOK := row["discarded_raw"].(map[string]interface{})
			if !keptOK || !discardedOK {
				return nil, fmt.Errorf("%s[%d] must contain kept_raw and discarded_raw evidence", label, index)
			}
		}
	}
	return rows, nil
}

func presentationFactModelIDs(rows []interface{}) []string {
	ids := make([]string, 0, len(rows))
	for _, value := range rows {
		if row, ok := value.(map[string]interface{}); ok {
			ids = append(ids, presentationFactModelRowID(row))
		}
	}
	sort.Strings(ids)
	return ids
}

func presentationFactModelSameIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func presentationFactModelResolveSource(factPath, source string) string {
	source = filepath.FromSlash(strings.TrimSpace(source))
	if source == "" {
		return ""
	}
	if filepath.IsAbs(source) {
		if info, err := os.Stat(source); err == nil && !info.IsDir() {
			return source
		}
		return ""
	}
	dir := filepath.Dir(factPath)
	for depth := 0; depth < 8; depth++ {
		candidate := filepath.Join(dir, source)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func presentationFactModelCSV(path string) ([]string, [][]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	if len(records) < 2 {
		return nil, nil, fmt.Errorf("CSV has no data rows")
	}
	headers := make([]string, len(records[0]))
	for index, header := range records[0] {
		headers[index] = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(header, "\ufeff")))
	}
	return headers, records[1:], nil
}

func presentationFactModelColumn(headers []string, names ...string) int {
	for _, name := range names {
		for index, header := range headers {
			if header == name {
				return index
			}
		}
	}
	return -1
}

func presentationFactModelCell(row []string, index int) string {
	if index < 0 || index >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[index])
}

func presentationFactModelParseAmount(raw string) (float64, bool) {
	cleaned := strings.NewReplacer(
		",", "", "，", "", "￥", "", "¥", "", "$", "", "€", "", "£", "", "%", "", " ", "",
	).Replace(strings.TrimSpace(raw))
	if cleaned == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(cleaned, 64)
	return value, err == nil
}

func presentationFactModelClose(left, right float64) bool {
	return math.Abs(left-right) <= 0.01
}

type presentationSourceAggregate struct {
	Revenue float64
	Cost    float64
}

func validatePresentationFactModelSources(path string, model map[string]interface{}, includedRows, excludedRows, duplicateRows []interface{}) error {
	sources, ok := presentationFactModelObject(model, "source_files")
	if !ok {
		return nil
	}
	salesPath := presentationFactModelResolveSource(path, presentationFactModelText(sources["sales"]))
	policyPath := presentationFactModelResolveSource(path, presentationFactModelText(sources["policy"]))
	if salesPath == "" || policyPath == "" {
		return fmt.Errorf("source_files.sales and source_files.policy must resolve to readable local files")
	}
	policyBytes, err := os.ReadFile(policyPath)
	if err != nil {
		return err
	}
	policy := strings.ToLower(string(policyBytes))
	headers, rows, err := presentationFactModelCSV(salesPath)
	if err != nil {
		return fmt.Errorf("cannot parse source sales CSV: %w", err)
	}
	idColumn := presentationFactModelColumn(headers, "order_id", "id", "row_id")
	statusColumn := presentationFactModelColumn(headers, "status")
	regionColumn := presentationFactModelColumn(headers, "region")
	revenueColumn := presentationFactModelColumn(headers, "revenue", "amount", "sales")
	costColumn := presentationFactModelColumn(headers, "cost")
	if idColumn < 0 || revenueColumn < 0 || costColumn < 0 {
		return nil
	}

	bestByID := make(map[string][]string)
	bestScore := make(map[string]int)
	order := make([]string, 0, len(rows))
	duplicateIDs := make([]string, 0)
	for rowIndex, row := range rows {
		id := presentationFactModelCell(row, idColumn)
		if id == "" {
			id = fmt.Sprintf("__row_%d", rowIndex+2)
		}
		score := 0
		for _, cell := range row {
			if strings.TrimSpace(cell) != "" {
				score++
			}
		}
		if _, exists := bestByID[id]; !exists {
			order = append(order, id)
			bestByID[id] = row
			bestScore[id] = score
			continue
		}
		duplicateIDs = append(duplicateIDs, id)
		if score > bestScore[id] {
			bestByID[id] = row
			bestScore[id] = score
		}
	}

	excludeCancelled := statusColumn >= 0 && strings.Contains(policy, "cancelled")
	includedIDs := make([]string, 0, len(order))
	excludedIDs := make([]string, 0)
	regions := make(map[string]presentationSourceAggregate)
	total := presentationSourceAggregate{}
	for _, id := range order {
		row := bestByID[id]
		if excludeCancelled && strings.EqualFold(presentationFactModelCell(row, statusColumn), "cancelled") {
			excludedIDs = append(excludedIDs, id)
			continue
		}
		includedIDs = append(includedIDs, id)
		revenue, revenueOK := presentationFactModelParseAmount(presentationFactModelCell(row, revenueColumn))
		cost, costOK := presentationFactModelParseAmount(presentationFactModelCell(row, costColumn))
		if !revenueOK {
			return fmt.Errorf("source row %s has an unreadable revenue", id)
		}
		total.Revenue += revenue
		if costOK {
			total.Cost += cost
		}
		region := presentationFactModelCell(row, regionColumn)
		if region != "" {
			aggregate := regions[region]
			aggregate.Revenue += revenue
			if costOK {
				aggregate.Cost += cost
			}
			regions[region] = aggregate
		}
	}
	if !presentationFactModelSameIDs(includedIDs, presentationFactModelIDs(includedRows)) {
		return fmt.Errorf("included_rows IDs disagree with deterministic source cleaning: expected %v", includedIDs)
	}
	if !presentationFactModelSameIDs(excludedIDs, presentationFactModelIDs(excludedRows)) {
		return fmt.Errorf("excluded_rows IDs disagree with deterministic source cleaning: expected %v", excludedIDs)
	}
	if !presentationFactModelSameIDs(duplicateIDs, presentationFactModelIDs(duplicateRows)) {
		return fmt.Errorf("duplicate_pairs IDs disagree with deterministic source cleaning: expected %v", duplicateIDs)
	}

	totals, _ := presentationFactModelObject(model, "totals", "totals_after_cleaning")
	revenue, revenueOK := presentationFactModelCount(totals, "revenue", "revenue_net", "total_revenue")
	cost, costOK := presentationFactModelCount(totals, "cost", "total_cost")
	if !revenueOK || !costOK || !presentationFactModelClose(revenue, total.Revenue) || !presentationFactModelClose(cost, total.Cost) {
		return fmt.Errorf("source totals disagree: expected revenue %.2f and cost %.2f", total.Revenue, total.Cost)
	}
	regionModel, _ := presentationFactModelObject(model, "by_region", "region", "region_aggregates")
	for region, expected := range regions {
		value, ok := regionModel[region].(map[string]interface{})
		if !ok {
			return fmt.Errorf("by_region is missing source region %s", region)
		}
		regionRevenue, revenueOK := presentationFactModelCount(value, "revenue", "actual_revenue")
		regionCost, costOK := presentationFactModelCount(value, "cost", "actual_cost")
		if !revenueOK || !costOK || !presentationFactModelClose(regionRevenue, expected.Revenue) || !presentationFactModelClose(regionCost, expected.Cost) {
			return fmt.Errorf("region %s disagrees with source: expected revenue %.2f and cost %.2f", region, expected.Revenue, expected.Cost)
		}
	}

	targetSource := presentationFactModelText(sources["targets"])
	if targetSource == "" {
		return nil
	}
	targetPath := presentationFactModelResolveSource(path, targetSource)
	if targetPath == "" {
		return fmt.Errorf("source_files.targets must resolve to a readable local file")
	}
	targetHeaders, targetRows, err := presentationFactModelCSV(targetPath)
	if err != nil {
		return fmt.Errorf("cannot parse source targets CSV: %w", err)
	}
	targetRegionColumn := presentationFactModelColumn(targetHeaders, "region")
	revenueTargetColumn := presentationFactModelColumn(targetHeaders, "q1_revenue_target", "revenue_target", "target_revenue")
	marginTargetColumn := presentationFactModelColumn(targetHeaders, "q1_gross_margin_target", "gross_margin_target", "target_margin")
	if targetRegionColumn < 0 || revenueTargetColumn < 0 || marginTargetColumn < 0 {
		return nil
	}
	targets, ok := presentationFactModelObject(model, "targets")
	if !ok {
		return fmt.Errorf("targets must be a region-keyed object copied from source_files.targets")
	}
	for _, row := range targetRows {
		region := presentationFactModelCell(row, targetRegionColumn)
		expectedRevenue, revenueOK := presentationFactModelParseAmount(presentationFactModelCell(row, revenueTargetColumn))
		expectedMargin, marginOK := presentationFactModelParseAmount(presentationFactModelCell(row, marginTargetColumn))
		value, valueOK := targets[region].(map[string]interface{})
		if !revenueOK || !marginOK || !valueOK {
			return fmt.Errorf("targets is missing source region %s", region)
		}
		actualRevenue, actualRevenueOK := presentationFactModelCount(value, "revenue_target", "target_revenue", "q1_revenue_target")
		actualMargin, actualMarginOK := presentationFactModelCount(value, "gross_margin_target", "target_margin", "q1_gross_margin_target")
		if !actualRevenueOK || !actualMarginOK || !presentationFactModelClose(actualRevenue, expectedRevenue) || math.Abs(actualMargin-expectedMargin) > 0.0001 {
			return fmt.Errorf("target %s disagrees with source: expected revenue %.2f and margin %.4f", region, expectedRevenue, expectedMargin)
		}
	}
	return nil
}

func presentationFactModelMissingCostContradiction(model map[string]interface{}) error {
	missingRows, _ := model["missing_cost_rows"].([]interface{})
	duplicatePairs, _ := model["duplicate_pairs"].([]interface{})
	if len(missingRows) == 0 || len(duplicatePairs) == 0 {
		return nil
	}
	retainedCost := make(map[string]string)
	for _, value := range duplicatePairs {
		pair, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		orderID := presentationFactModelText(pair["order_id"])
		kept, _ := pair["kept_raw"].(map[string]interface{})
		cost := presentationFactModelText(kept["cost"])
		if orderID != "" && cost != "" && !strings.EqualFold(cost, "null") {
			retainedCost[orderID] = cost
		}
	}
	for _, value := range missingRows {
		row, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		orderID := presentationFactModelText(row["order_id"])
		if cost := retainedCost[orderID]; cost != "" {
			return fmt.Errorf("missing-cost anomaly for %s contradicts retained duplicate row cost %s", orderID, cost)
		}
	}
	return nil
}

func validatePresentationFactModel(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.UseNumber()
	model := make(map[string]interface{})
	if err := decoder.Decode(&model); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	raw, rawOK := presentationFactModelCount(model, "raw_row_count", "raw_rows")
	included, includedOK := presentationFactModelCount(model, "included_count", "included_rows", "included_row_ids", "included_after_cleaning")
	excluded, excludedOK := presentationFactModelCount(model, "excluded_count", "excluded", "excluded_rows")
	duplicates, duplicatesOK := presentationFactModelCount(model, "duplicate_dropped_count", "duplicates_dropped_count", "duplicate_discarded", "duplicate_dropped", "duplicates_dropped", "duplicate_pairs")
	if !rawOK || !includedOK || !excludedOK || !duplicatesOK {
		return fmt.Errorf("missing raw/included/excluded/duplicate reconciliation counts")
	}
	if raw != included+excluded+duplicates {
		return fmt.Errorf("row reconciliation failed: raw %.0f != included %.0f + excluded %.0f + duplicate-discarded %.0f", raw, included, excluded, duplicates)
	}
	includedRows, err := presentationFactModelTraceRows(model, included, "included_rows", "included_rows", "included_row_ids")
	if err != nil {
		return err
	}
	excludedRows, err := presentationFactModelTraceRows(model, excluded, "excluded_rows", "excluded_rows", "excluded")
	if err != nil {
		return err
	}
	duplicateRows, err := presentationFactModelTraceRows(model, duplicates, "duplicate_pairs", "duplicate_pairs", "duplicate_discarded", "duplicate_dropped", "duplicates_dropped")
	if err != nil {
		return err
	}
	reconciliation, ok := presentationFactModelObject(model, "reconciliation", "row_reconciliation")
	if !ok {
		return fmt.Errorf("missing reconciliation object")
	}
	sumCheck, sumCheckExists := reconciliation["sum_check"].(bool)
	balanced, balancedExists := reconciliation["balanced"].(bool)
	if (!sumCheckExists && !balancedExists) || (sumCheckExists && !sumCheck) || (balancedExists && !balanced) {
		return fmt.Errorf("reconciliation must contain a true sum_check or balanced flag")
	}
	if recRaw, exists := presentationFactModelCount(reconciliation, "raw_rows", "raw_row_count", "raw"); exists && recRaw != raw {
		return fmt.Errorf("reconciliation raw rows %.0f disagree with top-level %.0f", recRaw, raw)
	}

	if err := presentationFactModelMissingCostContradiction(model); err != nil {
		return err
	}

	totals, totalsOK := presentationFactModelObject(model, "totals", "totals_after_cleaning")
	if !totalsOK {
		return fmt.Errorf("missing canonical totals object")
	}
	regions, regionsOK := presentationFactModelObject(model, "by_region", "region", "region_aggregates")
	if !regionsOK {
		return fmt.Errorf("missing canonical by_region object")
	}
	totalRevenue, revenueOK := presentationFactModelCount(totals, "revenue", "revenue_net", "total_revenue")
	totalCost, costOK := presentationFactModelCount(totals, "cost", "total_cost")
	grossProfit, grossProfitOK := presentationFactModelCount(totals, "gross_profit")
	grossMargin, grossMarginOK := presentationFactModelCount(totals, "gross_margin")
	if !revenueOK || !costOK || !grossProfitOK || !grossMarginOK {
		return fmt.Errorf("totals must contain revenue, cost, gross_profit, and gross_margin")
	}
	if !presentationFactModelClose(grossProfit, totalRevenue-totalCost) {
		return fmt.Errorf("gross profit reconciliation failed: %.2f != revenue %.2f - cost %.2f", grossProfit, totalRevenue, totalCost)
	}
	if totalRevenue != 0 && math.Abs(grossMargin-grossProfit/totalRevenue) > 0.0001 {
		return fmt.Errorf("gross margin reconciliation failed: %.6f != gross profit %.2f / revenue %.2f", grossMargin, grossProfit, totalRevenue)
	}
	regionRevenue := 0.0
	regionCost := 0.0
	for _, value := range regions {
		region, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("invalid region aggregate")
		}
		revenue, ok := presentationFactModelCount(region, "revenue", "actual_revenue")
		if !ok {
			return fmt.Errorf("region aggregate missing revenue")
		}
		cost, ok := presentationFactModelCount(region, "cost", "actual_cost")
		if !ok {
			return fmt.Errorf("region aggregate missing cost")
		}
		regionRevenue += revenue
		regionCost += cost
	}
	if !presentationFactModelClose(regionRevenue, totalRevenue) {
		return fmt.Errorf("revenue reconciliation failed: regions %.2f != total %.2f", regionRevenue, totalRevenue)
	}
	if !presentationFactModelClose(regionCost, totalCost) {
		return fmt.Errorf("cost reconciliation failed: regions %.2f != total %.2f", regionCost, totalCost)
	}
	if excludedRevenue, exists := presentationFactModelCount(totals, "missing_cost_revenue_excluded_from_margin"); exists && excludedRevenue > 0 {
		allRegionMissingCountsAreZero := len(regions) > 0
		for _, value := range regions {
			region, ok := value.(map[string]interface{})
			if !ok {
				allRegionMissingCountsAreZero = false
				break
			}
			missing, ok := presentationFactModelCount(region, "missing_cost_rows")
			if !ok || missing != 0 {
				allRegionMissingCountsAreZero = false
				break
			}
		}
		if allRegionMissingCountsAreZero {
			return fmt.Errorf("missing-cost revenue %.2f contradicts zero missing-cost rows in every region", excludedRevenue)
		}
	}
	if err := validatePresentationFactModelSources(path, model, includedRows, excludedRows, duplicateRows); err != nil {
		return err
	}
	return nil
}

func presentationFactModelIssue(ctx context.Context, builderPath string) string {
	workspace := strings.TrimSpace(projectctx.ResolvedWorkspace(ctx))
	if workspace == "" || strings.TrimSpace(builderPath) == "" {
		return ""
	}
	candidates := make([]string, 0, 4)
	for _, builder := range presentationCandidateAbsolutePaths(workspace, builderPath) {
		candidates = append(candidates, filepath.Join(filepath.Dir(builder), "fact_model.json"))
	}
	candidates = append(candidates,
		filepath.Join(workspace, ".openkitty", "storage", "fact_model.json"),
		filepath.Join(workspace, "storage", "fact_model.json"),
	)
	seen := make(map[string]struct{}, len(candidates))
	for _, factPath := range candidates {
		key := strings.ToLower(filepath.Clean(factPath))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		if info, err := os.Stat(factPath); err != nil || info.IsDir() {
			continue
		}
		if err := validatePresentationFactModel(factPath); err != nil {
			return fmt.Sprintf("%s: %v", factPath, err)
		}
		return ""
	}
	return ""
}

func presentationEvidenceNoteKind(note string) (sourceBacked, planning, unknown, neutral bool) {
	lower := strings.ToLower(strings.TrimSpace(note))
	sourceBacked = presentationContainsAny(lower,
		"user evidence", "project evidence", "local evidence", "public evidence",
		"数据来源 local", "来源 local", "evidence: local",
		"用户陈述", "用户确认", "用户材料", "来源：用户", "source: user",
		"项目方陈述", "项目方材料", "项目资料", "来源：项目", "source: project",
		"本地材料", "本地文件", "来源：本地", "source: local", "已核验", "公开来源", "source: public",
		"来源：http://", "来源：https://", "source: http://", "source: https://",
	)
	planning = presentationContainsAny(lower,
		"规划估算（非项目事实）", "规划估算(非项目事实)", "planning estimate (not a project fact)",
	)
	unknown = presentationContainsAny(lower, "待确认", "未统计", "未提供", "未知")
	neutral = presentationContainsAny(lower, "不含新增事实", "no new factual claims")
	return
}

func presentationSpecText(value interface{}) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func presentationNormalizedEvidenceText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), ""))
}

func presentationEvidenceExcerptGrounded(excerpt, sourceContext string) bool {
	excerpt = presentationNormalizedEvidenceText(excerpt)
	if len([]rune(excerpt)) < 10 {
		return false
	}
	return strings.Contains(presentationNormalizedEvidenceText(sourceContext), excerpt)
}

func presentationSpecFlattenText(value interface{}) string {
	var parts []string
	var walk func(interface{})
	walk = func(current interface{}) {
		switch typed := current.(type) {
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				parts = append(parts, text)
			}
		case json.Number:
			parts = append(parts, typed.String())
		case float64, float32, int, int32, int64, uint, uint32, uint64:
			parts = append(parts, fmt.Sprint(typed))
		case []interface{}:
			for _, item := range typed {
				walk(item)
			}
		case map[string]interface{}:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(typed[key])
			}
		}
	}
	walk(value)
	return strings.Join(parts, "\n")
}

func presentationSlideAudienceText(slide map[string]interface{}) string {
	content := make(map[string]interface{}, len(slide))
	for key, value := range slide {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "kind", "narrative_job", "focal_anchor", "evidence_form", "composition_family", "source_note", "evidence_refs":
			continue
		default:
			content[key] = value
		}
	}
	return presentationSpecFlattenText(content)
}

func presentationSpecEvidenceRefs(value interface{}) []string {
	values, ok := value.([]interface{})
	if !ok {
		return nil
	}
	refs := make([]string, 0, len(values))
	for _, raw := range values {
		if ref := strings.TrimSpace(fmt.Sprint(raw)); ref != "" {
			refs = append(refs, ref)
		}
	}
	return textutil.UniqueStrings(refs)
}

func validateSourceBoundPresentationSpec(path, sourceContext string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	var spec struct {
		Meta           map[string]interface{}   `json:"meta"`
		EvidencePolicy interface{}              `json:"evidence_policy"`
		ArtDirection   map[string]interface{}   `json:"art_direction"`
		EvidenceLedger []map[string]interface{} `json:"evidence_ledger"`
		Slides         []map[string]interface{} `json:"slides"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return fmt.Errorf("cannot parse %s: %w", path, err)
	}
	policyValue := spec.EvidencePolicy
	if presentationSpecText(policyValue) == "" {
		policyValue = spec.Meta["evidence_policy"]
	}
	policy := strings.ToLower(presentationSpecText(policyValue))
	var issues []string
	if !presentationContainsAny(policy, "source-bound", "evidence-bound", "事实约束", "证据约束") {
		issues = append(issues, "evidence_policy must be source-bound")
	}
	visualThesis := presentationSpecText(spec.ArtDirection["visual_thesis"])
	compositionStrategy := presentationSpecText(spec.ArtDirection["composition_strategy"])
	assetStrategy := presentationSpecText(spec.ArtDirection["asset_strategy"])
	paletteStrategy := presentationSpecText(spec.ArtDirection["palette_strategy"])
	typographyStrategy := presentationSpecText(spec.ArtDirection["typography_strategy"])
	backgroundRhythm := presentationSpecText(spec.ArtDirection["background_rhythm"])
	if visualThesis == "" || compositionStrategy == "" || assetStrategy == "" ||
		paletteStrategy == "" || typographyStrategy == "" || backgroundRhythm == "" {
		issues = append(issues, "art_direction must define visual_thesis, composition_strategy, asset_strategy, palette_strategy, typography_strategy, and background_rhythm")
	}
	if len(spec.Slides) < 2 {
		issues = append(issues, "deck must contain at least two slides")
	}
	ledger := make(map[string]string, len(spec.EvidenceLedger))
	for index, entry := range spec.EvidenceLedger {
		id := presentationSpecText(entry["id"])
		source := presentationSpecText(entry["source"])
		locator := presentationSpecText(entry["locator"])
		excerpt := presentationSpecText(entry["excerpt"])
		if !presentationEvidenceIDPattern.MatchString(id) {
			issues = append(issues, fmt.Sprintf("evidence_ledger[%d] needs a stable id using letters, numbers, dot, underscore, or hyphen", index))
			continue
		}
		if _, exists := ledger[id]; exists {
			issues = append(issues, fmt.Sprintf("evidence_ledger contains duplicate id %q", id))
			continue
		}
		sourceBacked, planning, unknown, neutral := presentationEvidenceNoteKind(source)
		if source == "" || !sourceBacked || planning || unknown || neutral {
			issues = append(issues, fmt.Sprintf("evidence_ledger[%s] source must identify user, local, project, or public evidence", id))
		}
		if locator == "" {
			issues = append(issues, fmt.Sprintf("evidence_ledger[%s] needs a page, section, row, timestamp, or URL locator", id))
		}
		if !presentationEvidenceExcerptGrounded(excerpt, sourceContext) {
			issues = append(issues, fmt.Sprintf("evidence_ledger[%s] excerpt is too short or is not present in trusted user/local/direct-source evidence", id))
			continue
		}
		ledger[id] = excerpt
	}
	for index, slide := range spec.Slides {
		kind := strings.ToLower(presentationSpecText(slide["kind"]))
		if index == 0 || kind == "cover" || kind == "title" {
			continue
		}
		if presentationSpecText(slide["narrative_job"]) == "" ||
			presentationSpecText(slide["focal_anchor"]) == "" ||
			presentationSpecText(slide["evidence_form"]) == "" ||
			presentationSpecText(slide["composition_family"]) == "" {
			issues = append(issues, fmt.Sprintf("slide %d must define narrative_job, focal_anchor, evidence_form, and composition_family before rendering", index+1))
		}
		note := presentationSpecText(slide["source_note"])
		sourceBacked, planning, unknown, neutral := presentationEvidenceNoteKind(note)
		if note == "" || (!sourceBacked && !planning && !unknown && !neutral) {
			issues = append(issues, fmt.Sprintf("slide %d needs a visible source_note with an exact marker such as source: local - <path>, source: user, source: public - <URL>, 规划估算（非项目事实）, 待确认/未统计, or no new factual claims", index+1))
			continue
		}
		audienceText := presentationSlideAudienceText(slide)
		numericClaims := textutil.UniqueStrings(pptPolishNumericClaims(audienceText))
		if planning {
			continue
		}
		if (unknown || neutral) && len(numericClaims) > 0 {
			issues = append(issues, fmt.Sprintf("slide %d contains numeric claims (%s) but its source_note says the values are unknown or that no facts were added", index+1, strings.Join(numericClaims, "、")))
			continue
		}
		if unknown || neutral {
			continue
		}
		if !sourceBacked {
			continue
		}

		refs := presentationSpecEvidenceRefs(slide["evidence_refs"])
		if len(refs) == 0 && !neutral && !unknown {
			issues = append(issues, fmt.Sprintf("slide %d is source-backed and must cite one or more evidence_refs from evidence_ledger", index+1))
			continue
		}
		var referencedExcerpts []string
		for _, ref := range refs {
			excerpt, exists := ledger[ref]
			if !exists {
				issues = append(issues, fmt.Sprintf("slide %d evidence_ref %q does not resolve to a grounded evidence_ledger entry", index+1, ref))
				continue
			}
			referencedExcerpts = append(referencedExcerpts, excerpt)
		}
		referencedText := strings.Join(referencedExcerpts, "\n")
		allowedNumbers := make(map[string]bool)
		for _, claim := range pptPolishNumericClaims(referencedText) {
			allowedNumbers[claim] = true
		}
		var unsupported []string
		for _, claim := range numericClaims {
			if !allowedNumbers[claim] {
				unsupported = append(unsupported, claim)
			}
		}
		if len(unsupported) > 0 {
			issues = append(issues, fmt.Sprintf("slide %d contains numeric claims not present in its referenced evidence excerpts (%s)", index+1, strings.Join(textutil.UniqueStrings(unsupported), "、")))
		}

		upperEvidence := strings.ToUpper(referencedText)
		var unsupportedTokens []string
		for _, token := range presentationTechnicalTokenPattern.FindAllString(audienceText, -1) {
			if !strings.Contains(upperEvidence, token) {
				unsupportedTokens = append(unsupportedTokens, token)
			}
		}
		if unsupportedTokens = textutil.UniqueStrings(unsupportedTokens); len(unsupportedTokens) > 0 {
			issues = append(issues, fmt.Sprintf("slide %d contains technical acronyms absent from its referenced evidence excerpts (%s)", index+1, strings.Join(unsupportedTokens, "、")))
		}
	}
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("source-bound deck-spec validation failed: %s", strings.Join(textutil.UniqueStrings(issues), " | "))
}

func checkPresentationToolGate(ctx context.Context, toolCall ToolCall) (ToolResult, error, bool) {
	state := presentationProductionStateFromContext(ctx)
	if state == nil {
		return ToolResult{}, nil, false
	}
	if trustedActionIntent(toolCall) != "" {
		if issues := presentationBlockingIssues(ctx); len(issues) > 0 {
			return presentationGateBlockedResult(toolCall,
				"用户要求发布包含 PPT 的交付物，但关键交付条件尚未满足："+strings.Join(issues, "；")+"。正式对外稿必须先完成可定位的 PPTX、结构审计、真实渲染和视觉检查；只有用户明确选择快速内部草稿或关闭严格验收时，缺失项才可作为告警披露。")
		}
	}
	payload := presentationToolPayload(toolCall)
	skillName := normalizedPresentationSkillName(toolCall)
	if skillName != "" {
		state.mu.Lock()
		expected := state.Expected
		strictQA := state.StrictQA
		activated := state.ArtDirectorActivated
		activatedSkill := state.ActivatedSkill
		route := state.Route
		level2BuildFailures := state.Level2BuildFailures
		taskSpecificBuilderRequired := state.TaskSpecificBuilderRequired
		pptMasterAllowed := state.PPTMasterAllowed
		pptMasterBuildFailures := state.PPTMasterBuildFailures
		pptMasterUnavailable := state.PPTMasterUnavailable
		baseAllowed := presentationDisasterFallbackReady(
			state.BasePPTHandoffAllowed,
			taskSpecificBuilderRequired,
			level2BuildFailures,
			pptMasterAllowed,
			pptMasterBuildFailures,
			pptMasterUnavailable,
		)
		state.mu.Unlock()
		if expected && taskSpecificBuilderRequired && skillName == "base-ppt" {
			return presentationGateBlockedResult(toolCall, "用户明确要求任务专属构建器；本次不得切换或降级到 base-ppt，必须修正现有 deck_spec.json 并继续同一个任务 builder。")
		}
		if expected && skillName == "base-ppt" && !baseAllowed {
			if pptMasterAllowed && level2BuildFailures >= 2 && !pptMasterUnavailable && pptMasterBuildFailures < 2 {
				return presentationGateBlockedResult(toolCall, "base-ppt 是最后灾备，不是正常选路。Level 2 已完成初次构建和一次针对性修正但仍失败；本任务适合精美路线，下一步必须切换到 ppt-master。只有 ppt-master 客观不可用，或其最终导出在一次修正后仍连续失败，才允许公开降级。")
			}
			return presentationGateBlockedResult(toolCall, "base-ppt 是最后灾备，不是正常选路。请先完成当前 Level 2/精美路线的真实构建与一次针对性修正；命令猜测、目录探测、依赖检查或源码写入失败都不算生产路线失败。")
		}
		if expected && strictQA {
			if skillName == "ppt-art-director" && activated {
				return presentationSkillCheckpointResult(toolCall, state)
			}
			if skillName != "ppt-art-director" && !activated {
				return presentationGateBlockedResult(toolCall, "严格验收任务必须先激活 ppt-art-director，再选择并执行生产路线。")
			}
			if activated && route == "level2-task-builder" {
				switch skillName {
				case "ppt-master":
					if !pptMasterAllowed {
						return presentationGateBlockedResult(toolCall, "当前普通演示任务已选择 Level 2；没有高端发布、董事会、投资人或客户提案需求，不能切换到 ppt-master。请继续已选路线：所有正式稿无论页数都先运行脚手架，把 deck_spec.json 作为事实契约，并在任务 builder 中完成逐页 renderer；独立紧凑构建仅用于明确的简单、快速、内部、周期性或固定结构稿。")
					}
				case "base-ppt":
					if !baseAllowed {
						return presentationGateBlockedResult(toolCall, "当前严格验收任务已选择精美路线；base-ppt 仅在用户明确要求快速/内部固定结构，或所有适用的精美路线均完成真实构建与针对性修正后仍失败时才可公开降级。")
					}
				}
			}
			if activated && skillName == activatedSkill {
				return presentationSkillCheckpointResult(toolCall, state)
			}
		}
		return ToolResult{}, nil, false
	}
	state.mu.Lock()
	expected := state.Expected
	strictQA := state.StrictQA
	activated := state.ArtDirectorActivated
	activatedSkill := state.ActivatedSkill
	route := state.Route
	scaffolded := state.Scaffolded
	builderRead := state.BuilderRead
	minimumSlides := state.RequestedMinimumSlides
	maximumSlides := state.RequestedMaximumSlides
	pptPolishContract := state.PPTPolishContract
	contractDeckPath := state.ContractDeckPath
	contractWorkDir := state.ContractWorkDir
	contractPreviewDir := state.ContractPreviewDir
	boundedRevision := state.BoundedRevision
	revisionMode := state.RevisionMode
	revisionBuildLimit := state.RevisionBuildLimit
	revisionBuildCount := state.RevisionBuildCount
	revisionMutationCount := state.RevisionMutationCount
	requiredBodyFontSize := state.RequiredBodyFontSize
	requiredTitleFontSize := state.RequiredTitleFontSize
	sourceDirty := state.SourceDirty
	auditFailed := state.AuditFailed
	auditFailureMutationCount := state.AuditFailureMutationCount
	auditPassed := state.AuditPassed
	deckCreated := state.DeckCreated
	rendered := state.Rendered
	renderTarget := state.RenderTarget
	visualCoverageComplete := state.VisualCoverageComplete
	aestheticQAPassed := state.AestheticQAPassed
	visualDefectsDetected := state.VisualDefectsDetected
	builderPath := state.BuilderPath
	substantive := state.Substantive
	perSlideVisionOptOut := state.PerSlideVisionOptOut
	renderedSlideCount := state.RenderedSlideCount
	deckSlideCount := state.DeckSlideCount
	inspectedSlideCount := presentationDistinctInspectedSlideCount(
		state.VisualInspectedImages,
		max(state.RenderedSlideCount, state.DeckSlideCount),
	)
	perSlideCustomizationRequired := state.PerSlideCustomizationRequired
	builderCustomized := state.BuilderCustomized
	sourceContext := state.SourceContext
	pptPolishExecution := state.PPTPolishExecution
	pptPolishRequest := state.PPTPolishRequest
	baseAllowed := presentationDisasterFallbackReady(
		state.BasePPTHandoffAllowed,
		state.TaskSpecificBuilderRequired,
		state.Level2BuildFailures,
		state.PPTMasterAllowed,
		state.PPTMasterBuildFailures,
		state.PPTMasterUnavailable,
	)
	state.mu.Unlock()
	action := permissionpolicy.ExtractAction(toolCall.Arguments)
	expectedVisualSlides := max(renderedSlideCount, deckSlideCount)
	perSlideVisualComplete := !substantive || perSlideVisionOptOut ||
		(expectedVisualSlides > 0 && inspectedSlideCount >= expectedVisualSlides)
	if expected && !deckCreated && isPrematurePresentationQAArtifactWrite(toolCall) {
		nextStep := "下一步先调用 use_skill 激活 ppt-art-director（仅一次），然后严格使用返回的 Runtime Directory 运行 scaffold_task_pptx.py。"
		if activated {
			nextStep = "ppt-art-director 已激活；不要重复加载。下一步直接使用它已返回的 Runtime Directory 运行 scaffold_task_pptx.py。"
		}
		return presentationGateBlockedResult(toolCall, "不能在真实 PPTX 生成前先写验收报告或修复日志，也不能用计划表冒充已执行 QA。"+nextStep+"完成 deck_spec.json 和逐页 renderer，构建可打开的 PPTX；之后再审计、渲染、看图并写 ppt_qa.md。")
	}
	if expected && strictQA && isPrematurePresentationQAArtifactWrite(toolCall) && (!auditPassed || !rendered || !visualCoverageComplete || visualDefectsDetected) {
		return presentationGateBlockedResult(toolCall, "ppt_qa.md may be written only after the exact final PPTX passes the built-in structural audit, is rendered by the built-in real-office renderer, and every rendered slide is visually inspected with no unresolved defects.")
	}
	if expected && strictQA && substantive && isPresentationAestheticQAArtifactWrite(toolCall) && !perSlideVisualComplete {
		return presentationGateBlockedResult(toolCall, fmt.Sprintf("aesthetic_qa.json must be authored from separate full-size vision inspection of every rendered slide; current coverage is %d/%d. A contact sheet is deck-flow evidence only.", inspectedSlideCount, expectedVisualSlides))
	}
	if expected && strictQA && substantive && isPrematurePresentationQAArtifactWrite(toolCall) &&
		!isPresentationAestheticQAArtifactWrite(toolCall) && (!perSlideVisualComplete || !aestheticQAPassed) {
		return presentationGateBlockedResult(toolCall, "The final PPT QA report comes after individual vision coverage and a passing built-in aesthetic QA receipt. Complete aesthetic_qa.json and validate it against the exact deck first.")
	}
	if expected && isPresentationAestheticQAAction(action) {
		if !isPresentationCanonicalAestheticQAAction(action) {
			return presentationGateBlockedResult(toolCall, "Use only the current ppt-art-director Runtime Directory scripts/validate_aesthetic_qa.py; a workspace-local validator cannot create trusted aesthetic evidence.")
		}
		if strictQA && substantive && (!auditPassed || !rendered || !perSlideVisualComplete || visualDefectsDetected) {
			return presentationGateBlockedResult(toolCall, fmt.Sprintf("Aesthetic QA validation is the final visual gate. First pass structural audit, native render, and separate vision inspection for every slide with no unresolved defects; current individual coverage is %d/%d.", inspectedSlideCount, expectedVisualSlides))
		}
		if aestheticQAPassed {
			return presentationGateBlockedResult(toolCall, "The exact final deck already has a passing aesthetic QA receipt; do not rerun the validator unless the deck is rebuilt.")
		}
	}
	if expected && isCounterfeitPresentationQAHelperAction(toolCall, payload) {
		return presentationGateBlockedResult(toolCall, "Do not create or run workspace-local render/audit/probe helpers for presentation QA. Use only the current ppt-art-director Runtime Directory scripts/audit_pptx.py and scripts/render_pptx.py (or render_pptx.ps1). If desktop PowerPoint is already in use, leave it untouched and run the built-in Python renderer through an already available WSL LibreOffice/Poppler environment.")
	}
	if expected {
		if method, argument, unsafe := unsafePresentationGeometrySource(toolCall); unsafe {
			return presentationGateBlockedResult(toolCall, fmt.Sprintf(
				"演示文稿 builder 的 %s 几何参数 %q 没有在 python-pptx 调用边界转换为整数 EMU；英寸标量请逐项使用 Inches(...) 或 _I(...)，已有 EMU 表达式请显式使用 int(...)。禁止把原始浮点坐标写入 PPTX。",
				method,
				truncate(argument, 120),
			))
		}
	}
	if expected {
		if issue := presentationSpecPageBoundIssue(ctx, toolCall, minimumSlides, maximumSlides); issue != "" {
			return presentationGateBlockedResult(toolCall, issue)
		}
	}
	if expected && strictQA && substantive && isPresentationAuditAction(action) {
		if issue := presentationAuditFontFloorIssue(action, requiredBodyFontSize, requiredTitleFontSize); issue != "" {
			return presentationGateBlockedResult(toolCall, issue)
		}
	}
	if expected && toolCall.Name == "convert" && presentationContainsAny(strings.ToLower(payload), "ppt", "powerpoint", "presentation") && route != "level1-base-ppt" {
		if !baseAllowed {
			return presentationGateBlockedResult(toolCall, "当前任务尚未满足灾备降级条件，不能用 convert/base-ppt 绕过精美路线。先修复当前定制构建；若本任务适合 PPTMaster，还必须先尝试并修正该精美路线。")
		}
		return presentationGateBlockedResult(toolCall, "灾备条件已成立，但仍必须先显式调用 use_skill base-ppt、记录公开降级原因，再执行 convert；禁止静默切换生成器。")
	}
	if expected && perSlideCustomizationRequired && route == "level2-task-builder" {
		if isPresentationFactModelDirectMutation(toolCall) {
			return presentationGateBlockedResult(toolCall, "fact_model.json 不能由 write_file/edit_file/json_patch 直接编写；请用一次确定性数据分析执行或小型任务脚本从原始表格生成，再完整回读并核对行数与汇总。")
		}
		builderAction := isPresentationBuilderAction(toolCall, payload)
		builderSourceWrite := isPresentationBuilderSourceWrite(toolCall, payload)
		builderExecution := isPresentationBuilderExecutionAction(toolCall, payload)
		creationAction := builderAction || builderSourceWrite || isPresentationScaffoldAction(toolCall, payload)
		if creationAction && !activated {
			return presentationGateBlockedResult(toolCall, "正式演示的逐页定制生产必须先调用 use_skill ppt-art-director；激活后使用其真实运行时脚手架，不能直接写或执行通用 builder。")
		}
		if activated && isPresentationStableSpecBuilderAction(toolCall, payload) {
			return presentationGateBlockedResult(toolCall, "正式演示默认逐页定制，不能用 build_task_pptx.py 的通用 kind 渲染器直接出稿；请先运行 scaffold_task_pptx.py，随后逐页定制任务 builder。独立紧凑路线仅用于明确的简单、快速、内部、周期性或固定结构任务。")
		}
		if activated && isPresentationScaffoldAction(toolCall, payload) && !presentationScaffoldActionStandalone(toolCall) {
			return presentationGateBlockedResult(toolCall, "scaffold_task_pptx.py 必须作为单独命令直接运行；工具已位于任务 workspace，不要前置 cd/mkdir、不要套 cmd.exe/PowerShell，也不要追加管道或退出码探测。")
		}
		if activated && isPresentationInlineExecution(toolCall, payload) {
			return presentationGateBlockedResult(toolCall, "正式演示禁止用 ptc_execute、python -c 或一次性内联脚本绕过逐页定制。请先运行技能脚手架，再在任务 builder 中用有界锚定编辑实现每页构图。")
		}
		if activated && (builderAction || builderSourceWrite) && !scaffolded {
			return presentationGateBlockedResult(toolCall, "正式演示的首次生成代码动作必须是 scaffold_task_pptx.py；脚手架成功前不能写或执行 build_deck.py。")
		}
		if activated && scaffolded && isPresentationBuilderRead(toolCall) && strings.TrimSpace(builderPath) != "" {
			readPath := firstStringArg(toolCall.Arguments, "path", "file_path", "file")
			if !samePresentationPath(readPath, builderPath) {
				return presentationGateBlockedResult(toolCall, fmt.Sprintf("逐页定制只能读取本次脚手架生成的任务 builder %s；禁止改读运行时模板或其他任务 builder：%s。", builderPath, readPath))
			}
		}
		if activated && scaffolded && builderSourceWrite {
			if !builderRead {
				return presentationGateBlockedResult(toolCall, "逐页定制前必须先完整读取一次本次任务的 build_deck.py，再用有锚点的 edit_file/multiedit 修改 TASK PAGE COMPOSITIONS。")
			}
			if strings.EqualFold(strings.TrimSpace(toolCall.Name), "write_file") {
				return presentationGateBlockedResult(toolCall, "脚手架 builder 是受控现有文件；禁止用 write_file 整体覆盖。请只用有锚点的 edit_file/multiedit 修改 TASK PAGE COMPOSITIONS。")
			}
			if !presentationBuilderMutationTargets(toolCall, builderPath) {
				return presentationGateBlockedResult(toolCall, fmt.Sprintf("逐页定制只能修改本次任务 builder %s；当前编辑没有命中该文件。", builderPath))
			}
		}
		if activated && scaffolded && builderExecution && !builderCustomized {
			return presentationGateBlockedResult(toolCall, "正式演示尚未完成逐页定制：先完整读取一次任务 build_deck.py，再用有界锚定编辑为每一页登记独立 renderer；未完成 TASK_PAGE_RENDERERS 映射不得构建。")
		}
	}
	if expected && route == "level3-ppt-master" && activated && activatedSkill == "ppt-art-director" {
		creationAction := isPresentationBuilderAction(toolCall, payload) ||
			isPresentationBuilderSourceWrite(toolCall, payload) ||
			isPresentationScaffoldAction(toolCall, payload)
		if creationAction {
			return presentationGateBlockedResult(toolCall, "This beauty-first/high-stakes deck is routed to Level 3. Call use_skill ppt-master now and run its full Strategist/Executor workflow before any scaffold or alternate builder action.")
		}
	}
	if expected && substantive && isPresentationBuilderExecutionAction(toolCall, payload) && strings.TrimSpace(projectctx.ResolvedWorkspace(ctx)) != "" {
		if issue := presentationFactModelIssue(ctx, builderPath); issue != "" {
			return presentationGateBlockedResult(toolCall, "正式构建前 fact_model.json 对账失败；请只修正确定性分析脚本并重新生成、完整回读，再重试 builder："+issue)
		}
		specPath, err := presentationSourceBoundSpecPath(ctx, toolCall, builderPath)
		if err != nil {
			return presentationGateBlockedResult(toolCall,
				"正式演示必须先形成任务目录中的 deck_spec.json，并逐页声明事实来源；"+err.Error()+"。")
		}
		if err := validateSourceBoundPresentationSpec(specPath, sourceContext); err != nil {
			return presentationGateBlockedResult(toolCall,
				"正式演示构建前事实边界校验失败；请只修正本次 deck_spec.json 后重试："+truncate(err.Error(), 1800))
		}
	}
	if expected && pptPolishContract {
		if issue := pptPolishContractToolIssue(
			toolCall, payload, contractDeckPath, contractWorkDir, contractPreviewDir, builderPath,
			scaffolded, boundedRevision, revisionMode, revisionBuildLimit, revisionBuildCount, revisionMutationCount,
			requiredBodyFontSize, requiredTitleFontSize, sourceDirty, auditFailed, auditFailureMutationCount,
			auditPassed, rendered, renderTarget,
		); issue != "" {
			return presentationGateBlockedResult(toolCall, issue)
		}
		if scaffolded && isPresentationBuilderAction(toolCall, permissionpolicy.ExtractAction(toolCall.Arguments)) && pptPolishExecution != nil {
			workspace := strings.TrimSpace(projectctx.ResolvedWorkspace(ctx))
			if workspace == "" {
				workspace = strings.TrimSpace(pptPolishExecution.ResolvedWorkspace)
			}
			if err := validatePPTPolishDeckSpecGrounding(pptPolishExecution, pptPolishRequest, workspace); err != nil {
				return presentationGateBlockedResult(toolCall,
					"PPT 构建前内容保真校验失败；请只修正本次任务 deck_spec.json 后再直接运行 builder："+truncate(err.Error(), 1400))
			}
		}
	}

	if expected && isCounterfeitPresentationScaffoldAction(toolCall, payload) {
		return presentationGateBlockedResult(toolCall, "不能把用户存储中的 scaffold_task_pptx.py 冒充内置运行时脚本；请使用实际运行时脚本或其他可用的 PPT 生成方式。")
	}
	if expected && route == "qa-existing-deck" {
		return ToolResult{}, nil, false
	}
	if expected && route == "level2-task-builder" {
		if scaffolded && isPresentationScaffoldAction(toolCall, payload) {
			return presentationGateBlockedResult(toolCall, "任务专用脚手架已经成功创建；不要重新运行 scaffold_task_pptx.py。继续使用同目录 deck_spec.json 作为事实契约，并在现有 build_deck.py 中完成逐页构图。")
		}
		if activated && !scaffolded && isPresentationBuilderRead(toolCall) {
			return presentationGateBlockedResult(toolCall, "先单独执行 scaffold_task_pptx.py 并确认成功；随后完整读取一次它生成的任务 build_deck.py。不要把脚手架创建和 builder 读取放进同一批并行调用。")
		}
		if activated && isPresentationDesignHelperRead(toolCall) {
			return presentationGateBlockedResult(toolCall, "ppt-art-director 已提供并由脚手架复制可用的 pptx_design.py；不要读取或改写设计辅助库。逐页定制只读取一次任务 build_deck.py，并在 TASK PAGE COMPOSITIONS 区域做有界锚定修改。")
		}
		if builderRead && isPresentationBuilderRead(toolCall) && !presentationBoundedBuilderReread(toolCall) {
			return presentationGateBlockedResult(toolCall, "build_deck.py 已在本步骤读取过（且已完整读取）；不要重复读取。继续编辑 deck_spec.json，并在 TASK PAGE COMPOSITIONS 中做有界锚定修改。")
		}
	}
	// Ordinary production may fall back to an equivalent capable workflow when
	// one bundled route is unavailable. An explicit strict-QA request is
	// different: it promises a reproducible production chain, so do not let a
	// builder bypass the current-turn Skill activation and Level-2 scaffold.
	if expected && strictQA {
		creationAction := isPresentationBuilderAction(toolCall, payload) ||
			isPresentationBuilderSourceWrite(toolCall, payload) ||
			isPresentationScaffoldAction(toolCall, payload)
		if creationAction && !activated {
			return presentationGateBlockedResult(toolCall, "严格验收的 PPT 生产开始前必须先调用 use_skill ppt-art-director；该状态只对当前任务有效。")
		}
		if !activated {
			return ToolResult{}, nil, false
		}
		if isPresentationInlineExecution(toolCall, payload) {
			return presentationGateBlockedResult(toolCall, "禁止用 ptc_execute、python -c 或一次性内联大脚本创建 PPTX。Level 2 必须先运行技能提供的 scaffold_task_pptx.py，并在落盘 builder 中完成逐页定制。")
		}
		if route == "level2-task-builder" {
			if toolCall.Name == "convert" && strings.Contains(strings.ToLower(payload), "ppt") {
				return presentationGateBlockedResult(toolCall, "当前选择的是 Level 2 任务专用 renderer，不能用 convert/base-ppt 偷换路线；先脚手架，再执行落盘 builder。")
			}
			if (isPresentationBuilderAction(toolCall, payload) || isPresentationBuilderSourceWrite(toolCall, payload)) && !scaffolded {
				return presentationGateBlockedResult(toolCall, "Level 2 的首次生成代码动作必须是 scaffold_task_pptx.py；脚手架成功前不能写或执行 build_deck.py。正式稿脚手架成功后还必须完成逐页 builder 定制。")
			}
		}
	}
	return ToolResult{}, nil, false
}

func presentationToolSucceeded(toolCall ToolCall, result ToolResult, err error) bool {
	result.Name = toolCall.Name
	if !ToolResultExecutionSucceeded(result, err) {
		return false
	}
	lower := strings.ToLower(result.Content)
	return !presentationContainsAny(lower,
		"技能执行失败", "skill execution failed", "[blocked:", "status: failed", "status=failed",
		"traceback (most recent call last)", "syntaxerror:", "indentationerror:", "nameerror:",
		"typeerror:", "valueerror:", "modulenotfounderror:", "filenotfounderror:",
		"command not found", "no such file or directory",
	)
}

func presentationToolOutputPayload(result string) string {
	lower := strings.ToLower(result)
	if index := strings.LastIndex(lower, "output result:"); index >= 0 {
		return strings.TrimSpace(result[index+len("output result:"):])
	}
	return strings.TrimSpace(result)
}

func presentationBuilderResultConfirmsCreation(result, builtPath string) bool {
	if presentationOutputHasFailedExit(result) {
		return false
	}
	payload := presentationToolOutputPayload(result)
	if payload == "" {
		return false
	}
	reported := firstPresentationPath(payload)
	// Older trusted tool receipts used a compact status-only form. Keep that
	// compatibility for non-envelope results; real shell envelopes must report
	// the file from command output rather than merely mentioning it in argv.
	compactSuccessReceipt := !strings.Contains(strings.ToLower(result), "output result:") &&
		presentationContainsAny(strings.ToLower(payload), "status: success", "status=succeeded", "status: succeeded")
	if reported == "" && !compactSuccessReceipt {
		return false
	}
	if builtPath != "" {
		if reported != "" && !samePresentationPath(reported, builtPath) {
			return false
		}
	}
	lower := strings.ToLower(payload)
	return compactSuccessReceipt || reported != "" || presentationContainsAny(lower,
		"saved ", "saved:", "created ", "created:", "generated ", "generated:",
		"wrote ", "written ", "已保存", "已生成", "已创建", "写入成功",
		`"success":true`, `"ok":true`,
	)
}

func firstPresentationPath(values ...string) string {
	for _, value := range values {
		if match := presentationFilePattern.FindString(value); match != "" {
			return strings.Trim(strings.TrimSpace(match), "\"'`.,;，。；）)]}")
		}
	}
	return ""
}

func allPresentationPaths(values ...string) []string {
	seen := make(map[string]bool)
	var paths []string
	for _, value := range values {
		for _, match := range presentationFilePattern.FindAllString(value, -1) {
			path := strings.Trim(strings.TrimSpace(match), "\"'`.,;[]{}()")
			key := normalizedPresentationPath(path)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			paths = append(paths, path)
		}
	}
	return paths
}

func presentationToolCanObserveArtifact(toolCall ToolCall, payload string) bool {
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	if presentationContainsAny(name,
		"ptc_execute", "cli", "persistent_shell", "shell", "command",
		"convert", "render", "audit", "validate",
	) {
		return true
	}
	if presentationContainsAny(name, "write_file", "write") {
		path := firstStringArg(toolCall.Arguments, "path", "file_path", "file", "output", "output_path")
		return strings.EqualFold(filepath.Ext(strings.TrimSpace(path)), ".pptx")
	}
	return isPresentationBuilderAction(toolCall, payload)
}

func recordPresentationCandidatePaths(state *presentationProductionState, toolCall ToolCall, payload, result string) {
	if state == nil || !presentationToolCanObserveArtifact(toolCall, payload) {
		return
	}
	if state.CandidatePaths == nil {
		state.CandidatePaths = make(map[string]struct{})
	}
	for _, path := range allPresentationPaths(payload, result) {
		state.CandidatePaths[path] = struct{}{}
	}
}

func presentationToolDeckPath(toolCall ToolCall, payload, result string) string {
	for _, key := range []string{"output_path", "output", "pptx_path", "pptx", "file_path", "file", "input_path", "input", "path"} {
		value, _ := toolCall.Arguments[key].(string)
		if path := firstPresentationPath(value); path != "" {
			return path
		}
	}
	if command := firstStringArg(toolCall.Arguments, "command", "script", "code", "args"); command != "" {
		if path := firstPresentationPath(command); path != "" {
			return path
		}
	}
	return firstPresentationPath(payload, result)
}

func normalizedPresentationPath(raw string) string {
	// Preserve a leading dot in workspace-relative paths such as
	// .openkitty/storage/build_deck.py. Trimming '.' from both ends made that
	// path differ from the same file reported as an absolute path.
	value := strings.Trim(strings.TrimSpace(raw), "\"'`,;，。；（）()[]{}")
	value = strings.TrimRight(value, ".")
	if value == "" {
		return ""
	}
	value = portablepath.Normalize(value)
	return strings.ToLower(strings.TrimSuffix(value, "/"))
}

func presentationPathVariants(raw string) []string {
	value := normalizedPresentationPath(raw)
	if value == "" {
		return nil
	}
	values := []string{value}
	if !portablepath.IsAbs(value) {
		switch {
		case strings.HasPrefix(value, ".openkitty/"):
			values = append(values, strings.TrimPrefix(value, "."))
		case strings.HasPrefix(value, "openkitty/"):
			values = append(values, "."+value)
		}
	}
	return textutil.UniqueStrings(values)
}

func samePresentationPath(left, right string) bool {
	aValues := presentationPathVariants(left)
	bValues := presentationPathVariants(right)
	if len(aValues) == 0 || len(bValues) == 0 {
		return false
	}
	for _, a := range aValues {
		for _, b := range bValues {
			if a == b {
				return true
			}
			// A renderer may report an absolute path while the builder used a
			// path relative to the workspace. Accept only a full path-component
			// suffix, not a loose basename substring.
			if strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a) {
				return true
			}
		}
	}
	return false
}

func presentationEvidenceTargetsDeck(currentPath, evidencePath string) bool {
	if strings.TrimSpace(currentPath) == "" {
		return strings.TrimSpace(evidencePath) != ""
	}
	return samePresentationPath(currentPath, evidencePath)
}

func firstPresentationImagePath(values ...string) string {
	for _, value := range values {
		if match := presentationImagePattern.FindString(value); match != "" {
			return strings.Trim(strings.TrimSpace(match), "\"'`.,;，。；（）()[]{}")
		}
	}
	return ""
}

func presentationRenderTarget(toolCall ToolCall, payload, result string) string {
	for _, key := range []string{"output_dir", "outdir", "preview_dir", "render_dir", "destination", "dest", "output"} {
		value, _ := toolCall.Arguments[key].(string)
		value = strings.TrimSpace(value)
		if value != "" && firstPresentationPath(value) == "" {
			return value
		}
	}
	command := firstStringArg(toolCall.Arguments, "command", "script", "code", "args")
	for _, source := range []string{command, payload} {
		match := presentationRenderOutputFlagPattern.FindStringSubmatch(source)
		if len(match) <= 1 {
			continue
		}
		for _, candidate := range match[1:] {
			if strings.TrimSpace(candidate) != "" && firstPresentationPath(candidate) == "" {
				return strings.TrimSpace(candidate)
			}
		}
	}
	if imagePath := firstPresentationImagePath(result); imagePath != "" {
		return filepath.Dir(imagePath)
	}
	return ""
}

func presentationVisualPathTargetsRender(imagePath, renderTarget string) bool {
	image := normalizedPresentationPath(imagePath)
	target := normalizedPresentationPath(renderTarget)
	if image == "" || target == "" {
		return false
	}
	if samePresentationPath(image, target) {
		return true
	}
	if presentationImagePattern.MatchString(target) {
		return false
	}
	return presentationPathWithinContract(image, target)
}

func presentationVisualImagePaths(toolCall ToolCall) []string {
	if toolCall.Arguments == nil {
		return nil
	}
	seen := make(map[string]bool)
	var paths []string
	appendValue := func(value string) {
		for _, match := range presentationImagePattern.FindAllString(value, -1) {
			path := strings.Trim(strings.TrimSpace(match), "\"'`.,;，。；（）()[]{}")
			key := normalizedPresentationPath(path)
			if key != "" && !seen[key] {
				seen[key] = true
				paths = append(paths, path)
			}
		}
	}
	for _, key := range []string{"path", "file", "file_path", "image", "images", "paths", "files"} {
		switch value := toolCall.Arguments[key].(type) {
		case string:
			appendValue(value)
		case []string:
			for _, item := range value {
				appendValue(item)
			}
		case []interface{}:
			for _, item := range value {
				if text, ok := item.(string); ok {
					appendValue(text)
				}
			}
		}
	}
	return paths
}

func presentationVisualTargetsRender(toolCall ToolCall, renderTarget string) bool {
	if strings.TrimSpace(renderTarget) == "" {
		return false
	}
	for _, path := range presentationVisualImagePaths(toolCall) {
		if presentationVisualPathTargetsRender(path, renderTarget) {
			return true
		}
	}
	// Some trusted QA tools inspect the whole render directory and return a
	// structured all-pages receipt instead of accepting individual image paths.
	for _, key := range []string{"path", "directory", "dir", "render_dir", "preview_dir", "output_dir"} {
		if value, _ := toolCall.Arguments[key].(string); samePresentationPath(value, renderTarget) {
			return true
		}
	}
	return false
}

func presentationReportedSlideCount(result string) int {
	best := 0
	for _, pattern := range []*regexp.Regexp{presentationJSONSlideCountPattern, presentationTextSlideCountPattern} {
		for _, match := range pattern.FindAllStringSubmatch(result, -1) {
			for _, raw := range match[1:] {
				if count, err := strconv.Atoi(raw); err == nil && count > best {
					best = count
				}
			}
		}
	}
	if best > 0 {
		return best
	}
	seen := make(map[string]bool)
	for _, match := range presentationImagePattern.FindAllString(result, -1) {
		path := normalizedPresentationPath(match)
		base := strings.ToLower(filepath.Base(path))
		if path == "" || presentationContainsAny(base, "contact_sheet", "contact-sheet", "contactsheet", "montage") {
			continue
		}
		seen[path] = true
	}
	return len(seen)
}

func presentationVisualCoverageComplete(toolCall ToolCall, result string, expected int, inspected map[string]bool) bool {
	if presentationEvidenceMarker(result,
		`"all_slides_inspected":true`, `"all_pages_inspected":true`, `"coverage":"all"`,
		`"visual_qa_passed":true`, `"presentation_visual_valid":true`,
		"all slides inspected", "all pages inspected", "逐页检查完成", "所有页面检查完成", "全部幻灯片检查完成", "全量页面检查完成",
	) {
		return true
	}
	for _, path := range presentationVisualImagePaths(toolCall) {
		base := strings.ToLower(filepath.Base(normalizedPresentationPath(path)))
		if presentationContainsAny(base, "contact_sheet", "contact-sheet", "contactsheet", "montage", "all_slides", "all-slides") {
			return true
		}
	}
	for _, match := range presentationInspectedCountPattern.FindAllStringSubmatch(result, -1) {
		if len(match) == 2 {
			if count, err := strconv.Atoi(match[1]); err == nil && expected > 0 && count >= expected {
				return true
			}
		}
	}
	return expected > 0 && len(inspected) >= expected
}

func recordPresentationInspectedImages(target string, toolCall ToolCall, inspected map[string]bool) {
	for _, path := range presentationVisualImagePaths(toolCall) {
		if presentationVisualPathTargetsRender(path, target) {
			inspected[normalizedPresentationPath(path)] = true
		}
	}
}

func presentationDistinctInspectedSlideCount(inspected map[string]bool, maximum int) int {
	if len(inspected) == 0 {
		return 0
	}
	slidePattern := regexp.MustCompile(`(?i)^slide[_-]?0*([1-9]\d*)\.(?:png|jpe?g|webp)$`)
	seen := make(map[int]bool)
	for path, successful := range inspected {
		if !successful {
			continue
		}
		match := slidePattern.FindStringSubmatch(filepath.Base(normalizedPresentationPath(path)))
		if len(match) != 2 {
			continue
		}
		number, err := strconv.Atoi(match[1])
		if err == nil && number > 0 && (maximum <= 0 || number <= maximum) {
			seen[number] = true
		}
	}
	return len(seen)
}

func presentationVisualDefectDetected(result string) bool {
	lower := strings.ToLower(strings.TrimSpace(result))
	// Substring matching alone reads “未发现裁切” as “发现裁切”. Strip only
	// explicit negative findings; a positive defect elsewhere still survives.
	lower = presentationNegatedDefectPattern.ReplaceAllString(lower, "")
	if presentationPositiveDefectPattern.MatchString(lower) {
		return true
	}
	if presentationEvidenceMarker(lower,
		`"visual_qa_passed":false`, `"presentation_visual_valid":false`, `"visual_defects":true`,
		"发现裁切", "发现重叠", "发现遮挡", "发现溢出", "发现乱码", "发现空白页", "发现占位符",
		"存在裁切", "存在重叠", "存在遮挡", "存在溢出", "存在乱码", "存在空白页", "存在占位符",
		"需要修正的裁切", "需要修正的重叠", "需要修正的遮挡", "需要修正的溢出",
		"detected clipping", "detected overlap", "found clipping", "found overlap", "unresolved visual defect", "visual qa failed",
	) {
		return true
	}
	return false
}

func invalidatePresentationDownstreamEvidence(state *presentationProductionState) {
	if state == nil {
		return
	}
	state.AuditPassed = false
	state.AuditEvidence = ""
	state.DeckSlideCount = 0
	state.Rendered = false
	state.RenderEvidence = ""
	state.RenderTarget = ""
	state.RenderedSlideCount = 0
	state.VisualInspectedImages = make(map[string]bool)
	state.FinalVisualInspected = false
	state.VisualCoverageComplete = false
	state.VisualEvidence = ""
	state.AestheticQAPassed = false
	state.AestheticQAEvidence = ""
	state.VisualDefectsDetected = false
	state.VisualDefectEvidence = ""
	state.NativeQAAttempted = false
	state.SelfAuditFailed = false
	state.SelfAuditEvidence = ""
}

func presentationCountsLevel2BuildFailure(state *presentationProductionState, toolCall ToolCall, action string) bool {
	if state == nil || !isPresentationBuilderAction(toolCall, action) {
		return false
	}
	// A failed validation snippet, a failed attempt to locate a module, or a
	// source-file write is not a failed production renderer. Only a concrete
	// stable-spec build, or the scaffolded task builder itself, can consume the
	// bounded Level-2 disaster-recovery budget.
	if isPresentationStableSpecBuilderAction(toolCall, presentationToolPayload(toolCall)) {
		return true
	}
	if !state.Scaffolded || strings.TrimSpace(state.BuilderPath) == "" {
		return false
	}
	action = strings.ToLower(filepath.ToSlash(action))
	builder := strings.ToLower(filepath.ToSlash(strings.TrimSpace(state.BuilderPath)))
	return strings.Contains(action, builder) || strings.Contains(action, strings.ToLower(filepath.Base(builder)))
}

func presentationCountsPPTMasterBuildFailure(state *presentationProductionState, toolCall ToolCall, action string) bool {
	if state == nil || state.Route != "level3-ppt-master" {
		return false
	}
	lower := strings.ToLower(filepath.ToSlash(action + "\n" + presentationToolPayload(toolCall)))
	if strings.Contains(lower, "svg_to_pptx.py") {
		return true
	}
	return strings.Contains(lower, "batch_validate.py") && strings.Contains(lower, "--export")
}

func presentationSourceReadAuthorized(sourceContext, path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.Trim(strings.TrimSpace(path), "\"'`")))
	contextText := strings.ToLower(filepath.ToSlash(sourceContext))
	if path == "" || contextText == "" {
		return false
	}
	if strings.Contains(contextText, path) {
		return true
	}
	base := filepath.Base(filepath.FromSlash(path))
	if base != "." && base != "" && strings.Contains(contextText, strings.ToLower(base)) {
		return true
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// A user may authorize a directory (for example fixtures/data) rather than
	// enumerate every CSV inside it. Match only suffixes containing at least two
	// path segments so generic words such as "data" do not authorize unrelated
	// reads elsewhere in the workspace.
	for start := 0; start+2 <= len(parts)-1; start++ {
		directorySuffix := strings.Join(parts[start:len(parts)-1], "/")
		if strings.Contains(contextText, directorySuffix) {
			return true
		}
	}
	return false
}

func presentationDirectSourceTool(toolCall ToolCall) bool {
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	if !securitycore.IsExternalEvidenceToolCall(toolCall) {
		return false
	}
	if securitycore.ExternalEvidenceSearchMode(toolCall) == "direct" {
		return true
	}
	// These tools expose already-opened or host-acquired source content rather
	// than search snippets. Discovery-only web_search/platform_search results do
	// not enter the numeric truth boundary.
	return presentationContainsAny(name, "web_page", "source_acquire", "browser_task")
}

func recordPresentationSourceEvidence(state *presentationProductionState, toolCall ToolCall, content string) {
	if state == nil {
		return
	}
	name := strings.ToLower(strings.TrimSpace(toolCall.Name))
	path := firstStringArg(toolCall.Arguments, "path", "file_path", "file", "input", "input_path")
	label := ""
	switch name {
	case "read_file", "extract":
		authorization := state.SourceAuthorizationContext
		if strings.TrimSpace(authorization) == "" {
			authorization = state.SourceContext
		}
		if !presentationSourceReadAuthorized(authorization, path) {
			return
		}
		label = "LOCAL PRESENTATION EVIDENCE: " + filepath.ToSlash(strings.TrimSpace(path))
	default:
		if !presentationDirectSourceTool(toolCall) {
			return
		}
		locator := firstStringArg(toolCall.Arguments, "url", "href", "source", "symbol", "ticker")
		if strings.TrimSpace(locator) == "" {
			locator = name
		}
		label = "DIRECT PUBLIC PRESENTATION EVIDENCE: " + strings.TrimSpace(locator)
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	const perFileRuneLimit = 64000
	if runes := []rune(content); len(runes) > perFileRuneLimit {
		content = string(runes[:perFileRuneLimit])
	}
	const totalRuneLimit = 256000
	prefix := "\n[" + label + "]\n"
	if strings.Contains(state.SourceContext, prefix) {
		return
	}
	remaining := totalRuneLimit - len([]rune(state.SourceContext))
	if remaining <= len([]rune(prefix)) {
		return
	}
	addition := prefix + content
	if runes := []rune(addition); len(runes) > remaining {
		addition = string(runes[:remaining])
	}
	state.SourceContext += addition
}

func recordPresentationToolResult(ctx context.Context, toolCall ToolCall, result ToolResult, err error) {
	state := presentationProductionStateFromContext(ctx)
	if state == nil {
		return
	}
	payload := presentationToolPayload(toolCall)
	action := permissionpolicy.ExtractAction(toolCall.Arguments)
	evidencePayload := action
	if strings.TrimSpace(evidencePayload) == "" {
		evidencePayload = payload
	}
	skillName := normalizedPresentationSkillName(toolCall)
	succeeded := presentationToolSucceeded(toolCall, result, err)
	if !succeeded {
		state.mu.Lock()
		blockedByGate := strings.Contains(result.Content, "[BLOCKED: PRESENTATION PRODUCTION GATE]")
		if !blockedByGate && state.Expected && skillName == "ppt-master" {
			state.PPTMasterUnavailable = true
			state.Route = "level2-task-builder"
			state.ActivatedSkill = "ppt-art-director"
		}
		if state.Expected && state.PPTPolishContract && isPresentationAuditAction(action) &&
			isPPTPolishCanonicalAuditAction(action) &&
			!blockedByGate {
			invalidatePresentationDownstreamEvidence(state)
			state.AuditFailed = true
			state.AuditFailureMutationCount = state.RevisionMutationCount
		}
		if !blockedByGate && state.Expected && state.Route == "level2-task-builder" &&
			presentationCountsLevel2BuildFailure(state, toolCall, action) {
			state.Level2BuildFailures++
		}
		if !blockedByGate && state.Expected && presentationCountsPPTMasterBuildFailure(state, toolCall, action) {
			state.PPTMasterBuildFailures++
			if state.PPTMasterBuildFailures >= 2 {
				state.Route = "level2-task-builder"
				state.ActivatedSkill = "ppt-art-director"
			}
		}
		if !blockedByGate && state.Expected && isPresentationAestheticQAAction(action) {
			state.AestheticQAPassed = false
			state.AestheticQAEvidence = ""
		}
		state.mu.Unlock()
		return
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	// Tool observations may refine an already requested presentation workflow,
	// but they must never create that workflow. In particular, inspecting a PPT
	// Skill for a capability report is not a request to produce a deck. The
	// turn's user intent is the sole authority for opening this delivery gate.
	if !state.Expected {
		return
	}
	recordPresentationCandidatePaths(state, toolCall, payload, result.Content)
	recordPresentationSourceEvidence(state, toolCall, result.Content)

	switch skillName {
	case "ppt-art-director":
		state.ArtDirectorActivated = true
		state.ActivatedSkill = skillName
		state.RuntimeDir = presentationRuntimeDirectory(result.Content)
		if state.Route == "" {
			state.Route = "level2-task-builder"
		}
		return
	case "base-ppt":
		if !presentationDisasterFallbackReady(
			state.BasePPTHandoffAllowed,
			state.TaskSpecificBuilderRequired,
			state.Level2BuildFailures,
			state.PPTMasterAllowed,
			state.PPTMasterBuildFailures,
			state.PPTMasterUnavailable,
		) {
			return
		}
		state.ArtDirectorActivated = true
		state.ActivatedSkill = skillName
		state.Route = "level1-base-ppt"
		return
	case "ppt-master":
		state.ArtDirectorActivated = true
		state.ActivatedSkill = skillName
		state.Route = "level3-ppt-master"
		state.PPTMasterUnavailable = false
		return
	}
	if skillName != "" && state.Expected && isPresentationProductionSkill(skillName, result.Content) {
		// User-defined presentation skills live on disk while built-ins may be
		// memory-backed. A successful presentation-specific skill activation is a
		// valid route selection and must not be forced through the built-in Level 2
		// scaffold contract.
		state.ArtDirectorActivated = true
		state.ActivatedSkill = skillName
		state.Route = "custom-presentation-skill"
		return
	}

	if isPresentationScaffoldAction(toolCall, payload) {
		builderPath := presentationScaffoldBuilderPath(toolCall)
		receiptBuilderPath := presentationScaffoldReceiptBuilderPath(result.Content)
		if (builderPath == "" && receiptBuilderPath == "") ||
			!presentationScaffoldArtifactsExist(ctx, builderPath, receiptBuilderPath) {
			return
		}
		if builderPath == "" {
			builderPath = receiptBuilderPath
		}
		state.Scaffolded = true
		state.BuilderRead = false
		state.BuilderCustomized = false
		state.Route = "level2-task-builder"
		state.BuilderPath = builderPath
		return
	}
	if isPresentationBuilderRead(toolCall) {
		state.BuilderRead = true
		state.BuilderPath = firstStringArg(toolCall.Arguments, "path", "file_path", "file")
		return
	}
	if state.PerSlideCustomizationRequired && state.Scaffolded && state.BuilderRead &&
		presentationBuilderMutationTargets(toolCall, state.BuilderPath) {
		state.BuilderCustomized = true
	}
	if state.PPTPolishContract {
		if mutationPath := presentationMutationPath(toolCall); mutationPath != "" &&
			presentationPathWithinContract(mutationPath, state.ContractWorkDir) &&
			!presentationPathWithinContract(mutationPath, state.ContractPreviewDir) {
			ext := strings.ToLower(filepath.Ext(strings.TrimSpace(mutationPath)))
			if ext == ".json" || ext == ".py" || ext == ".js" || ext == ".mjs" || ext == ".ts" {
				state.RevisionMutationCount++
				state.SourceDirty = true
				invalidatePresentationDownstreamEvidence(state)
			}
		}
	}

	structureEvidence := hasPresentationStructureEvidence(evidencePayload, result.Content)
	renderEvidence := hasPresentationRenderEvidenceForTool(toolCall, evidencePayload, result.Content)
	visualEvidence := hasPresentationVisualEvidence(toolCall, result.Content)
	aestheticEvidence := hasPresentationAestheticQAEvidence(evidencePayload, result.Content)
	evidenceDeckPath := presentationToolDeckPath(toolCall, payload, result.Content)
	if observed, failed, evidence := presentationSelfAuditObservation(toolCall, result.Content); observed {
		state.SelfAuditFailed = failed
		state.SelfAuditEvidence = evidence
	}

	// Reference-deck QA is deliberately separate from final-deck QA. Rendering
	// the user's example must never create a phantom output deck or satisfy the
	// final render/vision receipts.
	if state.ReferenceRequired && !state.DeckCreated {
		if renderEvidence && presentationEvidenceTargetsDeck(state.ReferencePath, evidenceDeckPath) {
			state.ReferenceRendered = true
			state.ReferenceRenderTarget = presentationRenderTarget(toolCall, payload, result.Content)
			state.ReferenceSlideCount = presentationReportedSlideCount(result.Content)
			state.ReferenceInspectedImages = make(map[string]bool)
			state.ReferenceVisualInspected = false
			return
		}
		if visualEvidence && state.ReferenceRendered && presentationVisualTargetsRender(toolCall, state.ReferenceRenderTarget) {
			if state.ReferenceInspectedImages == nil {
				state.ReferenceInspectedImages = make(map[string]bool)
			}
			recordPresentationInspectedImages(state.ReferenceRenderTarget, toolCall, state.ReferenceInspectedImages)
			state.ReferenceVisualInspected = presentationVisualCoverageComplete(toolCall, result.Content, state.ReferenceSlideCount, state.ReferenceInspectedImages)
			return
		}
	}

	evidenceRecorded := false
	if structureEvidence && presentationEvidenceTargetsDeck(state.DeckPath, evidenceDeckPath) {
		state.DeckCreated = true
		state.AuditPassed = true
		state.AuditFailed = false
		state.AuditEvidence = strings.TrimSpace(toolCall.Name)
		if state.DeckPath == "" {
			state.DeckPath = evidenceDeckPath
		}
		evidenceRecorded = true
	}
	if renderEvidence && presentationEvidenceTargetsDeck(state.DeckPath, evidenceDeckPath) {
		state.DeckCreated = true
		state.Rendered = true
		state.FinalVisualInspected = false
		state.RenderEvidence = strings.TrimSpace(toolCall.Name)
		state.RenderTarget = presentationRenderTarget(toolCall, payload, result.Content)
		state.RenderedSlideCount = presentationReportedSlideCount(result.Content)
		state.VisualInspectedImages = make(map[string]bool)
		state.VisualEvidence = ""
		state.VisualCoverageComplete = false
		state.AestheticQAPassed = false
		state.AestheticQAEvidence = ""
		state.VisualDefectsDetected = false
		state.VisualDefectEvidence = ""
		if state.DeckPath == "" {
			state.DeckPath = evidenceDeckPath
		}
		evidenceRecorded = true
	}
	visualTargetsRender := presentationVisualTargetsRender(toolCall, state.RenderTarget) ||
		(renderEvidence && isTrustedLocalPresentationVisualQA(toolCall) && strings.TrimSpace(state.RenderTarget) != "")
	if visualEvidence && state.Rendered && visualTargetsRender {
		if state.VisualInspectedImages == nil {
			state.VisualInspectedImages = make(map[string]bool)
		}
		recordPresentationInspectedImages(state.RenderTarget, toolCall, state.VisualInspectedImages)
		state.FinalVisualInspected = true
		state.VisualCoverageComplete = state.VisualCoverageComplete || presentationVisualCoverageComplete(toolCall, result.Content, state.RenderedSlideCount, state.VisualInspectedImages)
		state.VisualEvidence = strings.TrimSpace(toolCall.Name)
		if presentationVisualDefectDetected(result.Content) {
			state.VisualDefectsDetected = true
			state.VisualDefectEvidence = strings.TrimSpace(result.Content)
			state.AestheticQAPassed = false
			state.AestheticQAEvidence = ""
		}
		evidenceRecorded = true
	}
	if aestheticEvidence && state.Rendered && presentationEvidenceTargetsDeck(state.DeckPath, evidenceDeckPath) {
		state.AestheticQAPassed = true
		state.AestheticQAEvidence = strings.TrimSpace(result.Content)
		evidenceRecorded = true
	}
	if evidenceRecorded {
		return
	}
	// Writing or editing build_deck.py prepares source code; it does not prove
	// that the builder ran or that the PPTX bytes were regenerated. Treating a
	// source edit as DeckCreated lets the scaffold's placeholder output pass as
	// the final deliverable and produces contradictory failure/success text.
	if isPresentationBuilderAction(toolCall, action) && !isPresentationBuilderSourceWrite(toolCall, payload) {
		builtPath := presentationToolDeckPath(toolCall, payload, result.Content)
		if builtPath == "" || !presentationBuilderResultConfirmsCreation(result.Content, builtPath) {
			return
		}
		if state.PPTPolishContract && state.BoundedRevision {
			state.RevisionBuildCount++
		}
		if state.AuditFailed && state.RevisionMutationCount > state.AuditFailureMutationCount {
			state.AuditFailed = false
		}
		state.SourceDirty = false
		if state.Route == "level3-ppt-master" {
			state.PPTMasterBuildFailures = 0
		} else {
			state.Level2BuildFailures = 0
		}
		state.DeckCreated = true
		// A rebuilt deck invalidates every downstream QA receipt. Otherwise a
		// correction made after vision could be delivered using stale evidence
		// from the previous file bytes.
		invalidatePresentationDownstreamEvidence(state)
		state.DeckPath = builtPath
	}
}

func presentationRuntimeDirectory(result string) string {
	for _, line := range strings.Split(strings.ReplaceAll(result, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		for _, prefix := range []string{"运行时目录:", "runtime directory:"} {
			if strings.HasPrefix(lower, strings.ToLower(prefix)) {
				return strings.TrimSpace(line[len(prefix):])
			}
		}
	}
	return ""
}

func presentationSkillActivationReceipt(ctx context.Context, toolCall ToolCall, result ToolResult, err error) string {
	if normalizedPresentationSkillName(toolCall) == "" || !presentationToolSucceeded(toolCall, result, err) {
		return ""
	}
	state := presentationProductionStateFromContext(ctx)
	if state == nil {
		return ""
	}
	state.mu.Lock()
	expected := state.Expected
	activated := state.ArtDirectorActivated
	skillName := state.ActivatedSkill
	route := state.Route
	state.mu.Unlock()
	if !expected || !activated || skillName == "" {
		return ""
	}
	receipt := `[HOST PRESENTATION ACTIVATION RECEIPT]
Current-turn use_skill activation for "` + skillName + `" succeeded. Do not call the same Skill again. Continue with the instructions and Runtime Directory returned above.`
	if skillName == "ppt-art-director" && route == "level2-task-builder" {
		receipt += `
Host route for this deck: Level 2 editable task builder with per-slide customization. Use standalone compact JSON plus scripts/build_task_pptx.py only for an explicitly simple/fast/internal/recurring/fixed-schema deck; a short page count alone is not sufficient. For every substantive audience-facing deck, run the scaffold first, read its task-local build_deck.py exactly once, use deck_spec.json as the source-bound truth contract, and complete visual_plan.json before the first build. Validate the plan with scripts/validate_visual_plan.py, acquire every planned media asset, then register one page-owned renderer for every slide. The selected palette/font pair and SLIDE_VISUALS are executable inputs, not prose. Stable cover/chart/table/timeline/matrix/card renderers are mechanics, not the whole-deck layout engine. Do not inspect pptx_design.py or replace the builder wholesale. Audit at 16pt body/35pt title, render, inspect the contact sheet plus every slide separately, then validate a vision-authored aesthetic_qa.json. Repeated-layout, tiny-text, card-dominance, or low aesthetic scores require one focused correction. Do not call base-ppt, probe runtime directories, or create a planning detour.`
	} else if skillName == "ppt-art-director" && route == "level3-ppt-master" {
		receipt += `
Host route for this deck: Level 3 beauty-first/high-stakes production. Call use_skill ppt-master now and run its complete Strategist/Executor workflow. Do not scaffold a conservative Level-2 deck first, do not imitate premium quality with a generic dark theme, and do not switch to base-ppt. After export, audit at 16pt body/35pt title, native-render the exact deck, inspect the contact sheet and every slide separately, and validate a vision-authored aesthetic_qa.json with the ppt-art-director runtime validator.`
	}
	return receipt
}

type presentationAcceptance struct {
	Level          string
	DeckPath       string
	BlockingIssues []string
	Warnings       []string
}

func presentationPPTXStructureValid(path string) bool {
	_, valid := presentationPPTXSlideCount(path)
	return valid
}

func presentationPPTXSlideCount(path string) (int, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1024 {
		return 0, false
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		return 0, false
	}
	defer archive.Close()

	hasContentTypes := false
	hasPresentation := false
	slideCount := 0
	for _, file := range archive.File {
		switch file.Name {
		case "[Content_Types].xml":
			hasContentTypes = true
		case "ppt/presentation.xml":
			hasPresentation = true
		default:
			if strings.HasPrefix(file.Name, "ppt/slides/slide") &&
				strings.HasSuffix(file.Name, ".xml") &&
				!strings.Contains(file.Name, "/_rels/") {
				slideCount++
			}
		}
	}
	return slideCount, hasContentTypes && hasPresentation && slideCount > 0
}

func presentationCandidateAbsolutePaths(workspace, raw string) []string {
	raw = strings.Trim(strings.TrimSpace(raw), "\"'`.,;[]{}()")
	if raw == "" {
		return nil
	}
	raw = filepath.FromSlash(raw)
	if filepath.IsAbs(raw) {
		return []string{filepath.Clean(raw)}
	}
	candidates := []string{filepath.Join(workspace, raw)}
	if filepath.Base(raw) == raw {
		candidates = append(candidates, filepath.Join(workspace, ".openkitty", "storage", raw))
	}
	return candidates
}

func presentationWorkspaceRelativePath(workspace, path string) string {
	rel, err := filepath.Rel(workspace, path)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	return filepath.ToSlash(filepath.Clean(rel))
}

// reconcilePresentationArtifactFromWorkspace verifies the artifact itself
// instead of requiring the model to use one exact filename or validation
// phrase. Only a PPTX mentioned by a successful current-turn artifact-capable
// tool, created inside this run's workspace, and structurally readable is
// accepted.
func reconcilePresentationArtifactFromWorkspace(ctx context.Context) {
	state := presentationProductionStateFromContext(ctx)
	workspace := projectctx.ResolvedWorkspace(ctx)
	if state == nil || strings.TrimSpace(workspace) == "" {
		return
	}
	workspace = projectctx.NormalizeWorkspace(workspace)

	state.mu.Lock()
	if !state.Expected {
		state.mu.Unlock()
		return
	}
	startedAt := state.StartedAt
	currentPath := state.DeckPath
	rawCandidates := make([]string, 0, len(state.CandidatePaths)+1)
	if strings.TrimSpace(currentPath) != "" {
		rawCandidates = append(rawCandidates, currentPath)
	}
	for candidate := range state.CandidatePaths {
		rawCandidates = append(rawCandidates, candidate)
	}
	state.mu.Unlock()

	var bestPath string
	bestSlideCount := 0
	var bestModTime time.Time
	seen := make(map[string]bool)
	for _, raw := range rawCandidates {
		for _, candidate := range presentationCandidateAbsolutePaths(workspace, raw) {
			candidate, err := filepath.Abs(candidate)
			if err != nil {
				continue
			}
			candidate = filepath.Clean(candidate)
			key := projectctx.IdentityKey(candidate)
			if seen[key] {
				continue
			}
			seen[key] = true
			if !projectctx.ContainsPath(workspace, candidate) {
				continue
			}
			if err := projectctx.EnsureResolvedPathInside(workspace, candidate); err != nil {
				continue
			}
			info, err := os.Stat(candidate)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if !startedAt.IsZero() && info.ModTime().Before(startedAt.Add(-2*time.Second)) {
				continue
			}
			slideCount, valid := presentationPPTXSlideCount(candidate)
			if !valid {
				continue
			}
			if bestPath == "" || info.ModTime().After(bestModTime) {
				bestPath = candidate
				bestModTime = info.ModTime()
				bestSlideCount = slideCount
			}
		}
	}
	if bestPath == "" {
		return
	}

	state.mu.Lock()
	state.DeckCreated = true
	state.DeckPath = presentationWorkspaceRelativePath(workspace, bestPath)
	state.DeckSlideCount = bestSlideCount
	state.mu.Unlock()
}

func presentationAcceptanceSnapshot(ctx context.Context) (presentationAcceptance, bool) {
	reconcilePresentationArtifactFromWorkspace(ctx)
	state := presentationProductionStateFromContext(ctx)
	if state == nil {
		return presentationAcceptance{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.Expected {
		return presentationAcceptance{}, false
	}

	acceptance := presentationAcceptance{DeckPath: state.DeckPath}
	if state.ReferenceRequired && !state.ReferenceVisualInspected {
		acceptance.Warnings = append(acceptance.Warnings, "未获得用户指定参考稿的真实页面视觉证据")
	}
	if !state.DeckCreated {
		acceptance.BlockingIssues = append(acceptance.BlockingIssues, "未形成可交付的 PPTX")
	} else if strings.TrimSpace(state.DeckPath) == "" {
		acceptance.BlockingIssues = append(acceptance.BlockingIssues, "已报告生成 PPTX，但没有可定位的交付路径")
	}
	if state.PPTPolishContract && state.SourceDirty {
		acceptance.BlockingIssues = append(acceptance.BlockingIssues, "PPT 源规格或任务 builder 在最后一次成功构建后又被修改；必须重新构建并重新完成审计、渲染和逐页视觉检查")
	}
	if state.PPTPolishContract && state.AuditFailed {
		acceptance.BlockingIssues = append(acceptance.BlockingIssues, "PPT 严格结构审计失败，且尚未经过受控修正与成功重建")
	}
	if state.DeckCreated && state.RequestedMinimumSlides > 0 {
		verifiedSlideCount := state.DeckSlideCount
		if verifiedSlideCount == 0 && state.RenderedSlideCount > 0 {
			// A trusted renderer receipt is also direct evidence of the deck's
			// current page count. This covers remote/external deck paths that the
			// local workspace reconciler cannot open itself.
			verifiedSlideCount = state.RenderedSlideCount
		}
		switch {
		case verifiedSlideCount == 0:
			acceptance.BlockingIssues = append(acceptance.BlockingIssues, fmt.Sprintf("无法核验 PPTX 页数是否满足至少 %d 页的明确约束", state.RequestedMinimumSlides))
		case verifiedSlideCount < state.RequestedMinimumSlides:
			acceptance.BlockingIssues = append(acceptance.BlockingIssues, fmt.Sprintf("PPTX 实际只有 %d 页，少于明确要求的 %d 页", verifiedSlideCount, state.RequestedMinimumSlides))
		}
	}
	if state.DeckCreated && state.RequestedMaximumSlides > 0 {
		verifiedSlideCount := state.DeckSlideCount
		if verifiedSlideCount == 0 && state.RenderedSlideCount > 0 {
			verifiedSlideCount = state.RenderedSlideCount
		}
		if verifiedSlideCount > state.RequestedMaximumSlides {
			acceptance.BlockingIssues = append(acceptance.BlockingIssues, fmt.Sprintf("PPTX 实际有 %d 页，超过明确允许的最多 %d 页", verifiedSlideCount, state.RequestedMaximumSlides))
		}
	}
	if state.DeckCreated && !state.AuditPassed {
		acceptance.Warnings = append(acceptance.Warnings, "未获得 PPTX 结构完整性验证证据")
	}
	if state.DeckCreated && !state.Rendered {
		acceptance.Warnings = append(acceptance.Warnings, "未获得真实 PowerPoint 页面渲染证据")
	}
	if state.DeckCreated && !state.FinalVisualInspected {
		acceptance.Warnings = append(acceptance.Warnings, "未获得渲染页面的像素级视觉检查证据")
	}
	if state.DeckCreated && state.FinalVisualInspected && !state.VisualCoverageComplete {
		acceptance.Warnings = append(acceptance.Warnings, "视觉检查只覆盖部分渲染页面，未完成逐页或完整联系表检查")
	}
	if state.DeckCreated {
		expected := state.RenderedSlideCount
		if expected <= 0 {
			expected = state.DeckSlideCount
		}
		if expected <= 0 {
			expected = state.RequestedMinimumSlides
		}
		perSlideRequired := state.PerSlideVisionRequired ||
			(state.Substantive && !state.PerSlideVisionOptOut)
		inspected := presentationDistinctInspectedSlideCount(state.VisualInspectedImages, expected)
		if perSlideRequired && (expected <= 0 || inspected < expected) {
			policy := "正式对外稿要求逐页分别调用 vision"
			if state.PerSlideVisionRequired {
				policy = "用户明确要求逐页分别调用 vision"
			}
			acceptance.Warnings = append(acceptance.Warnings, fmt.Sprintf(
				"%s，但成功记录仅 %d/%d；contact sheet 不能替代单页检查",
				policy,
				inspected,
				expected,
			))
		}
	}
	if state.DeckCreated && state.StrictQA && state.Substantive && !state.PerSlideVisionOptOut && !state.AestheticQAPassed {
		acceptance.Warnings = append(acceptance.Warnings, "未获得逐页审美评分账本及内置 aesthetic QA 验证通过证据")
	}
	if state.DeckCreated && state.FinalVisualInspected && state.VisualDefectsDetected {
		acceptance.Warnings = append(acceptance.Warnings, "视觉检查发现尚未修复的裁切、重叠或其他页面问题")
	}
	if state.DeckCreated && state.SelfAuditFailed {
		issue := "PPT 自检报告仍含失败或待修项"
		if strings.TrimSpace(state.SelfAuditEvidence) != "" {
			issue += "：" + state.SelfAuditEvidence
		}
		acceptance.Warnings = append(acceptance.Warnings, issue)
	}

	// Audience-facing production is strict by default. Only an explicit
	// fast/internal/draft route or an explicit QA opt-out may deliver a usable
	// deck with clearly disclosed partial verification.
	if state.StrictQA && len(acceptance.Warnings) > 0 {
		acceptance.BlockingIssues = append(acceptance.BlockingIssues, acceptance.Warnings...)
		acceptance.Warnings = nil
	}
	sort.Strings(acceptance.BlockingIssues)
	sort.Strings(acceptance.Warnings)
	switch {
	case len(acceptance.BlockingIssues) > 0:
		acceptance.Level = "blocked"
	case len(acceptance.Warnings) > 0:
		acceptance.Level = "partial"
	default:
		acceptance.Level = "verified"
	}
	return acceptance, true
}

func presentationCompletionIssues(ctx context.Context) []string {
	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok {
		return nil
	}
	issues := append([]string(nil), acceptance.BlockingIssues...)
	issues = append(issues, acceptance.Warnings...)
	sort.Strings(issues)
	return issues
}

func presentationBlockingIssues(ctx context.Context) []string {
	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok {
		return nil
	}
	return append([]string(nil), acceptance.BlockingIssues...)
}

func presentationQualityWarnings(ctx context.Context) []string {
	acceptance, ok := presentationAcceptanceSnapshot(ctx)
	if !ok {
		return nil
	}
	return append([]string(nil), acceptance.Warnings...)
}

func shouldStreamPresentationCandidate(ctx context.Context) bool {
	state := presentationProductionStateFromContext(ctx)
	if state == nil {
		return true
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return !state.Expected
}

func presentationProductionGateAvailable(ctx context.Context) bool {
	state := presentationProductionStateFromContext(ctx)
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.Expected || state.IntakeRequired
}

func presentationProductionRepairAvailable(ctx context.Context, registry *toolregistry.Registry) bool {
	if !presentationProductionGateAvailable(ctx) || registry == nil {
		return false
	}
	_, ok := registry.Get("use_skill")
	return ok
}

func presentationCurrentTurnExecutionPrompt(ctx context.Context) string {
	state := presentationProductionStateFromContext(ctx)
	if state == nil {
		return ""
	}
	state.mu.Lock()
	expected := state.Expected
	intakeRequired := state.IntakeRequired
	route := state.Route
	substantive := state.Substantive
	activated := state.ArtDirectorActivated
	activatedSkill := state.ActivatedSkill
	state.mu.Unlock()
	if intakeRequired {
		return `[PRESENTATION INTAKE REQUIRED]
The user explicitly required confirmation before production. Ask only for the missing audience, use/setting, and core viewpoint (or confirm values already supplied), then stop this turn. Do not infer defaults, call tools, create scripts, or claim delivery. Production resumes after the user answers.`
	}
	if !expected || route == "qa-existing-deck" {
		return ""
	}
	if substantive && route == "level3-ppt-master" {
		next := `Load "ppt-art-director" exactly once. Its selected route is Level 3; immediately call use_skill ppt-master and run the complete Strategist/Executor workflow.`
		if activated && activatedSkill == "ppt-art-director" {
			next = `ppt-art-director is already active. Call use_skill ppt-master now; do not scaffold or start a Level-2 builder first.`
		} else if activated && activatedSkill == "ppt-master" {
			next = `ppt-master is already active. Continue its complete Strategist/Executor workflow; do not reload either Skill or switch generators.`
		}
		return `[PRESENTATION CURRENT-TURN EXECUTION CONTRACT]
Create the real beauty-first/high-stakes PPTX in this turn. ` + next + ` Freeze the communication job, compare materially different visual directions, select one visual thesis, plan purposeful assets slide by slide, and make the cover, evidence page, system/diagram page, and closing establish a coherent visual world. Do not imitate premium quality with a generic dark theme, repeated cards, or ornamental gradients. Preserve source evidence and never invent metrics.
After export, run the canonical structural audit with at least --min-body-font-size 16 --min-title-font-size 35, perform a native render, inspect the contact sheet for rhythm, and call vision separately on every full-size slide_NN image. Prepare a fresh hash-bound ledger with validate_aesthetic_qa.py --prepare, then fill aesthetic_qa.json only from those observations, with hierarchy, composition, typography, asset_quality, visual_relevance, and density_balance scores for every slide plus deck-level coherence, rhythm, visual_storytelling, and originality. Then run the ppt-art-director Runtime Directory scripts/validate_aesthetic_qa.py against the exact deck and render directory. Any rebuild invalidates all QA evidence. base-ppt remains a disclosed disaster fallback only after both quality routes are objectively exhausted.`
	}
	if substantive {
		return `[PRESENTATION CURRENT-TURN EXECUTION CONTRACT]
After the task-local renderer edits, execute exactly that generated build_deck.py as a no-argument Python script. Its spec, visual plan, and output paths are already embedded; do not rerun scaffold_task_pptx.py, invent --spec/--facts arguments, import it through ptc_execute, or switch to build_task_pptx.py.
Create a real audience-facing PPTX in this turn. Load "ppt-art-director" exactly once and follow the returned Runtime Directory and task-specific Level-2 route. Create the scaffold in one new task-private directory such as .openkitty/storage/ppt_runs/<unique-run-id>/; never place task files or previews in the shared storage root. Per-slide customization is the default regardless of page count: scaffold the builder, read it once, and keep deck_spec.json as the source-bound truth/narrative contract.
Before the first build, complete visual_plan.json as an executable Visual Director contract: freeze the communication job, compare 2-3 materially different directions (one is enough only when an inspected reference owns the system), select one direction with a concrete visual thesis, executable palette and font pair, 50/35/24/16 type scale, image/chart rules, deck rhythm, and one purposeful primary asset for every slide. Acquire or generate planned media first and run the Runtime Directory scripts/validate_visual_plan.py with --expected-slides. Task-local renderers must consume SLIDE_VISUALS and the selected THEME; planned pictures/charts/tables must become real slide objects. Do not ship a text-and-rectangle inventory or generic stock imagery.
Complete deck_spec.json with evidence_policy="source-bound" and art_direction.visual_thesis, composition_strategy, asset_strategy, palette_strategy, typography_strategy, and background_rhythm; also give every slide a narrative_job, conclusion-led takeaway/title, focal_anchor, evidence_form, composition_family, source_note, and evidence_refs. Register one page-owned renderer for every slide: this is content-driven page design, not template filling. Stable mechanics are not the whole-deck layout engine. Never switch to base-ppt after a probe or first failure; make one focused repair, then use another quality route only when appropriate.
After the build, run the canonical structural audit with at least --min-body-font-size 16 --min-title-font-size 35, perform a native render, inspect the contact sheet for rhythm, and call vision separately on every full-size slide_NN image. Prepare a fresh hash-bound ledger with validate_aesthetic_qa.py --prepare, then fill aesthetic_qa.json only from those observations, score every slide and the whole deck, then run scripts/validate_aesthetic_qa.py against the exact deck and render directory. Any rebuild invalidates the audit, render, vision, and aesthetic receipt. Prior-turn files may be references but never satisfy this turn's QA.`
	}
	return `[PRESENTATION CURRENT-TURN EXECUTION CONTRACT]
Create a real PPTX in this turn. Use the explicitly requested fast/internal/recurring route, keep its real exit status, and inspect rendered output before delivery. Prior-turn files may be references, but never report them or their old validation as this turn's deliverable.`
}

func presentationSkillRecoveryInstruction(ctx context.Context) string {
	state := presentationProductionStateFromContext(ctx)
	if state != nil {
		state.mu.Lock()
		activated := state.ArtDirectorActivated
		skillName := state.ActivatedSkill
		state.mu.Unlock()
		if activated {
			if skillName == "" {
				skillName = "the presentation production skill"
			}
			return "The host already recorded successful use_skill activation for " + skillName + ". Do not call it again; continue with its returned instructions and Runtime Directory."
		}
	}
	return `Use ppt-art-director for substantive audience-facing work. Preserve the exact Runtime Directory and command returned by use_skill; one guessed command or environment probe is not a failed production build. Repair the selected quality route once, then try the other applicable quality route. base-ppt is a disclosed disaster fallback only after the host gate confirms those routes are unavailable or exhausted.`
}

func presentationFocusedQAInstruction(ctx context.Context, issues []string) string {
	joined := strings.Join(issues, "\n")
	if !presentationContainsAny(joined, "真实 PowerPoint 页面渲染", "像素级视觉检查", "视觉检查只覆盖", "逐页分别调用 vision", "审美评分账本", "aesthetic") {
		return ""
	}
	state := presentationProductionStateFromContext(ctx)
	if state == nil {
		return ""
	}
	state.mu.Lock()
	deckPath := state.DeckPath
	rendered := state.Rendered
	renderTarget := state.RenderTarget
	runtimeDir := state.RuntimeDir
	renderedSlideCount := state.RenderedSlideCount
	deckSlideCount := state.DeckSlideCount
	requestedMinimumSlides := state.RequestedMinimumSlides
	perSlideRequired := state.PerSlideVisionRequired ||
		(state.Substantive && !state.PerSlideVisionOptOut)
	aestheticQAPassed := state.AestheticQAPassed
	expected := max(max(renderedSlideCount, deckSlideCount), requestedMinimumSlides)
	inspected := presentationDistinctInspectedSlideCount(state.VisualInspectedImages, expected)
	state.mu.Unlock()
	if runtimeDir == "" {
		runtimeDir = `.\.openkitty\runtime\builtin\presentation\ppt-art-director`
	}
	if rendered && strings.TrimSpace(renderTarget) != "" {
		if perSlideRequired {
			qaPath := filepath.Join(filepath.Dir(deckPath), "aesthetic_qa.json")
			validator := filepath.Join(runtimeDir, "scripts", "validate_aesthetic_qa.py")
			if expected > 0 && inspected >= expected && !aestheticQAPassed {
				return fmt.Sprintf(`Individual vision coverage is already complete (%d/%d). Do not render or inspect again. Prepare a fresh ledger at %q with the canonical validator %q and --prepare (or retain a current prepared ledger), fill its scores from the recorded observations without changing artifact hashes, then validate without --prepare with --qa %q --deck %q --render-dir %q --expected-slides %d.`, inspected, expected, qaPath, validator, qaPath, deckPath, renderTarget, expected)
			}
			return fmt.Sprintf(`The native render is already recorded. Do not render again and do not use web_navigate, read_file, or a text-only script as visual inspection. Call the vision tool now: first inspect %q for deck-level rhythm, then call vision separately on every full-size slide_01.png through slide_%02d.png in %q. Check clipping, overlap, overflow, hierarchy, typography, asset relevance, density, and balance; record defects and aesthetic scores by slide number. The contact sheet never substitutes for these individual calls. When all %d slides pass, prepare a fresh ledger at %q with %q and --prepare against the exact deck and render directory, fill it from those observations while retaining the artifact hashes, then validate without --prepare.`, filepath.Join(renderTarget, "contact_sheet.jpg"), expected, renderTarget, expected, qaPath, validator)
		}
		contactSheet := filepath.Join(renderTarget, "contact_sheet.jpg")
		return fmt.Sprintf(`The native render is already recorded. Do not render again and do not use web_navigate, read_file, or a text-only script as visual inspection. Call the vision tool now with path=%q and ask it to inspect every page for clipping, overlap, overflow, unreadable text, blank pages, and placeholders.`, contactSheet)
	}
	if strings.TrimSpace(deckPath) == "" {
		deckPath = "<final.pptx>"
	}
	previewDir := filepath.ToSlash(filepath.Join(filepath.Dir(deckPath), "rendered_pages"))
	scriptName, _, ok := presentationQARendererForOS(runtime.GOOS)
	if !ok {
		scriptName = "scripts/render_pptx.py (or the platform's equivalent native renderer)"
	} else {
		scriptName = "scripts/" + scriptName
	}
	return fmt.Sprintf(`Strict visual QA has two sequential steps. First run the bundled platform renderer %q from %q against %q with output directory %q. It creates contact_sheet.jpg and validates every rendered page. If the renderer receipt does not include complete local pixel inspection, on the following model call use vision on that contact sheet and inspect every page for clipping, overlap, overflow, unreadable text, blank pages, and placeholders. Rendering alone is not visual inspection; do not substitute web_navigate, read_file, or a text-only script.`, scriptName, runtimeDir, deckPath, previewDir)
}

func presentationQARendererForOS(goos string) (scriptName, runner string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "windows":
		return "render_pptx.ps1", "powershell", true
	case "darwin", "linux":
		return "render_pptx.py", "python3", true
	default:
		return "", "", false
	}
}

func presentationShellQuote(value string, goos string) string {
	if strings.EqualFold(goos, "windows") {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return `'` + strings.ReplaceAll(value, `'`, `'"'"'`) + `'`
}

// buildPresentationNativeQAToolCall turns the strict platform render receipt
// into a bounded host action once the artifact itself is present and its page
// bounds are valid. Leaving this step to another model turn made delivery
// depend on whether the model remembered an exact bundled script path; it also
// encouraged repeated LibreOffice/COM probes. Windows uses PowerPoint's native
// renderer; macOS and Linux use the bundled LibreOffice/Poppler renderer. The
// call stays workspace-local, uses only the materialized trusted runtime, and
// is invalidated when the deck is rebuilt.
func buildPresentationNativeQAToolCall(ctx context.Context, iteration int) (ToolCall, bool) {
	return buildPresentationNativeQAToolCallForOS(ctx, iteration, runtime.GOOS)
}

func buildPresentationNativeQAToolCallForOS(ctx context.Context, iteration int, goos string) (ToolCall, bool) {
	scriptName, runner, supported := presentationQARendererForOS(goos)
	if !supported {
		return ToolCall{}, false
	}
	reconcilePresentationArtifactFromWorkspace(ctx)
	state := presentationProductionStateFromContext(ctx)
	workspace := projectctx.NormalizeWorkspace(projectctx.ResolvedWorkspace(ctx))
	if state == nil || strings.TrimSpace(workspace) == "" {
		return ToolCall{}, false
	}

	state.mu.Lock()
	expected := state.Expected
	strictQA := state.StrictQA
	activated := state.ArtDirectorActivated
	attempted := state.NativeQAAttempted
	deckCreated := state.DeckCreated
	auditPassed := state.AuditPassed
	visualComplete := state.FinalVisualInspected && state.VisualCoverageComplete
	deckPath := strings.TrimSpace(state.DeckPath)
	runtimeDir := strings.TrimSpace(state.RuntimeDir)
	slideCount := state.DeckSlideCount
	minimumSlides := state.RequestedMinimumSlides
	maximumSlides := state.RequestedMaximumSlides
	startedAt := state.StartedAt
	state.mu.Unlock()

	if !expected || !strictQA || !activated || attempted || !deckCreated || !auditPassed || visualComplete || deckPath == "" {
		return ToolCall{}, false
	}
	if (minimumSlides > 0 && slideCount < minimumSlides) || (maximumSlides > 0 && slideCount > maximumSlides) {
		return ToolCall{}, false
	}

	var deckAbsolute string
	for _, candidate := range presentationCandidateAbsolutePaths(workspace, deckPath) {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		absolute = filepath.Clean(absolute)
		if !projectctx.ContainsPath(workspace, absolute) || projectctx.EnsureResolvedPathInside(workspace, absolute) != nil {
			continue
		}
		if count, valid := presentationPPTXSlideCount(absolute); valid &&
			(minimumSlides <= 0 || count >= minimumSlides) &&
			(maximumSlides <= 0 || count <= maximumSlides) {
			deckAbsolute = absolute
			break
		}
	}
	if deckAbsolute == "" {
		return ToolCall{}, false
	}

	if runtimeDir == "" {
		runtimeDir = filepath.Join(".openkitty", "runtime", "builtin", "presentation", "ppt-art-director")
	}
	runtimeAbsolute := filepath.Clean(runtimeDir)
	if !filepath.IsAbs(runtimeAbsolute) {
		runtimeAbsolute = filepath.Join(workspace, runtimeAbsolute)
	}
	runtimeAbsolute, err := filepath.Abs(runtimeAbsolute)
	if err != nil || !projectctx.ContainsPath(workspace, runtimeAbsolute) || projectctx.EnsureResolvedPathInside(workspace, runtimeAbsolute) != nil {
		return ToolCall{}, false
	}
	renderScript := filepath.Join(runtimeAbsolute, "scripts", scriptName)
	info, err := os.Stat(renderScript)
	if err != nil || !info.Mode().IsRegular() || projectctx.EnsureResolvedPathInside(workspace, renderScript) != nil {
		return ToolCall{}, false
	}

	outputDir := filepath.Join(filepath.Dir(deckAbsolute), "rendered_pages")
	if !projectctx.ContainsPath(workspace, outputDir) {
		return ToolCall{}, false
	}
	quote := func(value string) string { return presentationShellQuote(value, goos) }
	var command string
	if strings.EqualFold(goos, "windows") {
		command = fmt.Sprintf(
			"%s -NoProfile -ExecutionPolicy Bypass -File %s -InputPptx %s -OutputDir %s",
			runner, quote(renderScript), quote(deckAbsolute), quote(outputDir),
		)
	} else {
		command = fmt.Sprintf(
			"%s %s --input %s --output-dir %s",
			runner, quote(renderScript), quote(deckAbsolute), quote(outputDir),
		)
	}

	state.mu.Lock()
	if state.NativeQAAttempted || (state.FinalVisualInspected && state.VisualCoverageComplete) || state.DeckPath != deckPath {
		state.mu.Unlock()
		return ToolCall{}, false
	}
	state.NativeQAAttempted = true
	state.mu.Unlock()

	stamp := startedAt.UnixNano()
	if stamp <= 0 {
		stamp = int64(iteration)
	}
	return ToolCall{
		ID:          fmt.Sprintf("host_presentation_native_qa_%d_%d", stamp, iteration),
		Name:        "cli",
		HostManaged: true,
		Arguments: map[string]interface{}{
			"command": command,
			"reason":  "render and validate every presentation page",
			"timeout": 300,
		},
	}, true
}

// prioritizePresentationNativeQA gives a ready, bounded host QA action the
// next execution slot even when the model keeps proposing unrelated work. The
// displaced model calls are not executed from a stale response; the model can
// re-plan them on the following turn after seeing the authoritative QA receipt.
func prioritizePresentationNativeQA(ctx context.Context, iteration int, modelCalls []ToolCall) ([]ToolCall, int, bool) {
	call, ok := buildPresentationNativeQAToolCall(ctx, iteration)
	if !ok {
		return modelCalls, 0, false
	}
	return []ToolCall{call}, len(modelCalls), true
}

func buildPresentationQARepairPromptForContext(ctx context.Context, issues []string) map[string]interface{} {
	focusedQA := presentationFocusedQAInstruction(ctx, issues)
	if focusedQA != "" {
		focusedQA = "\n" + focusedQA
	}
	return map[string]interface{}{
		"role": "system",
		"content": `[PRESENTATION PRODUCTION GATE]
The PPT has unresolved production or QA evidence:
- ` + strings.Join(issues, "\n- ") + `

` + presentationSkillRecoveryInstruction(ctx) + focusedQA + `
Converge now: the real PPTX matters more than further scaffold inspection, architecture rewrites, or optional polish. If a scaffold exists and no PPTX exists yet, finish one complete deck_spec.json edit, read the task builder only if it has not yet been read, and complete the bounded TASK PAGE COMPOSITIONS edits before executing it. Do not inspect pptx_design.py or replace the builder wholesale. Allow at most one targeted correction on the selected quality route. If it still fails, switch to the other applicable quality route; use base-ppt only when the host gate confirms both quality routes are objectively unavailable or exhausted. Keep the remaining calls for build, validation, render, vision, and focused repair. Do not repeat a failed call without first making the targeted correction. Structural audit, native render, and visual inspection are blocking for substantive audience-facing production by default; only an explicit fast/internal/draft route or explicit QA opt-out may report them as warnings.`,
	}
}

func buildPresentationQARepairPrompt(issues []string) map[string]interface{} {
	return buildPresentationQARepairPromptForContext(context.Background(), issues)
}

func presentationDeliveryReservationRemaining(maxIterations int) int {
	if maxIterations <= 1 {
		return 0
	}
	remaining := maxIterations * 2 / 3
	if remaining < 5 {
		remaining = 5
	}
	if remaining >= maxIterations {
		remaining = maxIterations - 1
	}
	return remaining
}

func buildPresentationFinalQAToolWindowPromptForContext(ctx context.Context, issues []string) map[string]interface{} {
	focusedQA := presentationFocusedQAInstruction(ctx, issues)
	if focusedQA != "" {
		focusedQA = "\n" + focusedQA
	}
	return agentloop.NewInternalControlMessage("PRESENTATION FINAL QA WINDOW", `[FINAL PRESENTATION QA TOOL WINDOW]
This is the last planned tool-capable model call. A blocking PPT delivery requirement is still missing:
- `+strings.Join(issues, "\n- ")+`

`+presentationSkillRecoveryInstruction(ctx)+focusedQA+`
Issue only the essential missing tool call(s) now. Prioritize a real PPTX. Do not repeat failed calls, and do not claim checks that did not run.`)
}

func buildPresentationFinalQAToolWindowPrompt(issues []string) map[string]interface{} {
	return buildPresentationFinalQAToolWindowPromptForContext(context.Background(), issues)
}

func buildPresentationDeliveryWarning(ctx context.Context, _ []string) string {
	acceptance, _ := presentationAcceptanceSnapshot(ctx)
	var out strings.Builder
	out.WriteString("PPT 已生成")
	if path := strings.TrimSpace(acceptance.DeckPath); path != "" {
		out.WriteString("：")
		out.WriteString(path)
	}
	out.WriteString("\n\n文件可以下载和继续编辑。当前环境未完成全部逐页视觉复核，建议打开后快速确认字体替换、自动换行和页面裁切。")
	return out.String()
}

func appendPresentationDeliveryWarning(output string, ctx context.Context, warnings []string) string {
	output = stripUnsupportedPresentationQAClaims(strings.TrimSpace(output))
	if strings.Contains(output, "文件可以下载和继续编辑。当前环境未完成全部逐页视觉复核") {
		return output
	}
	warning := buildPresentationDeliveryWarning(ctx, warnings)
	if output == "" {
		return warning
	}
	if strings.Contains(output, warning) {
		return output
	}
	return output + "\n\n---\n\n" + warning
}

// stripUnsupportedPresentationQAClaims removes only affirmative visual-QA
// statements that the host cannot substantiate. Other current-answer content
// is preserved, so stale presentation state cannot replace an unrelated reply.
func stripUnsupportedPresentationQAClaims(output string) string {
	if output == "" {
		return ""
	}
	markers := []string{
		"逐页检查", "布局正常", "无重叠", "无裁切", "字体合适",
		"视觉检查已完成", "像素级检查已完成", "所有页面正常",
	}
	lines := strings.Split(output, "\n")
	kept := lines[:0]
	for _, line := range lines {
		unsupported := false
		for _, marker := range markers {
			if strings.Contains(line, marker) {
				unsupported = true
				break
			}
		}
		if !unsupported {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func appendPresentationDeliveryFailure(output string, issues []string) string {
	output = strings.TrimSpace(output)
	for _, issue := range issues {
		if strings.Contains(issue, "未形成可交付的 PPTX") {
			return "这次没有成功生成可交付的 PPT。系统未检测到可打开的新 PPTX，因此不会把构建脚本、临时文件或未验证路径当作成品。"
		}
	}
	message := "PPT 已生成，但没有通过最终交付校验，暂不把它标记为可用成品。"
	if presentationContainsAny(strings.ToLower(output),
		"✅ 交付完成", "交付完成说明", "作为最终交付物", "实际文件位置", "一键复现命令",
		"delivery complete", "final deliverable",
	) {
		return message + "\n\n检测到的脚本、日志或预览目录仅属于中间产物，没有作为 PPT 成品交付。"
	}
	if output == "" {
		return message
	}
	if strings.Contains(output, message) {
		return output
	}
	return message
}
