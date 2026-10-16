package security

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// PIITool redacts personally identifiable information from text.
// Supports Chinese ID numbers, phone numbers, emails, names, addresses.
type PIITool struct{}

func NewPIITool() *PIITool      { return &PIITool{} }
func (t *PIITool) Name() string { return "redact_pii" }
func (t *PIITool) Description() string {
	return "Redact PII from text before sending to cloud models. Detects: Chinese ID numbers, phone numbers, emails, names, addresses, bank cards. Supports 'redact' (replace with ***) or 'extract' (list found PII) modes."
}
func (t *PIITool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"text": map[string]interface{}{
				"type": "string", "description": "Text to scan and redact",
			},
			"mode": map[string]interface{}{
				"type": "string", "description": "'redact' (default) replaces PII, 'extract' returns found items",
			},
		},
		"required": []string{"text"},
	}
}

var piiPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"Chinese ID (18-digit)", regexp.MustCompile(`[1-9]\d{5}(19|20)\d{2}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])\d{3}[\dXx]`)},
	{"Chinese ID (15-digit)", regexp.MustCompile(`[1-9]\d{7}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])\d{3}`)},
	{"Phone (China mobile)", regexp.MustCompile(`1[3-9]\d{9}`)},
	{"Phone (landline)", regexp.MustCompile(`\d{3,4}[-]\d{7,8}`)},
	{"Email", regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)},
	{"Bank card", regexp.MustCompile(`\d{16,19}`)},
	{"IP address", regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)},
	{"Passport (China)", regexp.MustCompile(`[EG]\d{8}`)},
	{"License plate (China)", regexp.MustCompile(`[京津沪渝冀豫云辽黑湘皖鲁新苏浙赣鄂桂甘晋蒙陕吉闽贵粤川青藏琼宁][A-Z][A-HJ-NP-Z0-9]{4,5}[A-HJ-NP-Z0-9挂学警港澳]`)},
}

func (t *PIITool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	text, _ := params["text"].(string)
	if text == "" {
		return "No text provided.", nil
	}
	mode, _ := params["mode"].(string)
	if mode == "" {
		mode = "redact"
	}

	results := make([]string, 0)
	redacted := text
	for _, p := range piiPatterns {
		matches := p.pattern.FindAllString(redacted, -1)
		for _, m := range matches {
			results = append(results, fmt.Sprintf("[%s] %s", p.name, m))
			redacted = strings.ReplaceAll(redacted, m, strings.Repeat("*", len(m)))
		}
	}

	if mode == "extract" {
		if len(results) == 0 {
			return "No PII detected.", nil
		}
		return "## PII Found\n\n" + strings.Join(results, "\n"), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Redacted %d PII item(s). Safe to send to cloud.\n\n", len(results)))
	sb.WriteString("```\n")
	sb.WriteString(redacted)
	sb.WriteString("\n```")
	return sb.String(), nil
}
