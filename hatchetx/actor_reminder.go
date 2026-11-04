package hatchetx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	"github.com/hatchet-dev/hatchet/sdks/go/features"
)

// CreateReminderInput schedules a recurring Hatchet cron that delivers input to a workflow.
//
// Semantics:
//   - period only: runs on schedule until deleted (no dueTime / one-shot mode)
//   - missed schedules are not replayed
//   - when Upsert is true, any existing reminder with the same workflow and Name is replaced
type CreateReminderInput struct {
	WorkflowName string // actor / standalone task name
	Name         string // cron trigger name; default reminderName-actorId
	ActorType    string
	ActorID      string // required; stamped into input as actorId for concurrency
	ReminderName string
	Period       string         // standard cron (e.g. "*/15 * * * * *") or @hourly, @daily
	Data         string         // optional payload data
	Metadata     map[string]any // optional Hatchet cron metadata
	Input        map[string]any // optional full cron input; default ReminderPayload fields
	Upsert       bool           // if true, replace any existing cron with the same name
}

// ReminderPayload is the default input shape delivered to actor reminder workflows.
// Embeds [ActorRef] so the target actor's concurrency key (input.actorId) is set.
type ReminderPayload struct {
	ActorRef
	ActorType    string `json:"actorType"`
	ReminderName string `json:"reminderName"`
	Data         string `json:"data,omitempty"`
}

// CreateReminder attaches a recurring Hatchet cron to a workflow (reminder-like ticks).
// When in.Upsert is true, replaces any existing cron trigger with the same name.
func CreateReminder(ctx context.Context, client *Client, in CreateReminderInput) (cronID string, err error) {
	if client == nil || !client.Enabled() || client.Crons() == nil {
		return "", fmt.Errorf("hatchetx: client disabled")
	}
	workflow := strings.TrimSpace(in.WorkflowName)
	if workflow == "" {
		return "", fmt.Errorf("hatchetx: workflow name is required")
	}
	actorID := strings.TrimSpace(in.ActorID)
	if actorID == "" {
		return "", fmt.Errorf("hatchetx: actorId is required")
	}
	reminder := strings.TrimSpace(in.ReminderName)
	if reminder == "" {
		return "", fmt.Errorf("hatchetx: reminder name is required")
	}
	expr, err := NormalizeCronExpression(in.Period)
	if err != nil {
		return "", err
	}

	cronName := strings.TrimSpace(in.Name)
	if cronName == "" {
		cronName = reminder + "-" + actorID
	}

	if in.Upsert {
		if err := DeleteReminderByName(ctx, client, workflow, cronName); err != nil {
			return "", fmt.Errorf("hatchetx: upsert delete existing reminder: %w", err)
		}
	}

	payload := in.Input
	if payload == nil {
		payload, err = toInputMap(ReminderPayload{
			ActorRef:     ActorRef{ActorID: actorID},
			ActorType:    strings.TrimSpace(in.ActorType),
			ReminderName: reminder,
			Data:         in.Data,
		})
		if err != nil {
			return "", err
		}
	} else {
		// Always stamp the validated actorId so concurrency keys stay correct.
		payload = cloneMap(payload)
		payload["actorId"] = actorID
	}

	meta := map[string]any{
		"fxkit_actor_id": actorID,
		"fxkit_reminder": reminder,
		"period":         in.Period,
	}
	if t := strings.TrimSpace(in.ActorType); t != "" {
		meta["fxkit_actor_type"] = t
	}
	for k, v := range in.Metadata {
		meta[k] = v
	}

	created, err := client.Crons().Create(ctx, workflow, features.CreateCronTrigger{
		Name:               cronName,
		Expression:         expr,
		Input:              payload,
		AdditionalMetadata: meta,
	})
	if err != nil {
		return "", fmt.Errorf("hatchetx: create reminder: %w", err)
	}
	if created == nil {
		return "", fmt.Errorf("hatchetx: create reminder: empty response")
	}
	return created.Metadata.Id, nil
}

// UpsertReminder creates or replaces a recurring reminder by name.
func UpsertReminder(ctx context.Context, client *Client, in CreateReminderInput) (string, error) {
	in.Upsert = true
	return CreateReminder(ctx, client, in)
}

