package textnormalize

import (
	"encoding/json"
	"strings"
)

// DecodeProviderJSON deliberately parses the provider envelope before any
// lossy text cleanup. Removing control bytes from serialized JSON can turn an
// invalid response into valid JSON while leaving printable terminal fragments
// in message.content.
func DecodeProviderJSON(body []byte, target interface{}) error {
	return json.Unmarshal(body, target)
}

// ModelText removes terminal control sequences from decoded model text
// and conservatively drops a short terminal-like tail after a complete JSON
// value, a closed Markdown fence, or an otherwise complete sentence. It does
// not search for provider-specific literals, so ordinary bracketed prose and
// code remain intact.
func ModelText(text string) string {
	if text == "" {
		return text
	}
	return stripStructuredTerminalTail(StripTerminalControlSequences(text))
}

type terminalControlState uint8

const (
	terminalStateText terminalControlState = iota
	terminalStateEscape
	terminalStateEscapeIntermediate
	terminalStateCSI
	terminalStateString
	terminalStateStringEscape
)

// TerminalControlStreamSanitizer is deliberately stateful. A provider is free
// to split an escape sequence at any UTF-8 string boundary, for example
// "\x1b[" / "31m" or "\x1b]0;title" / "\x07". Holding an incomplete
// sequence here prevents its printable tail from leaking through a streaming
// callback before the final, whole-message sanitizer gets a chance to run.
//
// The zero value is ready to use.
type TerminalControlStreamSanitizer struct {
	state terminalControlState
}

func (s *TerminalControlStreamSanitizer) SanitizeChunk(text string) string {
	if text == "" {
		return ""
	}

	var out strings.Builder
	out.Grow(len(text))

	for _, r := range text {
	processRune:
		switch s.state {
		case terminalStateText:
			switch r {
			case 0x1b: // ESC
				s.state = terminalStateEscape
			case 0x9b: // C1 CSI
				s.state = terminalStateCSI
			case 0x90, 0x98, 0x9d, 0x9e, 0x9f: // DCS/SOS/OSC/PM/APC
				s.state = terminalStateString
			default:
				if isDiscardedTerminalControl(r) {
					continue
				}
				out.WriteRune(r)
			}

		case terminalStateEscape:
			switch {
			case r == 0x1b:
				// A fresh ESC supersedes the incomplete one.
			case r == '[':
				s.state = terminalStateCSI
			case r == ']' || r == 'P' || r == '^' || r == '_' || r == 'X':
				s.state = terminalStateString
			case r >= 0x20 && r <= 0x2f:
				s.state = terminalStateEscapeIntermediate
			case r >= 0x30 && r <= 0x7e:
				// Complete two-byte escape command.
				s.state = terminalStateText
			case isDiscardedTerminalControl(r):
				// Drop both control bytes and abandon the malformed command.
				s.state = terminalStateText
			default:
				// ESC followed by an ordinary non-ASCII character is not a
				// terminal command. Remove ESC but preserve the character.
				s.state = terminalStateText
				goto processRune
			}

		case terminalStateEscapeIntermediate:
			switch {
			case r == 0x1b:
				s.state = terminalStateEscape
			case r >= 0x20 && r <= 0x2f:
				// More intermediate bytes.
			case r >= 0x30 && r <= 0x7e:
				s.state = terminalStateText
			case isDiscardedTerminalControl(r):
				s.state = terminalStateText
			default:
				s.state = terminalStateText
				goto processRune
			}

		case terminalStateCSI:
			switch {
			case r == 0x1b:
				// Recover from a malformed/incomplete CSI at a new command.
				s.state = terminalStateEscape
			case r >= 0x40 && r <= 0x7e:
				// ANSI final byte.
				s.state = terminalStateText
			case (r >= 0x20 && r <= 0x3f) || isDiscardedTerminalControl(r):
				// Parameter/intermediate/control bytes remain private.
			default:
				// Preserve an unrelated printable Unicode rune after a
				// malformed sequence, but never expose the buffered prefix.
				s.state = terminalStateText
				goto processRune
			}

		case terminalStateString:
			switch r {
			case 0x07, 0x9c: // BEL or C1 ST
				s.state = terminalStateText
			case 0x1b:
				s.state = terminalStateStringEscape
			default:
				// OSC/DCS/SOS/PM/APC payload is never display text.
			}

		case terminalStateStringEscape:
			switch r {
			case '\\', 0x07, 0x9c: // ESC \\ or a defensive direct terminator
				s.state = terminalStateText
			case 0x1b:
				// Still waiting for ST after the newest ESC.
			default:
				s.state = terminalStateString
			}
		}
	}

	return out.String()
}

