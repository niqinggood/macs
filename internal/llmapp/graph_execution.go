package llmapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrLLMAppEffectOutcomeUnknown = errors.New("llm_app effect outcome is unknown")

type llmAppGraphNodeState struct {
	NodeID               string                   `json:"node_id"`
	NodeType             string                   `json:"node_type"`
	Status               string                   `json:"status"`
	EffectID             string                   `json:"effect_id,omitempty"`
	ReplayPolicy         string                   `json:"replay_policy,omitempty"`
	ValidationAttempt    int                      `json:"validation_attempt,omitempty"`
	Messages             []map[string]interface{} `json:"messages,omitempty"`
	Output               interface{}              `json:"output,omitempty"`
	Error                string                   `json:"error,omitempty"`
	RecoveredEffectCount int                      `json:"recovered_effect_count,omitempty"`
	CreatedAt            time.Time                `json:"created_at"`
	UpdatedAt            time.Time                `json:"updated_at"`
}

func (m *LLMAppManager) graphNodeStatePath(runID, nodeID string) string {
	sum := sha256.Sum256([]byte(nodeID))
	return filepath.Join(m.runDir(runID), "nodes", hex.EncodeToString(sum[:8])+".json")
}

func (m *LLMAppManager) readGraphNodeState(runID, nodeID string) (llmAppGraphNodeState, error) {
	var state llmAppGraphNodeState
	err := readLLMAppJSON(m.graphNodeStatePath(runID, nodeID), &state)
	return state, err
}

func (m *LLMAppManager) writeGraphNodeState(run *llmAppManagedRun, state llmAppGraphNodeState) error {
	now := time.Now()
	if state.CreatedAt.IsZero() {
		state.CreatedAt = now
	}
	state.UpdatedAt = now
	return m.writeRunJSON(run, m.graphNodeStatePath(run.snapshot().RunID, state.NodeID), state)
}

