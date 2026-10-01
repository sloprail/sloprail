package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sloprail/sloprail/internal/sessionstate"
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
	e.WriteFile(proj, "docs/b.md", "FORBIDDEN words")
	e.CommitAll(proj, "add b")

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
	if !strings.Contains(got, "could not be read") || !strings.Contains(got, "not judged") {
		t.Fatalf("an unreadable registry was not refused, naming the error; refusals: %q", got)
	}
}

// (c) a session whose store has a baseline but never kept the commit it began at is not
// wedged: the start is derived from the HEAD reflog at the record's first timestamp, and
// the violation after it is judged and refused for what it is.
func TestT003_60_ALostSessionStartIsDerivedFromTheReflog(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	e.Run(proj, "s-003-60c", "clean", Turns("done", harness.CommitFile("c1", "docs/a.md", "clean words", "add a")))
	if e.Meta(proj, "s-003-60c", sessionstate.MetaSessionStart) == "" || e.Meta(proj, "s-003-60c", sessionstate.MetaBaselineCommit) == "" {
		t.Fatal("the session kept no start or baseline to lose")
	}
	e.DeleteMeta(proj, "s-003-60c", sessionstate.MetaSessionStart)
	// Commits are timed to the second: the violation must fall after the session began.
	time.Sleep(1100 * time.Millisecond)

	e.Run(proj, "s-003-60c", "violate", Turns("done", harness.CommitFile("c2", "docs/b.md", "FORBIDDEN words", "add b")))
	got := stopRefusals(e, proj, "s-003-60c")
	if strings.Contains(got, "did not keep the commit") {
		t.Fatalf("the session is wedged on its lost start instead of deriving it:\n%s", got)
	}
	if !strings.Contains(got, refusalText) {
		t.Fatalf("the violation after the derived start was not judged; refusals: %q", got)
	}
	if e.Meta(proj, "s-003-60c", sessionstate.MetaSessionStart) == "" {
		t.Fatal("the derived start was not kept")
	}
}

// (c2) when the start cannot be derived (no record of when the session began), the refusal
// names a recovery that works: running its command lets the next Stop judge.
func TestT003_60_AnUnderivableStartRefusesWithARecoveryThatWorks(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	e.Run(proj, "s-003-60d", "clean", Turns("done", harness.CommitFile("c1", "docs/a.md", "clean words", "add a")))
	e.DeleteMeta(proj, "s-003-60d", sessionstate.MetaSessionStart)
	// A record with no timestamps says nothing about when the session began.
	record := e.TranscriptPath(proj, "s-003-60d")
	body, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil {
			delete(rec, "timestamp")
			b, _ := json.Marshal(rec)
			line = string(b)
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(record, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := e.StopNow(proj, "s-003-60d", false)
	if !harness.Blocked(res) || !strings.Contains(res.Output, "rm -f ") {
		t.Fatalf("an underivable start did not refuse with a recovery command:\n%s", res.Output)
	}
	idx := strings.Index(res.Output, "rm -f ")
	cmdline := res.Output[idx:]
	db := e.StateDBPath(proj, "s-003-60d")
	if !strings.Contains(cmdline, db) {
		t.Fatalf("the recovery does not name the session's state %s:\n%s", db, cmdline)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(db + suffix)
	}
	if res := e.StopNow(proj, "s-003-60d", false); harness.Blocked(res) && strings.Contains(res.Output, "did not keep the commit") {
		t.Fatalf("the recovery did not release the session:\n%s", res.Output)
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

	payload, _ := json.Marshal(map[string]any{
		"session_id": "s-003-60e", "transcript_path": e.TranscriptPath(proj, "s-003-60e"),
		"cwd": wt, "stop_hook_active": false, "hook_event_name": "Stop",
	})
	e.CLIDirectStdinEnv(wt, string(payload), e.SessionEnv(""), "sr-session", "stop")

	if dirs := e.SessionStoreDirs(proj, "s-003-60e"); len(dirs) != 1 {
		t.Fatalf("a hook run from another worktree opened a second store for the same session: %v", dirs)
	}
}
