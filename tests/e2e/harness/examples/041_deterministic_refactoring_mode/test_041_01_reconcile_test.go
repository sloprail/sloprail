package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This file drives the deterministic-refactoring reconcile end to end against
// the SHIPPED example (the moved-content-reconciles gate, with a same-named
// file-guard as the Stop after-check): a write of a file carrying an `sr:moved-from origin@sha:
// s-e` marker is admitted only when its body reconciles (byte-identical minus
// imports/whitespace) against the origin range at that commit, and refused
// otherwise — by the gate, at PreToolUse, before the write lands.
//
// The origin is a real committed file; the marker's fqn is `<path>@<sha>:<start>-
// <end>` (path BEFORE the @, then the commit, then the line range). The moved
// file's content is the marker line (stripped before comparison) plus the exact
// origin lines. See the package report for the two behaviors that surprised: the
// context activates on ANY PreToolUse (so the marker, not the #refactor
// declaration, is what actually narrows the guard), and the exit.sh completeness
// check does not fire through this harness.

// reconcileRefusal / originRefusal are the guard's own wordings — the words that
// must reach the agent on each refusal path.
const reconcileRefusal = "does not reconcile against its origin"
const originRefusal = "names a commit or path this checkout does not have"

// completenessRefusal is the Stop GATE's wording (gate/refactor-complete), the
// words that must reach the agent when a declared move never landed.
const completenessRefusal = "Refactor declared but not complete"

// declRefactor is the #refactor declaration prose. The scope names the ACTUAL fqn
// a completed move's `sr:moved-from` marker carries — `<path>@<sha>:<start>-<end>`
// — not a logical nickname. That is the bug-3 design: the declared token IS the
// marker's fqn, so the Stop completeness gate can verify a literal correspondence
// (a file carrying `sr:moved-from <this fqn>` exists) rather than guessing which
// landed marker a nickname meant. Here the declared move is origin.go lines 1-3 at
// the pinned commit — the exact fqn the moved file carries in these scenarios.
func declRefactor(sha string) string {
	return "Refactoring. #refactor scope=origin.go@" + sha + ":1-3"
}

// setupOrigin installs the example and commits an origin file whose lines 1-3 are
// a self-contained function, returning the harness/project and the commit sha the
// marker pins.
func setupOrigin(t *testing.T) (env *scene, sha string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	// origin lines 1-3 are the function itself, so the moved chunk is just it.
	e.WriteFile(proj, "origin.go", "func Beta() int {\n\treturn 1\n}\n")
	e.CommitAll(proj, "install + origin")
	return &scene{e: e, proj: proj}, e.Git(proj, "rev-parse", "HEAD")
}

// scene bundles the harness env and project so each scenario reads the same way.
type scene struct {
	e    *harness.Env
	proj string
}

