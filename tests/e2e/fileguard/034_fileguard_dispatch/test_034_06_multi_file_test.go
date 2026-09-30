package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This file covers a GATE asked about a call that changes SEVERAL files (issue
// #87). One tool call — `rm a b`, two sr-file calls joined by && — runs whole or
// not at all, so the gate must be asked about every file the call would change
// before it runs, not only the first it selects: a gate that passes the first file
// and is never asked about the second admits the second's not-fine change, and only
// the Stop after-check sees it, once the file is already gone. The one deny names
// every file that was refused.

// keepGate is a gate over src/ bound to deletes as well as writes, so it can
// refuse an `rm` before it runs.
const keepGate = `on:
  - event: PreFileWrite
    match: event.path startsWith "src/"
  - event: PreFileDelete
    match: event.path startsWith "src/"
checks:
  - script: ./check.sh
`

// refuseKeepDeletes refuses the delete of a file whose name says keep, and
// passes everything else — so which file of a multi-file `rm` fails is decided
// by the file, not by its position in the command.
const refuseKeepDeletes = `#!/bin/sh
payload="$(cat)"
kind="$(printf '%s' "$payload" | jq -r '.event.kind')"
path="$(printf '%s' "$payload" | jq -r '.event.path')"
case "$kind:$path" in
  *FileDelete:*keep*)
    echo '{"reason":"KEEP-REFUSED: a file named keep is not deleted"}'
    exit 1 ;;
esac
exit 0
`

// keepProject is a repository whose src/ holds the given files, committed with
// the gate before the session, so an `rm` of them is a delete of tracked files.
func keepProject(t *testing.T, files ...string) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	for _, f := range files {
		e.WriteFile(proj, f, "package src\n")
	}
	e.Gate(proj, "keep-files", keepGate, map[string]string{"check.sh": refuseKeepDeletes})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")
	return e, proj
}

// denyText is the reason of every pre-tool deny in a run — what the agent was
// told — without the rest of the stream, which also holds the agent's own
// command and so names every file whether or not a deny did. Built on
// harness.Result.Refusals, which reads the tool_result records rather than
// scanning the stream for a fixed marker string: real Claude Code (and the
// mock, since harness-mocks#4/v0.2.0) answers a PreToolUse refusal with
// "PreToolUse:<Tool> hook error: <reason>", not the older, fictitious "Tool
// call blocked by a PreToolUse hook" text a prior mock version emitted.
func denyText(res harness.Result) string {
	return strings.Join(res.Refusals(), "\n")
}

// T034_12: `rm src/a.go src/keep.go` — the FIRST file passes the gate, the
// SECOND does not. The call is refused before it runs: both files are still on
// disk, and the deny names src/keep.go (and not src/a.go, which was fine).
//
// On origin/main the gate was asked about src/a.go only, passed it, and broke
// out of its loop — the rm ran and both files were gone before any check saw
// src/keep.go.
func TestT034_12_SecondFileOfMultiFileCallIsChecked(t *testing.T) {
	e, proj := keepProject(t, "src/a.go", "src/keep.go")

	res := e.Run(proj, "s-034-12", "clean up src", Turns("done",
		Bash("b1", "rm src/a.go src/keep.go"),
	))

	if !res.Refused() {
		t.Fatalf("an rm whose second file the gate refuses was not refused before it ran:\n%s", res.Output)
	}
	if !e.Exists(proj, "src/keep.go") || !e.Exists(proj, "src/a.go") {
		t.Errorf("the refused rm ran anyway: src/a.go exists=%v, src/keep.go exists=%v",
			e.Exists(proj, "src/a.go"), e.Exists(proj, "src/keep.go"))
	}
	deny := denyText(res)
	if !strings.Contains(deny, "KEEP-REFUSED") || !strings.Contains(deny, "src/keep.go") {
		t.Errorf("the deny does not give the gate's reason for src/keep.go:\n%s", deny)
	}
	if strings.Contains(deny, "src/a.go") {
		t.Errorf("the deny names src/a.go, which the gate passed:\n%s", deny)
	}
}

