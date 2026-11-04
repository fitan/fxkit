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

// RunStatus 表示 workflow 运行的执行状态。
type RunStatus string

const (
	RunStatusQueued    RunStatus = "QUEUED"
	RunStatusRunning   RunStatus = "RUNNING"
	RunStatusCompleted RunStatus = "COMPLETED"
	RunStatusFailed    RunStatus = "FAILED"
	RunStatusCancelled RunStatus = "CANCELLED"
	RunStatusUnknown   RunStatus = "UNKNOWN"
)

// RunInfo 包含 workflow 运行的结构化执行详情与状态。
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

// GetRun 按 run ID 返回 workflow 运行的完整详情，并整理为干净的 RunInfo 领域模型。
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

// GetRunStatus 返回 workflow 运行的当前执行状态。
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

// CancelRun 请求取消活跃的 workflow 运行。
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

// ReplayRun 请求重放/重试 workflow 运行。
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

// SubscribeToStream 订阅 workflow 运行的实时事件/日志流。
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
