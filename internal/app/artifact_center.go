package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mac/internal/agentloop"
	weatherartifact "mac/internal/artifacts/weather"
	"mac/internal/browserruntime"
	"mac/internal/permissionpolicy"
	"mac/internal/portablepath"
	"mac/internal/projectctx"
	routingcore "mac/internal/routing"
	securitycore "mac/internal/security"
	"mac/internal/textutil"
	"mac/internal/toolregistry"
	contenttools "mac/internal/tools/content"
	desktoptools "mac/internal/tools/desktop"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type ArtifactCenterItem struct {
	Path             string    `json:"path"`
	Name             string    `json:"name"`
	Type             string    `json:"type"`
	TaskKind         string    `json:"task_kind,omitempty"`
	DownloadURL      string    `json:"download_url"`
	SourceStep       string    `json:"source_step,omitempty"`
	ExecID           string    `json:"exec_id"`
	BrowserTaskID    string    `json:"browser_task_id,omitempty"`
	DesktopTaskID    string    `json:"desktop_task_id,omitempty"`
	PipelineName     string    `json:"pipeline_name"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	Size             int64     `json:"size,omitempty"`
	ModTime          time.Time `json:"mod_time,omitempty"`
	EstimatedCredits int       `json:"estimated_credits,omitempty"`
	EvidenceLevel    string    `json:"evidence_level,omitempty"`
	SourceCount      int       `json:"source_count,omitempty"`
	RiskLevel        string    `json:"risk_level,omitempty"`
}

type ArtifactCenterSummary struct {
	TotalArtifacts   int            `json:"total_artifacts"`
	Executions       int            `json:"executions"`
	ByType           map[string]int `json:"by_type"`
	EstimatedCredits int            `json:"estimated_credits"`
}

type DeliverySummary struct {
	ExecID             string               `json:"exec_id"`
	PipelineName       string               `json:"pipeline_name"`
	Status             string               `json:"status"`
	Title              string               `json:"title"`
	CreatedAt          time.Time            `json:"created_at"`
	DurationMs         int64                `json:"duration_ms"`
	ProgressPercent    int                  `json:"progress_percent"`
	FinalStepName      string               `json:"final_step_name,omitempty"`
	FinalOutputExcerpt string               `json:"final_output_excerpt,omitempty"`
	Artifacts          []ArtifactCenterItem `json:"artifacts,omitempty"`
	ArtifactCount      int                  `json:"artifact_count"`
	EstimatedCredits   int                  `json:"estimated_credits"`
	CreditBreakdown    []string             `json:"credit_breakdown,omitempty"`
	EvidenceLevel      string               `json:"evidence_level,omitempty"`
	SourceCount        int                  `json:"source_count,omitempty"`
	PrimarySourceCount int                  `json:"primary_source_count,omitempty"`
	RiskLevel          string               `json:"risk_level"`
	Risks              []string             `json:"risks,omitempty"`
	NextActions        []string             `json:"next_actions,omitempty"`
	Steps              []DeliveryStepInfo   `json:"steps,omitempty"`
	ModelRoutes        []ModelRouteInfo     `json:"model_routes,omitempty"`
	Markdown           string               `json:"markdown,omitempty"`
}

type DeliveryStepInfo struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	ToolCalls  int    `json:"tool_calls"`
}

func (g *GatewayService) handleArtifacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	items, summary := g.buildArtifactCenter(limit)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"artifacts": items,
		"summary":   summary,
	})
}

func (g *GatewayService) handleDeliveries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if g == nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"deliveries": []DeliverySummary{}, "summary": map[string]int{}})
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	deliveries := make([]*DeliverySummary, 0)
	statusCounts := map[string]int{}
	totalCredits := 0
	totalArtifacts := 0
	if g.pipelineExec != nil {
		execs := g.pipelineExec.ListExecutions(limit)
		for _, exec := range execs {
			summary := g.buildDeliverySummary(exec)
			if summary == nil {
				continue
			}
			summary.Markdown = ""
			deliveries = append(deliveries, summary)
			statusCounts[summary.Status]++
			totalCredits += summary.EstimatedCredits
			totalArtifacts += summary.ArtifactCount
		}
	}
	for _, task := range globalBrowserTaskManager.List(limit) {
		summary := g.buildBrowserTaskDeliverySummary(task)
		if summary == nil {
			continue
		}
		summary.Markdown = ""
		deliveries = append(deliveries, summary)
		statusCounts[summary.Status]++
		totalCredits += summary.EstimatedCredits
		totalArtifacts += summary.ArtifactCount
	}
	for _, task := range globalDesktopTaskManager.List(limit) {
		summary := g.buildDesktopTaskDeliverySummary(task)
		if summary == nil {
			continue
		}
		summary.Markdown = ""
		deliveries = append(deliveries, summary)
		statusCounts[summary.Status]++
		totalCredits += summary.EstimatedCredits
		totalArtifacts += summary.ArtifactCount
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"deliveries": deliveries,
		"summary": map[string]interface{}{
			"executions":        len(deliveries),
			"by_status":         statusCounts,
			"estimated_credits": totalCredits,
			"total_artifacts":   totalArtifacts,
		},
	})
}

func (g *GatewayService) handleDeliverySummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	execID := strings.TrimSpace(r.URL.Query().Get("exec_id"))
	if execID == "" {
		execID = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	if execID == "" {
		http.Error(w, `{"error":"exec_id required"}`, http.StatusBadRequest)
		return
	}
	if g == nil {
		http.Error(w, `{"error":"pipeline executor not ready"}`, http.StatusServiceUnavailable)
		return
	}
	if g.pipelineExec != nil {
		if exec, ok := g.pipelineExec.GetExecutionByID(execID); ok && exec != nil {
			summary := g.buildDeliverySummary(exec)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"delivery": summary})
			return
		}
	}
	if task, ok := globalBrowserTaskManager.Get(execID); ok && task != nil {
		summary := g.buildBrowserTaskDeliverySummary(task)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"delivery": summary})
		return
	}
	if task, ok := globalDesktopTaskManager.Get(execID); ok && task != nil {
		summary := g.buildDesktopTaskDeliverySummary(task)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"delivery": summary})
		return
	}
	http.Error(w, `{"error":"execution not found"}`, http.StatusNotFound)
}

func (g *GatewayService) buildArtifactCenter(limit int) ([]ArtifactCenterItem, ArtifactCenterSummary) {
	summary := ArtifactCenterSummary{ByType: map[string]int{}}
	if g == nil {
		return nil, summary
	}
	items := make([]ArtifactCenterItem, 0)
	seen := map[string]bool{}
	if g.pipelineExec != nil {
		execs := g.pipelineExec.ListExecutions(limit)
		summary.Executions += len(execs)
		for _, exec := range execs {
			if exec == nil {
				continue
			}
			view := NewPipelineExecutionView(exec)
			if view == nil || len(view.Artifacts) == 0 {
				continue
			}
			credits := 0
			risk := ""
			evidenceLevel := ""
			sourceCount := 0
			if view.Delivery != nil {
				credits = view.Delivery.EstimatedCredits
				risk = view.Delivery.RiskLevel
				if view.Delivery.Evidence != nil {
					evidenceLevel = view.Delivery.Evidence.Level
					sourceCount = view.Delivery.Evidence.SourceCount
				}
			}
			for _, artifact := range view.Artifacts {
				if artifact.Path == "" {
					continue
				}
				key := exec.ID + "|" + artifact.Path
				if seen[key] {
					continue
				}
				seen[key] = true
				item := ArtifactCenterItem{
					Path:             artifact.Path,
					Name:             filepath.Base(filepath.ToSlash(artifact.Path)),
					Type:             classifyArtifactType(artifact.Path),
					TaskKind:         "pipeline",
					DownloadURL:      artifact.DownloadURL,
					SourceStep:       artifact.SourceStep,
					ExecID:           exec.ID,
					PipelineName:     exec.PipelineName,
					Status:           exec.Status,
					CreatedAt:        exec.CreatedAt,
					EstimatedCredits: credits,
					EvidenceLevel:    evidenceLevel,
					SourceCount:      sourceCount,
					RiskLevel:        risk,
				}
				if info, ok := g.statArtifactPath(artifact.Path); ok {
					item.Size = info.Size()
					item.ModTime = info.ModTime()
				}
				items = append(items, item)
				summary.TotalArtifacts++
				summary.ByType[item.Type]++
				if credits > 0 {
					summary.EstimatedCredits += credits
				}
			}
		}
	}
	browserTasks := globalBrowserTaskManager.List(limit)
	browserItems := g.browserTaskArtifactItems(browserTasks, seen)
	summary.Executions += len(browserTasks)
	for _, item := range browserItems {
		items = append(items, item)
		summary.TotalArtifacts++
		summary.ByType[item.Type]++
		if item.EstimatedCredits > 0 {
			summary.EstimatedCredits += item.EstimatedCredits
		}
	}
	desktopTasks := globalDesktopTaskManager.List(limit)
	desktopItems := g.desktopTaskArtifactItems(desktopTasks, seen)
	summary.Executions += len(desktopTasks)
	for _, item := range desktopItems {
		items = append(items, item)
		summary.TotalArtifacts++
		summary.ByType[item.Type]++
		if item.EstimatedCredits > 0 {
			summary.EstimatedCredits += item.EstimatedCredits
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i].CreatedAt
		right := items[j].CreatedAt
		if left.Equal(right) {
			return items[i].Path < items[j].Path
		}
		return left.After(right)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, summary
}

func (g *GatewayService) desktopTaskArtifactItems(tasks []*desktoptools.DesktopTaskRecord, seen map[string]bool) []ArtifactCenterItem {
	if seen == nil {
		seen = map[string]bool{}
	}
	items := make([]ArtifactCenterItem, 0)
	for _, task := range tasks {
		if task == nil || strings.TrimSpace(task.ScreenshotPath) == "" {
			continue
		}
		key := task.ID + "|" + task.ScreenshotPath
		if seen[key] {
			continue
		}
		seen[key] = true
		item := ArtifactCenterItem{
			Path:             task.ScreenshotPath,
			Name:             filepath.Base(filepath.ToSlash(task.ScreenshotPath)),
			Type:             classifyArtifactType(task.ScreenshotPath),
			TaskKind:         "desktop",
			DownloadURL:      task.ScreenshotURL,
			SourceStep:       "screenshot",
			ExecID:           task.ID,
			DesktopTaskID:    task.ID,
			PipelineName:     "desktop-task",
			Status:           string(task.Status),
			CreatedAt:        task.StartedAt,
			EstimatedCredits: task.EstimatedCredit,
			RiskLevel:        desktopTaskRiskLevel(task),
		}
		if info, ok := g.statArtifactPath(task.ScreenshotPath); ok {
			item.Size = info.Size()
			item.ModTime = info.ModTime()
		}
		items = append(items, item)
	}
	return items
}

func (g *GatewayService) browserTaskArtifactItems(tasks []*browserruntime.BrowserTaskRecord, seen map[string]bool) []ArtifactCenterItem {
	if seen == nil {
		seen = map[string]bool{}
	}
	items := make([]ArtifactCenterItem, 0)
	for _, task := range tasks {
		if task == nil {
			continue
		}
		for _, artifact := range []struct {
			path string
			url  string
			step string
		}{
			{path: task.ScreenshotPath, url: task.ScreenshotURL, step: "screenshot"},
			{path: task.ReplayPath, url: task.ReplayURL, step: "replay"},
		} {
			if strings.TrimSpace(artifact.path) == "" {
				continue
			}
			key := task.ID + "|" + artifact.path
			if seen[key] {
				continue
			}
			seen[key] = true
			item := ArtifactCenterItem{
				Path:             artifact.path,
				Name:             filepath.Base(filepath.ToSlash(artifact.path)),
				Type:             classifyArtifactType(artifact.path),
				TaskKind:         "browser",
				DownloadURL:      artifact.url,
				SourceStep:       artifact.step,
				ExecID:           task.ID,
				BrowserTaskID:    task.ID,
				PipelineName:     "browser-task",
				Status:           string(task.Status),
				CreatedAt:        task.StartedAt,
				EstimatedCredits: task.EstimatedCredit,
				RiskLevel:        browserTaskRiskLevel(task),
			}
			if info, ok := g.statArtifactPath(artifact.path); ok {
				item.Size = info.Size()
				item.ModTime = info.ModTime()
			}
			items = append(items, item)
		}
	}
	return items
}

func (g *GatewayService) buildBrowserTaskDeliverySummary(task *browserruntime.BrowserTaskRecord) *DeliverySummary {
	if task == nil {
		return nil
	}
	artifacts := g.browserTaskArtifactItems([]*browserruntime.BrowserTaskRecord{task}, nil)
	risk := browserTaskRiskLevel(task)
	summary := &DeliverySummary{
		ExecID:             task.ID,
		PipelineName:       "browser-task",
		Status:             string(task.Status),
		Title:              "browser task delivery",
		CreatedAt:          task.StartedAt,
		DurationMs:         task.DurationMs,
		ProgressPercent:    browserTaskProgress(task),
		FinalStepName:      "browser_task",
		FinalOutputExcerpt: truncate(strings.TrimSpace(task.ExtractExcerpt), 800),
		Artifacts:          artifacts,
		ArtifactCount:      len(artifacts),
		EstimatedCredits:   task.EstimatedCredit,
		CreditBreakdown:    []string{fmt.Sprintf("%d browser operation step(s)", len(task.Steps))},
		RiskLevel:          risk,
		NextActions:        append([]string(nil), task.NextActions...),
	}
	if task.Error != "" {
		summary.Risks = append(summary.Risks, task.Error)
	}
	for _, step := range task.Steps {
		summary.Steps = append(summary.Steps, DeliveryStepInfo{
			Name:       step.Name,
			Status:     step.Status,
			DurationMs: step.DurationMs,
			Error:      step.Error,
			ToolCalls:  1,
		})
		if step.Error != "" && step.Error != task.Error {
			summary.Risks = append(summary.Risks, step.Name+": "+step.Error)
		}
	}
	if len(summary.NextActions) == 0 {
		summary.NextActions = browserruntime.BrowserTaskNextActions(task)
	}
	summary.Markdown = browserruntime.FormatBrowserTaskMarkdown(task)
	return summary
}

func browserTaskRiskLevel(task *browserruntime.BrowserTaskRecord) string {
	if task == nil {
		return "medium"
	}
	switch task.Status {
	case browserruntime.BrowserTaskFailed:
		return "high"
	case browserruntime.BrowserTaskRunning:
		return "medium"
	default:
		if task.Error != "" {
			return "medium"
		}
		return "low"
	}
}

func browserTaskProgress(task *browserruntime.BrowserTaskRecord) int {
	if task == nil {
		return 0
	}
	switch task.Status {
	case browserruntime.BrowserTaskCompleted:
		return 100
	case browserruntime.BrowserTaskFailed:
		if len(task.Steps) == 0 {
			return 0
		}
		return 50
	case browserruntime.BrowserTaskRunning:
		if len(task.Steps) == 0 {
			return 10
		}
		return 60
	default:
		return 0
	}
}

func (g *GatewayService) buildDesktopTaskDeliverySummary(task *desktoptools.DesktopTaskRecord) *DeliverySummary {
	if task == nil {
		return nil
	}
	artifacts := g.desktopTaskArtifactItems([]*desktoptools.DesktopTaskRecord{task}, nil)
	risk := desktopTaskRiskLevel(task)
	summary := &DeliverySummary{
		ExecID:             task.ID,
		PipelineName:       "desktop-task",
		Status:             string(task.Status),
		Title:              "desktop task delivery",
		CreatedAt:          task.StartedAt,
		DurationMs:         task.DurationMs,
		ProgressPercent:    desktopTaskProgress(task),
		FinalStepName:      "desktop_task",
		FinalOutputExcerpt: desktopTaskFinalExcerpt(task),
		Artifacts:          artifacts,
		ArtifactCount:      len(artifacts),
		EstimatedCredits:   task.EstimatedCredit,
		CreditBreakdown:    []string{fmt.Sprintf("%d desktop operation step(s)", len(task.Steps))},
		RiskLevel:          risk,
		Risks:              append([]string(nil), task.Risks...),
		NextActions:        append([]string(nil), task.NextActions...),
	}
	if task.Error != "" {
		summary.Risks = append(summary.Risks, task.Error)
	}
	for _, step := range task.Steps {
		summary.Steps = append(summary.Steps, DeliveryStepInfo{
			Name:       step.Name,
			Status:     step.Status,
			DurationMs: step.DurationMs,
			Error:      step.Error,
			ToolCalls:  1,
		})
		if step.Error != "" && step.Error != task.Error {
			summary.Risks = append(summary.Risks, step.Name+": "+step.Error)
		}
	}
	if len(summary.NextActions) == 0 {
		summary.NextActions = desktoptools.NextActions(task)
	}
	summary.Markdown = desktoptools.FormatMarkdown(task)
	return summary
}

func desktopTaskRiskLevel(task *desktoptools.DesktopTaskRecord) string {
	if task == nil {
		return "medium"
	}
	switch task.Status {
	case desktoptools.DesktopTaskFailed:
		return "high"
	case desktoptools.DesktopTaskRunning:
		return "medium"
	default:
		if len(task.Risks) > 0 || task.Error != "" {
			return "medium"
		}
		return "low"
	}
}

func desktopTaskProgress(task *desktoptools.DesktopTaskRecord) int {
	if task == nil {
		return 0
	}
	switch task.Status {
	case desktoptools.DesktopTaskCompleted:
		return 100
	case desktoptools.DesktopTaskFailed:
		if len(task.Steps) == 0 {
			return 0
		}
		return 50
	case desktoptools.DesktopTaskRunning:
		if len(task.Steps) == 0 {
			return 10
		}
		return 60
	default:
		return 0
	}
}

func desktopTaskFinalExcerpt(task *desktoptools.DesktopTaskRecord) string {
	if task == nil {
		return ""
	}
	if len(task.Steps) == 0 {
		return ""
	}
	return truncate(strings.TrimSpace(task.Steps[len(task.Steps)-1].Output), 800)
}

func (g *GatewayService) buildDeliverySummary(exec *PipelineExecution) *DeliverySummary {
	if exec == nil {
		return nil
	}
	view := NewPipelineExecutionView(exec)
	if view == nil || view.Delivery == nil {
		return nil
	}
	delivery := view.Delivery
	exec.mu.Lock()
	steps := append([]StepResult(nil), exec.Steps...)
	createdAt := exec.CreatedAt
	status := exec.Status
	pipelineName := exec.PipelineName
	execID := exec.ID
	exec.mu.Unlock()

	summary := &DeliverySummary{
		ExecID:             execID,
		PipelineName:       pipelineName,
		Status:             status,
		Title:              deliveryTitle(pipelineName, execID),
		CreatedAt:          createdAt,
		DurationMs:         delivery.DurationMs,
		FinalStepName:      view.FinalStepName,
		FinalOutputExcerpt: truncate(strings.TrimSpace(view.FinalOutput), 800),
		Artifacts:          g.artifactItemsForExecution(exec, view.Artifacts, delivery),
		EstimatedCredits:   delivery.EstimatedCredits,
		CreditBreakdown:    append([]string(nil), delivery.CreditBreakdown...),
		RiskLevel:          delivery.RiskLevel,
		Risks:              append([]string(nil), delivery.Risks...),
		NextActions:        append([]string(nil), delivery.NextActions...),
	}
	summary.ArtifactCount = len(summary.Artifacts)
	if delivery.TotalSteps > 0 {
		summary.ProgressPercent = int(float64(delivery.CompletedSteps) / float64(delivery.TotalSteps) * 100)
		if summary.ProgressPercent > 100 {
			summary.ProgressPercent = 100
		}
	}
	if status == "completed" {
		summary.ProgressPercent = 100
	}
	if delivery.Evidence != nil {
		summary.EvidenceLevel = delivery.Evidence.Level
		summary.SourceCount = delivery.Evidence.SourceCount
		summary.PrimarySourceCount = delivery.Evidence.PrimarySourceCount
	}
	for _, step := range steps {
		summary.Steps = append(summary.Steps, DeliveryStepInfo{
			Name:       step.Name,
			Status:     step.Status,
			DurationMs: step.DurationMs,
			Error:      truncate(strings.TrimSpace(step.Error), 240),
			ToolCalls:  len(step.ToolCalls),
		})
	}
	if globalProviderRegistry != nil {
		summary.ModelRoutes = append(summary.ModelRoutes,
			globalProviderRegistry.ExplainResolveForAE("", "text-to-text"),
			globalProviderRegistry.ExplainResolveForAE("", "multi-modal"),
			globalProviderRegistry.ExplainResolveForAE("", "text-to-image"),
		)
	}
	summary.Markdown = formatDeliverySummaryMarkdown(summary)
	return summary
}

func (g *GatewayService) artifactItemsForExecution(exec *PipelineExecution, artifacts []PipelineArtifact, delivery *PipelineDelivery) []ArtifactCenterItem {
	if exec == nil || len(artifacts) == 0 {
		return nil
	}
	credits := 0
	risk := ""
	evidenceLevel := ""
	sourceCount := 0
	if delivery != nil {
		credits = delivery.EstimatedCredits
		risk = delivery.RiskLevel
		if delivery.Evidence != nil {
			evidenceLevel = delivery.Evidence.Level
			sourceCount = delivery.Evidence.SourceCount
		}
	}
	items := make([]ArtifactCenterItem, 0, len(artifacts))
	seen := map[string]bool{}
	for _, artifact := range artifacts {
		if artifact.Path == "" || seen[artifact.Path] {
			continue
		}
		seen[artifact.Path] = true
		item := ArtifactCenterItem{
			Path:             artifact.Path,
			Name:             filepath.Base(filepath.ToSlash(artifact.Path)),
			Type:             classifyArtifactType(artifact.Path),
			DownloadURL:      artifact.DownloadURL,
			SourceStep:       artifact.SourceStep,
			ExecID:           exec.ID,
			PipelineName:     exec.PipelineName,
			Status:           exec.Status,
			CreatedAt:        exec.CreatedAt,
			EstimatedCredits: credits,
			EvidenceLevel:    evidenceLevel,
			SourceCount:      sourceCount,
			RiskLevel:        risk,
			Size:             artifact.Size,
		}
		if info, ok := g.statArtifactPath(artifact.Path); ok {
			item.Size = info.Size()
			item.ModTime = info.ModTime()
		}
		items = append(items, item)
	}
	return items
}

func deliveryTitle(pipelineName, execID string) string {
	pipelineName = strings.TrimSpace(pipelineName)
	if pipelineName != "" {
		return pipelineName + " delivery"
	}
	if execID != "" {
		return execID + " delivery"
	}
	return "delivery"
}

func formatDeliverySummaryMarkdown(summary *DeliverySummary) string {
	if summary == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Delivery summary\n\n")
	b.WriteString("- Execution: " + summary.ExecID + "\n")
	b.WriteString("- Pipeline: " + emptyDash(summary.PipelineName) + "\n")
	b.WriteString("- Status: " + emptyDash(summary.Status) + "\n")
	b.WriteString("- Progress: " + strconv.Itoa(summary.ProgressPercent) + "%\n")
	b.WriteString("- Estimated credits: " + strconv.Itoa(summary.EstimatedCredits) + "\n")
	if summary.RiskLevel != "" {
		b.WriteString("- Risk: " + summary.RiskLevel + "\n")
	}
	if summary.EvidenceLevel != "" {
		b.WriteString("- Evidence: " + summary.EvidenceLevel + " (" + strconv.Itoa(summary.SourceCount) + " sources)\n")
	}
	if summary.FinalOutputExcerpt != "" {
		b.WriteString("\n### Final output\n\n")
		b.WriteString(summary.FinalOutputExcerpt + "\n")
	}
	b.WriteString("\n### Artifacts\n\n")
	if len(summary.Artifacts) == 0 {
		b.WriteString("- No explicit file artifact recorded.\n")
	} else {
		for _, artifact := range summary.Artifacts {
			b.WriteString("- [" + artifact.Name + "](" + artifact.DownloadURL + ")")
			if artifact.Type != "" {
				b.WriteString(" - " + artifact.Type)
			}
			if artifact.SourceStep != "" {
				b.WriteString(" from " + artifact.SourceStep)
			}
			b.WriteString("\n")
		}
	}
	if len(summary.Risks) > 0 {
		b.WriteString("\n### Risks\n\n")
		for _, risk := range summary.Risks {
			b.WriteString("- " + risk + "\n")
		}
	}
	if len(summary.NextActions) > 0 {
		b.WriteString("\n### Next actions\n\n")
		for _, action := range summary.NextActions {
			b.WriteString("- " + action + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func emptyDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func (g *GatewayService) statArtifactPath(path string) (os.FileInfo, bool) {
	baseDir := "."
	if g != nil && g.config != nil && strings.TrimSpace(g.config.Workspace) != "" {
		baseDir = g.config.Workspace
	}
	clean := filepath.Clean(strings.TrimLeft(path, `/\`))
	full := filepath.Join(baseDir, clean)
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return nil, false
	}
	return info, true
}

func classifyArtifactType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".txt", ".pdf", ".doc", ".docx":
		return "report"
	case ".ppt", ".pptx", ".key":
		return "slides"
	case ".html", ".htm", ".css", ".js", ".tsx", ".jsx":
		return "web"
	case ".go", ".py", ".ts", ".java", ".rs", ".cpp", ".c", ".cs", ".php", ".rb":
		return "code"
	case ".csv", ".tsv", ".xlsx", ".xls", ".json", ".parquet":
		return "data"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg":
		return "image"
	case ".log":
		return "log"
	default:
		return "file"
	}
}

type requestedTextArtifactTurnContract struct {
	DirectiveInput string
	PrimaryPath    string
	Paths          []string
	RequiredFields []string
}

// requestedTextArtifactContractForTurn derives file-delivery obligations only
// from the frozen user-authored request. Pipeline prompts and intake output may
// describe a hypothetical write_file path, but generated orchestration text is
// not permission or intent to create that artifact.
func requestedTextArtifactContractForTurn(ctx context.Context, generatedInput string) requestedTextArtifactTurnContract {
	directiveInput := permissionpolicy.UserToolPolicyInput(ctx, generatedInput)
	contract := requestedTextArtifactTurnContract{
		DirectiveInput: directiveInput,
		PrimaryPath:    requestedTextArtifactPath(directiveInput),
		Paths:          requestedTextArtifactPaths(directiveInput),
		RequiredFields: requestedTextArtifactRequiredFields(directiveInput),
	}
	// A QA/audit Markdown file requested alongside a real PPTX is a companion
	// deliverable, not the primary synthesis target. Selecting it here causes the
	// generic text fast path to suppress presentation tools and ask the model to
	// write an acceptance report before the deck exists. Keep the path in Paths
	// so completion still requires its receipt, but let the presentation route
	// build, render, and inspect the PPTX before authoring that report.
	if presentationProductionGateAvailable(ctx) && isPresentationQAArtifactPath(contract.PrimaryPath) {
		contract.PrimaryPath = ""
		contract.RequiredFields = nil
	}
	return contract
}

var (
	explicitTextArtifactPathPattern        = regexp.MustCompile(`(?i)[a-z]:[\\/][^\r\n<>|?*]*?\.(?:md|markdown|txt|json|ya?ml|csv|html?)`)
	relativeTextArtifactPathPattern        = regexp.MustCompile(`(?i)(?:^|[\s(（])((?:[a-z0-9_][a-z0-9_.-]*[\\/])+[a-z0-9_.-]+\.(?:md|markdown|txt|html?))(?:$|[\s,，。；;:：)）])`)
	relativeDeliverableArtifactPathPattern = regexp.MustCompile(`(?i)(?:[a-z0-9_][a-z0-9_.-]*[\\/])+[a-z0-9_.-]+\.(?:md|markdown|txt|json|ya?ml|csv|tsv|html?|xml|toml)`)
	explicitTextArtifactVerbPattern        = regexp.MustCompile(`(?i)(?:保存|写入|写到|写成|写至|输出(?:到)?|导出(?:到)?|落盘|整理(?:为|成|到)|制作(?:为|成|到)|生成(?:为|到)?|创建|新建|save|write|export|create)`)
	explicitArtifactDeliveryVerbPattern    = regexp.MustCompile(`(?i)(?:保存|写入|写到|写成|写至|输出(?:到)?|导出(?:到)?|落盘|整理(?:为|成|到)|制作(?:为|成|到)|生成(?:为|到)?|创建|新建|转成|转换(?:为|成)|save|write|export|create|convert(?:ed|ing)?(?:\s+[^\r\n,，。；;:：]{0,40})?\s+to)`)
	explicitTextArtifactNegation           = regexp.MustCompile(`(?i)(?:不要|无需|不必|禁止|不得|不允许|do\s+not|don't|without).{0,16}(?:保存|写入|写到|写成|写至|输出|导出|落盘|整理|制作|生成|创建|新建|save|write|export|create)`)
	qualitativeEvidenceSignal              = regexp.MustCompile(`(?i)(?:行业位置|行业定位|行业格局|竞争格局|竞争地位|主营业务|业务结构|核心驱动|经营驱动|company\s+profile|business\s+mix|industry\s+position|competitive\s+position)`)
	beijingGenerationTimeSignal            = regexp.MustCompile(`(?i)(?:报告生成时间|生成时间|generated\s+at).{0,40}(?:北京时间|beijing\s+time)`)
	explicitMarkdownHeadingPattern         = regexp.MustCompile(`(?:^|[、，,；;：:\n])\s*(#{2,6}\s+[^、，,；;。\r\n]+)`)
	artifactBudgetSignal                   = regexp.MustCompile(`(?i)(?:预算|budget|成本上限|费用上限|价格上限)`)
	artifactCustomerMixSignal              = regexp.MustCompile(`(?i)(?:客群|客户结构|客户构成|海外客户|国内客户|customer\s+mix)`)
	artifactWorkloadSignal                 = regexp.MustCompile(`(?i)(?:日均|月均|年均|每日|每月|每年).{0,16}(?:工单|tickets?|会话|咨询)`)
	unstatedBudgetAssumptionSignal         = regexp.MustCompile(`(?im)^\s*(?:[-*>]\s*)?(?:预算|成本)(?:假设|基线|上限)?[^\n]*(?:[$￥¥]|usd|cny|rmb|每\s*(?:agent|坐席)|/agent)`)
	unstatedCustomerMixSignal              = regexp.MustCompile(`(?im)^.*(?:客群|客户结构|客户构成|假设)[^\n]*(?:海外|国内|中文|英文|%)`)
	unstatedWorkloadSignal                 = regexp.MustCompile(`(?im)^.*(?:日均|月均|年均|每日|每月|每年)(?:[^\n]*[0-9][^\n]*(?:工单|tickets?|会话|咨询)|[^\n]*(?:工单|tickets?|会话|咨询)[^\n]*[0-9])`)
	unstatedTeamPreferenceSignal           = regexp.MustCompile(`(?im)^.*(?:团队|公司)[^\n]*(?:倾向|偏好)[^\n]*$`)
	unstatedKnowledgeCutoffSignal          = regexp.MustCompile(`(?im)^.*(?:知识截止|knowledge\s+cutoff)[^\n]*$`)
)

// requestedTextArtifactPath recognizes only explicit, user-requested text
// artifacts with a concrete path. Absolute paths retain the historical broad
// extension support. Relative targets are accepted only when backticked,
// locally safe, text-only, and directly paired with an output verb; this keeps
// input fixtures and JSON/binary delivery contracts out of this fast path.
func requestedTextArtifactPath(input string) string {
	input = permissionpolicy.TrustedDirectiveText(input)
	if input == "" || explicitTextArtifactNegation.MatchString(input) || !explicitTextArtifactVerbPattern.MatchString(input) {
		return ""
	}
	if absolute := strings.TrimSpace(explicitTextArtifactPathPattern.FindString(input)); absolute != "" {
		return absolute
	}
	for _, location := range relativeTextArtifactPathPattern.FindAllStringSubmatchIndex(input, -1) {
		if len(location) < 4 {
			continue
		}
		candidate := strings.TrimSpace(input[location[2]:location[3]])
		if !safeRelativeTextArtifactPath(candidate) {
			continue
		}
		clauseStart := strings.LastIndexAny(input[:location[0]], "。！？!?；;\n\r") + 1
		prefix := input[clauseStart:location[0]]
		if len([]rune(prefix)) > 80 {
			runes := []rune(prefix)
			prefix = string(runes[len(runes)-80:])
		}
		if explicitTextArtifactVerbPattern.MatchString(prefix) {
			return candidate
		}
	}
	return ""
}

// requestedTextArtifactPaths returns every concrete text/data output path that
// the user explicitly asked the current turn to create. It is intentionally
// separate from requestedTextArtifactPath: the latter selects one Markdown-like
// target for the synthesis fast path, while this collection is a completion
// contract. A source/input path in the same sentence is excluded because each
// candidate must follow an output or conversion verb in its own clause.
func requestedTextArtifactPaths(input string) []string {
	input = permissionpolicy.TrustedDirectiveText(input)
	if input == "" {
		return nil
	}

	type candidate struct {
		start int
		end   int
		path  string
	}
	candidates := make([]candidate, 0, 4)
	for _, location := range explicitTextArtifactPathPattern.FindAllStringIndex(input, -1) {
		candidates = append(candidates, candidate{
			start: location[0],
			end:   location[1],
			path:  strings.TrimSpace(input[location[0]:location[1]]),
		})
	}
	for _, location := range relativeDeliverableArtifactPathPattern.FindAllStringIndex(input, -1) {
		if location[0] > 0 && strings.ContainsRune(":/\\", rune(input[location[0]-1])) {
			// This is the suffix of an absolute path already collected above.
			continue
		}
		path := strings.TrimSpace(input[location[0]:location[1]])
		if !safeRelativeDeliverableArtifactPath(path) {
			continue
		}
		candidates = append(candidates, candidate{start: location[0], end: location[1], path: path})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].start < candidates[j].start })

	paths := make([]string, 0, len(candidates))
	seen := make(map[string]bool)
	for _, item := range candidates {
		clauseStart := strings.LastIndexAny(input[:item.start], "。！？!?；;\n\r") + 1
		prefix := input[clauseStart:item.start]
		if len([]rune(prefix)) > 120 {
			runes := []rune(prefix)
			prefix = string(runes[len(runes)-120:])
		}
		if !explicitArtifactDeliveryVerbPattern.MatchString(prefix) || explicitTextArtifactNegation.MatchString(prefix) {
			continue
		}
		key := normalizeToolArtifactPath(item.path)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		paths = append(paths, item.path)
	}
	return paths
}

