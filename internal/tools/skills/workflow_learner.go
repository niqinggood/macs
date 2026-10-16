package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mac/internal/fsutil"
	"mac/internal/toolcontract"
	"mac/internal/workflowlearn"
)

const workflowRecipeFile = "recipe.json"

type WorkflowLearnerRuntime struct {
	LoadComputerSession func(sessionID string) (string, error)
	ExecuteComputer     func(context.Context, map[string]interface{}) (string, error)
}

// WorkflowLearnerTool compiles a successful computer session into a reusable
// Skill, replays it with new inputs, and evaluates later runs.
type WorkflowLearnerTool struct {
	skillsDir string
	runtime   WorkflowLearnerRuntime
}

func NewWorkflowLearnerTool(skillsDir string, runtime WorkflowLearnerRuntime) *WorkflowLearnerTool {
	return &WorkflowLearnerTool{skillsDir: skillsDir, runtime: runtime}
}

func (t *WorkflowLearnerTool) Name() string { return "workflow_learner" }

func (t *WorkflowLearnerTool) Description() string {
	return "Turn a successful browser or desktop computer session into a parameterized reusable Skill, replay it with new inputs, and evaluate the result. " +
		"Use preview before save when learning a new LIMS, enterprise form-filling, or document workflow."
}

func (t *WorkflowLearnerTool) ToolSearchMetadata() toolcontract.ToolSearchMetadata {
	return toolcontract.ToolSearchMetadata{
		Aliases: []string{"record and replay", "workflow recorder", "learn workflow", "teach once", "操作录制", "流程学习", "演示生成技能", "录制回放"},
		Tags:    []string{"workflow", "computer-use", "LIMS", "form filling", "skill generation", "evaluation", "automation"},
	}
}

func (t *WorkflowLearnerTool) IsNeverParallel() bool { return true }

func (t *WorkflowLearnerTool) IsReadOnlyForCall(params map[string]interface{}) bool {
	action := strings.ToLower(strings.TrimSpace(stringValue(params, "action")))
	return action == "preview" || action == "inspect" || action == "list" ||
		(action == "evaluate" && !boolValue(params, "save_report", true))
}

func (t *WorkflowLearnerTool) MaxResultSize() int { return 16000 }

func (t *WorkflowLearnerTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string", "enum": []string{"preview", "save", "replay", "evaluate", "inspect", "list"},
				"description": "preview compiles without writing; save creates the Skill; replay runs it; evaluate scores one or more sessions.",
			},
			"name": map[string]interface{}{
				"type": "string", "description": "Reusable workflow/Skill name, preferably kebab-case.",
			},
			"description": map[string]interface{}{
				"type": "string", "description": "When this learned workflow should be used.",
			},
			"session_id": map[string]interface{}{
				"type": "string", "description": "Successful computer session used as a demonstration or evaluation run.",
			},
			"session_ids": map[string]interface{}{
				"type": "array", "items": map[string]interface{}{"type": "string"},
				"description": "Multiple computer sessions for repeated evaluation.",
			},
			"surface": map[string]interface{}{
				"type": "string", "enum": []string{"browser", "desktop"},
				"description": "Required only for a manually supplied demonstration.",
			},
			"objective": map[string]interface{}{
				"type": "string", "description": "Workflow goal for a manually supplied demonstration.",
			},
			"url": map[string]interface{}{
				"type": "string", "description": "Stable start URL for a manually supplied browser demonstration.",
			},
			"steps": map[string]interface{}{
				"type":        "array",
				"description": "Optional manual trace steps. Prefer session_id so recorded arguments and evidence are preserved.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"operation": map[string]interface{}{"type": "string"},
						"status":    map[string]interface{}{"type": "string"},
						"arguments": map[string]interface{}{"type": "object", "additionalProperties": true},
						"output":    map[string]interface{}{"type": "string"},
						"error":     map[string]interface{}{"type": "string"},
					},
					"required": []string{"operation"},
				},
			},
			"variables": map[string]interface{}{
				"type":        "array",
				"description": "Optional names for demonstrated literal values. Values are used only during compilation and are not saved.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name":        map[string]interface{}{"type": "string"},
						"value":       map[string]interface{}{"type": "string"},
						"description": map[string]interface{}{"type": "string"},
						"required":    map[string]interface{}{"type": "boolean"},
					},
					"required": []string{"name", "value"},
				},
			},
			"assertions": map[string]interface{}{
				"type":        "array",
				"description": "Success evidence such as visible_text or active_window.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"kind":     map[string]interface{}{"type": "string"},
						"expected": map[string]interface{}{"type": "string"},
					},
					"required": []string{"kind", "expected"},
				},
			},
			"inputs": map[string]interface{}{
				"type": "object", "additionalProperties": true,
				"description": "Input values used for replay. Keys must match the recipe input definitions.",
			},
			"overwrite": map[string]interface{}{
				"type": "boolean", "description": "Create a new revision when the workflow already exists.",
			},
			"dry_run": map[string]interface{}{
				"type": "boolean", "description": "Resolve replay steps without operating the computer.",
			},
			"evaluate": map[string]interface{}{
				"type": "boolean", "description": "Evaluate the session after replay. Default true.",
			},
			"case_name": map[string]interface{}{
				"type": "string", "description": "Label for an evaluation case, such as sample type or template version.",
			},
			"save_report": map[string]interface{}{
				"type": "boolean", "description": "Persist the evaluation report beside the Skill. Default true.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *WorkflowLearnerTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action := strings.ToLower(strings.TrimSpace(stringValue(params, "action")))
	switch action {
	case "preview", "save":
		return t.compile(params, action == "save")
	case "replay":
		return t.replay(ctx, params)
	case "evaluate":
		return t.evaluate(params)
	case "inspect":
		return t.inspect(params)
	case "list":
		return t.list()
	default:
		return "", fmt.Errorf("unknown workflow_learner action %q", action)
	}
}

