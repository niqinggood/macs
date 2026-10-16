package workflowlearn

import (
	"fmt"
	"strings"
)

// RenderSkill creates a small Skill wrapper around the machine-readable recipe.
func RenderSkill(recipe Recipe) string {
	description := strings.TrimSpace(recipe.Description)
	if description == "" {
		description = "Replay and verify the learned workflow " + recipe.Name
	}
	var body strings.Builder
	body.WriteString("---\n")
	body.WriteString("name: " + recipe.Name + "\n")
	body.WriteString("description: " + yamlQuoted(description) + "\n")
	body.WriteString("read_when:\n")
	body.WriteString("  - " + yamlQuoted(description) + "\n")
	body.WriteString("  - \"需要复用已演示的企业流程、LIMS 操作或模板填报时\"\n")
	body.WriteString("tags:\n  - workflow\n  - automation\n  - learned\n---\n\n")
	body.WriteString("# " + recipe.Name + "\n\n")
	if recipe.Objective != "" {
		body.WriteString("## Goal\n\n" + recipe.Objective + "\n\n")
	}
	body.WriteString("## Inputs\n\n")
	if len(recipe.Inputs) == 0 {
		body.WriteString("This workflow has no variable inputs.\n\n")
	} else {
		body.WriteString("| Name | Required | Description |\n| --- | --- | --- |\n")
		for _, input := range recipe.Inputs {
			description := strings.ReplaceAll(input.Description, "|", "\\|")
			body.WriteString(fmt.Sprintf("| `%s` | %t | %s |\n", input.Name, input.Required, description))
		}
		body.WriteString("\n")
	}
	body.WriteString("## Execution\n\n")
	body.WriteString("1. Collect every required input. Do not invent missing laboratory or sample values.\n")
	body.WriteString("2. Call `workflow_learner` with `action=\"replay\"`, `name=\"")
	body.WriteString(recipe.Name)
	body.WriteString("\"`, and the `inputs` object. Keep `evaluate=true`.\n")
	body.WriteString("3. If replay reports an unstable selector or coordinate, observe the current UI, repair the workflow, and save a new version rather than silently guessing.\n")
	body.WriteString("4. Treat the workflow as complete only when its evaluation report passes.\n\n")
	body.WriteString("The machine-readable action recipe is stored in `recipe.json` beside this file.\n")
	if len(recipe.Assertions) > 0 {
		body.WriteString("\n## Verification\n\n")
		for _, assertion := range recipe.Assertions {
			body.WriteString(fmt.Sprintf("- `%s`: %s\n", assertion.Kind, assertion.Expected))
		}
	}
	return body.String()
}

func yamlQuoted(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return "\"" + value + "\""
}
