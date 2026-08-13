package hatchetx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hatchet-dev/hatchet/sdks/go/features"
)

// CreateReminderInput schedules a recurring Hatchet cron that delivers input to a workflow.
//
// This is not a full Dapr Actor Reminder: there is no dueTime / one-shot mode,
// missed schedules are not replayed, and Create is not upsert (duplicate
// Name+Expression may fail Hatchet's unique constraint — delete first or use a new name).
type CreateReminderInput struct {
	WorkflowName string // actor / standalone task name
	Name         string // cron trigger name; default reminderName-actorId
	ActorType    string
	ActorID      string // required; stamped into input as actorId for concurrency
	ReminderName string
	Period       string         // 15s, @every 15s, or cron expression
	Data         string         // optional payload data
	Metadata     map[string]any // optional Hatchet cron metadata
	Input        map[string]any // optional full cron input; default ReminderPayload fields
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
//
// Semantics vs Dapr Actor Reminder:
//   - period only — runs on schedule until [DeleteReminder]
//   - no catch-up for ticks missed while the system was down
//   - not idempotent upsert — callers should track the returned cron id
//
// Overlapping ticks for the same actorId are serialized when the workflow is a
// [NewActor] with MaxRuns=1 (queued via GROUP_ROUND_ROBIN).
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

	cronName := strings.TrimSpace(in.Name)
	if cronName == "" {
		cronName = reminder + "-" + actorID
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

// DeleteReminder removes a Hatchet cron by id.
// In-flight runs are not cancelled; only future cron triggers stop.
func DeleteReminder(ctx context.Context, client *Client, cronID string) error {
	if client == nil || !client.Enabled() || client.Crons() == nil {
		return fmt.Errorf("hatchetx: client disabled")
	}
	cronID = strings.TrimSpace(cronID)
	if cronID == "" {
		return fmt.Errorf("hatchetx: cron id is required")
	}
	return client.Crons().Delete(ctx, cronID)
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
