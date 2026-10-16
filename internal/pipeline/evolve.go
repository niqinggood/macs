package pipeline

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ====================== Evolution Metrics ======================

type PipelineMetrics struct {
	PipelineName string             `json:"pipeline"`
	TotalRuns    int                `json:"total_runs"`
	SuccessRate  float64            `json:"success_rate"`
	AvgDuration  time.Duration      `json:"avg_duration"`
	StepStats    []StepMetrics      `json:"step_stats"`
	LastEvolved  time.Time          `json:"last_evolved"`
	Suggestions  []EvolveSuggestion `json:"suggestions,omitempty"`
}

type StepMetrics struct {
	StepName       string         `json:"step"`
	AvgDuration    time.Duration  `json:"avg_duration"`
	AvgToolCalls   float64        `json:"avg_tool_calls"`
	FailureRate    float64        `json:"failure_rate"`
	ToolUsage      map[string]int `json:"tool_usage"`       // tool_name → call count
	ToolErrors     map[string]int `json:"tool_errors"`      // tool_name → error count
	ApprovalWaits  int            `json:"approval_waits"`   // times waited for approval
	ApprovalAvgMin float64        `json:"approval_avg_min"` // avg wait time in minutes
}

type EvolveSuggestion struct {
	Type        string  `json:"type"` // "add_retry", "remove_tool", "make_parallel", "adjust_max_iter", "add_approval"
	Step        string  `json:"step"`
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"` // 0-1
	Applied     bool    `json:"applied"`
}

type ExecutionSnapshot struct {
	PipelineName string
	Status       string
	CreatedAt    time.Time
	Steps        []StepSnapshot
}

type StepSnapshot struct {
	Name      string
	Status    string
	Error     string
	ToolCalls []ToolCallSnapshot
}

type ToolCallSnapshot struct {
	ToolName   string
	DurationMs int64
}

// ====================== Evolution Engine ======================

type PipelineEvolver struct {
	metricsFile string
	metrics     map[string]*PipelineMetrics // pipeline_name → metrics
	minRuns     int                         // minimum runs before suggesting
	autoApply   bool                        // auto-apply high-confidence suggestions
	logger      *log.Logger
}

func NewPipelineEvolver(metricsFile string, autoApply bool, logger *log.Logger) *PipelineEvolver {
	ev := &PipelineEvolver{
		metricsFile: metricsFile,
		metrics:     make(map[string]*PipelineMetrics),
		minRuns:     5,
		autoApply:   autoApply,
		logger:      logger,
	}
	ev.load()
	return ev
}

func (ev *PipelineEvolver) load() {
	data, err := os.ReadFile(ev.metricsFile)
	if err != nil {
		return
	}
	json.Unmarshal(data, &ev.metrics)
}

func (ev *PipelineEvolver) save() {
	data, _ := json.MarshalIndent(ev.metrics, "", "  ")
	if dir := strings.TrimSpace(filepath.Dir(ev.metricsFile)); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0755)
	}
	os.WriteFile(ev.metricsFile, data, 0644)
}