func (t *WorkflowLearnerTool) compile(params map[string]interface{}, save bool) (string, error) {
	trace, err := t.traceFromParams(params)
	if err != nil {
		return "", err
	}
	variables, err := decodeJSONSlice[workflowlearn.VariableHint](params["variables"])
	if err != nil {
		return "", fmt.Errorf("decode variables: %w", err)
	}
	for index := range variables {
		if raw, ok := params["variables"].([]interface{}); ok && index < len(raw) {
			if object, ok := raw[index].(map[string]interface{}); ok {
				if _, exists := object["required"]; !exists {
					variables[index].Required = true
				}
			}
		}
	}
	assertions, err := decodeJSONSlice[workflowlearn.Assertion](params["assertions"])
	if err != nil {
		return "", fmt.Errorf("decode assertions: %w", err)
	}
	recipe, err := workflowlearn.Compile(workflowlearn.CompileRequest{
		Name:        stringValue(params, "name"),
		Description: stringValue(params, "description"),
		Trace:       trace,
		Variables:   variables,
		Assertions:  assertions,
	})
	if err != nil {
		return "", err
	}
	result := map[string]interface{}{
		"ok":        true,
		"saved":     false,
		"recipe":    recipe,
		"readiness": recipeReadiness(recipe),
	}
	if save {
		path, err := t.saveRecipe(&recipe, boolValue(params, "overwrite", false))
		if err != nil {
			return "", err
		}
		result["saved"] = true
		result["skill_path"] = filepath.Join(path, "SKILL.md")
		result["recipe_path"] = filepath.Join(path, workflowRecipeFile)
		result["next"] = "Reload Skills, then replay with workflow_learner(action=replay, name=..., inputs=...)."
	}
	return encodeJSON(result)
}

func (t *WorkflowLearnerTool) traceFromParams(params map[string]interface{}) (workflowlearn.Trace, error) {
	if sessionID := strings.TrimSpace(stringValue(params, "session_id")); sessionID != "" {
		if t.runtime.LoadComputerSession == nil {
			return workflowlearn.Trace{}, errors.New("computer session integration is unavailable")
		}
		raw, err := t.runtime.LoadComputerSession(sessionID)
		if err != nil {
			return workflowlearn.Trace{}, err
		}
		var trace workflowlearn.Trace
		if err := json.Unmarshal([]byte(raw), &trace); err != nil {
			return workflowlearn.Trace{}, fmt.Errorf("decode computer session: %w", err)
		}
		return trace, nil
	}
	steps, err := decodeJSONSlice[workflowlearn.TraceStep](params["steps"])
	if err != nil {
		return workflowlearn.Trace{}, fmt.Errorf("decode steps: %w", err)
	}
	if len(steps) == 0 {
		return workflowlearn.Trace{}, errors.New("session_id or at least one manual step is required")
	}
	return workflowlearn.Trace{
		ID:        "manual",
		Surface:   stringValue(params, "surface"),
		Objective: stringValue(params, "objective"),
		URL:       stringValue(params, "url"),
		Steps:     steps,
	}, nil
}

