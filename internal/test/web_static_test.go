package app

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenKittyLogoUsesCanonicalKitty3Asset(t *testing.T) {
	canonical, err := os.ReadFile(filepath.Join("winres", "kitty3.png"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join("web", "openkitty-avatar.png"), filepath.Join("winres", "kitty.png")} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, canonical) {
			t.Fatalf("%s does not use the canonical winres/kitty3.png bytes", path)
		}
	}
	sum := sha256.Sum256(canonical)
	version := fmt.Sprintf("%x", sum[:4])
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	if count := strings.Count(indexHTML, "openkitty-avatar.png?v="+version); count != 3 {
		t.Fatalf("logo cache key count=%d, want 3 references to content hash %s", count, version)
	}
}

func TestWebSessionHeaderKeepsControlsWhenTitleChanges(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	chatJS := mustReadWebTestFile(t, "web/chat.js")

	if !strings.Contains(indexHTML, `id="sessionTitleText"`) {
		t.Fatalf("web/index.html must keep a dedicated #sessionTitleText span so title updates do not remove header controls")
	}
	if !strings.Contains(chatJS, "function setSessionHeaderTitle") {
		t.Fatalf("web/chat.js must update session title through setSessionHeaderTitle")
	}
	if strings.Contains(chatJS, "sessionTitle.textContent =") {
		t.Fatalf("web/chat.js must not assign sessionTitle.textContent directly; that removes nested header buttons/badges")
	}
}

func TestWebMessageKeepsTextAndAttachmentsInOneBubble(t *testing.T) {
	appJS := mustReadWebTestFile(t, "web/app.js")
	chatJS := mustReadWebTestFile(t, "web/chat.js")
	styleCSS := mustReadWebTestFile(t, "web/style.css")
	indexHTML := mustReadWebTestFile(t, "web/index.html")

	for _, marker := range []string{
		"let pendingMessageAttachments = []",
		"window.getPendingMessageAttachments = getPendingMessageAttachments",
		"window.clearPendingMessageAttachments = clearPendingMessageAttachments",
		"queueMessageAttachment(file, previewURL, revokePreviewURL)",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("unified composer attachment flow missing %q", marker)
		}
	}
	for _, marker := range []string{
		"function normalizeMessageAttachments",
		"function renderMessageAttachments",
		"attachments: messageAttachments",
		`'<div class="message-content">' + renderedContent + renderedAttachments + '</div>'`,
		"function buildFileInlineUrl",
	} {
		if !strings.Contains(chatJS, marker) {
			t.Fatalf("message attachment rendering missing %q", marker)
		}
	}
	if strings.Contains(chatJS, "uploadedFilePath") || strings.Contains(chatJS, "用户上传了文件: ' + uploadedFileName") {
		t.Fatal("legacy prompt-string upload path must not remain in the Web send flow")
	}
	for _, selector := range []string{".message-attachments", ".message-attachment-file", ".message-attachment-image", ".composer-attachment"} {
		if !strings.Contains(styleCSS, selector) {
			t.Fatalf("attachment styling missing %s", selector)
		}
	}
	if !strings.Contains(indexHTML, "20260720-message-attachments-v1") {
		t.Fatal("changed attachment assets must be cache-busted")
	}
}

func TestWebComposerActionsStayCompactAndUseVectorSendIcon(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	styleCSS := mustReadWebTestFile(t, "web/style.css")

	for _, marker := range []string{
		`class="composer-actions"`,
		`class="send-arrow" viewBox="0 0 24 24"`,
		`aria-label="语音输入"`,
		`aria-label="上传文件"`,
		`aria-label="上传图片"`,
		`aria-label="发送消息"`,
		"composer-actions-v1",
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("compact composer action markup missing %q", marker)
		}
	}
	if strings.Contains(indexHTML, `<span class="send-arrow">➤</span>`) {
		t.Fatal("composer send control must use the vector arrow instead of the legacy text glyph")
	}
	toolRule := webCSSRule(t, styleCSS, ".input-icon-btn")
	for _, marker := range []string{"width: 26px;", "height: 26px;"} {
		if !strings.Contains(toolRule, marker) {
			t.Fatalf("compact composer tool styling missing %q in %s", marker, toolRule)
		}
	}
	sendRule := webCSSRule(t, styleCSS, ".send-btn")
	for _, marker := range []string{"width: 28px;", "height: 28px;"} {
		if !strings.Contains(sendRule, marker) {
			t.Fatalf("compact composer send styling missing %q in %s", marker, sendRule)
		}
	}
	if !strings.Contains(styleCSS, ".dark .input-icon-btn") {
		t.Fatal("compact composer must retain dark-mode tool styling")
	}
}

func TestChatImageExportUsesNeutralProductWatermark(t *testing.T) {
	chatJS := mustReadWebTestFile(t, "web/chat.js")
	indexHTML := mustReadWebTestFile(t, "web/index.html")

	for _, marker := range []string{
		`.watermark{`,
		`font-size:8px`,
		`MACS | 内容由 AI 生成，仅供参考，不构成任何建议`,
	} {
		if !strings.Contains(chatJS, marker) {
			t.Fatalf("chat image export neutral watermark missing %q", marker)
		}
	}
	if !strings.Contains(indexHTML, "macs-brand-v1") {
		t.Fatal("changed chat export asset must be cache-busted")
	}
}

func TestWebHelpOmitsCommercialBrandingAndRestrictiveTerms(t *testing.T) {
	files := map[string]string{
		"web/index.html":  mustReadWebTestFile(t, "web/index.html"),
		"web/app.js":      mustReadWebTestFile(t, "web/app.js"),
		"web/chat.js":     mustReadWebTestFile(t, "web/chat.js"),
		"web/pipeline.js": mustReadWebTestFile(t, "web/pipeline.js"),
	}
	for path, content := range files {
		for _, banned := range []string{"Silicon Surge", "silicon surge", "硅基涌动"} {
			if strings.Contains(content, banned) {
				t.Fatalf("%s still contains company branding %q", path, banned)
			}
		}
	}

	indexHTML := files["web/index.html"]
	for _, banned := range []string{
		"商业使用必须取得授权",
		"注册资本300万元",
		"企业商业使用需获取授权",
		"有限技术支持，高可用场景请用商业版",
	} {
		if strings.Contains(indexHTML, banned) {
			t.Fatalf("help content still contains restrictive commercial wording %q", banned)
		}
	}
	for _, marker := range []string{"反馈与支持", "使用说明", "随附的 LICENSE 文件及发布说明"} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("help content is missing neutral guidance %q", marker)
		}
	}
	for _, marker := range []string{
		"MACS（麦卡斯）",
		"Multi-Agent Collaboration System",
		"中文名：麦卡斯",
		"英文全称：Multi-Agent Collaboration System",
		"内部代号（小名）：OpenKitty",
		"主要功能",
		"独立开发者",
		"倪平强",
		"Patrick · niqinggood",
		"niqinggood@163.com",
		"https://github.com/niqinggood",
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("help content is missing MACS or independent developer marker %q", marker)
		}
	}
	for _, removed := range []string{"产品对比", "Token节省80%+", "40+ 内置工具", "80+ 技能包"} {
		if strings.Contains(indexHTML, removed) {
			t.Fatalf("help content should focus on MACS and still contains %q", removed)
		}
	}
}

