package skills

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	skillMentionClientReference     = "at_mention"
	defaultSkillMentionTask         = "执行已绑定 Skill 的默认工作流；如果缺少必需输入，请向用户询问，不要自行编造。"
	medGuidNaturalDialogueDirective = "本轮交付方式：这是普通健康诊聊，不是正式报告。直接讲对用户决策有用的要点，通常控制在 220–340 个中文字符、最多 3 个短段；信息少就更短，不为凑字数补背景。第一句直接写判断，但不要输出“核心判断：”“结论：”等标签。第二段只解释最多 2 个最可能且会改变行动的现代医学原因，并用一个短句补充 1 个最相关的中医待辨方向；不要列备选证型。最后用“先做：”给不超过 3 项按优先级排列的行动，科室只给一个首选路径。只有一个确实会改变判断的问题时才用“还需确认：”追问；最多一个问句且只问一件事，不得用“以及/和/或”把多个问题塞在一起，否则不要追问。不要自我介绍、寒暄、复述用户原话或数据、罗列缺失资料、总结全文，也不要写“下面分几层”“仅供参考”等套话。不要使用 Markdown 标题、分隔线、表格、连续项目符号或完整清单；除“先做：”“还需确认：”外不使用标签。不要逐项解释正常数据，不罗列所有可能病因、证型、科室、风险提示或生活常识。用户未提供年龄、性别或设备算法时不得推断；未知设备分数只称设备提示，不猜测由 HRV 等具体机制计算。没有当前红旗症状时，不附泛化急症清单；有红旗时直接给急诊行动。"
)

type skillMentionCandidate struct {
	Alias     string
	SkillName string
	Canonical bool
}

type skillMentionHit struct {
	skillMentionCandidate
	Index   int
	End     int
	Mention string
}

type MentionRunRequest struct {
	SkillName       string
	ClientReference string
}

// ResolveExplicitMention converts one complete @name or @alias into a
// host-bound Skill Run request. It intentionally does no fuzzy matching: an
// unknown @mention remains ordinary conversation, while an ambiguous alias is
// rejected instead of letting catalog/map order choose a workflow.
func ResolveExplicitMention(message string, loader *SkillsLoader) (*MentionRunRequest, string, bool, error) {
	message = strings.TrimSpace(message)
	if message == "" || loader == nil || !strings.Contains(message, "@") {
		return nil, message, false, nil
	}

	hits := make([]skillMentionHit, 0)
	for _, candidate := range skillMentionCandidates(loader.SnapshotSkills()) {
		index, end, mention, ok := findExplicitSkillMention(message, candidate.Alias)
		if !ok {
			continue
		}
		hits = append(hits, skillMentionHit{
			skillMentionCandidate: candidate,
			Index:                 index, End: end, Mention: mention,
		})
	}
	if len(hits) == 0 {
		return nil, message, false, nil
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Index != hits[j].Index {
			return hits[i].Index < hits[j].Index
		}
		if len(hits[i].Mention) != len(hits[j].Mention) {
			return len(hits[i].Mention) > len(hits[j].Mention)
		}
		if hits[i].Canonical != hits[j].Canonical {
			return hits[i].Canonical
		}
		return hits[i].SkillName < hits[j].SkillName
	})

	best := hits[0]
	resolved := map[string]skillMentionHit{strings.ToLower(best.SkillName): best}
	for _, hit := range hits[1:] {
		if hit.Index != best.Index || len(hit.Mention) != len(best.Mention) {
			break
		}
		// A canonical installed name has the same precedence as
		// SkillsLoader.getSkillByNameOrAlias. Aliases only compete when no
		// canonical name matched this exact mention.
		if best.Canonical && !hit.Canonical {
			continue
		}
		if !best.Canonical && hit.Canonical {
			best = hit
			resolved = map[string]skillMentionHit{strings.ToLower(hit.SkillName): hit}
			continue
		}
		resolved[strings.ToLower(hit.SkillName)] = hit
	}
	if len(resolved) > 1 {
		names := make([]string, 0, len(resolved))
		for _, hit := range resolved {
			names = append(names, hit.SkillName)
		}
		sort.Strings(names)
		return nil, message, false, fmt.Errorf(
			"Skill mention %q is ambiguous; it matches %s",
			best.Mention, strings.Join(names, ", "),
		)
	}
	for _, hit := range resolved {
		best = hit
	}

	task := cleanMatchedSkillTask(message, best.Index, best.End)
	if task == "" {
		task = defaultSkillMentionTask
	}
	task = applySkillMentionDeliveryDirective(best.SkillName, task)
	return &MentionRunRequest{
		SkillName:       best.SkillName,
		ClientReference: skillMentionClientReference,
	}, task, true, nil
}

func applySkillMentionDeliveryDirective(skillName, task string) string {
	task = strings.TrimSpace(task)
	if !strings.EqualFold(strings.TrimSpace(skillName), "med-guid") || medGuidRequestsFormalDelivery(task) {
		return task
	}
	return task + "\n\n" + medGuidNaturalDialogueDirective
}