func (t *WorkflowLearnerTool) saveRecipe(recipe *workflowlearn.Recipe, overwrite bool) (string, error) {
	dir, err := t.recipeDir(recipe.Name)
	if err != nil {
		return "", err
	}
	existing, loadErr := t.loadRecipe(recipe.Name)
	if loadErr == nil {
		if !overwrite {
			return "", fmt.Errorf("workflow %q already exists; set overwrite=true to create a new revision", recipe.Name)
		}
		recipe.Revision = existing.Revision + 1
		recipe.CreatedAt = existing.CreatedAt
		recipe.UpdatedAt = time.Now().UTC()
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return "", loadErr
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create workflow skill directory: %w", err)
	}
	recipeData, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode workflow recipe: %w", err)
	}
	if err := fsutil.WriteAtomic(filepath.Join(dir, workflowRecipeFile), append(recipeData, '\n'), 0644); err != nil {
		return "", fmt.Errorf("save workflow recipe: %w", err)
	}
	if err := fsutil.WriteAtomic(filepath.Join(dir, "SKILL.md"), []byte(workflowlearn.RenderSkill(*recipe)), 0644); err != nil {
		return "", fmt.Errorf("save workflow skill: %w", err)
	}
	metadata, _ := json.MarshalIndent(map[string]interface{}{
		"created_at": recipe.CreatedAt,
		"updated_at": recipe.UpdatedAt,
		"version":    recipe.Version,
		"revision":   recipe.Revision,
		"source":     "workflow_learner",
	}, "", "  ")
	if err := fsutil.WriteAtomic(filepath.Join(dir, "_workflow_meta.json"), append(metadata, '\n'), 0644); err != nil {
		return "", fmt.Errorf("save workflow metadata: %w", err)
	}
	return dir, nil
}

func (t *WorkflowLearnerTool) replay(ctx context.Context, params map[string]interface{}) (string, error) {
	recipe, err := t.loadRecipe(stringValue(params, "name"))
	if err != nil {
		return "", err
	}
	inputs, _ := params["inputs"].(map[string]interface{})
	if inputs == nil {
		inputs = map[string]interface{}{}
	}
	resolvedRecipe, err := workflowlearn.ResolveRecipe(recipe, inputs)
	if err != nil {
		return "", err
	}
	steps := resolvedRecipe.Steps
	if boolValue(params, "dry_run", false) {
		return encodeJSON(map[string]interface{}{"ok": true, "dry_run": true, "recipe": recipe.Name, "steps": steps, "assertions": resolvedRecipe.Assertions})
	}
	if t.runtime.ExecuteComputer == nil || t.runtime.LoadComputerSession == nil {
		return "", errors.New("computer replay integration is unavailable")
	}
	var sessionID string
	var batches []interface{}
	for offset := 0; offset < len(steps); offset += 16 {
		end := offset + 16
		if end > len(steps) {
			end = len(steps)
		}
		batchSteps := make([]interface{}, 0, end-offset)
		for _, step := range steps[offset:end] {
			arguments := cloneArguments(step.Arguments)
			arguments["operation"] = step.Operation
			batchSteps = append(batchSteps, arguments)
		}
		call := map[string]interface{}{
			"action":        "run",
			"surface":       resolvedRecipe.Surface,
			"objective":     resolvedRecipe.Objective,
			"steps":         batchSteps,
			"observe_after": true,
			"stop_on_error": true,
		}
		if sessionID != "" {
			call["session_id"] = sessionID
		} else if resolvedRecipe.StartURL != "" {
			call["url"] = resolvedRecipe.StartURL
		}
		if end == len(steps) {
			applyComputerAssertions(call, resolvedRecipe.Assertions)
		}
		raw, executeErr := t.runtime.ExecuteComputer(ctx, call)
		var response map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &response); err != nil {
			return "", fmt.Errorf("decode computer replay result: %w", err)
		}
		batches = append(batches, response)
		if session, ok := response["session"].(map[string]interface{}); ok {
			sessionID, _ = session["id"].(string)
		}
		if executeErr != nil {
			return encodeJSON(map[string]interface{}{"ok": false, "session_id": sessionID, "batches": batches, "error": executeErr.Error()})
		}
		if ok, exists := response["ok"].(bool); exists && !ok {
			return encodeJSON(map[string]interface{}{"ok": false, "session_id": sessionID, "batches": batches})
		}
	}
	result := map[string]interface{}{"ok": true, "session_id": sessionID, "batches": batches}
	if boolValue(params, "evaluate", true) && sessionID != "" {
		report, reportPath, err := t.evaluateOne(resolvedRecipe, sessionID, stringValue(params, "case_name"), boolValue(params, "save_report", true))
		if err != nil {
			return "", err
		}
		result["evaluation"] = report
		if reportPath != "" {
			result["evaluation_path"] = reportPath
		}
		result["ok"] = report.Passed
	}
	return encodeJSON(result)
}

