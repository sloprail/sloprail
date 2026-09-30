package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The file SHAPES a cycle has to classify, driven through a real session.
//
// 023_git_states aims at the seam between the two commands gitrepo.Changed asks
// — the status alphabet, the path encoding, the two questions disagreeing. This
// directory aims at the shapes a FILE itself can take, and at what an agent can
// do to one within a cycle. The two are different failure surfaces: a differ can
// handle every status letter and still mis-handle a file whose only change is
// its mode, or one whose content is empty.
//
// What is here, and why each is not already covered elsewhere:
//
//   - MODE. A file whose bytes are unchanged and whose permission bit is not.
//     git reports M for it, so an engine reading the status alphabet gets the
//     event right — but the FINGERPRINT is content-derived, so the verdict for
//     the new mode is the verdict for the old content. identity_is_content says
//     that is correct, and nothing asserted it.
//   - EMPTY and NO-TRAILING-NEWLINE. Both are ordinary files that break naive
//     readers: an empty file is falsy to a shell test, and a file whose last
//     line has no newline is where line-oriented tooling loses a byte.
//   - CREATE-THEN-MODIFY within one cycle, which must arrive as ONE create of
//     the final bytes rather than as two events or as a create of the first
//     draft. 022 covers create-then-delete and delete-then-recreate; this is the
//     third member of that family and the one that decides WHICH content a rule
//     is judged against.
//   - A NESTED REPOSITORY that is not a worktree of this one — someone's stray
//     clone. 021 covers the linked-worktree case end to end; the exclusion in
//     gitrepo is deliberately about what the thing IS rather than about the
//     relationship, and this is the other half of that claim at the session
//     layer.
//
// Every test asserts a POSITIVE — some path this cycle really changed reached
// the rule — before asserting anything about what did not. A differ that fell
// over on an odd tree reports nothing at all, and "the strange path was not
// reported" is satisfied perfectly by that.

// recordEverything is a file-guard that records the changeset it is handed.
//
// `match: path != ""` — an expression that admits every real path, the file-guard
// "match everything" this directory needs: it drives non-`.md` paths a `**/*.md`
// match would silence (`script.sh` in T026_01, `vendor/clone/…` in T026_05). It is
// an expression rather than the `**` glob because `**` compiles to the regexp
// `.*`, whose `.` does not match a newline — the same blind spot 023 documents —
// and an empty match is rejected at load.
const recordEverything = `match: path != ""
checks:
  - script: ./record.sh
`

const recordScript = `#!/bin/sh
cat >> "$SR_GUARDRAIL_DIR/seen"
echo >> "$SR_GUARDRAIL_DIR/seen"
exit 0
`

// observed is one file a recorded changeset selected.
type observed struct {
	Status     string
	Path       string
	NewContent string
}

