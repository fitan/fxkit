package platform_test

import (
	"context"
	"testing"
	"time"

	"github.com/fitan/fxkit/di"
	"github.com/fitan/fxkit/hatchetx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ArticleViewedEvent represents a sample event for declarative event handling.
type ArticleViewedEvent struct {
	ArticleID string    `json:"articleId"`
	UserID    string    `json:"userId"`
	ViewedAt  time.Time `json:"viewedAt"`
}

func TestHatchet_EscapeHatchAndSubclients(t *testing.T) {
	t.Parallel()

	// When client is disabled / nil SDK
	client := &hatchetx.Client{}
	assert.False(t, client.Enabled())
	assert.Nil(t, client.Raw(), "Raw SDK escape hatch should return nil when client is disabled")

	// Subclient accessors must return nil safely without panic
	assert.Nil(t, client.Runs())
	assert.Nil(t, client.Schedules())
	assert.Nil(t, client.Crons())
	assert.Nil(t, client.RateLimits())
	assert.Nil(t, client.Events())
	assert.Nil(t, client.Workflows())
	assert.Nil(t, client.Workers())
	assert.Nil(t, client.Filters())
	assert.Nil(t, client.Metrics())
	assert.Nil(t, client.Logs())
	assert.Nil(t, client.CEL())
	assert.Nil(t, client.Webhooks())
}

func TestHatchet_DeclarativeProvideEventHandler(t *testing.T) {
	t.Parallel()

	injector := di.New()

	// 1. Declaratively register an event handler
	eventOpt := hatchetx.ProvideEventHandler(
		"catalog-on-article-viewed",
		"article-viewed",
		func(ctx hatchetx.Context, ev ArticleViewedEvent) error {
			return nil
		},
		hatchetx.WithRetries(3),
	)
	eventOpt(injector)

	// 2. Declaratively register a standalone background task
	taskOpt := hatchetx.ProvideTask(
		"catalog-cache-refresh",
		func(ctx hatchetx.Context) error {
			return nil
		},
		hatchetx.WithWorkflowCron("@hourly"),
	)
	taskOpt(injector)

	// 3. Verify both registrars were registered in the DI group
	registrars := di.GetGroup[hatchetx.Registrar](injector)
	require.Len(t, registrars, 2, "both event handler and task should be registered as registrars")

	client := &hatchetx.Client{}
	for i, reg := range registrars {
		wfs, err := reg(client)
		require.NoError(t, err, "registrar %d should not error", i)
		_ = wfs
	}
}

func TestHatchet_DAGWorkflowRegistration(t *testing.T) {
	t.Parallel()

	injector := di.New()

	// Register workflows using ProvideWorkflow and ProvideWorkflows
	opt1 := hatchetx.ProvideWorkflow(nil)
	opt1(injector)

	opt2 := hatchetx.ProvideWorkflows(nil, nil)
	opt2(injector)

	opt3 := hatchetx.ProvideWorkflowFn(func(c *hatchetx.Client) ([]hatchetx.WorkflowBase, error) {
		return nil, nil
	})
	opt3(injector)

	regs := di.GetGroup[hatchetx.Registrar](injector)
	assert.Len(t, regs, 3)
}

func TestHatchet_ScheduleOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := &hatchetx.Client{}

	// 1. Validation on disabled client
	_, err := client.ScheduleOnce(ctx, hatchetx.ScheduleOnceInput{
		WorkflowName: "order-delayed-check",
		RunAt:        time.Now().Add(10 * time.Minute),
		Input:        map[string]any{"orderId": "ord-123"},
	})
	assert.ErrorContains(t, err, "client disabled")

	// 2. Standalone helper function validation
	_, err = hatchetx.ScheduleOnce(ctx, client, hatchetx.ScheduleOnceInput{
		WorkflowName: "order-delayed-check",
		RunAt:        time.Now().Add(10 * time.Minute),
	})
	assert.ErrorContains(t, err, "client disabled")

	// 3. Cancel scheduled run validation
	err = client.CancelScheduled(ctx, "sched-999")
	assert.ErrorContains(t, err, "client disabled")

	// 4. List scheduled runs returns clean ScheduledRun domain models
	runs, err := client.ListScheduled(ctx, hatchetx.WorkflowScheduledListParams{})
	assert.ErrorContains(t, err, "client disabled")
	assert.Nil(t, runs)
}

func TestHatchet_RunInfoAndLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := &hatchetx.Client{}

	// 1. GetRun domain model validation
	info, err := client.GetRun(ctx, "run-555")
	assert.ErrorContains(t, err, "client disabled")
	assert.Nil(t, info)

	// 2. GetRunStatus domain enum validation
	status, err := client.GetRunStatus(ctx, "run-555")
	assert.ErrorContains(t, err, "client disabled")
	assert.Empty(t, status)

	// Verify domain status enum values
	assert.Equal(t, hatchetx.RunStatus("QUEUED"), hatchetx.RunStatusQueued)
	assert.Equal(t, hatchetx.RunStatus("RUNNING"), hatchetx.RunStatusRunning)
	assert.Equal(t, hatchetx.RunStatus("COMPLETED"), hatchetx.RunStatusCompleted)
	assert.Equal(t, hatchetx.RunStatus("FAILED"), hatchetx.RunStatusFailed)
	assert.Equal(t, hatchetx.RunStatus("CANCELLED"), hatchetx.RunStatusCancelled)

	// 3. CancelRun validation
	err = client.CancelRun(ctx, "run-555")
	assert.ErrorContains(t, err, "client disabled")

	// 4. ReplayRun validation
	err = client.ReplayRun(ctx, "run-555")
	assert.ErrorContains(t, err, "client disabled")

	// 5. SubscribeToStream validation
	stream, err := client.SubscribeToStream(ctx, "run-555")
	assert.ErrorContains(t, err, "client disabled")
	assert.Nil(t, stream)
}

func TestHatchet_RateLimiting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := &hatchetx.Client{}

	// Rate limit upsert validation
	err := client.UpsertRateLimit(ctx, "view-counter-limit", 500, hatchetx.RateLimitMinute)
	assert.ErrorContains(t, err, "client disabled")

	// Rate limit list validation
	rlList, err := client.ListRateLimits(ctx, nil)
	assert.ErrorContains(t, err, "client disabled")
	assert.Nil(t, rlList)

	// RateLimit duration constants
	assert.Equal(t, hatchetx.RateLimitDuration("second"), hatchetx.RateLimitSecond)
	assert.Equal(t, hatchetx.RateLimitDuration("minute"), hatchetx.RateLimitMinute)
	assert.Equal(t, hatchetx.RateLimitDuration("hour"), hatchetx.RateLimitHour)
	assert.Equal(t, hatchetx.RateLimitDuration("day"), hatchetx.RateLimitDay)
}

func TestHatchet_UpsertReminder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := &hatchetx.Client{}

	// UpsertReminder validation
	_, err := hatchetx.UpsertReminder(ctx, client, hatchetx.CreateReminderInput{
		WorkflowName: "catalog-heartbeat",
		ActorID:      "node-1",
		ReminderName: "tick",
		Period:       "@hourly",
		Upsert:       true,
	})
	assert.ErrorContains(t, err, "client disabled")

	// DeleteActorReminder validation
	err = hatchetx.DeleteActorReminder(ctx, client, "catalog-heartbeat", "tick", "node-1")
	assert.ErrorContains(t, err, "client disabled")
}
