package runtimeutil

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDeadlineDeliveryBudgetNotice(t *testing.T) {
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithDeadline(context.Background(), started.Add(5*time.Minute))
	defer cancel()

	if notice, ok := DeadlineDeliveryBudgetNotice(ctx, started, started.Add(2*time.Minute)); ok || notice != "" {
		t.Fatalf("notice fired before delivery reserve: %q", notice)
	}
	notice, ok := DeadlineDeliveryBudgetNotice(ctx, started, started.Add(3*time.Minute+30*time.Second))
	if !ok || !strings.Contains(notice, "90 seconds") {
		t.Fatalf("notice = %q, ok = %v; want a 90-second checkpoint", notice, ok)
	}
}

func TestDeadlineDeliveryBudgetNoticeRequiresUsefulDeadline(t *testing.T) {
	started := time.Now()
	if notice, ok := DeadlineDeliveryBudgetNotice(context.Background(), started, started); ok || notice != "" {
		t.Fatalf("background context produced notice %q", notice)
	}
	ctx, cancel := context.WithDeadline(context.Background(), started.Add(time.Minute))
	defer cancel()
	if notice, ok := DeadlineDeliveryBudgetNotice(ctx, started, started.Add(30*time.Second)); ok || notice != "" {
		t.Fatalf("short deadline produced notice %q", notice)
	}
}