func TestWebHTMLArtifactUsesDedicatedClickableSourceAndPreviewLinks(t *testing.T) {
	chatJS := mustReadWebTestFile(t, "web/chat.js")
	for _, marker := range []string{
		"var sourceUrl = buildFileDownloadUrl(smatch)",
		"var previewUrl = buildFileViewUrl(smatch)",
		"下载 HTML 源文件",
		"预览 HTML 页面",
	} {
		if !strings.Contains(chatJS, marker) {
			t.Fatalf("HTML artifact link handling missing %q", marker)
		}
	}
	if strings.Contains(chatJS, "s + '&preview=1'") {
		t.Fatal("HTML preview must not point at the attachment-only download endpoint")
	}
}

func TestWebTaskPanelDefaultsToAllHistory(t *testing.T) {
	appJS := mustReadWebTestFile(t, "web/app.js")

	if !strings.Contains(appJS, "let currentTaskTab = 'all'") {
		t.Fatalf("task panel should default to all history, not only running tasks")
	}
	if !strings.Contains(appJS, "/api/tasks?limit=50") {
		t.Fatalf("all task tab should load persisted task history from /api/tasks, not only the running snapshot")
	}
	if !strings.Contains(appJS, "暂无任务记录。运行中为空时可切到") {
		t.Fatalf("empty task panel text should explain that task history is available under the all tab")
	}
}

func TestWebArtifactCenterLivesInWorkspaceMenu(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	artifactsJS := mustReadWebTestFile(t, "web/artifacts.js")

	if !strings.Contains(indexHTML, `id="workbenchMenuButton"`) ||
		!strings.Contains(indexHTML, `data-i18n="workbenchButton">工作台</span>`) {
		t.Fatalf("top-level navigation should call the combined menu 工作台")
	}
	if strings.Contains(indexHTML, "⚙ 工具 ▾") {
		t.Fatalf("legacy 工具 top-level label should not remain")
	}
	if !strings.Contains(artifactsJS, "getElementById('toolMenu')") ||
		!strings.Contains(artifactsJS, "artifactCenterMenuItem") {
		t.Fatalf("artifact center should be injected into the workspace menu")
	}
	if !strings.Contains(artifactsJS, "window.closeDropdownMenus()") ||
		!strings.Contains(artifactsJS, "window.openArtifactCenter()") {
		t.Fatalf("artifact menu item should close and synchronize the workspace menu before opening the center")
	}
	if strings.Contains(artifactsJS, "getElementById('headerButtons')") ||
		strings.Contains(artifactsJS, "document.querySelector('.header-buttons')") ||
		strings.Contains(artifactsJS, "artifactCenterBtn") {
		t.Fatalf("artifact center should no longer inject a top-level header button")
	}
}

func TestWebSessionListShowsFirstQuestionBetweenIDAndDate(t *testing.T) {
	appJS := mustReadWebTestFile(t, "web/app.js")
	styleCSS := mustReadWebTestFile(t, "web/style.css")

	for _, marker := range []string{
		"function compactSessionQuestion",
		"s.first_question",
		"class=\"session-question\"",
		"class=\\\"session-question\\\"",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("web/app.js must render the first user question; missing %q", marker)
		}
	}
	if !strings.Contains(styleCSS, ".session-question") {
		t.Fatalf("web/style.css must style the inserted session question")
	}
	nameStart := strings.Index(styleCSS, ".session-name {")
	if nameStart < 0 {
		t.Fatalf("web/style.css must keep the session-name rule")
	}
	nameEnd := strings.Index(styleCSS[nameStart:], "}")
	if nameEnd < 0 || !strings.Contains(styleCSS[nameStart:nameStart+nameEnd], "font-size: 10px") {
		t.Fatalf("session identifier should use the smaller visual treatment")
	}

	nameMarker := "'<div class=\"session-name\">' + sourceIcon"
	questionMarker := "'<div class=\"session-question\">' + escapeHtml(firstQuestion)"
	timeMarker := "'<div class=\"session-time\">' + messageCount"
	nameIndex := strings.Index(appJS, nameMarker)
	questionIndex := strings.Index(appJS, questionMarker)
	timeIndex := strings.Index(appJS, timeMarker)
	if nameIndex < 0 || questionIndex <= nameIndex || timeIndex <= questionIndex {
		t.Fatalf("session rows must render in name, first question, date order")
	}
}

func TestWebHeaderControlsUseCompactSpacing(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	styleCSS := mustReadWebTestFile(t, "web/style.css")

	if !strings.Contains(indexHTML, `class="agent-control-group"`) {
		t.Fatalf("mode buttons and the default agent selector should share a compact control group")
	}
	if strings.Contains(indexHTML, `id="modeSwitch" style=`) {
		t.Fatalf("inline mode-switch spacing must not override the compact stylesheet")
	}
	for selector, markers := range map[string][]string{
		".agent-control-group": {"gap: 3px"},
		".mode-switch":         {"gap: 2px", "padding: 2px"},
		".mode-btn":            {"padding: 3px 7px"},
		".chat-header":         {"padding: 8px 10px 8px 20px"},
	} {
		rule := webCSSRule(t, styleCSS, selector)
		for _, marker := range markers {
			if !strings.Contains(rule, marker) {
				t.Fatalf("%s should contain compact spacing %q; rule=%q", selector, marker, rule)
			}
		}
	}
}

