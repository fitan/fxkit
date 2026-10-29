package hatchetx

import (
	"context"
	"strings"
	"testing"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
)

func TestRuns_DisabledClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := &Client{}

	if _, err := c.GetRun(ctx, "run-123"); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	if _, err := c.GetRunStatus(ctx, "run-123"); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	if err := c.CancelRun(ctx, "run-123"); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	if err := c.ReplayRun(ctx, "run-123"); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	if _, err := c.SubscribeToStream(ctx, "run-123"); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}
}

func TestRuns_EmptyParameters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Even if enabled was true, empty runID checks run before SDK calls
	c := &Client{}
	if _, err := c.GetRun(ctx, ""); err == nil {
		t.Fatal("expected error for empty runId")
	}
	if _, err := c.GetRunStatus(ctx, ""); err == nil {
		t.Fatal("expected error for empty runId")
	}
	if err := c.CancelRun(ctx, ""); err == nil {
		t.Fatal("expected error for empty runId")
	}
	if err := c.ReplayRun(ctx, ""); err == nil {
		t.Fatal("expected error for empty runId")
	}
	if _, err := c.SubscribeToStream(ctx, ""); err == nil {
		t.Fatal("expected error for empty runId")
	}
}

func TestMapTaskStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   rest.V1TaskStatus
		want RunStatus
	}{
		{rest.V1TaskStatus("QUEUED"), RunStatusQueued},
		{rest.V1TaskStatus("RUNNING"), RunStatusRunning},
		{rest.V1TaskStatus("COMPLETED"), RunStatusCompleted},
		{rest.V1TaskStatus("FAILED"), RunStatusFailed},
		{rest.V1TaskStatus("CANCELLED"), RunStatusCancelled},
		{rest.V1TaskStatus("CUSTOM"), RunStatus("CUSTOM")},
	}

	for _, tc := range cases {
		got := mapTaskStatus(tc.in)
		if got != tc.want {
			t.Errorf("mapTaskStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
