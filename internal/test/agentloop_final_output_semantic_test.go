package app

import (
	"strings"
	"testing"
)

func TestSemanticAssessmentParserAcceptsStrictStates(t *testing.T) {
	for _, state := range []finalOutputState{finalOutputFinal, finalOutputProgress, finalOutputBlocked} {
		raw := `{"state":"` + string(state) + `","confidence":0.91,"reason":"semantic evidence"}`
		assessment, ok := parseFinalOutputAssessment(raw)
		if !ok || assessment.State != state || assessment.Confidence != 0.91 {
			t.Fatalf("parseFinalOutputAssessment(%s) = %#v, %v", state, assessment, ok)
		}
	}
	if _, ok := parseFinalOutputAssessment(`{"state":"unknown","confidence":1,"reason":"bad"}`); ok {
		t.Fatal("unknown semantic state must be rejected")
	}
}

func TestSubstantiveAnswerWithoutProtocolKeywordsDoesNotDoubleModelCalls(t *testing.T) {
	assessment := classifyFinalOutputHeuristically("微博榜单包含十六个话题，气象、民生和航天内容占比较高；其中台风相关话题热度最高，后续讨论主要集中在救援和出行影响。")
	if assessment.State != finalOutputFinal {
		t.Fatalf("substantive natural-language answer should be accepted directly, got %#v", assessment)
	}
}

func TestDirectAnswerMayQuoteNextStepWithoutBecomingProgress(t *testing.T) {
	output := `同步 vs 异步的核心差异是是否阻塞当前执行流。

- 同步：必须等待操作完成才能继续。
- 异步：当前执行流立即继续，结果稍后处理。

一句话总结：同步是"等结果再走下一步"，异步是"先走下一步，结果到了再回来"。`
	assessment := classifyFinalOutputHeuristically(output)
	if assessment.State != finalOutputFinal {
		t.Fatalf("quoted subject-matter language must not be treated as live progress, got %#v", assessment)
	}
	if isProgressOnlyFinalOutput(output) {
		t.Fatal("final post-processing must preserve the substantive answer")
	}
}

func TestExplicitFutureActionIntentRemainsProgress(t *testing.T) {
	assessment := classifyFinalOutputHeuristically("目前只有原始数据，报告尚未生成；下一步我将整理并发布。")
	if assessment.State != finalOutputProgress {
		t.Fatalf("an explicit future-action commitment should remain progress, got %#v", assessment)
	}
}

func TestLeadingFutureActionLanguageRemainsProgress(t *testing.T) {
	assessment := classifyFinalOutputHeuristically("下一步我将整理数据并发布报告。")
	if assessment.State != finalOutputProgress {
		t.Fatalf("a leading action promise should remain progress, got %#v", assessment)
	}
}

func TestStepHeadingCannotHideFutureActionProgress(t *testing.T) {
	output := "## 步骤2：生成交付包文件\n\n现在我将创建三个必需的文件：README.md、summary.json、checksums.txt"
	assessment := classifyFinalOutputHeuristically(output)
	if assessment.State != finalOutputProgress {
		t.Fatalf("a step heading followed by an unexecuted action promise must remain progress, got %#v", assessment)
	}
}

func TestLowYieldSearchSelfNoteRemainsProgress(t *testing.T) {
	for _, output := range []string{
		"新闻搜索结果不够精准，需要换更精确关键词重新搜索。",
		"第一轮搜索未给出独立来源，改为精确事件标题搜索。",
	} {
		assessment := classifyFinalOutputHeuristically(output)
		if assessment.State != finalOutputProgress {
			t.Fatalf("a proposal to search again is not a final answer: output=%q assessment=%#v", output, assessment)
		}
	}
}

func TestRecommendedNextStepIsNotHardProgress(t *testing.T) {
	output := "建议的下一步：检查配置文件中的端口是否重复。"
	assessment := classifyFinalOutputHeuristically(output)
	if assessment.State != finalOutputAmbiguous {
		t.Fatalf("a recommendation should be context-judged rather than hard-retried, got %#v", assessment)
	}
	if isProgressOnlyFinalOutput(output) {
		t.Fatal("a user-facing recommendation must survive synchronous finalization")
	}
}

func TestNextJSTitleIsNotFutureAction(t *testing.T) {
	assessment := classifyFinalOutputHeuristically("Next.js 是一个基于 React 的 Web 应用框架。")
	if assessment.State != finalOutputFinal {
		t.Fatalf("Next.js must not be mistaken for an English next-action prefix, got %#v", assessment)
	}
}

func TestMixedCompletionAndFutureActionUsesSemanticJudge(t *testing.T) {
	assessment := classifyFinalOutputHeuristically("数据抓取已完成，但下一步我将生成报告并发布到 Chatroom。")
	if assessment.State != finalOutputAmbiguous {
		t.Fatalf("mixed completion/future language should use semantic judgment, got %#v", assessment)
	}
}

