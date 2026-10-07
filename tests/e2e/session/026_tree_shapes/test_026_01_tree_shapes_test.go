package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
	"github.com/sloprail/sloprail/tests/e2e/session/changesetkit"
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

// recordEverything is a NEW-FORMAT file-guard that records every Changeset it is handed .
//
// `match: path != ""` — an expression that admits every real path, the file-guard
// "match everything" this directory needs: it drives non-`.md` paths a `**/*.md`
// match would silence (`script.sh` in T026_01, `vendor/clone/…` in T026_05) and
// asserts what does or does not reach the rule for them. It is an expression rather
// than the `**` glob because `**` compiles to the regexp `.*`, whose `.` does not
// match a newline — the same blind spot 023 documents — and an empty match is
// rejected at load. A single file-guard fires on every committed change, so the create/update/delete classification the kind assertions read
// comes through the new dispatch unchanged.
//
// Because the match is this wide it WOULD also select the guard's own ledger
// (`.sloprail/file-guard/watcher/seen`), so the `.sloprail/*` skip in the check is
// LOAD-BEARING — it stops the guard re-observing its own bookkeeping.
const recordEverything = `match: path != ""
checks:
  - script: ./record.sh
`

// countPath is how many times a path was put in front of the rule.
func countPath(got []changesetkit.Observed, path string) int {
	return len(changesetkit.Statuses(got, path))
}

// project is a repository with the recording guardrail committed, so the rule's
// own folder is part of the baseline rather than of every difference. The third
// result is the ledger the rule records into.
func project(t *testing.T) (*harness.Env, string, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	ledger := filepath.Join(t.TempDir(), "seen")
	e.GitInit(proj)
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": harness.RecordScript(ledger)})
	e.CommitAll(proj, "the project before the session")
	return e, proj, ledger
}

