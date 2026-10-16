package middleware

import (
	"regexp"
	"strings"
)

// InputSanitizationMiddleware defends against prompt injection by escaping
// blocked XML-like tags in user input. Strategy: de-identify, don't reject.
//
// Blocked tags like <system>, <instruction>, <role>, <override> are HTML-escaped
// (→ &lt;system&gt;) so they render as literal text rather than structured context
// markers. The user's intent is preserved, but the tags lose semantic significance.
//
// Clean user input is wrapped in plain-text boundary markers:
//   --- BEGIN USER INPUT ---
//   <actual user text>
//   --- END USER INPUT ---
// as a secondary semantic defense (OWASP structured-prompt guidance).
//
// Only the last genuine user message is sanitized. Normal HTML tags (<div>, <span>)
// are NOT affected — only the finite blocked set.

type InputSanitizationMiddleware struct {
	Base
}

func NewInputSanitizationMiddleware() *InputSanitizationMiddleware {
	return &InputSanitizationMiddleware{Base: NewBase("input_sanitization")}
}

// Blocked tag names: system-reserved + common injection patterns.
var blockedTagNames = []string{
	// System-reserved (used by the agent framework for structured context)
	"system-reminder", "memory", "current_date", "think", "analysis",
	"subagent_system", "skill_system", "uploaded_files", "todo_list_system",
	// Common prompt-injection patterns
	"system", "instruction", "role", "important", "override", "ignore", "prompt",
}

var blockedTagPattern = buildBlockedTagPattern()

func buildBlockedTagPattern() *regexp.Regexp {
	var parts []string
	for _, tag := range blockedTagNames {
		parts = append(parts, regexp.QuoteMeta(tag))
	}
	// Match <tag>, </tag>, <tag attrs>, <tag/>, bare <tag
	pattern := `<\s*/?\s*(?:` + strings.Join(parts, "|") + `)\b[^>]*>?`
	return regexp.MustCompile(pattern)
}

const (
	UserInputBegin   = "--- BEGIN USER INPUT ---"
	UserInputEnd     = "--- END USER INPUT ---"
	NeutralizedBegin = "[BEGIN USER INPUT]"
	NeutralizedEnd   = "[END USER INPUT]"
)

var boundaryTokenRe = regexp.MustCompile(
	regexp.QuoteMeta(UserInputBegin) + "|" + regexp.QuoteMeta(UserInputEnd),
)

func (m *InputSanitizationMiddleware) BeforeModel(ctx *Context) error {
	if len(ctx.Messages) == 0 {
		return nil
	}

	// Find the last genuine user message (not system-injected, not a summary)
	for i := len(ctx.Messages) - 1; i >= 0; i-- {
		msg := ctx.Messages[i]
		role, _ := msg["role"].(string)
		if role != "user" {
			continue
		}
		content, _ := msg["content"].(string)
		if content == "" {
			continue
		}

		processed := SanitizeUserInput(content)
		if processed != content {
			msg["content"] = processed
			ctx.Messages[i] = msg
		}
		break // only sanitize the last user message
	}

	return nil
}

func SanitizeUserInput(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	// Step 1: HTML-escape blocked tags
	text = blockedTagPattern.ReplaceAllStringFunc(text, func(match string) string {
		return strings.ReplaceAll(strings.ReplaceAll(match, "<", "&lt;"), ">", "&gt;")
	})

	// Step 2: Idempotency — if already properly wrapped, neutralize any
	// inner boundary tokens (break-out defense), then return unchanged.
	if strings.HasPrefix(text, UserInputBegin) && strings.HasSuffix(text, UserInputEnd) {
		inner := text[len(UserInputBegin) : len(text)-len(UserInputEnd)]
		neutralized := boundaryTokenRe.ReplaceAllStringFunc(inner, func(m string) string {
			if m == UserInputBegin {
				return NeutralizedBegin
			}
			return NeutralizedEnd
		})
		if neutralized != inner {
			return UserInputBegin + neutralized + UserInputEnd
		}
		return text
	}

	// Step 3: Neutralize boundary tokens embedded by the user.
	// Prevents boundary forgery: if the user types "--- BEGIN USER INPUT ---",
	// it becomes "[BEGIN USER INPUT]" so it can't be mistaken for a real boundary.
	text = boundaryTokenRe.ReplaceAllStringFunc(text, func(m string) string {
		if m == UserInputBegin {
			return NeutralizedBegin
		}
		return NeutralizedEnd
	})

	// Step 4: Wrap in boundary markers
	return UserInputBegin + "\n" + text + "\n" + UserInputEnd
}
