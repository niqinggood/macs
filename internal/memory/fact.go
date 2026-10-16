package memory

// Fact represents a subject-predicate-object memory with temporal validity.
type Fact struct {
	ID         int64   `json:"id"`
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	Source     string  `json:"source,omitempty"`
	ValidFrom  string  `json:"valid_from,omitempty"`
	ValidTo    string  `json:"valid_to,omitempty"`
	Confidence float64 `json:"confidence"`
	CreatedAt  string  `json:"created_at"`
}
