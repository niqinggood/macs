package skills

import (
	"context"
	"fmt"
	"mac/internal/agentloop"
	"mac/internal/projectctx"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"mac/internal/permissionpolicy"
	"mac/internal/toolcontract"
	workspacetools "mac/internal/tools/workspace"
)

type explicitSkillRequirementContextKey struct{}

type explicitSkillFieldContract struct {
	Key           string
	Syntax        string
	AllowedValues []string
}

type explicitSkillArtifactContract struct {
	Filename  string
	FirstLine string
	Headings  []string
	Fields    []explicitSkillFieldContract
}

type explicitSkillArtifactRead struct {
	Path     string
	Content  string
	Complete bool
}

type explicitSkillRequirement struct {
	mu             sync.RWMutex
	Name           string
	Workspace      string
	Activated      bool
	Artifact       *explicitSkillArtifactContract
	ArtifactWrites []string
	ArtifactReads  []explicitSkillArtifactRead
}

var (
	explicitSkillBacktickLiteralPattern = regexp.MustCompile("`([^`\\r\\n]+)`")
	explicitSkillFieldSyntaxPattern     = regexp.MustCompile(`^([\p{L}_][\p{L}\p{N}_.-]{0,63}):\s*([^\s|:]+(?:\|[^\s|:]+)+)$`)
	explicitSkillNumberedLinePattern    = regexp.MustCompile(`^\s*(?:\d+[.)、．]|[-*+])\s*`)
	explicitSkillMarkdownDividerPattern = regexp.MustCompile(`^:?-{3,}:?$`)
	explicitSkillChineseNegationPattern = regexp.MustCompile(`(?i)(?:不要|不得|禁止|不用|无需|切勿)\s*(?:再\s*)?(?:(?:调用|使用|启用|激活|加载|执行)\s*)?(?:任何|任意|这个|该|上述|用户目录|自定义)?\s*(?:[a-z0-9_.-]+\s*)?(?:skill\b|技能)`)
	explicitSkillEnglishNegationPattern = regexp.MustCompile(`(?i)\b(?:do\s+not|don't|never|without)\s+(?:(?:call|invoke|use|activate|load|execute)\s+)?(?:(?:any|the|this|that|named|user|custom)\s+)*(?:[a-z0-9_.-]+\s+)?skills?\b`)
)

var explicitSkillTextArtifactExtensions = map[string]bool{
	".cfg": true, ".csv": true, ".htm": true, ".html": true, ".ini": true,
	".json": true, ".jsonl": true, ".log": true, ".md": true, ".markdown": true,
	".rst": true, ".tex": true, ".toml": true, ".tsv": true, ".txt": true,
	".xml": true, ".yaml": true, ".yml": true,
}

func WithExplicitSkillRequirement(ctx context.Context, input string, loader *SkillsLoader, skillRunActive bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if explicitSkillRequirementFromContext(ctx) != nil || skillRunActive {
		return ctx
	}
	name := explicitlyRequestedSkillName(input, loader)
	if name == "" {
		return ctx
	}
	workspace := strings.TrimSpace(loader.Workspace())
	if workspace != "" {
		if absolute, err := filepath.Abs(workspace); err == nil {
			workspace = filepath.Clean(absolute)
		}
	}
	return context.WithValue(ctx, explicitSkillRequirementContextKey{}, &explicitSkillRequirement{Name: name, Workspace: workspace})
}

// withActivatedSkillRequirement lets a host-bound Skill Run reuse the exact
// artifact completion gate used by an explicit conversational use_skill call.
// The host has already activated the frozen instructions, so no model-visible
// tool call is required and all ordinary tools remain available.
func WithActivatedSkillRequirement(ctx context.Context, name, workspace, activatedOutput string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ctx
	}
	workspace = strings.TrimSpace(workspace)
	if workspace != "" {
		if absolute, err := filepath.Abs(workspace); err == nil {
			workspace = filepath.Clean(absolute)
		}
	}
	requirement := &explicitSkillRequirement{
		Name:      name,
		Workspace: workspace,
		Activated: true,
		Artifact:  extractExplicitSkillArtifactContract(activatedOutput),
	}
	return context.WithValue(ctx, explicitSkillRequirementContextKey{}, requirement)
}

