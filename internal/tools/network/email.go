package network

import (
	"context"
	"fmt"
	"log"
	"os/exec"
)

// ====================== Email Triage Tool ======================

type EmailTriageTool struct {
	config EmailTriageConfig
	logger *log.Logger
}

type EmailTriageConfig struct {
	Enabled  bool
	IMAPHost string
	IMAPPort int
	Username string
	Password string
}

func NewEmailTriageTool(cfg EmailTriageConfig, logger *log.Logger) *EmailTriageTool {
	return &EmailTriageTool{config: cfg, logger: logger}
}

func (t *EmailTriageTool) Name() string { return "email_check" }
func (t *EmailTriageTool) Description() string {
	return `Check emails from the configured mailbox. Lists recent emails with sender/subject/date.
Use this to triage inbox, identify important emails, and draft replies.
Requires email config in config.yaml (email_config section).`
}
func (t *EmailTriageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"limit": map[string]interface{}{"type": "integer", "description": "Max emails (default 10)"},
		},
	}
}
func (t *EmailTriageTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if !t.config.Enabled {
		return "", fmt.Errorf("email not enabled in config")
	}
	limit := "10"
	if l, ok := params["limit"].(float64); ok {
		limit = fmt.Sprintf("%d", int(l))
	}

	// Use curl + Python for IMAP (no extra Go deps)
	script := fmt.Sprintf(`
import imaplib, email, sys
try:
    m = imaplib.IMAP4_SSL('%s', %d)
    m.login('%s', '%s')
    m.select('INBOX')
    _, data = m.search(None, 'ALL')
    ids = data[0].split()[-%s:]
    for mid in reversed(ids):
        _, msg_data = m.fetch(mid, '(BODY[HEADER.FIELDS (FROM SUBJECT DATE)])')
        print(msg_data[0][1].decode('utf-8', errors='ignore')[:300])
        print('---END---')
    m.logout()
except Exception as e:
    print('ERROR:', str(e))
`, t.config.IMAPHost, t.config.IMAPPort, t.config.Username, t.config.Password, limit)

	out, err := exec.CommandContext(ctx, "python3", "-c", script).Output()
	if err != nil {
		return "", fmt.Errorf("email check failed: %w (is python3 with imaplib available?)", err)
	}

	result := "# 收件箱\n\n" + string(out)
	if t.logger != nil {
		t.logger.Printf("[Email] Checked inbox")
	}
	return result, nil
}
