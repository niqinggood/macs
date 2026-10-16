package permissionpolicy

import (
	"context"
	"regexp"
	"strings"

	"mac/internal/shellpolicy"
	"mac/internal/toolcontract"
)

type userToolPolicyContextKey struct{}
type userToolPolicyInputContextKey struct{}

type UserToolPolicy struct {
	ForbidAllTools      bool
	ForbidMutations     bool
	ForbidFileMutations bool
}

type directiveBlockPattern struct {
	paired *regexp.Regexp
	open   *regexp.Regexp
}

var (
	directiveBlockPatterns = compileDirectiveBlockPatterns([]string{
		"web", "webpage", "website", "tool_result", "tool-result", "tool_output", "tool-output",
		"quote", "quoted", "document", "data", "untrusted", "content", "excerpt", "source",
		"retrieved_content", "retrieved-content",
	})
	directiveBlockquotePattern = regexp.MustCompile(`(?m)^[\t ]*>[^\r\n]*(?:\r?\n|$)`)
	directiveQuotedPatterns    = []*regexp.Regexp{
		regexp.MustCompile(`"(?:\\.|[^"\\])*"`),
		regexp.MustCompile("“[^”]*”"),
		regexp.MustCompile("「[^」]*」"),
		regexp.MustCompile("『[^』]*』"),
		regexp.MustCompile("`[^`\\r\\n]*`"),
	}
	directiveLiteralPattern   = regexp.MustCompile(`(?i)^(?:[a-z]:[\\/].+|/[^\s].*|(?:[a-z0-9_][a-z0-9_.-]*[\\/])+[a-z0-9_.-]+|[a-z_][a-z0-9_.-]*)$`)
	explicitNoToolsPattern    = regexp.MustCompile(`(?i)(?:(?:不(?:要|得|允许|必|用|再)?|禁止|切勿|别|勿)\s*(?:(?:调用|使用|执行|运行)\s*)?(?:任何|任意|所有)?\s*(?:外部\s*)?工具|(?:do\s+not|don't|never|without)\s+(?:(?:call|invoke|use|run|execute|using|calling)\s+)?(?:any\s+|all\s+)?tools?\b)`)
	explicitReadOnlyPattern   = regexp.MustCompile(`(?i)(?:(?:这是|本次|进行|以|保持|仅限|只做|只能做).{0,12}只读|只读(?:能力)?(?:评测|审查|分析|检查|核验|任务|模式|方式)|(?:this\s+is|perform|keep|strictly|in)\s+(?:a\s+)?read[\s-]*only|read[\s-]*only\s+(?:review|audit|analysis|task|mode))`)
	deferredProductionPattern = regexp.MustCompile(`(?i)(?:(?:(?:不要|禁止|切勿)\s*(?:现在|本次|实际|直接)?\s*(?:开始|着手)|(?:先别|暂不|暂时不要)\s*(?:开始|着手)?).{0,10}(?:制作|生成|创建|实现|执行|开发|改动|修改|落盘)|(?:do\s+not|don't)\s+(?:start|begin)\s+(?:build(?:ing)?|creat(?:e|ing)|produc(?:e|ing)|implement(?:ing|ation)?|execut(?:e|ing)|mak(?:e|ing)\s+changes?|writ(?:e|ing)\s+files?)|(?:not\s+yet|hold\s+off).{0,18}(?:build(?:ing)?|creat(?:e|ing)|produc(?:e|ing)|implement(?:ing|ation)?|execut(?:e|ing)|mak(?:e|ing)\s+changes?|writ(?:e|ing)\s+files?))`)
	explicitFileCreationBan   = regexp.MustCompile(`(?i)(?:(?:不要|不得|禁止|切勿|无需|不必)\s*(?:再|实际|直接)?\s*(?:创建|生成|新建|写入|落盘|保存|导出)\s*(?:任何|任意|新的?)?\s*(?:文件|文档|目录|工作区内容)|(?:do\s+not|don't|never|without)\s+(?:create|generate|write|save|export)(?:\s+any|\s+new)?\s+(?:files?|documents?|directories?))`)
)

