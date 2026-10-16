package pipeline

// PipelineConfig is the serializable collection of named pipelines and their triggers.
type PipelineConfig struct {
	Triggers  []PipelineTrigger   `json:"triggers,omitempty" yaml:"triggers,omitempty"`
	Pipelines map[string]Pipeline `json:"pipelines" yaml:"pipelines"`
}

type PipelineTrigger struct {
	When     string `json:"when" yaml:"when"`
	Pipeline string `json:"run" yaml:"run"`
	Channel  string `json:"channel,omitempty" yaml:"channel,omitempty"`
}

// Pipeline describes an executable workflow independently of its application host.
type Pipeline struct {
	Name                   string                       `json:"name" yaml:"name"`
	Desc                   string                       `json:"desc,omitempty" yaml:"desc,omitempty"`
	DatabaseConnections    []PipelineDatabaseConnection `json:"database_connections,omitempty" yaml:"database_connections,omitempty"`
	Steps                  []PipelineStep               `json:"steps" yaml:"steps"`
	AIEmployeeOriginalTask string                       `json:"ai_employee_original_task,omitempty" yaml:"-"`
	AIEmployeeContextPath  string                       `json:"ai_employee_context_path,omitempty" yaml:"-"`
	AIEmployeeID           string                       `json:"ai_employee_id,omitempty" yaml:"-"`
	AIEmployeeName         string                       `json:"ai_employee_name,omitempty" yaml:"-"`
	AIEmployeeAtName       string                       `json:"ai_employee_at_name,omitempty" yaml:"-"`
}

// PipelineDatabaseConnection is a database profile owned by one Pipeline.
// DSNEnv is preferred so credentials do not have to be persisted in the
// pipeline definition. Disabled profiles are safe placeholders in built-ins.
type PipelineDatabaseConnection struct {
	ID             string   `json:"id" yaml:"id"`
	Enabled        bool     `json:"enabled" yaml:"enabled"`
	Driver         string   `json:"driver" yaml:"driver"`
	DSN            string   `json:"dsn,omitempty" yaml:"dsn,omitempty"`
	DSNEnv         string   `json:"dsn_env,omitempty" yaml:"dsn_env,omitempty"`
	Permission     string   `json:"permission,omitempty" yaml:"permission,omitempty"`
	AllowedTables  []string `json:"allowed_tables,omitempty" yaml:"allowed_tables,omitempty"`
	DeniedColumns  []string `json:"denied_columns,omitempty" yaml:"denied_columns,omitempty"`
	MaxRows        int      `json:"max_rows,omitempty" yaml:"max_rows,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
}

type PipelineStep struct {
	Name               string                    `json:"name" yaml:"name"`
	Prompt             string                    `json:"prompt" yaml:"prompt"`
	Tools              []string                  `json:"tools" yaml:"tools"`
	DatabaseConnection string                    `json:"database_connection,omitempty" yaml:"database_connection,omitempty"`
	RequiredTools      []string                  `json:"required_tools,omitempty" yaml:"required_tools,omitempty"`
	RequiredSkills     []string                  `json:"required_skills,omitempty" yaml:"required_skills,omitempty"`
	NoTools            bool                      `json:"no_tools,omitempty" yaml:"no_tools,omitempty"`
	TimeoutSeconds     int                       `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	OutputTokenLimit   int                       `json:"output_token_limit,omitempty" yaml:"output_token_limit,omitempty"`
	KBSlugs            []string                  `json:"kb_slugs,omitempty" yaml:"kb_slugs,omitempty"`
	EmbeddingMode      string                    `json:"embedding_mode,omitempty" yaml:"embedding_mode,omitempty"`
	EmbeddingThreshold float64                   `json:"embedding_threshold,omitempty" yaml:"embedding_threshold,omitempty"`
	MaxIter            int                       `json:"max_iter" yaml:"max_iter"`
	RequireApproval    bool                      `json:"require_approval,omitempty" yaml:"require_approval,omitempty"`
	ContinueOnError    bool                      `json:"continue_on_error,omitempty" yaml:"continue_on_error,omitempty"`
	AllowMissingInput  bool                      `json:"allow_missing_input,omitempty" yaml:"allow_missing_input,omitempty"`
	ApprovalTemplate   string                    `json:"approval_template,omitempty" yaml:"approval_template,omitempty"`
	EscalationAfter    string                    `json:"escalation_after,omitempty" yaml:"escalation_after,omitempty"`
	EscalateTo         string                    `json:"escalate_to,omitempty" yaml:"escalate_to,omitempty"`
	Agent              string                    `json:"agent,omitempty" yaml:"agent,omitempty"`
	Model              string                    `json:"model,omitempty" yaml:"model,omitempty"`
	Capability         string                    `json:"capability,omitempty" yaml:"capability,omitempty"`
	SourceContract     *PipelineSourceContract   `json:"source_contract,omitempty" yaml:"source_contract,omitempty"`
	ArtifactContract   *PipelineArtifactContract `json:"artifact_contract,omitempty" yaml:"artifact_contract,omitempty"`
	OutputContract     *PipelineOutputContract   `json:"output_contract,omitempty" yaml:"output_contract,omitempty"`
	ProfessionalAgent  *ProfessionalAgentStep    `json:"professional_agent,omitempty" yaml:"professional_agent,omitempty"`
	Slot               *PipelineSlotContract     `json:"slot,omitempty" yaml:"slot,omitempty"`
	Routing            []StepRoute               `json:"routing,omitempty" yaml:"routing,omitempty"`
	Repeat             *StepRepeat               `json:"repeat,omitempty" yaml:"repeat,omitempty"`
	Rework             *StepRework               `json:"rework,omitempty" yaml:"rework,omitempty"`
	Parallel           []PipelineStep            `json:"parallel,omitempty" yaml:"parallel,omitempty"`
	StepExamples       []StepExample             `json:"step_examples,omitempty" yaml:"step_examples,omitempty"`
}

type ProfessionalAgentStep struct {
	ID         string `json:"id" yaml:"id"`
	Capability string `json:"capability" yaml:"capability"`
}

type StepExample struct {
	UserMessage string `json:"user_message" yaml:"user_message"`
	RouteTo     string `json:"route_to" yaml:"route_to"`
}

type StepRoute struct {
	When string `json:"when" yaml:"when"`
	Goto string `json:"goto" yaml:"goto"`
}

type StepRepeat struct {
	Max       int    `json:"max" yaml:"max"`
	Condition string `json:"condition" yaml:"condition"`
	OnMax     string `json:"on_max,omitempty" yaml:"on_max,omitempty"`
}

// FallbackStepName returns the conventional fallback name for a step.
func (s *PipelineStep) FallbackStepName() string {
	if s == nil {
		return ""
	}
	return s.Name + "_fallback"
}

func (p *PipelineConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type raw PipelineConfig
	return unmarshal((*raw)(p))
}