func (t *WorkflowLearnerTool) evaluate(params map[string]interface{}) (string, error) {
	recipe, err := t.loadRecipe(stringValue(params, "name"))
	if err != nil {
		return "", err
	}
	if inputs, ok := params["inputs"].(map[string]interface{}); ok && len(inputs) > 0 {
		recipe, err = workflowlearn.ResolveRecipe(recipe, inputs)
		if err != nil {
			return "", err
		}
	}
	sessionIDs := stringSlice(params["session_ids"])
	if sessionID := strings.TrimSpace(stringValue(params, "session_id")); sessionID != "" {
		sessionIDs = append([]string{sessionID}, sessionIDs...)
	}
	sessionIDs = uniqueNonEmpty(sessionIDs)
	if len(sessionIDs) == 0 {
		return "", errors.New("session_id or session_ids is required for evaluation")
	}
	save := boolValue(params, "save_report", true)
	runs := make([]workflowlearn.EvaluationReport, 0, len(sessionIDs))
	paths := make([]string, 0, len(sessionIDs))
	for index, sessionID := range sessionIDs {
		caseName := strings.TrimSpace(stringValue(params, "case_name"))
		if caseName == "" && len(sessionIDs) > 1 {
			caseName = fmt.Sprintf("run-%d", index+1)
		}
		report, path, err := t.evaluateOne(recipe, sessionID, caseName, save)
		if err != nil {
			return "", err
		}
		runs = append(runs, report)
		if path != "" {
			paths = append(paths, path)
		}
	}
	passed := 0
	totalScore := 0.0
	minScore := 100.0
	for _, run := range runs {
		totalScore += run.Score
		if run.Score < minScore {
			minScore = run.Score
		}
		if run.Passed {
			passed++
		}
	}
	return encodeJSON(map[string]interface{}{
		"ok":     passed == len(runs),
		"recipe": recipe.Name,
		"runs":   runs,
		"summary": map[string]interface{}{
			"total_runs": len(runs), "passed_runs": passed,
			"pass_rate":  float64(passed) / float64(len(runs)),
			"mean_score": totalScore / float64(len(runs)), "min_score": minScore,
		},
		"report_paths": paths,
	})
}

func (t *WorkflowLearnerTool) evaluateOne(recipe workflowlearn.Recipe, sessionID, caseName string, save bool) (workflowlearn.EvaluationReport, string, error) {
	if t.runtime.LoadComputerSession == nil {
		return workflowlearn.EvaluationReport{}, "", errors.New("computer session integration is unavailable")
	}
	raw, err := t.runtime.LoadComputerSession(sessionID)
	if err != nil {
		return workflowlearn.EvaluationReport{}, "", err
	}
	var trace workflowlearn.Trace
	if err := json.Unmarshal([]byte(raw), &trace); err != nil {
		return workflowlearn.EvaluationReport{}, "", fmt.Errorf("decode evaluation session: %w", err)
	}
	report := workflowlearn.Evaluate(recipe, trace, caseName, time.Now().UTC())
	if !save {
		return report, "", nil
	}
	dir, err := t.recipeDir(recipe.Name)
	if err != nil {
		return workflowlearn.EvaluationReport{}, "", err
	}
	evalDir := filepath.Join(dir, "evals")
	if err := os.MkdirAll(evalDir, 0755); err != nil {
		return workflowlearn.EvaluationReport{}, "", fmt.Errorf("create workflow eval directory: %w", err)
	}
	label := workflowlearn.NormalizeName(caseName)
	if label == "" {
		label = "run"
	}
	path := filepath.Join(evalDir, time.Now().UTC().Format("20060102T150405.000000000Z")+"-"+label+".json")
	data, _ := json.MarshalIndent(report, "", "  ")
	if err := fsutil.WriteAtomic(path, append(data, '\n'), 0644); err != nil {
		return workflowlearn.EvaluationReport{}, "", fmt.Errorf("save workflow evaluation: %w", err)
	}
	return report, path, nil
}