func explicitlyRequestedSkillName(input string, loader *SkillsLoader) string {
	if loader == nil {
		return ""
	}
	text := permissionpolicy.TrustedDirectiveText(input)
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "skill") && !strings.Contains(text, "技能") {
		return ""
	}
	// User Skills can be installed after the long-lived loader starts. Reloading
	// here keeps explicit routing current; ReloadUserSkills swaps its snapshot
	// only after a complete successful scan, so an invalid edit leaves the
	// previous catalog available for this request.
	_, _ = loader.ReloadUserSkills(nil)
	if explicitSkillUseIsNegated(text) {
		return ""
	}

	snapshots := loader.SnapshotSkills()
	sort.SliceStable(snapshots, func(i, j int) bool { return len(snapshots[i].Name) > len(snapshots[j].Name) })
	for _, snapshot := range snapshots {
		name := strings.TrimSpace(snapshot.Name)
		if name == "" {
			continue
		}
		pattern := regexp.MustCompile(`(?i)(?:^|[^a-z0-9_.-])` + regexp.QuoteMeta(name) + `(?:$|[^a-z0-9_.-])`)
		if pattern.MatchString(text) {
			return name
		}
	}
	return ""
}

// explicitSkillUseIsNegated recognizes a refusal to activate a Skill, not
// merely any negative sentence that happens to mention one. For example,
// "不要把它当内置 Skill" strengthens a user-Skill request and must not turn
// off explicit routing. Requiring either direct adjacency or a concrete
// activation verb keeps that distinction language-independent and bounded.
func explicitSkillUseIsNegated(text string) bool {
	return explicitSkillChineseNegationPattern.MatchString(text) ||
		explicitSkillEnglishNegationPattern.MatchString(text)
}

func explicitSkillRequirementFromContext(ctx context.Context) *explicitSkillRequirement {
	if ctx == nil {
		return nil
	}
	requirement, _ := ctx.Value(explicitSkillRequirementContextKey{}).(*explicitSkillRequirement)
	return requirement
}

func ExplicitSkillRequirementPrompt(ctx context.Context) string {
	requirement := explicitSkillRequirementFromContext(ctx)
	if requirement == nil {
		return ""
	}
	requirement.mu.RLock()
	name := requirement.Name
	activated := requirement.Activated
	requirement.mu.RUnlock()
	if activated || name == "" {
		return ""
	}
	return fmt.Sprintf(`## Explicit Skill Contract
The user explicitly required skill %q. Your first tool call must be use_skill with top-level skill_name=%q. Do not read or probe SKILL.md directly and do not start source inspection or artifact production before activation. Treat fixed filenames, first lines, headings, field syntax, and backticked literals in the returned instructions as exact contracts: a similar table column or paraphrase does not satisfy a required literal. Re-read the final artifact and check those exact tokens before claiming completion.`, name, name)
}

func ExplicitSkillPolicyBlocksTool(ctx context.Context, call toolcontract.Call) (bool, string) {
	requirement := explicitSkillRequirementFromContext(ctx)
	if requirement == nil {
		return false, ""
	}
	requirement.mu.RLock()
	name := requirement.Name
	activated := requirement.Activated
	requirement.mu.RUnlock()
	if activated {
		if explicitSkillDirectInstructionRead(call, name) {
			return true, "the requested skill is already activated; use its returned instructions instead of reading SKILL.md directly"
		}
		return false, ""
	}

	toolName := toolcontract.NormalizeToolName(call.Name)
	if toolName == "list_skills" {
		return false, ""
	}
	if toolName != "use_skill" {
		return true, fmt.Sprintf("activate the explicitly requested skill %q with use_skill before any other tool", name)
	}
	requested, _ := call.Arguments["skill_name"].(string)
	if !strings.EqualFold(strings.TrimSpace(requested), name) {
		return true, fmt.Sprintf("use_skill must activate the exact user-requested skill %q first", name)
	}
	return false, ""
}

func explicitSkillDirectInstructionRead(call toolcontract.Call, skillName string) bool {
	name := toolcontract.NormalizeToolName(call.Name)
	if name != "read_file" && name != "extract" && name != "cli" && name != "persistent_shell" {
		return false
	}
	arguments := strings.ToLower(fmt.Sprint(call.Arguments))
	return strings.Contains(arguments, "skill.md") && strings.Contains(arguments, strings.ToLower(skillName))
}