func safeRelativeTextArtifactPath(value string) bool {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.HasPrefix(value, "/") || filepath.IsAbs(value) ||
		regexp.MustCompile(`(?i)^[a-z]:/`).MatchString(value) || strings.ContainsAny(value, "<>|?*\"'") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	ext := strings.ToLower(filepath.Ext(value))
	return ext == ".md" || ext == ".markdown" || ext == ".txt" || ext == ".htm" || ext == ".html"
}

func safeRelativeDeliverableArtifactPath(value string) bool {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.HasPrefix(value, "/") || filepath.IsAbs(value) ||
		regexp.MustCompile(`(?i)^[a-z]:/`).MatchString(value) || strings.ContainsAny(value, "<>|?*\"'") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	switch strings.ToLower(filepath.Ext(value)) {
	case ".md", ".markdown", ".txt", ".json", ".yaml", ".yml", ".csv", ".tsv", ".htm", ".html", ".xml", ".toml":
		return true
	default:
		return false
	}
}

func substantiveArtifactEvidenceTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "stock_facts", "weather_forecast", "fuel_price", "web_fetch", "web_search", "social_trending",
		"source_acquire", "nova_chatroom_search", "rag_search", "read_file", "extract", "browser_task":
		return true
	default:
		return false
	}
}

func textArtifactEvidenceReady(results []string) bool {
	successes := 0
	weatherProviders := make(map[string]bool)
	for _, result := range results {
		record, ok := parseOpenKittyToolRecord(result)
		if !ok || !record.Succeeded || !substantiveArtifactEvidenceTool(record.Tool) {
			continue
		}
		successes++
		if strings.EqualFold(record.Tool, "weather_forecast") {
			if provider, ok := weatherartifact.ForecastProvider(result); ok {
				weatherProviders[strings.ToLower(provider)] = true
			}
		}
	}
	// Two complete weather packets from independent providers fully cover an
	// explicit two-source forecast request; requiring an unrelated third fetch
	// only delays deterministic report delivery.
	return len(weatherProviders) >= 2 || successes >= 3
}

