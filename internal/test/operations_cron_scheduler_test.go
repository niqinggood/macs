package app

import (
	"testing"
	"time"

	agenttools "mac/internal/tools/agents"
)

func TestCronSchedulerNew(t *testing.T) {
	cs := NewCronScheduler(nil, nil)
	if cs == nil {
		t.Fatal("NewCronScheduler returned nil")
	}
	if cs.started {
		t.Error("should not be started initially")
	}
}

func TestCronSchedulerAddRemove(t *testing.T) {
	cs := NewCronScheduler(nil, nil)
	cs.Start()
	defer cs.Stop()

	// Add a task with a far-future cron (every year on Jan 1)
	err := cs.AddTask(ScheduledTask{
		Name: "test-task", CronExpr: "0 0 0 1 1 *", DEID: "de1",
		Prompt: "test prompt", Enabled: true,
	})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}

	// Should be in the list
	tasks := cs.ListTasks()
	if len(tasks) < 1 {
		t.Error("should have at least 1 task")
	}

	// Remove
	err = cs.RemoveTask("test-task")
	if err != nil {
		t.Fatalf("remove task: %v", err)
	}

	tasks = cs.ListTasks()
	for _, task := range tasks {
		if task.Name == "test-task" {
			t.Error("task should be removed")
		}
	}
}

func TestCronSchedulerInvalidExpr(t *testing.T) {
	cs := NewCronScheduler(nil, nil)
	cs.Start()
	defer cs.Stop()

	err := cs.AddTask(ScheduledTask{
		Name: "bad", CronExpr: "not a cron expr", DEID: "d1",
	})
	if err == nil {
		t.Error("should error on invalid cron expression")
	}
}

func TestCronSchedulerStartStop(t *testing.T) {
	cs := NewCronScheduler(nil, nil)
	cs.Start()
	if !cs.started {
		t.Error("should be started")
	}
	cs.Stop()
	if cs.started {
		t.Error("should be stopped")
	}
}

func TestCronTaskToolActions(t *testing.T) {
	cs := NewCronScheduler(nil, nil)
	cs.Start()
	defer cs.Stop()
	tool := agenttools.NewCronTaskTool(cs)

	// List empty
	result, err := tool.Execute(nil, map[string]interface{}{"action": "list"})
	if err != nil || result == "" {
		t.Error("list should return result even when empty")
	}

	// Add invalid
	result, _ = tool.Execute(nil, map[string]interface{}{"action": "add", "name": "t", "cron_expr": "bad"})
	if result == "" {
		t.Error("should return error message")
	}
}

func TestScheduledTaskStruct(t *testing.T) {
	task := ScheduledTask{
		Name: "test", CronExpr: "0 9 * * *", DEID: "de1",
		Prompt: "daily report", Enabled: true,
	}
	if task.Name != "test" || !task.Enabled {
		t.Error("task fields wrong")
	}
}

func TestCronSchedulerDuplicateAdd(t *testing.T) {
	cs := NewCronScheduler(nil, nil)
	cs.Start()
	defer cs.Stop()

	cs.AddTask(ScheduledTask{Name: "dup", CronExpr: "0 0 0 1 1 *", DEID: "d1"})
	// Adding same name should replace
	err := cs.AddTask(ScheduledTask{Name: "dup", CronExpr: "0 0 0 1 1 *", DEID: "d1"})
	if err != nil {
		t.Errorf("re-adding same task should not error: %v", err)
	}
}

func TestCronSchedulerLoadFromConfig(t *testing.T) {
	cs := NewCronScheduler(nil, nil)
	cs.Start()
	defer cs.Stop()

	// Load from nil AE manager
	count := cs.LoadFromConfig(nil)
	if count != 0 {
		t.Error("load from nil should return 0")
	}
}

func TestCronSchedulerRemoveNonexistent(t *testing.T) {
	cs := NewCronScheduler(nil, nil)
	err := cs.RemoveTask("nonexistent")
	if err == nil {
		t.Error("should error on nonexistent task")
	}
}

func TestCronTaskAddRequiresName(t *testing.T) {
	cs := NewCronScheduler(nil, nil)
	tool := agenttools.NewCronTaskTool(cs)
	result, _ := tool.Execute(nil, map[string]interface{}{"action": "add", "cron_expr": "0 0 0 * * *"})
	if result == "" || result[:5] != "Error" {
		t.Errorf("should require name: %s", result)
	}
}

func TestCronSchedulerPanicSafety(t *testing.T) {
	// Verify nil gateway doesn't panic on task execution
	cs := NewCronScheduler(nil, nil)
	cs.Start()
	defer cs.Stop()

	cs.AddTask(ScheduledTask{Name: "safe", CronExpr: "0 0 0 1 1 *", DEID: "d1"})
	// executeTask should handle nil gateway
	cs.executeTask(ScheduledTask{Name: "manual", Prompt: "test"})
	// Should not panic
	_ = time.Now()
}
