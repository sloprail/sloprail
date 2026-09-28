package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This file covers a PREVENTIVE file-guard asked about a call that changes
// SEVERAL files (issue #87). One tool call — `rm a b`, two sr-file calls joined
// by && — runs whole or not at all, so the guard must be asked about every file
// the call would change before it runs, not only the first it selects: a guard
// that passes the first file and is never asked about the second admits the
// second's not-fine change, and only the Stop after-check sees it, once the file
// is already gone. The one deny names every file that was refused.

// keepGuard is a preventive guard over src/ that is handed deletes too, so it
// can refuse an `rm` before it runs.
const keepGuard = `match: "src/**"
preventive: true
deletions: include
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
// the guard before the session, so an `rm` of them is a delete of tracked files.
func keepProject(t *testing.T, files ...string) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	for _, f := range files {
		e.WriteFile(proj, f, "package src\n")
	}
	e.FileGuard(proj, "keep-files", keepGuard, map[string]string{"check.sh": refuseKeepDeletes})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")
	return e, proj
}

// denyText is the text of every pre-tool deny in a run's stream — what the agent
// was told — without the rest of the stream, which also holds the agent's own
// command and so names every file whether or not a deny did.
func denyText(res harness.Result) string {
	var out []string
	for _, line := range strings.Split(res.Output, "\n") {
		if i := strings.Index(line, "Tool call blocked by a PreToolUse hook"); i >= 0 {
			out = append(out, line[i:])
		}
	}
	return strings.Join(out, "\n")
}

// T034_12: `rm src/a.go src/keep.go` — the FIRST file passes the guard, the
// SECOND does not. The call is refused before it runs: both files are still on
// disk, and the deny names src/keep.go (and not src/a.go, which was fine).
//
// On origin/main the guard was asked about src/a.go only, passed it, and broke
// out of its loop — the rm ran and both files were gone before any check saw
// src/keep.go.
func TestT034_12_SecondFileOfMultiFileCallIsChecked(t *testing.T) {
	e, proj := keepProject(t, "src/a.go", "src/keep.go")

	res := e.Run(proj, "s-034-12", "clean up src", Turns("done",
		Bash("b1", "rm src/a.go src/keep.go"),
	))

	if !res.Refused() {
		t.Fatalf("an rm whose second file the preventive guard refuses was not refused before it ran:\n%s", res.Output)
	}
	if !e.Exists(proj, "src/keep.go") || !e.Exists(proj, "src/a.go") {
		t.Errorf("the refused rm ran anyway: src/a.go exists=%v, src/keep.go exists=%v",
			e.Exists(proj, "src/a.go"), e.Exists(proj, "src/keep.go"))
	}
	deny := denyText(res)
	if !strings.Contains(deny, "KEEP-REFUSED") || !strings.Contains(deny, "src/keep.go") {
		t.Errorf("the deny does not give the guard's reason for src/keep.go:\n%s", deny)
	}
	if strings.Contains(deny, "src/a.go") {
		t.Errorf("the deny names src/a.go, which the guard passed:\n%s", deny)
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
		t.Fatalf("an rm of two files the preventive guard refuses was not refused:\n%s", res.Output)
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
		t.Errorf("the deny names src/a.go, which the guard passed:\n%s", deny)
	}
}

// T034_14: the control — `rm src/a.go src/b.go`, where the guard passes every
// file. Checking every file does not refuse a call whose files are all fine:
// the rm runs and both files are gone.
func TestT034_14_MultiFileCallOfFineFilesRuns(t *testing.T) {
	e, proj := keepProject(t, "src/a.go", "src/b.go")

	res := e.Run(proj, "s-034-14", "clean up src", Turns("done",
		Bash("b1", "rm src/a.go src/b.go"),
	))

	if res.Refused() {
		t.Fatalf("an rm of files the guard passes was refused:\n%s", res.Output)
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
// On origin/main the guard passed secrets/a.env, never looked at secrets/b.env,
// and the key was written to disk.
func TestT034_15_SecondWriteOfMultiFileCallIsChecked(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "no-plaintext-keys", preventiveGuard, map[string]string{"check.sh": checkNoPlaintextKey})
	commitGuards(t, proj)

	res := e.Run(proj, "s-034-15", "write two secrets", Turns("done",
		Bash("b1", `sr-file write secrets/a.env --content 'KEY_REF=vault://a' && sr-file write secrets/b.env --content 'KEY=hunter2'`),
	))

	if !res.Refused() {
		t.Fatalf("a call whose second write holds a plaintext key was not refused before it ran:\n%s", res.Output)
	}
	if e.Exists(proj, "secrets/b.env") {
		t.Errorf("the plaintext key LANDED: the preventive guard was not asked about the call's second file")
	}
	if e.Exists(proj, "secrets/a.env") {
		t.Errorf("part of the refused call ran: secrets/a.env landed")
	}
	deny := denyText(res)
	if !strings.Contains(deny, "plaintext KEY") || !strings.Contains(deny, "secrets/b.env") {
		t.Errorf("the deny does not give the guard's reason for secrets/b.env:\n%s", deny)
	}
}
