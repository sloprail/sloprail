package e2e

import (
	"strings"
	"testing"
)

// An `exit` that is killed on its timeout never answered "done?", exactly as one that cannot
// start never did. Reading either as "stay active, say nothing" lets the turn end with the mode
// still in force and no one told it can never close. So the Stop is refused, naming the context
// and the timeout, and the context stays active (more guarding, never less).

const exitHangs = "#!/bin/sh\ncat >/dev/null\nsleep 30\nexit 0\n"

// T035_28: an exit that sleeps past the timeout refuses the Stop, naming the context and the
// timeout, and the context stays active.
func TestT035_28_TimedOutExitRefusesTheStop(t *testing.T) {
	e := New(t)
	e.SetCheckTimeout("2s")
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "guarded", onPostWrite, map[string]string{"enter.sh": enterActivates, "exit.sh": exitHangs})
	e.CommitAll(proj, "before the session")

	sess := "s-035-28"
	e.Run(proj, sess, "write a note", Turns("done", Write("w1", "notes.md", "hello")))
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("the Stop passed though the context's exit was killed on its timeout")
	}
	wantAll(t, "the Stop refusal", strings.Join(blocks, "\n"), "guarded", "exit.sh", "killed after 2s")
	if active, _ := e.ContextState(proj, sess, "guarded"); !active {
		t.Errorf("the context ended though its exit never answered")
	}
}