// Record records a pipeline execution and updates metrics.
func (ev *PipelineEvolver) Record(exec ExecutionSnapshot) {
	m, ok := ev.metrics[exec.PipelineName]
	if !ok {
		m = &PipelineMetrics{
			PipelineName: exec.PipelineName,
			StepStats:    make([]StepMetrics, len(exec.Steps)),
		}
		ev.metrics[exec.PipelineName] = m
	}

	m.TotalRuns++

	// Calculate duration
	duration := time.Since(exec.CreatedAt)

	// Update success rate
	if exec.Status == "completed" {
		m.SuccessRate = (m.SuccessRate*float64(m.TotalRuns-1) + 1.0) / float64(m.TotalRuns)
	} else {
		m.SuccessRate = (m.SuccessRate * float64(m.TotalRuns-1)) / float64(m.TotalRuns)
	}

	// Rolling average duration
	m.AvgDuration = time.Duration((int64(m.AvgDuration)*(int64(m.TotalRuns)-1) + int64(duration)) / int64(m.TotalRuns))

	// Per-step metrics
	for i, step := range exec.Steps {
		if i >= len(m.StepStats) {
			m.StepStats = append(m.StepStats, StepMetrics{StepName: step.Name})
		}
		sm := &m.StepStats[i]
		sm.StepName = step.Name

		// Tool calls
		if len(step.ToolCalls) > 0 {
			n := float64(len(step.ToolCalls))
			sm.AvgToolCalls = (sm.AvgToolCalls*float64(m.TotalRuns-1) + n) / float64(m.TotalRuns)

			// Average duration from tool calls
			var totalDur int64
			for _, tc := range step.ToolCalls {
				totalDur += tc.DurationMs
				// Track tool usage
				if sm.ToolUsage == nil {
					sm.ToolUsage = make(map[string]int)
				}
				sm.ToolUsage[tc.ToolName]++
			}
			sm.AvgDuration = time.Duration((int64(sm.AvgDuration)*int64(m.TotalRuns-1) + totalDur*int64(time.Millisecond)) / int64(m.TotalRuns))
		}

		// Failure tracking
		if step.Status == "failed" {
			sm.FailureRate = (sm.FailureRate*float64(m.TotalRuns-1) + 1.0) / float64(m.TotalRuns)
			if step.Error != "" {
				if sm.ToolErrors == nil {
					sm.ToolErrors = make(map[string]int)
				}
				sm.ToolErrors["_step_error"]++
			}
		} else {
			sm.FailureRate = (sm.FailureRate * float64(m.TotalRuns-1)) / float64(m.TotalRuns)
		}

		// Approval tracking
		if step.Status == "waiting_approval" {
			sm.ApprovalWaits++
		}
	}

	// Evolve after enough data
	if m.TotalRuns >= ev.minRuns && m.TotalRuns%5 == 0 {
		ev.Evolve(m)
	}

	ev.save()
}

// Evolve analyzes metrics and generates optimization suggestions.
func (ev *PipelineEvolver) Evolve(m *PipelineMetrics) {
	m.Suggestions = nil

	for i := range m.StepStats {
		sm := &m.StepStats[i]

		// Suggestion 1: Remove unused tools (confidence: high)
		for tool, count := range sm.ToolUsage {
			if count <= 1 && m.TotalRuns >= 10 {
				m.Suggestions = append(m.Suggestions, EvolveSuggestion{
					Type:        "remove_tool",
					Step:        sm.StepName,
					Description: fmt.Sprintf("Tool '%s' only used %d times in %d runs — consider removing from whitelist", tool, count, m.TotalRuns),
					Confidence:  0.85,
				})
			}
		}

		// Suggestion 2: Add retry for failing steps (confidence: medium)
		if sm.FailureRate > 0.2 && m.TotalRuns >= 5 {
			m.Suggestions = append(m.Suggestions, EvolveSuggestion{
				Type:        "add_retry",
				Step:        sm.StepName,
				Description: fmt.Sprintf("Step fails %.0f%% of the time — consider adding retry (repeat: max=3)", sm.FailureRate*100),
				Confidence:  0.75,
			})
		}

		// Suggestion 3: Add approval for flaky steps (confidence: medium)
		if sm.FailureRate > 0.15 && sm.ApprovalWaits == 0 {
			m.Suggestions = append(m.Suggestions, EvolveSuggestion{
				Type:        "add_approval",
				Step:        sm.StepName,
				Description: fmt.Sprintf("Step fails %.0f%% without human review — consider require_approval: true", sm.FailureRate*100),
				Confidence:  0.7,
			})
		}

		// Suggestion 4: High tool count → suggest reduce max_iter
		if sm.AvgToolCalls > 8 {
			m.Suggestions = append(m.Suggestions, EvolveSuggestion{
				Type:        "adjust_max_iter",
				Step:        sm.StepName,
				Description: fmt.Sprintf("Step averages %.0f tool calls — consider reducing max_iter or splitting into sub-steps", sm.AvgToolCalls),
				Confidence:  0.6,
			})
		}
	}

	// Suggestion 5: Parallel opportunities
	for i := 0; i < len(m.StepStats)-1; i++ {
		a, b := &m.StepStats[i], &m.StepStats[i+1]
		// Adjacent steps with different tool sets and no approval between them
		toolsA := toolSet(a.ToolUsage)
		toolsB := toolSet(b.ToolUsage)
		if len(toolsA) > 0 && len(toolsB) > 0 && !hasOverlap(toolsA, toolsB) && a.ApprovalWaits == 0 {
			m.Suggestions = append(m.Suggestions, EvolveSuggestion{
				Type:        "make_parallel",
				Step:        fmt.Sprintf("%s + %s", a.StepName, b.StepName),
				Description: fmt.Sprintf("Steps use different tools and no approval between them — consider running in parallel to save %.0fs", (a.AvgDuration+b.AvgDuration).Seconds()*0.4),
				Confidence:  0.5,
			})
		}
	}

	// Sort by confidence
	sort.Slice(m.Suggestions, func(i, j int) bool {
		return m.Suggestions[i].Confidence > m.Suggestions[j].Confidence
	})

	m.LastEvolved = time.Now()

	// Auto-apply high confidence suggestions
	if ev.autoApply {
		for _, s := range m.Suggestions {
			if s.Confidence >= 0.8 && !s.Applied {
				ev.logger.Printf("[Evolve] AUTO-APPLY: %s — %s", s.Step, s.Description)
				s.Applied = true
			}
		}
	}

	if len(m.Suggestions) > 0 {
		ev.logger.Printf("[Evolve] %s: %d suggestions after %d runs", m.PipelineName, len(m.Suggestions), m.TotalRuns)
	}
}