func TestPipelineStepAcceptsSubstantiveStageHandoffWithoutWholeTaskJudge(t *testing.T) {
	output := `# 素材准备结果

素材准备已完成：已冻结并读取真实来源，下面给出素材定位、受众、事实与观点边界以及正文大纲。下一步我将核验两项关键数字后生成发布物。`
	global := classifyFinalOutputHeuristically(output)
	if global.State != finalOutputAmbiguous {
		t.Fatalf("fixture should be ambiguous for a whole-task judge, got %#v", global)
	}
	stage := classifyPipelineStepOutput(output)
	if stage.State != finalOutputFinal {
		t.Fatalf("substantive stage hand-off should finish the current pipeline step, got %#v", stage)
	}
}

func TestPipelineStepStillRejectsPureFutureAction(t *testing.T) {
	assessment := classifyPipelineStepOutput("下一步我将调用工具获取素材并整理大纲。")
	if assessment.State != finalOutputProgress {
		t.Fatalf("pure future-action text must remain progress in a pipeline step, got %#v", assessment)
	}
}

func TestLongMixedCompletionAndFutureActionUsesSemanticJudge(t *testing.T) {
	output := "数据抓取已完成。" + strings.Repeat("这里是已经整理的背景信息和阶段性结果。", 80) + "下一步我将生成报告并发布到 Chatroom。"
	if len([]rune(output)) <= 600 {
		t.Fatal("test fixture must exceed the historical 600-rune cutoff")
	}
	assessment := classifyFinalOutputHeuristically(output)
	if assessment.State != finalOutputAmbiguous {
		t.Fatalf("long mixed completion/future language should use semantic judgment, got %#v", assessment)
	}
}

func TestCompletedAuditMayQuoteInternalCheckpointMarker(t *testing.T) {
	output := `# 审查结论

任务完成，并已保存到 audit.md。日志中曾出现“[Checkpoint - iteration 12] what remains to be done?”，这是当时的内部控制消息，不是本报告的执行指令。`
	assessment := classifyFinalOutputHeuristically(output)
	if assessment.State != finalOutputFinal {
		t.Fatalf("a completed audit quoting a checkpoint should remain final, got %#v", assessment)
	}
}

func TestLeadingInternalCheckpointRemainsProgress(t *testing.T) {
	assessment := classifyFinalOutputHeuristically("[Checkpoint - iteration 12]\nReview your progress so far: what remains to be done?")
	if assessment.State != finalOutputProgress {
		t.Fatalf("a leading internal checkpoint must remain progress, got %#v", assessment)
	}
}

func TestLeadingInternalControlRemainsProgress(t *testing.T) {
	assessment := classifyFinalOutputHeuristically("[INTERNAL CONTROL — REFLECTION]\nReview your progress and continue.")
	if assessment.State != finalOutputProgress || assessment.Confidence != 1 {
		t.Fatalf("a leading internal control message must remain progress, got %#v", assessment)
	}
}

func TestPersistedExecutionBriefCannotBecomeFinalAnswer(t *testing.T) {
	output := `[preference_1785492032522719178 的执行简报]

意图理解：本会话属于投融资链路起始步，仅做意图识别与执行简报，不做业务研究、不调用工具、不生成最终交付物。

请宿主把当前执行简报交给下游步骤。本步骤未生成任何交付文件。`
	assessment := classifyFinalOutputHeuristically(output)
	if assessment.State != finalOutputProgress || assessment.Confidence != 1 {
		t.Fatalf("internal execution brief must be retried instead of shown to the user: %#v", assessment)
	}
}

func TestCompletedAuditMayQuoteInternalControlMarker(t *testing.T) {
	output := "安全审计已完成，系统会阻止模型把内部控制内容直接交付给用户。示例标记是 `[INTERNAL CONTROL — REFLECTION]`，该引用仅用于说明检测规则。"
	assessment := classifyFinalOutputHeuristically(output)
	if assessment.State != finalOutputFinal {
		t.Fatalf("a completed audit quoting an internal marker should remain final, got %#v", assessment)
	}
}

func TestVerifiedActionReceiptAppendsWithoutReplacingFinalAnswer(t *testing.T) {
	final := "微博热点已整理完成，共 16 条，其中台风话题排名第一。"
	receipt := "已完成工具动作。\n- 结果 ID：#2529"
	merged := appendVerifiedActionReceipt(final, receipt)
	for _, want := range []string{final, "已验证操作回执", "#2529"} {
		if !strings.Contains(merged, want) {
			t.Fatalf("merged final answer lost %q: %s", want, merged)
		}
	}
	if strings.HasPrefix(merged, receipt) {
		t.Fatalf("receipt replaced the substantive final answer: %s", merged)
	}
}

