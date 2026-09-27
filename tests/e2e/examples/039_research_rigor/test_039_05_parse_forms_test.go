package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The shapes a clone and a read arrive in. The gate reads the engine's parsed
// invocations — argv plus the directory each program runs in (`.cwd`, threaded
// through the line's own `cd`s) — joined onto the directory the record ran in,
// so each of these places the clone and the reads in the same directory the
// shell did.

// admitted runs a #research scenario and fails the test if the depth gate
// refused it.
func admitted(t *testing.T, e *harness.Env, proj, sess string, turns ...harness.Turn) {
	t.Helper()
	res := e.Run(proj, sess, "research retry", Turns("done", turns...))
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("refused:\n%s\n%s", strings.Join(blocks, "\n"), res.Output)
	}
	if st := e.GateState(proj, sess, "depth-check"); st != "pass" {
		t.Fatalf("the depth gate recorded %q, want pass:\n%s", st, res.Output)
	}
}

// refused runs a #research scenario and returns the depth refusal, failing the
// test if there was none.
func refused(t *testing.T, e *harness.Env, proj, sess string, turns ...harness.Turn) string {
	t.Helper()
	res := e.Run(proj, sess, "research retry", Turns("done", turns...))
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("not refused:\n%s", res.Output)
	}
	return strings.Join(blocks, "\n")
}

// T039_12: `git -C <dir> clone --depth=1 <url> <dest>` lands in <dir>/<dest>, and
// `cd <that> && cat … && sed …` reads relative to it.
func TestT039_12_GitDashCAndCdThenRelativeReads(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	s := scratch(t)
	admitted(t, e, proj, "s-039-12",
		SayBash("b1", "#research", "git -C "+s+" clone --depth=1 file://"+src+" studied"),
		Bash("b2", "cd "+filepath.Join(s, "studied")+" && cat lib/retry.js && sed -n '1,20p' lib/backoff.js"),
	)
}

// T039_13: `cd <dir> && git clone --depth 1 <url>` — no destination, so git names
// it after the repository (retry-lib); a separated `--depth 1` value is not read
// as the repository. Read-tool paths are absolute.
func TestT039_13_CdThenCloneWithoutDestination(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	s := scratch(t)
	admitted(t, e, proj, "s-039-13",
		SayBash("b1", "#research", "cd "+s+" && git clone --depth 1 file://"+src),
		Read("r1", filepath.Join(s, "retry-lib", "lib", "retry.js")),
		Read("r2", filepath.Join(s, "retry-lib", "index.js")),
	)
}

// T039_14: a clone inside the project through relative directories, read back by
// relative paths from the project root: `grep -rn` over a source directory and
// `head -n` of a file (whose `20` is head's value, not a file).
func TestT039_14_RelativeCloneInsideProject(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	admitted(t, e, proj, "s-039-14",
		SayBash("b1", "#research", "mkdir -p vendor && cd vendor && git clone "+src),
		Bash("b2", "grep -rn backoff vendor/retry-lib/lib"),
		Bash("b3", "head -n 20 vendor/retry-lib/index.js"),
	)
}

// T039_15: a clone inside a subshell, `(cd <dir> && git clone <url>)`, lands in
// <dir>; the subshell's cd does not move the reads that follow it.
func TestT039_15_CloneInSubshell(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	s := scratch(t)
	dst := filepath.Join(s, "retry-lib")
	admitted(t, e, proj, "s-039-15",
		SayBash("b1", "#research", "(cd "+s+" && git clone "+src+") && cat "+filepath.Join(dst, "lib", "retry.js")+" "+filepath.Join(dst, "lib", "backoff.js")),
	)
}

// T039_16: the Grep tool over a cloned source directory counts as reading it.
// The mock does not implement Grep, so its result is supplied as a record
// (ToolUseWithResult) — placed last, since that record closes the mock's turn.
func TestT039_16_GrepToolCounts(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	dst := filepath.Join(scratch(t), "retry-lib")
	grepUse, grepResult := harness.ToolUseWithResult("g1", "Grep",
		map[string]string{"pattern": "backoff", "path": filepath.Join(dst, "lib")},
		`"lib/retry.js:1:const backoff = require('./backoff');"`)
	admitted(t, e, proj, "s-039-16",
		SayBash("b1", "#research", "git clone "+src+" "+dst),
		Read("r1", filepath.Join(dst, "index.js")),
		grepUse, grepResult,
	)
}

// T039_17: a clone that FAILED — its destination already held a checkout from
// before this run — does not make that checkout this run's research, even when a
// pipeline hides git's exit status. The reads of it are refused as reads of a
// directory this run did not clone.
func TestT039_17_FailedCloneIntoExistingDirectoryRefused(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	dst := filepath.Join(scratch(t), "retry-lib")
	e.Git(filepath.Dir(dst), "clone", src, dst) // left over from an earlier session

	joined := refused(t, e, proj, "s-039-17",
		SayBash("b1", "#research", "git clone "+src+" "+dst+" 2>&1 | tail -n 3"),
		Read("r1", filepath.Join(dst, "lib", "retry.js")),
		Read("r2", filepath.Join(dst, "lib", "backoff.js")),
	)
	for _, want := range []string{
		noCloneReason,
		"Your git clone into " + dst + " failed because the directory was already there",
		"Reads of directories this run did not clone do not count (e.g. " + filepath.Join(dst, "lib"),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal is missing %q:\n%s", want, joined)
		}
	}
}

// T039_18: a destination built from a variable cannot be placed — the engine
// never guesses at the environment — so the refusal asks for a literal path
// rather than crediting a directory the clone did not land in.
func TestT039_18_DestinationFromVariableRefusedAsUnplaceable(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	s := scratch(t)
	joined := refused(t, e, proj, "s-039-18",
		SayBash("b1", "#research", "D="+s+"; git clone "+src+" \"$D/retry-lib\""),
		Read("r1", filepath.Join(s, "retry-lib", "lib", "retry.js")),
		Read("r2", filepath.Join(s, "retry-lib", "lib", "backoff.js")),
	)
	if !strings.Contains(joined, "clone into a literal path") {
		t.Errorf("the refusal did not ask for a literal clone path:\n%s", joined)
	}
}