func (m *LLMAppManager) executeGraph(
	ctx context.Context,
	run *llmAppManagedRun,
	definition LLMAppDefinition,
	request llmAppRunRequest,
	runtime Runtime,
) (interface{}, error) {
	if definition.Graph == nil {
		return nil, errors.New("graph definition is unavailable")
	}
	ordered, err := llmAppGraphTopologicalOrder(*definition.Graph)
	if err != nil {
		return nil, err
	}
	runID := run.snapshot().RunID
	values := make(map[string]interface{}, len(ordered))
	states := make(map[string]llmAppGraphNodeState, len(ordered))
	completed := 0
	for _, node := range ordered {
		state, readErr := m.readGraphNodeState(runID, node.ID)
		if os.IsNotExist(readErr) {
			state = llmAppGraphNodeState{NodeID: node.ID, NodeType: node.Type, Status: llmAppItemPending}
			if err := m.writeGraphNodeState(run, state); err != nil {
				return nil, err
			}
		} else if readErr != nil {
			return nil, readErr
		}
		if state.Status == llmAppItemEffectPending {
			if state.ReplayPolicy == llmAppReplayNever {
				return nil, fmt.Errorf(
					"%w: graph node %s effect %s is non-replayable; reconcile it before starting a replacement run",
					ErrLLMAppEffectOutcomeUnknown, node.ID, state.EffectID,
				)
			}
			state.Status = llmAppItemPending
			state.RecoveredEffectCount++
			if node.Type == llmAppGraphModel {
				run.mu.Lock()
				run.record.Usage.UnknownModelCalls++
				run.mu.Unlock()
			}
			if err := m.writeGraphNodeState(run, state); err != nil {
				return nil, err
			}
		}
		if state.Status == llmAppItemSettled {
			if state.Error != "" {
				return nil, fmt.Errorf("graph node %s previously failed: %s", node.ID, state.Error)
			}
			values[node.ID] = state.Output
			completed++
		}
		states[node.ID] = state
	}
	m.updateRun(run, func(record *LLMAppRunRecord) {
		record.Progress = LLMAppProgress{Completed: completed, Total: len(ordered)}
		if len(ordered) > 0 {
			record.Progress.Percent = float64(completed) * 100 / float64(len(ordered))
		}
		record.Checkpoint = fmt.Sprintf("%d/%d", completed, len(ordered))
	})

	for _, node := range ordered {
		state := states[node.ID]
		if state.Status == llmAppItemSettled {
			continue
		}
		if err := m.waitUntilRunnable(ctx, run); err != nil {
			return nil, err
		}
		m.updateRun(run, func(record *LLMAppRunRecord) {
			record.Phase = "graph"
			record.CurrentItem = node.ID
			record.LastProgressAt = time.Now()
		})
		m.emitRunEvent(run, llmAppEventGraphNodeStarted, "worker", "graph node started", map[string]interface{}{
			"node_id": node.ID, "node_type": node.Type,
		})
		input := graphDependencyValue(node, values, request)
		output, executeErr := m.executeGraphNode(ctx, run, definition, request, runtime, node, input, values, state)
		state, readErr := m.readGraphNodeState(runID, node.ID)
		if readErr != nil {
			return nil, readErr
		}
		if executeErr != nil {
			if state.Status != llmAppItemEffectPending || state.ReplayPolicy != llmAppReplayNever {
				state.Status = llmAppItemSettled
				state.Error = executeErr.Error()
				state.Output = nil
				state.Messages = nil
				state.EffectID = ""
				state.ReplayPolicy = ""
				if writeErr := m.writeGraphNodeState(run, state); writeErr != nil {
					return nil, errors.Join(executeErr, writeErr)
				}
			}
			m.emitRunEvent(run, llmAppEventGraphNodeFailed, "worker", "graph node failed", map[string]interface{}{
				"node_id": node.ID, "node_type": node.Type, "error": executeErr.Error(),
			})
			return nil, fmt.Errorf("graph node %s: %w", node.ID, executeErr)
		}
		state.Status = llmAppItemSettled
		state.Output = output
		state.Error = ""
		state.Messages = nil
		state.EffectID = ""
		state.ReplayPolicy = ""
		if err := m.writeGraphNodeState(run, state); err != nil {
			return nil, err
		}
		values[node.ID] = output
		states[node.ID] = state
		completed++
		m.updateRun(run, func(record *LLMAppRunRecord) {
			record.Progress.Completed = completed
			record.Progress.Total = len(ordered)
			record.Progress.Percent = float64(completed) * 100 / float64(len(ordered))
			record.Checkpoint = fmt.Sprintf("%d/%d", completed, len(ordered))
			record.LastProgressAt = time.Now()
		})
		m.emitRunEvent(run, llmAppEventGraphNodeCompleted, "worker", "graph node completed", map[string]interface{}{
			"node_id": node.ID, "node_type": node.Type, "completed": completed, "total": len(ordered),
		})
	}
	output := values[definition.Graph.OutputNode]
	if err := validateLLMAppJSONValue(definition.OutputSchema, output); err != nil {
		return nil, fmt.Errorf("graph output validation failed: %w", err)
	}
	return output, nil
}

func (m *LLMAppManager) executeGraphNode(
	ctx context.Context,
	run *llmAppManagedRun,
	definition LLMAppDefinition,
	request llmAppRunRequest,
	runtime Runtime,
	node LLMAppGraphNode,
	input interface{},
	values map[string]interface{},
	state llmAppGraphNodeState,
) (interface{}, error) {
	switch node.Type {
	case llmAppGraphSource:
		switch node.Source {
		case "input":
			return request.Input, nil
		case "items":
			return request.Items, nil
		case "file":
			data, err := os.ReadFile(request.File)
			if err != nil {
				return nil, err
			}
			return string(data), nil
		}
	case llmAppGraphModel:
		return m.executeGraphModelNode(ctx, run, definition, request, runtime, node, input, values, state)
	case llmAppGraphTool:
		arguments, err := resolveLLMAppGraphValue(node.Arguments, request, input, values)
		if err != nil {
			return nil, err
		}
		argumentMap, ok := arguments.(map[string]interface{})
		if !ok {
			return nil, errors.New("resolved tool arguments are not an object")
		}
		policy, err := runtime.resolveToolPolicy(node.Tool, argumentMap)
		if err != nil {
			return nil, err
		}
		replayPolicy := llmAppReplayNever
		if policy.ReadOnly {
			replayPolicy = llmAppReplaySafe
		}
		effectID := fmt.Sprintf("%s:graph:%s", run.snapshot().RunID, node.ID)
		state.Status = llmAppItemEffectPending
		state.EffectID = effectID
		state.ReplayPolicy = replayPolicy
		if err := m.writeGraphNodeState(run, state); err != nil {
			return nil, err
		}
		result, err := runtime.executeTool(ctx, ToolCall{CallID: effectID, Name: node.Tool, Arguments: argumentMap})
		if err != nil {
			return nil, err
		}
		if !result.Succeeded {
			return nil, fmt.Errorf("tool %s reported an unsuccessful execution", node.Tool)
		}
		return decodeLLMAppGraphToolOutput(result.Content), nil
	case llmAppGraphValidate:
		if err := validateLLMAppJSONValue(node.OutputSchema, input); err != nil {
			return nil, err
		}
		return input, nil
	case llmAppGraphReduce:
		return reduceLLMAppGraphValues(node, values)
	case llmAppGraphSink:
		if err := validateLLMAppJSONValue(node.OutputSchema, input); err != nil {
			return nil, err
		}
		return input, nil
	}
	return nil, fmt.Errorf("unsupported graph node type %q", node.Type)
}

