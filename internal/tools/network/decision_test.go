package network

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"mac/internal/modelruntime/decision"
)

type decisionEvaluatorFunc func(context.Context, decision.Request) (decision.Response, error)

func (f decisionEvaluatorFunc) Evaluate(ctx context.Context, r decision.Request) (decision.Response, error) {
	return f(ctx, r)
}

func TestDecisionToolFallbackAndAdvisoryContract(t *testing.T) {
	params := map[string]interface{}{"action": "evaluate", "state": "Example state", "questions": map[string]interface{}{"ready": map[string]interface{}{"type": "noul", "instructions": "Is the page ready?"}}}
	for _, tc := range []struct {
		name     string
		provider decision.Evaluator
		want     string
	}{
		{"disabled", nil, `"reason":"provider_not_configured"`},
		{"failure", decisionEvaluatorFunc(func(context.Context, decision.Request) (decision.Response, error) {
			return decision.Response{}, errors.New("private provider error")
		}), `"reason":"provider_failed"`},
		{"invalid", decisionEvaluatorFunc(func(context.Context, decision.Request) (decision.Response, error) {
			return decision.Response{Model: "test"}, nil
		}), `"reason":"invalid_provider_response"`},
		{"uncertain", decisionEvaluatorFunc(func(context.Context, decision.Request) (decision.Response, error) {
			p := 0.5
			return decision.Response{Model: "test", Answers: map[string]decision.Answer{"ready": {Type: "noul", Noul: &p}}}, nil
		}), `"route":"deliberate"`},
		{"confident", decisionEvaluatorFunc(func(context.Context, decision.Request) (decision.Response, error) {
			p := 0.99
			return decision.Response{Model: "test", Answers: map[string]decision.Answer{"ready": {Type: "noul", Noul: &p}}}, nil
		}), `"route":"fast"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := NewDecisionTool(tc.provider).Execute(context.Background(), params)
			if err != nil || !strings.Contains(out, tc.want) || !strings.Contains(out, `"advisory_only":true`) || strings.Contains(out, "private") {
				t.Fatalf("out=%s err=%v", out, err)
			}
		})
	}
}

func TestDecisionToolStatusAndValidationNeverCallProvider(t *testing.T) {
	tool := NewDecisionTool(decisionEvaluatorFunc(func(context.Context, decision.Request) (decision.Response, error) {
		t.Fatal("provider should not be called")
		return decision.Response{}, nil
	}))
	out, err := tool.Execute(context.Background(), map[string]interface{}{"action": "status"})
	if err != nil || !json.Valid([]byte(out)) {
		t.Fatalf("status: %s %v", out, err)
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"action": "evaluate", "state": true}); err == nil {
		t.Fatal("invalid request accepted")
	}
	if !tool.IsReadOnly() || tool.SandboxCapabilitiesForCall(map[string]interface{}{"action": "status"}).Network || !tool.SandboxCapabilitiesForCall(map[string]interface{}{"action": "evaluate"}).Network {
		t.Fatal("incorrect tool capability metadata")
	}
}
