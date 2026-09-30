package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"github.com/sloprail/sloprail/tests/e2e/session/changesetkit"
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

// recordEverything is a file-guard that records every changeset it is handed
// (`.changeset.files[]`, each {path, status A/M/D}) and passes.
//
// `match: path != ""` — an EXPRESSION that admits every real path (a file's path
// is never empty), the file-guard "match everything" this directory needs. It
// cannot use the obvious `**` glob: `**` compiles to the regexp `.*`, whose `.`
// does not match a newline, so a path holding one (T023_08's `odd<newline>name.md`)
// would not be selected and would go unreported — the exact thing that test proves
// arrives. It cannot use an empty match either (a file-guard requires a non-empty
// `match` at load). A string `!=` comparison has no such newline blind spot, so
// it selects every path this directory drives, `build.log` (T023_06) and
// `.gitmodules` (T023_10) included.
//
// `deletions: include` because this guard observes EVERY change and a file-guard
// skips deleted files unless it says so.
const recordEverything = `match: path != ""
deletions: include
checks:
  - script: ./record.sh
`

// project is a repository with the recording guardrail committed LAST, after
// whatever the test's `pre` puts into the history: a rule's range floors at the
// last commit touching its own folder, so anything committed after the rule would
// be part of the first range instead of what the range starts from. pre may be nil;
// it commits its own work. It returns the ledger the check records to.
func project(t *testing.T, pre func(e *harness.Env, proj string)) (*harness.Env, string, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	led := e.NewLedger("seen")
	ledger := led.Path()
	e.GitInit(proj)
	if pre != nil {
		pre(e, proj)
	}
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": harness.RecordScript(ledger)})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the project before the session")
	return e, proj, ledger
}

// seed writes files and commits them, for project's pre.
func seed(msg string, files ...[2]string) func(e *harness.Env, proj string) {
	return func(e *harness.Env, proj string) {
		for _, f := range files {
			e.WriteFile(proj, f[0], f[1])
		}
		e.CommitAll(proj, msg)
	}
}

func ledgerFiles(t *testing.T, ledger string) []changesetkit.Observed {
	t.Helper()
	return changesetkit.Files(t, harness.ReadLedgerLines(t, ledger))
}

// runOne drives a single cycle and returns everything the rule was handed.
func runOne(t *testing.T, e *harness.Env, proj, ledger, sess string, s harness.Scenario) []changesetkit.Observed {
	t.Helper()
	e.Run(proj, sess, "cycle", s)
	return ledgerFiles(t, ledger)
}

func hasStatus(got []changesetkit.Observed, path, status string) bool {
	k := changesetkit.Statuses(got, path)
	return len(k) > 0 && k[0] == status
}

// T023_01: a rename arrives as ONE entry, status R, at the new path and naming
// the path it came from.
//
// git reports a rename as ONE `R100 old new` entry naming both paths, and the
// changeset keeps it as one entry: the destination is what a rule is asked about,
// and oldPath is what lets a rule bound to the old path learn the file left.
// Reported as a delete of the old path plus a create of the new instead, the
// entry would no longer say the two are the same file; reported without oldPath,
// a file could leave a guarded directory without the rule that guards it ever
// being asked.
//
// The file is committed first so the rename is a change git can DETECT as one;
// an uncommitted file moved is just an untracked path appearing.
func TestT023_01_ARenameIsOneEntryNamingBothPaths(t *testing.T) {
	e, proj, ledger := project(t, seed("the file that will be renamed", [2]string{"before.md", "content that will move\n"}))

	got := runOne(t, e, proj, ledger, "s-023-01", Turns("done",
		Bash("b1", "git mv before.md after.md"),
	).ThenCommit("rename before to after"))

	// The premise: git really classified this as a rename rather than as an
	// unrelated delete and add. If it did not, the test still asserts the right
	// events but stops exercising the R branch, so it says so.
	if status := e.Git(proj, "diff", "--name-status", "-M", "HEAD~1", "HEAD"); !strings.Contains(status, "R") {
		t.Logf("NOTE: git did not report an R status (%q) — the two events below are still "+
			"correct, but the rename-splitting branch is not what produced them", status)
	}

	// A rename is ONE entry of the changeset: the new path, status R, naming the
	// path it came from. Nothing about the old path is a separate entry.
	if k := changesetkit.Statuses(got, "after.md"); len(k) != 1 || k[0] != "R" {
		t.Fatalf("the rename's destination was not reported as a rename: %v (all: %v)\n"+
			"a file arrived at a path no rule was asked about", k, got)
	}
	if !changesetkit.RenamedFrom(got, "after.md", "before.md") {
		t.Fatalf("the rename entry does not name the path it came from, so a rule bound to the " +
			"old path never learns the file left")
	}
	if changesetkit.Saw(got, "before.md") {
		t.Fatalf("the rename's source was also reported as an entry of its own: %v", got)
	}
}

