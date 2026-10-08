package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_60: Stop never silently skips judging because the session's own bookkeeping is
// unreadable.

// damageState overwrites the session's state database with bytes that are not one, the
// way a corrupt file or a store from nowhere reads: the engine cannot open it.
func damageState(t *testing.T, e *Env, proj, sess string) string {
	t.Helper()
	db := e.StateDBPath(proj, sess)
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(db + suffix)
	}
	if err := os.WriteFile(db, []byte("this is not a sqlite database, it is a damaged state file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return db
}

// (a) the engine's state cannot be opened at Stop (no identity to key it by): the
// file-guards still run and the violation is refused. It used to return before any rule
// ran, with a line on stderr and a pass.
func TestT003_60_AStateThatCannotBeOpenedStillJudgesFileGuards(t *testing.T) {
	e, proj, _ := project(t, docsRule)

	// The judging turn (`sr-checks run`, inside the session: a run without one stores no
	// refusal) judges and stores the verdict. The Stop only reads verdicts, and cannot name the
	// session, so it refuses both for that and for the violation the stored verdict carries.
	e.Run(proj, "s-003-60a", "add b", Turns("done", harness.CommitFile("c1", "docs/b.md", "FORBIDDEN words", "add b")))
	payload, _ := json.Marshal(map[string]any{"cwd": proj, "stop_hook_active": false, "hook_event_name": "Stop"})
	res := e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "stop")
	if !harness.Blocked(res) || !strings.Contains(res.Output, refusalText) {
		t.Fatalf("with its state unopenable, Stop let a violating commit through unjudged:\n%s", res.Output)
	}
}

// (b) the session's registry (its folder and ref rows) cannot be read: Stop refuses,
// naming the error, rather than reading it as "no other branches".
func TestT003_60_AnUnreadableRegistryIsARefusalNotNoOtherTips(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	e.Run(proj, "s-003-60b", "clean", Turns("done", harness.CommitFile("c1", "docs/a.md", "clean words", "add a")))
	damageState(t, e, proj, "s-003-60b")

	e.Run(proj, "s-003-60b", "clean again", Turns("done", harness.CommitFile("c2", "docs/b.md", "more clean words", "add b")))
	got := stopRefusals(e, proj, "s-003-60b")
	if !strings.Contains(got, "could not be read") || !strings.Contains(got, "nothing to judge") {
		t.Fatalf("an unreadable registry was not refused, naming the error; refusals: %q", got)
	}
}

// (d) a root whose hooks run from another worktree (it `cd`'d) keeps ONE store, keyed by
// where its record says it began; it used to get a fresh one per directory, resetting
// its verdicts, baseline and counters.
func TestT003_60_ARootThatMovedToAnotherWorktreeKeepsItsStore(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	e.Run(proj, "s-003-60e", "clean", Turns("done", harness.CommitFile("c1", "docs/a.md", "clean words", "add a")))
	wt := filepath.Join(t.TempDir(), "elsewhere")
	e.Git(proj, "worktree", "add", "-q", "-b", "elsewhere", wt)
	if n := len(e.SessionStoreDirs(proj, "s-003-60e")); n != 1 {
		t.Fatalf("before the move the session has %d stores, want 1", n)
	}

	e.StopFrom(proj, "s-003-60e", wt)

	dirs := e.SessionStoreDirs(proj, "s-003-60e")
	if harness.HasCap(t, harness.CapRecordNamesStartDir) {
		if len(dirs) != 1 {
			t.Fatalf("a hook run from another worktree opened a second store for the same session: %v", dirs)
		}
		return
	}
	// This harness's record names no start directory, so the folder a hook reports is all
	// that says where the session is: the other worktree is a store of its own.
	if len(dirs) != 2 {
		t.Fatalf("a record naming no start directory: want one store per reported folder (2), got %v", dirs)
	}
}
