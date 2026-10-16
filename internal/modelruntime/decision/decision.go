// Package decision defines bounded, typed judgments. A judgment is evidence for
// a caller's branch, never a grant of authority to execute that branch.
package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

const MaxRequestBytes = 256 << 10

type Question struct {
	Type         string          `json:"type"`
	Instructions json.RawMessage `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

type Request struct {
	State     json.RawMessage     `json:"state"`
	Questions map[string]Question `json:"questions"`
}

type Answer struct {
	Type          string                     `json:"type"`
	Choice        string                     `json:"choice,omitempty"`
	Score         *float64                   `json:"score,omitempty"`
	Noul          *float64                   `json:"noul,omitempty"`
	Confidence    *float64                   `json:"confidence,omitempty"`
	Probabilities map[string]float64         `json:"probabilities,omitempty"`
	Legend        map[string]json.RawMessage `json:"legend,omitempty"`
}

type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type Evaluator interface {
	Evaluate(context.Context, Request) (Response, error)
}

// Validate bounds both cost and the finite answer space before inference.
func (r Request) Validate() error {
	if !structured(r.State, false) {
		return errors.New("state must be a nonempty string, object, or array")
	}
	if len(r.Questions) == 0 || len(r.Questions) > 64 {
		return errors.New("questions must contain 1..64 entries")
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > MaxRequestBytes {
		return errors.New("decision request is invalid or exceeds 256 KiB")
	}
	for id, q := range r.Questions {
		if strings.TrimSpace(id) == "" || len(id) > 128 || !structured(q.Instructions, false) {
			return errors.New("question names and instructions must be nonempty")
		}
		switch q.Type {
		case "choice":
			var options map[string]json.RawMessage
			if json.Unmarshal(q.Criteria, &options) != nil || len(options) < 2 || len(options) > 255 {
				return errors.New("choice criteria must contain 2..255 named options")
			}
			for name, desc := range options {
				if strings.TrimSpace(name) == "" || !structured(desc, true) {
					return errors.New("invalid choice option description")
				}
			}
		case "score":
			var levels []json.RawMessage
			if json.Unmarshal(q.Criteria, &levels) != nil || len(levels) < 2 || len(levels) > 10 {
				return errors.New("score criteria must contain 2..10 ordered levels")
			}
			for _, desc := range levels {
				if !structured(desc, false) {
					return errors.New("invalid score level description")
				}
			}
		case "noul":
			if len(q.Criteria) != 0 {
				var criteria map[string]json.RawMessage
				if json.Unmarshal(q.Criteria, &criteria) != nil || criteria == nil {
					return errors.New("noul criteria must be an object")
				}
				for name, desc := range criteria {
					if (name != "true" && name != "false") || !structured(desc, false) {
						return errors.New("noul criteria only accepts true and false descriptions")
					}
				}
			}
		default:
			return errors.New("question type must be choice, score, or noul")
		}
	}
	return nil
}

func structured(raw json.RawMessage, allowNull bool) bool {
	var value interface{}
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) != ""
	case map[string]interface{}:
		return len(v) > 0
	case []interface{}:
		return len(v) > 0
	case nil:
		return allowNull
	default:
		return false
	}
}

func probability(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1 }

// ValidateResponse never trusts a remote provider's schema guarantee. Missing
// numbers must remain missing, rather than becoming plausible zero values.
func ValidateResponse(req Request, response Response) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(response.Model) == "" || len(response.Answers) != len(req.Questions) {
		return errors.New("decision response is missing its model or exact question coverage")
	}
	for id, q := range req.Questions {
		a, ok := response.Answers[id]
		if !ok || a.Type != q.Type {
			return errors.New("decision response question type does not match")
		}
		if q.Type == "noul" {
			if a.Noul == nil || !probability(*a.Noul) || a.Score != nil || a.Choice != "" {
				return errors.New("invalid noul probability")
			}
			continue
		}
		if a.Confidence == nil || !probability(*a.Confidence) || a.Noul != nil {
			return errors.New("decision response has missing or invalid confidence")
		}
		var options map[string]json.RawMessage
		var levels []json.RawMessage
		if q.Type == "choice" {
			_ = json.Unmarshal(q.Criteria, &options)
			if _, ok := options[a.Choice]; !ok || a.Score != nil {
				return errors.New("decision selected an option outside the declared choices")
			}
		} else {
			_ = json.Unmarshal(q.Criteria, &levels)
			options = make(map[string]json.RawMessage, len(levels))
			for i := range levels {
				options[strconv.Itoa(i)] = nil
			}
			if a.Score == nil || math.IsNaN(*a.Score) || math.IsInf(*a.Score, 0) || *a.Score < 0 || *a.Score > float64(len(levels)-1) || a.Choice != "" || len(a.Legend) != len(levels) {
				return errors.New("invalid score or legend")
			}
		}
		if len(a.Probabilities) != len(options) {
			return errors.New("incomplete probability distribution")
		}
		sum, weighted := 0.0, 0.0
		for name := range options {
			p, exists := a.Probabilities[name]
			if !exists || !probability(p) {
				return errors.New("invalid probability distribution")
			}
			sum += p
			if q.Type == "choice" && p > a.Probabilities[a.Choice]+0.00001 {
				return errors.New("selected choice does not have maximum probability")
			}
			if q.Type == "score" {
				i, _ := strconv.Atoi(name)
				var actual, expected interface{}
				if json.Unmarshal(a.Legend[name], &actual) != nil || json.Unmarshal(levels[i], &expected) != nil || !reflect.DeepEqual(actual, expected) {
					return errors.New("score legend does not match its levels")
				}
				weighted += float64(i) * p
			}
		}
		if math.Abs(sum-1) > 0.001 {
			return errors.New("probabilities do not sum to one")
		}
		if q.Type == "score" && math.Abs(weighted-*a.Score) > 0.01 {
			return errors.New("score does not match its probability distribution")
		}
	}
	return nil
}

// NeedsDeliberation applies caller-owned uncertainty thresholds. It does not
// interpret a high-confidence answer as permission, success, or a visual audit.
func NeedsDeliberation(req Request, response Response, minConfidence float64) (bool, error) {
	if !probability(minConfidence) {
		return true, fmt.Errorf("min_confidence must be within 0..1")
	}
	if err := ValidateResponse(req, response); err != nil {
		return true, err
	}
	for _, answer := range response.Answers {
		confidence := 0.0
		if answer.Type == "noul" {
			p := *answer.Noul
			confidence = math.Abs(2*p - 1)
		} else {
			confidence = *answer.Confidence
		}
		if confidence < minConfidence {
			return true, nil
		}
	}
	return false, nil
}
