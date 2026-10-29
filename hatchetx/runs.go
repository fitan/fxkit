package hatchetx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// RunStatus represents the execution state of a workflow run.
type RunStatus string

const (
	RunStatusQueued    RunStatus = "QUEUED"
	RunStatusRunning   RunStatus = "RUNNING"
	RunStatusCompleted RunStatus = "COMPLETED"
	RunStatusFailed    RunStatus = "FAILED"
	RunStatusCancelled RunStatus = "CANCELLED"
	RunStatusUnknown   RunStatus = "UNKNOWN"
)

// RunInfo contains structured execution details and status of a workflow run.
type RunInfo struct {
	ID           string                     `json:"id"`
	Status       RunStatus                  `json:"status"`
	WorkflowName string                     `json:"workflowName,omitempty"`
	ErrorMessage string                     `json:"errorMessage,omitempty"`
	StartedAt    *time.Time                 `json:"startedAt,omitempty"`
	FinishedAt   *time.Time                 `json:"finishedAt,omitempty"`
	DurationMs   int                        `json:"durationMs,omitempty"`
	Raw          *rest.V1WorkflowRunDetails `json:"-"`
}

// GetRun returns full details of a workflow run by its run ID as a clean RunInfo domain model.
func (c *Client) GetRun(ctx context.Context, runID string) (*RunInfo, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("hatchetx: client disabled")
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return nil, fmt.Errorf("hatchetx: runId is required")
	}
	details, err := c.SDK.Runs().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if details == nil {
		return nil, fmt.Errorf("hatchetx: run not found: %s", id)
	}

	info := &RunInfo{
		ID:           id,
		Status:       mapTaskStatus(details.Run.Status),
		WorkflowName: details.Run.DisplayName,
		StartedAt:    details.Run.StartedAt,
		FinishedAt:   details.Run.FinishedAt,
		Raw:          details,
	}
	if details.Run.Duration != nil {
		info.DurationMs = *details.Run.Duration
	}
	if details.Run.ErrorMessage != nil {
		info.ErrorMessage = *details.Run.ErrorMessage
	}
	return info, nil
}

// GetRunStatus returns the current execution status of a workflow run.
func (c *Client) GetRunStatus(ctx context.Context, runID string) (RunStatus, error) {
	if !c.Enabled() {
		return "", fmt.Errorf("hatchetx: client disabled")
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return "", fmt.Errorf("hatchetx: runId is required")
	}
	status, err := c.SDK.Runs().GetStatus(ctx, id)
	if err != nil {
		return "", err
	}
	if status == nil {
		return RunStatusUnknown, nil
	}
	return mapTaskStatus(*status), nil
}

func mapTaskStatus(s rest.V1TaskStatus) RunStatus {
	switch string(s) {
	case string(RunStatusQueued):
		return RunStatusQueued
	case string(RunStatusRunning):
		return RunStatusRunning
	case string(RunStatusCompleted):
		return RunStatusCompleted
	case string(RunStatusFailed):
		return RunStatusFailed
	case string(RunStatusCancelled):
		return RunStatusCancelled
	default:
		return RunStatus(s)
	}
}

// CancelRun requests cancellation of an active workflow run.
func (c *Client) CancelRun(ctx context.Context, runID string) error {
	if !c.Enabled() {
		return fmt.Errorf("hatchetx: client disabled")
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return fmt.Errorf("hatchetx: runId is required")
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("hatchetx: invalid runId %q: %w", runID, err)
	}
	ids := []openapi_types.UUID{u}
	_, err = c.SDK.Runs().Cancel(ctx, rest.V1CancelTaskRequest{
		ExternalIds: &ids,
	})
	return err
}

// ReplayRun requests a replay/retry of a workflow run.
func (c *Client) ReplayRun(ctx context.Context, runID string) error {
	if !c.Enabled() {
		return fmt.Errorf("hatchetx: client disabled")
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return fmt.Errorf("hatchetx: runId is required")
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("hatchetx: invalid runId %q: %w", runID, err)
	}
	ids := []openapi_types.UUID{u}
	_, err = c.SDK.Runs().Replay(ctx, rest.V1ReplayTaskRequest{
		ExternalIds: &ids,
	})
	return err
}

// SubscribeToStream subscribes to the real-time event/log stream of a workflow run.
func (c *Client) SubscribeToStream(ctx context.Context, runID string) (<-chan string, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("hatchetx: client disabled")
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return nil, fmt.Errorf("hatchetx: runId is required")
	}
	return c.SDK.Runs().SubscribeToStream(ctx, id), nil
}