func textArtifactNeedsQualitativeEvidence(input string) bool {
	return qualitativeEvidenceSignal.MatchString(strings.TrimSpace(input))
}

func hasArtifactQualitativeEvidenceAttempt(results []string) bool {
	for _, result := range results {
		record, ok := parseOpenKittyToolRecord(result)
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(record.Tool)) {
		case "web_fetch", "web_search", "source_acquire", "nova_chatroom_search", "rag_search", "browser_task":
			return true
		}
	}
	return false
}

func textArtifactDeliverySucceeded(results []string, targetPath string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(targetPath)))
	for i := len(results) - 1; i >= 0; i-- {
		record, ok := parseOpenKittyToolRecord(results[i])
		if !ok || !record.Succeeded || !strings.EqualFold(record.Tool, "write_file") {
			continue
		}
		if base == "" || strings.Contains(strings.ToLower(results[i]), base) {
			return true
		}
	}
	return false
}

// missingRequestedTextArtifactPaths applies one completion rule to a set of
// explicitly requested outputs: every path needs an exact, successful host
// receipt. A successful complete read_file is accepted as independent proof of
// an artifact produced through another execution tool; basename-only matches
// and prose claims never satisfy the contract.
func missingRequestedTextArtifactPaths(paths, results []string) []string {
	if len(paths) == 0 {
		return nil
	}
	delivered := make(map[string]bool, len(paths))
	for _, result := range results {
		record, ok := parseOpenKittyToolRecord(result)
		if !ok || !record.Succeeded || strings.TrimSpace(record.ArtifactPath) == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(record.Tool)) {
		case "write_file", "edit_file", "read_file":
		default:
			continue
		}
		for _, targetPath := range paths {
			if equivalentTextArtifactPath(targetPath, record.ArtifactPath) {
				delivered[normalizeToolArtifactPath(targetPath)] = true
			}
		}
	}

	missing := make([]string, 0, len(paths))
	for _, targetPath := range paths {
		if !delivered[normalizeToolArtifactPath(targetPath)] {
			missing = append(missing, targetPath)
		}
	}
	return missing
}

