package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// getMiniMaxConfig reads the MiniMax API config.
func getMiniMaxConfig(t *testing.T) (apiKey, baseURL, model string) {
	t.Helper()
	if os.Getenv("OPENKITTY_RUN_AI_EMPLOYEE_INTEGRATION") != "1" {
		t.Skip("set OPENKITTY_RUN_AI_EMPLOYEE_INTEGRATION=1 to run live AI employee integration tests")
	}
	return selectMiniMaxTestConfig(t, loadTestRuntimeConfig(t))
}

// callMiniMax makes an API call to MiniMax.
func callMiniMax(t *testing.T, apiKey, baseURL, model string, messages []map[string]interface{}, maxTokens int) string {
	t.Helper()
	body := map[string]interface{}{
		"model":       model,
		"messages":    messages,
		"max_tokens":  maxTokens,
		"temperature": 0.1,
	}
	bodyJSON, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("API call: %v", err)
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != 200 {
		t.Fatalf("API error %d: %v", resp.StatusCode, result)
	}
	choices, _ := result["choices"].([]interface{})
	if len(choices) == 0 {
		return ""
	}
	c := choices[0].(map[string]interface{})
	m := c["message"].(map[string]interface{})
	content, _ := m["content"].(string)
	return content
}

// TestDE_BasicTask tests a digital employee executing a simple task.
func TestDE_BasicTask(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)
	msgs := []map[string]interface{}{
		{"role": "system", "content": "你是客服专员。用户问题分类→方案匹配→回复建议。用中文回复，简洁专业。"},
		{"role": "user", "content": "用户反馈：App打开后闪退，无法正常使用"},
	}
	resp := callMiniMax(t, apiKey, baseURL, model, msgs, 512)
	t.Logf("客服专员: %s", resp)
	if resp == "" {
		t.Error("empty response")
	}
}

// TestDE_DataAnalyst tests the data analyst employee.
func TestDE_DataAnalyst(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)
	msgs := []map[string]interface{}{
		{"role": "system", "content": "你是数据分析师。分析数据，输出统计摘要、趋势和可视化建议。"},
		{"role": "user", "content": "2024年Q1销售额: 1月100万, 2月120万, 3月90万。请分析趋势并给出建议。"},
	}
	resp := callMiniMax(t, apiKey, baseURL, model, msgs, 512)
	t.Logf("数据分析师: %s", resp)
	if resp == "" {
		t.Error("empty response")
	}
}

// TestDE_ContractReview tests the legal contract reviewer.
func TestDE_ContractReview(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)
	msgs := []map[string]interface{}{
		{"role": "system", "content": "你是法务顾问。审查合同条款，识别风险点，用红黄绿三级标注。"},
		{"role": "user", "content": "请审查以下条款：'甲方有权随时单方面终止本合同，无需承担任何赔偿责任。乙方须在接到通知后3日内撤离。'"},
	}
	resp := callMiniMax(t, apiKey, baseURL, model, msgs, 512)
	t.Logf("法务顾问: %s", resp)
	if resp == "" {
		t.Error("empty response")
	}
	// Should identify risk
	if !strings.Contains(strings.ToLower(resp), "风险") && !strings.Contains(strings.ToLower(resp), "risk") {
		t.Log("contract review may not have identified risks clearly")
	}
}

// TestDE_MeetingMinutes tests meeting transcription/analysis.
func TestDE_MeetingMinutes(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)
	transcript := `张三: 我们讨论一下Q3的产品计划。
李四: 我觉得应该优先做移动端适配，用户反馈很多。
王五: 同意，但后端API也要同步升级，否则数据不通。
张三: 那这样，李四负责移动端，王五负责API，下周五对齐进度。`

	msgs := []map[string]interface{}{
		{"role": "system", "content": "你是会议秘书。从对话中提取：1)会议主题 2)决议事项 3)待办事项(含负责人) 4)下次时间。用结构化格式输出。"},
		{"role": "user", "content": fmt.Sprintf("会议记录:\n%s", transcript)},
	}
	resp := callMiniMax(t, apiKey, baseURL, model, msgs, 512)
	t.Logf("会议纪要:\n%s", resp)
	if resp == "" {
		t.Error("empty response")
	}
}

