package hatchetx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	"github.com/hatchet-dev/hatchet/sdks/go/features"
)

// ScheduleOnceInput configures a one-off scheduled workflow execution at a future time.
//
// Unlike recurring cron reminders ([CreateReminder]), ScheduleOnce fires once at RunAt
// and can be queried or canceled by its returned scheduledRunID.
type ScheduleOnceInput struct {
	WorkflowName string         // Target workflow name
	RunAt        time.Time      // Time at which the workflow should execute
	Input        any            // Payload delivered to the workflow
	Priority     *RunPriority   // Optional priority
	Metadata     map[string]any // Optional metadata attached to the scheduled run
}

// ScheduleOnce schedules a workflow to execute once at a future time (dueTime semantics).
func ScheduleOnce(ctx context.Context, client *Client, in ScheduleOnceInput) (scheduledRunID string, err error) {
	if client == nil || !client.Enabled() || client.Schedules() == nil {
		return "", fmt.Errorf("hatchetx: client disabled")
	}
	workflow := strings.TrimSpace(in.WorkflowName)
	if workflow == "" {
		return "", fmt.Errorf("hatchetx: workflow name is required")
	}
	if in.RunAt.IsZero() {
		return "", fmt.Errorf("hatchetx: runAt time is required")
	}

	payload, err := toInputMap(in.Input)
	if err != nil {
		return "", fmt.Errorf("hatchetx: schedule payload: %w", err)
	}

	trigger := features.CreateScheduledRunTrigger{
		TriggerAt:          in.RunAt,
		Input:              payload,
		AdditionalMetadata: in.Metadata,
		Priority:           in.Priority,
	}

	res, err := client.Schedules().Create(ctx, workflow, trigger)
	if err != nil {
		return "", fmt.Errorf("hatchetx: schedule workflow: %w", err)
	}
	if res == nil {
		return "", fmt.Errorf("hatchetx: schedule workflow: empty response")
	}
	return res.Metadata.Id, nil
}

// CancelScheduled cancels a previously scheduled one-off workflow run.
func CancelScheduled(ctx context.Context, client *Client, scheduledRunID string) error {
	if client == nil || !client.Enabled() || client.Schedules() == nil {
		return fmt.Errorf("hatchetx: client disabled")
	}
	id := strings.TrimSpace(scheduledRunID)
	if id == "" {
		return fmt.Errorf("hatchetx: scheduledRunId is required")
	}
	return client.Schedules().Delete(ctx, id)
}

// ListScheduled lists scheduled workflow runs matching the given filter params.
func ListScheduled(ctx context.Context, client *Client, opts rest.WorkflowScheduledListParams) (*rest.ScheduledWorkflowsList, error) {
	if client == nil || !client.Enabled() || client.Schedules() == nil {
		return nil, fmt.Errorf("hatchetx: client disabled")
	}
	return client.Schedules().List(ctx, opts)
}

// ScheduleOnce schedules a one-off workflow execution at a future time on this client.
func (c *Client) ScheduleOnce(ctx context.Context, in ScheduleOnceInput) (string, error) {
	return ScheduleOnce(ctx, c, in)
}

// CancelScheduled cancels a previously scheduled workflow run on this client.
func (c *Client) CancelScheduled(ctx context.Context, scheduledRunID string) error {
	return CancelScheduled(ctx, c, scheduledRunID)
}

// ListScheduled lists scheduled workflow runs matching the given filter params on this client.
func (c *Client) ListScheduled(ctx context.Context, opts rest.WorkflowScheduledListParams) (*rest.ScheduledWorkflowsList, error) {
	return ListScheduled(ctx, c, opts)
}
