package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// This file drives what depth MEANS: a clone this run made, and its source read.

// T039_04: a research run that cloned a repository and read two of its source
// files ADMITS.
//
// The happy path, and the control that proves the refusals below are
// conditional: one real clone, a Read of one source file and a sed of another.
func TestT039_04_DeepResearchAdmits(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	dst := filepath.Join(scratch(t), "retry-lib")

	sess := "s-039-04"
	res := e.Run(proj, sess, "deep research", Turns("done",
		SayBash("b1", "Cloning to study it. #research", "git clone "+src+" "+dst),
		Read("r1", filepath.Join(dst, "lib", "retry.js")),
		Bash("b2", "sed -n '1,40p' "+filepath.Join(dst, "lib", "backoff.js")),
	))

	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("a deep research run (clone + two source reads) was refused:\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("unexpected refusal:\n%s", res.Output)
	}
	// The gate recorded a pass this cycle, and the context — whose exit reads
	// that pass — deactivated.
	if st := e.GateState(proj, sess, "depth-check"); st != "pass" {
		t.Errorf("the depth gate recorded %q, want pass", st)
	}
	if active, _ := e.ContextState(proj, sess, "research-run"); active {
		t.Errorf("the research context stayed active after the depth gate passed — its exit should deactivate it")
	}
}

// T039_05: a research run that cloned but read only the README and a docs page
// (plus one source file) is REFUSED — reading what a library says about itself
// is not reading how it works, and one file is below the floor.
func TestT039_05_CloneButReadmeOnlyRefused(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	dst := filepath.Join(scratch(t), "retry-lib")

	sess := "s-039-05"
	res := e.Run(proj, sess, "clone but skim", Turns("done",
		SayBash("b1", "Cloning. #research", "git clone "+src+" "+dst),
		Read("r1", filepath.Join(dst, "README.md")),
		Bash("b2", "cat "+filepath.Join(dst, "docs", "guide.md")),
		Read("r2", filepath.Join(dst, "lib", "retry.js")),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a research run that read only the README, docs and one source file was not refused:\n%s", res.Output)
	}
	joined := strings.Join(blocks, "\n")
	for _, want := range []string{
		"This #research run cloned " + dst,
		"but read only one source file in it (" + filepath.Join(dst, "lib", "retry.js") + "), and 2 are needed",
		"a README or docs file does not count",
		readMore(1, dst),
		"reading the same file again does not add one",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal is missing %q:\n%s", want, joined)
		}
	}
}

// T039_06: a research run that cloned NOTHING but read source files of a
// checkout already on disk is REFUSED, and the refusal names those reads as not
// counting.
//
// The leak a real run hit: `ls /tmp` found repositories earlier sessions had
// cloned, and the agent "researched" those. Here the stale checkout is made by
// the test, before the session, so nothing in this run cloned it.
func TestT039_06_ReadsOfUnclonedDirectoryRefused(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	stale := filepath.Join(scratch(t), "backoff")
	staleClone(t, src, stale)

	sess := "s-039-06"
	res := e.Run(proj, sess, "research what is lying around", Turns("done",
		SayBash("b1", "Looking at what is already here. #research", "ls "+filepath.Dir(stale)),
		Read("r1", filepath.Join(stale, "lib", "retry.js")),
		Bash("b2", "cat "+filepath.Join(stale, "lib", "backoff.js")),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("research over a checkout this run did not clone was not refused:\n%s", res.Output)
	}
	joined := strings.Join(blocks, "\n")
	for _, want := range []string{
		noCloneReason,
		"Reads of directories this run did not clone do not count (e.g. " + filepath.Join(stale, "lib"),
		whatToDo,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal is missing %q:\n%s", want, joined)
		}
	}
}

// T039_23: with several clones the refusal counts across them, in grammar that
// fits several — not "inside it".
func TestT039_23_SeveralClonesWording(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	s := scratch(t)
	a, b := filepath.Join(s, "a"), filepath.Join(s, "b")

	sess := "s-039-23"
	res := e.Run(proj, sess, "clone two, read one", Turns("done",
		SayBash("b1", "Cloning. #research", "git clone "+src+" "+a+" && git clone "+src+" "+b),
		Read("r1", filepath.Join(b, "lib", "retry.js")),
	))

	joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	want := "This #research run cloned " + a + ", " + b + " but read only one source file across them (" + filepath.Join(b, "lib", "retry.js") + "), and 2 are needed"
	if !strings.Contains(joined, want) {
		t.Errorf("the refusal is missing %q:\n%s\n%s", want, joined, res.Output)
	}
}