func RecordExplicitSkillToolResult(ctx context.Context, call toolcontract.Call, result toolcontract.Result, err error) {
	requirement := explicitSkillRequirementFromContext(ctx)
	if requirement == nil || !toolcontract.ToolResultExecutionSucceeded(result, err) {
		return
	}

	switch toolcontract.NormalizeToolName(call.Name) {
	case "use_skill":
		requested, _ := call.Arguments["skill_name"].(string)
		requirement.mu.Lock()
		if strings.EqualFold(strings.TrimSpace(requested), requirement.Name) {
			requirement.Activated = true
			requirement.Artifact = extractExplicitSkillArtifactContract(result.Content)
		}
		requirement.mu.Unlock()
	case "write_file", "edit_file":
		artifactPath := explicitSkillToolPath(call.Arguments)
		if artifactPath == "" || explicitSkillInstructionPath(artifactPath) {
			return
		}
		requirement.mu.Lock()
		if requirement.Activated && requirement.Artifact != nil &&
			explicitSkillArtifactPathMatches(requirement.Artifact.Filename, artifactPath) {
			requirement.ArtifactWrites = appendUniqueExact(requirement.ArtifactWrites, artifactPath)
		}
		requirement.mu.Unlock()
	case "read_file", "extract":
		artifactPath := explicitSkillToolPath(call.Arguments)
		if artifactPath == "" || explicitSkillInstructionPath(artifactPath) {
			return
		}
		complete := true
		if toolcontract.NormalizeToolName(call.Name) == "read_file" {
			if linesRange, _ := call.Arguments["lines_range"].(string); strings.TrimSpace(linesRange) != "" {
				complete = false
			}
		}
		toolName := toolcontract.NormalizeToolName(call.Name)
		requirement.mu.Lock()
		if requirement.Activated && requirement.Artifact != nil {
			requirement.ArtifactReads = append(requirement.ArtifactReads, explicitSkillArtifactRead{
				Path: artifactPath, Content: result.Content, Complete: complete,
			})
		}
		contract := cloneExplicitSkillArtifactContract(requirement.Artifact)
		workspace := requirement.Workspace
		writes := append([]string(nil), requirement.ArtifactWrites...)
		requirement.mu.Unlock()

		// A successful full read is the narrowest safe repair point: the host has
		// the exact current bytes and can verify the deterministic rewrite before
		// completion. Any ambiguity or write/readback failure leaves the original
		// observation in state, so the ordinary completion gate still fails closed.
		if toolName == "read_file" && complete && contract != nil &&
			explicitSkillArtifactPathMatches(contract.Filename, artifactPath) {
			if repaired, ok := repairExplicitSkillMarkdownEnumArtifact(
				ctx, workspace, writes, artifactPath, result.Content, contract,
			); ok {
				requirement.mu.Lock()
				requirement.ArtifactReads = append(requirement.ArtifactReads, explicitSkillArtifactRead{
					Path: artifactPath, Content: repaired, Complete: true,
				})
				requirement.mu.Unlock()
			}
		}
	}
}

func explicitSkillToolPath(arguments map[string]interface{}) string {
	for _, key := range []string{"path", "file_path", "source_path"} {
		if value, _ := arguments[key].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func explicitSkillInstructionPath(value string) bool {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	return strings.EqualFold(path.Base(normalized), "SKILL.md")
}

func extractExplicitSkillArtifactContract(instructions string) *explicitSkillArtifactContract {
	var filenames []string
	contract := &explicitSkillArtifactContract{}
	seenHeadings := make(map[string]bool)
	seenFields := make(map[string]bool)

	for _, rawLine := range strings.Split(strings.ReplaceAll(instructions, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" || explicitSkillExampleLine(line) {
			continue
		}
		literals := explicitSkillBacktickLiterals(line)
		if len(literals) == 0 {
			continue
		}

		if explicitSkillObligationLine(line) && explicitSkillFilenameLine(line) {
			for _, literal := range literals {
				if explicitSkillArtifactFilename(literal) {
					filenames = appendUniqueExact(filenames, strings.TrimSpace(literal))
				}
			}
		}

		if explicitSkillObligationLine(line) && explicitSkillFirstLineContract(line) {
			for _, literal := range literals {
				literal = strings.TrimSpace(literal)
				if literal != "" && !explicitSkillArtifactFilename(literal) {
					contract.FirstLine = literal
					break
				}
			}
		}

		if explicitSkillObligationLine(line) && explicitSkillHeadingContract(line) {
			for _, literal := range literals {
				literal = strings.TrimSpace(literal)
				if literal == "" || explicitSkillArtifactFilename(literal) || seenHeadings[literal] {
					continue
				}
				if _, ok := parseExplicitSkillFieldContract(literal); ok {
					continue
				}
				seenHeadings[literal] = true
				contract.Headings = append(contract.Headings, literal)
			}
		}

		if explicitSkillFieldContractLine(line) {
			for _, literal := range literals {
				field, ok := parseExplicitSkillFieldContract(strings.TrimSpace(literal))
				if !ok || seenFields[field.Syntax] {
					continue
				}
				seenFields[field.Syntax] = true
				contract.Fields = append(contract.Fields, field)
			}
		}
	}

	// More than one distinct mandatory filename is ambiguous. A conservative
	// completion gate must not guess which output is the final artifact.
	if len(filenames) != 1 {
		return nil
	}
	contract.Filename = filenames[0]
	return contract
}

func explicitSkillBacktickLiterals(line string) []string {
	matches := explicitSkillBacktickLiteralPattern.FindAllStringSubmatch(line, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			result = append(result, match[1])
		}
	}
	return result
}

func explicitSkillExampleLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(line, "示例") || strings.Contains(line, "例如") || strings.Contains(line, "比如") ||
		strings.Contains(lower, "example") || strings.Contains(lower, "for example") ||
		strings.Contains(lower, "e.g.") || strings.Contains(lower, "sample command")
}

