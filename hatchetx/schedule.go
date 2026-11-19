package hatchetx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	"github.com/hatchet-dev/hatchet/sdks/go/features"
)

// WorkflowScheduledListParams 是 Hatchet 定时运行查询过滤选项的别名。
type WorkflowScheduledListParams = rest.WorkflowScheduledListParams

// ScheduleOnceInput 配置未来某一时刻的一次性 workflow 执行。
//
// 与周期性 cron 定时提醒（[CreateReminder]）不同，ScheduleOnce 只在 RunAt
// 触发一次，可通过返回的 scheduledRunID 查询或取消。
type ScheduleOnceInput struct {
	// WorkflowName 是要触发的 workflow 名称，必填。
	WorkflowName string // 目标 workflow 名称
	// RunAt 是计划执行时间，必填；零值会返回错误。
	RunAt time.Time // workflow 应执行的时间
	// Input 是投递给 workflow 的载荷，会经 JSON 转成 map。
	Input any // 投递给 workflow 的载荷
	// Priority 是可选的运行优先级。
	Priority *RunPriority // 可选优先级
	// Metadata 是附加到定时运行的可选 metadata。
	Metadata map[string]any // 附加到定时运行的可选 metadata
}

// ScheduledRun 表示一次性的定时 workflow 运行。
type ScheduledRun struct {
	// ID 是定时运行 id，可用于 [CancelScheduled]。
	ID string `json:"id"`
	// WorkflowName 是定时运行要触发的 workflow 名称。
	WorkflowName string `json:"workflowName"`
	// TriggerAt 是计划执行时间，来自 [ScheduleOnceInput.RunAt]。
	TriggerAt time.Time `json:"triggerAt"`
	// Status 是关联 workflow 运行的状态；尚未产生运行时为空字符串。
	Status string `json:"status,omitempty"`
	// Raw 是 Hatchet 返回的原始记录，不参与 JSON 序列化。
	Raw *rest.ScheduledWorkflows `json:"-"`
}

// ScheduleOnce 安排 workflow 在未来某一时刻执行一次（dueTime 语义）。
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

// CancelScheduled 取消先前安排的一次性 workflow 运行。
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

// ListScheduled 列出符合给定过滤参数的定时 workflow 运行，并整理为干净的 ScheduledRun 模型。
func ListScheduled(ctx context.Context, client *Client, opts WorkflowScheduledListParams) ([]ScheduledRun, error) {
	if client == nil || !client.Enabled() || client.Schedules() == nil {
		return nil, fmt.Errorf("hatchetx: client disabled")
	}
	res, err := client.Schedules().List(ctx, opts)
	if err != nil {
		return nil, err
	}
	if res == nil || res.Rows == nil {
		return nil, nil
	}
	runs := make([]ScheduledRun, 0, len(*res.Rows))
	for _, row := range *res.Rows {
		status := ""
		if row.WorkflowRunStatus != nil {
			status = string(*row.WorkflowRunStatus)
		}
		r := row
		runs = append(runs, ScheduledRun{
			ID:           row.Metadata.Id,
			WorkflowName: row.WorkflowName,
			TriggerAt:    row.TriggerAt,
			Status:       status,
			Raw:          &r,
		})
	}
	return runs, nil
}

// ScheduleOnce 通过该 client 安排未来某一时刻的一次性 workflow 执行。
func (c *Client) ScheduleOnce(ctx context.Context, in ScheduleOnceInput) (string, error) {
	return ScheduleOnce(ctx, c, in)
}

// CancelScheduled 通过该 client 取消先前安排的 workflow 运行。
func (c *Client) CancelScheduled(ctx context.Context, scheduledRunID string) error {
	return CancelScheduled(ctx, c, scheduledRunID)
}

// ListScheduled 通过该 client 列出符合给定过滤参数的定时 workflow 运行。
func (c *Client) ListScheduled(ctx context.Context, opts WorkflowScheduledListParams) ([]ScheduledRun, error) {
	return ListScheduled(ctx, c, opts)
}
