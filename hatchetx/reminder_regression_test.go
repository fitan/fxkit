package hatchetx

import "testing"

func TestReminderCronNameIsUnambiguous(t *testing.T) {
	a := reminderCronName("job-a", "b")
	b := reminderCronName("job", "a-b")
	if a == b {
		t.Fatalf("cron name collision: %q == %q", a, b)
	}
}