func (t *WorkflowLearnerTool) inspect(params map[string]interface{}) (string, error) {
	recipe, err := t.loadRecipe(stringValue(params, "name"))
	if err != nil {
		return "", err
	}
	return encodeJSON(map[string]interface{}{"ok": true, "recipe": recipe, "readiness": recipeReadiness(recipe)})
}

func (t *WorkflowLearnerTool) list() (string, error) {
	entries, err := os.ReadDir(t.skillsDir)
	if errors.Is(err, os.ErrNotExist) {
		return encodeJSON(map[string]interface{}{"ok": true, "workflows": []interface{}{}})
	}
	if err != nil {
		return "", fmt.Errorf("list workflow skills: %w", err)
	}
	workflows := make([]map[string]interface{}, 0)
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		recipe, err := t.loadRecipe(entry.Name())
		if err != nil {
			continue
		}
		workflows = append(workflows, map[string]interface{}{
			"name": recipe.Name, "description": recipe.Description, "surface": recipe.Surface,
			"revision": recipe.Revision, "inputs": len(recipe.Inputs), "steps": len(recipe.Steps),
			"assertions": len(recipe.Assertions), "updated_at": recipe.UpdatedAt,
		})
	}
	sort.Slice(workflows, func(i, j int) bool { return fmt.Sprint(workflows[i]["name"]) < fmt.Sprint(workflows[j]["name"]) })
	return encodeJSON(map[string]interface{}{"ok": true, "workflows": workflows})
}

func (t *WorkflowLearnerTool) loadRecipe(name string) (workflowlearn.Recipe, error) {
	dir, err := t.recipeDir(name)
	if err != nil {
		return workflowlearn.Recipe{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, workflowRecipeFile))
	if err != nil {
		return workflowlearn.Recipe{}, err
	}
	var recipe workflowlearn.Recipe
	if err := json.Unmarshal(data, &recipe); err != nil {
		return workflowlearn.Recipe{}, fmt.Errorf("decode workflow recipe %q: %w", name, err)
	}
	if recipe.Version != workflowlearn.RecipeVersion || recipe.Name == "" || len(recipe.Steps) == 0 {
		return workflowlearn.Recipe{}, fmt.Errorf("workflow recipe %q is invalid or unsupported", name)
	}
	return recipe, nil
}

func (t *WorkflowLearnerTool) recipeDir(name string) (string, error) {
	name = workflowlearn.NormalizeName(name)
	if name == "" || filepath.Base(name) != name {
		return "", errors.New("workflow name is required")
	}
	return filepath.Join(t.skillsDir, name), nil
}

func applyComputerAssertions(call map[string]interface{}, assertions []workflowlearn.Assertion) {
	for _, assertion := range assertions {
		switch strings.ToLower(assertion.Kind) {
		case "visible_text", "expect_text", "text":
			if call["expect_text"] == nil {
				call["expect_text"] = assertion.Expected
			}
		case "active_window", "expect_window", "window":
			if call["expect_window"] == nil {
				call["expect_window"] = assertion.Expected
			}
		}
	}
}

func recipeReadiness(recipe workflowlearn.Recipe) map[string]interface{} {
	stable := len(recipe.Warnings) == 0
	score := 100 - len(recipe.Warnings)*10
	if len(recipe.Assertions) == 0 {
		score -= 20
		stable = false
	}
	if score < 0 {
		score = 0
	}
	return map[string]interface{}{
		"score": score, "ready_for_unattended_replay": stable,
		"warning_count": len(recipe.Warnings), "has_success_assertions": len(recipe.Assertions) > 0,
	}
}

func decodeJSONSlice[T any](value interface{}) ([]T, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result []T
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func cloneArguments(arguments map[string]interface{}) map[string]interface{} {
	data, _ := json.Marshal(arguments)
	var clone map[string]interface{}
	_ = json.Unmarshal(data, &clone)
	if clone == nil {
		clone = make(map[string]interface{})
	}
	return clone
}

func stringValue(params map[string]interface{}, name string) string {
	value, _ := params[name].(string)
	return value
}

func boolValue(params map[string]interface{}, name string, fallback bool) bool {
	value, exists := params[name].(bool)
	if !exists {
		return fallback
	}
	return value
}

func stringSlice(value interface{}) []string {
	items, ok := value.([]interface{})
	if !ok {
		if strings, ok := value.([]string); ok {
			return append([]string(nil), strings...)
		}
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func encodeJSON(value interface{}) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
