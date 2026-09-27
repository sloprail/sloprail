package main

import (
	"errors"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

// payloadGrace is how long a trajectory read waits for a payload to show up on
// standard input before deciding there is none. A hook payload, or a script's
// `printf '%s' "$payload" | sr-session trajectory …`, is written as the process
// starts, and poll returns the moment it arrives — so this bound is only ever
// paid in full by a caller that has nothing to pipe at all.
const payloadGrace = 2 * time.Second

// readPayloadIfWaiting is readPayload for the trajectory subcommands (cite,
// describe, normalize, tool-result), which are run by an AGENT as often as by a
// hook: `sr-session trajectory cite '<quote>' && git commit …` is exactly what
// a gate requiring a citation tells an agent to run.
//
// readPayload reads standard input to its end, and an agent's shell does not
// always close it. Measured: `claude -p`'s Bash tool hands a command an open,
// empty pipe, so an unconditional read waits for an end that never comes —
// every `cite` an unattended agent ran hung until its tool call timed out, and
// the agent could not follow the very remedy the refusal named. A payload is
// something already written (or a writer that already closed); an open pipe
// with nothing on it is not one, and the command then resolves the session
// from the environment exactly as it does for an empty stdin.
//
// Hook entry points (pre-tool, stop, …) keep readPayload: a harness always
// writes their payload, and waiting on it is right.
func readPayloadIfWaiting(cmd *cobra.Command) HookPayload {
	if f, ok := cmd.InOrStdin().(*os.File); ok && !stdinWaiting(f, payloadGrace) {
		return HookPayload{}
	}
	return readPayload(cmd)
}

// stdinWaiting reports whether reading f would return without blocking
// indefinitely: data is there, the writer has closed, or f is not something
// poll can wait on (a regular file, /dev/null on darwin — both read to an end
// by themselves). Only a descriptor that stays silent for the whole grace —
// an open pipe, socket or terminal nobody writes to — reports false.
func stdinWaiting(f *os.File, grace time.Duration) bool {
	fds := []unix.PollFd{{Fd: int32(f.Fd()), Events: unix.POLLIN}}
	deadline := time.Now().Add(grace)
	for {
		ms := int(time.Until(deadline) / time.Millisecond)
		if ms < 0 {
			ms = 0
		}
		n, err := unix.Poll(fds, ms)
		if errors.Is(err, unix.EINTR) && time.Now().Before(deadline) {
			continue
		}
		if err != nil {
			// A descriptor poll cannot judge: fall back to the old behaviour
			// and read it.
			return true
		}
		return n > 0
	}
}
