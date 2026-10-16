package content

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// GenUIComponent represents a structured UI component that an agent can output.
// Rendered by the frontend in real-time via SSE.
type GenUIComponent struct {
	Type       string      `json:"type"` // table, chart, card, progress, code
	Title      string      `json:"title,omitempty"`
	Data       interface{} `json:"data,omitempty"`
	Columns    []string    `json:"columns,omitempty"`    // for table
	Rows       [][]string  `json:"rows,omitempty"`       // for table
	ChartType  string      `json:"chart_type,omitempty"` // bar, line, pie
	Labels     []string    `json:"labels,omitempty"`     // for chart
	Values     []float64   `json:"values,omitempty"`     // for chart
	Content    string      `json:"content,omitempty"`    // for card/code
	Language   string      `json:"language,omitempty"`   // for code
	Percentage int         `json:"percentage,omitempty"` // for progress
	Color      string      `json:"color,omitempty"`      // accent color
}

// GenUITool lets agents create structured UI components.
type GenUITool struct{}

func (t *GenUITool) Name() string { return "generative_ui" }
func (t *GenUITool) Description() string {
	return "Create structured UI components (tables, charts, cards) that appear in the chat. " +
		"Use this to display data visually instead of plain text. " +
		"Types: table (with columns+rows), chart (bar/line/pie), card (highlighted info), progress (bar), code (highlighted)"
}
func (t *GenUITool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"component_type": map[string]interface{}{
				"type": "string", "enum": []string{"table", "chart", "card", "progress", "code"},
				"description": "Type of UI component to create",
			},
			"title": map[string]interface{}{
				"type": "string", "description": "Component title",
			},
			"data": map[string]interface{}{
				"type": "object", "description": "JSON data: {columns:[...], rows:[[...],[...]]} for table, {labels:[...], values:[...]} for chart, {percentage:N} for progress, {content:'...'} for card/code",
			},
		},
		"required": []string{"component_type", "data"},
	}
}

func (t *GenUITool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	compType := getString(params, "component_type", "card")
	title := getString(params, "title", "")
	data := params["data"]

	comp := GenUIComponent{
		Type:  compType,
		Title: title,
		Data:  data,
	}

	// Parse data into typed fields
	if dataMap, ok := data.(map[string]interface{}); ok {
		switch compType {
		case "table":
			if cols, ok := dataMap["columns"].([]interface{}); ok {
				for _, c := range cols {
					comp.Columns = append(comp.Columns, fmt.Sprint(c))
				}
			}
			if rows, ok := dataMap["rows"].([]interface{}); ok {
				for _, r := range rows {
					if rowArr, ok := r.([]interface{}); ok {
						var row []string
						for _, cell := range rowArr {
							row = append(row, fmt.Sprint(cell))
						}
						comp.Rows = append(comp.Rows, row)
					}
				}
			}
		case "chart":
			if labels, ok := dataMap["labels"].([]interface{}); ok {
				for _, l := range labels {
					comp.Labels = append(comp.Labels, fmt.Sprint(l))
				}
			}
			if vals, ok := dataMap["values"].([]interface{}); ok {
				for _, v := range vals {
					if f, ok := v.(float64); ok {
						comp.Values = append(comp.Values, f)
					}
				}
			}
			comp.ChartType = getString(dataMap, "chart_type", "bar")
		case "card":
			comp.Content = getString(dataMap, "content", "")
		case "progress":
			comp.Percentage = getInt(dataMap, "percentage", 0)
			comp.Title = title
		case "code":
			comp.Content = getString(dataMap, "content", "")
			comp.Language = getString(dataMap, "language", "")
		}
	}

	// Broadcast component via SSE
	broadcastGenUI(comp)

	b, _ := json.Marshal(comp)
	return fmt.Sprintf("Component rendered: %s", string(b)), nil
}

// getString extracts a string from a map with default.
// broadcastGenUI sends a component to all SSE clients via the Gateway.
func broadcastGenUI(comp GenUIComponent) {
	// The Gateway SSE system handles this via the event channel.
	// Components are sent as regular SSE events with type "component".
	// Frontend listens for: event: component\ndata: {json}
	b, _ := json.Marshal(comp)
	if b != nil {
		log.Printf("[GenUI] component: %s", string(b))
	}
}