func (m *LLMAppManager) executeGraphModelNode(
	ctx context.Context,
	run *llmAppManagedRun,
	definition LLMAppDefinition,
	request llmAppRunRequest,
	runtime Runtime,
	node LLMAppGraphNode,
	input interface{},
	values map[string]interface{},
	state llmAppGraphNodeState,
) (interface{}, error) {
	if runtime.ModelCaller == nil {
		return nil, errors.New("model caller is unavailable")
	}
	prompt, err := renderLLMAppGraphTemplate(node.PromptTemplate, request, input, values)
	if err != nil {
		return nil, err
	}
	systemPrompt := node.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = definition.SystemPrompt
	}
	messages := []map[string]interface{}{}
	if systemPrompt != "" {
		messages = append(messages, map[string]interface{}{"role": "system", "content": systemPrompt})
	}
	messages = append(messages, map[string]interface{}{"role": "user", "content": prompt})
	attempt := 0
	if len(state.Messages) > 0 {
		messages = cloneLLMAppMessages(state.Messages)
		attempt = state.ValidationAttempt
	}
	model := node.Model
	if model == "" {
		model = definition.Model
	}
	for ; attempt <= definition.Health.ValidationRetries; attempt++ {
		inputTokens := estimateLLMAppTokens(llmAppMessageCharacters(messages))
		effectID := fmt.Sprintf("%s:graph:%s:model:%d", run.snapshot().RunID, node.ID, attempt)
		state.Status = llmAppItemEffectPending
		state.EffectID = effectID
		state.ReplayPolicy = llmAppReplaySafe
		state.ValidationAttempt = attempt
		state.Messages = cloneLLMAppMessages(messages)
		if err := m.writeGraphNodeState(run, state); err != nil {
			return nil, err
		}
		if err := m.reserveModelCall(run, inputTokens, node.MaxTokens); err != nil {
			state.Status = llmAppItemPending
			state.EffectID = ""
			state.ReplayPolicy = ""
			_ = m.writeGraphNodeState(run, state)
			return nil, err
		}
		response, callErr := runtime.ModelCaller(ctx, model, messages, node.MaxTokens, node.Temperature)
		if settleErr := m.settleModelCall(run, inputTokens, node.MaxTokens, response, callErr); settleErr != nil {
			return nil, settleErr
		}
		if callErr != nil {
			return nil, callErr
		}
		if len(node.OutputSchema) == 0 {
			return response.Content, nil
		}
		value, decodeErr := decodeLLMAppStructuredOutput(response.Content)
		if decodeErr == nil {
			decodeErr = validateLLMAppJSONValue(node.OutputSchema, value)
		}
		if decodeErr == nil {
			return value, nil
		}
		if attempt == definition.Health.ValidationRetries {
			return nil, fmt.Errorf("output validation failed: %w", decodeErr)
		}
		messages = append(messages,
			map[string]interface{}{"role": "assistant", "content": response.Content},
			map[string]interface{}{"role": "user", "content": fmt.Sprintf(
				"The previous output did not satisfy the required JSON schema (%v). Return only one valid JSON value that satisfies it.", decodeErr,
			)},
		)
		state.Status = llmAppItemPending
		state.EffectID = ""
		state.ReplayPolicy = ""
		state.ValidationAttempt = attempt + 1
		state.Messages = cloneLLMAppMessages(messages)
		if err := m.writeGraphNodeState(run, state); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("model node exhausted validation attempts")
}

