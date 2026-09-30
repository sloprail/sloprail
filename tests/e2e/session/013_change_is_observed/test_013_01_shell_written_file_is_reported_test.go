package e2e

import (
	"encoding/json"
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

// recordEverything is a NEW-FORMAT file-guard that records every after-the-fact
// file event it is handed, and permits unconditionally — the question here is
// which changes were observed, not what anyone decided about them.
//
// `match: "**/*.md"` selects every markdown file the cycle's difference produces,
// at the repository root or any depth (`**/` compiles to an OPTIONAL leading
// directory), which is the faithful stand-in for the old binding to all three
// after-the-fact kinds: a file-guard fires on whichever Post kind the change
// produced, so create, update and delete all reach the one check. Every path
// this directory writes is `.md`, and the guard's own ledger (`seen`, no `.md`)
// is not matched — so, unlike the old $PWD-under-.sloprail/ ledger, the guard
// cannot re-observe its own bookkeeping.
//
// (Re-vehicled from the old GUARDRAIL.md hooks per tests/e2e/REVEHICLE-PATTERN.md:
// the new declaration store does not read GUARDRAIL.md, so this coverage of the
// shared tree-difference machinery would vanish once the old dispatch is deleted.
// It observes the SAME behavior through the NEW dispatch — the flat CheckPayload
// (`.event.path`, `.event.kind`) via the file-guard's own ledger.)
const recordEverything = `match: "**/*.md"
checks:
  - script: ./record.sh
`

// recordScript appends the whole payload as one line.
//
// The ledger is $SR_GUARDRAIL_DIR/seen — the folder the engine sets for a
// file-guard check (`.sloprail/file-guard/<name>/`), the new-format ledger idiom
// that replaces the old hook's $PWD (which the mock never set a project dir for).
const recordScript = `#!/bin/sh
cat >> "$SR_GUARDRAIL_DIR/seen"
echo >> "$SR_GUARDRAIL_DIR/seen"
exit 0
`

// observed is one file a recorded changeset selected.
type observed struct {
	Status string
	Path   string
}

// observedFiles decodes what a file-guard's check was handed: the Changeset
// payload, whose `files` are what the range's commits changed.
func observedFiles(t *testing.T, lines []string) []observed {
	t.Helper()
	var got []observed
	for _, line := range lines {
		var p struct {
			Changeset struct {
				Files []struct {
					Path   string `json:"path"`
					Status string `json:"status"`
				} `json:"files"`
			} `json:"changeset"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("the check was handed something that is not a changeset payload: %v\n%s", err, line)
		}
		for _, f := range p.Changeset.Files {
			got = append(got, observed{Status: f.Status, Path: f.Path})
		}
	}
	return got
}

// sawPath reports whether any observed event names the given path.
func sawPath(got []observed, path string) bool {
	for _, o := range got {
		if o.Path == path {
			return true
		}
	}
	return false
}

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
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": recordScript})
	// The rule is committed before the session: a rule's range starts at the last
	// commit touching its own folder, so a rule committed together with the work
	// would judge an empty range.
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-013-01", "write through a script", Turns("done",
		Bash("b1", "printf 'made by a script\n' > from-script.md"),
	).ThenCommit("write through a script"))

	got := observedFiles(t, e.FileGuardLedgerLines(proj, "watcher", "seen"))
	if !sawPath(got, "from-script.md") {
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
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": recordScript})
	// The rule is committed before the session: a rule's range starts at the last
	// commit touching its own folder, so a rule committed together with the work
	// would judge an empty range.
	e.CommitAll(proj, "the project before the session")

	// One turn that genuinely writes, so the cycle is not empty and a build
	// dispatching nothing at all cannot pass this by being inert. The assertion
	// below is about the OTHER path.
	e.Run(proj, "s-013-02", "name a file without writing it", Turns("done",
		Bash("b1", "printf 'real\n' > really-written.md"),
		Bash("b2", "echo would have written never-written.md"),
	).ThenCommit("name a file without writing it"))

	got := observedFiles(t, e.FileGuardLedgerLines(proj, "watcher", "seen"))
	// The control: the cycle DID report something, so the absence asserted next
	// is a real absence rather than an engine that dispatched nothing.
	if !sawPath(got, "really-written.md") {
		t.Fatalf("the file that was actually written is missing from %v — "+
			"nothing was observed at all, so the assertion below would pass for the wrong reason", got)
	}
	if sawPath(got, "never-written.md") {
		t.Fatalf("a path a command only mentioned was reported as a change: %v — "+
			"the cycle's difference is what the tree shows, not what an action announced", got)
	}
}