// T034_13: `rm src/keep1.go src/a.go src/keep2.go` — the first and the LAST
// file fail. The deny names both, so the agent hears every file it must not
// delete in one answer, not one per retry.
func TestT034_13_EveryRefusedFileIsNamed(t *testing.T) {
	e, proj := keepProject(t, "src/keep1.go", "src/a.go", "src/keep2.go")

	res := e.Run(proj, "s-034-13", "clean up src", Turns("done",
		Bash("b1", "rm src/keep1.go src/a.go src/keep2.go"),
	))

	if !res.Refused() {
		t.Fatalf("an rm of two files the gate refuses was not refused:\n%s", res.Output)
	}
	for _, f := range []string{"src/keep1.go", "src/a.go", "src/keep2.go"} {
		if !e.Exists(proj, f) {
			t.Errorf("the refused rm ran anyway: %s is gone", f)
		}
	}
	deny := denyText(res)
	for _, want := range []string{"src/keep1.go", "src/keep2.go", "KEEP-REFUSED"} {
		if !strings.Contains(deny, want) {
			t.Errorf("the deny is missing %q:\n%s", want, deny)
		}
	}
	if strings.Contains(deny, "src/a.go") {
		t.Errorf("the deny names src/a.go, which the gate passed:\n%s", deny)
	}
}

// T034_14: the control — `rm src/a.go src/b.go`, where the gate passes every
// file. Checking every file does not refuse a call whose files are all fine:
// the rm runs and both files are gone.
func TestT034_14_MultiFileCallOfFineFilesRuns(t *testing.T) {
	e, proj := keepProject(t, "src/a.go", "src/b.go")

	res := e.Run(proj, "s-034-14", "clean up src", Turns("done",
		Bash("b1", "rm src/a.go src/b.go"),
	))

	if res.Refused() {
		t.Fatalf("an rm of files the gate passes was refused:\n%s", res.Output)
	}
	if e.Exists(proj, "src/a.go") || e.Exists(proj, "src/b.go") {
		t.Errorf("the admitted rm did not run: src/a.go exists=%v, src/b.go exists=%v",
			e.Exists(proj, "src/a.go"), e.Exists(proj, "src/b.go"))
	}
}

// T034_15: the same hole on WRITES — two sr-file writes joined by &&, each a
// write whose result the engine computes before it runs. The first is fine, the
// second holds a plaintext key. The call is refused and NEITHER file lands; the
// deny names secrets/b.env.
//
// On origin/main the gate passed secrets/a.env, never looked at secrets/b.env,
// and the key was written to disk.
func TestT034_15_SecondWriteOfMultiFileCallIsChecked(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "no-plaintext-keys", preventGate, map[string]string{"check.sh": checkNoPlaintextKey})
	commitGuards(t, proj)

	res := e.Run(proj, "s-034-15", "write two secrets", Turns("done",
		Bash("b1", `sr-file write secrets/a.env --content 'KEY_REF=vault://a' && sr-file write secrets/b.env --content 'KEY=hunter2'`),
	))

	if !res.Refused() {
		t.Fatalf("a call whose second write holds a plaintext key was not refused before it ran:\n%s", res.Output)
	}
	if e.Exists(proj, "secrets/b.env") {
		t.Errorf("the plaintext key LANDED: the gate was not asked about the call's second file")
	}
	if e.Exists(proj, "secrets/a.env") {
		t.Errorf("part of the refused call ran: secrets/a.env landed")
	}
	deny := denyText(res)
	if !strings.Contains(deny, "plaintext KEY") || !strings.Contains(deny, "secrets/b.env") {
		t.Errorf("the deny does not give the gate's reason for secrets/b.env:\n%s", deny)
	}
}