func TestWebSettingsButtonUsesSoftRedCallout(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	styleCSS := mustReadWebTestFile(t, "web/style.css")

	for _, marker := range []string{
		`class="hb-btn hb-btn-settings"`,
		`aria-haspopup="dialog"`,
		`aria-controls="configModal"`,
		"settings-callout-v2",
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("settings entry is missing first-run marker %q", marker)
		}
	}
	rule := webCSSRule(t, styleCSS, ".hb-btn-settings")
	for _, marker := range []string{"background: #fef2f2", "color: #b91c1c", "border-color: #fecaca", "font-weight: 500", "box-shadow: none"} {
		if !strings.Contains(rule, marker) {
			t.Fatalf("soft-red settings callout must contain %q; rule=%q", marker, rule)
		}
	}
	for _, forbidden := range []string{"background: #dc2626", "color: #fff", "font-weight: 700"} {
		if strings.Contains(rule, forbidden) {
			t.Fatalf("settings callout must not keep the old heavy treatment %q; rule=%q", forbidden, rule)
		}
	}
	hoverRule := webCSSRule(t, styleCSS, ".hb-btn-settings:hover")
	if !strings.Contains(hoverRule, "background: #fee2e2") || !strings.Contains(hoverRule, "color: #991b1b") {
		t.Fatalf("settings hover state must remain a readable soft-red callout; rule=%q", hoverRule)
	}
}

func TestWebPrefersAssistantInAgentAndModelSelectors(t *testing.T) {
	appJS := mustReadWebTestFile(t, "web/app.js")
	chatJS := mustReadWebTestFile(t, "web/chat.js")
	indexHTML := mustReadWebTestFile(t, "web/index.html")

	for _, marker := range []string{
		"function agentPreferenceRank",
		"function sortAgentsAssistantFirst",
		"function preferredAssistantAgent",
		"function sortModelNamesAssistantFirst",
		"default_agent: currentConfig?.default_agent",
		"prioritizedAgentEntries(agentsList, preferredName)",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("Assistant-first Web preference missing %q", marker)
		}
	}
	for _, marker := range []string{
		"window._defaultAgentName = data.default_agent || ''",
		"window.sortAgentsAssistantFirst(data.agents || [], window._defaultAgentName)",
		"preferredAgentName(agents)",
	} {
		if !strings.Contains(chatJS, marker) {
			t.Fatalf("chat Agent selector preference missing %q", marker)
		}
	}
	if !strings.Contains(indexHTML, "assistant-default-v1") {
		t.Fatal("changed Assistant preference assets must be cache-busted")
	}
}

func TestWebTreatsHeartbeatsAsTransientStatus(t *testing.T) {
	sseJS := mustReadWebTestFile(t, "web/sse.js")
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	for _, marker := range []string{
		`if (data.type === "heartbeat")`,
		"heartbeatText.textContent",
		"return;",
	} {
		if !strings.Contains(sseJS, marker) {
			t.Fatalf("heartbeat timeline suppression missing %q", marker)
		}
	}
	if !strings.Contains(indexHTML, "sse.js?v=20260728-progress-v1") {
		t.Fatal("updated SSE behavior must be cache-busted")
	}
}

func TestWebStyleSheetBlocksAreBalanced(t *testing.T) {
	css := mustReadWebTestFile(t, "web/style.css")
	depth := 0
	line := 1
	quote := byte(0)
	inComment := false
	escaped := false
	for i := 0; i < len(css); i++ {
		ch := css[i]
		if ch == '\n' {
			line++
		}
		if inComment {
			if ch == '*' && i+1 < len(css) && css[i+1] == '/' {
				inComment = false
				i++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '/' && i+1 < len(css) && css[i+1] == '*' {
			inComment = true
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				t.Fatalf("web/style.css has an unmatched closing brace near line %d", line)
			}
		}
	}
	if depth != 0 || quote != 0 || inComment {
		t.Fatalf("web/style.css is structurally incomplete: brace_depth=%d quote=%q in_comment=%v", depth, quote, inComment)
	}
	if strings.Contains(css, "anslateY(-4px)") {
		t.Fatal("web/style.css still contains the truncated animation fragment")
	}
}

func TestWebDebugControlsLiveInToolsSettings(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	appJS := mustReadWebTestFile(t, "web/app.js")

	for _, marker := range []string{
		`switchConfigTab('tools')`,
		`data-i18n="toolsConfigTab">工具配置`,
		`id="toolsConfig"`,
		`data-tool-name="debug"`,
		`id="debugToolEnabled"`,
		`id="debugModeToggle"`,
		"工具启停会写入 openkitty-config.yaml",
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("tools settings UI missing %q", marker)
		}
	}
	if strings.Count(indexHTML, `id="debugModeToggle"`) != 1 {
		t.Fatalf("debug display toggle must have exactly one DOM owner")
	}
	if strings.Index(indexHTML, `id="debugModeToggle"`) < strings.Index(indexHTML, `id="configModal"`) {
		t.Fatalf("debug display toggle must be inside system settings, not the header")
	}

	for _, marker := range []string{
		"else if (tab === 'tools') sectionId = 'toolsConfig'",
		"debugToolEnabledEl.checked = !disabledTools.some",
		"function collectDisabledToolsFromSettings()",
		"disabled_tools: collectDisabledToolsFromSettings()",
		"disabled.push('debug')",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("tools settings JavaScript missing %q", marker)
		}
	}
}

func TestWebProgressTimelineUsesLongerUnicodeSafePreviews(t *testing.T) {
	sseJS := mustReadWebTestFile(t, "web/sse.js")

	for _, marker := range []string{
		"var progressTimelineTextLimit = 800",
		"var progressStatusTextLimit = 240",
		"function truncateProgressText",
		"Array.from(text)",
		"truncateProgressText(message, progressTimelineTextLimit)",
		"truncateProgressText(currentText, progressStatusTextLimit)",
		"truncateProgressText(msg, progressStatusTextLimit)",
	} {
		if !strings.Contains(sseJS, marker) {
			t.Fatalf("web progress rendering is missing %q", marker)
		}
	}
	for _, legacyCap := range []string{
		"msg.length > 200",
		"msg.substring(0, 200)",
		"msg.length > 50",
		"msg.substring(0, 50)",
		"last.length > 50",
		"lastMsg.length > 50",
	} {
		if strings.Contains(sseJS, legacyCap) {
			t.Fatalf("legacy short progress cap remains in web/sse.js: %q", legacyCap)
		}
	}
}

func TestWebProgressUsesStructuredAgentLogCards(t *testing.T) {
	sseJS := mustReadWebTestFile(t, "web/sse.js")
	chatJS := mustReadWebTestFile(t, "web/chat.js")
	styleCSS := mustReadWebTestFile(t, "web/style.css")
	indexHTML := mustReadWebTestFile(t, "web/index.html")

	for _, marker := range []string{
		"function renderProgressEventBubble",
		"function findProgressBubbleByToolCallID",
		"document.createElement('details')",
		"data-tool-call-id",
		"window.renderProgressEventBubble",
		"progressAggregateText",
	} {
		if !strings.Contains(sseJS, marker) {
			t.Fatalf("structured progress renderer missing %q", marker)
		}
	}
	for _, marker := range []string{
		`message-bubble agent-run-card`,
		`class="log-summary"`,
		`role="log"`,
	} {
		if !strings.Contains(chatJS, marker) {
			t.Fatalf("structured progress shell missing %q", marker)
		}
	}
	for _, marker := range []string{
		".progress-bubble-summary",
		".progress-bubble-detail",
		".agent-run-card:not(.is-log-expanded)",
	} {
		if !strings.Contains(styleCSS, marker) {
			t.Fatalf("structured progress styling missing %q", marker)
		}
	}
	if strings.Contains(sseJS, "正在执行所需步骤") {
		t.Fatal("fixed generic execution copy must not replace real progress messages")
	}
	if !strings.Contains(indexHTML, "agent-log-cards-v1") {
		t.Fatal("structured progress assets must be cache-busted")
	}
}

