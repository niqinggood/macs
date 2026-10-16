package conversation

// Generation is a persisted, credential-free snapshot of a platform media task.
// RequestID belongs to the local submission; Task.ID belongs to the platform.
type Generation struct {
	RequestID  string         `json:"request_id"`
	Capability string         `json:"capability"`
	Model      string         `json:"model"`
	ModelName  string         `json:"model_name"`
	ErrorCode  string         `json:"error_code,omitempty"`
	Task       GenerationTask `json:"task"`
}

type GenerationTask struct {
	ID            uint64   `json:"id"`
	Capability    string   `json:"capability"`
	Status        string   `json:"status"`
	Progress      int      `json:"progress"`
	Title         string   `json:"title,omitempty"`
	Prompt        string   `json:"prompt,omitempty"`
	ResultURL     string   `json:"result_url,omitempty"`
	Lyrics        string   `json:"lyrics,omitempty"`
	ErrorMessage  string   `json:"error_message,omitempty"`
	PaymentStatus string   `json:"payment_status,omitempty"`
	Cost          *float64 `json:"cost"`
}

func (t GenerationTask) Terminal() bool {
	return t.Status == "completed" || t.Status == "failed" || t.Status == "stopped"
}
