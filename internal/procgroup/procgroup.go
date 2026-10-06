// Package procgroup keeps the children this process starts from outliving it.
//
// A child that must be killable as a tree (a check script that spawns a model call that spawns
// tools) is started in its OWN process group, so one signal to the negated pgid reaches all of
// it. The cost is that a signal sent to this process or its group no longer reaches the child:
// it is orphaned when this process is killed. So every such child is also registered here, and a
// process that handles SIGTERM/SIGINT (ExitOnSignal) signals every registered group before it
// exits: SIGTERM, a grace period, then SIGKILL.
package procgroup

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Grace is how long a group gets between SIGTERM and SIGKILL.
const Grace = 3 * time.Second

var live = struct {
	sync.Mutex
	pgids map[int]int // pgid -> how many registrations
}{pgids: map[int]int{}}

// Own makes c start in its own process group. Call before Start.
func Own(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

// Track registers the group c leads (c started with Own) until the returned func is called,
// which is when c has been waited for. A c that has not started is not tracked.
func Track(c *exec.Cmd) (untrack func()) {
	if c.Process == nil {
		return func() {}
	}
	pgid := c.Process.Pid
	live.Lock()
	live.pgids[pgid]++
	live.Unlock()
	return func() {
		live.Lock()
		if live.pgids[pgid]--; live.pgids[pgid] <= 0 {
			delete(live.pgids, pgid)
		}
		live.Unlock()
	}
}

// Tracked is how many groups are registered.
func Tracked() int {
	live.Lock()
	defer live.Unlock()
	return len(live.pgids)
}

// KillAll terminates every registered group: SIGTERM to all, then SIGKILL to what is left once
// grace has passed (earlier when every group is gone).
func KillAll(grace time.Duration) {
	live.Lock()
	var all []int
	for pgid := range live.pgids {
		all = append(all, pgid)
	}
	live.Unlock()
	terminate(all, grace)
}

// Terminate is KillAll for one group.
func Terminate(pgid int, grace time.Duration) { terminate([]int{pgid}, grace) }

func terminate(pgids []int, grace time.Duration) {
	var alive []int
	for _, p := range pgids {
		if err := syscall.Kill(-p, syscall.SIGTERM); err == nil {
			alive = append(alive, p)
		}
	}
	deadline := time.Now().Add(grace)
	for len(alive) > 0 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		alive = stillThere(alive)
	}
	for _, p := range alive {
		_ = syscall.Kill(-p, syscall.SIGKILL)
	}
}

func stillThere(pgids []int) []int {
	var out []int
	for _, p := range pgids {
		if err := syscall.Kill(-p, 0); err == nil || !errors.Is(err, syscall.ESRCH) {
			out = append(out, p)
		}
	}
	return out
}

// ExitOnSignal makes SIGTERM, SIGINT and SIGHUP kill every registered group, run before (when
// not nil), and exit with the conventional 128+signal status. The returned func stops the handler.
func ExitOnSignal(before func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		select {
		case sig := <-ch:
			KillAll(Grace)
			if before != nil {
				before()
			}
			code := 128 + int(syscall.SIGTERM)
			if s, ok := sig.(syscall.Signal); ok {
				code = 128 + int(s)
			}
			os.Exit(code)
		case <-done:
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}
