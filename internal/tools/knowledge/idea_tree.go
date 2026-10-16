package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

func truncateStr(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}

// IdeaStatus tracks the lifecycle of an idea.
type IdeaStatus string

const (
	IdeaProposed  IdeaStatus = "proposed"  // suggested, not yet tried
	IdeaTrying    IdeaStatus = "trying"    // currently experimenting
	IdeaWorked    IdeaStatus = "worked"    // validated, merged
	IdeaFailed    IdeaStatus = "failed"    // didn't work
	IdeaAbandoned IdeaStatus = "abandoned" // not worth pursuing
)

// IdeaNode represents a single hypothesis or approach the agent tried.
// Borrowed from Arbor: src/coordinator/idea_tree.py
type IdeaNode struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Hypothesis  string     `json:"hypothesis"` // what we expected
	Status      IdeaStatus `json:"status"`
	Score       float64    `json:"score"`        // 0-10 effectiveness rating
	Insight     string     `json:"insight"`      // what we learned
	GitBranch   string     `json:"git_branch"`   // related worktree/branch
	RelatedWork string     `json:"related_work"` // references, citations
	ParentID    string     `json:"parent_id"`    // which idea this branched from
	ChildrenIDs []string   `json:"children_ids"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// IdeaTree manages a persistent tree of agent ideas and experiments.
type IdeaTree struct {
	mu       sync.RWMutex
	FilePath string               `json:"-"`
	Nodes    map[string]*IdeaNode `json:"nodes"`
	RootIDs  []string             `json:"root_ids"`
}

// LoadIdeaTree loads or creates the idea tree from disk.
func LoadIdeaTree(storageDir string) *IdeaTree {
	tree := &IdeaTree{
		FilePath: filepath.Join(storageDir, ".openkitty", "idea_tree.json"),
		Nodes:    make(map[string]*IdeaNode),
	}
	os.MkdirAll(filepath.Dir(tree.FilePath), 0755)

	data, err := os.ReadFile(tree.FilePath)
	if err == nil {
		json.Unmarshal(data, tree)
	}
	if tree.Nodes == nil {
		tree.Nodes = make(map[string]*IdeaNode)
	}
	return tree
}

// Save persists the idea tree to disk.
func (t *IdeaTree) Save() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(t.FilePath, data, 0644)
}

// AddIdea creates a new idea node.
func (t *IdeaTree) AddIdea(title, hypothesis, parentID, gitBranch string) *IdeaNode {
	t.mu.Lock()
	defer t.mu.Unlock()

	id := fmt.Sprintf("idea_%d", time.Now().UnixNano())
	node := &IdeaNode{
		ID:         id,
		Title:      title,
		Hypothesis: hypothesis,
		Status:     IdeaProposed,
		ParentID:   parentID,
		GitBranch:  gitBranch,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	t.Nodes[id] = node

	if parentID != "" {
		if parent, ok := t.Nodes[parentID]; ok {
			parent.ChildrenIDs = append(parent.ChildrenIDs, id)
		}
	} else {
		t.RootIDs = append(t.RootIDs, id)
	}

	return node
}

// UpdateIdea updates status, score, and insight after experimentation.
func (t *IdeaTree) UpdateIdea(id string, status IdeaStatus, score float64, insight string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	node, ok := t.Nodes[id]
	if !ok {
		return fmt.Errorf("idea '%s' not found", id)
	}
	node.Status = status
	node.Score = score
	node.Insight = insight
	node.UpdatedAt = time.Now()
	return nil
}

// GetAncestry returns the lineage of an idea (root → this node).
func (t *IdeaTree) GetAncestry(id string) []*IdeaNode {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var path []*IdeaNode
	current, ok := t.Nodes[id]
	if !ok {
		return nil
	}
	path = append(path, current)

	for current.ParentID != "" {
		parent, ok := t.Nodes[current.ParentID]
		if !ok {
			break
		}
		path = append([]*IdeaNode{parent}, path...)
		current = parent
	}
	return path
}

// GetChildren returns direct children of an idea.
func (t *IdeaTree) GetChildren(id string) []*IdeaNode {
	t.mu.RLock()
	defer t.mu.RUnlock()

	node, ok := t.Nodes[id]
	if !ok {
		return nil
	}
	var children []*IdeaNode
	for _, cid := range node.ChildrenIDs {
		if child, ok := t.Nodes[cid]; ok {
			children = append(children, child)
		}
	}
	return children
}

// ListActive returns ideas that are proposed or in-progress.
func (t *IdeaTree) ListActive() []*IdeaNode {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var active []*IdeaNode
	for _, n := range t.Nodes {
		if n.Status == IdeaProposed || n.Status == IdeaTrying {
			active = append(active, n)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].CreatedAt.After(active[j].CreatedAt)
	})
	return active
}

// ListCompleted returns ideas that worked or failed, with their insights.
func (t *IdeaTree) ListCompleted() []*IdeaNode {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var done []*IdeaNode
	for _, n := range t.Nodes {
		if n.Status == IdeaWorked || n.Status == IdeaFailed {
			done = append(done, n)
		}
	}
	sort.Slice(done, func(i, j int) bool {
		return done[i].UpdatedAt.After(done[j].UpdatedAt)
	})
	return done
}

// InheritedInsights collects insights from all ancestors of an idea.
func (t *IdeaTree) InheritedInsights(id string) []string {
	ancestry := t.GetAncestry(id)
	var insights []string
	for _, n := range ancestry {
		if n.Insight != "" && n.ID != id {
			insights = append(insights, fmt.Sprintf("[%s] %s", n.Title, n.Insight))
		}
	}
	return insights
}

// Summary returns a markdown summary of the entire tree.
func (t *IdeaTree) Summary() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("## Idea Tree\n\n")

	active := t.ListActive()
	if len(active) > 0 {
		sb.WriteString("### 🟡 Active\n")
		for _, n := range active {
			sb.WriteString(fmt.Sprintf("- **%s** `%s` — %s\n", n.Title, n.Status, truncateStr(n.Hypothesis, 80)))
		}
		sb.WriteString("\n")
	}

	completed := t.ListCompleted()
	if len(completed) > 0 {
		worked := 0
		failed := 0
		for _, n := range completed {
			if n.Status == IdeaWorked {
				worked++
			} else {
				failed++
			}
		}
		sb.WriteString(fmt.Sprintf("### ✅ Worked (%d) | ❌ Failed (%d)\n", worked, failed))
		for _, n := range completed {
			icon := "✅"
			if n.Status == IdeaFailed {
				icon = "❌"
			}
			insight := ""
			if n.Insight != "" {
				insight = fmt.Sprintf(" — %s", truncateStr(n.Insight, 60))
			}
			sb.WriteString(fmt.Sprintf("- %s **%s** (score: %.1f)%s\n", icon, n.Title, n.Score, insight))
		}
	}

	if len(t.Nodes) == 0 {
		sb.WriteString("*No ideas recorded yet.*\n")
	}

	return sb.String()
}

// ==================== idea_tree tool ====================

// IdeaTreeTool allows the agent to record and query experimental ideas.
type IdeaTreeTool struct {
	tree *IdeaTree
}

func NewIdeaTreeTool(tree *IdeaTree) *IdeaTreeTool {
	return &IdeaTreeTool{tree: tree}
}

func (t *IdeaTreeTool) Name() string { return "idea_tree" }

func (t *IdeaTreeTool) Description() string {
	return "Manage a structured idea tree for tracking experiments. " +
		"Actions: " +
		"add(title, hypothesis, parent_id?) — propose a new idea; " +
		"update(id, status, score, insight) — record result after trying; " +
		"list — show all active and completed ideas; " +
		"path(id) — show the lineage of an idea with inherited insights; " +
		"children(id) — show direct child ideas."
}

func (t *IdeaTreeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "string",
				"enum": []string{"add", "update", "list", "path", "children"},
			},
			"title":      map[string]interface{}{"type": "string", "description": "Short name for the idea"},
			"hypothesis": map[string]interface{}{"type": "string", "description": "What do we expect will happen?"},
			"parent_id":  map[string]interface{}{"type": "string", "description": "Parent idea ID this branches from"},
			"id":         map[string]interface{}{"type": "string", "description": "Idea ID (for update/path/children)"},
			"status":     map[string]interface{}{"type": "string", "enum": []string{"proposed", "trying", "worked", "failed", "abandoned"}},
			"score":      map[string]interface{}{"type": "number", "description": "Effectiveness 0-10"},
			"insight":    map[string]interface{}{"type": "string", "description": "What did we learn?"},
		},
		"required": []string{"action"},
	}
}

func (t *IdeaTreeTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	action, _ := params["action"].(string)

	switch action {
	case "add":
		title, _ := params["title"].(string)
		hypothesis, _ := params["hypothesis"].(string)
		parentID, _ := params["parent_id"].(string)
		if title == "" {
			return "Error: title is required", nil
		}
		node := t.tree.AddIdea(title, hypothesis, parentID, "")
		t.tree.Save()
		return fmt.Sprintf("Idea '%s' created (id: %s). Status: proposed.", title, node.ID), nil

	case "update":
		id, _ := params["id"].(string)
		statusStr, _ := params["status"].(string)
		score, _ := params["score"].(float64)
		insight, _ := params["insight"].(string)
		if id == "" {
			return "Error: id is required", nil
		}
		status := IdeaStatus(statusStr)
		if status == "" {
			status = IdeaWorked
		}
		if err := t.tree.UpdateIdea(id, status, score, insight); err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		t.tree.Save()
		return fmt.Sprintf("Idea '%s' updated: status=%s score=%.1f", id, status, score), nil

	case "list":
		return t.tree.Summary(), nil

	case "path":
		id, _ := params["id"].(string)
		if id == "" {
			return "Error: id is required", nil
		}
		ancestry := t.tree.GetAncestry(id)
		if len(ancestry) == 0 {
			return fmt.Sprintf("Idea '%s' not found.", id), nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("## Lineage of '%s'\n\n", id))
		for i, n := range ancestry {
			indent := strings.Repeat("  ", i)
			sb.WriteString(fmt.Sprintf("%s- **%s** [%s] score:%.1f\n", indent, n.Title, n.Status, n.Score))
			if n.Hypothesis != "" {
				sb.WriteString(fmt.Sprintf("%s  Hypothesis: %s\n", indent, n.Hypothesis))
			}
			if n.Insight != "" && n.ID != id {
				sb.WriteString(fmt.Sprintf("%s  ↳ Inherited insight: %s\n", indent, n.Insight))
			}
		}
		return sb.String(), nil

	case "children":
		id, _ := params["id"].(string)
		if id == "" {
			return "Error: id is required", nil
		}
		children := t.tree.GetChildren(id)
		if len(children) == 0 {
			return fmt.Sprintf("Idea '%s' has no child ideas.", id), nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("## Children of '%s'\n\n", id))
		for _, c := range children {
			sb.WriteString(fmt.Sprintf("- **%s** [%s] — %s\n", c.Title, c.Status, truncateStr(c.Hypothesis, 60)))
		}
		return sb.String(), nil

	default:
		return t.tree.Summary(), nil
	}
}
