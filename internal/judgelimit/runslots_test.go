package judgelimit

import (
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// N runs at once never hold more than RunSlots slots, and none of them fails: they queue.
func TestRunSlotsAtMostNHolders(t *testing.T) {
	l := Limiter{Dir: t.TempDir(), RunSlots: 2, Poll: 5 * time.Millisecond, Notify: time.Hour}
	var cur, peak int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := l.AcquireRunSlot()
			if err != nil {
				t.Error(err)
				return
			}
			n := atomic.AddInt32(&cur, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
					break
				}
			}
			time.Sleep(15 * time.Millisecond)
			atomic.AddInt32(&cur, -1)
			rel()
		}()
	}
	wg.Wait()
	if peak != 2 {
		t.Fatalf("peak holders = %d, want 2", peak)
	}
}

// Run slots and judge slots are separate pools: a full set of judges does not hold a run back.
func TestRunSlotsAreNotJudgeSlots(t *testing.T) {
	l := Limiter{Dir: t.TempDir(), Slots: 1, RunSlots: 1, Poll: 5 * time.Millisecond, Notify: time.Hour}
	judge, err := l.AcquireSlot()
	if err != nil {
		t.Fatal(err)
	}
	defer judge()
	done := make(chan struct{})
	go func() {
		rel, _ := l.AcquireRunSlot()
		rel()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a run waited behind a judge")
	}
}

// A run started by a run that holds a slot takes none, or it would wait behind its own parent.
func TestNestedRunTakesNoSlot(t *testing.T) {
	l := Limiter{Dir: t.TempDir(), RunSlots: 1, Poll: 5 * time.Millisecond, Notify: time.Hour}
	release, err := l.HoldRunSlot()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv(RunHeldEnv) == "" {
		t.Fatal("the holder did not mark its children")
	}
	done := make(chan struct{})
	go func() {
		rel, _ := l.HoldRunSlot() // the nested one: would wait forever for the only slot
		rel()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a nested run queued behind its own parent")
	}
	release()
	if os.Getenv(RunHeldEnv) != "" {
		t.Fatal("the mark outlived the slot")
	}
}

func TestRunSlotsDefaultsToAQuarterOfTheCPUs(t *testing.T) {
	t.Setenv(RunSlotsEnv, "")
	if got, want := DefaultRunSlots(), max(2, runtime.NumCPU()/4); got != want || RunSlots() != want {
		t.Fatalf("slots = %d, want %d", got, want)
	}
	t.Setenv(RunSlotsEnv, "5")
	if RunSlots() != 5 {
		t.Fatalf("the environment did not win: %d", RunSlots())
	}
	if Fanout() < 2 {
		t.Fatalf("fanout %d", Fanout())
	}
}