func requestedTextArtifactDeliveriesComplete(paths, results []string) bool {
	return len(missingRequestedTextArtifactPaths(paths, results)) == 0
}

func buildMissingTextArtifactDeliveryPrompt(paths []string) map[string]interface{} {
	return agentloop.NewInternalControlMessage("REQUESTED ARTIFACTS INCOMPLETE", fmt.Sprintf(`[REQUESTED ARTIFACTS INCOMPLETE]
Do not finish yet. The user explicitly requested concrete text/data outputs, and these exact workspace paths still lack a successful write, edit, or complete read receipt:
- %s

Create and verify only the missing outputs now. Preserve the user's requested formats and content constraints. Do not rewrite outputs that already have successful receipts, narrate completion, or claim a path exists without a real tool receipt.`, strings.Join(paths, "\n- ")))
}

func buildTextArtifactDeliveryMessages(messages []map[string]interface{}, input, targetPath string, results []string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, 4)
	if len(messages) > 0 {
		if role, _ := messages[0]["role"].(string); role == "system" {
			out = append(out, messages[0])
		}
	}

	var evidence strings.Builder
	evidence.WriteString("Validated current-turn evidence packets:\n\n")
	kept := 0
	artifactResearch := isArtifactResearchTurn(input)
	for _, result := range results {
		record, ok := parseOpenKittyToolRecord(result)
		if !ok || !record.Succeeded || !substantiveArtifactEvidenceTool(record.Tool) {
			continue
		}
		if artifactResearch && !strings.EqualFold(record.SearchMode, "direct") {
			// Discovery snippets may identify candidate URLs, but they are not
			// evidence for product, price, security, or regional-availability
			// claims in the delivered artifact.
			continue
		}
		if artifactResearch && !artifactResearchDirectEvidenceUsable(result) {
			continue
		}
		content := result
		if idx := strings.Index(content, "\n"); idx >= 0 {
			content = content[idx+1:]
		}
		content = strings.TrimSpace(content)
		if strings.EqualFold(record.Tool, "stock_facts") {
			// The first stock_facts content line is the compact structured marker;
			// it already contains every number, status, timestamp, and source URL.
			// Repeating the human renderer roughly doubles MiniMax's synthesis
			// context without adding evidence.
			if idx := strings.Index(content, "\n"); idx >= 0 {
				content = content[:idx]
			}
			content = textutil.TruncateUTF8Bytes(content, 2200)
		} else if artifactResearch {
			content = textutil.TruncateUTF8Bytes(content, 1800)
		} else {
			content = textutil.TruncateUTF8Bytes(content, 2600)
		}
		fmt.Fprintf(&evidence, "[%s]\n%s\n\n", record.Tool, content)
		kept++
		if kept >= 9 || evidence.Len() >= 13000 {
			break
		}
	}
	out = append(out,
		map[string]interface{}{
			"role": "system",
			"content": fmt.Sprintf(`[ARTIFACT DELIVERY WINDOW]
Core evidence acquisition is complete. Your only next action is one write_file call that creates the requested artifact at this exact path:
%s

Do not call plan_task, update_plan_step, web/search, stock/data tools, shell, or verification tools. Do not narrate a plan. Return the complete artifact as plain Markdown only; the host will atomically write it to the exact path above after grounding checks. Preserve source URLs and cutoff dates, label missing fields as unknown, and treat user-stated stress scenarios as conditional assumptions. Never invent a missing value. For research artifacts, use only direct evidence packets for vendor facts. In a "requirements and assumptions" section, repeat only user-provided organization facts; if customer mix, workload, budget, team preference, deployment constraint, or knowledge cutoff was not supplied, write unknown instead of proposing a baseline. Never create a data-residency or legal/compliance conclusion. Recommendations, weights, scores, and PoC targets must be explicitly labeled as analyst-defined decision criteria rather than vendor facts.

Keep an ordinary Markdown report between roughly 2,200 and 3,600 Chinese characters unless the user explicitly asks for more. Be concise: one evidence table, one weighted matrix, and short PoC/risk lists are enough. Follow every user-specified Markdown heading verbatim and in the requested order; do not add Chinese section numbers to those headings. Otherwise use no more than eight main sections and avoid repeating the same caveat. Before the write_file call, silently check every numeric comparison direction: mixed metrics must be described separately, never collapsed into an incorrect "all higher/lower" claim. Every company profile, business-mix, market-segment, exchange-board, leadership/ranking, or causal statement must be explicit in the evidence; otherwise label it as analysis/assumption/unknown or omit it. Do not introduce an arbitrary segment-share percentage merely to make a stress scenario look quantitative; use formulas or a qualitative sensitivity matrix when the share is unknown.

For stock_facts evidence, copy financial amounts in the exact raw CNY units returned by the structured markers. Do not rescale any amount into 亿元/万元 and do not calculate revenue deltas; this avoids manual unit errors. Copy every source URL byte-for-byte from an evidence packet—never shorten, reconstruct, or remove query parameters. Preserve RFC3339 timezone labels exactly; do not relabel a Z retrieval clock as Beijing time. Omit a report-generation clock unless the user supplied it.`, targetPath),
		},
		map[string]interface{}{"role": "user", "content": input},
		map[string]interface{}{"role": "system", "content": evidence.String()},
	)
	return out
}

// buildTextArtifactRepairMessages deliberately does not replay the entire
// research transcript. The first synthesis already consumed the frozen
// evidence set; a preflight repair only needs the rejected full draft and the
// host's exact diagnostics. Replaying the transcript made the model regenerate
// the report from scratch and could double artifact latency.
func buildTextArtifactRepairMessages(input, targetPath, draft string, issues []string) []map[string]interface{} {
	return []map[string]interface{}{
		{
			"role": "system",
			"content": fmt.Sprintf(`[ARTIFACT PREFLIGHT REPAIR]
Repair the rejected Markdown draft with the smallest possible edits. Do not add new product facts, URLs, dates, organization assumptions, or analysis inputs. Preserve all supported content, exact requested headings, source URLs, tables, and the requested section order.

The host will write the repaired Markdown to this exact path:
%s

Fix every host diagnostic below:
- %s

Return only the complete corrected Markdown, with no code fence. Do not search, plan, verify, or narrate.`, targetPath, strings.Join(issues, "\n- ")),
		},
		{"role": "user", "content": fmt.Sprintf(`Original request:
%s

Rejected full draft to repair:

%s`, input, draft)},
	}
}

func textArtifactMarkdownToolCall(content, targetPath string) (ToolCall, bool) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```markdown") {
		content = strings.TrimSpace(strings.TrimPrefix(content, "```markdown"))
	} else if strings.HasPrefix(content, "```md") {
		content = strings.TrimSpace(strings.TrimPrefix(content, "```md"))
	}
	if strings.HasSuffix(content, "```") {
		content = strings.TrimSpace(strings.TrimSuffix(content, "```"))
	}
	if content == "" {
		return ToolCall{}, false
	}
	return ToolCall{
		Name: "write_file",
		Arguments: map[string]interface{}{
			"path":    targetPath,
			"content": content,
		},
	}, true
}

func textArtifactDraftContent(calls []ToolCall) string {
	for _, call := range calls {
		if !strings.EqualFold(strings.TrimSpace(call.Name), "write_file") {
			continue
		}
		content, _ := call.Arguments["content"].(string)
		return strings.TrimSpace(content)
	}
	return ""
}

func textArtifactDraftIssues(content string, results []string, inputs ...string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return []string{"write_file content is empty"}
	}
	issues := securitycore.UnsupportedHighRiskClaimsForResults(content, results)
	if len(inputs) > 0 {
		input := inputs[0]
		issues = append(issues, textArtifactRequiredFieldIssues(requestedTextArtifactRequiredFields(input), content)...)
		lastIndex := -1
		for _, match := range explicitMarkdownHeadingPattern.FindAllStringSubmatch(input, -1) {
			if len(match) != 2 {
				continue
			}
			heading := strings.TrimSpace(match[1])
			linePattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(heading) + `[ \t]*$`)
			location := linePattern.FindStringIndex(content)
			if location == nil {
				issues = append(issues, "missing exact requested Markdown heading: "+heading)
				continue
			}
			index := location[0]
			if index < lastIndex {
				issues = append(issues, "requested Markdown headings are out of order: "+heading)
			}
			lastIndex = index
		}
		if isArtifactResearchTurn(input) {
			if !artifactBudgetSignal.MatchString(input) && unstatedBudgetAssumptionSignal.MatchString(content) {
				issues = append(issues, "unstated customer budget assumption; mark budget unknown instead of inventing a threshold")
			}
			if !artifactCustomerMixSignal.MatchString(input) && artifactDraftHasUnstatedAssumption(unstatedCustomerMixSignal, content) {
				issues = append(issues, "unstated customer-mix assumption; keep customer composition unknown")
			}
			if !artifactWorkloadSignal.MatchString(input) && unstatedWorkloadSignal.MatchString(content) {
				issues = append(issues, "unstated support-workload assumption; keep ticket volume unknown")
			}
			if artifactDraftHasUnstatedAssumption(unstatedTeamPreferenceSignal, content) {
				issues = append(issues, "unstated organization preference; do not invent what the team favors")
			}
			if artifactDraftHasUnstatedAssumption(unstatedKnowledgeCutoffSignal, content) && !strings.Contains(strings.ToLower(input), "知识截止") && !strings.Contains(strings.ToLower(input), "knowledge cutoff") {
				issues = append(issues, "unsupported knowledge-cutoff label; preserve only source dates explicitly supplied or observed")
			}
		}
	}
	hasStockEvidence := false
	for _, result := range results {
		record, ok := parseOpenKittyToolRecord(result)
		if ok && record.Succeeded && strings.EqualFold(record.Tool, "stock_facts") {
			hasStockEvidence = true
			break
		}
	}
	if hasStockEvidence {
		if strings.Contains(content, "亿元") || strings.Contains(content, "万元") || strings.Contains(content, "万亿元") {
			issues = append(issues, "manual stock CNY rescaling is forbidden; preserve raw CNY values")
		}
		if beijingGenerationTimeSignal.MatchString(content) {
			issues = append(issues, "unsupported Beijing report-generation clock")
		}
	}
	return uniqueSortedStrings(issues)
}