// Finish discards an incomplete terminal command. No ordinary display text is
// buffered, so there is intentionally nothing to flush to a callback.
func (s *TerminalControlStreamSanitizer) Finish() {
	s.state = terminalStateText
}

func isDiscardedTerminalControl(r rune) bool {
	return (r < 0x20 && r != '\n' && r != '\r' && r != '\t') ||
		r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

func StripTerminalControlSequences(text string) string {
	var sanitizer TerminalControlStreamSanitizer
	clean := sanitizer.SanitizeChunk(text)
	sanitizer.Finish()
	return clean
}

func stripStructuredTerminalTail(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return trimmed
	}

	if fenceEnd := closedFenceEnd(trimmed); fenceEnd > 0 && looksLikeTerminalResidue(trimmed[fenceEnd:]) {
		return strings.TrimSpace(trimmed[:fenceEnd])
	}

	if trimmed[0] == '{' || trimmed[0] == '[' {
		decoder := json.NewDecoder(strings.NewReader(trimmed))
		var value json.RawMessage
		if err := decoder.Decode(&value); err == nil {
			end := int(decoder.InputOffset())
			if end > 0 && end < len(trimmed) && looksLikeTerminalResidue(trimmed[end:]) {
				return strings.TrimSpace(trimmed[:end])
			}
		}
	}

	// Some providers occasionally append the printable remainder of a broken
	// terminal command without the ESC byte, for example "。[e~[". Limit this
	// recovery to a short suffix after completed prose so examples such as
	// "terminal example: [e~[" and fenced code remain untouched.
	windowStart := len(trimmed) - min(len(trimmed), 64)
	for searchFrom := windowStart; searchFrom < len(trimmed); {
		relative := strings.Index(trimmed[searchFrom:], "[")
		if relative < 0 {
			break
		}
		bracket := searchFrom + relative
		prefix := strings.TrimSpace(trimmed[:bracket])
		if bracket > 0 && endsWithCompleteSentence(prefix) && looksLikeTerminalResidue(trimmed[bracket:]) {
			return prefix
		}
		searchFrom = bracket + 1
	}
	return trimmed
}

func endsWithCompleteSentence(text string) bool {
	for _, suffix := range []string{"。", "！", "？", ".", "!", "?"} {
		if strings.HasSuffix(text, suffix) {
			return true
		}
	}
	return false
}

func closedFenceEnd(text string) int {
	if len(text) < 3 {
		return 0
	}
	fence := text[:3]
	if fence != "```" && fence != "~~~" {
		return 0
	}
	idx := strings.LastIndex(text, "\n"+fence)
	if idx <= 0 {
		return 0
	}
	return idx + 1 + len(fence)
}

func looksLikeTerminalResidue(tail string) bool {
	tail = strings.TrimSpace(tail)
	if tail == "" || len(tail) > 64 {
		return false
	}

	openBrackets, closeBrackets := 0, 0
	hasParameterSignal := false
	for _, r := range tail {
		switch {
		case r == 0x1b || (r >= 0x80 && r <= 0x9f):
			hasParameterSignal = true
		case r > 0x7e:
			return false
		case r == '[':
			openBrackets++
		case r == ']':
			closeBrackets++
		case (r >= '0' && r <= '9') || r == '~' || r == ';' || r == '?':
			hasParameterSignal = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			// ANSI final bytes are letters, but a letter alone is not enough
			// to classify a suffix as transport residue.
		case r == ':' || r == '<' || r == '>' || r == '=' || r == '!':
		default:
			return false
		}
	}
	return openBrackets > closeBrackets && hasParameterSignal
}
