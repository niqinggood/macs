package app

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"mac/internal/modelruntime/decision"
	networktools "mac/internal/tools/network"
)

// Explicit operator opt-in is required before sending decision state to a new
// inference provider. A key alone does not enable it. Configuration is captured
// when the agent's tools are assembled, as with other model clients.
func newDecisionTool() *networktools.DecisionTool {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("OPENKITTY_DECISION_PROVIDER")), "jev") {
		return networktools.NewDecisionTool(nil)
	}
	timeout := 1500
	if raw := strings.TrimSpace(os.Getenv("OPENKITTY_DECISION_TIMEOUT_MS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 30000 {
			log.Print("[WARN] decision provider disabled: invalid timeout configuration")
			return networktools.NewDecisionTool(nil)
		}
		timeout = value
	}
	client, err := decision.NewJev(os.Getenv("TYPESAFE_API_KEY"), os.Getenv("OPENKITTY_DECISION_MODEL"), time.Duration(timeout)*time.Millisecond)
	if err != nil {
		log.Print("[WARN] decision provider disabled: missing or invalid provider configuration")
		return networktools.NewDecisionTool(nil)
	}
	return networktools.NewDecisionTool(client)
}
