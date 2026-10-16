package llmapp

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	llmAppGraphSource   = "source"
	llmAppGraphModel    = "model"
	llmAppGraphTool     = "tool"
	llmAppGraphValidate = "validate"
	llmAppGraphReduce   = "reduce"
	llmAppGraphSink     = "sink"
)

var llmAppGraphNodeIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// LLMAppGraphDefinition is deliberately small: deterministic plumbing remains
// in the host, while model and tool effects still pass through the same model
// policy and Tool Registry used by the main MACS agent.
type LLMAppGraphDefinition struct {
	Nodes      []LLMAppGraphNode `json:"nodes"`
	OutputNode string            `json:"output_node"`
}

type LLMAppGraphNode struct {
	ID             string                 `json:"id"`
	Type           string                 `json:"type"`
	DependsOn      []string               `json:"depends_on,omitempty"`
	Source         string                 `json:"source,omitempty"`
	SystemPrompt   string                 `json:"system_prompt,omitempty"`
	PromptTemplate string                 `json:"prompt_template,omitempty"`
	Model          string                 `json:"model,omitempty"`
	MaxTokens      int                    `json:"max_tokens,omitempty"`
	Temperature    float64                `json:"temperature,omitempty"`
	OutputSchema   map[string]interface{} `json:"output_schema,omitempty"`
	Tool           string                 `json:"tool,omitempty"`
	Arguments      map[string]interface{} `json:"arguments,omitempty"`
	Reducer        string                 `json:"reducer,omitempty"`
	Separator      string                 `json:"separator,omitempty"`
}

