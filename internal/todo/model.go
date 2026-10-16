package todo

import (
	"time"

	"gorm.io/gorm"
)

type TodoStatus string

const (
	TodoStatusPending   TodoStatus = "pending"
	TodoStatusRunning   TodoStatus = "running"
	TodoStatusCompleted TodoStatus = "completed"
	TodoStatusFailed    TodoStatus = "failed"
	TodoStatusCancelled TodoStatus = "cancelled"
)

type TodoPriority string

const (
	PriorityLow    TodoPriority = "low"
	PriorityNormal TodoPriority = "normal"
	PriorityHigh   TodoPriority = "high"
	PriorityUrgent TodoPriority = "urgent"
)

type Todo struct {
	ID     uint   `gorm:"primarykey" json:"id"`
	TodoID string `gorm:"uniqueIndex;size:100;not null" json:"todo_id"`

	Title       string       `gorm:"size:500;not null" json:"title"`
	Description string       `gorm:"type:text" json:"description"`
	Priority    TodoPriority `gorm:"size:20;default:normal" json:"priority"`

	AgentName    string `gorm:"index;size:100" json:"agent_name"`
	PipelineName string `gorm:"size:100" json:"pipeline_name"`
	TeamName     string `gorm:"index;size:100" json:"team_name"`
	CreatedBy    string `gorm:"size:100" json:"created_by"`
	Channel      string `gorm:"size:50" json:"channel"`

	ScheduleType string     `gorm:"size:20;default:once" json:"schedule_type"`
	CronExpr     string     `gorm:"size:100" json:"cron_expr"`
	IntervalSec  int        `json:"interval_sec"`
	RunAt        *time.Time `json:"run_at"`
	MaxRetries   int        `gorm:"default:3" json:"max_retries"`

	Status     TodoStatus `gorm:"index;size:20;default:pending" json:"status"`
	LastRunAt  *time.Time `json:"last_run_at"`
	NextRunAt  *time.Time `json:"next_run_at"`
	RunCount   int        `json:"run_count"`
	RetryCount int        `json:"retry_count"`
	LastResult string     `gorm:"type:text" json:"last_result"`
	LastError  string     `gorm:"type:text" json:"last_error"`

	InputData  string `gorm:"type:text" json:"input_data"`
	OutputData string `gorm:"type:text" json:"output_data"`
	Metadata   string `gorm:"type:json" json:"metadata"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type TodoExecutionLog struct {
	ID         uint      `gorm:"primarykey" json:"id"`
	TodoID     string    `gorm:"index;size:100" json:"todo_id"`
	RunAt      time.Time `json:"run_at"`
	Status     string    `json:"status"`
	Result     string    `gorm:"type:text" json:"result"`
	Error      string    `gorm:"type:text" json:"error"`
	DurationMs int64     `json:"duration_ms"`
}