func explicitSkillObligationLine(line string) bool {
	lower := strings.ToLower(line)
	for _, marker := range []string{
		"必须", "务必", "固定", "精确", "严格", "只能", "不得", "要求",
		"must", "required", "requires", "shall", "exactly", "fixed", "mandatory",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func explicitSkillFilenameLine(line string) bool {
	lower := strings.ToLower(line)
	for _, marker := range []string{
		"文件名", "文件名称", "输出文件", "交付文件", "产物文件", "保存为", "另存为",
		"filename", "file name", "output file", "artifact file", "deliverable file", "save as",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func explicitSkillFirstLineContract(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(line, "第一行") || strings.Contains(line, "首行") || strings.Contains(lower, "first line")
}

func explicitSkillHeadingContract(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(line, "标题") || strings.Contains(line, "小节") ||
		strings.Contains(lower, "heading") || strings.Contains(lower, "title")
}

func explicitSkillFieldContractLine(line string) bool {
	if explicitSkillObligationLine(line) {
		return true
	}
	lower := strings.ToLower(line)
	fieldIntent := strings.Contains(line, "字段") || strings.Contains(line, "格式") || strings.Contains(line, "语法") ||
		strings.Contains(line, "标记") || strings.Contains(lower, "field") || strings.Contains(lower, "syntax") ||
		strings.Contains(lower, "format") || strings.Contains(lower, "marker")
	return fieldIntent && (explicitSkillNumberedLinePattern.MatchString(line) || strings.Contains(line, "每个") ||
		strings.Contains(line, "每项") || strings.Contains(lower, "each ") || strings.Contains(lower, "every "))
}

func explicitSkillArtifactFilename(literal string) bool {
	literal = strings.TrimSpace(literal)
	if literal == "" || strings.ContainsAny(literal, " \t\r\n`*?{}<>|\"'") {
		return false
	}
	normalized := strings.ReplaceAll(literal, "\\", "/")
	if strings.Contains(normalized, "://") || strings.HasPrefix(normalized, "/") ||
		regexp.MustCompile(`(?i)^[a-z]:/`).MatchString(normalized) {
		return false
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == "" || part == ".." {
			return false
		}
	}
	base := path.Base(normalized)
	if strings.EqualFold(base, "SKILL.md") || strings.HasPrefix(base, ".") {
		return false
	}
	return explicitSkillTextArtifactExtensions[strings.ToLower(path.Ext(base))]
}

func parseExplicitSkillFieldContract(literal string) (explicitSkillFieldContract, bool) {
	match := explicitSkillFieldSyntaxPattern.FindStringSubmatch(literal)
	if len(match) != 3 {
		return explicitSkillFieldContract{}, false
	}
	allowed := strings.Split(match[2], "|")
	if len(allowed) < 2 {
		return explicitSkillFieldContract{}, false
	}
	return explicitSkillFieldContract{Key: match[1], Syntax: literal, AllowedValues: allowed}, true
}

func appendUniqueExact(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

type explicitSkillMarkdownTable struct {
	Header          []string
	DataLineIndexes []int
	WellFormed      bool
}

func repairExplicitSkillMarkdownEnumArtifact(
	ctx context.Context,
	configuredWorkspace string,
	writtenPaths []string,
	artifactPath string,
	content string,
	contract *explicitSkillArtifactContract,
) (string, bool) {
	if contract == nil || len(contract.Fields) == 0 ||
		!explicitSkillArtifactPathMatches(contract.Filename, artifactPath) {
		return "", false
	}
	filenameExtension := strings.ToLower(path.Ext(strings.ReplaceAll(contract.Filename, "\\", "/")))
	if filenameExtension != ".md" && filenameExtension != ".markdown" {
		return "", false
	}
	if !explicitSkillArtifactNonFieldContractValid(content, contract) {
		return "", false
	}

	workspace, target, ok := resolveExplicitSkillRepairPath(ctx, configuredWorkspace, artifactPath)
	if !ok || !explicitSkillArtifactWasWritten(workspace, target, writtenPaths) {
		return "", false
	}
	repaired, changed, unambiguous := normalizeExplicitSkillMarkdownEnumFields(content, contract.Fields)
	if !unambiguous || !changed || !explicitSkillArtifactContractValid(repaired, contract) {
		return "", false
	}

	writeResult, writeErr := workspacetools.NewWriteFileTool(workspace).Execute(ctx, map[string]interface{}{
		"path": target, "content": repaired,
	})
	if !toolcontract.ToolExecutionSucceededForTool("write_file", writeResult, writeErr) {
		return "", false
	}
	readback, readErr := workspacetools.NewReadFileTool(workspace).Execute(ctx, map[string]interface{}{"path": target})
	if readErr != nil || !explicitSkillArtifactContractValid(readback, contract) {
		return "", false
	}
	return readback, true
}

func resolveExplicitSkillRepairPath(ctx context.Context, configuredWorkspace, artifactPath string) (string, string, bool) {
	configuredWorkspace = strings.TrimSpace(configuredWorkspace)
	runWorkspace := strings.TrimSpace(projectctx.ResolvedWorkspace(ctx))
	if runWorkspace != "" && configuredWorkspace != "" && !projectctx.SameWorkspace(runWorkspace, configuredWorkspace) {
		return "", "", false
	}
	workspace := configuredWorkspace
	if runWorkspace != "" {
		workspace = runWorkspace
	}
	if workspace == "" {
		return "", "", false
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", "", false
	}
	workspace = filepath.Clean(workspace)
	target := strings.TrimSpace(artifactPath)
	if !filepath.IsAbs(target) {
		target = filepath.Join(workspace, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", "", false
	}
	target = filepath.Clean(target)
	if !projectctx.ContainsPath(workspace, target) || projectctx.EnsureResolvedPathInside(workspace, target) != nil {
		return "", "", false
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", false
	}
	extension := strings.ToLower(filepath.Ext(target))
	if extension != ".md" && extension != ".markdown" {
		return "", "", false
	}
	return workspace, target, true
}

func explicitSkillArtifactWasWritten(workspace, target string, writtenPaths []string) bool {
	resolved := resolvedExplicitSkillWrittenArtifacts(workspace, writtenPaths)
	if len(resolved) != 1 {
		return false
	}
	_, ok := resolved[projectctx.IdentityKey(target)]
	return ok
}

func resolvedExplicitSkillWrittenArtifacts(workspace string, writtenPaths []string) map[string]string {
	resolved := make(map[string]string)
	for _, candidate := range writtenPaths {
		_, writtenTarget, ok := resolveExplicitSkillRepairPath(context.Background(), workspace, candidate)
		if ok {
			resolved[projectctx.IdentityKey(writtenTarget)] = writtenTarget
		}
	}
	return resolved
}

func explicitSkillArtifactNonFieldContractValid(content string, contract *explicitSkillArtifactContract) bool {
	if contract == nil {
		return false
	}
	lines := explicitSkillArtifactLines(content)
	if contract.FirstLine != "" && (len(lines) == 0 || lines[0] != contract.FirstLine) {
		return false
	}
	for _, heading := range contract.Headings {
		if !explicitSkillArtifactHasExactLine(lines, heading) {
			return false
		}
	}
	return true
}

func explicitSkillArtifactContractValid(content string, contract *explicitSkillArtifactContract) bool {
	if !explicitSkillArtifactNonFieldContractValid(content, contract) {
		return false
	}
	lines := explicitSkillArtifactLines(content)
	for _, field := range contract.Fields {
		if !explicitSkillArtifactFieldValid(lines, field) {
			return false
		}
	}
	return true
}

func explicitSkillArtifactLines(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 0 {
		lines[0] = strings.TrimPrefix(strings.TrimSuffix(lines[0], "\r"), "\ufeff")
	}
	return lines
}

func normalizeExplicitSkillMarkdownEnumFields(content string, fields []explicitSkillFieldContract) (string, bool, bool) {
	lineEnding := "\n"
	if strings.Contains(content, "\r\n") {
		lineEnding = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	changed := false
	for _, field := range fields {
		if explicitSkillArtifactFieldValid(lines, field) {
			continue
		}
		fieldChanged, ok := normalizeExplicitSkillMarkdownEnumField(lines, field)
		if !ok {
			return "", false, false
		}
		changed = changed || fieldChanged
	}
	if !changed {
		return content, false, true
	}
	return strings.Join(lines, lineEnding), true, true
}

func normalizeExplicitSkillMarkdownEnumField(lines []string, field explicitSkillFieldContract) (bool, bool) {
	tables := explicitSkillMarkdownTables(lines)
	allowed := make(map[string]bool, len(field.AllowedValues))
	for _, value := range field.AllowedValues {
		allowed[value] = true
	}
	matchedTables := 0
	changed := false
	for _, table := range tables {
		column := -1
		for index, header := range table.Header {
			if header != field.Key {
				continue
			}
			if column >= 0 {
				return false, false
			}
			column = index
		}
		if column < 0 {
			continue
		}
		matchedTables++
		if !table.WellFormed || len(table.DataLineIndexes) == 0 {
			return false, false
		}
		for _, lineIndex := range table.DataLineIndexes {
			cells, ok := parseExplicitSkillMarkdownRow(lines[lineIndex])
			if !ok || column >= len(cells) {
				return false, false
			}
			cell := cells[column]
			value, ok := explicitSkillEnumCellValue(cell, field, allowed)
			if !ok {
				return false, false
			}
			canonical := explicitSkillCanonicalFieldValue(field, value)
			if cell == canonical {
				continue
			}
			rewritten, ok := replaceExplicitSkillMarkdownCell(lines[lineIndex], column, canonical)
			if !ok {
				return false, false
			}
			lines[lineIndex] = rewritten
			changed = true
		}
	}
	return changed, matchedTables > 0
}

func explicitSkillEnumCellValue(cell string, field explicitSkillFieldContract, allowed map[string]bool) (string, bool) {
	cell = strings.TrimSpace(cell)
	if allowed[cell] {
		return cell, true
	}
	for _, value := range field.AllowedValues {
		if cell == explicitSkillCanonicalFieldValue(field, value) {
			return value, true
		}
	}
	return "", false
}

func explicitSkillCanonicalFieldValue(field explicitSkillFieldContract, value string) string {
	colon := strings.Index(field.Syntax, ":")
	if colon < 0 {
		return field.Key + ": " + value
	}
	afterColon := field.Syntax[colon+1:]
	spaceCount := len(afterColon) - len(strings.TrimLeft(afterColon, " \t"))
	return field.Key + ":" + afterColon[:spaceCount] + value
}

func explicitSkillMarkdownTables(lines []string) []explicitSkillMarkdownTable {
	tables := make([]explicitSkillMarkdownTable, 0)
	for lineIndex := 0; lineIndex+1 < len(lines); lineIndex++ {
		header, headerOK := parseExplicitSkillMarkdownRow(lines[lineIndex])
		divider, dividerOK := parseExplicitSkillMarkdownRow(lines[lineIndex+1])
		if !headerOK || !dividerOK || len(header) == 0 || len(header) != len(divider) ||
			!explicitSkillMarkdownDivider(divider) {
			continue
		}
		table := explicitSkillMarkdownTable{Header: header, WellFormed: true}
		dataIndex := lineIndex + 2
		for ; dataIndex < len(lines); dataIndex++ {
			trimmed := strings.TrimSpace(strings.TrimSuffix(lines[dataIndex], "\r"))
			if trimmed == "" || !strings.HasPrefix(trimmed, "|") {
				break
			}
			row, ok := parseExplicitSkillMarkdownRow(lines[dataIndex])
			if !ok || len(row) != len(header) || explicitSkillMarkdownDivider(row) {
				table.WellFormed = false
				break
			}
			table.DataLineIndexes = append(table.DataLineIndexes, dataIndex)
		}
		tables = append(tables, table)
		if dataIndex > lineIndex+1 {
			lineIndex = dataIndex - 1
		}
	}
	return tables
}

func parseExplicitSkillMarkdownRow(line string) ([]string, bool) {
	line = strings.TrimSuffix(line, "\r")
	if line != strings.TrimSpace(line) || len(line) < 3 || !strings.HasPrefix(line, "|") ||
		!strings.HasSuffix(line, "|") || strings.Contains(line, `\|`) {
		return nil, false
	}
	parts := strings.Split(line, "|")
	if len(parts) < 3 || parts[0] != "" || parts[len(parts)-1] != "" {
		return nil, false
	}
	cells := make([]string, 0, len(parts)-2)
	for _, part := range parts[1 : len(parts)-1] {
		cells = append(cells, strings.TrimSpace(part))
	}
	return cells, true
}

func explicitSkillMarkdownDivider(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if !explicitSkillMarkdownDividerPattern.MatchString(cell) {
			return false
		}
	}
	return true
}

func replaceExplicitSkillMarkdownCell(line string, column int, value string) (string, bool) {
	if _, ok := parseExplicitSkillMarkdownRow(line); !ok {
		return "", false
	}
	parts := strings.Split(strings.TrimSuffix(line, "\r"), "|")
	partIndex := column + 1
	if partIndex <= 0 || partIndex >= len(parts)-1 {
		return "", false
	}
	raw := parts[partIndex]
	leading := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
	trailing := raw[len(strings.TrimRight(raw, " \t")):]
	parts[partIndex] = leading + value + trailing
	return strings.Join(parts, "|"), true
}

func ExplicitSkillCompletionIssues(ctx context.Context) []string {
	requirement := explicitSkillRequirementFromContext(ctx)
	if requirement == nil {
		return nil
	}
	requirement.mu.RLock()
	activated := requirement.Activated
	contract := cloneExplicitSkillArtifactContract(requirement.Artifact)
	workspace := requirement.Workspace
	writes := append([]string(nil), requirement.ArtifactWrites...)
	reads := append([]explicitSkillArtifactRead(nil), requirement.ArtifactReads...)
	requirement.mu.RUnlock()
	if !activated || contract == nil {
		return nil
	}
	if len(resolvedExplicitSkillWrittenArtifacts(workspace, writes)) > 1 {
		return []string{fmt.Sprintf("required Skill artifact %q was written to multiple ambiguous paths", contract.Filename)}
	}

	var artifact *explicitSkillArtifactRead
	for i := len(reads) - 1; i >= 0; i-- {
		if reads[i].Complete && explicitSkillArtifactPathMatches(contract.Filename, reads[i].Path) {
			candidate := reads[i]
			artifact = &candidate
			break
		}
	}
	if artifact == nil {
		return []string{fmt.Sprintf("required Skill artifact %q has not been successfully re-read in full", contract.Filename)}
	}

	lines := explicitSkillArtifactLines(artifact.Content)
	issues := make([]string, 0)
	if contract.FirstLine != "" && (len(lines) == 0 || lines[0] != contract.FirstLine) {
		issues = append(issues, fmt.Sprintf("artifact %q first line must be exactly %q", artifact.Path, contract.FirstLine))
	}
	for _, heading := range contract.Headings {
		if !explicitSkillArtifactHasExactLine(lines, heading) {
			issues = append(issues, fmt.Sprintf("artifact %q is missing exact required heading %q", artifact.Path, heading))
		}
	}
	for _, field := range contract.Fields {
		if !explicitSkillArtifactFieldValid(lines, field) {
			issues = append(issues, fmt.Sprintf("artifact %q must use exact field syntax %q", artifact.Path, field.Syntax))
		}
	}
	return issues
}

func cloneExplicitSkillArtifactContract(contract *explicitSkillArtifactContract) *explicitSkillArtifactContract {
	if contract == nil {
		return nil
	}
	clone := *contract
	clone.Headings = append([]string(nil), contract.Headings...)
	clone.Fields = make([]explicitSkillFieldContract, len(contract.Fields))
	for i, field := range contract.Fields {
		clone.Fields[i] = field
		clone.Fields[i].AllowedValues = append([]string(nil), field.AllowedValues...)
	}
	return &clone
}

func explicitSkillArtifactPathMatches(required, actual string) bool {
	required = strings.TrimPrefix(path.Clean(strings.ReplaceAll(strings.TrimSpace(required), "\\", "/")), "./")
	actual = strings.TrimPrefix(path.Clean(strings.ReplaceAll(strings.TrimSpace(actual), "\\", "/")), "./")
	if required == "" || actual == "" {
		return false
	}
	if strings.Contains(required, "/") {
		return actual == required || strings.HasSuffix(actual, "/"+required)
	}
	return path.Base(actual) == required
}

func explicitSkillArtifactHasExactLine(lines []string, required string) bool {
	for _, line := range lines {
		if strings.TrimSuffix(line, "\r") == required {
			return true
		}
	}
	return false
}

func explicitSkillArtifactFieldValid(lines []string, field explicitSkillFieldContract) bool {
	hasTable, tablesValid := explicitSkillMarkdownFieldValid(lines, field)
	standaloneFound := false
	for _, line := range lines {
		candidate := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		for _, prefix := range []string{"- ", "* ", "+ "} {
			if strings.HasPrefix(candidate, prefix) {
				candidate = strings.TrimSpace(strings.TrimPrefix(candidate, prefix))
				break
			}
		}
		if !strings.HasPrefix(candidate, field.Key+":") {
			continue
		}
		standaloneFound = true
		valid := false
		for _, value := range field.AllowedValues {
			if candidate == explicitSkillCanonicalFieldValue(field, value) {
				valid = true
				break
			}
		}
		if !valid {
			return false
		}
	}
	if hasTable {
		return tablesValid
	}
	return standaloneFound
}

func explicitSkillMarkdownFieldValid(lines []string, field explicitSkillFieldContract) (bool, bool) {
	tables := explicitSkillMarkdownTables(lines)
	hasTable := false
	for _, table := range tables {
		column := -1
		for index, header := range table.Header {
			if header != field.Key {
				continue
			}
			if column >= 0 {
				return true, false
			}
			column = index
		}
		if column < 0 {
			continue
		}
		hasTable = true
		if !table.WellFormed || len(table.DataLineIndexes) == 0 {
			return true, false
		}
		for _, lineIndex := range table.DataLineIndexes {
			cells, ok := parseExplicitSkillMarkdownRow(lines[lineIndex])
			if !ok || column >= len(cells) {
				return true, false
			}
			valid := false
			for _, value := range field.AllowedValues {
				if cells[column] == explicitSkillCanonicalFieldValue(field, value) {
					valid = true
					break
				}
			}
			if !valid {
				return true, false
			}
		}
	}
	return hasTable, hasTable
}

func BuildExplicitSkillRepairPrompt(ctx context.Context, issues []string) map[string]interface{} {
	requirement := explicitSkillRequirementFromContext(ctx)
	var contract *explicitSkillArtifactContract
	if requirement != nil {
		requirement.mu.RLock()
		contract = cloneExplicitSkillArtifactContract(requirement.Artifact)
		requirement.mu.RUnlock()
	}
	if len(issues) == 0 {
		issues = ExplicitSkillCompletionIssues(ctx)
	}
	contractLines := []string{"Preserve every literal contract returned by the already-activated Skill."}
	if contract != nil {
		contractLines = []string{fmt.Sprintf("Required filename: `%s`", contract.Filename)}
		if contract.FirstLine != "" {
			contractLines = append(contractLines, fmt.Sprintf("Exact first line: `%s`", contract.FirstLine))
		}
		for _, heading := range contract.Headings {
			contractLines = append(contractLines, fmt.Sprintf("Exact heading: `%s`", heading))
		}
		for _, field := range contract.Fields {
			contractLines = append(contractLines, fmt.Sprintf("Exact field syntax: `%s`", field.Syntax))
		}
	}
	return agentloop.NewInternalControlMessage("EXPLICIT SKILL ARTIFACT REPAIR", `[EXPLICIT SKILL ARTIFACT REPAIR]
The explicit Skill artifact contract is not yet satisfied:
- `+strings.Join(issues, "\n- ")+`

`+strings.Join(contractLines, "\n")+`

	Do not call use_skill again. Repair the existing final artifact (or create the exact required file if it is absent), then call read_file on that exact final artifact without lines_range. Do not claim completion until the full re-read contains every required literal exactly.`)
}

func AppendExplicitSkillArtifactFailure(output string, issues []string) string {
	message := "Explicit Skill artifact contract was not satisfied: " + strings.Join(issues, "; ") + "."
	if strings.TrimSpace(output) == "" {
		return message
	}
	return strings.TrimSpace(output) + "\n\n## Skill artifact verification failed\n" + message
}