func TestWebExposesSafeInterruptedTaskRecovery(t *testing.T) {
	chatJS := mustReadWebTestFile(t, "web/chat.js")
	styleCSS := mustReadWebTestFile(t, "web/style.css")

	for _, marker := range []string{
		"data.recoveries || []",
		"function renderSessionRecoveryPanel",
		"/api/task/resume",
		"data-recovery-task",
		"恢复任务已启动",
		"typeof restorePendingTasks === 'function'",
	} {
		if !strings.Contains(chatJS, marker) {
			t.Fatalf("interrupted-task recovery UI missing %q", marker)
		}
	}
	for _, marker := range []string{
		".session-recovery-panel",
		".session-recovery-resume",
		".dark .session-recovery-panel",
	} {
		if !strings.Contains(styleCSS, marker) {
			t.Fatalf("interrupted-task recovery styling missing %q", marker)
		}
	}
}

func TestWebLanguageSwitchRerendersDynamicSettings(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	i18nJS := mustReadWebTestFile(t, "web/i18n.js")
	appJS := mustReadWebTestFile(t, "web/app.js")

	for _, marker := range []string{
		`id="languageToggle"`,
		`data-i18n-html="settingsBindingNotice"`,
		`data-i18n="modbusToolTitle"`,
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("language-aware settings markup missing %q", marker)
		}
	}
	for _, marker := range []string{
		"document.querySelectorAll('[data-i18n-html]')",
		"window.openKittyApplyLanguage = applyLanguage",
		"recoveryResume: 'Resume safely'",
		"settingsGuideModel:",
	} {
		if !strings.Contains(i18nJS, marker) {
			t.Fatalf("dynamic language runtime missing %q", marker)
		}
	}
	for _, marker := range []string{
		"function settingsText",
		"settingsRefreshConfigHealth();",
		"renderAgentsList()",
		"renderModelsList()",
		"renderChannelsList()",
		"settingsText('settingsEdit'",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("dynamic settings translation missing %q", marker)
		}
	}
}

func TestWebCompactAgentEditorPreservesAdvancedFields(t *testing.T) {
	appJS := mustReadWebTestFile(t, "web/app.js")
	if !strings.Contains(appJS, "var existing = isNew ? {} : currentConfig.agents[index];") ||
		!strings.Contains(appJS, "Object.assign({}, existing, {") {
		t.Fatal("compact Agent editor must preserve fields it does not expose")
	}
}

func TestGenericCodexApprovalEntrypointsAreRemoved(t *testing.T) {
	files := []string{
		"internal/app/gateway.go",
		"internal/app/channel.go",
		"internal/app/channel_feishu_adapter.go",
		"internal/app/channel_telegram_adapter.go",
		"internal/app/channel_dingtalk_adapter.go",
		"internal/channels/dingtalk.go",
		"internal/channels/feishu.go",
		"internal/channels/telegram.go",
		"internal/app/channel_qq_adapter.go",
		"internal/channels/qq.go",
		"internal/app/channel_wecom_adapter.go",
		"internal/channels/wecom.go",
		"web/sse.js",
	}
	for _, path := range files {
		content := mustReadWebTestFile(t, path)
		for _, retired := range []string{"/api/codex/approval", "handleCodexApprovalTextCommand"} {
			if strings.Contains(content, retired) {
				t.Fatalf("legacy generic approval entrypoint %q remains in %s", retired, path)
			}
		}
	}
	sseJS := mustReadWebTestFile(t, "web/sse.js")
	if strings.Contains(sseJS, "data.type === 'permission_request'") ||
		strings.Contains(sseJS, `case "permission_request"`) ||
		strings.Contains(sseJS, "case 'permission_request'") {
		t.Fatal("Web progress must not show a stale approval prompt when tools run autonomously")
	}
}

func TestAIEmployeeWorkbenchUsesUnifiedInvestmentAdvisorEntry(t *testing.T) {
	pipelineJS := mustReadWebTestFile(t, "web/pipeline.js")
	aePanelJS := mustReadWebTestFile(t, "web/ae_panel.js")
	indexHTML := mustReadWebTestFile(t, "web/index.html")

	for _, marker := range []string{
		`"investment-advisor"`,
		"金融信息核验",
		"短线交易情报",
		"function renderDELatestOutput",
		"查看详情",
		"latest_success",
		"已验证产物",
	} {
		if !strings.Contains(pipelineJS, marker) {
			t.Fatalf("AI employee workbench is missing %q", marker)
		}
	}
	for _, retired := range []string{`"quant-trader"`, `"investment-research-manager"`, `"financial-intelligence-analyst"`} {
		if strings.Contains(pipelineJS, retired) || strings.Contains(aePanelJS, retired) {
			t.Fatalf("internal financial facade remains in normal employee UI: %q", retired)
		}
	}
	for _, commonListMarker := range []string{
		"var aeCommonEmployeeIDs",
		"function orderCommonDEEmployees",
		"var employees = orderCommonDEEmployees(allEmployees)",
		"function renderDEWorkbenchCards",
		">常用员工</div>",
	} {
		if !strings.Contains(pipelineJS, commonListMarker) {
			t.Fatalf("AI employee workbench must keep the compact common-employee list; missing %q", commonListMarker)
		}
	}
	if strings.Count(pipelineJS, "function renderDEWorkbench(data)") != 1 {
		t.Fatal("the compact common-employee list must be the single active renderDEWorkbench entry point")
	}
	if !strings.Contains(indexHTML, `/web/pipeline.js?v=20260715-pipeline-database-v1`) ||
		!strings.Contains(indexHTML, `/web/ae_panel.js?v=20260714-investment-advisor-v2`) {
		t.Fatal("unified investment-advisor UI assets must be cache-busted for existing browsers")
	}
}

