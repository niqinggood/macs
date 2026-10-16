// Command browser_driver exercises the real embedded OpenKitty Web UI for W29.
// It prints one JSON evidence object to stdout and writes diagnostics to stderr.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type evidence struct {
	SessionID                     string   `json:"session_id"`
	SameBubbleAttachmentCount     int      `json:"same_bubble_attachment_count"`
	RequestAttachmentCount        int      `json:"request_attachment_count"`
	PersistedAttachmentCount      int      `json:"persisted_attachment_count,omitempty"`
	RefreshPreservesAttachments   bool     `json:"refresh_preserves_attachments"`
	MobileOverflowX               float64  `json:"mobile_overflow_x"`
	ConsoleErrorCount             int      `json:"console_error_count"`
	ConsoleErrors                 []string `json:"console_errors,omitempty"`
	RequestMessage                string   `json:"request_message,omitempty"`
	InternalAttachmentPromptFound bool     `json:"internal_attachment_prompt_found"`
}

func main() {
	baseURL := flag.String("base-url", "http://127.0.0.1:8080", "OpenKitty gateway base URL")
	csvPath := flag.String("csv", "", "CSV fixture path")
	pngPath := flag.String("png", "", "PNG fixture path")
	timeout := flag.Duration("timeout", 4*time.Minute, "browser scenario timeout")
	flag.Parse()

	if err := requireFile(*csvPath); err != nil {
		fatal(err)
	}
	if err := requireFile(*pngPath); err != nil {
		fatal(err)
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.NoSandbox,
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()
	ctx, cancel := context.WithTimeout(browserCtx, *timeout)
	defer cancel()

	var mu sync.Mutex
	consoleErrors := []string{}
	requestAttachmentCount := 0
	requestMessage := ""
	chromedp.ListenTarget(ctx, func(event any) {
		switch value := event.(type) {
		case *cdpruntime.EventExceptionThrown:
			text := "unhandled exception"
			if value.ExceptionDetails != nil {
				text = value.ExceptionDetails.Error()
			}
			mu.Lock()
			consoleErrors = append(consoleErrors, text)
			mu.Unlock()
		case *cdplog.EventEntryAdded:
			if value.Entry != nil && value.Entry.Level == cdplog.LevelError {
				mu.Lock()
				consoleErrors = append(consoleErrors, value.Entry.Text)
				mu.Unlock()
			}
		case *network.EventRequestWillBeSent:
			if value.Request == nil || value.Request.Method != "POST" {
				return
			}
			parsed, err := url.Parse(value.Request.URL)
			if err != nil || parsed.Path != "/api/chat" {
				return
			}
			go func(requestID network.RequestID) {
				var postData []byte
				err := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
					var getErr error
					postData, getErr = network.GetRequestPostData(requestID).Do(actionCtx)
					return getErr
				}))
				if err != nil {
					return
				}
				var payload struct {
					Message     string            `json:"message"`
					Attachments []json.RawMessage `json:"attachments"`
				}
				if json.Unmarshal(postData, &payload) != nil {
					return
				}
				mu.Lock()
				requestAttachmentCount = len(payload.Attachments)
				requestMessage = payload.Message
				mu.Unlock()
			}(value.RequestID)
		}
	})

	pageURL := strings.TrimRight(*baseURL, "/") + "/web"
	if err := chromedp.Run(ctx,
		network.Enable(),
		cdplog.Enable(),
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("#messageInput", chromedp.ByQuery),
		chromedp.SetUploadFiles("#fileInput", []string{absolute(*csvPath), absolute(*pngPath)}, chromedp.ByQuery),
		chromedp.Poll(
			`() => window.hasPendingMessageAttachments && window.hasPendingMessageAttachments() && document.querySelectorAll('.composer-attachment.ready,.composer-image.ready').length === 2`,
			nil,
			chromedp.WithPollingTimeout(90*time.Second),
		),
		chromedp.SendKeys("#messageInput", "请结合文档和图片做经营摘要", chromedp.ByQuery),
		chromedp.Click("#sendBtn", chromedp.ByQuery),
		chromedp.Poll(
			`() => { const items = document.querySelectorAll('#messagesArea .message.user'); const last = items[items.length - 1]; return !!last && last.querySelectorAll('.message-attachment').length === 2; }`,
			nil,
			chromedp.WithPollingTimeout(90*time.Second),
		),
	); err != nil {
		fatal(fmt.Errorf("send attachment message: %w", err))
	}

	var sessionID string
	var bubbleCount int
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`String(window.currentSessionId || currentSessionId || '')`, &sessionID),
		chromedp.Evaluate(`(() => { const items = document.querySelectorAll('#messagesArea .message.user'); const last = items[items.length - 1]; return last ? last.querySelectorAll('.message-attachment').length : 0; })()`, &bubbleCount),
	); err != nil {
		fatal(fmt.Errorf("inspect sent bubble: %w", err))
	}
	if strings.TrimSpace(sessionID) == "" {
		fatal(fmt.Errorf("web UI did not expose a session id"))
	}

	// Wait until the request observer has decoded the outgoing JSON. The task
	// itself may still be running; W29 verifies the atomic user message contract.
	requestDeadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(requestDeadline) {
		mu.Lock()
		count := requestAttachmentCount
		mu.Unlock()
		if count > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := chromedp.Run(ctx,
		chromedp.Reload(),
		chromedp.WaitReady("#messageInput", chromedp.ByQuery),
		chromedp.Poll(
			fmt.Sprintf(`() => String(window.currentSessionId || currentSessionId || '') === %q && document.querySelectorAll('#messagesArea .message.user .message-attachment').length >= 2`, sessionID),
			nil,
			chromedp.WithPollingTimeout(90*time.Second),
		),
	); err != nil {
		fatal(fmt.Errorf("refresh and restore attachments: %w", err))
	}

	var refreshedCount int
	var overflow float64
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`document.querySelectorAll('#messagesArea .message.user .message-attachment').length`, &refreshedCount),
		chromedp.Evaluate(`(() => { const m=document.querySelector('#messagesArea .message.user .message-attachment'); if(m){m.scrollIntoView({block:'center'});} return true; })()`, nil),
		chromedp.ActionFunc(func(actionCtx context.Context) error {
			return chromedp.EmulateViewport(390, 844).Do(actionCtx)
		}),
		chromedp.Evaluate(`Math.max(0, document.documentElement.scrollWidth - window.innerWidth)`, &overflow),
	); err != nil {
		fatal(fmt.Errorf("mobile attachment inspection: %w", err))
	}

	mu.Lock()
	errorsSnapshot := append([]string(nil), consoleErrors...)
	requestCountSnapshot := requestAttachmentCount
	requestMessageSnapshot := requestMessage
	mu.Unlock()
	result := evidence{
		SessionID:                     sessionID,
		SameBubbleAttachmentCount:     bubbleCount,
		RequestAttachmentCount:        requestCountSnapshot,
		RefreshPreservesAttachments:   refreshedCount >= 2,
		MobileOverflowX:               overflow,
		ConsoleErrorCount:             len(errorsSnapshot),
		ConsoleErrors:                 errorsSnapshot,
		RequestMessage:                requestMessageSnapshot,
		InternalAttachmentPromptFound: strings.Contains(requestMessageSnapshot, "OpenKitty 用户附件清单") || strings.Contains(requestMessageSnapshot, "用户上传了文件"),
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		fatal(err)
	}
}

func requireFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("fixture path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("fixture is a directory: %s", path)
	}
	return nil
}

func absolute(path string) string {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return resolved
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