func compileDirectiveBlockPatterns(tags []string) []directiveBlockPattern {
	patterns := make([]directiveBlockPattern, 0, len(tags))
	for _, tag := range tags {
		quoted := regexp.QuoteMeta(tag)
		patterns = append(patterns, directiveBlockPattern{
			paired: regexp.MustCompile(`(?is)<\s*` + quoted + `\b[^>]*>.*?<\s*/\s*` + quoted + `\s*>`),
			open:   regexp.MustCompile(`(?is)<\s*` + quoted + `\b[^>]*>`),
		})
	}
	return patterns
}

// trustedDirectiveText removes common data/quotation containers before host
// code infers user intent. It is deliberately conservative: a bare tool name
// or path wrapped for Markdown styling is retained, while quoted instructions
// and fetched/tool-output blocks are treated as data, never authority.
func trustedDirectiveText(input string) string {
	text := stripDelimitedDirectiveBlocks(input, "```")
	text = stripDelimitedDirectiveBlocks(text, "~~~")
	for _, pattern := range directiveBlockPatterns {
		text = pattern.paired.ReplaceAllString(text, " ")
		if location := pattern.open.FindStringIndex(text); location != nil {
			text = text[:location[0]] + " "
		}
	}
	text = directiveBlockquotePattern.ReplaceAllString(text, " ")
	for _, pattern := range directiveQuotedPatterns {
		text = pattern.ReplaceAllStringFunc(text, func(quoted string) string {
			inner := strings.TrimSpace(strings.Trim(quoted, "`\"“”「」『』"))
			if directiveLiteralPattern.MatchString(inner) {
				return " " + inner + " "
			}
			return " "
		})
	}
	return strings.TrimSpace(text)
}

// TrustedDirectiveText removes quoted, fetched, and tool-produced content
// before interpreting user-authored policy directives.
func TrustedDirectiveText(input string) string {
	return trustedDirectiveText(input)
}

func stripDelimitedDirectiveBlocks(text, delimiter string) string {
	for {
		start := strings.Index(text, delimiter)
		if start < 0 {
			return text
		}
		end := strings.Index(text[start+len(delimiter):], delimiter)
		if end < 0 {
			return text[:start] + " "
		}
		end += start + 2*len(delimiter)
		text = text[:start] + " " + text[end:]
	}
}

func DeriveUserToolPolicy(input string) UserToolPolicy {
	return deriveUserToolPolicyFromTrustedText(trustedDirectiveText(input))
}

// DeriveUserToolPolicyFromTrustedText derives policy from text that has
// already passed through TrustedDirectiveText.
func DeriveUserToolPolicyFromTrustedText(text string) UserToolPolicy {
	return deriveUserToolPolicyFromTrustedText(text)
}

func deriveUserToolPolicyFromTrustedText(text string) UserToolPolicy {
	text = strings.TrimSpace(text)
	policy := UserToolPolicy{}
	if text == "" {
		return policy
	}
	policy.ForbidAllTools = explicitNoToolsPattern.MatchString(text)
	policy.ForbidMutations = explicitReadOnlyPattern.MatchString(text) || hasBroadStateMutationBan(text)
	policy.ForbidFileMutations = hasSimpleExplicitFileMutationBan(text) || hasBroadFileMutationBan(text) ||
		deferredProductionPattern.MatchString(text) || explicitFileCreationBan.MatchString(text)
	return policy
}

// hasSimpleExplicitFileMutationBan handles ordinary wording without trying to
// infer a broad permission model. A nearby negative word, file target, and file
// action are enough; generated prompts and quoted data have already been
// removed by trustedDirectiveText.
func hasSimpleExplicitFileMutationBan(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if containsAnyText(lower, []string{
		"只在聊天", "仅在聊天", "直接在聊天", "只在对话", "仅在对话", "chat only",
	}) {
		return true
	}
	runes := []rune(lower)
	for _, marker := range []string{
		"不要", "不得", "禁止", "切勿", "无需", "不必", "别", "do not", "don't", "never", "without",
	} {
		needle := []rune(marker)
		for start := 0; start <= len(runes)-len(needle); start++ {
			if string(runes[start:start+len(needle)]) != marker {
				continue
			}
			end := start + 100
			if end > len(runes) {
				end = len(runes)
			}
			window := negativeDirectiveClause(string(runes[start:end]))
			hasTarget := containsAnyText(window, []string{
				"文件", "文档", "目录", "工作区", "file", "document", "directory", "workspace",
			})
			hasAction := containsAnyText(window, []string{
				"保存", "生成", "导出", "创建", "新建", "写入", "落盘", "修改", "删除",
				"save", "generate", "export", "create", "write", "modify", "delete",
			})
			if hasTarget && hasAction {
				return true
			}
		}
	}
	return false
}

