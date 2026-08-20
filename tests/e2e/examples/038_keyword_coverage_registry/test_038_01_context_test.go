package e2e

import (
	"strings"
	"testing"
)

// This file proves the REAL, working half of keyword-coverage-registry: the
// scanner-declared context recognises a declared scanner from a real
// scanners/<name>/scanner.yaml file, logs its keyword set, activates only for an
// ACTIVE scanner, and narrows to the scanner.yaml path. The gate's enforcement is
// the broken half, pinned in test_038_02.

// activeScanner is a scanner.yaml the context's enter parses: `active:` and
// `keywords:` at column 0, the keyword list as a YAML sequence.
const activeScanner = "active: true\nkeywords:\n  - guardrail\n  - llm\n  - agent\n"

// T038_01: writing an ACTIVE scanner.yaml activates the context and logs the
// scanner's keyword set into its registry.
//
// The "write it down anywhere real" half of the unit: a scanner must be declared
// as a real file before any search, and the context records the declared keyword
// set (keyed scanner:<name>) so a coverage check could later hold searches to it.
func TestT038_01_ContextLogsDeclaredScanner(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-038-01"
	e.Run(proj, sess, "declare a scanner", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
	))

	reg := e.GuardrailState(proj, sess, "scanner-declared", "")
	val, ok := reg["scanner:mine"]
	if !ok {
		t.Fatalf("the context did not log the declared scanner; registry=%v", reg)
	}
	// The logged value is the keyword set as JSON — it must carry every declared
	// keyword.
	for _, kw := range []string{"guardrail", "llm", "agent"} {
		if !strings.Contains(val, kw) {
			t.Errorf("the logged keyword set is missing %q: %s", kw, val)
		}
	}
}

// T038_02: an INACTIVE scanner (active: false) is NOT logged, and the context does
// not treat it as a declared-active scanner.
//
// The declarative "not active" path the unit names: a scanner switched off is not
// a coverage obligation. enter emits {scanner, active:false} and writes NOTHING to
// the registry, so no scanner:<name> entry exists to be required.
func TestT038_02_InactiveScannerNotLogged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-038-02"
	res := e.Run(proj, sess, "declare an inactive scanner", Turns("done",
		Write("w1", "scanners/off/scanner.yaml", "active: false\nkeywords:\n  - foo\n"),
	))

	reg := e.GuardrailState(proj, sess, "scanner-declared", "")
	if _, ok := reg["scanner:off"]; ok {
		t.Errorf("an inactive scanner was logged to the registry: %v", reg)
	}
	// And nothing is refused — an inactive scanner is not a coverage obligation.
	if res.Refused() || len(e.BlockingErrorsFrom(proj, sess, "Stop")) != 0 {
		t.Errorf("an inactive scanner triggered a refusal:\n%s", res.Output)
	}
}

// T038_03: the context does NOT activate for a file outside scanners/<name>/
// scanner.yaml.
//
// The control for the trigger's match (`event.path startsWith "scanners/" and
// event.path endsWith "/scanner.yaml"`). A file at a different path — even one
// named scanner.yaml at the top level, or a non-scanner.yaml under scanners/ —
// must not activate the context.
func TestT038_03_DoesNotFireOutsideScope(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")

	sess := "s-038-03"
	e.Run(proj, sess, "write non-scanner files", Turns("done",
		Write("w1", "scanners/mine/notes.md", "not a scanner.yaml"),
		Write("w2", "scanner.yaml", "active: true\nkeywords:\n  - x\n"),
	))

	if active, _ := e.ContextState(proj, sess, "scanner-declared"); active {
		t.Errorf("the context activated for a path outside scanners/<name>/scanner.yaml")
	}
	if len(e.GuardrailState(proj, sess, "scanner-declared", "")) != 0 {
		t.Errorf("the context logged something for an out-of-scope path")
	}
}
