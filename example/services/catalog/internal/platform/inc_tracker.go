package platform

import (
	"sync"
	"sync/atomic"
	"time"
)

const maxHold = 3 * time.Second

type incTracker struct {
	global  atomic.Int32
	byActor sync.Map // string -> *atomic.Int32
}

var counterInflight incTracker

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

func (t *incTracker) enter(actorID string) (global, actor int32, leave func()) {
	g := t.global.Add(1)
	v, _ := t.byActor.LoadOrStore(actorID, &atomic.Int32{})
	a := v.(*atomic.Int32).Add(1)
	return g, a, func() {
		v.(*atomic.Int32).Add(-1)
		t.global.Add(-1)
	}
}