func graphDependencyValue(node LLMAppGraphNode, values map[string]interface{}, request llmAppRunRequest) interface{} {
	switch len(node.DependsOn) {
	case 0:
		return request.Input
	case 1:
		return values[node.DependsOn[0]]
	default:
		result := make(map[string]interface{}, len(node.DependsOn))
		for _, dependency := range node.DependsOn {
			result[dependency] = values[dependency]
		}
		return result
	}
}

func renderLLMAppGraphTemplate(
	template string,
	request llmAppRunRequest,
	input interface{},
	values map[string]interface{},
) (string, error) {
	valueText, err := marshalLLMAppGraphValue(input)
	if err != nil {
		return "", err
	}
	rendered := strings.ReplaceAll(template, "{{input}}", request.Input)
	rendered = strings.ReplaceAll(rendered, "{{value}}", valueText)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		text, err := marshalLLMAppGraphValue(values[key])
		if err != nil {
			return "", err
		}
		rendered = strings.ReplaceAll(rendered, "{{node."+key+"}}", text)
	}
	return rendered, nil
}

func resolveLLMAppGraphValue(
	value interface{},
	request llmAppRunRequest,
	input interface{},
	values map[string]interface{},
) (interface{}, error) {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "{{input}}" {
			return request.Input, nil
		}
		if trimmed == "{{value}}" {
			return input, nil
		}
		if strings.HasPrefix(trimmed, "{{node.") && strings.HasSuffix(trimmed, "}}") {
			id := strings.TrimSuffix(strings.TrimPrefix(trimmed, "{{node."), "}}")
			if resolved, ok := values[id]; ok {
				return resolved, nil
			}
		}
		return renderLLMAppGraphTemplate(typed, request, input, values)
	case map[string]interface{}:
		resolved := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			value, err := resolveLLMAppGraphValue(child, request, input, values)
			if err != nil {
				return nil, err
			}
			resolved[key] = value
		}
		return resolved, nil
	case []interface{}:
		resolved := make([]interface{}, len(typed))
		for index, child := range typed {
			value, err := resolveLLMAppGraphValue(child, request, input, values)
			if err != nil {
				return nil, err
			}
			resolved[index] = value
		}
		return resolved, nil
	default:
		return value, nil
	}
}

func reduceLLMAppGraphValues(node LLMAppGraphNode, values map[string]interface{}) (interface{}, error) {
	inputs := make([]interface{}, 0, len(node.DependsOn))
	for _, dependency := range node.DependsOn {
		inputs = append(inputs, values[dependency])
	}
	switch node.Reducer {
	case "collect":
		return inputs, nil
	case "first":
		if len(inputs) == 0 {
			return nil, errors.New("first reducer has no inputs")
		}
		return inputs[0], nil
	case "concat":
		parts := make([]string, 0, len(inputs))
		for _, input := range inputs {
			text, err := marshalLLMAppGraphValue(input)
			if err != nil {
				return nil, err
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, node.Separator), nil
	case "merge_objects":
		merged := map[string]interface{}{}
		for _, input := range inputs {
			object, ok := input.(map[string]interface{})
			if !ok {
				return nil, errors.New("merge_objects requires object inputs")
			}
			for key, value := range object {
				merged[key] = value
			}
		}
		return merged, nil
	}
	return nil, fmt.Errorf("unsupported reducer %q", node.Reducer)
}

func marshalLLMAppGraphValue(value interface{}) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	data, err := json.Marshal(value)
	return string(data), err
}

func decodeLLMAppGraphToolOutput(content string) interface{} {
	var value interface{}
	if json.Unmarshal([]byte(strings.TrimSpace(content)), &value) == nil {
		return value
	}
	return content
}