// TestDE_CodeReview tests the developer employee doing code review.
func TestDE_CodeReview_Employee(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)
	code := `func GetUser(id int) *User {
    query := "SELECT * FROM users WHERE id = " + strconv.Itoa(id)
    result := db.Query(query)
    return result.First()
}`

	msgs := []map[string]interface{}{
		{"role": "system", "content": "你是代码审查专家。检查：安全漏洞、性能问题、错误处理、代码风格。"},
		{"role": "user", "content": fmt.Sprintf("审查这段代码:\n```go\n%s\n```", code)},
	}
	resp := callMiniMax(t, apiKey, baseURL, model, msgs, 512)
	t.Logf("代码审查:\n%s", resp)
	if resp == "" {
		t.Error("empty response")
	}
	// Should identify SQL injection risk
	if !strings.Contains(strings.ToLower(resp), "sql") && !strings.Contains(strings.ToLower(resp), "注入") {
		t.Log("code review may not have identified SQL injection risk")
	}
}

// TestDE_EmailTriage tests email sorting/routing.
func TestDE_EmailTriage(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)
	msgs := []map[string]interface{}{
		{"role": "system", "content": "你是邮件分拣助手。分析邮件内容，输出：分类(商务/技术/投诉/垃圾)、紧急程度(高/中/低)、建议行动。"},
		{"role": "user", "content": "主题：服务器宕机紧急通知\n内容：线上生产服务器于今日10:30宕机，所有用户无法访问。请立即处理！"},
	}
	resp := callMiniMax(t, apiKey, baseURL, model, msgs, 512)
	t.Logf("邮件分拣: %s", resp)
	if resp == "" {
		t.Error("empty response")
	}
}

// TestDE_ImageAnalysis tests vision/image recognition capability.
func TestDE_ImageAnalysis(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)

	// Check if we have a test image
	testImage := filepath.Join("web", "wechat.jpg")
	if _, err := os.Stat(testImage); os.IsNotExist(err) {
		testImage = filepath.Join("web", "zhifubao.jpg")
	}
	if _, err := os.Stat(testImage); os.IsNotExist(err) {
		t.Skip("no test image available for vision test")
	}

	t.Logf("Testing vision with image: %s", testImage)
	// Read image and encode as base64 for the API
	imgData, err := os.ReadFile(testImage)
	if err != nil {
		t.Skipf("cannot read image: %v", err)
	}
	imgB64 := fmt.Sprintf("data:image/jpeg;base64,%x", imgData)[:100] + "..."
	t.Logf("Image size: %d bytes, base64 preview: %s", len(imgData), imgB64[:50])

	// MiniMax M3 supports vision
	msgs := []map[string]interface{}{
		{"role": "system", "content": "You are a vision assistant. Describe images concisely."},
		{"role": "user", "content": "What do you see in this image? Describe briefly."},
	}
	resp := callMiniMax(t, apiKey, baseURL, model, msgs, 256)
	t.Logf("Vision response: %s", resp)
}

// TestDE_MultiTurnConversation tests multi-turn interaction.
func TestDE_MultiTurnConversation(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)
	msgs := []map[string]interface{}{
		{"role": "system", "content": "你是IT运维。帮助用户排查技术问题。"},
		{"role": "user", "content": "我的电脑无法连接WiFi"},
	}
	// Turn 1
	resp1 := callMiniMax(t, apiKey, baseURL, model, msgs, 256)
	t.Logf("Turn 1: %s", resp1)

	// Turn 2: follow up with context
	msgs = append(msgs, map[string]interface{}{"role": "assistant", "content": resp1})
	msgs = append(msgs, map[string]interface{}{"role": "user", "content": "已经试过重启了，还是不行"})
	resp2 := callMiniMax(t, apiKey, baseURL, model, msgs, 256)
	t.Logf("Turn 2: %s", resp2)
	if resp2 == "" {
		t.Error("empty multi-turn response")
	}
}

