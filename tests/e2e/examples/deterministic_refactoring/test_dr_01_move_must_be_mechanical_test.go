package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The deterministic-refactoring example, driven as a user would get it.
//
// The guardrail under test is not written out here. It is READ FROM the example
// directory this repo ships, so a test passing means those files work — a test
// carrying its own copy would keep passing after the shipped example broke,
// which is the failure mode this whole example exists to argue against.
//
// Both directions are proved. A guardrail that refused every write would pass
// any test that only checked refusal, so the permitted cases carry as much
// weight here as the refused one: a genuine byte-for-byte move lands, a move
// with a declared rename lands, and only the write whose bytes do not match its
// declared source is stopped.

// source is the file the moves are taken from. Line numbers matter — the
// provenance headers below name ranges in it — so it is written as one literal
// with its lines counted in the constants beneath it.
const source = `package src

import "fmt"

func Alpha() string {
	return "alpha"
}

func Beta(n int) string {
	return fmt.Sprintf("beta-%d", n)
}
`

// The range of Beta in source, 1-based and inclusive. Asserted in
// TestDR_00 below rather than trusted, because every case here rests on it and
// a miscounted range would make a refusal look like the rule working.
const (
	betaStart = 9
	betaEnd   = 11
)

// betaText is those lines, exactly. The permitted cases write this; the refused
// case writes something else.
const betaText = `func Beta(n int) string {
	return fmt.Sprintf("beta-%d", n)
}
`

// exampleDir locates the shipped example relative to the repo root, so the test
// installs the real files rather than a copy of them.
func exampleDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	root := strings.TrimSpace(string(out))
	dir := filepath.Join(root, "examples", "deterministic-refactoring",
		".sloprail", "guardrails", "deterministic-refactoring")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the shipped example is not where the test expects it: %v", err)
	}
	return dir
}

