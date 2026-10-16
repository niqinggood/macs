package aiemployee

import "time"

type WorkRecord struct {
	ID          string    `json:"id"`
	EmployeeID  string    `json:"employee_id"`
	Task        string    `json:"task"`
	Pipeline    string    `json:"pipeline"`
	ExecID      string    `json:"exec_id"`
	Status      string    `json:"status"`
	Result      string    `json:"result,omitempty"`
	ToolCalls   string    `json:"tool_calls,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	ChannelUser string    `json:"channel_user,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type WorkRecordDB struct {
	ID          uint   `gorm:"primaryKey"`
	RecordID    string `gorm:"uniqueIndex;size:256"`
	EmployeeID  string `gorm:"index;size:128"`
	Task        string `gorm:"size:1024"`
	Pipeline    string `gorm:"size:256"`
	ExecID      string `gorm:"size:256"`
	Status      string `gorm:"size:32"`
	Result      string `gorm:"size:4096"`
	ToolCalls   string `gorm:"size:4096"`
	SessionID   string `gorm:"size:256"`
	ChannelUser string `gorm:"size:128"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type NotificationDB struct {
	ID         uint      `gorm:"column:id;primaryKey"`
	EmployeeID string    `gorm:"column:employee_id;size:128;index"`
	Supervisor string    `gorm:"column:supervisor;size:256"`
	Channel    string    `gorm:"column:channel;size:64"`
	Message    string    `gorm:"column:message;type:text"`
	CreatedAt  time.Time `gorm:"column:created_at"`
}

func (NotificationDB) TableName() string { return "de_notifications" }

func (record *WorkRecordDB) FromMemory(memory *WorkRecord) {
	record.RecordID = memory.ID
	record.EmployeeID = memory.EmployeeID
	record.Task = memory.Task
	record.Pipeline = memory.Pipeline
	record.ExecID = memory.ExecID
	record.Status = memory.Status
	record.Result = memory.Result
	record.ToolCalls = memory.ToolCalls
	record.SessionID = memory.SessionID
	record.ChannelUser = memory.ChannelUser
	record.CreatedAt = memory.CreatedAt
	record.UpdatedAt = memory.UpdatedAt
}

func (record *WorkRecordDB) ToMemory() *WorkRecord {
	return &WorkRecord{
		ID: record.RecordID, EmployeeID: record.EmployeeID, Task: record.Task,
		Pipeline: record.Pipeline, ExecID: record.ExecID, Status: record.Status,
		Result: record.Result, ToolCalls: record.ToolCalls,
		SessionID: record.SessionID, ChannelUser: record.ChannelUser,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}
