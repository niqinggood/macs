package macs_test

import (
	"context"
	"log"
	"os"
	"time"

	macs "mac/sdk/go/macs"
)

// This example shows the recommended external-call pattern: submit quickly,
// stream useful progress, and let WaitTask fall back to durable polling if the
// SSE connection is interrupted.
func ExampleClient_WaitTask() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	client, err := macs.NewClient(
		os.Getenv("MACS_BASE_URL"),
		macs.WithBearerToken(os.Getenv("MACS_SERVICE_TOKEN")),
	)
	if err != nil {
		log.Print(err)
		return
	}
	task, err := client.SubmitTask(ctx, macs.SubmitTaskRequest{
		RoleID:  "laboratory-report",
		Message: "按企业模板填写样品检测报告",
	}, "lims-report-S-2026-001")
	if err != nil {
		log.Print(err)
		return
	}

	final, err := client.WaitTask(ctx, task.TaskID, macs.WaitOptions{
		OnProgress: func(progress macs.Progress) error {
			log.Printf("MACS progress: step=%s message=%s", progress.StepName, progress.Message)
			return nil
		},
		OnSnapshot: func(snapshot macs.Task) error {
			log.Printf("MACS snapshot: status=%s phase=%s", snapshot.Status, snapshot.PhaseLabel)
			return nil
		},
	})
	if err != nil {
		log.Print(err)
		return
	}
	log.Printf("MACS task finished: status=%s artifacts=%d", final.Status, len(final.Artifacts))
}
