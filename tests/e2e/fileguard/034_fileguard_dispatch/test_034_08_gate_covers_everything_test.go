package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Nothing a preventive file-guard could do is lost by prevention being a gate.
// These pin the three things the removed `preventive:` path did that a plain gate
// must also do: refuse a DELETE before it runs while reading what would be lost,
// fail closed on a write whose result the engine cannot compute, and be asked
// about every file of a call. (The multi-file `rm` and `sr-file &&` cases are in
// test_034_06; a `require: citation` gate is in grounding/041.)

// deleteAndUnknownGate is a gate bound to writes and deletes under src/. Its one
// check refuses (1) a delete whose lost bytes hold the word KEEP-ME — quoting what
// it read off `oldContent`, and (2) any create or update whose result the engine
// could not compute (`resultKnown` not true), the fail-closed a preventive guard
// gave for free. Everything else passes.
const deleteAndUnknownGate = `on:
  - event: PreFileWrite
    match: event.path startsWith "src/"
  - event: PreFileDelete
    match: event.path startsWith "src/"
checks:
  - script: ./verify.sh
`

const verifyScript = `#!/bin/sh
payload="$(cat)"
kind="$(printf '%s' "$payload" | jq -r '.event.kind')"
path="$(printf '%s' "$payload" | jq -r '.event.path')"
case "$kind" in
  PreFileDelete)
    if [ "$(printf '%s' "$payload" | jq -r '.event.oldContentKnown')" = "true" ] &&
       printf '%s' "$payload" | jq -r '.event.oldContent' | grep -q 'KEEP-ME'; then
      echo "{\"reason\":\"DELETE-REFUSED: $path holds KEEP-ME, which the delete would lose\"}"
      exit 1
    fi ;;
  PreFileCreate|PreFileUpdate)
    if [ "$(printf '%s' "$payload" | jq -r '.event.resultKnown')" != "true" ]; then
      echo "{\"reason\":\"UNKNOWN-REFUSED: the result of this write to $path could not be computed, so it cannot be checked before it lands\"}"
      exit 1
    fi ;;
esac
exit 0
`

func coverProject(t *testing.T, files map[string]string) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	for path, body := range files {
		e.WriteFile(proj, path, body)
	}
	e.Gate(proj, "cover", deleteAndUnknownGate, map[string]string{"verify.sh": verifyScript})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")
	return e, proj
}

// T034_16: a PreFileDelete gate reads the bytes about to be lost and refuses the
// delete before it runs. Refusal first: the file survives, and the deny quotes
// what the gate read. Then the pass: a delete of a file that holds nothing worth
// keeping runs.
func TestT034_16_DeleteGateReadsLostContentAndRefusesBeforeItRuns(t *testing.T) {
	e, proj := coverProject(t, map[string]string{
		"src/precious.go": "package src // KEEP-ME\n",
		"src/scratch.go":  "package src\n",
	})

	res := e.Run(proj, "s-034-16a", "tidy src", Turns("done",
		Bash("b1", "rm src/precious.go"),
	))
	if !res.Refused() {
		t.Fatalf("a delete of a file holding KEEP-ME was not refused before it ran:\n%s", res.Output)
	}
	if !e.Exists(proj, "src/precious.go") {
		t.Errorf("the refused rm ran anyway: src/precious.go is gone")
	}
	if !strings.Contains(denyText(res), "DELETE-REFUSED: src/precious.go") {
		t.Errorf("the deny does not carry what the gate read off oldContent:\n%s", denyText(res))
	}

	res = e.Run(proj, "s-034-16b", "remove scratch", Turns("done",
		Bash("b2", "rm src/scratch.go"),
	))
	if res.Refused() {
		t.Fatalf("a delete of a file the gate passes was refused:\n%s", res.Output)
	}
	if e.Exists(proj, "src/scratch.go") {
		t.Errorf("the admitted rm did not run")
	}
}

// T034_17: a RECURSIVE removal is expanded into one PreFileDelete per file inside
// the directory, so `rm -rf src/pkg` is refused when any file in it holds what the
// gate guards, and the whole directory survives.
func TestT034_17_RecursiveDeleteIsCheckedFileByFile(t *testing.T) {
	e, proj := coverProject(t, map[string]string{
		"src/pkg/a.go": "package pkg\n",
		"src/pkg/b.go": "package pkg // KEEP-ME\n",
	})

	res := e.Run(proj, "s-034-17", "remove the package", Turns("done",
		Bash("b1", "rm -rf src/pkg"),
	))
	if !res.Refused() {
		t.Fatalf("rm -rf of a directory holding a KEEP-ME file was not refused:\n%s", res.Output)
	}
	if !e.Exists(proj, "src/pkg/a.go") || !e.Exists(proj, "src/pkg/b.go") {
		t.Errorf("the refused rm -rf ran anyway: a.go exists=%v, b.go exists=%v",
			e.Exists(proj, "src/pkg/a.go"), e.Exists(proj, "src/pkg/b.go"))
	}
	deny := denyText(res)
	if !strings.Contains(deny, "src/pkg/b.go") || strings.Contains(deny, "src/pkg/a.go") {
		t.Errorf("the deny should name src/pkg/b.go (the file holding KEEP-ME) and not src/pkg/a.go:\n%s", deny)
	}
}

// T034_18: a write whose result the engine cannot compute is visible to the gate
// as resultKnown false, and the gate fails closed on it. `sed -i` over two files
// is two underivable updates: the call is refused, the deny names both, and
// neither file is touched. The control: a Write the engine CAN derive passes the
// same gate and lands.
func TestT034_18_UnderivableWriteFailsClosedAndDerivableOneLands(t *testing.T) {
	e, proj := coverProject(t, map[string]string{
		"src/one.go": "package src // one\n",
		"src/two.go": "package src // two\n",
	})

	res := e.Run(proj, "s-034-18a", "rename in place", Turns("done",
		Bash("b1", `sed -i.bak 's/package src/package lib/' src/one.go src/two.go`),
	))
	if !res.Refused() {
		t.Fatalf("an in-place sed over two files, whose result the engine cannot compute, was not refused:\n%s", res.Output)
	}
	if got := readFile(t, proj, "src/one.go"); !strings.Contains(got, "package src") {
		t.Errorf("the refused sed ran anyway: src/one.go is %q", got)
	}
	deny := denyText(res)
	for _, want := range []string{"UNKNOWN-REFUSED", "src/one.go", "src/two.go"} {
		if !strings.Contains(deny, want) {
			t.Errorf("the deny is missing %q:\n%s", want, deny)
		}
	}

	res = e.Run(proj, "s-034-18b", "rewrite one file directly", Turns("done",
		Write("w1", "src/one.go", "package lib // one\n"),
	))
	if res.Refused() {
		t.Fatalf("a Write the engine can derive was refused:\n%s", res.Output)
	}
	if got := readFile(t, proj, "src/one.go"); !strings.Contains(got, "package lib") {
		t.Errorf("the derivable write did not land: %q", got)
	}
}

func readFile(t *testing.T, proj, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(proj, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}
