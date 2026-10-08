// Package procgroup keeps the children this process starts from outliving it.
//
// A child that must be killable as a tree (a check script that spawns a model call that spawns
// tools) is started in its OWN process group, so one signal to the negated pgid reaches all of
// it. The cost is that a signal sent to this process or its group no longer reaches the child:
// it is orphaned when this process is killed. So every such child is also registered here, and a
// process that handles SIGTERM/SIGINT (ExitOnSignal) signals every registered group before it
// exits: SIGTERM, a grace period, then SIGKILL.
//
// Starting and registering a child is atomic against the handler: a child started before the
// handler takes its snapshot is in it, and one started after fails to start. A second signal
// kills everything at once.
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

// BeforeLimit bounds the cleanup run by ExitOnSignal after the groups are gone.
const BeforeLimit = 5 * time.Second

// ErrClosing is returned by a start attempted once the process has begun to exit on a signal.
var ErrClosing = errors.New("procgroup: the process is exiting on a signal")

// target is a registered child: the group it leads, or just itself when it could not be given one.
type target struct {
	pid   int
	group bool
}

func (t target) kill(sig syscall.Signal) error {
	if t.group {
		return syscall.Kill(-t.pid, sig)
	}
	return syscall.Kill(t.pid, sig)
}

var state = struct {
	sync.RWMutex // starters hold the read lock; the handler's snapshot holds the write lock
	mu           sync.Mutex
	live         map[*exec.Cmd]target
	closing      bool
}{live: map[*exec.Cmd]target{}}

// KillGroup SIGKILLs the group led by pid, for an exec.Cmd's Cancel: a group already gone is
// os.ErrProcessDone, which Cancel treats as nothing to do rather than a failure.
func KillGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

// Own makes c start in its own process group. Run does it; use Own alone only with Track.
func Own(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}

// Start starts c in its own process group (grouped false: in its caller's, for a child that must
// stay in the foreground group, such as one reading a terminal) and registers it, atomically with
// respect to ExitOnSignal. The returned func unregisters it once c has been waited for. After a
// signal has begun the exit, Start fails with ErrClosing and starts nothing.
func Start(c *exec.Cmd, grouped bool) (untrack func(), err error) {
	state.RLock()
	defer state.RUnlock()
	state.mu.Lock()
	closing := state.closing
	state.mu.Unlock()
	if closing {
		return nil, ErrClosing
	}
	if grouped {
		Own(c)
	}
	if err := c.Start(); err != nil {
		return nil, err
	}
	state.mu.Lock()
	state.live[c] = target{pid: c.Process.Pid, group: grouped}
	state.mu.Unlock()
	return func() {
		state.mu.Lock()
		delete(state.live, c)
		state.mu.Unlock()
	}, nil
}

// Adopt registers c, already started by other means (on a pseudo-terminal, as the leader of a
// session of its own when group is set), as Start would have. After a signal has begun the
// exit, c is killed and Adopt fails with ErrClosing.
func Adopt(c *exec.Cmd, group bool) (untrack func(), err error) {
	state.RLock()
	defer state.RUnlock()
	t := target{pid: c.Process.Pid, group: group}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closing {
		_ = t.kill(syscall.SIGKILL)
		return nil, ErrClosing
	}
	state.live[c] = t
	return func() {
		state.mu.Lock()
		delete(state.live, c)
		state.mu.Unlock()
	}, nil
}

// Run is Start, Wait and the unregistering: c.Run for a child that a signal to this process must
// reach.
func Run(c *exec.Cmd, grouped bool) error {
	untrack, err := Start(c, grouped)
	if err != nil {
		return err
	}
	defer untrack()
	return c.Wait()
}

// Tracked is how many children are registered.
func Tracked() int {
	state.mu.Lock()
	defer state.mu.Unlock()
	return len(state.live)
}

// closeAndSnapshot stops further starts and returns what is registered.
func closeAndSnapshot() []target {
	state.Lock() // waits for every start in flight to finish registering
	defer state.Unlock()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.closing = true
	out := make([]target, 0, len(state.live))
	for _, t := range state.live {
		out = append(out, t)
	}
	return out
}

// KillAll terminates every registered child: SIGTERM to all, then SIGKILL to what is left once
// grace has passed (earlier when every one is gone). It does not stop further starts.
func KillAll(grace time.Duration) {
	state.mu.Lock()
	all := make([]target, 0, len(state.live))
	for _, t := range state.live {
		all = append(all, t)
	}
	state.mu.Unlock()
	terminate(all, grace)
}

// registered reports whether t is still a registered child: one that was waited for since the
// snapshot is gone, and its pid may belong to something else now.
func registered(t target) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, r := range state.live {
		if r == t {
			return true
		}
	}
	return false
}

func terminate(all []target, grace time.Duration) {
	var alive []target
	for _, t := range all {
		if !registered(t) {
			continue
		}
		if err := t.kill(syscall.SIGTERM); err == nil {
			alive = append(alive, t)
		}
	}
	deadline := time.Now().Add(grace)
	for len(alive) > 0 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
		alive = stillThere(alive)
	}
	for _, t := range alive {
		if registered(t) {
			_ = t.kill(syscall.SIGKILL)
		}
	}
}

func stillThere(all []target) []target {
	var out []target
	for _, t := range all {
		if !registered(t) {
			continue
		}
		if err := t.kill(0); err == nil || !errors.Is(err, syscall.ESRCH) {
			out = append(out, t)
		}
	}
	return out
}

func killNow(all []target) {
	for _, t := range all {
		_ = t.kill(syscall.SIGKILL)
	}
}

// ExitOnSignal makes SIGTERM, SIGINT and SIGHUP end every registered child, run before (when not
// nil, bounded by BeforeLimit), and exit with the conventional 128+signal status. A second signal
// kills every child at once and exits. The returned func stops the handler.
func ExitOnSignal(before func()) (stop func()) {
	ch := make(chan os.Signal, 2)
	done := make(chan struct{})
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		var sig os.Signal
		select {
		case sig = <-ch:
		case <-done:
			return
		}
		code := 128 + int(syscall.SIGTERM)
		if s, ok := sig.(syscall.Signal); ok {
			code = 128 + int(s)
		}
		snapshot := closeAndSnapshot()
		finished := make(chan struct{})
		go func() {
			terminate(snapshot, Grace)
			if before != nil {
				boundedRun(before, BeforeLimit)
			}
			close(finished)
		}()
		select {
		case <-finished:
		case <-ch: // impatient: no grace, no cleanup
			killNow(snapshot)
			KillAll(0)
		}
		os.Exit(code)
	}()
	return func() { signal.Stop(ch); close(done) }
}

func boundedRun(f func(), limit time.Duration) {
	ran := make(chan struct{})
	go func() { f(); close(ran) }()
	select {
	case <-ran:
	case <-time.After(limit):
	}
}