func TestWebTopbarAccountAuthIsOptionalAndUsesNovaDirectly(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	appJS := mustReadWebTestFile(t, "web/app.js")
	i18nJS := mustReadWebTestFile(t, "web/i18n.js")
	loginHTML := mustReadWebTestFile(t, "web/login.html")
	styleCSS := mustReadWebTestFile(t, "web/style.css")

	for _, marker := range []string{
		`id="topbarAuth"`,
		`id="topbarLoginButton"`,
		`id="topbarUserState"`,
		`id="topbarUserCenterButton"`,
		`id="topbarAccountMenu"`,
		`id="topbarAccountCenterLink"`,
		`id="topbarAccountUsername"`,
		`id="topbarLogoutButton"`,
		`aria-haspopup="menu"`,
		`id="communityWorkspace"`,
		`id="communityFeed"`,
		`id="communityWorkspaceRefreshButton"`,
		"account-auth-v2",
		"community-workspace-v1",
		"community-workspace-v2",
		"account-center-v1",
		"account-menu-v1",
		"https://www.novaexel.com/login/",
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("topbar account UI missing %q", marker)
		}
	}
	for _, marker := range []string{
		"function renderTopbarAccount",
		"function refreshTopbarAccount",
		"function logoutAccount",
		"function setAccountMenuOpen",
		"function toggleAccountMenu",
		"function closeAccountMenu",
		"function openCommunityWorkspace",
		"function refreshCommunityWorkspace",
		"communityFeedGeneration",
		"communityFeedLoading",
		"/api/community/feed?limit=8",
		"/api/account/config",
		"https://www.novaexel.com/nova/check-login",
		"https://www.novaexel.com/nova/logout",
		"data.account_center_url",
		"accountEndpoints.center",
		"readAccountDisplayName",
		"openkitty_account_display_username",
		"credentials: 'include'",
		"/web/login.html?redirect=",
		"userName.textContent = accountText('accountCenter', '用户中心')",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("topbar account behavior missing %q", marker)
		}
	}
	for _, marker := range []string{"accountCenter: '用户中心'", "accountCenterTitle: '打开用户中心'", "accountCurrentUser: '当前账号'", "accountOpenCenter: '进入用户中心'", "accountLogout: '退出登录'", "accountCenter: 'Account'"} {
		if !strings.Contains(i18nJS, marker) {
			t.Fatalf("account center translation missing %q", marker)
		}
	}
	for _, marker := range []string{
		"/api/account/config",
		"https://www.novaexel.com/nova/login",
		"https://www.novaexel.com/nova/check-login",
		"https://www.novaexel.com/nova/logout",
		"注册账号",
		"暂不登录",
		"credentials: 'include'",
		"rememberMe: false",
		"openkitty_account_display_username",
		"只在当前标签页暂存用户名",
	} {
		if !strings.Contains(loginHTML, marker) {
			t.Fatalf("optional account login page missing %q", marker)
		}
	}
	hintWrite := strings.Index(loginHTML, "rememberAccountDisplayName(data.user || username)")
	returnToWorkbench := strings.Index(loginHTML, "window.location.replace(safeReturnPath())")
	if hintWrite < 0 || returnToWorkbench < 0 || hintWrite > returnToWorkbench {
		t.Fatal("successful login must remember the display-only username before returning to the workbench")
	}
	if strings.Contains(appJS, "userName.textContent = username") {
		t.Fatal("signed-in topbar must say 用户中心 instead of using the username as the control label")
	}
	if strings.Contains(indexHTML, `id="topbarWorkspaceButton"`) ||
		strings.Contains(indexHTML, `class="topbar-auth-workspace"`) {
		t.Fatal("the signed-in topbar must not expose a separate 工作区 control")
	}
	menuIndex := strings.Index(indexHTML, `id="topbarAccountMenu"`)
	logoutIndex := strings.Index(indexHTML, `id="topbarLogoutButton"`)
	if menuIndex < 0 || logoutIndex < menuIndex || strings.Count(indexHTML, `id="topbarLogoutButton"`) != 1 {
		t.Fatal("退出登录 must exist exactly once inside the 用户中心 menu")
	}
	for _, forbidden := range []string{
		"sessionStorage.setItem('auth_token'",
		"localStorage.setItem('auth_token'",
		"localStorage.setItem('password'",
		"fetch('/api/account/login'",
	} {
		if strings.Contains(loginHTML, forbidden) {
			t.Fatalf("login page must not persist credentials or provider tokens: %q", forbidden)
		}
	}
	for _, selector := range []string{".topbar-auth", ".topbar-auth-login", ".topbar-auth-user", ".topbar-auth-center", ".topbar-account-menu", ".topbar-account-menu-item", ".topbar-account-menu-logout", ".community-workspace", ".community-post-card"} {
		if !strings.Contains(styleCSS, selector) {
			t.Fatalf("topbar account styling missing %s", selector)
		}
	}
	for _, retiredSelector := range []string{".topbar-auth-workspace", ".topbar-auth-logout"} {
		if strings.Contains(styleCSS, retiredSelector) {
			t.Fatalf("retired topbar action styling remains: %s", retiredSelector)
		}
	}
}

func TestWebHeaderActionsMatchLoginStyleAndMessageInputCanResize(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	styleCSS := mustReadWebTestFile(t, "web/style.css")
	chatJS := mustReadWebTestFile(t, "web/chat.js")

	for _, marker := range []string{
		`class="hb-btn topbar-auth-login" onclick="createNewSession()"`,
		`class="hb-btn topbar-auth-login" onclick="openAEPanel()"`,
		`id="messageInputResizeHandle"`,
		`aria-orientation="horizontal"`,
		`20260726-header-actions-v1-input-resize-v1`,
		`20260726-input-resize-v1`,
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("workbench header/input UI missing %q", marker)
		}
	}
	for _, legacy := range []string{
		`onclick="createNewSession()" style="background:#366ef4`,
		`onclick="openAEPanel()" style="background:#4f6ef7`,
	} {
		if strings.Contains(indexHTML, legacy) {
			t.Fatalf("solid blue header action remains: %q", legacy)
		}
	}
	for _, marker := range []string{
		".message-input-resize-handle",
		"cursor: ns-resize",
		"max-height: min(50vh, 420px)",
		"body.is-resizing-message-input",
	} {
		if !strings.Contains(styleCSS, marker) {
			t.Fatalf("resizable message input styling missing %q", marker)
		}
	}
	for _, marker := range []string{
		"function initMessageInputResize()",
		"openkitty.messageInputHeight",
		"handle.addEventListener('pointerdown'",
		"handle.addEventListener('keydown'",
		"handle.addEventListener('dblclick'",
		"document.addEventListener('DOMContentLoaded', initMessageInputResize)",
	} {
		if !strings.Contains(chatJS, marker) {
			t.Fatalf("resizable message input behavior missing %q", marker)
		}
	}
}

