package hatchetx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hatchet-dev/hatchet/pkg/client/rest"
	"github.com/hatchet-dev/hatchet/sdks/go/features"
)

// CreateReminderInput 定义一个周期性 Hatchet cron，向 workflow 投递输入。
//
// 语义：
//   - 仅 period：按计划一直运行直到被删除（没有 dueTime / 一次性模式）
//   - 错过的调度不会补跑
//   - Upsert 为 true 时，替换同一 workflow 和 Name 下已存在的定时提醒
type CreateReminderInput struct {
	// WorkflowName 是目标 workflow 名称，必填。
	WorkflowName string // actor / standalone task 名称
	// Name 是 cron 触发器名称；为空时由 ReminderName 与 ActorID 生成。
	Name string // cron 触发器名称；默认 reminderName-<len>-actorId（无歧义）
	// ActorType 是可选的目标 actor 类型名；非空时写入 payload 与 metadata fxkit_actor_type。
	ActorType string
	// ActorID 是目标 actor 实例 id，必填；写入输入作为并发键。
	ActorID string // 必填；作为 actorId 写入输入，用于并发
	// ReminderName 是提醒名称，必填；写入 payload 与 metadata fxkit_reminder，并参与默认 cron 名。
	ReminderName string
	// Period 是标准 cron 表达式或 @hourly、@daily 等别名。
	Period string // 标准 cron（例如 "*/15 * * * * *"）或 @hourly、@daily
	// Data 是投递给 workflow 的可选载荷数据。
	Data string // 可选载荷数据
	// Metadata 是可选 Hatchet cron metadata，与 fxkit_* 键合并（同名键覆盖）。
	Metadata map[string]any // 可选 Hatchet cron metadata
	// Input 是可选完整 cron 输入；为 nil 时用 ReminderPayload 字段构造。
	Input map[string]any // 可选完整 cron 输入；默认使用 ReminderPayload 字段
	// Upsert 为 true 时替换同名已存在的 cron（先建后删，避免无提醒窗口）。
	Upsert bool // 为 true 时替换同名已存在的 cron
}

// ReminderPayload 是投递给 actor 定时提醒 workflow 的默认输入结构。
// 内嵌 [ActorRef]，以便设置目标 actor 的并发键（input.actorId）。
type ReminderPayload struct {
	// ActorRef 提供目标 actor 的 actorId，用于并发键 input.actorId。
	ActorRef
	// ActorType 是目标 actor 的类型名，原样投递。
	ActorType string `json:"actorType"`
	// ReminderName 是提醒名称，原样投递。
	ReminderName string `json:"reminderName"`
	// Data 是可选的自定义载荷；为空时 JSON 中省略。
	Data string `json:"data,omitempty"`
}

// CreateReminder 给 workflow 挂上周期性 Hatchet cron（类似定时提醒的滴答）。
// 当 in.Upsert 为 true 时，替换同名已存在的 cron 触发器。
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
		cronName = reminderCronName(reminder, actorID)
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
		// 始终写入已校验的 actorId，保证并发键正确。
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

	create := func() (*rest.CronWorkflows, error) {
		return client.Crons().Create(ctx, workflow, features.CreateCronTrigger{
			Name:               cronName,
			Expression:         expr,
			Input:              payload,
			AdditionalMetadata: meta,
		})
	}

	var superseded *rest.CronWorkflows
	if in.Upsert {
		old, ferr := FindReminder(ctx, client, workflow, cronName)
		if ferr != nil {
			return "", fmt.Errorf("hatchetx: upsert lookup existing reminder: %w", ferr)
		}
		superseded = old
	}

	created, err := create()
	if err != nil && superseded != nil && superseded.Metadata.Id != "" {
		// 服务端通常拒绝同名 cron：删掉旧的重试一次。
		// 这是唯一需要「先删后建」的路径（仅当同名项确实存在）。
		if derr := DeleteReminder(ctx, client, superseded.Metadata.Id); derr != nil {
			return "", fmt.Errorf("hatchetx: upsert replace existing reminder: %w", derr)
		}
		superseded = nil
		created, err = create()
	}
	if err != nil {
		return "", fmt.Errorf("hatchetx: create reminder: %w", err)
	}
	if created == nil {
		return "", fmt.Errorf("hatchetx: create reminder: empty response")
	}
	// 新提醒已生效后再删旧 ID：删除与创建之间不再有「无提醒」窗口。
	if superseded != nil && superseded.Metadata.Id != "" && superseded.Metadata.Id != created.Metadata.Id {
		if derr := DeleteReminder(ctx, client, superseded.Metadata.Id); derr != nil {
			slog.WarnContext(ctx, "hatchetx: delete superseded reminder failed",
				"workflow", workflow, "cron", cronName,
				"old_id", superseded.Metadata.Id, "new_id", created.Metadata.Id, "error", derr)
		}
	}
	return created.Metadata.Id, nil
}

