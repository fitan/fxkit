package system

import (
	"encoding/json"
	"testing"

	"github.com/fitan/fxkit/hatchetx"
)

// TestIncInput_JSONActorIDForCEL 断言 actorId 的 JSON 字段名。
// Hatchet 的 CEL 队列表达式使用 input.actorId，改字段名会让同 actor 串行失效。
func TestIncInput_JSONActorIDForCEL(t *testing.T) {
	in := IncInput{ActorRef: hatchetx.ActorRef{ActorID: "counter-1"}, Delta: 2}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	got, _ := m["actorId"].(string)
	if got != "counter-1" {
		t.Fatalf("json actorId=%q body=%s (Hatchet CEL input.actorId needs this field)", got, raw)
	}
	if in.GetActorID() != "counter-1" {
		t.Fatalf("GetActorID=%q", in.GetActorID())
	}
}
