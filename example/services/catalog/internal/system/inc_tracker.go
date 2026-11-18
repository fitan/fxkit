package system

import (
	"sync"
	"sync/atomic"
	"time"
)

// maxHold 是 actor 测试里人为阻塞的时长上限，避免把请求拖太久。
const maxHold = 3 * time.Second

// incTracker 记录进程级和 actor 级的并发数，用于验证 Actor 队列语义。
type incTracker struct {
	global  atomic.Int32
	byActor sync.Map // string -> *atomic.Int32
}

// counterInflight 是进程级共享的 inflight 计数器（仅供演示）。
var counterInflight incTracker

// capHold 把请求里的 holdMs 夹到 [0, maxHold]。
func capHold(ms int) time.Duration {
	if ms <= 0 {
		return 0
	}
	d := time.Duration(ms) * time.Millisecond
	if d > maxHold {
		return maxHold
	}
	return d
}

// enter 同时递增全局与 actor 计数，返回离开时的清理函数。
func (t *incTracker) enter(actorID string) (global, actor int32, leave func()) {
	g := t.global.Add(1)
	v, _ := t.byActor.LoadOrStore(actorID, &atomic.Int32{})
	a := v.(*atomic.Int32).Add(1)
	return g, a, func() {
		v.(*atomic.Int32).Add(-1)
		t.global.Add(-1)
	}
}