func TestWebComposerPlacesCompactNeutralToolsBelowText(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	styleCSS := mustReadWebTestFile(t, "web/style.css")
	themeJS := mustReadWebTestFile(t, "web/theme.js")

	textIndex := strings.Index(indexHTML, `id="messageInput"`)
	actionsIndex := strings.Index(indexHTML, `class="composer-actions"`)
	if textIndex < 0 || actionsIndex < 0 || textIndex >= actionsIndex {
		t.Fatal("composer actions must remain after the textarea so CSS can place them on the lower row")
	}
	for _, marker := range []string{
		`id="micBtn"`,
		`id="uploadBtn"`,
		`id="imageBtn"`,
		`aria-label="输入工具与发送"`,
		"composer-toolbar-v2",
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("compact composer toolbar missing %q", marker)
		}
	}

	containerRule := webCSSRule(t, styleCSS, ".input-container")
	for _, marker := range []string{"flex-direction: column", "border-radius: 24px", "background: #fff"} {
		if !strings.Contains(containerRule, marker) {
			t.Fatalf("composer shell must stack text and tools in one neutral bubble; missing %q in %s", marker, containerRule)
		}
	}
	actionsRule := webCSSRule(t, styleCSS, ".composer-actions")
	if !strings.Contains(actionsRule, "width: 100%") {
		t.Fatalf("composer actions must occupy a lower row: %s", actionsRule)
	}
	toolRule := webCSSRule(t, styleCSS, ".input-icon-btn")
	for _, marker := range []string{"width: 26px", "height: 26px", "background: transparent", "border: 0", "color: #737373"} {
		if !strings.Contains(toolRule, marker) {
			t.Fatalf("composer tools must stay compact and neutral; missing %q in %s", marker, toolRule)
		}
	}
	sendRule := webCSSRule(t, styleCSS, ".send-btn")
	for _, marker := range []string{"width: 28px", "height: 28px", "margin-left: auto", "color: #525252", "background: #e5e5e5", "border: 1px solid #dedede", "border-radius: 50%", "box-shadow: none"} {
		if !strings.Contains(sendRule, marker) {
			t.Fatalf("send control must use the compact neutral treatment; missing %q in %s", marker, sendRule)
		}
	}
	sendHoverRule := webCSSRule(t, styleCSS, ".send-btn:hover:not(:disabled)")
	for _, marker := range []string{"color: #404040", "background: #d4d4d4", "border-color: #d4d4d4"} {
		if !strings.Contains(sendHoverRule, marker) {
			t.Fatalf("send hover must remain softly gray; missing %q in %s", marker, sendHoverRule)
		}
	}
	for _, black := range []string{"background: #303030", "background: #171717", "border-color: #171717"} {
		if strings.Contains(sendRule, black) || strings.Contains(sendHoverRule, black) {
			t.Fatalf("black send-button treatment remains: %s", black)
		}
	}
	for _, colored := range []string{"#6366f1", "#4f46e5", "#4338ca", "rgba(79, 70, 229"} {
		if strings.Contains(toolRule, colored) || strings.Contains(sendRule, colored) {
			t.Fatalf("blue-purple composer color remains in the primary tool rules: %s", colored)
		}
	}
	for _, marker := range []string{
		`.input-container{background:#1a1f2e!important`,
		`.input-icon-btn{background:transparent!important;color:#a3a3a3!important`,
		`.send-btn{background:#e5e5e5!important;color:#171717!important`,
	} {
		if !strings.Contains(themeJS, marker) {
			t.Fatalf("dark theme must keep composer controls neutral; missing %q", marker)
		}
	}
}

func TestWebTopbarDisclosureControlsUseSVGAndState(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	styleCSS := mustReadWebTestFile(t, "web/style.css")
	chatJS := mustReadWebTestFile(t, "web/chat.js")
	appJS := mustReadWebTestFile(t, "web/app.js")
	i18nJS := mustReadWebTestFile(t, "web/i18n.js")

	for _, marker := range []string{
		`id="headerCollapseBtn" class="topbar-collapse-toggle"`,
		`class="topbar-collapse-icon"`,
		`aria-controls="headerButtons"`,
		`id="workbenchMenuButton"`,
		`class="hb-btn topbar-menu-trigger"`,
		`aria-haspopup="menu"`,
		`aria-controls="toolMenu"`,
		`class="topbar-auth-chevron topbar-disclosure-icon"`,
		`20260726-topbar-disclosure-v1`,
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("topbar disclosure markup missing %q", marker)
		}
	}
	for _, marker := range []string{
		".topbar-collapse-toggle",
		".topbar-collapse-toggle.is-collapsed .topbar-collapse-icon",
		`.topbar-menu-trigger[aria-expanded="true"] .topbar-disclosure-icon`,
		"@media (prefers-reduced-motion: reduce)",
	} {
		if !strings.Contains(styleCSS, marker) {
			t.Fatalf("topbar disclosure styling missing %q", marker)
		}
	}
	for _, marker := range []string{
		"function syncHeaderCompactControl(isCollapsed)",
		"hb.classList.toggle('is-collapsed', nextCollapsed)",
		"btn.setAttribute('aria-expanded', isCollapsed ? 'false' : 'true')",
		"openkitty:languagechange",
	} {
		if !strings.Contains(chatJS, marker) {
			t.Fatalf("topbar collapse behavior missing %q", marker)
		}
	}
	for _, marker := range []string{
		"function setDropdownMenuState(menu, open, preferredTrigger)",
		"trigger.setAttribute('aria-expanded', open ? 'true' : 'false')",
		"trigger.classList.toggle('is-open', open)",
		"function closeDropdownMenus()",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("topbar menu behavior missing %q", marker)
		}
	}
	for _, marker := range []string{
		"workbenchButton: '工作台'",
		"workbenchButton: 'Workbench'",
		"topbarCollapseActions: '收起顶栏操作'",
		"topbarExpandActions: 'Expand topbar actions'",
	} {
		if !strings.Contains(i18nJS, marker) {
			t.Fatalf("topbar disclosure translation missing %q", marker)
		}
	}
	for _, legacy := range []string{
		`id="headerCollapseBtn" onclick="toggleHeaderCompact()" style=`,
		`data-i18n="workbenchButton">⚙ 工作台 ▾</button>`,
		"btn.textContent = '◀'",
		"btn.textContent = '▶'",
	} {
		if strings.Contains(indexHTML, legacy) || strings.Contains(chatJS, legacy) {
			t.Fatalf("legacy text disclosure control remains: %q", legacy)
		}
	}
}

