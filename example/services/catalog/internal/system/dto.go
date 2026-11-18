package system

import (
	"time"

	"github.com/fitan/fxkit/hatchetx"
)

// IncInput 是 counter actor 的输入；ActorRef 提供 actorId 供 Hatchet 队列串行化。
type IncInput struct {
	hatchetx.ActorRef
	// Delta 是本次增量，默认 1。
	Delta int64 `json:"delta"`
	// HoldMs 是人为阻塞时长（演示并发）。
	HoldMs int `json:"holdMs,omitempty"`
}

// IncOutput 返回累加结果与进入处理器时的并发观测值。
type IncOutput struct {
	// Value 是累加后的值。
	Value int64 `json:"value"`
	// Inflight 是进入处理器时的进程级计数。
	Inflight int32 `json:"inflight"`
	// ActorInflight 是进入处理器时该 actor 的计数。
	ActorInflight int32 `json:"actorInflight"`
}

// HeartbeatIn 是 reminder tick 的输入。
type HeartbeatIn struct {
	hatchetx.ActorRef
	// ReminderName 是提醒器名称。
	ReminderName string `json:"reminderName"`
}

// HeartbeatOut 返回累计 tick 次数与最后一次时间。
type HeartbeatOut struct {
	// Ticks 是累计 tick 次数。
	Ticks int64 `json:"ticks"`
	// LastTick 是最后一次 tick 时间。
	LastTick time.Time `json:"lastTick"`
}