func medGuidRequestsFormalDelivery(task string) bool {
	task = strings.TrimSpace(task)
	if task == "" {
		return false
	}
	lower := strings.ToLower(task)
	for _, naturalMarker := range []string{
		"不要报告", "不需要报告", "别写报告", "不用报告", "不要结构化", "不用结构化",
		"不要表格", "不用表格", "no report", "not a report", "without a report",
	} {
		if strings.Contains(lower, naturalMarker) {
			return false
		}
	}
	for _, formalMarker := range []string{
		"标准版", "精简版", "简版", "短版", "完整版", "详细版", "详版",
		"正式报告", "结构化报告", "生成报告", "出一份报告", "写一份报告", "整理成报告", "按报告格式",
		"structured report", "formal report", "generate a report", "write a report",
	} {
		if strings.Contains(lower, formalMarker) {
			return true
		}
	}
	return false
}

func skillMentionCandidates(snapshots []SkillSnapshot) []skillMentionCandidate {
	candidates := make([]skillMentionCandidate, 0, len(snapshots)*2)
	seen := make(map[string]bool)
	for _, snapshot := range snapshots {
		name := strings.TrimSpace(snapshot.Name)
		if name == "" {
			continue
		}
		labels := append([]string{name}, snapshot.Aliases...)
		for index, label := range labels {
			label = NormalizeMentionAlias(label)
			if label == "" {
				continue
			}
			key := strings.ToLower(name) + "\x00" + strings.ToLower(label)
			if seen[key] {
				continue
			}
			seen[key] = true
			candidates = append(candidates, skillMentionCandidate{
				Alias: label, SkillName: name, Canonical: index == 0,
			})
		}
	}
	return candidates
}

func SkillMentionAliasConflicts(snapshots []SkillSnapshot) map[string][]string {
	owners := make(map[string]map[string]bool)
	for _, candidate := range skillMentionCandidates(snapshots) {
		key := strings.ToLower(candidate.Alias)
		if owners[key] == nil {
			owners[key] = make(map[string]bool)
		}
		owners[key][candidate.SkillName] = true
	}
	conflicts := make(map[string][]string)
	for alias, skills := range owners {
		if len(skills) < 2 {
			continue
		}
		for name := range skills {
			conflicts[alias] = append(conflicts[alias], name)
		}
		sort.Strings(conflicts[alias])
	}
	return conflicts
}

func NormalizeMentionAlias(alias string) string {
	alias = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(alias), "@"))
	if alias == "" || len(alias) > 128 {
		return ""
	}
	for _, char := range alias {
		if unicode.IsControl(char) || char == '@' {
			return ""
		}
	}
	return alias
}

func findExplicitSkillMention(message, alias string) (int, int, string, bool) {
	alias = NormalizeMentionAlias(alias)
	if alias == "" {
		return -1, -1, "", false
	}
	pattern, err := regexp.Compile(`(?i)` + regexp.QuoteMeta("@"+alias))
	if err != nil {
		return -1, -1, "", false
	}
	for _, location := range pattern.FindAllStringIndex(message, -1) {
		if location[0] > 0 {
			previous, _ := utf8.DecodeLastRuneInString(message[:location[0]])
			// Reject email/identifier fragments while still allowing natural
			// Chinese such as "请用@报告".
			if previous <= unicode.MaxASCII &&
				(unicode.IsLetter(previous) || unicode.IsNumber(previous) ||
					previous == '_' || previous == '-' || previous == '.') {
				continue
			}
		}
		if location[1] < len(message) {
			next, width := utf8.DecodeRuneInString(message[location[1]:])
			if unicode.IsLetter(next) || unicode.IsNumber(next) || unicode.IsMark(next) ||
				next == '_' || next == '-' {
				continue
			}
			// A dot followed by a word rune is part of an email/qualified
			// identifier. A terminal sentence period remains valid punctuation.
			if next == '.' && location[1]+width < len(message) {
				afterDot, _ := utf8.DecodeRuneInString(message[location[1]+width:])
				if unicode.IsLetter(afterDot) || unicode.IsNumber(afterDot) || unicode.IsMark(afterDot) || afterDot == '_' {
					continue
				}
			}
		}
		return location[0], location[1], message[location[0]:location[1]], true
	}
	return -1, -1, "", false
}

func cleanMatchedSkillTask(message string, start, end int) string {
	if start < 0 || end < start || end > len(message) {
		return strings.TrimSpace(message)
	}
	before := message[:start]
	after := message[end:]
	if strings.TrimSpace(before) == "" {
		after = strings.TrimSpace(after)
		after = strings.TrimLeft(after, ":：,，;；、")
		return strings.TrimSpace(after)
	}
	return strings.TrimSpace(before + after)
}

func PrependMentionToTask(message, skillTask string) string {
	message = strings.TrimSpace(message)
	skillTask = strings.TrimSpace(skillTask)
	if skillTask == "" || skillTask == message {
		return message
	}
	return "显式 @Skill 解析后的当前任务：\n" + skillTask +
		"\n\n用户原始消息（保留用于对话展示和其中其他 @mention）：\n" + message
}