func TestWebSidebarDisclosureUsesSVGAndSynchronizedState(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	styleCSS := mustReadWebTestFile(t, "web/style.css")
	appJS := mustReadWebTestFile(t, "web/app.js")
	i18nJS := mustReadWebTestFile(t, "web/i18n.js")

	for _, marker := range []string{
		`class="sidebar-toggle" id="sidebarToggleBtn"`,
		`class="sidebar-toggle-icon"`,
		`class="sidebar-toggle-divider"`,
		`class="sidebar-toggle-grip"`,
		`aria-controls="sidebar"`,
		`aria-expanded="true"`,
		`sidebar-disclosure-v3`,
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("sidebar disclosure markup missing %q", marker)
		}
	}
	for _, marker := range []string{
		".sidebar-toggle:focus-visible",
		".sidebar-toggle-icon",
		".sidebar.collapsed .sidebar-toggle-icon",
		".dark .sidebar-toggle",
	} {
		if !strings.Contains(styleCSS, marker) {
			t.Fatalf("sidebar disclosure styling missing %q", marker)
		}
	}
	collapsedButtonRule := webCSSRule(t, styleCSS, ".sidebar.collapsed .sidebar-toggle")
	if strings.Contains(collapsedButtonRule, "transform:") {
		t.Fatalf("collapsed sidebar must rotate only its icon, not the whole button; rule=%q", collapsedButtonRule)
	}
	for _, marker := range []string{"width: 28px", "height: 28px"} {
		if !strings.Contains(collapsedButtonRule, marker) {
			t.Fatalf("collapsed sidebar control should stay visually secondary %q; rule=%q", marker, collapsedButtonRule)
		}
	}
	collapsedIconRule := webCSSRule(t, styleCSS, ".sidebar.collapsed .sidebar-toggle-icon")
	if !strings.Contains(collapsedIconRule, "transform: none") {
		t.Fatalf("sidebar-layout icon should keep a stable orientation; rule=%q", collapsedIconRule)
	}
	baseButtonRule := webCSSRule(t, styleCSS, ".sidebar-toggle")
	for _, marker := range []string{"width: 28px", "height: 28px", "background: transparent", "border: 1px solid transparent", "color: #6f7785", "opacity: 0.86"} {
		if !strings.Contains(baseButtonRule, marker) {
			t.Fatalf("sidebar toggle should use a small muted treatment %q; rule=%q", marker, baseButtonRule)
		}
	}
	hoverButtonRule := webCSSRule(t, styleCSS, ".sidebar-toggle:hover")
	if strings.Contains(hoverButtonRule, "box-shadow") {
		t.Fatalf("secondary sidebar toggle should not gain a prominent shadow; rule=%q", hoverButtonRule)
	}
	for _, marker := range []string{
		"function syncSidebarToggle(isCollapsed)",
		"var isCollapsed = sidebar.classList.toggle('collapsed')",
		"button.setAttribute('aria-expanded', isCollapsed ? 'false' : 'true')",
		"syncSidebarToggle(true)",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("sidebar disclosure behavior missing %q", marker)
		}
	}
	for _, marker := range []string{
		"sidebarCollapse: '收起侧边栏'",
		"sidebarExpand: '展开侧边栏'",
		"sidebarCollapse: 'Collapse sidebar'",
		"sidebarExpand: 'Expand sidebar'",
	} {
		if !strings.Contains(i18nJS, marker) {
			t.Fatalf("sidebar disclosure translation missing %q", marker)
		}
	}
	if strings.Contains(indexHTML, `<div class="sidebar-toggle" onclick="toggleSidebar()">◀</div>`) {
		t.Fatal("legacy text sidebar toggle remains")
	}
}

func TestWebSidebarUsesDistinctNewTaskAndWorkbenchIcons(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	styleCSS := mustReadWebTestFile(t, "web/style.css")

	for _, marker := range []string{
		`class="sidebar-compose-icon"`,
		`<circle cx="12" cy="12" r="9"/>`,
		`<path d="M12 8v8M8 12h8"/>`,
		`class="sidebar-search sidebar-history-search"`,
		`class="topbar-action-icon workbench-grid-icon"`,
		`<rect x="14" y="14" width="7" height="7" rx="1.5"/>`,
		"macs-sidebar-controls-v4",
		"new-task-search-brand-v2",
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("sidebar control treatment missing %q", marker)
		}
	}
	collapsedPrimaryRule := webCSSRule(t, styleCSS, ".sidebar.collapsed .sidebar-primary-action")
	for _, marker := range []string{"color: #4f46e5", "background: #eef2ff", "border-radius: 12px", "box-shadow:"} {
		if !strings.Contains(collapsedPrimaryRule, marker) {
			t.Fatalf("collapsed new-task control should be visually primary; missing %q in %q", marker, collapsedPrimaryRule)
		}
	}
	if strings.Index(indexHTML, `class="sidebar-search sidebar-history-search"`) < strings.Index(indexHTML, `class="sidebar-history-heading"`) {
		t.Fatal("conversation search should live in the history section")
	}
}

func TestWebTopBrandShowsChineseNameAndEnglishExpansion(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	styleCSS := mustReadWebTestFile(t, "web/style.css")
	for _, marker := range []string{
		"MACS（麦卡斯）· Multi-Agent Collaboration System",
		`class="brand-latin">MACS</span>`,
		`class="brand-cn">麦卡斯</span>`,
		`<small>Multi-Agent Collaboration System</small>`,
		`<small>麦卡斯</small>`,
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("top brand identity is missing %q", marker)
		}
	}
	for _, marker := range []string{
		".sidebar-header-left .brand-latin {",
		"color: #244a5f",
		".sidebar-header-left .brand-cn {",
		"color: #5f6f63",
	} {
		if !strings.Contains(styleCSS, marker) {
			t.Fatalf("brand color styling is missing %q", marker)
		}
	}
}

func TestWebDocumentAutomationIsNotExposedInTheUI(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	for _, marker := range []string{
		`openDocumentAutomationStudio()`,
		`/web/document_studio.js`,
		`/web/document_studio.css`,
		`data-i18n="documentAutomation"`,
		"文档与界面自动化",
	} {
		if strings.Contains(indexHTML, marker) {
			t.Fatalf("document automation should remain hidden from the Web UI; found %q", marker)
		}
	}
}

