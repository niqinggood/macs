package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func robotAPITestGateway(t *testing.T) *GatewayService {
	t.Helper()
	config := &Config{Workspace: t.TempDir()}
	config.ToolsConfig.Enabled = true
	config.ToolsConfig.Robot = enabledRobotTestConfig()
	return &GatewayService{config: config}
}

func newRobotAPIRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request.RemoteAddr = "127.0.0.1:43120"
	request.Host = "127.0.0.1:8080"
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func TestRobotAPIStatusCommandAndStop(t *testing.T) {
	gateway := robotAPITestGateway(t)

	statusRecorder := httptest.NewRecorder()
	gateway.handleRobotStatus(statusRecorder, newRobotAPIRequest(http.MethodGet, "/api/robot/status", ""))
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("status code=%d body=%s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var status map[string]interface{}
	if err := json.Unmarshal(statusRecorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status["device"] != "sim_rover" || status["driver"] != "sim" || status["connected"] != true {
		t.Fatalf("unexpected robot status: %#v", status)
	}

	commandRecorder := httptest.NewRecorder()
	commandRequest := newRobotAPIRequest(http.MethodPost, "/api/robot/command",
		`{"text":"前进10厘米","command_id":"api-move-001"}`)
	gateway.handleRobotCommand(commandRecorder, commandRequest)
	if commandRecorder.Code != http.StatusAccepted {
		t.Fatalf("command code=%d body=%s", commandRecorder.Code, commandRecorder.Body.String())
	}
	var command struct {
		OK     bool             `json:"ok"`
		Result robotMoveReceipt `json:"result"`
	}
	if err := json.Unmarshal(commandRecorder.Body.Bytes(), &command); err != nil {
		t.Fatalf("decode command: %v", err)
	}
	if !command.OK || command.Result.CommandID != "api-move-001" || command.Result.DistanceM != 0.1 {
		t.Fatalf("unexpected command response: %+v", command)
	}

	stopRecorder := httptest.NewRecorder()
	stopRequest := newRobotAPIRequest(http.MethodPost, "/api/robot/stop",
		`{"command_id":"api-stop-001","reason":"operator test"}`)
	gateway.handleRobotStop(stopRecorder, stopRequest)
	if stopRecorder.Code != http.StatusOK {
		t.Fatalf("stop code=%d body=%s", stopRecorder.Code, stopRecorder.Body.String())
	}
	var stop robotStopReceipt
	if err := json.Unmarshal(stopRecorder.Body.Bytes(), &stop); err != nil {
		t.Fatalf("decode stop: %v", err)
	}
	if !stop.OK || !stop.WasMoving || stop.StoppedCommandID != "api-move-001" {
		t.Fatalf("unexpected stop response: %+v", stop)
	}
}

func TestRobotAPIRequiresLoopbackSameOriginOrAPIKey(t *testing.T) {
	gateway := robotAPITestGateway(t)

	remote := newRobotAPIRequest(http.MethodGet, "/api/robot/status", "")
	remote.RemoteAddr = "192.0.2.40:51000"
	remoteRecorder := httptest.NewRecorder()
	gateway.handleRobotStatus(remoteRecorder, remote)
	if remoteRecorder.Code != http.StatusForbidden {
		t.Fatalf("remote robot API code=%d, want 403", remoteRecorder.Code)
	}

	crossOrigin := newRobotAPIRequest(http.MethodGet, "/api/robot/status", "")
	crossOrigin.Header.Set("Origin", "https://attacker.example")
	crossOriginRecorder := httptest.NewRecorder()
	gateway.handleRobotStatus(crossOriginRecorder, crossOrigin)
	if crossOriginRecorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin robot API code=%d, want 403", crossOriginRecorder.Code)
	}

	gateway.config.Gateway.APIKey = "robot-api-test-key"
	noKeyRecorder := httptest.NewRecorder()
	gateway.handleRobotStatus(noKeyRecorder, newRobotAPIRequest(http.MethodGet, "/api/robot/status", ""))
	if noKeyRecorder.Code != http.StatusForbidden {
		t.Fatalf("configured API key must be required even on loopback; code=%d", noKeyRecorder.Code)
	}

	keyed := newRobotAPIRequest(http.MethodGet, "/api/robot/status", "")
	keyed.RemoteAddr = "192.0.2.40:51000"
	keyed.Header.Set("X-API-Key", "robot-api-test-key")
	keyedRecorder := httptest.NewRecorder()
	gateway.handleRobotStatus(keyedRecorder, keyed)
	if keyedRecorder.Code != http.StatusOK {
		t.Fatalf("keyed robot API code=%d body=%s", keyedRecorder.Code, keyedRecorder.Body.String())
	}
}

func TestRobotAPIRejectsUnsafeOrMalformedRequests(t *testing.T) {
	t.Run("runtime disabled", func(t *testing.T) {
		gateway := robotAPITestGateway(t)
		gateway.config.ToolsConfig.Robot.Enabled = false
		recorder := httptest.NewRecorder()
		gateway.handleRobotStatus(recorder, newRobotAPIRequest(http.MethodGet, "/api/robot/status", ""))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("disabled status code=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("unknown JSON field", func(t *testing.T) {
		gateway := robotAPITestGateway(t)
		recorder := httptest.NewRecorder()
		gateway.handleRobotCommand(recorder, newRobotAPIRequest(http.MethodPost, "/api/robot/command",
			`{"text":"停","serial_bytes":"00ff"}`))
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "unknown field") {
			t.Fatalf("unknown field code=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("ambiguous text", func(t *testing.T) {
		gateway := robotAPITestGateway(t)
		recorder := httptest.NewRecorder()
		gateway.handleRobotCommand(recorder, newRobotAPIRequest(http.MethodPost, "/api/robot/command",
			`{"text":"去仓库拿箱子"}`))
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "ambiguous") {
			t.Fatalf("ambiguous command code=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("motion gate", func(t *testing.T) {
		gateway := robotAPITestGateway(t)
		gateway.config.ToolsConfig.Robot.AllowMotion = false
		recorder := httptest.NewRecorder()
		gateway.handleRobotCommand(recorder, newRobotAPIRequest(http.MethodPost, "/api/robot/command",
			`{"text":"前进10厘米"}`))
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("motion gate code=%d body=%s", recorder.Code, recorder.Body.String())
		}

		// A stop command remains allowed while motion permission is disabled.
		stopRecorder := httptest.NewRecorder()
		gateway.handleRobotCommand(stopRecorder, newRobotAPIRequest(http.MethodPost, "/api/robot/command",
			`{"text":"马上停"}`))
		if stopRecorder.Code != http.StatusAccepted {
			t.Fatalf("quick stop code=%d body=%s", stopRecorder.Code, stopRecorder.Body.String())
		}
	})

	t.Run("method", func(t *testing.T) {
		gateway := robotAPITestGateway(t)
		recorder := httptest.NewRecorder()
		gateway.handleRobotStop(recorder, newRobotAPIRequest(http.MethodGet, "/api/robot/stop", ""))
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("method code=%d body=%s", recorder.Code, recorder.Body.String())
		}
	})
}
