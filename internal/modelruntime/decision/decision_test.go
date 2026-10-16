package decision

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testRequest(t *testing.T) Request {
	t.Helper()
	var req Request
	if err := json.Unmarshal([]byte(`{"state":{"text":"A form is still loading"},"questions":{"next":{"type":"choice","instructions":"Choose the next observation step","criteria":{"wait":"Loading","inspect":"Ready"}},"risk":{"type":"score","instructions":"Rate uncertainty","criteria":["low","high"]},"ready":{"type":"noul","instructions":"Is the form ready?"}}}`), &req); err != nil {
		t.Fatal(err)
	}
	return req
}

const validResponse = `{"model":"jev-test","answers":{"next":{"type":"choice","choice":"wait","probabilities":{"wait":0.96,"inspect":0.04},"confidence":0.92},"risk":{"type":"score","score":0.95,"probabilities":{"0":0.05,"1":0.95},"confidence":0.9,"legend":{"0":"low","1":"high"}},"ready":{"type":"noul","noul":0.02}},"usage":{"input_tokens":100,"output_tokens":20}}`

func testResponse(t *testing.T) Response {
	t.Helper()
	var resp Response
	if err := json.Unmarshal([]byte(validResponse), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDecisionMixedQuestionsAndUncertainty(t *testing.T) {
	req, resp := testRequest(t), testResponse(t)
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	if need, err := NeedsDeliberation(req, resp, 0.8); err != nil || need {
		t.Fatalf("need=%v err=%v", need, err)
	}
	a := resp.Answers["ready"]
	v := 0.5
	a.Noul = &v
	resp.Answers["ready"] = a
	if need, err := NeedsDeliberation(req, resp, 0.8); err != nil || !need {
		t.Fatalf("ambiguous noul: need=%v err=%v", need, err)
	}
	// A confident no is not uncertain just because its yes-probability is low.
	v = 0
	if need, err := NeedsDeliberation(req, resp, 0.8); err != nil || need {
		t.Fatalf("confident no: need=%v err=%v", need, err)
	}
}

func TestDecisionRejectsUntrustedResponses(t *testing.T) {
	for name, mutate := range map[string]func(*Response){
		"missing answer":      func(r *Response) { delete(r.Answers, "next") },
		"extra answer":        func(r *Response) { r.Answers["extra"] = r.Answers["next"] },
		"invented option":     func(r *Response) { a := r.Answers["next"]; a.Choice = "execute"; r.Answers["next"] = a },
		"missing probability": func(r *Response) { delete(r.Answers["next"].Probabilities, "inspect") },
		"wrong sum":           func(r *Response) { r.Answers["next"].Probabilities["wait"] = 0.1 },
		"out of range":        func(r *Response) { r.Answers["next"].Probabilities["wait"] = 2 },
		"missing confidence":  func(r *Response) { a := r.Answers["next"]; a.Confidence = nil; r.Answers["next"] = a },
		"missing noul":        func(r *Response) { a := r.Answers["ready"]; a.Noul = nil; r.Answers["ready"] = a },
		"wrong type":          func(r *Response) { a := r.Answers["ready"]; a.Type = "choice"; r.Answers["ready"] = a },
		"wrong score":         func(r *Response) { a := r.Answers["risk"]; v := 0.1; a.Score = &v; r.Answers["risk"] = a },
		"wrong legend":        func(r *Response) { delete(r.Answers["risk"].Legend, "1") },
	} {
		t.Run(name, func(t *testing.T) {
			r := testResponse(t)
			mutate(&r)
			if err := ValidateResponse(testRequest(t), r); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestDecisionScorePreservesStructuredRubric(t *testing.T) {
	req, resp := testRequest(t), testResponse(t)
	q := req.Questions["risk"]
	q.Instructions = json.RawMessage(`{"question":"Rate uncertainty","context":["form state"]}`)
	q.Criteria = json.RawMessage(`[{"meaning":"low","examples":["ready"]},{"meaning":"high","examples":["loading"]}]`)
	req.Questions["risk"] = q
	a := resp.Answers["risk"]
	a.Legend = map[string]json.RawMessage{"0": json.RawMessage(`{"examples":["ready"],"meaning":"low"}`), "1": json.RawMessage(`{"meaning":"high","examples":["loading"]}`)}
	resp.Answers["risk"] = a
	if err := ValidateResponse(req, resp); err != nil {
		t.Fatal(err)
	}
	a.Legend["1"] = json.RawMessage(`{"meaning":"low"}`)
	if err := ValidateResponse(req, resp); err == nil {
		t.Fatal("provider changed the rubric meaning")
	}
}

func TestDecisionRejectsInvalidQuestionsBeforeInference(t *testing.T) {
	for _, raw := range []string{
		`{"state":true,"questions":{"a":{"type":"noul","instructions":"Ready?"}}}`,
		`{"state":"x","questions":{}}`,
		`{"state":"x","questions":{"a":{"type":"text","instructions":"Write"}}}`,
		`{"state":"x","questions":{"a":{"type":"choice","instructions":"Choose","criteria":{"only":null}}}}`,
		`{"state":"x","questions":{"a":{"type":"score","instructions":"Rate","criteria":["one"]}}}`,
		`{"state":"x","questions":{"a":{"type":"noul","instructions":"Ready?","criteria":{"maybe":"unsure"}}}}`,
	} {
		var req Request
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatal(err)
		}
		if req.Validate() == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDecisionJevContractAndNoSecretEcho(t *testing.T) {
	j, err := NewJev("synthetic-test-credential", "jev-test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	j.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != jevEndpoint || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic-test-credential" {
			t.Fatalf("unexpected request destination or authorization")
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "jev-test" || body["state"] == nil || body["questions"] == nil {
			t.Fatal("missing contract fields")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(validResponse)), Header: make(http.Header)}, nil
	})
	if _, err := j.Evaluate(context.Background(), testRequest(t)); err != nil {
		t.Fatal(err)
	}
	j.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("synthetic-test-credential private state")), Header: make(http.Header)}, nil
	})
	_, err = j.Evaluate(context.Background(), testRequest(t))
	if err == nil || strings.Contains(err.Error(), "credential") || strings.Contains(err.Error(), "private state") {
		t.Fatalf("unsafe error %v", err)
	}
}

func TestDecisionJevCancellationAndRedirectBoundary(t *testing.T) {
	j, _ := NewJev("synthetic-test-credential", "", time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	j.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })
	if _, err := j.Evaluate(ctx, testRequest(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if err := j.client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatal("redirect boundary missing")
	}
	if _, err := NewJev("", "", time.Second); err == nil {
		t.Fatal("missing key accepted")
	}
	if _, err := NewJev("synthetic-test-credential", "", 0); err == nil {
		t.Fatal("unbounded timeout accepted")
	}
}