// runOne drives a single cycle, in which the agent commits its work, and returns
// everything the rule was handed.
func runOne(t *testing.T, e *harness.Env, proj, ledger, sess string, s harness.Scenario) []changesetkit.Observed {
	t.Helper()
	e.Run(proj, sess, "cycle", s.ThenCommit("the agent's work"))
	return changesetkit.Files(t, harness.ReadLedgerLines(t, ledger))
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
	// The files are committed BEFORE the rule is installed: a rule's range starts
	// at the commit that added it, so they are at the baseline.
	e := New(t)
	proj := e.Project()
	ledger := filepath.Join(t.TempDir(), "seen")
	e.GitInit(proj)
	e.WriteFile(proj, "script.sh", "#!/bin/sh\necho hello\n")
	e.WriteFile(proj, "other.md", "original\n")
	e.CommitAll(proj, "a file the session will chmod")
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": harness.RecordScript(ledger)})
	e.CommitAll(proj, "the project before the session")

	// The premise: git is actually tracking the mode. On a filesystem or a
	// configuration where core.fileMode is off, `chmod` is invisible to git and
	// this test would be asserting about a change that does not exist.
	if mode := strings.Fields(harness.Git(t, proj, "ls-files", "-s", "script.sh"))[0]; mode != "100644" {
		t.Fatalf("the file does not start non-executable in the index (%s), so the chmod "+
			"below is not the change this test is about", mode)
	}

	got := runOne(t, e, proj, ledger, "s-026-01", Turns("done",
		Bash("b1", "chmod +x script.sh"),
		Write("w1", "other.md", "genuinely edited\n"),
	))

	// The premise, after the fact: git really sees a difference. If core.fileMode
	// is off in this environment the diff is empty and the silence below would be
	// correct rather than a defect.
	if changed := e.Git(proj, "diff", "--name-only", "HEAD~1", "HEAD", "--", "script.sh"); changed == "" {
		t.Skipf("git records no change for the chmod (core.fileMode is off on this " +
			"filesystem), so there is no mode difference for the engine to report")
	}

	// The control: the ledger is live.
	if !changesetkit.Saw(got, "other.md") {
		t.Fatalf("the genuinely edited file is missing from %v — nothing was dispatched, so "+
			"anything concluded about the mode change would be vacuous", got)
	}

	k := changesetkit.Statuses(got, "script.sh")
	if len(k) == 0 {
		t.Fatalf("a file whose mode changed was not reported at all: %v — the difference was "+
			"derived from content rather than from git's answer, so a change git records and "+
			"a person can see went unjudged", got)
	}
	if k[0] != "M" {
		t.Fatalf("a mode change on a file present at the baseline arrived as %v; want "+
			"PostFileUpdate — the path was at the point being measured from and is still "+
			"there, so it is neither a creation nor a deletion", k)
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
	e, proj, ledger := project(t)

	got := runOne(t, e, proj, ledger, "s-026-02", Turns("done",
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
	if !changesetkit.Saw(got, "nonempty.md") {
		t.Fatalf("the ordinary file is missing from %v — nothing was dispatched", got)
	}

	k := changesetkit.Statuses(got, "empty.md")
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
	e, proj, ledger := project(t)

	const body = "no newline at the end"

	got := runOne(t, e, proj, ledger, "s-026-03", Turns("done",
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
	if !changesetkit.Saw(got, "ordinary.md") {
		t.Fatalf("the ordinary file is missing from %v — nothing was dispatched", got)
	}
	if k := changesetkit.Statuses(got, "terse.md"); len(k) == 0 || k[0] != "A" {
		t.Fatalf("a file with no trailing newline was reported as %v; want one A: %v\n"+
			"the marker git prints for this in a textual diff names no path, and a reader that "+
			"mistook it for one would report a change to a file that does not exist", k, got)
	}
	// Nothing must have rewritten the agent's file in the course of judging it.
	if string(raw) != body {
		t.Fatalf("the file's bytes changed during the cycle: %q, want %q", string(raw), body)
	}
}

// T026_04: a file created and then MODIFIED within one cycle is one create of
// the final bytes.
//
// The third member of the family 022 covers — created-then-deleted is silent,
// deleted-then-recreated is a delete then an update — and the one that decides
// WHICH content a rule is judged against.
//
// Against the session's baseline the path is simply absent, so whatever the
// agent did to it along the way, the cycle's difference is one creation. Two
// events would mean the engine is accumulating what it saw happen rather than
// comparing; a create carrying the FIRST draft would mean a rule is judging
// bytes that are no longer on disk, which is the plainest violation of
// change_is_observed there is.
//
// The content half is asserted through a rule that reads the file, because the
// event carries a path rather than the bytes: the fixture greps the file on disk
// and records what it found, which is exactly what a real content rule does.
// sr:proves events/post-changes-are-the-tree-diff
func TestT026_04_CreatedThenModifiedInOneCycleIsOneCreateOfTheFinalBytes(t *testing.T) {
	e, proj, ledger := project(t)
	content := filepath.Join(t.TempDir(), "content")

	// Reads the file the changeset names and records what it found there. The
	// project root is derived from $SR_GUARDRAIL_DIR (`.sloprail/file-guard/watcher`,
	// so trimming `/.sloprail/file-guard/*` yields the project root). Both ledgers
	// live outside the repository.
	readsContent := "#!/bin/sh\npayload=\"$(cat)\"\nprintf '%s\\n' \"$payload\" >> '" + ledger + "'\n" +
		"root=\"${SR_GUARDRAIL_DIR%/.sloprail/file-guard/*}\"\n" +
		"case \"$payload\" in *drafted.md*) [ -f \"$root/drafted.md\" ] && printf 'drafted.md=%s\\n' \"$(cat \"$root/drafted.md\")\" >> '" + content + "' ;; esac\n" +
		"exit 0\n"
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": readsContent})
	e.CommitAll(proj, "the rule reads content")

	got := runOne(t, e, proj, ledger, "s-026-04", Turns("done",
		Write("w1", "drafted.md", "FIRSTDRAFT"),
		Write("w2", "drafted.md", "FINALVERSION"),
	))

	k := changesetkit.Statuses(got, "drafted.md")
	if len(k) == 0 {
		t.Fatalf("a file created and then edited in one cycle was not reported at all: %v", got)
	}
	if len(k) != 1 || k[0] != "A" {
		t.Fatalf("a file created and edited within one cycle arrived as %v; want exactly one "+
			"A — against the session's baseline the path is simply absent, so the "+
			"intermediate write is not a second difference; reporting two events means the "+
			"engine is accumulating what it saw rather than comparing against the point", k)
	}

	// The bytes the rule was shown must be the ones on disk at the end of the
	// cycle, not the draft that was overwritten during it.
	//
	// Read only the line for THIS path. The rule records one `path=content` line
	// per file it is handed, and the cycle also reports the harness's own
	// .scenario.sh — which literally contains the string "FIRSTDRAFT", because
	// the scenario script is what writes it. Scanning the whole ledger for that
	// word therefore matches the scenario file and fails against a correct
	// engine, which is what this assertion did before it was scoped.
	var line string
	for _, l := range harness.ReadLedgerLines(t, content) {
		if strings.HasPrefix(l, "drafted.md=") {
			line = strings.TrimPrefix(l, "drafted.md=")
		}
	}
	if line == "" {
		t.Fatalf("the rule never read the file it was told about, so which bytes it would have "+
			"been judging cannot be observed and the assertion below would be vacuous: %v",
			harness.ReadLedgerLines(t, content))
	}
	if line != "FINALVERSION" {
		t.Fatalf("the rule was shown %q, want %q — only what the tree holds when the "+
			"difference is taken can be judged, and a verdict about the overwritten draft is "+
			"a verdict about bytes that are not on disk", line, "FINALVERSION")
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
// WHAT THIS TEST CAN AND CANNOT CATCH, measured rather than claimed. Removing
// the exclusion in untrackedPaths — so the boundary marker is carried — does NOT
// fail this test. The path git emits is the DIRECTORY, and filemod's lookAt
// stats it, finds a directory, and reports ErrPathIsNotAFile instead of emitting
// an event. So the noise is absorbed one layer below the rules by a check that
// exists for an unrelated reason, and no guardrail ever sees it either way.
// Confirmed: with `strings.HasSuffix(p, "/")` disabled, this stayed green while
// internal/gitrepo's TestChanged_AnUnrelatedNestedCloneIsAlsoNotThisTreesContent
// failed, which is where that claim is really pinned.
//
// It is kept, and kept honest about that, for the reason T021_03 is kept: the
// absorption is defence in depth rather than the mechanism, and this is what
// would notice if a future kind were bound to something other than a regular
// file — at which point the exclusion in gitrepo becomes the only thing between
// another repository's tree and a rule about this project. It also pins the
// second half, which the unit test cannot: that the cycle still completes and
// the root's own work is judged normally with a foreign checkout sitting in the
// tree.
//
// Both premises are required before the silence is read: the root's own change
// must be present, and git must still be naming the clone in its raw listing.
func TestT026_05_AnUnrelatedNestedCloneReachesNoRule(t *testing.T) {
	e, proj, ledger := project(t)

	// A repository of its own, unrelated to this one, sitting inside the tree.
	other := t.TempDir()
	harness.InitRepo(t, other)
	if err := os.WriteFile(filepath.Join(other, "theirs.md"), []byte("not ours\n"), 0o644); err != nil {
		t.Fatalf("seed the other repository: %v", err)
	}
	harness.CommitAllIn(t, other, "a repository that is not this one")

	// The agent commits its own file only: the clone stays an untracked nested
	// repository, which commit-required must not count as guarded work.
	sess := "s-026-05"
	e.Run(proj, sess, "cycle", Turns("done",
		Bash("b1", "git clone -q "+shellArg(other)+" vendor/clone"),
		Write("w1", "root-own.md", "the root's own work\n"),
		harness.CommitPaths("c1", "the agent's work", "root-own.md"),
	))
	if errs := harness.CommitRequired(e.BlockingErrorsFrom(proj, sess, "Stop")); len(errs) != 0 {
		t.Fatalf("an untracked nested clone was treated as uncommitted guarded work: %q", errs)
	}
	got := changesetkit.Files(t, harness.ReadLedgerLines(t, ledger))
	// The premise: the clone really is a separate checkout sitting in the tree.
	if _, err := os.Stat(filepath.Join(proj, "vendor", "clone", ".git")); err != nil {
		t.Fatalf("the nested clone is not a repository of its own, so there is no foreign "+
			"tree here for the engine to keep out: %v", err)
	}

	// The control: the root's own work reached the rule.
	if !changesetkit.Saw(got, "root-own.md") {
		t.Fatalf("the root's own file never reached the rule: %v — nothing was dispatched, so "+
			"the silence below proves nothing", got)
	}

	// The clone was never committed (no gitlink), and no path inside it reached the rule.
	for _, o := range got {
		if strings.HasPrefix(o.Path, "vendor/clone/") {
			t.Fatalf("a path inside an unrelated nested repository reached a rule about this "+
				"project: %q (all: %v)\nanother checkout's tree is not this session's work, "+
				"whoever put it there", o.Path, got)
		}
	}
}

// T026_06: a file the agent COMMITS mid-cycle is judged on the committed bytes, and the
// verdict recorded for it stays settled.
//
// difference_spans_both is covered — 016 and 023_11 both show committed work
// still reaching a rule. What is NOT covered is what happens to the VERDICT
// across that boundary: the range holds the commit on the next cycle too, so the
// committed file is in front of the rule again, and re-asking a model about the
// same range is the cost a verdict cache exists to avoid. The recorder (a script)
// runs every time by design; a judge is asked only when the results hold no
// verdict for exactly what it would be given.
//
// The shape: cycle one writes and commits; cycle two touches something else, which
// makes the range a different question and asks the judge again; and a further
// `sr check run` over that same range asks nothing.
func TestT026_06_CommittedWorkIsJudgedOnceAndStaysSettled(t *testing.T) {
	e, proj, ledger := project(t)
	e.FileGuard(proj, "verdict", "match: \"**/*.md\"\nchecks:\n  - judge: ./rubric.md.j2\n",
		map[string]string{"rubric.md.j2": "Does this change hold up?\n{{ change }}\n"})
	e.CommitAll(proj, "the judged rule")
	const promptFile = ".git/judge-prompt"
	e.InstallJudgeClaudeCapturing(proj, promptFile, `{"pass": true, "reasoning": "fine"}`)

	first := runOne(t, e, proj, ledger, "s-026-06", Turns("done",
		Write("w1", "committed.md", "written then committed\n"),
	))

	// The premise: it really was committed, so the range genuinely spans the commit.
	if status := e.Git(proj, "status", "--porcelain", "--", "committed.md"); status != "" {
		t.Fatalf("the file is still outstanding (%q), so this does not test the committed case", status)
	}
	if n := countPath(first, "committed.md"); n == 0 {
		t.Fatalf("work the agent committed mid-cycle was not judged: %v — a difference that "+
			"only looked at outstanding work found nothing and called the cycle empty", first)
	}
	asked := e.JudgeCalls(proj, promptFile, "")
	if asked == 0 {
		t.Fatalf("the judge was never asked about the committed file")
	}
	if p := e.JudgePrompt(proj, promptFile); !strings.Contains(p, "written then committed") {
		t.Fatalf("the judge was not shown the committed bytes:\n%s", p)
	}

	// Cycle two touches something else; the range holds both files and is judged as a whole.
	second := runOne(t, e, proj, ledger, "s-026-06", Turns("done", Write("w2", "elsewhere.md", "cycle two\n")))
	if !changesetkit.Saw(second, "elsewhere.md") {
		t.Fatalf("cycle two reported nothing at all: %v — the silence below proves nothing", second)
	}
	settled := e.JudgeCalls(proj, promptFile, "")
	if settled == asked {
		t.Fatalf("cycle two never asked the judge about its own work (%d calls throughout)", settled)
	}

	// The same range again: the verdict is in the results, and nothing is asked.
	e.CheckRunRaw(proj, "s-026-06", "origin/main", "HEAD")
	if again := e.JudgeCalls(proj, promptFile, ""); again != settled {
		t.Fatalf("a settled verdict was re-asked: %d judge calls after the second cycle, %d after re-running the same range", settled, again)
	}
}

// shellArg renders a path as one single-quoted shell word, for a command a
// scenario hands to the agent.
func shellArg(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
