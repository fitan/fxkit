package hatchetx

import (
	"context"
	"strings"
	"testing"
)

func TestCreateReminder_Validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// client 为 nil
	if _, err := CreateReminder(ctx, nil, CreateReminderInput{}); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	// 禁用的 client
	disabled := &Client{}
	if _, err := CreateReminder(ctx, disabled, CreateReminderInput{}); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	// 未初始化 SDK 的空 client 包装
	client := &Client{}

	// 空 workflow
	in := CreateReminderInput{
		WorkflowName: "",
		ActorID:      "a1",
		ReminderName: "r1",
		Period:       "@hourly",
	}
	// client 已禁用，会先返回 client disabled
	if _, err := CreateReminder(ctx, client, in); err == nil {
		t.Fatal("expected error")
	}
}

func TestFindReminder_Validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	if _, err := FindReminder(ctx, nil, "wf", "c1"); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	if _, err := FindActorReminder(ctx, &Client{}, "wf", "", "a1"); err == nil || !strings.Contains(err.Error(), "reminderName and actorID are required") {
		t.Fatalf("expected reminderName and actorID required error, got %v", err)
	}

	if _, err := FindActorReminder(ctx, &Client{}, "wf", "r1", ""); err == nil || !strings.Contains(err.Error(), "reminderName and actorID are required") {
		t.Fatalf("expected reminderName and actorID required error, got %v", err)
	}
}

func TestDeleteReminder_Validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	if err := DeleteReminder(ctx, nil, "c1"); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	if err := DeleteActorReminder(ctx, &Client{}, "wf", "", "a1"); err == nil || !strings.Contains(err.Error(), "reminderName and actorID are required") {
		t.Fatalf("expected reminderName and actorID required error, got %v", err)
	}

	if err := DeleteActorReminder(ctx, &Client{}, "wf", "r1", ""); err == nil || !strings.Contains(err.Error(), "reminderName and actorID are required") {
		t.Fatalf("expected reminderName and actorID required error, got %v", err)
	}
}

// TestDeleteReminder_ValidationOnly 确认删除失败时不再依赖 SDK 错误文本。
//
// 旧实现靠 isNoContentErr("got status 204") 判定，SDK 文案一变就失效；
// 现在删除错误一律交给列表回读确认（见 cronState）。
func TestDeleteReminder_ValidationOnly(t *testing.T) {
	t.Parallel()
	if err := DeleteReminder(context.Background(), nil, "c1"); err == nil {
		t.Fatal("nil client must fail")
	}
	if err := DeleteReminder(context.Background(), &Client{}, "  "); err == nil {
		t.Fatal("blank id must fail")
	}
}

func TestToInputMap_Payload(t *testing.T) {
	t.Parallel()
	payload := ReminderPayload{
		ActorRef:     ActorRef{ActorID: "actor-42"},
		ActorType:    "device",
		ReminderName: "heartbeat",
		Data:         `{"status":"ok"}`,
	}
	m, err := toInputMap(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["actorId"] != "actor-42" {
		t.Fatalf("expected actorId=actor-42, got %v", m["actorId"])
	}
	if m["actorType"] != "device" {
		t.Fatalf("expected actorType=device, got %v", m["actorType"])
	}
	if m["reminderName"] != "heartbeat" {
		t.Fatalf("expected reminderName=heartbeat, got %v", m["reminderName"])
	}
}
