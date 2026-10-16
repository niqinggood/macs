// Package workflowlearn turns successful computer-use demonstrations into
// reusable, parameterized workflow recipes and evaluates later runs.
package workflowlearn

import "time"

const RecipeVersion = 1

// Trace is the neutral representation of one browser or desktop demonstration.
// It deliberately does not depend on the concrete computer-use implementation.
type Trace struct {
	ID              string           `json:"id,omitempty"`
	Surface         string           `json:"surface"`
	Objective       string           `json:"objective,omitempty"`
	URL             string           `json:"url,omitempty"`
	LastObservation string           `json:"last_observation,omitempty"`
	Steps           []TraceStep      `json:"steps"`
	Assertions      []TraceAssertion `json:"assertions,omitempty"`
}

type TraceStep struct {
	Number    int                    `json:"number,omitempty"`
	Operation string                 `json:"operation"`
	Status    string                 `json:"status,omitempty"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	Output    string                 `json:"output,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

type TraceAssertion struct {
	Kind     string `json:"kind"`
	Expected string `json:"expected"`
	Status   string `json:"status,omitempty"`
	Error    string `json:"error,omitempty"`
}

// VariableHint names a demonstrated value that should become an input slot.
// Value is used only while compiling and is never persisted in a Recipe.
type VariableHint struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

type InputDefinition struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Source      string `json:"source,omitempty"`
}

type Assertion struct {
	Kind     string `json:"kind"`
	Expected string `json:"expected"`
}

type RecipeStep struct {
	ID        string                 `json:"id"`
	Operation string                 `json:"operation"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// Recipe is the portable artifact saved beside a generated SKILL.md.
type Recipe struct {
	Version     int               `json:"version"`
	Revision    int               `json:"revision"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Objective   string            `json:"objective,omitempty"`
	Surface     string            `json:"surface"`
	StartURL    string            `json:"start_url,omitempty"`
	Inputs      []InputDefinition `json:"inputs,omitempty"`
	Steps       []RecipeStep      `json:"steps"`
	Assertions  []Assertion       `json:"assertions,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
	SourceTrace string            `json:"source_trace,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type CompileRequest struct {
	Name        string
	Description string
	Trace       Trace
	Variables   []VariableHint
	Assertions  []Assertion
	Now         time.Time
}

type StepEvaluation struct {
	StepID    string `json:"step_id"`
	Operation string `json:"operation"`
	Passed    bool   `json:"passed"`
	Reason    string `json:"reason,omitempty"`
}

type AssertionEvaluation struct {
	Kind     string `json:"kind"`
	Expected string `json:"expected"`
	Passed   bool   `json:"passed"`
	Reason   string `json:"reason,omitempty"`
}

type EvaluationReport struct {
	RecipeName        string                `json:"recipe_name"`
	CaseName          string                `json:"case_name,omitempty"`
	TraceID           string                `json:"trace_id,omitempty"`
	Passed            bool                  `json:"passed"`
	Score             float64               `json:"score"`
	StepPassRate      float64               `json:"step_pass_rate"`
	AssertionPassRate float64               `json:"assertion_pass_rate"`
	Steps             []StepEvaluation      `json:"steps"`
	Assertions        []AssertionEvaluation `json:"assertions,omitempty"`
	EvaluatedAt       time.Time             `json:"evaluated_at"`
}
