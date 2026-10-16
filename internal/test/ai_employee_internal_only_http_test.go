package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newInternalOnlyHTTPTestGateway(t *testing.T) *GatewayService {
	t.Helper()
	gateway := newAEHTTPTestGateway(t)
	gateway.aeManager.Register([]AIEmployeeConfig{
		{ID: "public-worker", Name: "Public Worker", AtName: "public", Pipeline: "public-pipeline"},
		{ID: "quant-trader", Name: "AI交易员", AtName: "交易员", Pipeline: "quant-trader", Hidden: true, InternalOnly: true},
	})
	gateway.config.Pipeline.Pipelines["public-pipeline"] = Pipeline{Name: "public-pipeline"}
	gateway.config.Pipeline.Pipelines["quant-trader"] = Pipeline{Name: "quant-trader"}
	gateway.pipelineExec = NewPipelineExecutor(nil, gateway, log.New(io.Discard, "", 0))
	return gateway
}

func TestInternalOnlyEmployeeHTTPListingsRequireDoubleDiagnosticOptIn(t *testing.T) {
	gateway := newInternalOnlyHTTPTestGateway(t)

	for _, endpoint := range []string{
		"/api/ae/list",
		"/api/ae/list?include_hidden=1",
		"/api/ae/readiness",
		"/api/ae/readiness?include_hidden=1",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, endpoint, nil)
		if strings.Contains(endpoint, "readiness") {
			gateway.handleAEReadiness(recorder, request)
		} else {
			gateway.handleAEList(recorder, request)
		}
		if strings.Contains(recorder.Body.String(), "quant-trader") || strings.Contains(recorder.Body.String(), "AI交易员") {
			t.Fatalf("%s disclosed an internal employee: %s", endpoint, recorder.Body.String())
		}
	}

	for _, endpoint := range []string{
		"/api/ae/list?include_hidden=1&include_internal=1",
		"/api/ae/readiness?include_hidden=1&include_internal=1",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, endpoint, nil)
		if strings.Contains(endpoint, "readiness") {
			gateway.handleAEReadiness(recorder, request)
		} else {
			gateway.handleAEList(recorder, request)
		}
		if !strings.Contains(recorder.Body.String(), "quant-trader") || !strings.Contains(recorder.Body.String(), `"internal_only":true`) {
			t.Fatalf("%s did not provide explicit diagnostics: %s", endpoint, recorder.Body.String())
		}
	}
}

func TestInternalOnlyEmployeeDetailAndExportAreNotPubliclyAddressable(t *testing.T) {
	gateway := newInternalOnlyHTTPTestGateway(t)

	for _, testCase := range []struct {
		endpoint string
		handler  func(http.ResponseWriter, *http.Request)
	}{
		{endpoint: "/api/ae/detail?id=quant-trader", handler: gateway.handleAEDetail},
		{endpoint: "/api/ae/export?id=quant-trader", handler: gateway.handleAEExport},
	} {
		recorder := httptest.NewRecorder()
		testCase.handler(recorder, httptest.NewRequest(http.MethodGet, testCase.endpoint, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", testCase.endpoint, recorder.Code)
		}

		recorder = httptest.NewRecorder()
		diagnosticEndpoint := testCase.endpoint + "&include_hidden=1&include_internal=1"
		testCase.handler(recorder, httptest.NewRequest(http.MethodGet, diagnosticEndpoint, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s diagnostic status = %d, body=%s", diagnosticEndpoint, recorder.Code, recorder.Body.String())
		}
	}
}

func TestInternalOnlyFinancialPipelinesCannotBeRunOrDiscoveredNormally(t *testing.T) {
	gateway := newInternalOnlyHTTPTestGateway(t)

	runBody, _ := json.Marshal(map[string]string{
		"pipeline": "quant-trader",
		"input":    "analyze a stock",
	})
	recorder := httptest.NewRecorder()
	gateway.handlePipelineRun(recorder, httptest.NewRequest(http.MethodPost, "/api/pipeline/run", bytes.NewReader(runBody)))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("internal pipeline run status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if len(gateway.pipelineExec.ListExecutions(10)) != 0 {
		t.Fatal("rejected internal pipeline run created an execution")
	}

	recorder = httptest.NewRecorder()
	gateway.handlePipelineList(recorder, httptest.NewRequest(http.MethodGet, "/api/pipeline/list", nil))
	if strings.Contains(recorder.Body.String(), "quant-trader") {
		t.Fatalf("normal pipeline list disclosed internal pipeline: %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	gateway.handlePipelineList(recorder, httptest.NewRequest(http.MethodGet, "/api/pipeline/list?include_hidden=1&include_internal=1", nil))
	if !strings.Contains(recorder.Body.String(), "quant-trader") {
		t.Fatalf("diagnostic pipeline list omitted internal pipeline: %s", recorder.Body.String())
	}
}

func TestInternalOnlyEmployeeWebhookIsRejectedBeforeDispatch(t *testing.T) {
	gateway := newInternalOnlyHTTPTestGateway(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/webhook/quant-trader", strings.NewReader(`{"event":"analyze"}`))
	gateway.handleWebhook(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("internal webhook status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if records := gateway.aeManager.GetWorkLog("quant-trader"); len(records) != 0 {
		t.Fatalf("internal webhook created work records: %#v", records)
	}
}
