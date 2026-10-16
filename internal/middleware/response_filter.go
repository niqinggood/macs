package middleware

import (
	"regexp"
	"strings"

	"mac/internal/textutil"
)

// ResponseFilterMiddleware cleans LLM responses before they reach users.
// It strips:
//   - <tool_call>...</tool_call> XML blocks
//   - DSML tool-call protocol blocks
//   - <think>...</think> reasoning blocks (from DeepSeek-R1 etc.)
//   - Raw JSON tool call traces
//   - Excessive newlines
//
// This runs in AfterModel, so downstream consumers (gateway, DE, Web UI)
// always receive clean output regardless of which path the response takes.

type ResponseFilterMiddleware struct {
	Base
}

func NewResponseFilterMiddleware() *ResponseFilterMiddleware {
	return &ResponseFilterMiddleware{Base: NewBase("response_filter")}
}

var (
	reToolCallBlock = regexp.MustCompile(`(?s)<tool_call>.*?</tool_call>`)
	reToolTag       = regexp.MustCompile(`(?s)<tool\s[^>]*>.*?</tool>`)
	reArguments     = regexp.MustCompile(`(?s)<arguments>.*?</arguments>`)
	reThinking      = regexp.MustCompile(`(?s)<think>.*?</think>`)
	reMultiNewline  = regexp.MustCompile(`\n{3,}`)
	// Some providers emit tool calls in ordinary assistant content using the
	// DSML envelope below. Accept ASCII or full-width vertical bars and harmless
	// whitespace/case differences, but fail closed on any incomplete envelope.
	reDSMLToolCallsOpen  = regexp.MustCompile(`(?i)<\s*[|｜]{2}\s*DSML\s*[|｜]{2}\s*tool_calls\s*>`)
	reDSMLToolCallsClose = regexp.MustCompile(`(?i)<\s*/\s*[|｜]{2}\s*DSML\s*[|｜]{2}\s*tool_calls\s*>`)
	reDSMLProtocolMarker = regexp.MustCompile(`(?i)<\s*/?\s*[|｜]{2}\s*DSML\b`)
	// Catches raw JSON tool-call-like patterns in model output
	reJSONToolCall = regexp.MustCompile(`\{"name":\s*"[^"]+",\s*"(?:arguments|args)":\s*\{[^}]*\}\}`)
)

func (m *ResponseFilterMiddleware) AfterModel(ctx *Context) error {
	// Find the last assistant message and clean it
	if len(ctx.Messages) == 0 {
		return nil
	}
	for i := len(ctx.Messages) - 1; i >= 0; i-- {
		msg := ctx.Messages[i]
		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}
		content, _ := msg["content"].(string)
		if content == "" {
			continue
		}
		cleaned := CleanResponse(content)
		if cleaned != content {
			msg["content"] = cleaned
			ctx.Messages[i] = msg
		}
		break // only clean the last assistant message
	}
	return nil
}

// cleanResponse applies all response cleaning rules outside Markdown code
// fences. Fenced protocol examples are user-facing technical content and must
// remain byte-for-byte intact (apart from the existing outer TrimSpace).
func CleanResponse(s string) string {
	cleaned, ok := cleanResponsePreservingMarkdownFences(s)
	if !ok {
		// A partial DSML envelope is neither safe prose nor an executable tool
		// call. Returning no content lets the existing terminal-output guard
		// retry/recover instead of leaking protocol fragments to the user.
		return ""
	}
	return strings.TrimSpace(cleaned)
}

func cleanResponsePreservingMarkdownFences(s string) (string, bool) {
	lines := strings.SplitAfter(s, "\n")
	var out strings.Builder
	var outside strings.Builder
	inFence := false

	flushOutside := func() bool {
		if outside.Len() == 0 {
			return true
		}
		cleaned, ok := cleanResponseOutsideMarkdownFences(outside.String())
		outside.Reset()
		if !ok {
			return false
		}
		out.WriteString(cleaned)
		return true
	}

	for _, line := range lines {
		if textutil.IsMarkdownFenceBoundary(line) {
			if !inFence && !flushOutside() {
				return "", false
			}
			out.WriteString(line)
			inFence = !inFence
			continue
		}
		if inFence {
			out.WriteString(line)
		} else {
			outside.WriteString(line)
		}
	}
	if !flushOutside() {
		return "", false
	}
	return out.String(), true
}

func cleanResponseOutsideMarkdownFences(s string) (string, bool) {
	var ok bool
	s, ok = stripDSMLToolCallBlocks(s)
	if !ok {
		return "", false
	}
	s = reToolCallBlock.ReplaceAllString(s, "")
	s = reToolTag.ReplaceAllString(s, "")
	s = reArguments.ReplaceAllString(s, "")
	s = reThinking.ReplaceAllString(s, "")
	s = reJSONToolCall.ReplaceAllString(s, "")
	s = reMultiNewline.ReplaceAllString(s, "\n\n")
	return s, true
}

// stripDSMLToolCallBlocks removes complete DSML envelopes and rejects every
// unmatched, nested, or otherwise residual DSML protocol marker.
func stripDSMLToolCallBlocks(s string) (string, bool) {
	var out strings.Builder
	rest := s
	for {
		open := reDSMLToolCallsOpen.FindStringIndex(rest)
		close := reDSMLToolCallsClose.FindStringIndex(rest)
		if open == nil {
			if close != nil || reDSMLProtocolMarker.MatchString(rest) {
				return "", false
			}
			out.WriteString(rest)
			return out.String(), true
		}
		if close != nil && close[0] < open[0] {
			return "", false
		}

		out.WriteString(rest[:open[0]])
		body := rest[open[1]:]
		close = reDSMLToolCallsClose.FindStringIndex(body)
		if close == nil {
			return "", false
		}
		if nestedOpen := reDSMLToolCallsOpen.FindStringIndex(body); nestedOpen != nil && nestedOpen[0] < close[0] {
			return "", false
		}
		rest = body[close[1]:]
	}
}

// StripToolCalls is the public helper (replaces the private one in ai_employee.go).
// Kept as a standalone function for callers that don't go through the middleware chain.
func StripToolCalls(s string) string {
	return CleanResponse(s)
}