func hasBroadFileMutationBan(text string) bool {
	return negativeDirectiveWindow(text, func(window string) bool {
		mentionsFiles := strings.Contains(window, "文件") || strings.Contains(window, "代码") ||
			strings.Contains(window, "目录") || strings.Contains(window, "工作区") ||
			strings.Contains(window, "files") || strings.Contains(window, "file ") ||
			strings.Contains(window, "code") || strings.Contains(window, "workspace")
		if !mentionsFiles {
			return false
		}
		groups := 0
		for _, alternatives := range [][]string{
			{"修改", "更改", "变更", "modify", "change", "edit"},
			{"创建", "新建", "写入", "create", "write"},
			{"删除", "移除", "delete", "remove"},
		} {
			if containsAnyText(window, alternatives) {
				groups++
			}
		}
		return groups >= 2 || (strings.Contains(window, "任何文件") && groups >= 1) ||
			(strings.Contains(window, "any file") && groups >= 1)
	})
}

func hasBroadStateMutationBan(text string) bool {
	return negativeDirectiveWindow(text, func(window string) bool {
		stateTarget := containsAnyText(window, []string{
			"服务", "进程", "数据库", "外部状态", "外部系统", "系统状态", "service", "process",
			"database", "external state", "system state",
		})
		mutation := containsAnyText(window, []string{
			"修改", "更改", "改变", "停止", "启动", "重启", "写入", "删除", "modify", "change", "stop",
			"start", "restart", "write", "delete",
		})
		return stateTarget && mutation
	})
}

func negativeDirectiveWindow(text string, match func(string) bool) bool {
	lower := strings.ToLower(text)
	negativeMarkers := []string{
		"不得", "不要", "禁止", "不允许", "切勿", "不能", "do not", "don't", "never", "must not", "without",
	}
	runes := []rune(lower)
	for _, marker := range negativeMarkers {
		markerRunes := []rune(marker)
		for start := 0; start <= len(runes)-len(markerRunes); start++ {
			if string(runes[start:start+len(markerRunes)]) != marker {
				continue
			}
			end := start + 110
			if end > len(runes) {
				end = len(runes)
			}
			if match(negativeDirectiveClause(string(runes[start:end]))) {
				return true
			}
		}
	}
	return false
}

// A negation applies to its clause, not to the next authorized instruction.
// Keep commas inside mutation lists ("do not edit, create, or delete files"),
// but stop at a comma after an unrelated instruction ("don't ask, write it").
func negativeDirectiveClause(window string) string {
	runes := []rune(window)
	for i, r := range runes {
		if strings.ContainsRune("。；;！？!?\n\r", r) || r == '.' && (i+1 == len(runes) || runes[i+1] == ' ') {
			return string(runes[:i])
		}
		if r == ',' || r == '，' {
			prefix := string(runes[:i])
			if !containsAnyText(prefix, []string{
				"修改", "更改", "变更", "保存", "生成", "导出", "创建", "新建", "写入", "落盘", "删除", "移除", "停止", "启动", "重启",
				"modify", "change", "edit", "save", "generate", "export", "create", "write", "delete", "remove", "stop", "start",
			}) {
				return prefix
			}
		}
	}
	return window
}

func WithUserToolPolicy(ctx context.Context, input string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	policy := DeriveUserToolPolicy(input)
	if policy == (UserToolPolicy{}) {
		return ctx
	}
	return context.WithValue(ctx, userToolPolicyContextKey{}, policy)
}

// withUserToolPolicyInput freezes the actual user-authored request separately
// from generated pipeline prompts. Pipeline templates and upstream step output
// frequently contain phrases such as "do not call tools" to describe another
// stage; those phrases are data, not a user directive for the current stage.
func WithUserToolPolicyInput(ctx context.Context, input string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, userToolPolicyInputContextKey{}, strings.TrimSpace(input))
}

func UserToolPolicyInput(ctx context.Context, fallback string) string {
	if ctx != nil {
		if input, ok := ctx.Value(userToolPolicyInputContextKey{}).(string); ok {
			return input
		}
	}
	return fallback
}

