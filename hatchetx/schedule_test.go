package hatchetx

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
)

func TestScheduleOnce_Validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// nil client
	if _, err := ScheduleOnce(ctx, nil, ScheduleOnceInput{}); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	// disabled client
	disabled := &Client{}
	if _, err := ScheduleOnce(ctx, disabled, ScheduleOnceInput{}); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	// client method wrapper
	if _, err := disabled.ScheduleOnce(ctx, ScheduleOnceInput{}); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error from client method, got %v", err)
	}
}

func TestCancelScheduled_Validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	if err := CancelScheduled(ctx, nil, "sched-1"); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	disabled := &Client{}
	if err := CancelScheduled(ctx, disabled, "sched-1"); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	if err := disabled.CancelScheduled(ctx, "sched-1"); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error from method, got %v", err)
	}
}

func TestListScheduled_Validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	if _, err := ListScheduled(ctx, nil, rest.WorkflowScheduledListParams{}); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	disabled := &Client{}
	if _, err := disabled.ListScheduled(ctx, rest.WorkflowScheduledListParams{}); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error from method, got %v", err)
	}
}

func TestScheduleOnceInput_Properties(t *testing.T) {
	t.Parallel()
	now := time.Now()
	in := ScheduleOnceInput{
		WorkflowName: "delayed-job",
		RunAt:        now,
		Input:        map[string]any{"key": "val"},
		Metadata:     map[string]any{"source": "test"},
	}
	if in.WorkflowName != "delayed-job" || in.RunAt != now {
		t.Fatalf("unexpected input: %+v", in)
	}
}