// UpsertReminder 按名称创建或替换周期性定时提醒。
func UpsertReminder(ctx context.Context, client *Client, in CreateReminderInput) (string, error) {
	in.Upsert = true
	return CreateReminder(ctx, client, in)
}

// FindReminder 按 cronName 查找 workflow 已存在的 cron 触发器。
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
	// Hatchet Go SDK 的 List 在 401/403/500 时返回 (nil, nil)：
	// 把它当成「不存在」会让删除/更新静默失效，必须显式报错。
	if res == nil {
		return nil, fmt.Errorf("hatchetx: list crons returned an empty response (the SDK hides non-200 statuses)")
	}
	if res.Rows == nil || len(*res.Rows) == 0 {
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

// FindActorReminder 按 workflowName、reminderName 和 actorID 查找 actor 实例已存在的定时提醒。
func FindActorReminder(ctx context.Context, client *Client, workflowName, reminderName, actorID string) (*rest.CronWorkflows, error) {
	reminder := strings.TrimSpace(reminderName)
	id := strings.TrimSpace(actorID)
	if reminder == "" || id == "" {
		return nil, fmt.Errorf("hatchetx: reminderName and actorID are required")
	}
	cronName := reminderCronName(reminder, id)
	return FindReminder(ctx, client, workflowName, cronName)
}

// DeleteActorReminder 按 workflowName、reminderName 和 actorID 删除 actor 实例已存在的定时提醒。
func DeleteActorReminder(ctx context.Context, client *Client, workflowName, reminderName, actorID string) error {
	reminder := strings.TrimSpace(reminderName)
	id := strings.TrimSpace(actorID)
	if reminder == "" || id == "" {
		return fmt.Errorf("hatchetx: reminderName and actorID are required")
	}
	cronName := reminderCronName(reminder, id)
	return DeleteReminderByName(ctx, client, workflowName, cronName)
}

// DeleteReminderByName 按 workflow 和 cronName 删除已存在的 cron 触发器。
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

// DeleteReminder 按 id 删除 Hatchet cron。
// 正在进行的运行不会被取消；只是停止后续的 cron 触发。
//
// 服务端对 DELETE 返回 204 No Content；Go SDK 自 v0.120.7 起接受任意 2xx，
// 因此直接以 SDK 返回值为准，不再需要回读兜底。
func DeleteReminder(ctx context.Context, client *Client, cronID string) error {
	if client == nil || !client.Enabled() || client.Crons() == nil {
		return fmt.Errorf("hatchetx: client disabled")
	}
	cronID = strings.TrimSpace(cronID)
	if cronID == "" {
		return fmt.Errorf("hatchetx: cron id is required")
	}
	if err := client.Crons().Delete(ctx, cronID); err != nil {
		return fmt.Errorf("hatchetx: delete reminder: %w", err)
	}
	return nil
}

// reminderCronName 构造无歧义的 cron 名。
//
// 直接拼 `reminder + "-" + actorID` 会让 ("job-a","b") 与 ("job","a-b") 碰撞，
// 导致查到/删掉别人的提醒。长度前缀消除了歧义。
func reminderCronName(reminder, actorID string) string {
	return fmt.Sprintf("%s-%d-%s", reminder, len(actorID), actorID)
}

// toInputMap 把 v 经 JSON 往返转换为 map[string]any。
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

// cloneMap 返回 map 的浅拷贝，便于安全覆盖单个键。
func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}