// T041_01: a move whose content RECONCILES against its origin is ADMITTED, and
// the write lands.
//
// The happy path and the control that proves the refusals below are conditional,
// not a guard that blocks every marked write: the moved body equals origin lines
// 1-3 exactly (the sr: marker line is stripped before comparison), so the guard
// admits.
func TestT041_01_ReconcilingMoveAdmits(t *testing.T) {
	env, sha := setupOrigin(t)
	e, proj := env.e, env.proj

	sess := "s-041-01"
	moved := "// sr:moved-from origin.go@" + sha + ":1-3\nfunc Beta() int {\n\treturn 1\n}\n"
	res := e.Run(proj, sess, "move the function", Turns("done",
		SayWrite("w1", declRefactor(sha), "dest.go", moved),
	).ThenCommit("write the files"))

	if res.Refused() {
		t.Errorf("a reconciling move was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, "dest.go") {
		t.Errorf("the reconciling move did not land")
	}
}

// T041_02: a move whose content does NOT reconcile (the body was regenerated, not
// carried) is REFUSED by the gate, the write never lands, and the guard's own
// reason reaches the agent.
//
// The core violation the unit exists to catch: an agent claiming to MOVE code
// while actually REGENERATING it. Here the moved body returns 999 where the
// origin returns 1 — after dropping imports and whitespace the bytes differ.
func TestT041_02_NonReconcilingMoveRefused(t *testing.T) {
	env, sha := setupOrigin(t)
	e, proj := env.e, env.proj

	sess := "s-041-02"
	movedBad := "// sr:moved-from origin.go@" + sha + ":1-3\nfunc Beta() int {\n\treturn 999\n}\n"
	res := e.Run(proj, sess, "move the function", Turns("done",
		SayWrite("w1", declRefactor(sha), "dest.go", movedBad),
	).ThenCommit("write the files"))

	if !res.Refused() {
		t.Fatalf("a regenerated (non-reconciling) move was NOT refused:\n%s", res.Output)
	}
	if e.Exists(proj, "dest.go") {
		t.Errorf("the non-reconciling write LANDED despite the gate")
	}
	if !res.Saw(reconcileRefusal) {
		t.Errorf("the guard's reconcile refusal reason did not reach the agent:\n%s", res.Output)
	}
	// The refusal names the guard so the agent can attribute it.
	if !res.Saw("moved-content-reconciles") {
		t.Errorf("the refusal did not name the gate:\n%s", res.Output)
	}
}

// T041_03: a move whose origin FQN names a commit/path not in the checkout is
// REFUSED — a move cannot be verified against bytes that are not here.
//
// A distinct violation path from T041_02: here the CONTENT might be anything, but
// the origin the marker points at cannot be fetched (a bogus sha), so the guard
// cannot verify the move at all and refuses.
func TestT041_03_UnfetchableOriginRefused(t *testing.T) {
	env, _ := setupOrigin(t)
	e, proj := env.e, env.proj

	sess := "s-041-03"
	// A sha that does not exist in the repo.
	bogus := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	moved := "// sr:moved-from origin.go@" + bogus + ":1-3\nfunc Beta() int {\n\treturn 1\n}\n"
	// The declaration names the fqn the agent will write (the bogus-sha move), so
	// the declaration↔marker correspondence holds — the move is refused because the
	// origin is unfetchable, so it never lands.
	res := e.Run(proj, sess, "move the function", Turns("done",
		SayWrite("w1", "Refactoring. #refactor scope=origin.go@"+bogus+":1-3", "dest.go", moved),
	).ThenCommit("write the files"))

	if !res.Refused() {
		t.Fatalf("a move against an unfetchable origin was NOT refused:\n%s", res.Output)
	}
	if !res.Saw(originRefusal) {
		t.Errorf("the guard's unfetchable-origin reason did not reach the agent:\n%s", res.Output)
	}
	if e.Exists(proj, "dest.go") {
		t.Errorf("the write LANDED despite the origin being unverifiable")
	}
}

// T041_04: a NORMAL write — a file carrying NO `sr:moved-from` marker — is NOT
// touched by the guard, even with a non-reconciling body.
//
// The control that proves the guard narrows to marked files: the file-guard's match is
// `any(markers, .kind == "moved-from")`. A file without that marker is not
// a declared move and must pass untouched. (The gate's context half is effectively
// always active on a PreToolUse — the enter-decline is a no-op — so the marker is
// what actually narrows the guard; this pins that.)
//
// The declaration here carries `#refactor` with NO `scope=`, so the completeness
// gate has nothing to complete (empty declared_markers → the gate permits): this
// test is about the file-guard's marker-narrowing, not completeness, and a scope
// would drag the Stop gate into it. The completeness gate's own firing is proven
// by T041_07 (blocks) and T041_08 (permits).
func TestT041_04_UnmarkedWriteNotGuarded(t *testing.T) {
	env, _ := setupOrigin(t)
	e, proj := env.e, env.proj

	sess := "s-041-04"
	res := e.Run(proj, sess, "write an ordinary file", Turns("done",
		SayWrite("w1", "Refactoring. #refactor (no scope)", "plain.go", "package x\n\nfunc Y() int { return 42 }\n"),
	).ThenCommit("write the files"))

	if res.Refused() {
		t.Errorf("the guard fired on a file carrying no sr:moved-from marker:\n%s", res.Output)
	}
	if !e.Exists(proj, "plain.go") {
		t.Errorf("an unmarked ordinary write did not land")
	}
}

// T041_05: a not-reconciling write RE-FIRES — it is refused every time it is
// attempted, and a corrected (reconciling) write is admitted.
//
// The re-fire property for a GATE reads slightly differently than for
// an after-check: a gate blocks the write BEFORE it lands, so a bad
// move never reaches disk, and re-attempting the same bad move is refused again
// (the guard is a pure function of the pending content, so it cannot be "used
// up"). A corrected move — the exact origin bytes — is then admitted. Two cycles
// in one session are two Run calls with the same session id.
func TestT041_05_NonReconcilingReFiresUntilFixed(t *testing.T) {
	env, sha := setupOrigin(t)
	e, proj := env.e, env.proj
	sess := "s-041-05"

	movedBad := "// sr:moved-from origin.go@" + sha + ":1-3\nfunc Beta() int {\n\treturn 999\n}\n"

	// Cycle 1: bad move — refused, does not land.
	res1 := e.Run(proj, sess, "move (regenerated)", Turns("done",
		SayWrite("w1", declRefactor(sha), "dest.go", movedBad),
	).ThenCommit("write the files"))
	if !res1.Refused() || e.Exists(proj, "dest.go") {
		t.Fatalf("cycle 1: the bad move was not blocked (refused=%v, exists=%v)", res1.Refused(), e.Exists(proj, "dest.go"))
	}

	// Cycle 2: the SAME bad move again — refused again (re-fire: the guard is not
	// used up by having refused once).
	res2 := e.Run(proj, sess, "move (still regenerated)", Turns("done",
		SayWrite("w2", declRefactor(sha), "dest.go", movedBad),
	).ThenCommit("write the files"))
	if !res2.Refused() {
		t.Fatalf("cycle 2: the still-bad move was NOT refused again — the guard was wrongly used up:\n%s", res2.Output)
	}
	if !res2.Saw(reconcileRefusal) {
		t.Errorf("cycle 2: the re-fired refusal did not carry the guard's reason:\n%s", res2.Output)
	}

	// Cycle 3: FIX it — the exact origin bytes — admitted, and it lands.
	movedGood := "// sr:moved-from origin.go@" + sha + ":1-3\nfunc Beta() int {\n\treturn 1\n}\n"
	res3 := e.Run(proj, sess, "move (correct bytes)", Turns("done",
		SayWrite("w3", declRefactor(sha), "dest.go", movedGood),
	).ThenCommit("write the files"))
	if res3.Refused() {
		t.Errorf("cycle 3: the corrected (reconciling) move was refused:\n%s", res3.Output)
	}
	if !e.Exists(proj, "dest.go") {
		t.Errorf("cycle 3: the corrected move did not land")
	}
}

// T041_10: a marked file rewritten in place by a command whose result the engine
// cannot compute (`sed -i`) is refused by the gate, before the write.
//
// A gate reads the pending bytes, and for sed -i there are none to read
// (resultKnown false, pending markers unknown). The gate still selects the update
// because the file ALREADY carries the moved-from marker, and refuses it: the
// regenerated body (999 for the origin's 1) never lands.
func TestT041_10_UnderivableInPlaceEditIsRefused(t *testing.T) {
	env, sha := setupOrigin(t)
	e, proj := env.e, env.proj

	good := "// sr:moved-from origin.go@" + sha + ":1-3\nfunc Beta() int {\n\treturn 1\n}\n"
	e.WriteFile(proj, "dest.go", good)
	e.CommitAll(proj, "an admitted move")

	res := e.Run(proj, "s-041-10", "tweak the moved function", Turns("done",
		Bash("b1", "sed -i.bak s/return\\ 1/return\\ 999/ dest.go"),
	).ThenCommit("write the files"))

	if !res.Refused() {
		t.Fatalf("a sed -i regeneration of a moved file was not refused before the write:\n%s", res.Output)
	}
	if !res.Saw("cannot be worked out") || !res.Saw("moved-content-reconciles") {
		t.Errorf("the refusal is not the reconcile gate's:\n%s", res.Output)
	}
	if body, _ := readFile(proj, "dest.go"); body != good {
		t.Errorf("the in-place edit landed despite the refusal: %q", body)
	}
}

func readFile(proj, rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(proj, rel))
	return string(b), err
}