// install puts the shipped guardrail into a fresh project, along with the
// source file the moves are taken from.
func install(t *testing.T, e *harness.Env) string {
	t.Helper()
	proj := e.Project()

	dir := exampleDir(t)
	decl, err := os.ReadFile(filepath.Join(dir, "GUARDRAIL.md"))
	if err != nil {
		t.Fatalf("read shipped declaration: %v", err)
	}
	script, err := os.ReadFile(filepath.Join(dir, "verify-move.sh"))
	if err != nil {
		t.Fatalf("read shipped hook: %v", err)
	}
	e.Guardrail(proj, "deterministic-refactoring", string(decl),
		map[string]string{"verify-move.sh": string(script)})

	if err := os.MkdirAll(filepath.Join(proj, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proj, "src", "big.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return proj
}

// landed reports whether the write actually reached disk. The refusal travels
// back through the tool result, but "the write was prevented" is a claim about
// the file system, and only looking there can settle it.
func landed(t *testing.T, proj, rel string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(proj, rel))
	return err == nil
}

// TestDR_00: the constants this file's cases rest on describe the source.
//
// Every case below names lines 9-11 of the source and expects betaText there.
// If that were miscounted, the permitted cases would fail and the refused case
// would pass for the wrong reason — the rule would look like it worked while
// testing nothing. This settles it before any of them run.
func TestDR_00_TheDeclaredRangeReallyIsTheFunction(t *testing.T) {
	lines := strings.SplitAfter(source, "\n")
	if betaEnd > len(lines) {
		t.Fatalf("the source has %d lines, but the cases name line %d", len(lines), betaEnd)
	}
	got := strings.Join(lines[betaStart-1:betaEnd], "")
	if got != betaText {
		t.Fatalf("lines %d-%d of the source are not what the cases write:\ngot:\n%s\nwant:\n%s",
			betaStart, betaEnd, got, betaText)
	}
}

// TestDR_01: a genuine mechanical move is permitted and lands.
//
// The over-firing half. A guardrail that refused everything would satisfy the
// refusal case below; this is what makes that case mean something.
func TestDR_01_ByteIdenticalMoveIsPermitted(t *testing.T) {
	e := New(t)
	proj := install(t, e)

	got := e.Run(proj, "s-dr-01", "move Beta into its own file", Turns("done",
		Write("w1", "src/beta.go", "// sloprail:moved-from src/big.go:9-11\n"+betaText),
	))

	if got.Saw("is not the code at") {
		t.Fatalf("a byte-for-byte move was refused:\n%s", got.Output)
	}
	if !landed(t, proj, "src/beta.go") {
		t.Fatalf("a byte-for-byte move was permitted but never reached disk:\n%s", got.Output)
	}
}

// TestDR_02: code silently regenerated under a claim of moving is refused.
//
// The header declares the same origin as the permitted case, and the function
// signature is identical — only the body differs, by one character. That is the
// shape of the failure this rule exists for: a diff that reads as a move and a
// behaviour that changed.
func TestDR_02_SilentlyRegeneratedMoveIsRefused(t *testing.T) {
	e := New(t)
	proj := install(t, e)

	regenerated := "func Beta(n int) string {\n\treturn fmt.Sprintf(\"beta:%d\", n)\n}\n"

	got := e.Run(proj, "s-dr-02", "move Beta into its own file", Turns("done",
		Write("w1", "src/beta.go", "// sloprail:moved-from src/big.go:9-11\n"+regenerated),
	))

	if !got.Saw("is not the code at") {
		t.Fatalf("regenerated code claiming to be a move was not refused:\n%s", got.Output)
	}
	if landed(t, proj, "src/beta.go") {
		t.Fatalf("the write was refused but landed anyway — the refusal did not prevent the work")
	}
	// The refusal must name the rule, or the agent cannot find what it broke.
	if !got.Saw("deterministic-refactoring") {
		t.Errorf("the refusal never names the guardrail:\n%s", got.Output)
	}
	// The agent is shown where the bytes parted company, not merely that they
	// did. A refusal it cannot act on sends it guessing.
	if !got.Saw("beta:%d") {
		t.Errorf("the refusal does not show the offending line, so the agent cannot see what differed:\n%s", got.Output)
	}
}

// TestDR_03: a declared rename is a permitted derivation.
//
// The bytes are NOT identical to the source here — this is the derived case,
// and it must pass, or the rule would force every move to be a bare copy and
// people would stop declaring their moves at all.
func TestDR_03_DeclaredRenameIsPermitted(t *testing.T) {
	e := New(t)
	proj := install(t, e)

	renamed := "func Gamma(n int) string {\n\treturn fmt.Sprintf(\"beta-%d\", n)\n}\n"

	got := e.Run(proj, "s-dr-03", "move Beta out as Gamma", Turns("done",
		Write("w1", "src/gamma.go",
			"// sloprail:moved-from src/big.go:9-11\n// sloprail:rename Beta=Gamma\n"+renamed),
	))

	if got.Saw("is not the code at") {
		t.Fatalf("a move with a declared rename was refused:\n%s", got.Output)
	}
	if !landed(t, proj, "src/gamma.go") {
		t.Fatalf("a declared rename was permitted but never reached disk:\n%s", got.Output)
	}
}

// TestDR_04: a declared rename does not license changing anything else.
//
// The dangerous reading of TestDR_03 is that declaring a rename makes the file
// unchecked. Same header as that case, same rename, and one further edit to the
// body — which must still be caught.
func TestDR_04_RenameDoesNotLicenseOtherChanges(t *testing.T) {
	e := New(t)
	proj := install(t, e)

	altered := "func Gamma(n int) string {\n\treturn fmt.Sprintf(\"gamma-%d\", n)\n}\n"

	got := e.Run(proj, "s-dr-04", "move Beta out as Gamma", Turns("done",
		Write("w1", "src/gamma.go",
			"// sloprail:moved-from src/big.go:9-11\n// sloprail:rename Beta=Gamma\n"+altered),
	))

	if !got.Saw("is not the code at") {
		t.Fatalf("a declared rename let an unrelated body change through:\n%s", got.Output)
	}
	if landed(t, proj, "src/gamma.go") {
		t.Fatalf("the write was refused but landed anyway")
	}
}

// TestDR_05: a file claiming no origin is not examined.
//
// The documented limit, tested so the documentation cannot quietly stop being
// true. An ordinary write must be unaffected by this rule — a guardrail that
// began refusing every new file would be a different rule than the one the
// prose describes.
func TestDR_05_OrdinaryWriteIsUntouched(t *testing.T) {
	e := New(t)
	proj := install(t, e)

	got := e.Run(proj, "s-dr-05", "write a note", Turns("done",
		Write("w1", "src/notes.md", "just a note, no moved code here\n"),
	))

	if got.Saw("deterministic-refactoring") {
		t.Fatalf("a write declaring no origin was judged by the move rule:\n%s", got.Output)
	}
	if !landed(t, proj, "src/notes.md") {
		t.Fatalf("an ordinary write did not land:\n%s", got.Output)
	}
}

// TestDR_06: a header naming a source that does not exist is refused.
//
// Refusing what it cannot verify is the rule's stated posture, and the case an
// agent reaches by inventing a plausible origin for code it wrote fresh.
func TestDR_06_UnverifiableOriginIsRefused(t *testing.T) {
	e := New(t)
	proj := install(t, e)

	got := e.Run(proj, "s-dr-06", "move something", Turns("done",
		Write("w1", "src/ghost.go", "// sloprail:moved-from src/nowhere.go:1-3\n"+betaText),
	))

	if !got.Saw("no such file in the project") {
		t.Fatalf("a header naming a nonexistent source was not refused:\n%s", got.Output)
	}
	if landed(t, proj, "src/ghost.go") {
		t.Fatalf("the write was refused but landed anyway")
	}
}