func artifactDraftExplicitlyMarksUnknown(line string) bool {
	line = strings.ToLower(strings.TrimSpace(line))
	for _, marker := range []string{"未知", "需商务确认", "需确认", "未提供", "unknown", "not provided", "不补假设"} {
		if strings.Contains(line, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func artifactDraftHasUnstatedAssumption(pattern *regexp.Regexp, content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if pattern.MatchString(line) && !artifactDraftExplicitlyMarksUnknown(line) {
			return true
		}
	}
	return false
}

// normalizeRequestedTextArtifactHeadings makes a harmless formatting repair
// when a model adds a suffix to an explicitly requested heading (for example
// "## 两周 PoC 计划"). It never creates missing sections or reorders content.
func normalizeRequestedTextArtifactHeadings(content, input string) string {
	for _, match := range explicitMarkdownHeadingPattern.FindAllStringSubmatch(input, -1) {
		if len(match) != 2 {
			continue
		}
		requested := strings.TrimSpace(match[1])
		exact := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(requested) + `[ \t]*$`)
		if exact.MatchString(content) {
			continue
		}
		withSuffix := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(requested) + `[^\r\n]+$`)
		content = withSuffix.ReplaceAllString(content, requested)
	}
	return content
}

// sanitizeUnstatedArtifactAssumptions removes only complete lines that assert
// organization inputs the user never supplied. This is safer and much faster
// than asking a model to regenerate a multi-thousand-character report for one
// bad table row. Explicit unknown/confirmation rows remain useful and survive.
func sanitizeUnstatedArtifactAssumptions(content, input string) string {
	if !isArtifactResearchTurn(input) {
		return content
	}
	lines := strings.Split(content, "\n")
	kept := lines[:0]
	for _, line := range lines {
		remove := false
		if !artifactDraftExplicitlyMarksUnknown(line) {
			remove = (!artifactBudgetSignal.MatchString(input) && unstatedBudgetAssumptionSignal.MatchString(line)) ||
				(!artifactCustomerMixSignal.MatchString(input) && unstatedCustomerMixSignal.MatchString(line)) ||
				(!artifactWorkloadSignal.MatchString(input) && unstatedWorkloadSignal.MatchString(line)) ||
				unstatedTeamPreferenceSignal.MatchString(line) ||
				(unstatedKnowledgeCutoffSignal.MatchString(line) && !strings.Contains(strings.ToLower(input), "知识截止") && !strings.Contains(strings.ToLower(input), "knowledge cutoff"))
		}
		if !remove {
			kept = append(kept, line)
		}
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func dropArtifactHousekeepingCalls(calls []ToolCall) ([]ToolCall, int) {
	if len(calls) < 2 {
		return calls, 0
	}
	hasSubstantive := false
	for _, call := range calls {
		switch strings.ToLower(strings.TrimSpace(call.Name)) {
		case "plan_task", "update_plan_step", "notepad", "report_progress":
		default:
			hasSubstantive = true
		}
	}
	if !hasSubstantive {
		return calls, 0
	}
	filtered := make([]ToolCall, 0, len(calls))
	dropped := 0
	for _, call := range calls {
		switch strings.ToLower(strings.TrimSpace(call.Name)) {
		case "plan_task", "update_plan_step", "notepad", "report_progress":
			dropped++
		default:
			filtered = append(filtered, call)
		}
	}
	return filtered, dropped
}

var (
	explicitTextArtifactFieldDirective = regexp.MustCompile(`(?i)(?:(?:必须|务必|需要|需|应当|应该|应|明确|请)\s*(?:在\s*(?:该|此|上述)?\s*(?:markdown\s*)?(?:报告|文档|文件|产物)(?:中|里)?\s*)?(?:明确\s*)?(?:记录|包含)|(?:记录|包含)\s*(?:以下|下列)\s*(?:字段|栏目|项目|要点)|(?:(?:must|required\s+to|needs?\s+to|should|please)\s+)(?:explicitly\s+)?(?:record|include|contain)(?:\s+in\s+(?:the\s+)?(?:report|document|file))?|(?:record|include|contain)\s+the\s+following\s+(?:fields?|items?|sections?)|(?:记录|包含))`)
	textArtifactHTMLComment            = regexp.MustCompile(`(?s)<!--.*?-->`)
	textArtifactListPrefix             = regexp.MustCompile(`^\s*(?:(?:[-*•])\s*|(?:\d{1,2}|[一二三四五六七八九十]+)[.)、．]\s*)`)
	textArtifactMarkdownHeading        = regexp.MustCompile(`^(#{1,6})[ \t]+(.+?)\s*$`)
)

// requestedTextArtifactRequiredFields extracts literal, user-visible labels
// only from an explicit text-artifact request. It deliberately requires a
// strong record/include directive plus a list signal; ordinary discussion of
// what a report might contain is not promoted into a completion contract.
func requestedTextArtifactRequiredFields(input string) []string {
	input = permissionpolicy.TrustedDirectiveText(input)
	if requestedTextArtifactPath(input) == "" {
		return nil
	}

	clauses := splitTextArtifactContractClauses(input)
	artifactContext := false
	var fields []string
	seen := make(map[string]bool)
	for _, clause := range clauses {
		hasScope := textArtifactClauseHasScope(clause)
		if hasScope {
			artifactContext = true
		}
		for _, location := range explicitTextArtifactFieldDirective.FindAllStringIndex(clause, -1) {
			if textArtifactDirectiveIsNegated(clause, location[0]) {
				continue
			}
			directive := clause[location[0]:location[1]]
			if !hasScope && !(artifactContext && textArtifactDirectiveCanStandAlone(clause, location[0], directive)) {
				continue
			}
			tail := strings.TrimSpace(clause[location[1]:])
			candidates := parseExplicitTextArtifactFieldList(directive, tail)
			for _, candidate := range candidates {
				key := normalizeTextArtifactFieldLiteral(candidate)
				if key == "" || seen[key] {
					continue
				}
				seen[key] = true
				fields = append(fields, candidate)
			}
		}
	}
	return fields
}

func splitTextArtifactContractClauses(input string) []string {
	return strings.FieldsFunc(input, func(r rune) bool {
		switch r {
		case '。', '！', '？', '!', '?', '\n', '\r':
			return true
		default:
			return false
		}
	})
}

func textArtifactClauseHasScope(clause string) bool {
	if explicitTextArtifactPathPattern.MatchString(clause) || relativeTextArtifactPathPattern.MatchString(clause) {
		return true
	}
	lower := strings.ToLower(clause)
	for _, marker := range []string{"报告", "文档", "文件", "产物", "markdown", "report", "document"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func textArtifactDirectiveIsNegated(clause string, byteOffset int) bool {
	prefix := strings.ToLower(clause[:byteOffset])
	runes := []rune(prefix)
	if len(runes) > 28 {
		runes = runes[len(runes)-28:]
	}
	near := strings.TrimSpace(string(runes))
	for _, marker := range []string{"解释如何", "说明如何", "演示如何", "how to", "explain how", "describe how"} {
		if strings.Contains(near, marker) {
			return true
		}
	}
	for _, marker := range []string{"不", "无", "无需", "无须", "不必", "不要", "禁止", "不得", "not", "do not", "does not", "without"} {
		if strings.HasSuffix(near, marker) {
			return true
		}
	}
	return false
}

func textArtifactDirectiveCanStandAlone(clause string, byteOffset int, directive string) bool {
	prefix := strings.TrimSpace(clause[:byteOffset])
	prefix = strings.TrimSpace(strings.TrimPrefix(prefix, "并"))
	if utf8.RuneCountInString(prefix) > 6 {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(directive))
	for _, marker := range []string{
		"明确", "必须", "务必", "需要", "需", "应", "记录以下", "包含以下",
		"must", "required", "need", "should", "record the following", "include the following", "contain the following",
	} {
		if strings.HasPrefix(lower, marker) || strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func parseExplicitTextArtifactFieldList(directive, tail string) []string {
	originalTail := strings.TrimSpace(tail)
	lowerDirective := strings.ToLower(directive)
	lowerTail := strings.ToLower(originalTail)
	followingSignal := strings.Contains(lowerDirective, "以下") ||
		strings.Contains(lowerDirective, "下列") ||
		strings.Contains(lowerDirective, "following") ||
		strings.HasPrefix(lowerTail, "以下") ||
		strings.HasPrefix(lowerTail, "下列") ||
		strings.HasPrefix(lowerTail, "the following")
	colonSignal := strings.HasPrefix(strings.TrimSpace(originalTail), ":") ||
		strings.HasPrefix(strings.TrimSpace(originalTail), "：")
	separatorSignal := strings.ContainsAny(originalTail, "、,，;；")
	if !followingSignal && !colonSignal && !separatorSignal {
		return nil
	}

	tail = strings.TrimSpace(originalTail)
	for {
		before := tail
		for _, prefix := range []string{
			"以下字段", "下列字段", "以下栏目", "下列栏目", "以下项目", "下列项目", "以下要点", "下列要点",
			"以下内容", "下列内容", "这些字段", "the following fields", "the following items", "the following sections",
		} {
			if strings.HasPrefix(strings.ToLower(tail), strings.ToLower(prefix)) {
				tail = strings.TrimSpace(tail[len(prefix):])
				break
			}
		}
		tail = strings.TrimLeft(tail, " \t:：")
		if tail == before {
			break
		}
	}
	if tail == "" {
		return nil
	}

	parts := strings.FieldsFunc(tail, func(r rune) bool {
		switch r {
		case '、', ',', '，', ';', '；':
			return true
		default:
			return false
		}
	})
	parts = splitFinalTextArtifactConjunction(parts, followingSignal || colonSignal)

	fields := make([]string, 0, len(parts))
	for _, part := range parts {
		field := cleanTextArtifactFieldCandidate(part)
		if field == "" || textArtifactFieldLooksLikeInstruction(field) {
			continue
		}
		fields = append(fields, field)
		if len(fields) >= 12 {
			break
		}
	}
	if len(fields) < 2 && !followingSignal && !colonSignal {
		return nil
	}
	return fields
}

func splitFinalTextArtifactConjunction(parts []string, explicitList bool) []string {
	if len(parts) == 0 || (!explicitList && len(parts) < 2) {
		return parts
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	for _, separator := range []string{"以及", "和", " and "} {
		index := strings.LastIndex(strings.ToLower(last), strings.ToLower(separator))
		if index <= 0 || index+len(separator) >= len(last) {
			continue
		}
		left := strings.TrimSpace(last[:index])
		right := strings.TrimSpace(last[index+len(separator):])
		if left == "" || right == "" || utf8.RuneCountInString(left) > 32 || utf8.RuneCountInString(right) > 32 {
			continue
		}
		parts = append(parts[:len(parts)-1], left, right)
		break
	}
	return parts
}

func cleanTextArtifactFieldCandidate(field string) string {
	field = textArtifactListPrefix.ReplaceAllString(field, "")
	field = strings.TrimSpace(field)
	field = strings.Trim(field, " \t`*_#\"'“”‘’[]【】:：。.!！?？")
	if opening := strings.IndexAny(field, "（("); opening > 0 && (strings.HasSuffix(field, "）") || strings.HasSuffix(field, ")")) {
		field = strings.TrimSpace(field[:opening])
	}
	if utf8.RuneCountInString(field) == 0 || utf8.RuneCountInString(field) > 40 {
		return ""
	}
	return field
}

func textArtifactFieldLooksLikeInstruction(field string) bool {
	lower := strings.ToLower(strings.TrimSpace(field))
	for _, prefix := range []string{
		"并且", "并使用", "并保存", "并写", "然后", "同时", "确保", "不要", "不得", "无需", "禁止",
		"and then", "then ", "ensure ", "do not", "without ", "save ", "write ", "create ",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return explicitTextArtifactPathPattern.MatchString(field) || relativeTextArtifactPathPattern.MatchString(field)
}

func normalizeTextArtifactFieldLiteral(text string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func textArtifactRequiredFieldIssues(requiredFields []string, content string) []string {
	if len(requiredFields) == 0 {
		return nil
	}
	visible := normalizeTextArtifactFieldLiteral(textArtifactVisibleMarkdown(content))
	issues := make([]string, 0, len(requiredFields))
	for _, field := range requiredFields {
		literal := normalizeTextArtifactFieldLiteral(field)
		if literal == "" || strings.Contains(visible, literal) {
			continue
		}
		issues = append(issues, fmt.Sprintf("missing explicitly required report field label: %q", field))
	}
	return issues
}

func textArtifactVisibleMarkdown(content string) string {
	content = textArtifactHTMLComment.ReplaceAllString(content, "")
	lines := strings.SplitAfter(content, "\n")
	var out strings.Builder
	inFence := false
	for _, line := range lines {
		if textutil.IsMarkdownFenceBoundary(line) {
			inFence = !inFence
			continue
		}
		if !inFence {
			out.WriteString(line)
		}
	}
	return out.String()
}

func textArtifactDraftContentForPath(calls []ToolCall, targetPath string) (string, bool) {
	for _, call := range calls {
		if !strings.EqualFold(strings.TrimSpace(call.Name), "write_file") || !textArtifactToolCallTargetsPath(call, targetPath) {
			continue
		}
		content, _ := call.Arguments["content"].(string)
		return strings.TrimSpace(content), true
	}
	return "", false
}

func textArtifactCallsMutateTarget(calls []ToolCall, targetPath string) bool {
	for _, call := range calls {
		if textArtifactToolCallMutatesTarget(call, targetPath) {
			return true
		}
	}
	return false
}

func textArtifactCallsReadCompleteTarget(calls []ToolCall, targetPath string) bool {
	for _, call := range calls {
		if !strings.EqualFold(strings.TrimSpace(call.Name), "read_file") ||
			!textArtifactToolCallTargetsPath(call, targetPath) {
			continue
		}
		if linesRange, _ := call.Arguments["lines_range"].(string); strings.TrimSpace(linesRange) == "" {
			return true
		}
	}
	return false
}

func textArtifactToolCallMutatesTarget(call ToolCall, targetPath string) bool {
	switch strings.ToLower(strings.TrimSpace(call.Name)) {
	case "write_file", "edit_file":
		return textArtifactToolCallTargetsPath(call, targetPath)
	default:
		return false
	}
}

func textArtifactToolCallTargetsPath(call ToolCall, targetPath string) bool {
	return equivalentTextArtifactPath(targetPath, routingcore.ToolArtifactPath(call))
}

func equivalentTextArtifactPath(targetPath, actualPath string) bool {
	target := normalizeToolArtifactPath(targetPath)
	actual := normalizeToolArtifactPath(actualPath)
	if target == "" || actual == "" {
		return false
	}
	if target == actual {
		return true
	}

	// File tools commonly expand a requested workspace-relative path to an
	// absolute one. Accept only that exact path-component suffix, and only when
	// one side is absolute. This match merely triggers the host's independent
	// readback of targetPath, so an outside file with the same suffix can never
	// satisfy the delivery contract.
	targetAbsolute := portablepath.IsAbs(target)
	actualAbsolute := portablepath.IsAbs(actual)
	if targetAbsolute == actualAbsolute {
		return false
	}
	absolute, relative := actual, target
	if targetAbsolute {
		absolute, relative = target, actual
	}
	relative = strings.TrimPrefix(relative, "/")
	return relative != "" && strings.HasSuffix(absolute, "/"+relative)
}

type textArtifactHeading struct {
	lineIndex int
	title     string
	body      string
}

// normalizeTextArtifactRequiredHeadings repairs only a narrow presentation
// mismatch: a requested literal field label is absent, but one visible
// Markdown heading has an unambiguous equivalent meaning. It never invents a
// section body and fails closed when two headings score equally.
func normalizeTextArtifactRequiredHeadings(content string, requiredFields []string) (string, bool) {
	return normalizeTextArtifactRequiredHeadingsWithSynthesis(content, requiredFields, nil)
}

func normalizeTextArtifactRequiredHeadingsWithSynthesis(
	content string,
	requiredFields []string,
	synthesizedBodies map[string]string,
) (string, bool) {
	if len(requiredFields) == 0 || len(textArtifactRequiredFieldIssues(requiredFields, content)) == 0 {
		return content, false
	}
	lineEnding := "\n"
	if strings.Contains(content, "\r\n") {
		lineEnding = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	headings := visibleTextArtifactHeadings(lines)
	if len(headings) == 0 {
		return content, false
	}

	changed := false
	for _, field := range requiredFields {
		if len(textArtifactRequiredFieldIssues([]string{field}, strings.Join(lines, "\n"))) == 0 {
			continue
		}
		family := textArtifactHeadingFamily(field)
		if family == "" {
			return content, false
		}
		bestIndex, bestScore, ties := -1, -1, 0
		for index, heading := range headings {
			if textArtifactHeadingFamily(heading.title) != family {
				continue
			}
			score := textArtifactHeadingMatchScore(family, field, heading)
			switch {
			case score > bestScore:
				bestIndex, bestScore, ties = index, score, 1
			case score == bestScore:
				ties++
			}
		}
		if bestIndex < 0 {
			body := strings.TrimSpace(synthesizedBodies[family])
			if body == "" {
				return content, false
			}
			if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
				lines = append(lines, "")
			}
			lines = append(lines, "## "+strings.TrimSpace(field), body)
			headings = visibleTextArtifactHeadings(lines)
			changed = true
			continue
		}
		if ties != 1 {
			return content, false
		}
		heading := headings[bestIndex]
		match := textArtifactMarkdownHeading.FindStringSubmatch(lines[heading.lineIndex])
		if len(match) != 3 {
			return content, false
		}
		lines[heading.lineIndex] = match[1] + " " + strings.TrimSpace(match[2]) + "（" + strings.TrimSpace(field) + "）"
		headings[bestIndex].title = strings.TrimSpace(match[2]) + "（" + strings.TrimSpace(field) + "）"
		changed = true
	}

	repaired := strings.Join(lines, lineEnding)
	if !changed || len(textArtifactRequiredFieldIssues(requiredFields, repaired)) != 0 {
		return content, false
	}
	return repaired, true
}

func visibleTextArtifactHeadings(lines []string) []textArtifactHeading {
	var headings []textArtifactHeading
	inFence := false
	for index, line := range lines {
		if textutil.IsMarkdownFenceBoundary(line) {
			inFence = !inFence
			continue
		}
		if inFence || strings.Contains(line, "<!--") {
			continue
		}
		match := textArtifactMarkdownHeading.FindStringSubmatch(line)
		if len(match) == 3 {
			headings = append(headings, textArtifactHeading{lineIndex: index, title: strings.TrimSpace(match[2])})
		}
	}
	for index := range headings {
		start, end := headings[index].lineIndex+1, len(lines)
		if index+1 < len(headings) {
			end = headings[index+1].lineIndex
		}
		headings[index].body = strings.Join(lines[start:end], "\n")
	}
	return headings
}

func textArtifactHeadingFamily(value string) string {
	normalized := normalizeTextArtifactFieldLiteral(value)
	families := []struct {
		name    string
		aliases []string
	}{
		{"plan", []string{"原计划", "初始计划", "计划方案", "originalplan", "initialplan", "plannedapproach"}},
		{"failure", []string{"真实失败", "实际失败", "失败信息", "错误信息", "actualfailure", "realfailure", "failuredetails", "failureinformation"}},
		{"fallback", []string{"降级决策", "回退决策", "替代决策", "降级方案", "degradationdecision", "fallbackdecision", "fallbackpath", "degradedpath"}},
		{"verification", []string{"验证条数", "验证结果", "验证记录数", "记录数", "validationresults", "verificationresults", "verification", "recordcount", "recordswritten"}},
	}
	for _, family := range families {
		for _, alias := range family.aliases {
			if strings.Contains(normalized, normalizeTextArtifactFieldLiteral(alias)) {
				return family.name
			}
		}
	}
	return ""
}

func textArtifactHeadingMatchScore(family, required string, heading textArtifactHeading) int {
	title := normalizeTextArtifactFieldLiteral(heading.title)
	body := normalizeTextArtifactFieldLiteral(heading.body)
	score := 10
	switch family {
	case "plan":
		if strings.Contains(title, "originalplan") || strings.Contains(title, normalizeTextArtifactFieldLiteral("原计划")) {
			score += 20
		}
	case "failure":
		if strings.Contains(title, "actualfailure") || strings.Contains(title, normalizeTextArtifactFieldLiteral("真实失败")) {
			score += 20
		}
	case "fallback":
		if strings.Contains(title, "degradationdecision") || strings.Contains(title, normalizeTextArtifactFieldLiteral("降级决策")) {
			score += 20
		}
	case "verification":
		if strings.Contains(title, "validationresults") || strings.Contains(title, "verificationresults") ||
			strings.Contains(title, normalizeTextArtifactFieldLiteral("验证结果")) {
			score += 15
		}
		requiredNormalized := normalizeTextArtifactFieldLiteral(required)
		if strings.Contains(requiredNormalized, normalizeTextArtifactFieldLiteral("条数")) || strings.Contains(requiredNormalized, "count") {
			for _, marker := range []string{"totalrecords", "recordcount", "recordswritten", "总记录数", "记录数", "验证条数"} {
				if strings.Contains(body, normalizeTextArtifactFieldLiteral(marker)) {
					score += 30
					break
				}
			}
		}
	}
	return score
}

type textArtifactRecoveryEvidence struct {
	failedTool     string
	failureClass   string
	laterSuccess   bool
	successfulWork bool
}

func textArtifactRecoveryEvidenceFromResults(results []string) textArtifactRecoveryEvidence {
	evidence := textArtifactRecoveryEvidence{}
	failureIndex := -1
	for index, result := range results {
		record, ok := parseOpenKittyToolRecord(result)
		if !ok {
			continue
		}
		if !record.Succeeded {
			failureIndex = index
			evidence.failedTool = strings.TrimSpace(record.Tool)
			payload := result
			if newline := strings.Index(payload, "\n"); newline >= 0 {
				payload = payload[newline+1:]
			}
			evidence.failureClass = stableTextArtifactFailureClass(extractErrorSignature(payload))
			evidence.laterSuccess = false
			continue
		}
		if textArtifactReceiptProvesWork(record.Tool) {
			evidence.successfulWork = true
		}
		if failureIndex >= 0 && index > failureIndex && strings.EqualFold(strings.TrimSpace(record.Tool), evidence.failedTool) {
			evidence.laterSuccess = true
		}
	}
	return evidence
}

func textArtifactReceiptProvesWork(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "", "read_file", "list_dir", "glob", "grep", "project_map", "snapshot", "web_search", "web_fetch":
		return false
	default:
		return true
	}
}

func stableTextArtifactFailureClass(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, allowed := range []string{
		"encoding", "output_too_large", "network", "http_status", "permission", "not_found",
		"command_not_found", "syntax", "resource", "api_error",
	} {
		if value == allowed {
			return allowed
		}
	}
	return "execution_error"
}

func textArtifactReportConfirmsSuccess(content string) bool {
	visible := strings.ToLower(textArtifactVisibleMarkdown(content))
	normalized := normalizeTextArtifactFieldLiteral(visible)
	for _, negative := range []string{"notsuccessful", "unsuccessful", "未成功", "尚未完成", "执行失败"} {
		if strings.Contains(normalized, normalizeTextArtifactFieldLiteral(negative)) {
			return false
		}
	}
	for _, positive := range []string{"successfully", "succeeded", "completed", "执行成功", "转换完成", "成功使用", "已完成"} {
		if strings.Contains(normalized, normalizeTextArtifactFieldLiteral(positive)) {
			return true
		}
	}
	return false
}

func textArtifactRecoverySynthesis(content string, results []string) map[string]string {
	evidence := textArtifactRecoveryEvidenceFromResults(results)
	synthesis := make(map[string]string)
	if evidence.failedTool != "" {
		detail := fmt.Sprintf("本轮检测到 %s 工具失败（分类：%s）。", evidence.failedTool, evidence.failureClass)
		decision := fmt.Sprintf("在 %s 工具失败后停止重复该路径。", evidence.failedTool)
		if evidence.laterSuccess {
			detail += "随后同类工具的等价调用成功。"
			decision = fmt.Sprintf("在 %s 工具失败后切换到后续成功的等价调用；未重复同一失败操作。", evidence.failedTool)
		}
		synthesis["failure"] = detail
		synthesis["fallback"] = decision
		return synthesis
	}
	if evidence.successfulWork || textArtifactReportConfirmsSuccess(content) {
		synthesis["failure"] = "无；现有报告与当前回执显示主路径执行成功。"
		synthesis["fallback"] = "未触发；主路径执行成功，无需降级。"
	}
	return synthesis
}

// repairTextArtifactRequiredHeadings persists the conservative heading-only
// normalization and verifies the exact labels from a fresh full read. The
// artifact must have been written during this turn.
func repairTextArtifactRequiredHeadings(
	ctx context.Context,
	targetPath, content string,
	requiredFields []string,
	startedAt time.Time,
	toolResults []string,
) (string, []string, bool) {
	repaired, changed := normalizeTextArtifactRequiredHeadingsWithSynthesis(
		content,
		requiredFields,
		textArtifactRecoverySynthesis(content, toolResults),
	)
	if !changed {
		return "", nil, false
	}
	workspace := projectctx.NormalizeWorkspace(projectctx.ResolvedWorkspace(ctx))
	if workspace == "" {
		return "", nil, false
	}
	target := strings.TrimSpace(targetPath)
	if !filepath.IsAbs(target) {
		target = filepath.Join(workspace, target)
	}
	target, err := filepath.Abs(target)
	if err != nil || !projectctx.ContainsPath(workspace, target) || projectctx.EnsureResolvedPathInside(workspace, target) != nil {
		return "", nil, false
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() || (!startedAt.IsZero() && info.ModTime().Before(startedAt.Add(-2*time.Second))) {
		return "", nil, false
	}
	ext := strings.ToLower(filepath.Ext(target))
	if ext != ".md" && ext != ".markdown" {
		return "", nil, false
	}

	writeCall := ToolCall{ID: "host_text_artifact_heading_repair", Name: "write_file", Arguments: map[string]interface{}{
		"path": target, "content": repaired,
	}}
	writeResult, writeErr := NewWriteFileTool(workspace).Execute(ctx, writeCall.Arguments)
	writeSucceeded := ToolExecutionSucceededForTool("write_file", writeResult, writeErr)
	records := []string{toolResultForFinalization(writeCall, writeResult, writeSucceeded)}
	if !writeSucceeded {
		return "", records, false
	}
	readCall := ToolCall{ID: "host_text_artifact_heading_readback", Name: "read_file", Arguments: map[string]interface{}{"path": target}}
	readback, readErr := NewReadFileTool(workspace).Execute(ctx, readCall.Arguments)
	readSucceeded := ToolExecutionSucceededForTool("read_file", readback, readErr)
	records = append(records, toolResultForFinalization(readCall, readback, readSucceeded))
	if !readSucceeded || len(textArtifactRequiredFieldIssues(requiredFields, readback)) != 0 {
		return "", records, false
	}
	return readback, records, true
}

// recoverTextArtifactAfterProviderFailure is a narrow, evidence-bound fallback
// for an explicitly requested Markdown report. It is used only when the model
// channel fails after another current-turn artifact was fully read and parsed.
// Unknown field semantics, stale files, or unverified outputs fail closed.
func recoverTextArtifactAfterProviderFailure(
	ctx context.Context,
	targetPath string,
	requiredFields []string,
	toolResults []string,
	startedAt time.Time,
) (string, []string, bool) {
	workspace := projectctx.NormalizeWorkspace(projectctx.ResolvedWorkspace(ctx))
	if workspace == "" || len(requiredFields) == 0 {
		return "", nil, false
	}
	target := strings.TrimSpace(targetPath)
	if !filepath.IsAbs(target) {
		target = filepath.Join(workspace, target)
	}
	target, err := filepath.Abs(target)
	if err != nil || !projectctx.ContainsPath(workspace, target) {
		return "", nil, false
	}
	ext := strings.ToLower(filepath.Ext(target))
	if ext != ".md" && ext != ".markdown" {
		return "", nil, false
	}

	if info, statErr := os.Stat(target); statErr == nil && info.Mode().IsRegular() {
		if !startedAt.IsZero() && info.ModTime().Before(startedAt.Add(-2*time.Second)) {
			return "", nil, false
		}
		content, readErr := NewReadFileTool(workspace).Execute(ctx, map[string]interface{}{"path": target})
		if readErr != nil {
			return "", nil, false
		}
		return repairTextArtifactRequiredHeadings(ctx, target, content, requiredFields, startedAt, toolResults)
	}

	verification, ok := textArtifactVerifiedOutputSummary(ctx, target, toolResults, startedAt)
	if !ok {
		return "", nil, false
	}
	recovery := textArtifactRecoveryEvidenceFromResults(toolResults)
	if !recovery.successfulWork && recovery.failedTool == "" {
		return "", nil, false
	}
	bodies := textArtifactRecoveryReportBodies(recovery, verification)
	var report strings.Builder
	report.WriteString("# 任务恢复报告\n")
	for _, field := range requiredFields {
		family := textArtifactHeadingFamily(field)
		body := strings.TrimSpace(bodies[family])
		if family == "" || body == "" {
			return "", nil, false
		}
		report.WriteString("\n## " + strings.TrimSpace(field) + "\n" + body + "\n")
	}
	content := report.String()
	if len(textArtifactRequiredFieldIssues(requiredFields, content)) != 0 {
		return "", nil, false
	}

	writeCall := ToolCall{ID: "host_text_artifact_provider_recovery", Name: "write_file", Arguments: map[string]interface{}{
		"path": target, "content": content,
	}}
	writeResult, writeErr := NewWriteFileTool(workspace).Execute(ctx, writeCall.Arguments)
	writeSucceeded := ToolExecutionSucceededForTool("write_file", writeResult, writeErr)
	records := []string{toolResultForFinalization(writeCall, writeResult, writeSucceeded)}
	if !writeSucceeded {
		return "", records, false
	}
	readCall := ToolCall{ID: "host_text_artifact_provider_readback", Name: "read_file", Arguments: map[string]interface{}{"path": target}}
	readback, readErr := NewReadFileTool(workspace).Execute(ctx, readCall.Arguments)
	readSucceeded := ToolExecutionSucceededForTool("read_file", readback, readErr)
	records = append(records, toolResultForFinalization(readCall, readback, readSucceeded))
	if !readSucceeded || len(textArtifactRequiredFieldIssues(requiredFields, readback)) != 0 {
		return "", records, false
	}
	return readback, records, true
}

func textArtifactRecoveryReportBodies(recovery textArtifactRecoveryEvidence, verification string) map[string]string {
	bodies := map[string]string{
		"plan":         "按用户请求执行指定输入到目标产物的处理，并在需要时采用等价恢复路径。",
		"verification": verification,
	}
	if recovery.failedTool == "" {
		bodies["failure"] = "无；本轮工作型工具回执均成功。"
		bodies["fallback"] = "未触发；主路径完成后由主机独立读取产物验证。"
		return bodies
	}
	bodies["failure"] = fmt.Sprintf("检测到 %s 工具失败（分类：%s）。", recovery.failedTool, recovery.failureClass)
	bodies["fallback"] = fmt.Sprintf("停止重复 %s 的失败调用，并保留后续已验证的等价产物路径。", recovery.failedTool)
	if recovery.laterSuccess {
		bodies["failure"] += "随后同类工具的等价调用成功。"
		bodies["fallback"] = fmt.Sprintf("在 %s 工具失败后切换到后续成功的等价调用；未重复同一失败操作。", recovery.failedTool)
	}
	return bodies
}

func textArtifactVerifiedOutputSummary(
	ctx context.Context,
	targetPath string,
	toolResults []string,
	startedAt time.Time,
) (string, bool) {
	workspace := projectctx.NormalizeWorkspace(projectctx.ResolvedWorkspace(ctx))
	if workspace == "" {
		return "", false
	}
	seen := make(map[string]bool)
	var summaries []string
	for index := len(toolResults) - 1; index >= 0 && len(summaries) < 3; index-- {
		record, ok := parseOpenKittyToolRecord(toolResults[index])
		if !ok || !record.Succeeded || !strings.EqualFold(strings.TrimSpace(record.Tool), "read_file") ||
			strings.TrimSpace(record.ArtifactPath) == "" || equivalentTextArtifactPath(targetPath, record.ArtifactPath) {
			continue
		}
		candidate := strings.TrimSpace(record.ArtifactPath)
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(workspace, candidate)
		}
		candidate, err := filepath.Abs(candidate)
		if err != nil || !projectctx.ContainsPath(workspace, candidate) || projectctx.EnsureResolvedPathInside(workspace, candidate) != nil {
			continue
		}
		key := projectctx.IdentityKey(candidate)
		if seen[key] {
			continue
		}
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() || (!startedAt.IsZero() && info.ModTime().Before(startedAt.Add(-2*time.Second))) {
			continue
		}
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		var value interface{}
		if strings.EqualFold(filepath.Ext(candidate), ".json") && json.Unmarshal(data, &value) == nil {
			relative := presentationWorkspaceRelativePath(workspace, candidate)
			switch typed := value.(type) {
			case []interface{}:
				summaries = append(summaries, fmt.Sprintf("- %s：JSON 顶层记录 %d 条（主机完整解析）。", relative, len(typed)))
			case map[string]interface{}:
				summaries = append(summaries, fmt.Sprintf("- %s：JSON 对象含 %d 个顶层字段（主机完整解析）。", relative, len(typed)))
			}
		}
		seen[key] = true
	}
	if len(summaries) == 0 {
		return "", false
	}
	return strings.Join(summaries, "\n"), true
}

func executeTextArtifactReadback(ctx context.Context, registry *toolregistry.Registry, targetPath string) (string, string, bool, string) {
	call := ToolCall{
		ID:   "host_text_artifact_readback",
		Name: "read_file",
		Arguments: map[string]interface{}{
			"path": targetPath,
		},
	}
	if registry == nil {
		message := "read_file is unavailable for mandatory artifact readback"
		return "", toolResultForFinalization(call, message, false), false, message
	}
	tool, ok := registry.Get("read_file")
	if !ok {
		message := "read_file is unavailable for mandatory artifact readback"
		return "", toolResultForFinalization(call, message, false), false, message
	}
	content, err := tool.Execute(ctx, call.Arguments)
	succeeded := ToolExecutionSucceeded(content, err)
	result := content
	reason := ""
	if err != nil {
		reason = err.Error()
		if strings.TrimSpace(result) == "" {
			result = reason
		}
	} else if !succeeded {
		reason = strings.TrimSpace(content)
	}
	return content, toolResultForFinalization(call, result, succeeded), succeeded, reason
}

var (
	wechatBriefSectionPattern   = regexp.MustCompile(`(?m)^#{2,6}\s+\d+\.\s*([^\r\n]+)\s*$`)
	wechatNumberedTitlePattern  = regexp.MustCompile(`(?m)^\s*\d+[.、]\s*(?:\*\*)?([^\r\n*]+)(?:\*\*)?\s*$`)
	wechatProvenanceLinePattern = regexp.MustCompile(`(?m)^<!-- OPENKITTY_(?:EXECUTION_ID|SOURCE_SHA256): [^>\r\n]+ -->\s*\r?\n?`)
	wechatGeneratedCoverPattern = regexp.MustCompile(`(?mi)^!\[[^\]\r\n]*\]\(<?([^)>\r\n]*wechat_cover_[0-9a-f]{16}\.png)>?\)\s*$`)
)

func wechatBriefSections(brief string) map[string]string {
	matches := wechatBriefSectionPattern.FindAllStringSubmatchIndex(brief, -1)
	sections := make(map[string]string, len(matches))
	for i, match := range matches {
		start := match[1]
		end := len(brief)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		name := strings.TrimSpace(strings.Trim(brief[match[2]:match[3]], "*：: "))
		sections[name] = strings.TrimSpace(brief[start:end])
	}
	return sections
}

func wechatBriefSection(sections map[string]string, name string) string {
	if value := strings.TrimSpace(sections[name]); value != "" {
		return value
	}
	for sectionName, value := range sections {
		trimmedName := strings.TrimSpace(sectionName)
		if strings.HasPrefix(trimmedName, name+"（") || strings.HasPrefix(trimmedName, name+"(") || strings.HasPrefix(trimmedName, name+"：") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cleanWechatBriefBlock(block string) string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(block), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			lines = append(lines, "")
			continue
		}
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "SOURCE_STATUS:") || strings.HasPrefix(upper, "SOURCE_FILE:") || strings.Contains(upper, "PIPELINE_") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func firstWechatTitle(block string) string {
	match := wechatNumberedTitlePattern.FindStringSubmatch(block)
	if len(match) == 2 {
		return strings.TrimSpace(strings.Trim(match[1], "*：: 《》「」『』"))
	}
	return "把复杂信息讲明白：关键变化、判断与行动"
}

func wechatAlternativeTitles(block, selected string) []string {
	matches := wechatNumberedTitlePattern.FindAllStringSubmatch(block, -1)
	seen := map[string]bool{selected: true}
	var titles []string
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		title := strings.TrimSpace(strings.Trim(match[1], "*：: 《》「」『』"))
		if title == "" || seen[title] {
			continue
		}
		seen[title] = true
		titles = append(titles, title)
		if len(titles) == 3 {
			break
		}
	}
	return titles
}

func buildWechatArticleFallbackMarkdown(brief string) string {
	sections := wechatBriefSections(brief)
	titleBlock := wechatBriefSection(sections, "标题角度")
	title := firstWechatTitle(titleBlock)
	summary := cleanWechatBriefBlock(wechatBriefSection(sections, "摘要方向"))
	if summary == "" {
		summary = "这篇文章基于现有素材，梳理最值得关注的事实、判断边界和行动线索，帮助读者快速抓住重点。"
	}
	facts := cleanWechatBriefBlock(wechatBriefSection(sections, "素材明确陈述"))
	views := cleanWechatBriefBlock(wechatBriefSection(sections, "素材观点"))
	boundaries := cleanWechatBriefBlock(wechatBriefSection(sections, "事实与观点边界"))
	verification := cleanWechatBriefBlock(wechatBriefSection(sections, "待核验关键断言"))
	copyright := cleanWechatBriefBlock(wechatBriefSection(sections, "版权风险"))
	if facts == "" {
		facts = "- 现有素材已经形成明确主题，但部分事实细节仍需在发布前按原始来源复核。"
	}
	if views == "" {
		views = "- 真正重要的不是堆砌信息，而是区分已经确认的事实、基于事实的判断，以及会改变判断的新条件。"
	}
	if boundaries == "" {
		boundaries = "- 已确认事实可以直接呈现；推断和建议必须明确标注；无法确认的信息不写成确定结论。"
	}
	alternatives := wechatAlternativeTitles(titleBlock, title)
	if len(alternatives) == 0 {
		alternatives = []string{"看懂关键信号：事实、分歧与下一步", "别急着下结论：先把这几组信息理清", "一篇文章讲清楚：变化在哪里，风险又在哪里"}
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n", title)
	fmt.Fprintf(&out, "> %s\n\n", summary)
	out.WriteString("很多时候，信息并不少，真正稀缺的是把事实、判断和行动条件分开。面对一组看似矛盾的信号，与其急着给出简单答案，不如先回答三个问题：发生了什么、哪些判断有证据、什么变化会推翻当前结论。\n\n")
	out.WriteString("## 先说结论\n\n")
	out.WriteString(views)
	out.WriteString("\n\n")
	out.WriteString("这意味着，读者不需要被单一数字或单一事件牵着走。更稳妥的做法，是把当前结论当作一个可以持续验证的判断框架：证据增强时提高信心，关键条件失效时及时修正。\n\n")
	out.WriteString("## 关键事实：先把底牌摊开\n\n")
	out.WriteString(facts)
	out.WriteString("\n\n")
	out.WriteString("把这些信息放在一起看，比单独放大其中一项更有意义。数据回答的是“现在处在什么位置”，后续变化才回答“这个判断能否持续”。\n\n")
	out.WriteString("## 真正值得关注的，是变化能否持续\n\n")
	out.WriteString("一项改善如果缺少后续数据配合，可能只是阶段性波动；一个看起来积极的事件，如果正文、影响范围和兑现节奏尚未核验，也只能作为观察线索。判断质量取决于证据链是否完整，而不是标题是否足够醒目。\n\n")
	out.WriteString("因此，接下来更值得跟踪的是：核心指标能否延续、关键位置能否得到新的数据确认、公开信息是否改变原有假设，以及风险信号是否先于乐观预期出现。\n\n")
	out.WriteString("## 读者可以怎么做\n\n")
	out.WriteString("1. **先核对事实口径。** 日期、单位、数据来源和计算方式必须一致，避免用不同口径得出强结论。\n")
	out.WriteString("2. **把观点写成条件句。** 不问“会不会一定发生”，而问“什么条件出现时，原判断更可信”。\n")
	out.WriteString("3. **提前写下失效条件。** 当关键事实、趋势或事件与原假设相反时，应更新判断，而不是继续为旧结论寻找理由。\n")
	out.WriteString("4. **保留发布前核验。** 涉及财务、价格、人事、并购、法律和重大公告的表述，应回到原始来源确认。\n\n")
	out.WriteString("## 事实与观点的边界\n\n")
	out.WriteString(boundaries)
	if verification != "" {
		out.WriteString("\n\n### 发布前重点核验\n\n")
		out.WriteString(verification)
	}
	out.WriteString("\n\n## 写在最后\n\n")
	out.WriteString("一篇负责任的内容，不是替读者做决定，而是把证据、分歧和判断条件讲清楚。信息会变化，结论也应允许被新事实修正。真正有价值的关注，不是追逐每一次情绪波动，而是持续跟踪那些能够验证或推翻判断的关键变量。\n\n")
	out.WriteString("如果你希望继续看到这种“结论先行、证据分层、风险说透”的分析，欢迎关注，并把你最想拆解的话题留在评论区。\n\n")
	out.WriteString("---\n\n## 发布素材\n\n")
	out.WriteString("### 备选标题\n\n")
	for i, alternative := range alternatives {
		fmt.Fprintf(&out, "%d. %s\n", i+1, alternative)
	}
	fmt.Fprintf(&out, "\n### 摘要\n\n%s\n\n", summary)
	fmt.Fprintf(&out, "### 封面图提示词\n\n微信公众号横版头图，主题为“%s”，专业编辑插画风，清晰的信息层级，克制配色，留出标题区域，不出现收益承诺、夸张箭头或具体交易指令。\n\n", title)
	out.WriteString("### 文中配图建议\n\n- 一张“关键事实—当前判断—失效条件”三层信息图。\n- 一张按时间或逻辑顺序整理的关键数据图，必须标注日期、单位与来源。\n- 一张催化因素与风险因素的双栏对照图。\n\n")
	out.WriteString("### 版权与事实提醒\n\n")
	if copyright != "" {
		out.WriteString(copyright)
	} else {
		out.WriteString("发布前复核事实、数字、图片授权和原始来源；不大段复制原文，不把待核验信息写成确定事实。")
	}
	return strings.TrimSpace(out.String())
}

func (e *PipelineExecutor) hostFallbackWechatArticleDelivery(ctx context.Context, exec *PipelineExecution, step *PipelineStep, stepIndex int) (string, bool, error) {
	if e == nil || exec == nil || step == nil || exec.PipelineName != "wechat-article" || step.Name != "公众号交付物" {
		return "", false, nil
	}
	materialIndex := pipelineStepResultIndex(exec, "素材准备")
	if materialIndex < 0 || materialIndex >= stepIndex || materialIndex >= len(exec.Steps) {
		return "", false, nil
	}
	brief := strings.TrimSpace(exec.Steps[materialIndex].Output)
	if brief == "" || isPipelineStageFailurePlaceholder(brief) {
		return "", false, nil
	}
	article := buildWechatArticleFallbackMarkdown(brief)
	if utf8.RuneCountInString(article) < 800 {
		return "", true, fmt.Errorf("host WeChat fallback article is too short: %d chars", utf8.RuneCountInString(article))
	}
	workspace := "."
	if e.gateway != nil && e.gateway.config != nil && strings.TrimSpace(e.gateway.config.Workspace) != "" {
		workspace = e.gateway.config.Workspace
	}
	executionID := sanitizePipelineFilename(exec.ID)
	mdPath := ".openkitty/storage/wechat_article_" + executionID + ".md"
	htmlPath := ".openkitty/storage/wechat_article_" + executionID + ".html"
	if step.ArtifactContract != nil {
		for _, path := range pipelineArtifactContractPaths(exec, step.ArtifactContract) {
			switch strings.ToLower(filepath.Ext(path)) {
			case ".md":
				mdPath = path
			case ".html", ".htm":
				htmlPath = path
			}
		}
	}
	if existing, found, err := e.existingWechatArticleDelivery(exec, mdPath, htmlPath); found {
		return existing, true, err
	}
	params := map[string]interface{}{
		"markdown":     article,
		"md_path":      mdPath,
		"html_path":    htmlPath,
		"execution_id": executionID,
	}
	if snapshot := latestPipelineSourceSnapshot(exec, stepIndex); snapshot != nil && strings.TrimSpace(snapshot.SHA256) != "" {
		params["source_sha256"] = snapshot.SHA256
	}
	started := time.Now()
	receiptJSON, err := contenttools.NewWechatArticleBundleTool(workspace).Execute(ctx, params)
	if err != nil {
		return "", true, err
	}
	e.recordHostPipelineToolCall(exec, stepIndex, started, "wechat_article_bundle", map[string]interface{}{
		"execution_id": executionID, "md_path": mdPath, "html_path": htmlPath, "host_fallback": true,
	}, receiptJSON)
	var receipt contenttools.WechatArticleBundleReceipt
	if err := json.Unmarshal([]byte(receiptJSON), &receipt); err != nil {
		return "", true, fmt.Errorf("decode host WeChat fallback receipt: %w", err)
	}
	return fmt.Sprintf("%s\n\n---\n\n## 交付文件\n\n- Markdown：%s\n- 微信兼容 HTML：%s\n- 封面 PNG：%s\n- HTML 实际图片：%d（内嵌 %d，远程 %d）\n- Markdown SHA-256：%s\n- HTML SHA-256：%s", article, receipt.MDPath, receipt.HTMLPath, receipt.CoverPath, receipt.ImageCount, receipt.Embedded, receipt.Remote, receipt.MDSHA256, receipt.HTMLSHA256), true, nil
}

func (e *PipelineExecutor) existingWechatArticleDelivery(exec *PipelineExecution, mdPath, htmlPath string) (string, bool, error) {
	mdFullPath, mdOK := e.storageArtifactPath(mdPath)
	htmlFullPath, htmlOK := e.storageArtifactPath(htmlPath)
	if !mdOK || !htmlOK {
		return "", true, fmt.Errorf("WeChat fallback artifact path is outside storage")
	}
	mdData, mdErr := os.ReadFile(mdFullPath)
	htmlData, htmlErr := os.ReadFile(htmlFullPath)
	mdMissing := os.IsNotExist(mdErr)
	htmlMissing := os.IsNotExist(htmlErr)
	if mdMissing && htmlMissing {
		return "", false, nil
	}
	if mdErr != nil || htmlErr != nil {
		return "", true, fmt.Errorf("existing WeChat delivery bundle is incomplete: markdown=%v html=%v", mdErr, htmlErr)
	}
	article := strings.TrimSpace(wechatProvenanceLinePattern.ReplaceAllString(string(mdData), ""))
	if utf8.RuneCountInString(article) < 800 {
		return "", true, fmt.Errorf("existing WeChat delivery article is too short: %d chars", utf8.RuneCountInString(article))
	}
	imageCount := wechatRenderableImageCount(string(htmlData))
	if imageCount < 1 {
		return "", true, fmt.Errorf("existing WeChat delivery HTML has no renderable image")
	}
	mdHash := sha256.Sum256(mdData)
	htmlHash := sha256.Sum256(htmlData)
	coverPath := e.existingWechatArticleCoverPath(mdPath, mdData)
	return fmt.Sprintf("%s\n\n---\n\n## 交付文件\n\n- Markdown：%s\n- 微信兼容 HTML：%s\n- 封面 PNG：%s\n- HTML 实际图片：%d\n- Markdown SHA-256：%s\n- HTML SHA-256：%s", article, filepath.ToSlash(mdPath), filepath.ToSlash(htmlPath), coverPath, imageCount, hex.EncodeToString(mdHash[:]), hex.EncodeToString(htmlHash[:])), true, nil
}

func (e *PipelineExecutor) existingWechatArticleCoverPath(mdPath string, mdData []byte) string {
	match := wechatGeneratedCoverPattern.FindSubmatch(mdData)
	if len(match) != 2 {
		return ""
	}
	reference := filepath.FromSlash(strings.TrimSpace(string(match[1])))
	if reference == "" || filepath.IsAbs(reference) {
		return ""
	}
	coverPath := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(mdPath), reference)))
	coverFullPath, ok := e.storageArtifactPath(coverPath)
	if !ok {
		return ""
	}
	info, err := os.Stat(coverFullPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return ""
	}
	return coverPath
}
