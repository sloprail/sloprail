package e2e

import (
	"github.com/sloprail/sloprail/tests/e2e/session/changesetkit"
	"testing"
)

// change_is_observed: an event whose timing is `after` reports a difference
// observed in the tree, not one derived from what an action said it would do.
//
// The spec's reasoning is the whole design of the Stop hook: "A script writes
// files no parser could have named, and an agent can report work it never did.
// Only looking at the tree settles either."
//
// Both halves of that are testable from outside the binary, and this directory
// tests both, because they fail differently:
//
//   - the SCRIPT half (T013_01) — a file written by a shell redirect appears in
//     no tool call and no parseable command argument. An engine deriving the
//     cycle's changes from what it saw announced cannot name it. An engine that
//     looked at the tree can.
//   - the ANNOUNCEMENT half (T013_02) — a command line that names a file it
//     never writes. An engine deriving from announcements reports it; an engine
//     that looked at the tree does not.
//
// The two are deliberately opposite directions of the same error, and a build
// that got the invariant wrong fails one or the other whichever way it leaned.
// A test of only the first would pass against an engine that reported the union
// of everything announced AND everything on disk.

// recordEverything is a file-guard that records every changeset it is handed, and permits unconditionally — the question here is
// which changes were observed, not what anyone decided about them.
//
// `match: "**/*.md"` selects every markdown file the cycle's difference produces,
// at the repository root or any depth (`**/` compiles to an OPTIONAL leading
// directory), which is the faithful stand-in for the old binding to all three
// after-the-fact kinds: a file-guard fires on every committed change, so create, update and delete all reach the one check. Every path
// this directory writes is `.md`, and the guard's own ledger (`seen`, no `.md`)
// is not matched — so the guard
// cannot re-observe its own bookkeeping.
//
// The recorded payloads (`.changeset.files[]`) are read from the file-guard's own ledger.
const recordEverything = `match: "**/*.md"
checks:
  - script: ./record.sh
`

// T013_01: a file written by a shell redirect is reported.
//
// The file is created by `printf > path` inside a Bash turn. There is no Write
// tool call naming it, and the path is not an argument any command parser would
// resolve as a file it writes — it is a redirection target. So an engine that
// derived the cycle's changes from what the actions announced has no way to
// name this file, and one that compared the tree against the session's start
// names it without difficulty.
//
// This is the invariant's load-bearing case, and the reason the Stop hook
// compares rather than accumulates.
func TestT013_01_AFileWrittenByAShellRedirectIsReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("seen")
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": changesetkit.RecordScript(led.Path())})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-013-01", "write through a script", Turns("done",
		Bash("b1", "printf 'made by a script\n' > from-script.md"),
	).ThenCommit("script output"))

	got := changesetkit.Files(t, led.Lines())
	if !changesetkit.Saw(got, "from-script.md") {
		t.Fatalf("a file created by a shell redirect was not reported: got %v — "+
			"no tool call and no parseable argument names it, so only looking at the tree finds it", got)
	}
}

// T013_02: a file a command merely NAMES, without writing, is not reported.
//
// The other direction, and the one that makes T013_01 mean something. The
// command line mentions a path in a position an announcement-derived engine
// would happily read as "about to be written" — but the command does not create
// it, and the tree at the end of the cycle does not contain it.
//
// Without this case, an engine that reported the union of every path it saw
// mentioned anywhere would pass T013_01 while getting the invariant exactly
// backwards. The spec names this half outright: "an agent can report work it
// never did".
func TestT013_02_AFileOnlyNamedByACommandIsNotReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("seen")
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": changesetkit.RecordScript(led.Path())})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the project before the session")

	// One turn that genuinely writes, so the cycle is not empty and a build
	// dispatching nothing at all cannot pass this by being inert. The assertion
	// below is about the OTHER path.
	e.Run(proj, "s-013-02", "name a file without writing it", Turns("done",
		Bash("b1", "printf 'real\n' > really-written.md"),
		Bash("b2", "echo would have written never-written.md"),
	).ThenCommit("real output"))

	got := changesetkit.Files(t, led.Lines())
	// The control: the cycle DID report something, so the absence asserted next
	// is a real absence rather than an engine that dispatched nothing.
	if !changesetkit.Saw(got, "really-written.md") {
		t.Fatalf("the file that was actually written is missing from %v — "+
			"nothing was observed at all, so the assertion below would pass for the wrong reason", got)
	}
	if changesetkit.Saw(got, "never-written.md") {
		t.Fatalf("a path a command only mentioned was reported as a change: %v — "+
			"the cycle's difference is what the tree shows, not what an action announced", got)
	}
}