func WithTurnUserToolPolicy(ctx context.Context, fallbackInput string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if input, ok := ctx.Value(userToolPolicyInputContextKey{}).(string); ok {
		// A pipeline-frozen original request is authoritative for this stage,
		// including when it contains no restrictions. Write the zero value too,
		// so generated template text and inherited stage-local policy cannot
		// introduce a false prohibition.
		return context.WithValue(ctx, userToolPolicyContextKey{}, DeriveUserToolPolicy(input))
	}
	return WithUserToolPolicy(ctx, fallbackInput)
}

// withTurnIntentDerivedPolicies makes every host-side intent classifier use
// the same authoritative user-authored request. Pipeline prompts can contain
// output examples, upstream notes, or prohibitions that mention tools and file
// formats; those generated instructions must not create or suppress a
// presentation, research, or artifact workflow.
func UserToolPolicyFromContext(ctx context.Context) UserToolPolicy {
	if ctx == nil {
		return UserToolPolicy{}
	}
	policy, _ := ctx.Value(userToolPolicyContextKey{}).(UserToolPolicy)
	return policy
}

func UserToolPolicyBlocksTool(ctx context.Context, call toolcontract.Call, registry toolcontract.ToolLookup, readOnlyByName toolcontract.ReadOnlyNameClassifier) (bool, string) {
	return UserToolPolicyFromContext(ctx).Blocks(call, registry, readOnlyByName)
}

func (policy UserToolPolicy) Blocks(call toolcontract.Call, registry toolcontract.ToolLookup, readOnlyByName toolcontract.ReadOnlyNameClassifier) (bool, string) {
	if policy.ForbidAllTools {
		return true, "the user explicitly prohibited all tool calls"
	}
	if policy.ForbidMutations && !ToolCallIsReadOnlyForPolicy(call, registry, readOnlyByName) {
		return true, "the user explicitly required a read-only turn with no state changes"
	}
	if policy.ForbidFileMutations && ToolCallMutatesFiles(call) {
		return true, "the user explicitly prohibited file or workspace mutations"
	}
	return false, ""
}

func ToolCallIsReadOnlyForPolicy(call toolcontract.Call, registry toolcontract.ToolLookup, readOnlyByName toolcontract.ReadOnlyNameClassifier) bool {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if name == "cli" || name == "persistent_shell" {
		return shellpolicy.Classify(toolcontract.ExtractCLICommand(call.Arguments)) == shellpolicy.ClassReadOnly
	}
	if registry != nil {
		if tool, ok := registry.Get(call.Name); ok {
			if readOnly, ok := tool.(toolcontract.CallReadOnlyTool); ok {
				return readOnly.IsReadOnlyForCall(call.Arguments)
			}
			if readOnly, ok := tool.(toolcontract.ReadOnlyTool); ok {
				return readOnly.IsReadOnly()
			}
		}
	}
	return readOnlyByName != nil && readOnlyByName(name)
}

func ToolCallMutatesFiles(call toolcontract.Call) bool {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	switch name {
	case "write_file", "edit_file", "json_patch", "multiedit", "project_init", "create_skill",
		"test_gen", "doc_gen", "convert", "lsp_rename", "lsp_code_action", "refactor":
		return true
	case "notepad":
		return !strings.EqualFold(strings.TrimSpace(ExtractAction(call.Arguments)), "read")
	case "cli", "persistent_shell":
		class := shellpolicy.Classify(toolcontract.ExtractCLICommand(call.Arguments))
		return class == shellpolicy.ClassWrite || class == shellpolicy.ClassDestructive
	case "git":
		action := strings.ToLower(strings.TrimSpace(ExtractAction(call.Arguments)))
		return action == "add" || action == "commit" || action == "checkout" || action == "restore" || action == "init"
	default:
		return false
	}
}

func TaskContractExcerpt(input string, maxRunes int) string {
	cleaned := strings.TrimSpace(input)
	if cleaned == "" || maxRunes <= 0 {
		return "(empty request)"
	}
	runes := []rune(cleaned)
	if len(runes) <= maxRunes {
		return cleaned
	}
	if maxRunes < 80 {
		return string(runes[:maxRunes])
	}
	head := maxRunes * 3 / 5
	tail := maxRunes - head
	return string(runes[:head]) + "\n...[middle omitted]...\n" + string(runes[len(runes)-tail:])
}