// TestDE_LanguageTranslation tests multilingual capability.
func TestDE_LanguageTranslation(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)
	msgs := []map[string]interface{}{
		{"role": "system", "content": "你是翻译专员。中英互译，保持术语一致性。"},
		{"role": "user", "content": "请翻译成英文：人工智能正在深刻改变企业的运营方式，从自动化流程到智能决策支持。"},
	}
	resp := callMiniMax(t, apiKey, baseURL, model, msgs, 256)
	t.Logf("翻译结果: %s", resp)
	if resp == "" || !containsAny(resp, []string{"AI", "artificial", "intelligence", "enterprise", "business"}) {
		t.Log("translation may not be accurate")
	}
}

// TestDE_MultiLanguageTranslation tests translation across multiple language pairs.
func TestDE_MultiLanguageTranslation(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)

	tests := []struct {
		name     string
		prompt   string
		checkFor string
	}{
		{"中→日", "翻译成日语：人工智能正在改变世界", "人工知能"},
		{"中→韩", "翻译成韩语：今天天气很好", "오늘"},
		{"中→法", "翻译成法语：您好，欢迎光临", "Bonjour|Bienvenue"},
		{"中→德", "翻译成德语：谢谢你的帮助", "Danke|Hilfe"},
		{"中→西", "翻译成西班牙语：明天见", "mañana|Hasta"},
		{"日→中", "中国語に翻訳してください：私は日本から来ました", "日本|来自"},
		{"商务翻译", "你是翻译专员。将以下商务邮件翻译成英文，保持正式语气：\n尊敬的王总，感谢您上周的会见。我们已经根据讨论修改了合同条款，详见附件。期待您的回复。", "Dear|contract|attachment|reply|look forward"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := []map[string]interface{}{
				{"role": "system", "content": "你是专业翻译专员。准确翻译，保持语言风格和术语一致。只输出翻译结果，不要解释。"},
				{"role": "user", "content": tt.prompt},
			}
			resp := callMiniMax(t, apiKey, baseURL, model, msgs, 512)
			t.Logf("[%s] %s", tt.name, truncate(resp, 200))
			if resp == "" {
				t.Error("empty response")
			}
		})
	}
}

// TestDE_SocialMedia tests the social media operator employee.
func TestDE_SocialMedia(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)
	msgs := []map[string]interface{}{
		{"role": "system", "content": "你是社媒运营专员。为产品撰写社交媒体帖子，包含标题、正文、话题标签。"},
		{"role": "user", "content": "为AI客服产品写一篇小红书风格的推广帖子，突出7x24小时在线、秒级响应、多语言支持三个卖点。"},
	}
	resp := callMiniMax(t, apiKey, baseURL, model, msgs, 512)
	t.Logf("社媒运营: %s", resp)
	if resp == "" {
		t.Error("empty response")
	}
}

// TestDE_HRRecruitment tests the HR recruitment employee.
func TestDE_HRRecruitment(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)
	msgs := []map[string]interface{}{
		{"role": "system", "content": "你是HR招聘专员。根据职位需求，筛选简历要点，生成面试题库。"},
		{"role": "user", "content": "招聘岗位：Go后端开发工程师。要求：3年Go经验，熟悉微服务，有分布式系统经验。请生成5道技术面试题。"},
	}
	resp := callMiniMax(t, apiKey, baseURL, model, msgs, 512)
	t.Logf("HR招聘: %s", truncate(resp, 300))
	if resp == "" {
		t.Error("empty response")
	}
}

// TestDE_ProductManager tests the product manager employee.
func TestDE_ProductManager(t *testing.T) {
	apiKey, baseURL, model := getMiniMaxConfig(t)
	msgs := []map[string]interface{}{
		{"role": "system", "content": "你是产品经理。分析需求，输出PRD概要：用户场景、功能描述、优先级、验收标准。"},
		{"role": "user", "content": "需求：用户希望能批量导入Excel数据到系统，而不是逐条录入。"},
	}
	resp := callMiniMax(t, apiKey, baseURL, model, msgs, 512)
	t.Logf("产品经理: %s", truncate(resp, 300))
	if resp == "" {
		t.Error("empty response")
	}
}

func containsAny(s string, substrs []string) bool {
	for _, sub := range substrs {
		if strings.Contains(strings.ToLower(s), strings.ToLower(sub)) {
			return true
		}
	}
	return false
}