func TestWebCommonQuestionsLiveOutsideTheComposer(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	appJS := mustReadWebTestFile(t, "web/app.js")
	styleCSS := mustReadWebTestFile(t, "web/style.css")
	i18nJS := mustReadWebTestFile(t, "web/i18n.js")

	for _, marker := range []string{
		`id="commonQuestionsButton"`,
		`id="commonQuestionsPanel"`,
		`class="preset-btn common-question-item"`,
		`aria-controls="commonQuestionsPanel"`,
		"common-questions-v1",
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("common questions entry is missing %q", marker)
		}
	}
	if strings.Contains(indexHTML, `id="presetButtons"`) || strings.Contains(indexHTML, `class="preset-buttons"`) {
		t.Fatal("preset questions should not occupy space in the message composer")
	}
	for _, marker := range []string{
		"function setCommonQuestionsOpen(open)",
		"function toggleCommonQuestions(e)",
		"function closeCommonQuestions()",
		"input.value = message",
	} {
		if !strings.Contains(appJS, marker) {
			t.Fatalf("common questions behavior is missing %q", marker)
		}
	}
	for _, marker := range []string{
		".common-questions-panel {",
		".common-question-item.preset-btn {",
		".sidebar.collapsed .common-questions-panel {",
		"MACS macOS-inspired modal system",
	} {
		if !strings.Contains(styleCSS, marker) {
			t.Fatalf("common questions styling is missing %q", marker)
		}
	}
	for _, marker := range []string{
		"commonQuestions: '常见问题'",
		"commonQuestions: 'Common questions'",
	} {
		if !strings.Contains(i18nJS, marker) {
			t.Fatalf("common questions translation is missing %q", marker)
		}
	}
}

func TestWebNewConversationShowsSelectableStarterQuestions(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	chatJS := mustReadWebTestFile(t, "web/chat.js")
	styleCSS := mustReadWebTestFile(t, "web/style.css")
	i18nJS := mustReadWebTestFile(t, "web/i18n.js")

	for _, marker := range []string{
		`data-new-chat="true"`,
		`data-new-chat-label="帮我看看今天的财经和股票动态"`,
		"new-conversation-starters-v2",
	} {
		if !strings.Contains(indexHTML, marker) {
			t.Fatalf("new-conversation starter source is missing %q", marker)
		}
	}
	for _, marker := range []string{
		"function renderNewConversationState()",
		"function useNewConversationPrompt(button)",
		"function clearConversationEmptyState(container)",
		"input.dispatchEvent(new Event('input', {bubbles: true}))",
		"container.innerHTML = renderNewConversationState()",
	} {
		if !strings.Contains(chatJS, marker) {
			t.Fatalf("new-conversation starter behavior is missing %q", marker)
		}
	}
	for _, marker := range []string{
		".new-conversation-state {",
		".new-conversation-questions {",
		".new-conversation-question {",
		".new-conversation-question-surface {",
		".new-conversation-question.is-selected .new-conversation-question-surface {",
		"@keyframes newConversationQuestionFloat",
		"animation-play-state: paused",
	} {
		if !strings.Contains(styleCSS, marker) {
			t.Fatalf("new-conversation starter styling is missing %q", marker)
		}
	}
	for _, marker := range []string{
		"newConversationHeading: '开始一段新对话'",
		"newConversationHeading: 'Start a new conversation'",
	} {
		if !strings.Contains(i18nJS, marker) {
			t.Fatalf("new-conversation starter translation is missing %q", marker)
		}
	}
}

func TestWebTopbarModelBadgeUsesSmallerDarkText(t *testing.T) {
	indexHTML := mustReadWebTestFile(t, "web/index.html")
	styleCSS := mustReadWebTestFile(t, "web/style.css")

	if !strings.Contains(indexHTML, `id="modelBadge"`) ||
		!strings.Contains(indexHTML, "agent-model-row-v1") {
		t.Fatal("topbar model badge or its cache marker is missing")
	}
	stackRule := webCSSRule(t, styleCSS, ".agent-model-stack")
	for _, marker := range []string{"flex-direction: row", "align-items: center", "width: auto", "max-width: 226px"} {
		if !strings.Contains(stackRule, marker) {
			t.Fatalf("Agent and model should render side by side; missing %q in %q", marker, stackRule)
		}
	}
	rule := webCSSRule(t, styleCSS, ".agent-model-stack #modelBadge")
	for _, marker := range []string{"color: #475569", "font-size: 9px", "font-weight: 400"} {
		if !strings.Contains(rule, marker) {
			t.Fatalf("topbar model badge should use smaller dark text %q; rule=%q", marker, rule)
		}
	}
}

func TestWebChatBubblesHideInternalExecutionDetailsByDefault(t *testing.T) {
	chatJS := mustReadWebTestFile(t, "web/chat.js")
	sseJS := mustReadWebTestFile(t, "web/sse.js")
	indexHTML := mustReadWebTestFile(t, "web/index.html")

	for _, marker := range []string{
		"function isDetailedExecutionUIEnabled()",
		"function sanitizeAssistantBubbleContent(value)",
		"function looksLikeInternalExecutionReport(value)",
		"decodedContent = sanitizeAssistantBubbleContent(decodedContent)",
		"if (!isDetailedExecutionUIEnabled()) return ''",
		"var displayName = (smatch.replace",
		"normalizedPath.split('/').pop()",
	} {
		if !strings.Contains(chatJS, marker) {
			t.Fatalf("chat bubble presentation boundary missing %q", marker)
		}
	}
	for _, marker := range []string{
		"function userFacingProgressMessage(value, eventType, status)",
		"if (!detailedExecutionUIEnabled())",
		"var showDetailedProgress = detailedExecutionUIEnabled()",
		"exportBtn.style.display = showDetailedProgress ? 'block' : 'none'",
		"data-progress-detail', showDetailedProgress ? 'full' : 'safe'",
		"window.looksLikeInternalExecutionReport(failedResult)",
		"window.sanitizeAssistantBubbleContent(failedResult)",
		"处理未完成，请重试。",
	} {
		if !strings.Contains(sseJS, marker) {
			t.Fatalf("SSE user-facing failure/progress boundary missing %q", marker)
		}
	}
	if strings.Contains(sseJS, "var failedContent = failedResult") {
		t.Fatal("failed tasks must not append raw backend results directly to the chat bubble")
	}
	if strings.Contains(sseJS, "if (!detailedExecutionUIEnabled()) {\n\t\t\t\tvar compactStatus") {
		t.Fatal("ordinary mode must retain safe progress bubbles instead of reducing them to one status line")
	}
	if !strings.Contains(indexHTML, "bubble-safety-v1") {
		t.Fatal("chat bubble presentation changes must be cache-busted")
	}
}

func webCSSRule(t *testing.T, css, selector string) string {
	t.Helper()
	start := strings.Index(css, selector+" {")
	if start < 0 {
		t.Fatalf("missing CSS rule for %s", selector)
	}
	end := strings.Index(css[start:], "}")
	if end < 0 {
		t.Fatalf("unterminated CSS rule for %s", selector)
	}
	return css[start : start+end+1]
}

func mustReadWebTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
