package e2e

import (
	"fmt"
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
//
// The origin is a COMMIT, so the project these cases run in is a real git
// repository with the source really committed in it, and the sha the markers
// carry is the one `git rev-parse HEAD` returned. Faking it would leave the
// half of the rule that reads git untested, which is the half the origin
// exists for — see TestDR_07, which edits the working tree after the commit
// and proves the check still reads the pinned bytes.

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
// source file the moves are taken from, and returns the project directory and
// the commit the source was committed as.
//
// The project is a real git repository because the rule's origin is a commit:
// the hook reads `git show <sha>:<path>`, so a project with no history would
// make every case here fail on the "not a git repository" refusal and prove
// nothing about the comparison. The sha is returned rather than assumed, since
// it is what the markers below must name.
func install(t *testing.T, e *harness.Env) (string, string) {
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
	return proj, commitAll(t, proj)
}

// git runs one git command in dir and returns its trimmed stdout, failing the
// test on error with git's own message — a silent failure here would surface
// later as a confusing refusal about an unreachable commit.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// A deterministic identity and no user config: the machine running this
	// may have neither, and a commit that fails for want of a user.email would
	// look like the rule refusing.
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commitAll initialises a repository in the project and commits everything in
// it, returning the resulting sha.
func commitAll(t *testing.T, proj string) string {
	t.Helper()
	git(t, proj, "init", "-q", ".")
	git(t, proj, "add", "-A")
	git(t, proj, "commit", "-q", "-m", "source")
	return git(t, proj, "rev-parse", "HEAD")
}

// movedFrom is the origin marker naming the Beta range at the given commit —
// the written form the rule reads, built here from the sha the test really
// produced.
func movedFrom(sha string) string {
	return fmt.Sprintf("// sr:moved-from src/big.go@%s:%d-%d\n", sha, betaStart, betaEnd)
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
	proj, sha := install(t, e)

	got := e.Run(proj, "s-dr-01", "move Beta into its own file", Turns("done",
		Write("w1", "src/beta.go", movedFrom(sha)+betaText),
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
// The marker declares the same origin as the permitted case, and the function
// signature is identical — only the body differs, by one character. That is the
// shape of the failure this rule exists for: a diff that reads as a move and a
// behaviour that changed.
func TestDR_02_SilentlyRegeneratedMoveIsRefused(t *testing.T) {
	e := New(t)
	proj, sha := install(t, e)

	regenerated := "func Beta(n int) string {\n\treturn fmt.Sprintf(\"beta:%d\", n)\n}\n"

	got := e.Run(proj, "s-dr-02", "move Beta into its own file", Turns("done",
		Write("w1", "src/beta.go", movedFrom(sha)+regenerated),
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
	// And it names the commit it read, so the agent can run the same command.
	if !got.Saw(sha) {
		t.Errorf("the refusal never names the commit it compared against:\n%s", got.Output)
	}
}

// TestDR_03: a declared rename is a permitted derivation.
//
// The bytes are NOT identical to the source here — this is the derived case,
// and it must pass, or the rule would force every move to be a bare copy and
// people would stop declaring their moves at all.
func TestDR_03_DeclaredRenameIsPermitted(t *testing.T) {
	e := New(t)
	proj, sha := install(t, e)

	renamed := "func Gamma(n int) string {\n\treturn fmt.Sprintf(\"beta-%d\", n)\n}\n"

	got := e.Run(proj, "s-dr-03", "move Beta out as Gamma", Turns("done",
		Write("w1", "src/gamma.go",
			movedFrom(sha)+"// sr:moved-rename Beta=Gamma\n"+renamed),
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
// unchecked. Same origin as that case, same rename, and one further edit to the
// body — which must still be caught.
func TestDR_04_RenameDoesNotLicenseOtherChanges(t *testing.T) {
	e := New(t)
	proj, sha := install(t, e)

	altered := "func Gamma(n int) string {\n\treturn fmt.Sprintf(\"gamma-%d\", n)\n}\n"

	got := e.Run(proj, "s-dr-04", "move Beta out as Gamma", Turns("done",
		Write("w1", "src/gamma.go",
			movedFrom(sha)+"// sr:moved-rename Beta=Gamma\n"+altered),
	))

	if !got.Saw("is not the code at") {
		t.Fatalf("a declared rename let an unrelated body change through:\n%s", got.Output)
	}
	if landed(t, proj, "src/gamma.go") {
		t.Fatalf("the write was refused but landed anyway")
	}
}

// TestDR_05: a file carrying no origin marker is not examined.
//
// The documented limit, tested so the documentation cannot quietly stop being
// true. An ordinary write must be unaffected by this rule — a guardrail that
// began refusing every new file would be a different rule than the one the
// prose describes.
//
// This is also what proves the matcher selects on the MARKER rather than on the
// text: the note below contains the words "sr:moved-from" in prose, which the
// old `content contains "sloprail:moved-from"` matcher would have fired on. A
// marker is a whole line with a comment leader, so a mention of one inside a
// sentence is not one.
func TestDR_05_OrdinaryWriteIsUntouched(t *testing.T) {
	e := New(t)
	proj, _ := install(t, e)

	got := e.Run(proj, "s-dr-05", "write a note", Turns("done",
		Write("w1", "src/notes.md", "a note about how sr:moved-from works, no moved code here\n"),
	))

	if got.Saw("deterministic-refactoring") {
		t.Fatalf("a write carrying no origin marker was judged by the move rule:\n%s", got.Output)
	}
	if !landed(t, proj, "src/notes.md") {
		t.Fatalf("an ordinary write did not land:\n%s", got.Output)
	}
}

// TestDR_06: a marker naming a source absent from the pinned commit is refused.
//
// Refusing what it cannot verify is the rule's stated posture, and the case an
// agent reaches by inventing a plausible origin for code it wrote fresh.
func TestDR_06_UnverifiableOriginIsRefused(t *testing.T) {
	e := New(t)
	proj, sha := install(t, e)

	marker := fmt.Sprintf("// sr:moved-from src/nowhere.go@%s:1-3\n", sha)

	got := e.Run(proj, "s-dr-06", "move something", Turns("done",
		Write("w1", "src/ghost.go", marker+betaText),
	))

	if !got.Saw("that commit has no such file") {
		t.Fatalf("a marker naming a nonexistent source was not refused:\n%s", got.Output)
	}
	if landed(t, proj, "src/ghost.go") {
		t.Fatalf("the write was refused but landed anyway")
	}
}

// TestDR_07: the pinned commit is what is compared against, not the worktree.
//
// This is the case the sha exists for, and the one nothing else here covers. The
// source is edited AFTER the copy was taken — which is what happens when an
// earlier step of the same refactor already touched it, or when someone else's
// change landed in between. The bytes being written are still exactly the bytes
// at the declared commit, so the move is genuine and must be permitted.
//
// A check reading the working tree would refuse this. That is the "compares
// against the wrong bytes and nothing says so" failure the commit removes: it
// fails a correct move, and the agent's only way out is to weaken its own
// declaration.
func TestDR_07_DriftedWorktreeDoesNotBreakAPinnedMove(t *testing.T) {
	e := New(t)
	proj, sha := install(t, e)

	// The source moves on after the commit the marker names.
	drifted := strings.Replace(source, "beta-%d", "beta-CHANGED-%d", 1)
	if drifted == source {
		t.Fatal("the drift edit changed nothing, so this case would prove nothing")
	}
	if err := os.WriteFile(filepath.Join(proj, "src", "big.go"), []byte(drifted), 0o644); err != nil {
		t.Fatalf("drift the source: %v", err)
	}

	got := e.Run(proj, "s-dr-07", "move Beta into its own file", Turns("done",
		Write("w1", "src/beta.go", movedFrom(sha)+betaText),
	))

	if got.Saw("is not the code at") {
		t.Fatalf("a genuine move was refused because the worktree had moved on — "+
			"the check is reading the working tree rather than the pinned commit:\n%s", got.Output)
	}
	if !landed(t, proj, "src/beta.go") {
		t.Fatalf("a move pinned to a commit was permitted but never reached disk:\n%s", got.Output)
	}
}

// TestDR_08: an origin with no commit is refused.
//
// The bare `path:start-end` form the rule used before the sha. It is refused
// rather than accepted-with-a-default, because a default would mean "whatever
// that file says now" — precisely the unpinned comparison the commit replaced.
// The bytes here are correct, so nothing but the missing pin is being tested.
func TestDR_08_OriginWithoutACommitIsRefused(t *testing.T) {
	e := New(t)
	proj, _ := install(t, e)

	got := e.Run(proj, "s-dr-08", "move Beta into its own file", Turns("done",
		Write("w1", "src/beta.go",
			fmt.Sprintf("// sr:moved-from src/big.go:%d-%d\n", betaStart, betaEnd)+betaText),
	))

	if !got.Saw("must pin the commit") {
		t.Fatalf("an origin naming no commit was not refused:\n%s", got.Output)
	}
	if landed(t, proj, "src/beta.go") {
		t.Fatalf("the write was refused but landed anyway")
	}
}

// TestDR_09: an origin naming a commit this repository does not have is refused.
//
// The other half of TestDR_08. A sha is only a pin if a sha that cannot be read
// stops the write — an unreachable commit falling back to the working tree
// would take that fallback exactly when the pin mattered.
func TestDR_09_UnreachableCommitIsRefused(t *testing.T) {
	e := New(t)
	proj, _ := install(t, e)

	const ghost = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	got := e.Run(proj, "s-dr-09", "move Beta into its own file", Turns("done",
		Write("w1", "src/beta.go",
			fmt.Sprintf("// sr:moved-from src/big.go@%s:%d-%d\n", ghost, betaStart, betaEnd)+betaText),
	))

	if !got.Saw("has no such commit") {
		t.Fatalf("an origin naming an unreachable commit was not refused:\n%s", got.Output)
	}
	if landed(t, proj, "src/beta.go") {
		t.Fatalf("the write was refused but landed anyway")
	}
}
