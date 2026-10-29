package hatchetx

import (
	"context"
	"strings"
	"testing"
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
