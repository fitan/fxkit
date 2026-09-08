package platform

import (
	"sync"
	"testing"
	"time"
)

func TestCapHold(t *testing.T) {
	if capHold(0) != 0 || capHold(-1) != 0 {
		t.Fatal("zero")
	}
	if capHold(50) != 50*time.Millisecond {
		t.Fatal("50ms")
	}
	if capHold(99999) != maxHold {
		t.Fatalf("cap=%s", capHold(99999))
	}
}

func TestIncTracker_SameIDOverlapVisible(t *testing.T) {
	var tr incTracker
	g1, a1, leave1 := tr.enter("x")
	g2, a2, leave2 := tr.enter("x")
	if g1 != 1 || a1 != 1 {
		t.Fatalf("first global=%d actor=%d", g1, a1)
	}
	if g2 != 2 || a2 != 2 {
		t.Fatalf("second same id should overlap global=%d actor=%d", g2, a2)
	}
	leave2()
	leave1()
	g3, a3, leave3 := tr.enter("x")
	leave3()
	if g3 != 1 || a3 != 1 {
		t.Fatalf("after leave global=%d actor=%d", g3, a3)
	}
}

func TestIncTracker_DifferentIDsOverlapGlobalOnly(t *testing.T) {
	var tr incTracker
	g1, a1, leave1 := tr.enter("a")
	g2, a2, leave2 := tr.enter("b")
	if g1 != 1 || a1 != 1 || g2 != 2 || a2 != 1 {
		t.Fatalf("a=(%d,%d) b=(%d,%d)", g1, a1, g2, a2)
	}
	leave1()
	leave2()
}

func TestIncTracker_ParallelDifferentIDs(t *testing.T) {
	var tr incTracker
	var wg sync.WaitGroup
	wg.Add(2)
	seen := make(chan int32, 2)
	enter := func(id string) {
		defer wg.Done()
		g, a, leave := tr.enter(id)
		if a != 1 {
			t.Errorf("%s actorInflight=%d want 1", id, a)
		}
		seen <- g
		time.Sleep(30 * time.Millisecond)
		leave()
	}
	go enter("p")
	go enter("q")
	wg.Wait()
	close(seen)
	var max int32
	for g := range seen {
		if g > max {
			max = g
		}
	}
	if max < 2 {
		t.Fatalf("expected overlapping global inflight, max=%d", max)
	}
}