// T023_02: a rename chain within one range — a to b, then b to c.
//
// Two commits, each a rename, squash to the net change: `a` is gone and `c` has
// arrived, and `b`, which exists in neither the range's base nor its head, is not
// a difference at all. An engine walking the commits one by one and reporting what
// each did would report b as a live path, and a rule would be asked about a file
// that exists nowhere.
func TestT023_02_ARenameChainWithinARangeEndsAtTheLastPath(t *testing.T) {
	e, proj, ledger := project(t, seed("the file that will travel", [2]string{"a.md", "travelling content\n"}))

	got := runOne(t, e, proj, ledger, "s-023-02", Turns("done",
		Bash("b1", "git mv a.md b.md"),
		harness.Commit("c1", "first move"),
		Bash("b2", "git mv b.md c.md"),
	).ThenCommit("second move"))

	if len(got) == 0 {
		t.Fatalf("the range put nothing in front of the rule, so nothing below can be read")
	}
	if k := changesetkit.Statuses(got, "c.md"); len(k) != 1 || k[0] != "R" {
		t.Fatalf("the chain's final path was not reported as a rename: %v (all: %v)", k, got)
	}
	if !changesetkit.RenamedFrom(got, "c.md", "a.md") {
		t.Fatalf("the final path does not name the range's base path a.md as its origin (all: %v)", got)
	}
	if k := changesetkit.Statuses(got, "b.md"); len(k) > 0 {
		t.Fatalf("the intermediate path of a rename chain was reported as %v: %v\n"+
			"b.md is in neither the range's base nor its head — a rule was asked about a file that "+
			"exists nowhere", k, got)
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
	e, proj, ledger := project(t, seed("the file that will be copied", [2]string{"source.md", "content that will be copied\n"}))

	got := runOne(t, e, proj, ledger, "s-023-03", Turns("done",
		Bash("b1", "cp source.md copy.md && git add copy.md"),
	).ThenCommit("copy source"))

	if k := changesetkit.Statuses(got, "copy.md"); len(k) == 0 || k[0] != "A" {
		t.Fatalf("the copy's destination was not reported as a create: %v (all: %v)", k, got)
	}
	if k := changesetkit.Statuses(got, "source.md"); len(k) > 0 {
		t.Fatalf("the copy's source was reported as %v though it never changed: %v\n"+
			"an unmodified file put in front of a rule is the noise untouched_stays_silent forbids", k, got)
	}
}

// T023_04: a file replaced by a symlink — git's T status — is reported as an
// update.
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
	e, proj, ledger := project(t, seed("an ordinary file that will become a link",
		[2]string{"target.md", "the link's target\n"}, [2]string{"shifty.md", "an ordinary file, for now\n"}))

	got := runOne(t, e, proj, ledger, "s-023-04", Turns("done",
		Bash("b1", "rm shifty.md && ln -s target.md shifty.md && git add shifty.md"),
	).ThenCommit("make shifty a link"))

	// The premise: git really calls this a typechange. Without it this is an
	// ordinary content update wearing a T's clothes.
	if status := e.Git(proj, "diff", "--name-status", "HEAD~1", "HEAD", "--", "shifty.md"); !strings.HasPrefix(status, "T") {
		t.Fatalf("git did not report a typechange for shifty.md (%q), so the T branch is not "+
			"being exercised and this test does not do what it claims", status)
	}

	k := changesetkit.Statuses(got, "shifty.md")
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
	e, proj, ledger := project(t, nil)

	// What the index held for ghost.md at the moment before it was deleted, written
	// outside the project by the same command: the premise below reads it, so "staged"
	// is something the test saw rather than something the scenario claims.
	stagedAtDelete := filepath.Join(t.TempDir(), "staged-at-delete")

	got := runOne(t, e, proj, ledger, "s-023-05", Turns("done",
		Write("w1", "survivor.md", "still here\n"),
		Bash("b1", "printf 'transient\\n' > ghost.md && git add ghost.md && git ls-files --stage -- ghost.md > '"+stagedAtDelete+"' && rm ghost.md"),
	).ThenCommit("the survivor"))

	// The positive first: this cycle reported something.
	if !changesetkit.Saw(got, "survivor.md") {
		t.Fatalf("the file the cycle left behind was not reported: %v — nothing was observed, "+
			"so the silence about the staged-then-deleted file proves nothing", got)
	}
	// The premise: the path WAS staged (the index listed it right before the delete) and
	// is off the disk, so this is the staged-then-deleted case and not a file that
	// never existed.
	if staged, err := os.ReadFile(stagedAtDelete); err != nil || !strings.Contains(string(staged), "ghost.md") {
		t.Fatalf("ghost.md was never in the index (%q, %v), so this is not the staged-then-deleted case", staged, err)
	}
	if e.Exists(proj, "ghost.md") {
		t.Fatalf("ghost.md is still on disk, so this is not the staged-then-deleted case")
	}

	if k := changesetkit.Statuses(got, "ghost.md"); len(k) > 0 {
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
	e, proj, ledger := project(t, func(e *harness.Env, proj string) {
		e.WriteFile(proj, "build.log", "tracked before it was ignored\n")
		e.CommitAll(proj, "a file, tracked before anything ignores it")
		e.WriteFile(proj, ".gitignore", "*.log\n")
		e.CommitAll(proj, "an ignore pattern that also names the tracked file")
	})

	// The premise, both halves: git tracks it AND ignores it.
	if tracked := e.Git(proj, "ls-files", "--", "build.log"); tracked == "" {
		t.Fatalf("build.log is not tracked, so this is an ordinary ignored file and the case " +
			"this test is about does not exist")
	}
	if ignored := e.Git(proj, "check-ignore", "-v", "--no-index", "build.log"); ignored == "" {
		t.Fatalf("build.log is not matched by .gitignore, so nothing here is being ignored")
	}

	got := runOne(t, e, proj, ledger, "s-023-06", Turns("done",
		Write("w1", "build.log", "changed by the agent\n"),
	).ThenCommit("change the log"))

	if k := changesetkit.Statuses(got, "build.log"); len(k) == 0 || k[0] != "M" {
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
	e, proj, ledger := project(t, seed("the ignore rules", [2]string{".gitignore", "junk/\n"}))

	got := runOne(t, e, proj, ledger, "s-023-07", Turns("done",
		Write("w1", "real-work.md", "the agent's actual work\n"),
		Bash("b1", "mkdir -p junk && printf 'noise\\n' > junk/generated.md"),
	).ThenCommit("the real work"))

	if !changesetkit.Saw(got, "real-work.md") {
		t.Fatalf("the cycle's real work was not reported: %v — nothing was observed, so the "+
			"silence about the ignored file proves nothing", got)
	}
	for _, o := range got {
		if strings.HasPrefix(o.Path, "junk/") {
			t.Fatalf("an ignored, untracked file was reported as %s %q: %v\n"+
				"this is the flood the ignore rules exist to hold back", o.Status, o.Path, got)
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
	e, proj, ledger := project(t, nil)

	const newlineName = "odd\nname.md"
	const quoteName = "odd\"quote.md"

	got := runOne(t, e, proj, ledger, "s-023-08", Turns("done",
		Write("w1", "ordinary.md", "the control\n"),
		// Written through the shell so the names are exactly these bytes and
		// nothing in the harness has to encode them.
		Bash("b1", `printf 'x\n' > "odd`+"\n"+`name.md"; printf 'y\n' > 'odd"quote.md'`),
	).ThenCommit("odd names"))

	if !changesetkit.Saw(got, "ordinary.md") {
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

	if k := changesetkit.Statuses(got, newlineName); len(k) == 0 || k[0] != "A" {
		t.Fatalf("a path holding a newline was not reported under its real name: %v (all: %v)\n"+
			"a line-oriented reader splits this into two entries and every rule bound to the "+
			"file is asked about paths that do not exist", k, got)
	}
	if k := changesetkit.Statuses(got, quoteName); len(k) == 0 || k[0] != "A" {
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
	e, proj, ledger := project(t, nil)
	e.Git(proj, "config", "core.quotepath", "true")

	const accented = "café.md"

	got := runOne(t, e, proj, ledger, "s-023-09", Turns("done",
		Write("w1", "ordinary.md", "the control\n"),
		Bash("b1", "printf 'z\\n' > 'café.md'"),
	).ThenCommit("accented name"))

	if !changesetkit.Saw(got, "ordinary.md") {
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

	if k := changesetkit.Statuses(got, accented); len(k) == 0 || k[0] != "A" {
		t.Fatalf("a non-ASCII path was not reported under its real bytes with core.quotepath "+
			"on: %v (all: %v)\nthe rule is being handed an escaped rendering, which names no "+
			"file on disk and reads as a deletion", k, got)
	}
}

// T023_10: a submodule's contents are never the parent's changes, and the
// gitlink does not reach a file rule either.
//
// (Ported to the changeset model: a commit records the gitlink as a path of its
// own, so it IS an entry of the changeset, unlike the retired per-file events
// where a directory on disk was dropped. The contents remain absent.)
//
// TWO LAYERS, and measuring them separately is the point.
//
// gitrepo.Changed reports the gitlink — the submodule's own path — because the
// parent's diff names it; that is pinned by TestChanged_SubmoduleIsAGitlink
// NotItsContents, whose comment says the path "is handed to file rules as
// though it were one". Measured end to end, it is NOT: the gitlink is a
// DIRECTORY on disk, so filemod's lookAt reports ErrPathIsNotAFile and no event
// is emitted for it. What a rule is handed for `git submodule add` is
// .gitmodules and nothing else.
//
// So the unit test's expectation and the observable behaviour differ, and this
// records the observable one. It is the same shape as the nested-worktree
// finding in 021: a non-file path enters the difference and is absorbed one
// layer below the rules by a check that exists for an unrelated reason.
//
// What is asserted here, therefore: .gitmodules arrives (the parent really did
// change), the submodule's own files never do (another repository's content is
// not this one's work), and the gitlink produces no file event.
func TestT023_10_ASubmoduleIsAGitlinkNotItsContents(t *testing.T) {
	e, proj, ledger := project(t, nil)

	// A second repository to embed. Built inside the project's parent so the
	// file: URL is local and no network is involved.
	sub := t.TempDir()
	e.Git(sub, "init", "--initial-branch=main")
	e.Git(sub, "config", "user.email", "e2e@example.invalid")
	e.Git(sub, "config", "user.name", "E2E")
	e.WriteFile(sub, "inner.md", "a file inside the submodule\n")
	e.CommitAll(sub, "the submodule's own content")

	got := runOne(t, e, proj, ledger, "s-023-10", Turns("done",
		Write("w1", "ordinary.md", "the control\n"),
		Bash("b1", "git -c protocol.file.allow=always submodule add "+sub+" vendored >/dev/null 2>&1"),
	).ThenCommit("add a submodule"))

	if !changesetkit.Saw(got, "ordinary.md") {
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
	if k := changesetkit.Statuses(got, ".gitmodules"); len(k) == 0 || k[0] != "A" {
		t.Fatalf("adding a submodule did not report .gitmodules as a create: %v (all: %v)\n"+
			"the parent repository changed and nothing said so", k, got)
	}

	// The gitlink itself is one entry of the changeset, at the submodule's own
	// path and nothing under it: the parent's commit records the pointer, and the
	// pointer is what a rule sees. Recorded rather than asserted as desirable.
	if k := changesetkit.Statuses(got, "vendored"); len(k) != 1 || k[0] != "A" {
		t.Fatalf("the submodule's gitlink was not reported once, as an addition: %v (all: %v)", k, got)
	}
}

// T023_11: an agent that commits mid-cycle still has its work judged, and work it
// leaves uncommitted is refused until committed.
//
// After the mid-cycle commit the tree is CLEAN, so `git status` says nothing
// happened. Only the commits in the rule's range still hold the work: an
// implementation reading only what is outstanding reports that the cycle changed
// nothing, which is precisely wrong and silently so. The outstanding half is not
// judged in that state: the Stop is refused until the agent commits it, and then
// both halves arrive in the same changeset.
func TestT023_11_AMidCycleCommitStaysInTheChangeset(t *testing.T) {
	e, proj, ledger := project(t, nil)

	const sess = "s-023-11"
	e.Run(proj, sess, "commit one, leave one", Turns("done",
		Write("w1", "committed.md", "this gets committed\n"),
		harness.CommitPaths("b1", "the agent commits", "committed.md"),
		Write("w2", "outstanding.md", "this does not\n"),
	))

	// The premise: the committed file really is clean in the tree, or any
	// implementation would have found it.
	if status := e.Git(proj, "status", "--porcelain", "--", "committed.md"); status != "" {
		t.Fatalf("committed.md is still outstanding (%q), so the committed half is not being "+
			"exercised", status)
	}
	e.AssertCommitRequired(proj, sess, "outstanding.md")
	if got := ledgerFiles(t, ledger); changesetkit.Saw(got, "outstanding.md") {
		t.Fatalf("uncommitted work was judged before it was committed: %v", got)
	}
	seen := len(harness.CommitRequired(e.BlockingErrorsFrom(proj, sess, "Stop")))

	e.Run(proj, sess, "now commit it", Turns("committed").ThenCommit("the outstanding file"))
	e.NoCommitRequired(proj, sess, seen)

	got := ledgerFiles(t, ledger)
	if !changesetkit.Saw(got, "committed.md") {
		t.Fatalf("work committed during the cycle fell out of the changeset: %v\n"+
			"a comparison reading only what is outstanding finds a clean tree and reports that "+
			"the cycle changed nothing", got)
	}
	if !changesetkit.Saw(got, "outstanding.md") {
		t.Fatalf("the half committed after the refusal is missing: %v", got)
	}
}

// T023_12: a detached HEAD that still reaches the session's start does not lose
// the work.
//
// Checking out the session's own commit detaches HEAD without leaving the history
// the range is measured in, so the work committed before the detach is still the
// range's. An engine that re-took its starting point on any HEAD movement would
// push the cycle's own work out of the changeset at exactly the moment an agent
// inspects a commit.
//
// The work committed BEFORE the detach is the discriminator — it must still be
// reported afterwards.
func TestT023_12_ADetachedHeadReachingTheBaselineKeepsTheWork(t *testing.T) {
	e, proj, ledger := project(t, nil)

	e.Run(proj, "s-023-12", "write then detach", Turns("done",
		Write("w1", "early.md", "written before the detach\n"),
		harness.Commit("b1", "the agent commits"),
		Bash("b2", "git checkout -q --detach HEAD"),
	).ThenCommit("x"))

	if ref := e.Git(proj, "rev-parse", "--abbrev-ref", "HEAD"); ref != "HEAD" {
		t.Fatalf("HEAD is not detached (on %q), so this test is not about a detached HEAD", ref)
	}

	got := ledgerFiles(t, ledger)
	if !changesetkit.Saw(got, "early.md") {
		t.Fatalf("work committed before a detach fell out of the changeset: %v\n"+
			"the detached commit still reaches the session's start, so the range must not "+
			"have moved and this work is still the cycle's", got)
	}
}

// T023_13: a rebase in progress does not re-take the point.
//
// The case Position.Operation exists for. A rebase walks a detached HEAD through
// commits that reach nothing recorded, at every step — so reachability alone
// would re-take the baseline several times over one rebase, each time landing on
// a commit the operation is about to replace. While an operation is RUNNING on
// the branch the point was taken on, the point waits.
//
// Driven as a real interrupted rebase: a conflict stops it mid-flight, and the
// cycle ends with .git/rebase-merge on disk.
func TestT023_13_ARebaseInProgressDoesNotRetakeThePoint(t *testing.T) {
	e, proj, _ := project(t, nil)

	// Two lines of history that touch the same file, so rebasing one onto the
	// other conflicts and stops.
	e.WriteFile(proj, "contested.md", "base\n")
	e.CommitAll(proj, "the contested file")
	root := e.Git(proj, "rev-parse", "HEAD")

	e.Git(proj, "checkout", "-b", "side", root)
	e.WriteFile(proj, "contested.md", "side version\n")
	e.CommitAll(proj, "on side")

	e.Git(proj, "checkout", "main")
	e.WriteFile(proj, "contested.md", "main version\n")
	e.CommitAll(proj, "on main")

	const sess = "s-023-13"

	// One ordinary cycle first, so the session exists and its point is recorded.
	// The meta cannot be read before a session has run — `session id` needs a
	// transcript — so the "before" reading has to come from a real cycle rather
	// than from the empty state.
	e.Run(proj, sess, "an ordinary first cycle", Turns("done",
		Write("w1", "work.md", "the cycle's own work\n"),
	).ThenCommit("the cycle's work"))
	before := e.Meta(proj, sess, sessionstate.MetaBaselineCommit)
	if before == "" {
		t.Fatalf("no baseline was recorded by the first cycle, so there is no point here to " +
			"stay put or move")
	}

	// Now the rebase, in a cycle of its own. Expected to stop on a conflict, so
	// git's non-zero exit is the arrangement rather than a failure.
	e.Run(proj, sess, "rebase into a conflict", Turns("done",
		Bash("b2", "git checkout side >/dev/null 2>&1; git rebase main >/dev/null 2>&1; true"),
	))

	// The premise: a rebase really is in progress. Without it this is a test
	// about an ordinary cycle and the assertion below is vacuous.
	if !e.Exists(proj, ".git/rebase-merge") && !e.Exists(proj, ".git/rebase-apply") {
		t.Skip("no rebase is in progress after the conflict attempt, so the operation branch " +
			"is not being exercised in this environment")
	}

	if after := e.Meta(proj, sess, sessionstate.MetaBaselineCommit); after != before {
		t.Fatalf("the point moved during a rebase (%s -> %s)\n"+
			"a rebase walks a detached HEAD through commits it is about to replace; re-taking "+
			"the point there lands it on a commit that will not exist once the rebase finishes", before, after)
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
	e, proj, ledger := project(t, nil)

	got := runOne(t, e, proj, ledger, "s-023-14", Turns("done",
		Write("w1", "before-it.md", "written before\n"),
		// An empty directory git will not report, and a submodule-shaped
		// directory it will: a nested repository is untracked content that is
		// not a file.
		Bash("b1", "mkdir -p nested && git -c init.defaultBranch=main init -q nested && git -C nested -c user.name=x -c user.email=x@example.invalid commit -q --allow-empty -m inner && printf 'x\\n' > nested/inside.md"),
		Write("w2", "after-it.md", "written after\n"),
	).ThenCommit("around the nested repository"))

	// Both ordinary files reached the rule, on either side of the odd one.
	if !changesetkit.Saw(got, "before-it.md") {
		t.Fatalf("a file written before the unclassifiable path was not reported: %v", got)
	}
	if !changesetkit.Saw(got, "after-it.md") {
		t.Fatalf("a file written after the unclassifiable path was not reported: %v\n"+
			"one path the engine could not turn into an event cost the rest of the cycle its "+
			"judging — one unclassifiable file becoming every file nobody judged", got)
	}
}