func TestGraceOutcomeAcceptsFinalAndDoesNotResynthesizeBlocked(t *testing.T) {
	finalAssessment := finalOutputAssessment{State: finalOutputFinal, Confidence: 0.9}
	if got := graceOutcomeEventStatus(finalAssessment, true); got != "success" {
		t.Fatalf("grace final status = %q, want success", got)
	}
	if got := graceOutcomeEventStatus(finalAssessment, false); got != "failed" {
		t.Fatalf("grace final with incomplete plan status = %q, want failed", got)
	}

	blockedAssessment := finalOutputAssessment{State: finalOutputBlocked, Confidence: 1}
	if shouldSynthesizeGraceAnswer(blockedAssessment, true) {
		t.Fatal("a terminal blocked answer must not trigger grace synthesis")
	}
	if got := graceOutcomeEventStatus(blockedAssessment, true); got != "failed" {
		t.Fatalf("grace blocked status = %q, want failed", got)
	}
}

func TestSemanticAssessmentCacheKeyIncludesChangingToolEvidence(t *testing.T) {
	output := "数据已抓取完成，但下一步我将发布。"
	before := finalOutputAssessmentCacheKey(output, nil, []string{"search result only"})
	after := finalOutputAssessmentCacheKey(output, nil, []string{"search result only", "confirmed action receipt"})
	if before == after {
		t.Fatal("semantic assessment cache must not reuse a verdict after tool evidence changes")
	}
	if same := finalOutputAssessmentCacheKey(output, nil, []string{"search result only"}); same != before {
		t.Fatal("identical candidate context should retain a stable cache key")
	}
}

func TestStructuredActionReceiptSurvivesLongToolOutput(t *testing.T) {
	hash := strings.Repeat("a", 64)
	receipt := `OPENKITTY_ACTION_RESULT={"ok":true,"action":"post","result_id":2529,"topic_id":7,"share_url":"/chatroom/share?id=synthetic","content_sha256":"` + hash + `","robot_id":"openkitty-test"}`
	longResult := "OPENKITTY_ACTION_RESULT={broken}\n" + strings.Repeat("diagnostic noise ", 400) + "\n" + receipt
	call := ToolCall{Name: "ptc_execute", Arguments: map[string]interface{}{
		"script": `subprocess.run([python, "chatroom_agent.py", "post", "7", "@post.md"])`,
	}}
	stored := toolResultForFinalization(call, longResult, true)
	if !strings.Contains(stored, receipt) || strings.Contains(stored, "diagnostic noise") {
		t.Fatalf("finalization result did not preserve the compact receipt: %q", stored)
	}
	deliverable := buildActionDeliverableFinalAnswerFromToolResults([]string{stored})
	for _, want := range []string{"结果 ID：#2529", "topic #7", "/chatroom/share?id=synthetic", hash, "openkitty-test"} {
		if !strings.Contains(deliverable, want) {
			t.Fatalf("structured action deliverable missing %q: %s", want, deliverable)
		}
	}
}

func TestUntrustedReceiptCannotPromoteReadOnlyToolToSuccess(t *testing.T) {
	hash := strings.Repeat("b", 64)
	receipt := `OPENKITTY_ACTION_RESULT={"ok":true,"action":"post","result_id":99,"topic_id":7,"content_sha256":"` + hash + `"}`
	stored := toolResultForFinalization(ToolCall{Name: "read_file", Arguments: map[string]interface{}{"path": "old.log"}}, receipt, true)
	if deliverable := buildActionDeliverableFinalAnswerFromToolResults([]string{stored}); deliverable != "" {
		t.Fatalf("read-only tool forged an action deliverable: %s", deliverable)
	}
}

func TestNewerFailedMutationBeatsOlderSuccessReceipt(t *testing.T) {
	hash := strings.Repeat("c", 64)
	call := ToolCall{Name: "ptc_execute", Arguments: map[string]interface{}{
		"script": `subprocess.run([python, "chatroom_agent.py", "post", "7", "@post.md"])`,
	}}
	success := toolResultForFinalization(call, `OPENKITTY_ACTION_RESULT={"ok":true,"action":"post","result_id":100,"topic_id":7,"content_sha256":"`+hash+`"}`, true)
	failure := toolResultForFinalization(call, "ERROR: post rejected", false)
	if deliverable := buildActionDeliverableFinalAnswerFromToolResults([]string{success, failure}); deliverable != "" {
		t.Fatalf("older success overrode a newer failed mutation: %s", deliverable)
	}
}

