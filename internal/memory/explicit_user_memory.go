package memory

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode"

	"mac/internal/security"
)

// ExplicitMemoryWrite describes an additive write, never a semantic correction.
// Created is false for unsupported input and for a key already retained, edited,
// or forgotten by its owner. Key is populated only for a recognized instruction.
type ExplicitMemoryWrite struct {
	Key     string
	Scope   MemoryScope
	Kind    string
	Created bool
}

var explicitMemoryRole = regexp.MustCompile(`(?i)(?:^|[\s:：])(?:system|assistant|developer|user|tool|系统|助手|用户)\s*[:：]`)
var explicitMemoryCredential = regexp.MustCompile(`(?i)(?:api[-_ ]?key|password|passwd|secret|token|密钥|密码|口令|私钥|bearer\s|-----BEGIN|\b(?:sk-|gh[pousr]_)[a-z0-9_-]{12,}|\bAKIA[A-Z0-9]{16}\b)`)
var explicitMemoryInjection = security.NewPromptInjectionDetector()

// RecordExplicitUserTurn saves only an explicit, bounded instruction frozen by
// the host using WithUserTurn. Tool arguments, assistant replies, and recalled
// context cannot supply this evidence. Repeated input must not replace a user's
// edits or resurrect a forgotten key, so this path always uses CreateWithSource.
func (s *Service) RecordExplicitUserTurn(ctx context.Context, owner Owner) (ExplicitMemoryWrite, error) {
	var result ExplicitMemoryWrite
	if ctx == nil {
		return result, errors.New("explicit memory requires a context")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if s == nil || s.global {
		return result, errors.New("explicit user memory requires a personal service")
	}
	turn, _ := ctx.Value(userTurnKey{}).(string)
	turn = strings.TrimSpace(turn)
	value, scope, kind, ok := explicitUserMemoryInstruction(turn)
	if !ok {
		return result, nil
	}
	// Collapse presentation-only spacing and terminal punctuation for identity,
	// while retaining the exact user wording, case, conditions and entity names.
	normalized := strings.TrimRight(strings.Join(strings.Fields(value), " "), "。.!！")
	result = ExplicitMemoryWrite{Key: "remember_" + memoryHash(string(scope), normalized)[:32], Scope: scope, Kind: kind}
	source, _ := durableWriteSource(ctx, value)
	source.Type, source.Quote = "user_explicit", turn
	// Recording this new instruction must not scan or promote an old profile.
	owner.ImportLegacy = false
	err := s.CreateWithSource(ctx, owner, result.Key, value, scope, kind, 6, []string{"explicit-user-turn"}, source)
	if errors.Is(err, ErrMemoryKeyExists) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Created = true
	return result, nil
}

func explicitUserMemoryInstruction(turn string) (string, MemoryScope, string, bool) {
	if turn == "" || len([]rune(turn)) > 300 || strings.ContainsAny(turn, "\r\n") || security.HasZeroWidthChars(turn) {
		return "", "", "", false
	}
	for _, r := range turn {
		if unicode.IsControl(r) && r != '\t' {
			return "", "", "", false
		}
	}
	lower := strings.ToLower(turn)
	if explicitMemoryRole.MatchString(turn) || explicitMemoryCredential.MatchString(turn) || explicitMemoryInjection.Scan(turn, "explicit_user_memory").Severity != security.SevNone {
		return "", "", "", false
	}
	for _, fragment := range []string{"```", "<", ">", "?", "？", "这次", "本次", "仅此次", "暂时", "临时", "一次性", "这一次", "不要记住", "不用记住", "别记住", "不要保存", "不用保存", "无需保存", "无需记住", "别保存", "不需要记住", "不要当成", "只是举例", "例如", "比如", "他说", "她说", "原文", "引用", "是否", "能否", "为什么", "是什么", "什么意思", "能不能", "可不可以", "系统提示", "系统指令", "忽略之前", "忽略所有", "just this time", "this time only", "for now", "temporarily", "one-off", "do not remember", "don't remember", "do not save", "don't save", "do not store", "don't store", "not to remember", "not to save", "not to store", "for example", "he said", "she said", "quoted"} {
		if strings.Contains(lower, fragment) {
			return "", "", "", false
		}
	}
	for _, suffix := range []string{"吗", "嘛", "么", "呢"} {
		if strings.HasSuffix(strings.TrimRight(turn, "。.!！ "), suffix) {
			return "", "", "", false
		}
	}
	value := ""
	for _, prefix := range []string{"请保存为长期记忆", "请帮我记住", "帮我记住", "请记住", "记住", "please remember", "remember", "save to memory"} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		tail := turn[len(prefix):]
		if prefix[0] < 128 && tail != "" && !strings.ContainsRune(" :：=\t", []rune(tail)[0]) {
			continue
		}
		payload := strings.TrimLeft(strings.TrimSpace(tail), "：:= \t")
		if _, explicit := durableWriteSource(WithUserTurn(context.Background(), turn), payload); explicit {
			value = payload
		}
		break
	}
	if value == "" && explicitLongTermRequirement(lower) {
		value = turn
	}
	if value == "" || len([]rune(value)) < 3 || strings.ContainsRune("\"'“‘`{[", []rune(value)[0]) {
		return "", "", "", false
	}
	scope := MemoryUser
	scopeText := strings.ToLower(value)
	for _, prefix := range []string{"以后", "今后", "从现在开始", "from now on", "going forward"} {
		if strings.HasPrefix(scopeText, prefix) {
			scopeText = strings.TrimLeft(scopeText[len(prefix):], "，,:： \t")
			break
		}
	}
	for _, prefix := range []string{"本项目", "当前项目", "这个项目", "this project", "for this project", "in this project"} {
		if strings.HasPrefix(scopeText, prefix) {
			scope = MemoryProject
			break
		}
	}
	kind := "fact"
	for _, marker := range []string{"偏好", "喜欢", "习惯", "prefer ", "prefers ", "preference", "likes ", "like "} {
		if strings.Contains(strings.ToLower(value), marker) {
			kind = "preference"
			break
		}
	}
	if len(DetectArtifactFormatPreferences(value)) > 0 || explicitLongTermRequirement(strings.ToLower(value)) {
		kind = "preference"
	}
	return value, scope, kind, true
}

func explicitLongTermRequirement(lower string) bool {
	for _, prefix := range []string{"以后", "今后", "从现在开始", "from now on ", "from now on,", "going forward ", "going forward,"} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		for _, marker := range []string{"默认", "一律", "统一", "每次", "始终", "总是", "都用", "都要", "都先", "只用", "只要", "prefer ", "always ", "by default", "use ", "please "} {
			if strings.Contains(lower[len(prefix):], marker) {
				return true
			}
		}
	}
	return false
}
