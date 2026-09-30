package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The git states the differ has to survive, driven through a real session.
//
// gitrepo.Changed answers by asking git TWO questions and unioning them:
// `git diff --name-status -z -M <baseline>` for what git tracks, and
// `git ls-files -z --others --exclude-standard --full-name` for what it does
// not. The seam between them is where this file aims. Each test puts the tree
// into a state that stresses one of:
//
//   - the STATUS ALPHABET the diff can emit — R (rename), C (copy), T
//     (typechange), and the letters this engine does not know;
//   - the PATH ENCODING — a name holding a newline or a quote, which git
//     escapes and quotes in its default output and emits raw under -z, and
//     core.quotepath, which changes that default;
//   - the two questions DISAGREEING or OVERLAPPING — staged-then-deleted, a
//     tracked file that .gitignore also names, a submodule;
//   - the tree being somewhere the baseline does not describe — a mid-cycle
//     commit, a detached HEAD, a rebase in progress.
//
// Every test asserts a POSITIVE — some path this cycle really changed reached
// the rule — before asserting anything about what did not. A differ that fell
// over on an odd tree reports nothing at all, and "the strange path was not
// reported" is satisfied perfectly by that.

// recordEverything is a file-guard that records the changeset it is handed.
//
// `match: path != ""` — an EXPRESSION that admits every real path (a file's path
// is never empty), the file-guard "match everything" this directory needs. It
// cannot use the obvious `**` glob: `**` compiles to the regexp `.*`, whose `.`
// does not match a newline, so a path holding one (T023_08's `odd<newline>name.md`)
// would not be selected and would go unreported — the exact thing that test proves
// arrives. A string `!=` comparison has no such newline blind spot, so it selects
// every path this directory drives, `build.log` (T023_06) and `.gitmodules`
// (T023_10) included.
const recordEverything = `match: path != ""
# deletions: include — this guard observes EVERY change, and a file-guard
# skips deleted files unless it says so.
deletions: include
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
	Status  string
	Path    string
	OldPath string
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
					Path    string `json:"path"`
					OldPath string `json:"oldPath"`
					Status  string `json:"status"`
				} `json:"files"`
			} `json:"changeset"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("the check was handed something that is not a changeset payload: %v\n%s", err, line)
		}
		for _, f := range p.Changeset.Files {
			got = append(got, observed{Status: f.Status, Path: f.Path, OldPath: f.OldPath})
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

// settle commits what the test seeded AND touches the rule's own folder in that
// same commit. A rule's range starts at the last commit that touched its folder,
// so a file committed after the rule would be part of the rule's first range and a
// rename of it would squash to a plain add; touching the folder moves the range's
// start past the seed.
func settle(e *harness.Env, proj, msg string) {
	e.WriteFile(proj, ".sloprail/file-guard/watcher/settled.md", msg+"\n")
	e.CommitAll(proj, msg)
}

// runOne drives a single cycle, in which the agent commits its work at the end,
// and returns everything the rule was handed.
func runOne(t *testing.T, e *harness.Env, proj, sess string, s harness.Scenario) []observed {
	t.Helper()
	e.Run(proj, sess, "cycle", s.ThenCommit("the cycle"))
	return observedFiles(t, e.FileGuardLedgerLines(proj, "watcher", "seen"))
}

// T023_01: a rename arrives as ONE file at the new path, naming the old one.
//
// git reports a rename as a single `R100 old new` entry, and a file-guard's
// changeset keeps it as one: status R, the new path, and `oldPath`. It is not a
// delete plus an add, so a rule reading the changeset can tell a move from a file
// arriving and a file leaving — and a file moved into a guarded directory is seen
// arriving there.
//
// The file is committed first so the rename is a change git can DETECT as one.
func TestT023_01_ARenameIsOneFileNamingItsOldPath(t *testing.T) {
	e, proj := project(t)
	e.WriteFile(proj, "before.md", "content that will move\n")
	settle(e, proj, "the file that will be renamed")

	got := runOne(t, e, proj, "s-023-01", Turns("done",
		Bash("b1", "git mv before.md after.md"),
	))

	var moved *observed
	for _, o := range got {
		if o.Path == "after.md" {
			o := o
			moved = &o
		}
	}
	if moved == nil || moved.Status != "R" || moved.OldPath != "before.md" {
		t.Fatalf("the rename's destination was not reported as a rename from before.md: %+v (all: %v)", moved, got)
	}
	if k := statusesFor(got, "before.md"); len(k) > 0 {
		t.Fatalf("a rename's source was reported as its own file (%v): %v — a rename is not a deletion", k, got)
	}
}

// T023_02: a rename chain across cycles — a to b, then b to c — ends at the last
// path.
//
// The multi-cycle form. The tree has `c`, and whatever range the rule is judging
// ends there: the intermediate path `b` is in neither end of a squashed range that
// spans both moves, and if the range starts after the first move `b` is only the
// old path of the last one. Either way `b` is never a file of its own.
func TestT023_02_ARenameChainAcrossCyclesEndsAtTheLastPath(t *testing.T) {
	e, proj := project(t)
	e.WriteFile(proj, "a.md", "travelling content\n")
	settle(e, proj, "the file that will travel")

	const sess = "s-023-02"
	e.Run(proj, sess, "first move", Turns("done", Bash("b1", "git mv a.md b.md")).ThenCommit("first move"))
	firstEnd := len(e.FileGuardLedgerLines(proj, "watcher", "seen"))

	e.Run(proj, sess, "second move", Turns("done", Bash("b2", "git mv b.md c.md")).ThenCommit("second move"))
	all := e.FileGuardLedgerLines(proj, "watcher", "seen")
	if len(all) <= firstEnd {
		t.Fatalf("the second cycle put nothing in front of the rule, so nothing below can be read")
	}
	second := observedFiles(t, all[firstEnd:])

	// The positive: the final path arrived in the second cycle, as a rename.
	if k := statusesFor(second, "c.md"); len(k) == 0 || k[0] != "R" {
		t.Fatalf("the chain's final path was not reported as a rename in cycle two: %v (all: %v)", k, second)
	}
	// The intermediate path is never a file of its own.
	if k := statusesFor(second, "b.md"); len(k) > 0 {
		t.Fatalf("the intermediate path of a rename chain was reported as %v: %v", k, second)
	}
}

// T023_03: a copy reports the destination and leaves the source silent.
//
// A copy leaves the source untouched, so only the destination differs from the
// baseline. Naming the source as well would report a file that has not changed,
// which untouched_stays_silent forbids — and would send an unmodified file to
// every rule on every cycle it was ever copied from.
//
// Copy detection has to be asked for (`-C`), and this engine does not pass it,
// so git reports the new file as a plain A. The events are the same either way,
// which is the point: the assertion is about what the ENGINE reports, and it
// holds whether the C branch or the A branch produced it.
func TestT023_03_ACopyReportsOnlyTheDestination(t *testing.T) {
	e, proj := project(t)
	e.WriteFile(proj, "source.md", "content that will be copied\n")
	settle(e, proj, "the file that will be copied")

	got := runOne(t, e, proj, "s-023-03", Turns("done",
		Bash("b1", "cp source.md copy.md && git add copy.md"),
	))

	if k := statusesFor(got, "copy.md"); len(k) == 0 || k[0] != "A" {
		t.Fatalf("the copy's destination was not reported as a create: %v (all: %v)", k, got)
	}
	if k := statusesFor(got, "source.md"); len(k) > 0 {
		t.Fatalf("the copy's source was reported as %v though it never changed: %v\n"+
			"an unmodified file put in front of a rule is the noise untouched_stays_silent forbids", k, got)
	}
}

// T023_04: a file replaced by a symlink — git's T status — is reported as a
// modification.
//
// The typechange is present on both sides, so it is an update rather than a
// create or a delete. It is worth its own test because T is a status a parser
// can easily not know: an unrecognised letter used to fail the whole parse, and
// the caller turns a parse error into ZERO events — one unknown status silently
// disabling every file rule for the cycle.
//
// The symlink also exercises lookAt's deliberate choice to treat a link as a
// file (it stats the link, not the target), so this asserts the whole path from
// git's letter to the rule's event.
func TestT023_04_AFileReplacedByASymlinkIsAnUpdate(t *testing.T) {
	e, proj := project(t)
	e.WriteFile(proj, "target.md", "the link's target\n")
	e.WriteFile(proj, "shifty.md", "an ordinary file, for now\n")
	settle(e, proj, "an ordinary file that will become a link")

	got := runOne(t, e, proj, "s-023-04", Turns("done",
		Bash("b1", "rm shifty.md && ln -s target.md shifty.md && git add shifty.md"),
	))

	// The premise: git really calls this a typechange. Without it this is an
	// ordinary content update wearing a T's clothes.
	if status := e.Git(proj, "diff", "--name-status", "HEAD~1", "HEAD", "--", "shifty.md"); !strings.HasPrefix(status, "T") {
		t.Fatalf("git did not report a typechange for shifty.md (%q), so the T branch is not "+
			"being exercised and this test does not do what it claims", status)
	}

	k := statusesFor(got, "shifty.md")
	if len(k) == 0 {
		t.Fatalf("a file replaced by a symlink was not reported at all: %v\n"+
			"an unrecognised status costs the whole cycle its events, which is the silence the "+
			"engine exists to prevent", got)
	}
	if k[0] != "M" {
		t.Fatalf("a typechange was reported as %v; want M — the path is present "+
			"on both sides, so it is neither an arrival nor a departure", k)
	}
}

// T023_05: a file created, staged and then deleted within one cycle is reported
// as no change.
//
// The documented blind spot of the two questions, asserted so that it is a
// DECISION rather than an accident: the diff sees no difference (the path is in
// neither the worktree nor the baseline) and the untracked listing skips it (the
// path is tracked, having been added). gitrepo.Changed says so in its own doc
// comment and names the unit test that pins it; this is the same claim through
// the wiring a user gets.
//
// The honest answer for what this reports: the tree ends the cycle exactly as
// the baseline had it, and there is no content left to judge. The control is a
// file the same cycle leaves behind.
func TestT023_05_StagedThenDeletedIsReportedAsNoChange(t *testing.T) {
	e, proj := project(t)

	got := runOne(t, e, proj, "s-023-05", Turns("done",
		Write("w1", "survivor.md", "still here\n"),
		Bash("b1", "printf 'transient\\n' > ghost.md && git add ghost.md && rm ghost.md"),
	))

	// The positive first: this cycle reported something.
	if !sawPath(got, "survivor.md") {
		t.Fatalf("the file the cycle left behind was not reported: %v — nothing was observed, "+
			"so the silence about the staged-then-deleted file proves nothing", got)
	}
	// The premise: the path really was created and is off the disk, and no commit
	// ever held it.
	if e.Exists(proj, "ghost.md") {
		t.Fatalf("ghost.md is still on disk, so this is not the staged-then-deleted case")
	}
	if log := e.Git(proj, "log", "--all", "--oneline", "--", "ghost.md"); log != "" {
		t.Fatalf("a commit holds ghost.md (%q), so it is not the case this test is about", log)
	}

	if k := statusesFor(got, "ghost.md"); len(k) > 0 {
		t.Fatalf("a file created, staged and deleted within the cycle was reported as %v: %v\n"+
			"the tree ends as the baseline had it and there is no content to judge — this is "+
			"the documented answer, and a change here is a change to a stated decision", k, got)
	}
}

// T023_06: a tracked file that .gitignore also names is still reported.
//
// The two questions disagree here, and only one of them is asked with
// --exclude-standard. `git ls-files --others --exclude-standard` honours the
// ignore file, which is what keeps node_modules out of every guardrail's way —
// but a file git is ALREADY TRACKING is not an "other", so the diff reports it
// regardless of what .gitignore says. That is the correct answer: git itself
// keeps tracking such a file, and a change to it is a change to the repository.
//
// An engine that filtered by .gitignore after the fact — rather than letting the
// two questions each answer their own half — would silence a tracked file and a
// rule bound to it would stop firing the day someone added a broad ignore
// pattern.
func TestT023_06_ATrackedButIgnoredFileIsStillReported(t *testing.T) {
	e, proj := project(t)
	e.WriteFile(proj, "build.log", "tracked before it was ignored\n")
	e.WriteFile(proj, ".gitignore", "*.log\n")
	e.Git(proj, "add", "-A", "-f")
	e.Git(proj, "commit", "-m", "a tracked file that gitignore also names")
	settle(e, proj, "the range starts after it")

	// The premise, both halves: git tracks it AND ignores it.
	if tracked := e.Git(proj, "ls-files", "--", "build.log"); tracked == "" {
		t.Fatalf("build.log is not tracked, so this is an ordinary ignored file and the case " +
			"this test is about does not exist")
	}
	if ignored := e.Git(proj, "check-ignore", "-v", "--no-index", "build.log"); ignored == "" {
		t.Fatalf("build.log is not matched by .gitignore, so nothing here is being ignored")
	}

	got := runOne(t, e, proj, "s-023-06", Turns("done",
		Write("w1", "build.log", "changed by the agent\n"),
	))

	if k := statusesFor(got, "build.log"); len(k) == 0 || k[0] != "M" {
		t.Fatalf("a tracked file was not reported because .gitignore names it: %v (all: %v)\n"+
			"git tracks it and a change to it is a change to the repository; silencing it makes "+
			"a rule stop firing the day a broad ignore pattern is added", k, got)
	}
}

// T023_07: an ignored, untracked file stays out.
//
// The other side of T023_06, and what stops it being read as "ignore rules do
// nothing". A file that is untracked AND ignored is exactly what
// --exclude-standard exists to drop: reporting it would put node_modules in
// front of every guardrail the first time a dependency is installed.
//
// The control is a non-ignored file written in the same cycle.
func TestT023_07_AnIgnoredUntrackedFileStaysOut(t *testing.T) {
	e, proj := project(t)
	e.WriteFile(proj, ".gitignore", "junk/\n")
	e.CommitAll(proj, "the ignore rules")

	got := runOne(t, e, proj, "s-023-07", Turns("done",
		Write("w1", "real-work.md", "the agent's actual work\n"),
		Bash("b1", "mkdir -p junk && printf 'noise\\n' > junk/generated.md"),
	))

	if !sawPath(got, "real-work.md") {
		t.Fatalf("the cycle's real work was not reported: %v — nothing was observed, so the "+
			"silence about the ignored file proves nothing", got)
	}
	for _, o := range got {
		if strings.HasPrefix(o.Path, "junk/") {
			t.Fatalf("an ignored, untracked file was reported as %s %q: %v\n"+
				"this is the flood --exclude-standard exists to hold back", o.Status, o.Path, got)
		}
	}
}

// T023_08: a path holding a newline or a quote survives the encoding.
//
// The reason both git questions are asked with -z. In git's default output a
// path holding a newline, a quote or a non-ASCII byte is escaped and wrapped in
// quotes, so a line-oriented reader mis-splits "a\nb.md" into two entries and
// reads the literal quotes as part of the name. Under -z the paths are emitted
// raw and NUL-terminated.
//
// Both characters in one cycle, plus an ordinary file as the control. The
// assertion is that the rule is handed the REAL name — the same bytes the shell
// created — rather than a quoted or truncated rendering of it.
func TestT023_08_APathWithANewlineOrAQuoteSurvives(t *testing.T) {
	e, proj := project(t)

	const newlineName = "odd\nname.md"
	const quoteName = "odd\"quote.md"

	got := runOne(t, e, proj, "s-023-08", Turns("done",
		Write("w1", "ordinary.md", "the control\n"),
		// Written through the shell so the names are exactly these bytes and
		// nothing in the harness has to encode them.
		Bash("b1", `printf 'x\n' > "odd`+"\n"+`name.md"; printf 'y\n' > 'odd"quote.md'`),
	))

	if !sawPath(got, "ordinary.md") {
		t.Fatalf("the control file was not reported: %v — nothing was observed, so what the "+
			"odd names did or did not do proves nothing", got)
	}
	// The files really were created under these names, or the engine is being
	// asked about paths that do not exist.
	if !e.Exists(proj, newlineName) {
		t.Fatalf("the newline-named file was never created, so the encoding is not being tested")
	}
	if !e.Exists(proj, quoteName) {
		t.Fatalf("the quote-named file was never created, so the encoding is not being tested")
	}

	if k := statusesFor(got, newlineName); len(k) == 0 || k[0] != "A" {
		t.Fatalf("a path holding a newline was not reported under its real name: %v (all: %v)\n"+
			"a line-oriented reader splits this into two entries and every rule bound to the "+
			"file is asked about paths that do not exist", k, got)
	}
	if k := statusesFor(got, quoteName); len(k) == 0 || k[0] != "A" {
		t.Fatalf("a path holding a quote was not reported under its real name: %v (all: %v)\n"+
			"read from git's default output the literal quotes become part of the name", k, got)
	}
}

// T023_09: core.quotepath does not change what the rule is handed.
//
// core.quotepath=true is git's default for non-ASCII bytes in its LINE-oriented
// output, and a user may set it explicitly. Under -z it must make no difference
// at all — that is the whole reason -z is used rather than parsing the quoted
// form. This pins that: with the setting on, a non-ASCII path still arrives as
// its real bytes rather than as \303\251 escapes.
//
// If this ever fails, the failure is a rule being asked about a path that does
// not exist on disk, which reads to the rule as a deleted file.
func TestT023_09_QuotepathDoesNotChangeWhatTheRuleIsHanded(t *testing.T) {
	e, proj := project(t)
	e.Git(proj, "config", "core.quotepath", "true")

	const accented = "café.md"

	got := runOne(t, e, proj, "s-023-09", Turns("done",
		Write("w1", "ordinary.md", "the control\n"),
		Bash("b1", "printf 'z\\n' > 'café.md'"),
	))

	if !sawPath(got, "ordinary.md") {
		t.Fatalf("the control file was not reported: %v — nothing was observed", got)
	}
	if !e.Exists(proj, accented) {
		t.Fatalf("the accented file was never created, so quotepath is not being tested")
	}
	// The premise: the setting really is on, and git really would quote this
	// path in its line-oriented output.
	if v := e.Git(proj, "config", "core.quotepath"); v != "true" {
		t.Fatalf("core.quotepath is %q, so this test is not exercising the setting it names", v)
	}

	if k := statusesFor(got, accented); len(k) == 0 || k[0] != "A" {
		t.Fatalf("a non-ASCII path was not reported under its real bytes with core.quotepath "+
			"on: %v (all: %v)\nthe rule is being handed an escaped rendering, which names no "+
			"file on disk and reads as a deletion", k, got)
	}
}

// T023_10: a submodule's contents are never the parent's changes; the gitlink
// is one entry.
//
// The parent's diff names the gitlink — the submodule's own path — as one entry,
// and `.gitmodules` as an ordinary file.
//
// What is asserted: .gitmodules arrives (the parent really did change), the
// submodule's own files never do (another repository's content is not this one's
// work), and the gitlink is a single entry at the submodule's path.
func TestT023_10_ASubmoduleIsAGitlinkNotItsContents(t *testing.T) {
	e, proj := project(t)

	// A second repository to embed. Built inside the project's parent so the
	// file: URL is local and no network is involved.
	sub := t.TempDir()
	e.Git(sub, "init", "--initial-branch=main")
	e.Git(sub, "config", "user.email", "e2e@example.invalid")
	e.Git(sub, "config", "user.name", "E2E")
	e.WriteFile(sub, "inner.md", "a file inside the submodule\n")
	e.CommitAll(sub, "the submodule's own content")

	got := runOne(t, e, proj, "s-023-10", Turns("done",
		Write("w1", "ordinary.md", "the control\n"),
		Bash("b1", "git -c protocol.file.allow=always submodule add "+sub+" vendored >/dev/null 2>&1"),
	))

	if !sawPath(got, "ordinary.md") {
		t.Fatalf("the control file was not reported: %v — nothing was observed, so what the "+
			"submodule did or did not produce proves nothing", got)
	}
	// The premise: a submodule really was added. Without it this asserts the
	// absence of files that were never there.
	if !e.Exists(proj, ".gitmodules") {
		t.Skip("git refused to add a local submodule in this environment (protocol.file), so " +
			"there is no gitlink here to be right or wrong about")
	}

	// The submodule's own files are NOT the parent's changes.
	for _, o := range got {
		if strings.HasPrefix(o.Path, "vendored/") {
			t.Fatalf("a file inside a submodule was reported as the parent's work: %s %q\n"+
				"the parent's diff only notices a submodule when the commit it points at moves; "+
				"reporting its contents puts another repository in front of this one's rules",
				o.Status, o.Path)
		}
	}
	// The parent really did change, and said so: .gitmodules is an ordinary file
	// and arrives as one. This is the positive that makes the two absences above
	// and below readable.
	if k := statusesFor(got, ".gitmodules"); len(k) == 0 || k[0] != "A" {
		t.Fatalf("adding a submodule did not report .gitmodules as a create: %v (all: %v)\n"+
			"the parent repository changed and nothing said so", k, got)
	}

	// The gitlink itself is ONE entry at the submodule's own path — a pointer, not
	// a tree — and nothing beneath it (checked above).
	if k := statusesFor(got, "vendored"); len(k) != 1 || k[0] != "A" {
		t.Fatalf("the submodule's gitlink was reported as %v; want exactly one A entry: %v", k, got)
	}
}

// T023_11: an agent that commits mid-cycle still has its work in the difference.
//
// difference_spans_both, driven at the seam rather than at the invariant: after
// the commit the tree is CLEAN, so `git status` says nothing happened, and the
// untracked question answers nothing too. Only the diff against the session's
// baseline still holds the work. An implementation reading only what is
// outstanding reports that the cycle changed nothing, which is precisely wrong
// and silently so.
//
// Both halves in one cycle so neither can carry the test alone.
func TestT023_11_AMidCycleCommitStaysInTheDifference(t *testing.T) {
	e, proj := project(t)

	got := runOne(t, e, proj, "s-023-11", Turns("done",
		Write("w1", "committed.md", "this gets committed\n"),
		Bash("b1", "git add committed.md && git commit -m 'the agent commits'"),
		Write("w2", "outstanding.md", "this does not\n"),
	))

	if !sawPath(got, "committed.md") {
		t.Fatalf("work committed during the cycle fell out of the difference: %v\n"+
			"a comparison reading only what is outstanding finds a clean tree and reports that "+
			"the cycle changed nothing", got)
	}
	if !sawPath(got, "outstanding.md") {
		t.Fatalf("work left outstanding fell out of the difference: %v", got)
	}
}

// T023_12: a detached HEAD does not lose the work committed before it.
//
// Checking out the session's own commit detaches HEAD without leaving the
// history the rule's range is measured over, so the work is still in it. An
// engine that lost the range on any HEAD movement would push the cycle's own work
// out of the difference at exactly the moment an agent inspects a commit.
//
// The work written BEFORE the detach is the discriminator — it must still be
// reported afterwards.
func TestT023_12_ADetachedHeadReachingTheBaselineKeepsThePoint(t *testing.T) {
	e, proj := project(t)

	const sess = "s-023-12"
	e.Run(proj, sess, "write then detach", Turns("done",
		Write("w1", "early.md", "written before the detach\n"),
		Bash("b1", "git add early.md && git commit -m 'the agent commits' && git checkout --detach HEAD"),
	).ThenCommit("on the detached head"))

	// The premise: HEAD really is detached.
	if ref := e.Git(proj, "rev-parse", "--abbrev-ref", "HEAD"); ref != "HEAD" {
		t.Fatalf("HEAD is not detached (on %q), so this test is not about a detached HEAD", ref)
	}

	got := observedFiles(t, e.FileGuardLedgerLines(proj, "watcher", "seen"))
	if !sawPath(got, "early.md") {
		t.Fatalf("work committed before a detach fell out of the difference: %v\n"+
			"the detached commit still reaches the session's baseline, so the point must not "+
			"have moved and this work is still the cycle's", got)
	}
}

// T023_14: an unreadable status costs that path and no other.
//
// Not reachable through git itself — there is no way to make it emit a letter
// this engine does not know — so what is asserted here is the CONSEQUENCE that
// matters and is reachable: a cycle whose difference includes something the
// engine cannot classify still dispatches everything it could.
//
// The stand-in is a path git reports and the file module cannot turn into a file
// event: a directory, arriving as untracked content. filemod's lookAt reports
// ErrPathIsNotAFile for it, which is the same shape as an unclassifiable status
// — one path dropped, the rest kept. An engine that turned that into a failed
// cycle would silence every rule for the turn, which is the silence the whole
// design exists to prevent.
func TestT023_14_AnUnclassifiablePathDoesNotCostTheCycle(t *testing.T) {
	e, proj := project(t)

	got := runOne(t, e, proj, "s-023-14", Turns("done",
		Write("w1", "before-it.md", "written before\n"),
		// An empty directory git will not report, and a submodule-shaped
		// directory it will: a nested repository is untracked content that is
		// not a file.
		Bash("b1", "mkdir -p nested && git -c init.defaultBranch=main init -q nested && printf 'x\\n' > nested/inside.md && "+
			"git -C nested add -A && git -C nested -c user.email=a@b.invalid -c user.name=a commit -qm inside"),
		Write("w2", "after-it.md", "written after\n"),
	))

	// Both ordinary files reached the rule, on either side of the odd one.
	if !sawPath(got, "before-it.md") {
		t.Fatalf("a file written before the unclassifiable path was not reported: %v", got)
	}
	if !sawPath(got, "after-it.md") {
		t.Fatalf("a file written after the unclassifiable path was not reported: %v\n"+
			"one path the engine could not turn into an event cost the rest of the cycle its "+
			"judging — one unclassifiable file becoming every file nobody judged", got)
	}
}
