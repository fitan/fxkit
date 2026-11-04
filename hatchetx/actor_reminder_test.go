package hatchetx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCreateReminder_Validation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// nil client
	if _, err := CreateReminder(ctx, nil, CreateReminderInput{}); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	// disabled client
	disabled := &Client{}
	if _, err := CreateReminder(ctx, disabled, CreateReminderInput{}); err == nil || !strings.Contains(err.Error(), "client disabled") {
		t.Fatalf("expected client disabled error, got %v", err)
	}

	// dummy client wrapper without SDK initialized
	client := &Client{}

	// empty workflow
	in := CreateReminderInput{
		WorkflowName: "",
		ActorID:      "a1",
		ReminderName: "r1",
		Period:       "@hourly",
	}
	// Since client is disabled, will return client disabled first
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

// TestIsNoContentErr 锁定「SDK 204 误报」的识别（删除结果另行列表校验）。
func TestIsNoContentErr(t *testing.T) {
	t.Parallel()
	if isNoContentErr(nil) {
		t.Fatal("nil must be false")
	}
	if !isNoContentErr(errors.New("received non-200 response from server. got status 204 with body ''")) {
		t.Fatal("204 must be recognized")
	}
	if isNoContentErr(errors.New("got status 500")) {
		t.Fatal("500 must not be treated as 204")
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