// T041_11: the file-guard's reconcile refusal NAMES the file it is about, for every way
// it can refuse (the bytes differ; the origin is not in this checkout; the file's content
// or markers are missing from the changeset), so an agent never has to guess which of its
// files or commits a reason is about. The control, a faithful move, is permitted.
func TestT041_11_TheFileGuardRefusalNamesTheFile(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "origin.go"), []byte("func Beta() int {\n\treturn 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "origin"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	sha, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	fqn := "origin.go@" + strings.TrimSpace(string(sha)) + ":1-3"
	marker := `[{"kind":"moved-from","fqn":"` + fqn + `"}]`
	script := filepath.Join(repoRoot(t), "examples", exampleName, ".sloprail", "file-guard", "moved-content-reconciles", "reconciles-against-origin.sh")

	run := func(file string) (string, int) {
		cmd := exec.Command("bash", script)
		cmd.Dir = repo
		cmd.Stdin = strings.NewReader(`{"event":{"kind":"Changeset"},"changeset":{"files":[` + file + `]}}`)
		out, err := cmd.Output()
		if err == nil {
			return string(out), 0
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode()
		}
		t.Fatalf("run: %v", err)
		return "", -1
	}
	file := func(path, fields string) string {
		return `{"status":"A","path":"` + path + `",` + fields + `}`
	}

	if out, code := run(file("dest.go", `"newContent":"func Beta() int {\n\treturn 1\n}\n","newMarkers":`+marker)); code != 0 {
		t.Fatalf("control: a faithful move was refused (exit %d): %s", code, out)
	}
	for name, c := range map[string]string{
		"the bytes differ":            file("dest.go", `"newContent":"func Beta() int {\n\treturn 999\n}\n","newMarkers":`+marker),
		"the origin is not here":      file("dest.go", `"newContent":"x","newMarkers":[{"kind":"moved-from","fqn":"origin.go@0123456789012345678901234567890123456789:1-3"}]`),
		"the content is missing":      file("dest.go", `"newMarkers":`+marker),
		"the markers are missing":     file("dest.go", `"newContent":"func Beta() int {\n\treturn 1\n}\n"`),
		"a second file is the guilty": file("ok.go", `"newContent":"y","newMarkers":[]`) + "," + file("dest.go", `"newContent":"func Beta() int {\n\treturn 999\n}\n","newMarkers":`+marker),
	} {
		out, code := run(c)
		if code == 0 {
			t.Errorf("%s: the guard permitted it", name)
			continue
		}
		if !strings.Contains(out, "dest.go") {
			t.Errorf("%s: the refusal does not name the file it is about:\n%s", name, out)
		}
		if name == "a second file is the guilty" && strings.Contains(out, "ok.go") {
			t.Errorf("%s: the refusal blames the file that is fine:\n%s", name, out)
		}
	}
}
