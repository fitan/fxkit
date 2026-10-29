package hatchetx

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// GetRun returns full details of a workflow run by its run ID.
func (c *Client) GetRun(ctx context.Context, runID string) (*rest.V1WorkflowRunDetails, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("hatchetx: client disabled")
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return nil, fmt.Errorf("hatchetx: runId is required")
	}
	return c.SDK.Runs().Get(ctx, id)
}

// GetRunStatus returns the current execution status of a workflow run.
func (c *Client) GetRunStatus(ctx context.Context, runID string) (*rest.V1TaskStatus, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("hatchetx: client disabled")
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return nil, fmt.Errorf("hatchetx: runId is required")
	}
	return c.SDK.Runs().GetStatus(ctx, id)
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
