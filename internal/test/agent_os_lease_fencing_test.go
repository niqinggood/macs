package app

import (
	"fmt"
	"io"
	"log"
	"testing"
	"time"
)

func newAgentOSLeaseTestTask(taskID, leaseToken string, now time.Time) *GatewayTask {
	leaseUntil := now.Add(time.Minute)
	return &GatewayTask{
		ID: taskID,
		Request: &MessageRequest{
			TaskID: taskID, TaskType: "chat", SessionId: "session-" + taskID,
			TenantID: "tenant-a", RoleID: "role-a", AgentName: "Researcher",
			Channel: "api", ResolvedWorkspace: ".",
		},
		TenantID: "tenant-a", RoleID: "role-a",
		Status: "running", CurrentPhase: "model", CreatedAt: now,
		HeartbeatAt: &now, QueueName: agentOSQueueName,
		QueueOwner: "worker-a", LeaseToken: leaseToken, LeaseUntil: &leaseUntil,
		ResolvedWorkspace: ".",
	}
}

func seedAgentOSLeaseTestRecord(t *testing.T, g *GatewayService, task *GatewayTask) {
	t.Helper()
	if err := SaveGatewayTaskToDB(g.db, task); err != nil {
		t.Fatalf("seed AgentOS task: %v", err)
	}
}

func TestFinishAgentOSTaskRejectsReplacedWorkerLease(t *testing.T) {
	now := time.Now().Round(0)
	db := openAgentOSScenarioDB(t)
	g := &GatewayService{
		db: db, logger: log.New(io.Discard, "", 0),
		tasks: make(map[string]*GatewayTask),
	}
	current := newAgentOSLeaseTestTask("agt_replaced_lease", "lease-b", now)
	current.QueueOwner = "worker-b"
	seedAgentOSLeaseTestRecord(t, g, current)
	g.tasks[current.ID] = current

	stale := newAgentOSLeaseTestTask(current.ID, "lease-a", now.Add(-time.Minute))
	terminal, applied := g.finishGatewayTask(stale, "completed", "stale result", nil)
	if applied {
		t.Fatal("stale worker completion was applied")
	}
	if terminal == nil || terminal.Status != "running" || terminal.LeaseToken != "lease-b" {
		t.Fatalf("returned task = %+v, want current lease-b owner", terminal)
	}
	var record GatewayTaskRecord
	if err := db.Where("task_id = ?", current.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != "running" || record.LeaseToken != "lease-b" || record.Result != "" {
		t.Fatalf("durable stale-worker result escaped fencing: %+v", record)
	}
}

func TestFinishAgentOSTaskRejectsDurableLeaseChange(t *testing.T) {
	now := time.Now().Round(0)
	db := openAgentOSScenarioDB(t)
	g := &GatewayService{
		db: db, logger: log.New(io.Discard, "", 0),
		tasks: make(map[string]*GatewayTask),
	}
	local := newAgentOSLeaseTestTask("agt_durable_lease", "lease-a", now)
	seedAgentOSLeaseTestRecord(t, g, local)
	g.tasks[local.ID] = local
	if err := db.Model(&GatewayTaskRecord{}).
		Where("task_id = ?", local.ID).
		Updates(map[string]interface{}{
			"lease_token": "lease-b", "queue_owner": "worker-b",
		}).Error; err != nil {
		t.Fatal(err)
	}

	terminal, applied := g.finishGatewayTask(
		local, "failed", nil, fmt.Errorf("late worker failure"),
	)
	if applied {
		t.Fatal("completion with a lost durable lease was applied")
	}
	if terminal == nil || terminal.Status != "running" || terminal.LeaseToken != "lease-b" {
		t.Fatalf("returned task = %+v, want durable lease-b owner", terminal)
	}
	if local.Status != "running" || local.Result != nil || local.CompletedAt != nil {
		t.Fatalf("lost lease mutated local task: %+v", local)
	}
	var record GatewayTaskRecord
	if err := db.Where("task_id = ?", local.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != "running" || record.LeaseToken != "lease-b" || record.ErrorMsg != "" {
		t.Fatalf("durable task was overwritten: %+v", record)
	}
}

func TestFinishAgentOSTaskCommitsMatchingLeaseBeforePublishing(t *testing.T) {
	now := time.Now().Round(0)
	db := openAgentOSScenarioDB(t)
	g := &GatewayService{
		db: db, logger: log.New(io.Discard, "", 0),
		tasks: make(map[string]*GatewayTask),
	}
	task := newAgentOSLeaseTestTask("agt_matching_lease", "lease-a", now)
	seedAgentOSLeaseTestRecord(t, g, task)
	g.tasks[task.ID] = task

	terminal, applied := g.finishGatewayTask(task, "completed", "accepted result", nil)
	if !applied || terminal == nil || terminal.Status != "completed" {
		t.Fatalf("matching lease completion = applied %v, task %+v", applied, terminal)
	}
	var record GatewayTaskRecord
	if err := db.Where("task_id = ?", task.ID).First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.Status != "completed" || record.Result != `"accepted result"` ||
		record.CompletedAt == nil {
		t.Fatalf("terminal state was not durable before return: %+v", record)
	}
}
