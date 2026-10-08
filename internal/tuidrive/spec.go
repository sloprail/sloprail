package tuidrive

import (
	"fmt"
	"regexp"
	"time"

	"github.com/sloprail/sloprail/internal/harness"
)

// Spec is what a harness says of its interactive screen (internal/harness.TUI): when the
// input is live and how the session is quit.
type Spec = harness.TUI

// QuitStep is one step of quitting: keys pressed, then a screen waited for.
type QuitStep = harness.TUIQuit

// keyBytes are what a terminal sends for each named key.
var keyBytes = map[string]string{
	"enter": "\r", "esc": "\x1b", "tab": "\t", "backspace": "\x7f", "space": " ",
	"ctrl-c": "\x03", "ctrl-d": "\x04", "ctrl-j": "\n", "ctrl-a": "\x01", "ctrl-e": "\x05", "ctrl-l": "\x0c", "ctrl-o": "\x0f", "ctrl-r": "\x12", "ctrl-u": "\x15", "shift-tab": "\x1b[Z",
	"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
}

// check compiles the regexps and refuses a key it has no bytes for.
func checkSpec(s Spec) (ready *regexp.Regexp, expects []*regexp.Regexp, err error) {
	if s.Ready == "" {
		return nil, nil, fmt.Errorf("tuidrive: the spec names no ready screen: a prompt typed before the input is live is lost")
	}
	if ready, err = regexp.Compile(s.Ready); err != nil {
		return nil, nil, fmt.Errorf("tuidrive: ready screen: %w", err)
	}
	for _, q := range s.Quit {
		for _, k := range q.Keys {
			if _, ok := keyBytes[k]; !ok {
				return nil, nil, fmt.Errorf("tuidrive: quit key %q is not one of the keys the driver can press", k)
			}
		}
		var re *regexp.Regexp
		if q.Expect != "" {
			if re, err = regexp.Compile(q.Expect); err != nil {
				return nil, nil, fmt.Errorf("tuidrive: quit screen: %w", err)
			}
		}
		expects = append(expects, re)
	}
	if len(s.Quit) == 0 {
		return nil, nil, fmt.Errorf("tuidrive: the spec names no way to quit the session")
	}
	return ready, expects, nil
}

// Options is one session.
type Options struct {
	Spec Spec

	// Progress fingerprints the session record the harness writes: it must change whenever
	// the record grows (a prompt accepted, an answer, a follow-up). Nil means the record is
	// not watched, which leaves a turn with only the screen and the processes to go on.
	Progress func() string

	// Answered reports whether the session record ends with the agent's own answer (not a
	// prompt, not a tool call still to be answered): the turn cannot have ended before it
	// does, however quiet the screen. Nil means no such check.
	Answered func() bool

	// Stuck is how long the record and the screen may be quiet, with no new process running,
	// while the record does not end with an answer, before the turn is given up as hung
	// (default 10m).
	Stuck time.Duration

	// Settle is how long the record and the screen must both be quiet, with no new process
	// running, for a turn to have ended (default 10s).
	Settle time.Duration

	// ProcSettle is the same quiet while the harness has a process running that it did not
	// have when the prompt was typed: a stop hook running tests, a judge. Default 3m.
	ProcSettle time.Duration

	// Poll is how often the quiet is looked at (default 250ms).
	Poll time.Duration

	// ReadyTimeout bounds the wait for the input to be live (default 90s), SubmitTimeout the
	// wait for an accepted prompt to show in the record (default 60s), TurnTimeout a whole
	// turn (default 30m) and ExitTimeout the wait for the harness to exit after the quit
	// keys (default 60s: its session-end hooks run first).
	ReadyTimeout, SubmitTimeout, TurnTimeout, ExitTimeout time.Duration
}

func (o *Options) defaults() {
	set := func(d *time.Duration, v time.Duration) {
		if *d == 0 {
			*d = v
		}
	}
	set(&o.Settle, 10*time.Second)
	set(&o.ProcSettle, 3*time.Minute)
	set(&o.Stuck, 10*time.Minute)
	set(&o.Poll, 250*time.Millisecond)
	set(&o.ReadyTimeout, 90*time.Second)
	set(&o.SubmitTimeout, time.Minute)
	set(&o.TurnTimeout, 30*time.Minute)
	set(&o.ExitTimeout, time.Minute)
	if o.Spec.Rows == 0 || o.Spec.Cols == 0 {
		o.Spec.Rows, o.Spec.Cols = 50, 160
	}
}