func normalizeLLMAppGraph(definition *LLMAppDefinition) error {
	if definition.Graph == nil {
		return errors.New("graph workflow requires graph")
	}
	graph := definition.Graph
	if len(graph.Nodes) == 0 || len(graph.Nodes) > 64 {
		return errors.New("graph.nodes must contain between 1 and 64 nodes")
	}
	byID := make(map[string]*LLMAppGraphNode, len(graph.Nodes))
	for index := range graph.Nodes {
		node := &graph.Nodes[index]
		node.ID = strings.TrimSpace(node.ID)
		if !llmAppGraphNodeIDPattern.MatchString(node.ID) {
			return fmt.Errorf("graph node id %q is invalid", node.ID)
		}
		if _, exists := byID[node.ID]; exists {
			return fmt.Errorf("duplicate graph node id %q", node.ID)
		}
		byID[node.ID] = node
		node.Type = strings.ToLower(strings.TrimSpace(node.Type))
		for dependencyIndex, dependency := range node.DependsOn {
			node.DependsOn[dependencyIndex] = strings.TrimSpace(dependency)
		}
		node.Source = strings.ToLower(strings.TrimSpace(node.Source))
		node.SystemPrompt = strings.TrimSpace(node.SystemPrompt)
		node.PromptTemplate = strings.TrimSpace(node.PromptTemplate)
		node.Model = strings.TrimSpace(node.Model)
		node.Tool = strings.TrimSpace(node.Tool)
		node.Reducer = strings.ToLower(strings.TrimSpace(node.Reducer))
		switch node.Type {
		case llmAppGraphSource:
			if len(node.DependsOn) != 0 {
				return fmt.Errorf("source node %s cannot have dependencies", node.ID)
			}
			if node.Source == "" {
				node.Source = "input"
			}
			switch node.Source {
			case "input", "items", "file":
			default:
				return fmt.Errorf("source node %s has unsupported source %q", node.ID, node.Source)
			}
		case llmAppGraphModel:
			if node.PromptTemplate == "" {
				node.PromptTemplate = "{{value}}"
			}
			if len(node.SystemPrompt)+len(node.PromptTemplate) > llmAppMaximumPromptChars {
				return fmt.Errorf("model node %s prompt exceeds %d characters", node.ID, llmAppMaximumPromptChars)
			}
			if node.MaxTokens <= 0 {
				node.MaxTokens = definition.MaxTokens
			}
			if node.MaxTokens <= 0 || node.MaxTokens > 32768 {
				return fmt.Errorf("model node %s max_tokens must be between 1 and 32768", node.ID)
			}
			if node.Temperature < 0 || node.Temperature > 2 {
				return fmt.Errorf("model node %s temperature must be between 0 and 2", node.ID)
			}
			if err := validateLLMAppJSONSchema(node.OutputSchema); err != nil {
				return fmt.Errorf("model node %s: %w", node.ID, err)
			}
		case llmAppGraphTool:
			if node.Tool == "" {
				return fmt.Errorf("tool node %s requires tool", node.ID)
			}
		case llmAppGraphValidate:
			if len(node.OutputSchema) == 0 {
				return fmt.Errorf("validate node %s requires output_schema", node.ID)
			}
			if err := validateLLMAppJSONSchema(node.OutputSchema); err != nil {
				return fmt.Errorf("validate node %s: %w", node.ID, err)
			}
		case llmAppGraphReduce:
			if node.Reducer == "" {
				node.Reducer = "collect"
			}
			switch node.Reducer {
			case "collect", "concat", "merge_objects", "first":
			default:
				return fmt.Errorf("reduce node %s has unsupported reducer %q", node.ID, node.Reducer)
			}
		case llmAppGraphSink:
			if err := validateLLMAppJSONSchema(node.OutputSchema); err != nil {
				return fmt.Errorf("sink node %s: %w", node.ID, err)
			}
		default:
			return fmt.Errorf("graph node %s has unsupported type %q", node.ID, node.Type)
		}
	}
	graph.OutputNode = strings.TrimSpace(graph.OutputNode)
	if graph.OutputNode == "" {
		graph.OutputNode = graph.Nodes[len(graph.Nodes)-1].ID
	}
	if _, ok := byID[graph.OutputNode]; !ok {
		return fmt.Errorf("graph output_node %q does not exist", graph.OutputNode)
	}
	for _, node := range graph.Nodes {
		seenDependencies := make(map[string]bool, len(node.DependsOn))
		for _, dependency := range node.DependsOn {
			if seenDependencies[dependency] {
				return fmt.Errorf("graph node %s repeats dependency %s", node.ID, dependency)
			}
			seenDependencies[dependency] = true
			if dependency == node.ID {
				return fmt.Errorf("graph node %s cannot depend on itself", node.ID)
			}
			if _, ok := byID[dependency]; !ok {
				return fmt.Errorf("graph node %s depends on unknown node %s", node.ID, dependency)
			}
		}
		if node.Type != llmAppGraphSource && len(node.DependsOn) == 0 && node.Type != llmAppGraphModel && node.Type != llmAppGraphTool {
			return fmt.Errorf("graph node %s requires at least one dependency", node.ID)
		}
	}
	_, err := llmAppGraphTopologicalOrder(*graph)
	return err
}

func llmAppGraphTopologicalOrder(graph LLMAppGraphDefinition) ([]LLMAppGraphNode, error) {
	byID := make(map[string]LLMAppGraphNode, len(graph.Nodes))
	indegree := make(map[string]int, len(graph.Nodes))
	children := make(map[string][]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		byID[node.ID] = node
		indegree[node.ID] = len(node.DependsOn)
		for _, dependency := range node.DependsOn {
			children[dependency] = append(children[dependency], node.ID)
		}
	}
	ready := make([]string, 0, len(graph.Nodes))
	for id, count := range indegree {
		if count == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	ordered := make([]LLMAppGraphNode, 0, len(graph.Nodes))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		ordered = append(ordered, byID[id])
		for _, child := range children[id] {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
				sort.Strings(ready)
			}
		}
	}
	if len(ordered) != len(graph.Nodes) {
		return nil, errors.New("graph contains a dependency cycle")
	}
	return ordered, nil
}