// observedFiles decodes what a file-guard's check was handed: the Changeset
// payload's files.
func observedFiles(t *testing.T, lines []string) []observed {
	t.Helper()
	var got []observed
	for _, line := range lines {
		var p struct {
			Changeset struct {
				Files []struct {
					Path       string `json:"path"`
					Status     string `json:"status"`
					NewContent string `json:"newContent"`
				} `json:"files"`
			} `json:"changeset"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("the check was handed something that is not a changeset payload: %v\n%s", err, line)
		}
		for _, f := range p.Changeset.Files {
			got = append(got, observed{Status: f.Status, Path: f.Path, NewContent: f.NewContent})
		}
	}
	return got
}

func statusesFor(got []observed, path string) []string {
	var out []string
	for _, o := range got {
		if o.Path == path {
			out = append(out, o.Status)
		}
	}
	return out
}

func sawPath(got []observed, path string) bool { return len(statusesFor(got, path)) > 0 }

// countPath is how many times a path was put in front of the rule.
func countPath(got []observed, path string) int { return len(statusesFor(got, path)) }

// settle commits what the test seeded AND touches the rule's own folder in that
// same commit. A rule's range starts at the last commit that touched its folder,
// so a file committed after the rule would be part of the rule's first range;
// touching the folder moves the range's start past the seed.
func settle(e *harness.Env, proj, msg string) {
	e.WriteFile(proj, ".sloprail/file-guard/watcher/settled.md", msg+"\n")
	e.CommitAll(proj, msg)
}

// project is a repository with the recording guardrail committed, so the rule's
// own folder is part of the baseline rather than of every difference.
func project(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": recordScript})
	e.CommitAll(proj, "the project before the session")
	return e, proj
}

// runOne drives a single cycle, in which the agent commits its work at the end,
// and returns everything the rule was handed.
func runOne(t *testing.T, e *harness.Env, proj, sess string, s harness.Scenario) []observed {
	t.Helper()
	e.Run(proj, sess, "cycle", s.ThenCommit("the cycle"))
	return observedFiles(t, e.FileGuardLedgerLines(proj, "watcher", "seen"))
}

// cycles drives a sequence of cycles under one session id and returns, for each,
// only the events THAT cycle added to the ledger.
//
// The per-cycle slicing is the whole helper. Reading totals is how a multi-cycle
// test silently stops testing anything: eight sightings after cycle one and
// eight after cycle two is indistinguishable from a second cycle that reported
// nothing, and a "reported again" assertion written against a total passes on
// input where nothing was reported again at all.
func cycles(t *testing.T, e *harness.Env, proj, sess string, scenarios ...harness.Scenario) [][]observed {
	t.Helper()
	var out [][]observed
	seen := 0
	for i, s := range scenarios {
		e.Run(proj, sess, "cycle", s)
		lines := e.FileGuardLedgerLines(proj, "watcher", "seen")
		if len(lines) < seen {
			t.Fatalf("cycle %d: the ledger shrank (%d lines, was %d)", i+1, len(lines), seen)
		}
		out = append(out, observedFiles(t, lines[seen:]))
		seen = len(lines)
	}
	return out
}

// git runs a git command in dir, failing the test if it cannot run at all.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// T026_01: a file whose only change is its MODE is reported as an update.
//
// git records the executable bit, so `chmod +x` on a tracked file is a real
// difference to this repository and reports as M — the same letter an edit gets.
// The event must therefore be an update: the path was at the baseline and is
// still there.
//
// The failure this guards is an engine that classified from the CONTENT rather
// than from git's answer. Comparing bytes before and after finds them identical
// and concludes nothing changed, so the file falls silent — and a rule about
// executable files in the tree never fires on the commit that made one.
//
// The control is a second file whose content really did change, so the positive
// and the mode case are read from the same live ledger.
func TestT026_01_AModeChangeIsReportedAsAnUpdate(t *testing.T) {
	e, proj := project(t)

	e.WriteFile(proj, "script.sh", "#!/bin/sh\necho hello\n")
	e.WriteFile(proj, "other.md", "original\n")
	settle(e, proj, "a file the session will chmod")

	// The premise: git is actually tracking the mode. On a filesystem or a
	// configuration where core.fileMode is off, `chmod` is invisible to git and
	// this test would be asserting about a change that does not exist.
	if mode := strings.Fields(git(t, proj, "ls-files", "-s", "script.sh"))[0]; mode != "100644" {
		t.Fatalf("the file does not start non-executable in the index (%s), so the chmod "+
			"below is not the change this test is about", mode)
	}

	got := runOne(t, e, proj, "s-026-01", Turns("done",
		Bash("b1", "chmod +x script.sh"),
		Write("w1", "other.md", "genuinely edited\n"),
	))

	// The premise, after the fact: git really sees a difference. If core.fileMode
	// is off in this environment the diff is empty and the silence below would be
	// correct rather than a defect.
	if status := e.Git(proj, "diff", "--name-status", "HEAD~1", "HEAD", "--", "script.sh"); status == "" {
		t.Skipf("git records no change for the chmod (core.fileMode is off on this " +
			"filesystem), so there is no mode difference for the engine to report")
	}

	// The control: the ledger is live.
	if !sawPath(got, "other.md") {
		t.Fatalf("the genuinely edited file is missing from %v — nothing was dispatched, so "+
			"anything concluded about the mode change would be vacuous", got)
	}

	k := statusesFor(got, "script.sh")
	if len(k) == 0 {
		t.Fatalf("a file whose mode changed was not reported at all: %v — the difference was "+
			"derived from content rather than from git's answer, so a change git records and "+
			"a person can see went unjudged", got)
	}
	if k[0] != "M" {
		t.Fatalf("a mode change on a file present at the baseline arrived as %v; want "+
			"M — the path was in the rule's base and is still there, so it is neither an add nor a delete", k)
	}
}

// T026_02: an EMPTY file the cycle creates is reported like any other.
//
// Zero bytes is a legitimate file and an ordinary thing for an agent to leave —
// a placeholder, a truncated log, a `touch`ed marker. It is also where naive
// code drops out: a shell `[ -s file ]` is false, and an engine that used
// "has content" as a proxy for "is a file" reports nothing.
//
// Reported as a CREATE, because the path was not at the baseline. And it must
// carry a real event rather than being folded into silence: a rule that forbids
// empty files is exactly the rule that needs this one.
func TestT026_02_AnEmptyFileIsReported(t *testing.T) {
	e, proj := project(t)

	got := runOne(t, e, proj, "s-026-02", Turns("done",
		Bash("b1", ": > empty.md"),
		Write("w1", "nonempty.md", "has content\n"),
	))

	// The premise: the file exists and really is empty.
	info, err := os.Stat(filepath.Join(proj, "empty.md"))
	if err != nil {
		t.Fatalf("the empty file was never created, so this proves nothing: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("the file is %d bytes, not empty — this is not the case under test", info.Size())
	}

	// The control.
	if !sawPath(got, "nonempty.md") {
		t.Fatalf("the ordinary file is missing from %v — nothing was dispatched", got)
	}

	k := statusesFor(got, "empty.md")
	if len(k) == 0 {
		t.Fatalf("an empty file the cycle created was not reported: %v — zero bytes is a file "+
			"the agent wrote, and a rule forbidding empty files is precisely the one that "+
			"needs to see it", got)
	}
	if k[0] != "A" {
		t.Fatalf("an empty file absent from the baseline arrived as %v; want A", k)
	}
}

// T026_03: a file with NO TRAILING NEWLINE survives the round trip.
//
// git marks this in a diff with "\ No newline at end of file", which is a line
// of diff output that names no path. A reader taking the union of two commands
// and splitting on lines can pick it up as a filename; the engine reads
// --name-status under -z, which does not emit it at all. This pins that the
// answer is right at the session layer, and — the half worth having — that the
// FILE's own bytes are not altered by being judged.
//
// The bytes are checked after the cycle because a hook is handed a path and may
// read the file; an engine or a fixture that rewrote it to add the newline would
// change the agent's work while judging it.
func TestT026_03_AFileWithNoTrailingNewlineIsReportedAndUnaltered(t *testing.T) {
	e, proj := project(t)

	const body = "no newline at the end"

	got := runOne(t, e, proj, "s-026-03", Turns("done",
		Bash("b1", "printf '%s' 'no newline at the end' > terse.md"),
		Write("w1", "ordinary.md", "ends properly\n"),
	))

	// The premise: the file really lacks the trailing newline.
	raw, err := os.ReadFile(filepath.Join(proj, "terse.md"))
	if err != nil {
		t.Fatalf("the file was never written: %v", err)
	}
	if strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("the file ends with a newline (%q), so this is not the case under test", string(raw))
	}

	// The control.
	if !sawPath(got, "ordinary.md") {
		t.Fatalf("the ordinary file is missing from %v — nothing was dispatched", got)
	}
	if k := statusesFor(got, "terse.md"); len(k) == 0 || k[0] != "A" {
		t.Fatalf("a file with no trailing newline was reported as %v; want one A: %v\n"+
			"the marker git prints for this in a textual diff names no path, and a reader that "+
			"mistook it for one would report a change to a file that does not exist", k, got)
	}
	// Nothing must have rewritten the agent's file in the course of judging it.
	if string(raw) != body {
		t.Fatalf("the file's bytes changed during the cycle: %q, want %q", string(raw), body)
	}
}

// T026_04: a file created and then MODIFIED within one range is one add of the
// final bytes.
//
// The third member of the family 022 covers — created-then-deleted is silent,
// deleted-then-recreated within one range is silent — and the one that decides
// WHICH content a rule is judged against.
//
// Against the rule's base the path is simply absent, so whatever the agent did to
// it along the way, the range's change is one addition carrying the bytes at HEAD.
// Two entries would mean the engine is listing commits rather than squashing them;
// an add carrying the FIRST draft would mean a rule is judging bytes that are no
// longer there.
func TestT026_04_CreatedThenModifiedInOneRangeIsOneAddOfTheFinalBytes(t *testing.T) {
	e, proj := project(t)

	// The agent commits the first draft, then overwrites it: two commits in one
	// range.
	got := runOne(t, e, proj, "s-026-04", Turns("done",
		Write("w1", "drafted.md", "FIRSTDRAFT"),
		harness.Commit("draft", "the first draft"),
		Write("w2", "drafted.md", "FINALVERSION"),
	))

	var drafted []observed
	for _, o := range got {
		if o.Path == "drafted.md" {
			drafted = append(drafted, o)
		}
	}
	if len(drafted) == 0 {
		t.Fatalf("a file created and then edited in one range was not reported at all: %v", got)
	}
	if len(drafted) != 1 || drafted[0].Status != "A" {
		t.Fatalf("a file created and edited within one range arrived as %v; want exactly one "+
			"A — the range is one squashed change, so the intermediate commit is not a second "+
			"difference", drafted)
	}
	// The bytes the rule was handed must be the ones at HEAD, not the draft that
	// was overwritten along the way.
	if drafted[0].NewContent != "FINALVERSION" {
		t.Fatalf("the rule was handed %q, want %q — only what the tree holds at HEAD can be "+
			"judged, and a verdict about the overwritten draft is a verdict about bytes that "+
			"are not there", drafted[0].NewContent, "FINALVERSION")
	}
}

// T026_05: an unrelated nested CLONE reaches no rule, and costs the cycle
// nothing.
//
// 021 covers the linked worktree a sub-agent is dispatched into. The exclusion
// in gitrepo is deliberately not about that relationship — it keys on the
// TRAILING SLASH git puts on a path in `ls-files --others` when it declines to
// descend into a nested repository, which it does for any separate checkout
// whoever put it there. A stray clone someone left in the tree produces the
// identical entry for the identical reason, and it is the case that shows the
// rule is about what the thing IS.
//
// What the session layer adds to the gitrepo unit test is the second half: that
// the cycle still completes and the root's own work is judged normally with a
// foreign checkout sitting in the tree. The agent commits with `git add -A`, which
// records the clone as a single embedded-repository entry (a gitlink) and nothing
// of what is inside it, so what is asserted is that no file BENEATH the clone
// reaches the rule.
func TestT026_05_AnUnrelatedNestedCloneReachesNoRule(t *testing.T) {
	e, proj := project(t)

	// A repository of its own, unrelated to this one, sitting inside the tree.
	other := t.TempDir()
	git(t, other, "init", "--initial-branch=main", ".")
	git(t, other, "config", "user.email", "e2e@example.invalid")
	git(t, other, "config", "user.name", "E2E")
	if err := os.WriteFile(filepath.Join(other, "theirs.md"), []byte("not ours\n"), 0o644); err != nil {
		t.Fatalf("seed the other repository: %v", err)
	}
	git(t, other, "add", "-A")
	git(t, other, "commit", "-m", "a repository that is not this one")

	got := runOne(t, e, proj, "s-026-05", Turns("done",
		Bash("b1", "git clone -q "+shellArg(other)+" vendor/clone"),
		Write("w1", "root-own.md", "the root's own work\n"),
	))

	// The control: the root's own work reached the rule.
	if !sawPath(got, "root-own.md") {
		t.Fatalf("the root's own file never reached the rule: %v — nothing was dispatched, so "+
			"the silence below proves nothing", got)
	}

	for _, o := range got {
		if strings.HasPrefix(o.Path, "vendor/clone/") {
			t.Fatalf("a path inside an unrelated nested repository reached a rule about this "+
				"project: %q (all: %v)\nanother checkout's tree is not this session's work, "+
				"whoever put it there", o.Path, got)
		}
	}
}

// T026_06: a file the agent commits is judged once, and is not put in front of the
// rule again once it has passed.
//
// The shape: cycle one writes and commits; cycle two touches something else. The
// rule passed cycle one's range, so its base moved to that head and cycle two's
// range holds only what came after. Read from a ledger OUTSIDE the project: the
// rule's verdicts are keyed on a hash of its whole folder, so a check appending to
// a file inside its own folder would be a different rule at every Stop and its
// watermark would never hold.
func TestT026_06_CommittedWorkIsJudgedOnceAndStaysSettled(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	ledger := filepath.Join(t.TempDir(), "seen")
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{
		"record.sh": "#!/bin/sh\ncat >> " + ledger + "\necho >> " + ledger + "\nexit 0\n",
	})
	e.CommitAll(proj, "the project before the session")

	read := func() []observed {
		body, _ := os.ReadFile(ledger)
		var lines []string
		for _, l := range strings.Split(string(body), "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
		return observedFiles(t, lines)
	}

	e.Run(proj, "s-026-06", "cycle", Turns("done",
		Write("w1", "committed.md", "written then committed\n"),
	).ThenCommit("agent commit"))
	first := read()
	if n := countPath(first, "committed.md"); n == 0 {
		t.Fatalf("work the agent committed was not judged: %v", first)
	}

	e.Run(proj, "s-026-06", "cycle", Turns("done",
		Write("w2", "elsewhere.md", "cycle two\n"),
	).ThenCommit("cycle two"))
	second := read()[len(first):]

	// The control: cycle two dispatched.
	if !sawPath(second, "elsewhere.md") {
		t.Fatalf("cycle two reported nothing at all: %v — the silence below proves nothing", second)
	}
	if n := countPath(second, "committed.md"); n > 0 {
		t.Fatalf("a committed file already judged and passed was put in front of the rule "+
			"again on the next cycle (%d times): %v", n, second)
	}
}

// shellArg renders a path as one single-quoted shell word, for a command a
// scenario hands to the agent.
func shellArg(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
