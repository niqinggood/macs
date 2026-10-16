package app

import (
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDataAnalystWorkflowPlanIsProgressOnlyAndFinalResultReplacesLegacyPlan(t *testing.T) {
	gw := newAEPersistenceTestGateway(t)
	gw.pipelineExec = NewPipelineExecutor(nil, gw, log.New(io.Discard, "", 0))
	mgr := NewAIEmployeeManager(gw, log.New(io.Discard, "", 0))
	mgr.Register([]AIEmployeeConfig{{
		ID:       "data-analyst",
		Name:     "数据分析师",
		AtName:   "数据分析",
		Pipeline: "data-analysis-pipeline",
	}})
	gw.aeManager = mgr

	pptPipeline, ok := mgr.dataAnalystPresentationPipeline("data-analysis-pipeline", "")
	if !ok {
		t.Fatal("data analyst presentation pipeline should resolve")
	}
	gw.pipelineExec.RegisterDynamic(dataAnalysisPresentationPipelineName, pptPipeline)

	record := &AEWorkRecord{
		EmployeeID: "data-analyst",
		Task:       "diamonds.csv 整理成 ppt报告",
		Pipeline:   dataAnalysisPresentationPipelineName,
		ExecID:     "pipe_data_analysis_ppt_1",
		SessionID:  "ae_session_plan",
		Status:     "running",
	}
	plan := mgr.publishAIEmployeeWorkflowPlan(record)
	for _, want := range []string{"第一步将先理解意图", "权限边界", "交付标准", "执行流程", "数据加载", "输出报告", "PPT报告", "PPTX"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("workflow plan should contain %q, got %q", want, plan)
		}
	}

	var count int64
	gw.db.Model(&ChatMessage{}).Where("session_id = ? AND role = ?", record.SessionID, "assistant").Count(&count)
	if count != 0 {
		t.Fatalf("transient workflow plan leaked into chat history: %d", count)
	}
	legacy := ChatMessage{SessionID: record.SessionID, Role: "assistant", AgentName: "数据分析师", Content: plan, Status: "running", TaskID: "pipe_data_analysis_ppt_1-de"}
	if err := gw.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	record.Status = "completed"
	mgr.savePipelineResultToChat(record, "最终交付完成")
	if err := gw.db.First(&ChatMessage{}, legacy.ID).Error; err == nil {
		t.Fatal("legacy workflow plan was not deleted when final result arrived")
	}
	var final ChatMessage
	if err := gw.db.Where("session_id = ? AND role = ?", record.SessionID, "assistant").First(&final).Error; err != nil {
		t.Fatalf("final result not saved: %v", err)
	}
	if final.Content != "最终交付完成" || final.Status != "completed" {
		t.Fatalf("unexpected final result: %#v", final)
	}
}

func TestWorkflowPlanIsUniversalForNonDataEmployee(t *testing.T) {
	gw := newAEPersistenceTestGateway(t)
	gw.config = &Config{Pipeline: PipelineConfig{Pipelines: map[string]Pipeline{}}}
	gw.pipelineExec = NewPipelineExecutor(nil, gw, log.New(io.Discard, "", 0))
	pipeline := (&GatewayService{}).defaultPipelineSet()["wechat-article"]
	if err := gw.pipelineExec.RegisterDynamic("wechat-article", pipeline); err != nil {
		t.Fatalf("register pipeline: %v", err)
	}
	mgr := NewAIEmployeeManager(gw, log.New(io.Discard, "", 0))
	mgr.Register([]AIEmployeeConfig{{
		ID: "content-editor", Name: "微信公众号编辑", AtName: "微信公众号编辑", Pipeline: "wechat-article",
	}})
	gw.aeManager = mgr

	record := &AEWorkRecord{
		EmployeeID: "content-editor",
		Task:       "整理一份今日热点的公众号文章",
		Pipeline:   "wechat-article",
		ExecID:     "pipe_wechat_plan_1",
		SessionID:  "wechat_plan_session",
		Status:     "running",
	}
	plan := mgr.publishAIEmployeeWorkflowPlan(record)
	for _, want := range []string{"意图", "素材准备", "公众号交付物", "权限边界", "交付标准", "wechat_article_bundle"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("universal workflow plan missing %q: %s", want, plan)
		}
	}
	var count int64
	if err := gw.db.Model(&ChatMessage{}).Where("session_id = ? AND agent_name = ?", record.SessionID, "微信公众号编辑").Count(&count).Error; err != nil {
		t.Fatalf("count saved workflow plan: %v", err)
	}
	if count != 0 {
		t.Fatalf("workflow plan must be progress-only, saved count=%d", count)
	}
	// A repeated publication attempt must not duplicate the same running plan.
	mgr.publishAIEmployeeWorkflowPlan(record)
	if err := gw.db.Model(&ChatMessage{}).Where("session_id = ? AND agent_name = ?", record.SessionID, "微信公众号编辑").Count(&count).Error; err != nil {
		t.Fatalf("recount saved workflow plan: %v", err)
	}
	if count != 0 {
		t.Fatalf("workflow plans leaked into chat after repeated publish: %d", count)
	}
}

func TestSessionMessagesHideLegacyWorkflowPlanBubble(t *testing.T) {
	gw := newAEPersistenceTestGateway(t)
	rows := []ChatMessage{
		{SessionID: "legacy_plan_session", Role: "user", Content: "@投资顾问 分析下新安股份", Status: "degraded", TaskID: "task-de"},
		{SessionID: "legacy_plan_session", Role: "assistant", AgentName: "投资顾问", Content: "当前请求：分析下新安股份\n已接收任务。第一步将先理解意图、核对可用上下文和输入完整性，再决定是否启动后续业务步骤。\n填充槽位：task_brief", Status: "degraded", TaskID: "task-de"},
		{SessionID: "legacy_plan_session", Role: "assistant", AgentName: "投资顾问", Content: "最终投资分析结果", Status: "degraded", TaskID: "task-de"},
	}
	if err := gw.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/session/legacy_plan_session", nil)
	rr := httptest.NewRecorder()
	gw.handleSessionMessages(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var response struct {
		Messages []ChatMessage `json:"messages"`
		Count    int           `json:"count"`
		Total    int64         `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Count != 2 || response.Total != 2 {
		t.Fatalf("legacy plan was counted: %#v", response)
	}
	for _, message := range response.Messages {
		if strings.Contains(message.Content, "填充槽位") {
			t.Fatalf("legacy workflow plan leaked into session response: %s", message.Content)
		}
	}
}

func TestSessionMessagesStripLegacyAEPipelineDiagnosticPrefix(t *testing.T) {
	gw := newAEPersistenceTestGateway(t)
	sessionID := "web_legacy_degraded_delivery"
	legacy := "AI员工任务已产生阶段性结果，但存在降级/异常：\n- 分析 [degraded]: API返回错误状态码: 422 request_id=secret\n\n# 投资顾问报告\n\n## 综合结论\n可读交付"
	if err := gw.db.Create(&ChatMessage{SessionID: sessionID, Role: "assistant", Content: legacy}).Error; err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/session/"+sessionID, nil)
	gw.handleSessionMessages(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	var response struct {
		Messages []ChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Messages) != 1 {
		t.Fatalf("unexpected session messages: %s", body)
	}
	content := response.Messages[0].Content
	for _, forbidden := range []string{"422", "request_id", "secret", "阶段性结果"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("legacy diagnostic leaked from session response: %s", body)
		}
	}
	if !strings.Contains(content, "# 投资顾问报告") || !strings.Contains(content, "可读交付") {
		t.Fatalf("delivery content was lost: %s", body)
	}
}