// GetSuggestions returns evolution suggestions for a pipeline.
func (ev *PipelineEvolver) GetSuggestions(pipelineName string) []EvolveSuggestion {
	if m, ok := ev.metrics[pipelineName]; ok {
		return m.Suggestions
	}
	return nil
}

// GetMetrics returns metrics for a pipeline.
func (ev *PipelineEvolver) GetMetrics(pipelineName string) *PipelineMetrics {
	return ev.metrics[pipelineName]
}

// AllMetrics returns all pipeline metrics.
func (ev *PipelineEvolver) AllMetrics() map[string]*PipelineMetrics {
	return ev.metrics
}

func toolSet(usage map[string]int) map[string]bool {
	s := make(map[string]bool)
	for k := range usage {
		s[k] = true
	}
	return s
}

func hasOverlap(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

// SaveConfig updates the pipeline config in config.yaml with optimized params.
func (ev *PipelineEvolver) SaveConfig(configPath string, m *PipelineMetrics) error {
	// Read current config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	content := string(data)

	// Apply high-confidence auto-apply suggestions
	for _, s := range m.Suggestions {
		if !s.Applied || s.Confidence < 0.8 {
			continue
		}
		switch s.Type {
		case "add_retry":
			// Add repeat config to the step
			marker := fmt.Sprintf("name: \"%s\"", s.Step)
			if idx := strings.Index(content, marker); idx >= 0 {
				indent := "        "
				retryBlock := fmt.Sprintf("%srepeat:\n%smax: 3\n%scondition: \"contains '失败'\"", indent, indent+"  ", indent+"  ")
				content = content[:idx+len(marker)] + "\n" + retryBlock + content[idx+len(marker):]
			}
		case "remove_tool":
			// Remove unused tool from tools list
			parts := strings.SplitN(s.Description, "'", 3)
			if len(parts) >= 3 {
				toolName := parts[1]
				content = strings.ReplaceAll(content, fmt.Sprintf("\"%s\",", toolName), "")
				content = strings.ReplaceAll(content, fmt.Sprintf(", \"%s\"", toolName), "")
			}
		}
	}

	return os.WriteFile(configPath, []byte(content), 0644)
}