func TestExportAccountRedactionSupportsHyphenatedServiceKeys(t *testing.T) {
	secret := "sk-openkitty-synthetic-1234567890abcdef"
	clean := string(redactAccountInfoForExport([]byte("credential=" + secret)))
	if strings.Contains(clean, secret) || !strings.Contains(clean, "sk-[REDACTED]") {
		t.Fatalf("hyphenated service key was not redacted: %q", clean)
	}
}

func TestExportAccountRedactionPreservesOrdinaryNewsURLSlugs(t *testing.T) {
	fixtures := []string{
		"https://techcrunch.com/2026/07/10/sk-hynix-raises-26-5b-in-largest-foreign-ipo-in-us-history/",
		"https://example.com/disable-auto-play-and-infinite-scroll-or-risk-huge-fines/",
		"https://example.com/news/foo-sk-hynix-wall-street-debut/",
	}
	for _, fixture := range fixtures {
		if got := string(redactAccountInfoForExport([]byte(fixture))); got != fixture {
			t.Errorf("ordinary news URL was corrupted\ninput: %s\noutput: %s", fixture, got)
		}
	}
}

func TestResearchToolFinalizationPreservesLateArticleURLs(t *testing.T) {
	lateURL := "https://publisher.example/2026/07/late-rss-item"
	lateMarker := "LATE_RSS_TITLE_AND_PUBLICATION_METADATA"
	content := strings.Repeat("compact evidence ", 1500) + lateMarker + " " + lateURL
	stored := toolResultForFinalization(ToolCall{Name: "social_trending"}, content, true)
	if !strings.Contains(stored, lateMarker) || !strings.Contains(stored, lateURL) {
		t.Fatalf("research finalization dropped a late RSS article URL: %s", stored)
	}
	ordinary := toolResultForFinalization(ToolCall{Name: "read_file"}, content, true)
	if len(ordinary) >= len(stored) || strings.Contains(ordinary, lateMarker) {
		t.Fatalf("ordinary tool finalization unexpectedly exceeded its compact bound: ordinary=%d research=%d", len(ordinary), len(stored))
	}
}

func TestExportAccountRedactionCredentialMatrix(t *testing.T) {
	fixtures := []string{
		`bearer AbCdEfGhIjKlMnOpQrStUvWxYz012345+/=`,
		`{"api_key":"synthetic_api_key_1234567890"}`,
		`{"access_token":"synthetic/access+token=123456"}`,
		`{"refresh_token":"synthetic_refresh_token_123456"}`,
		`{"client_secret":"synthetic_client_secret_123456"}`,
		`{"password":"synthetic-password-123456"}`,
		`{"authorization":"Bearer synthetic.authorization.token"}`,
		`{"cookie":"session=synthetic-cookie-value"}`,
		`X-API-Key: synthetic-header-key-123456`,
		`token=synthetic_unquoted_token_123456`,
		`{"user_id":"customer-42"}`,
		`tenant_id=tenant-alpha`,
		`mobile=138-0013-8000`,
		`账号='demo-user'`,
		`账号=demo-user`,
		`-----BEGIN PRIVATE KEY-----\nSYNTHETICBASE64+/=\n-----END PRIVATE KEY-----`,
		`eyJhbGciOiJIUzI1NiJ9.c3ludGhldGljLXBheWxvYWQ.c3ludGhldGljLXNpZ25hdHVyZQ`,
	}
	for _, fixture := range fixtures {
		clean := string(redactAccountInfoForExport([]byte(fixture)))
		if clean == fixture || strings.Contains(clean, "synthetic") || strings.Contains(clean, "SYNTHETIC") {
			t.Errorf("credential fixture was not redacted\ninput: %s\noutput: %s", fixture, clean)
		}
		if repeated := string(redactAccountInfoForExport([]byte(clean))); repeated != clean {
			t.Errorf("export redaction is not idempotent\nfirst:  %s\nsecond: %s", clean, repeated)
		}
	}
}

func TestExportAccountRedactionDoesNotRedactOrdinaryMetadata(t *testing.T) {
	input := `{"token_count":1200,"password_policy":"minimum-16-characters","status":"ok"}`
	if got := string(redactAccountInfoForExport([]byte(input))); got != input {
		t.Fatalf("ordinary non-secret metadata was modified: %s", got)
	}
}

func TestSystemPromptUsesSpecificLiveToolAndFreezesPublishedContent(t *testing.T) {
	prompt := systemPromptStatic(false)
	for _, want := range []string{
		"social_trending",
		`source="weibo"`,
		"re-read the frozen source and final payload",
		"content hash",
		"closed evidence set",
		"never fill a missing field with a plausible value",
		"Local-first (non-live/project tasks)",
		"Do not call list_dir or notepad merely to restate a self-contained request",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
}
