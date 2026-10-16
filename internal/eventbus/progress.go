package eventbus

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mac/internal/textutil"
)

const ProgressToolCommandDetailLimit = 400

func truncateStr(value string, maximum int) string {
	return textutil.TruncateRunes(value, maximum)
}

// translateProgressMessage converts raw engineer messages to user-friendly display format
func TranslateProgressMessage(thought string, thoughtType string, clean func(string) string, failureSummaryLimit int) string {
	if clean == nil {
		clean = func(value string) string { return value }
	}
	if failureSummaryLimit <= 0 {
		failureSummaryLimit = 800
	}
	ts := time.Now().Format("15:04:05")

	if thoughtType == "use_tool" {
		var t string
		var argsStr string
		if idx := strings.Index(thought, "will use "); idx >= 0 {
			rest := thought[idx+9:]
			if colon := strings.Index(rest, ":"); colon >= 0 {
				t = strings.TrimSpace(rest[:colon])
				argsStr = strings.TrimSpace(rest[colon+1:])
			}
		}

		var detail string
		if argsStr != "" {
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(argsStr), &args); err == nil {
				switch t {
				case "web_fetch":
					if u, ok := args["url"].(string); ok && u != "" {
						detail = truncateStr(u, 60)
					} else if q, ok := args["query"].(string); ok && q != "" {
						detail = "q=" + truncateStr(q, 50)
					} else if q, ok := args["q"].(string); ok && q != "" {
						detail = "q=" + truncateStr(q, 50)
					} else if len(args) > 0 {
						detail = truncateStr(argsStr, 60)
					}
				case "web_search":
					if q, ok := args["query"].(string); ok && q != "" {
						detail = "q=" + truncateStr(q, 50)
					} else if q, ok := args["q"].(string); ok && q != "" {
						detail = "q=" + truncateStr(q, 50)
					}
				case "read_file", "write_file", "edit_file", "json_patch", "code_check":
					if p, ok := args["path"].(string); ok {
						detail = p
					} else if p, ok := args["file"].(string); ok {
						detail = p
					}
				case "cli", "bash":
					if c, ok := args["command"].(string); ok {
						detail = truncateStr(c, ProgressToolCommandDetailLimit)
					}
				case "grep":
					if p, ok := args["pattern"].(string); ok {
						detail = truncateStr(p, 40)
					}
				case "glob":
					if p, ok := args["pattern"].(string); ok {
						detail = truncateStr(p, 40)
					}
				case "search":
					if q, ok := args["query"].(string); ok {
						detail = truncateStr(q, 40)
					}
				case "use_skill":
					if s, ok := args["skill_name"].(string); ok {
						detail = s
					}
				case "plan_task":
					if steps, ok := args["steps"].([]interface{}); ok && len(steps) > 0 {
						var titles []string
						for i, s := range steps {
							if m, ok := s.(map[string]interface{}); ok {
								if t, ok := m["title"].(string); ok {
									titles = append(titles, fmt.Sprintf("%d.%s", i+1, truncateStr(t, 20)))
								}
							}
							if len(titles) >= 3 {
								break
							}
						}
						detail = strings.Join(titles, " | ")
					}
				case "update_plan_step":
					if sid, ok := args["step_id"].(string); ok {
						if title, ok := args["title"].(string); ok && title != "" && title != sid {
							detail = sid + ": " + title
						} else {
							detail = sid
						}
						if st, ok := args["status"].(string); ok {
							detail += " (" + st + ")"
						}
					}
				}
			}
		}

		emojiMap := map[string]string{
			"web_fetch": "\U0001F310", "read_file": "\U0001F4D6", "write_file": "✏️",
			"edit_file": "✂️", "cli": "⚡", "bash": "⚡",
			"grep": "\U0001F50D", "glob": "\U0001F4C1", "list_dir": "\U0001F4C2",
			"code_check": "\U0001F527", "search": "\U0001F50D", "plan_task": "\U0001F4CB",
			"web_search": "0001F310", "SPEAK": "\U0001F50A", "use_skill": "\U0001F9E0", "vision": "\U0001F441",
			"snapshot": "\U0001F4F8", "git": "\U0001F500",
		}
		emoji := emojiMap[t]
		if emoji == "" {
			emoji = "\U0001F539"
		}

		if detail != "" {
			return ts + " " + emoji + " " + t + " " + detail
		} else if t != "" {
			return ts + " " + emoji + " " + t
		}
		return ts + " " + emoji + " working"
	}

	if strings.Contains(thought, "Round") && strings.Contains(thought, "Action") {
		return ts + " ⚡ " + thought
	}
	if thoughtType == "rec" {
		if strings.HasPrefix(thought, "Task start (prompt:") {
			return ts + " Task start"
		}
		if strings.Contains(thought, "run tool[") && strings.Contains(thought, "] succ") {
			return ts + " ✅ Tool complete"
		}
		if strings.Contains(thought, "LLM call fail") {
			return ts + " ❌ LLM error, retrying..."
		}
		if strings.Contains(thought, "故障诊断") {
			return ts + " \U0001F527 Troubleshooting..."
		}
	}

	if thoughtType == "completion" {
		return ts + " \U0001F3AF Task complete"
	}
	if thoughtType == "completion_failed" {
		return ts + " ❌ Task failed"
	}
	if thoughtType == "tool_error" {
		cleaned := strings.TrimSpace(clean(thought))
		if cleaned == "" {
			return ts + " Tool failed"
		}
		return ts + " " + truncateStr(cleaned, failureSummaryLimit)
	}
	if thoughtType == "tool_recovery" {
		return ts + " 🔄 " + truncateStr(strings.TrimSpace(thought), 220)
	}

	if thoughtType == "permission_request" {
		return ts + " ⏳ " + truncateStr(thought, 180)
	}

	if thoughtType == "system" || thoughtType == "compress" {
		return ts + " \U0001F4E6 Compacting context..."
	}

	// Default: keep progress user-facing; raw model reasoning stays in engine debug logs.
	cleaned := strings.TrimSpace(clean(thought))
	if cleaned == "" {
		return ts + " Working"
	}
	lower := strings.ToLower(cleaned)
	if strings.HasPrefix(lower, "llm:") || strings.Contains(lower, "cold data:") || strings.Contains(lower, "prompt:") {
		return ts + " Working"
	}
	runes := []rune(cleaned)
	if len(runes) > 80 {
		return ts + " " + string(runes[:80]) + "..."
	}
	return ts + " " + cleaned
}