// FindReminder looks up an existing cron trigger for a workflow by cronName.
func FindReminder(ctx context.Context, client *Client, workflowName, cronName string) (*rest.CronWorkflows, error) {
	if client == nil || !client.Enabled() || client.Crons() == nil {
		return nil, fmt.Errorf("hatchetx: client disabled")
	}
	workflowName = strings.TrimSpace(workflowName)
	cronName = strings.TrimSpace(cronName)
	if workflowName == "" || cronName == "" {
		return nil, fmt.Errorf("hatchetx: workflowName and cronName are required")
	}
	limit := int64(50)
	res, err := client.Crons().List(ctx, rest.CronWorkflowListParams{
		WorkflowName: &workflowName,
		CronName:     &cronName,
		Limit:        &limit,
	})
	if err != nil {
		return nil, err
	}
	if res == nil || res.Rows == nil || len(*res.Rows) == 0 {
		return nil, nil
	}
	for _, row := range *res.Rows {
		if (row.WorkflowName == "" || row.WorkflowName == workflowName) && row.Name != nil && *row.Name == cronName {
			r := row
			return &r, nil
		}
	}
	return nil, nil
}

// FindActorReminder looks up an existing reminder for an actor instance by workflowName, reminderName and actorID.
func FindActorReminder(ctx context.Context, client *Client, workflowName, reminderName, actorID string) (*rest.CronWorkflows, error) {
	reminder := strings.TrimSpace(reminderName)
	id := strings.TrimSpace(actorID)
	if reminder == "" || id == "" {
		return nil, fmt.Errorf("hatchetx: reminderName and actorID are required")
	}
	cronName := reminder + "-" + id
	return FindReminder(ctx, client, workflowName, cronName)
}

// DeleteActorReminder removes an existing reminder for an actor instance by workflowName, reminderName and actorID.
func DeleteActorReminder(ctx context.Context, client *Client, workflowName, reminderName, actorID string) error {
	reminder := strings.TrimSpace(reminderName)
	id := strings.TrimSpace(actorID)
	if reminder == "" || id == "" {
		return fmt.Errorf("hatchetx: reminderName and actorID are required")
	}
	cronName := reminder + "-" + id
	return DeleteReminderByName(ctx, client, workflowName, cronName)
}

// DeleteReminderByName deletes an existing cron trigger by workflow and cronName.
func DeleteReminderByName(ctx context.Context, client *Client, workflowName, cronName string) error {
	found, err := FindReminder(ctx, client, workflowName, cronName)
	if err != nil {
		return err
	}
	if found == nil {
		return nil
	}
	return DeleteReminder(ctx, client, found.Metadata.Id)
}

// DeleteReminder removes a Hatchet cron by id.
// In-flight runs are not cancelled; only future cron triggers stop.
//
// 已知 SDK 问题：Hatchet 服务端对 DELETE 返回 204 No Content，而 Go SDK（截至
// v0.116.x）的 CronsClient.Delete 只接受 200，会把成功当失败返回。
// 这里不依赖错误文本判断成功，而是**回读确认**：
//   - Get 成功   → 仍然存在 → 返回原始错误
//   - Get 404    → 已删除   → 视为成功
//   - Get 其他错 → 无法确认 → 返回原始错误
func DeleteReminder(ctx context.Context, client *Client, cronID string) error {
	if client == nil || !client.Enabled() || client.Crons() == nil {
		return fmt.Errorf("hatchetx: client disabled")
	}
	cronID = strings.TrimSpace(cronID)
	if cronID == "" {
		return fmt.Errorf("hatchetx: cron id is required")
	}
	if err := client.Crons().Delete(ctx, cronID); err != nil {
		if !isNoContentErr(err) {
			return fmt.Errorf("hatchetx: delete reminder: %w", err)
		}
		// SDK 把 204 当错误；以「列表里是否还有这个 id」为准（最终一致，轮询）。
		deadline := time.Now().Add(10 * time.Second)
		for {
			absent, verifyErr := cronAbsent(ctx, client, cronID)
			if verifyErr != nil {
				return fmt.Errorf("hatchetx: delete reminder: %w (verify: %w)", err, verifyErr)
			}
			if absent {
				return nil
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("hatchetx: delete reminder: %w (cron still listed after 10s)", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	return nil
}

// cronAbsent 通过列表确认 cron 是否已不存在（Get 对已删除的记录仍可能返回 200）。
func cronAbsent(ctx context.Context, client *Client, cronID string) (bool, error) {
	limit := int64(200)
	res, err := client.Crons().List(ctx, rest.CronWorkflowListParams{Limit: &limit})
	if err != nil {
		return false, err
	}
	if res == nil || res.Rows == nil {
		return true, nil
	}
	for _, row := range *res.Rows {
		if row.Metadata.Id == cronID {
			return false, nil
		}
	}
	return true, nil
}

// isNoContentErr 识别 SDK 对 204 的误报（它没有导出状态码类型）。
func isNoContentErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "got status 204")
}

func toInputMap(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("hatchetx: encode reminder input: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("hatchetx: decode reminder input: %w", err)
	}
	return m, nil
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}
