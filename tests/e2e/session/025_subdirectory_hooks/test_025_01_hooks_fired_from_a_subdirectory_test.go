package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
	"github.com/sloprail/sloprail/tests/e2e/session/changesetkit"
)

// A session whose hooks fire from a SUBDIRECTORY of the repository, end to end.
//
// This was a real defect and it is the reason this directory exists. Everything
// that identifies a session is derived from the directory a hook reports, and
// the engine used to key on the RAW cwd. A hook reporting a subdirectory
// therefore opened a DIFFERENT database: an empty baseline, no read mark, and
// every verdict the session had recorded suddenly unreachable — silently, and
// in the middle of a session. Nothing about it is specific to sub-agents; it is
// the plain case of an agent working under `cd internal/foo`.
//
// The fix anchors the workspace on the GIT ROOT (services/sr-session/statedir.go,
// workspaceAnchor), so two hooks demonstrably in one tree agree about where its
// state lives. There is a second, independent half in the same shape: the
// DIFFERENCE is rooted at the repository too, because every path git reports is
// repository-relative. Rooted at the subdirectory instead, a modified file
// outside it does not resolve, stats as absent, and is classified as a DELETE —
// so a cycle dispatches deletions for files that are still on disk.
//
// Both halves are covered at the unit level — subagent_test.go for the anchor,
// observed_test.go's TestTreeDifference_FromASubdirectoryStillRootsAtThe
// Repository for the paths. NEITHER was covered end to end, and the unit tests
// cannot be: they call the engine's own functions with a directory of their
// choosing, which proves the functions agree while proving nothing about what a
// hook fired by a real harness is handed. That is the gap here.
//
// HOW THE ARRANGEMENT IS REACHED, measured rather than assumed. The mock reports
// its `--project-dir` as `cwd` on every hook payload and runs the agent's Bash
// turns there; the process working directory is ignored for both. A harness that
// moved only cmd.Dir would change nothing the engine can see. So harness.RunFrom
// passes the subdirectory AS the project dir, which is exactly the shape of the
// defect: a payload whose `cwd` sits below the repository root. See RunFrom.
//
// FILE-GUARDS ARE JUDGED BY `sr check run --base --head`, not by the session's
// Stop, and the command loads the rules of the repository it is run in — from the
// git root, wherever below it the caller stands. So the rule here lives at the
// repository root, and each test runs `sr check run` from the SUBDIRECTORY the
// session reported (judgeFromBelow): the subdirectory arrangement now reaches a
// file-guard through its caller's cwd rather than through a hook payload.
//
// Every test asserts a POSITIVE before any absence: a differ that fell over on
// this arrangement reports nothing at all, and "no spurious delete arrived" is
// satisfied perfectly by that.

// recordEverything is a file-guard that records every Changeset it is handed. `match: "**/*.md"` fires on every committed change; every path this directory drives is `.md`, and — crucially
// for this suite — the paths arrive REPOSITORY-relative (`sub/deep/inner.md`,
// `top.md`), which the `**/` optional-leading-directory glob matches at the root
// and at any depth alike. The ledger (`seen`, no `.md`) is not matched, so the
// guard cannot re-observe its own bookkeeping.
const recordEverything = `match: "**/*.md"
# deletions: include — this guard observes EVERY change, and a file-guard
# skips deleted files unless it says so.
deletions: include
checks:
  - script: ./record.sh
`

// subProject is a repository whose guardrail lives in a subdirectory two levels
// down, with the whole arrangement committed so none of it is any cycle's work.
//
// Two levels rather than one. One would separate the cwd from the root, but two
// is what distinguishes an engine that walks up a single step from one that asks
// git for the root — and `cd internal/foo` is the ordinary shape of the bug.
//
// Returns the repository root and the subdirectory the session reports.
func subProject(t *testing.T) (e *harness.Env, proj, sub, ledger string) {
	t.Helper()
	e = New(t)
	proj = e.Project()
	e.GitInit(proj)
	sub = filepath.Join(proj, "sub", "deep")
	e.WriteFile(sub, ".keep", "")
	ledger = filepath.Join(t.TempDir(), "seen")
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": harness.RecordScript(ledger)})
	e.CommitAll(proj, "the project before the session")
	return e, proj, sub, ledger
}

// judgeFromBelow runs `sr check run` over the session's range from the
// subdirectory, and returns its refusals.
func judgeFromBelow(e *harness.Env, sub, sess string) []string {
	return e.CheckRunRange(sub, sess, e.RunBase(sess), "HEAD")
}

// T025_01: a cycle reporting a subdirectory reports its work at all, and names
// it from the REPOSITORY root.
//
// The control for everything below, and not a formality. If the engine cannot
// identify a session whose hook reports a subdirectory, it opens no store,
// records no baseline, and dispatches nothing — and every absence the other
// tests assert would hold for that reason rather than for the right one.
//
// The path is the real assertion. Every path in the difference is
// repository-relative, because that is what git reports and what the consumer
// joins onto the repository root to decide whether a file still exists. A bare
// "inner.md" here would mean the paths are cwd-relative, and the same joining
// would resolve nothing.
func TestT025_01_ACycleFromASubdirectoryReportsItsWork(t *testing.T) {
	e, proj, sub, ledger := subProject(t)

	e.RunFrom(proj, "sub/deep", "s-025-01", "work from below", Turns("done",
		Bash("b1", "printf 'written from the subdirectory\n' > inner.md"),
	).ThenCommit("the agent's work"))
	judgeFromBelow(e, sub, "s-025-01")

	// The premise: the file really landed inside the subdirectory. The mock runs
	// Bash turns in the directory it was given, and a test that assumed otherwise
	// would be asserting about a path at the top of the tree.
	if !e.Wrote(proj, "sub/deep/inner.md") {
		t.Fatalf("the cycle's file is not where the subdirectory session should have written " +
			"it, so this is not testing the arrangement it claims to")
	}

	got := changesetkit.Files(t, harness.ReadLedgerLines(t, ledger))
	if !changesetkit.Saw(got, "sub/deep/inner.md") {
		t.Fatalf("a file written by a cycle reporting a subdirectory was not reported under its "+
			"repository-relative path: %v — either the session could not be identified from "+
			"there (no store, no baseline, nothing dispatched) or the paths are relative to the "+
			"cwd, in which case nothing downstream resolves them", got)
	}
}

// T025_02: a file modified OUTSIDE the subdirectory is an update, not a delete.
//
// The invariant this directory is really about, and the failure is the nastiest
// kind: not silence, but a confident wrong answer. Every path git reports is
// repository-relative. Resolve them against the SUBDIRECTORY and `top.md` does
// not exist there, so it stats as absent — and a path that was at the baseline
// and is absent now classifies as a DELETE. The cycle then dispatches a deletion
// for a file sitting on disk, unchanged in every way except that the session
// reported a directory below it.
//
// A rule bound to D is exactly the rule that must not be told this:
// "the agent removed a file" is the claim, and it is false.
//
// The assertion is on the KIND rather than on mere presence. An engine with this
// defect still reports the path — it reports it wrongly — so a test checking
// only that `top.md` arrived passes against the broken build.
func TestT025_02_AFileOutsideTheSubdirectoryIsNotReportedAsDeleted(t *testing.T) {
	// A file at the top of the tree, committed before the session: it is
	// unambiguously at the range's base. It is the one a cwd-rooted differ loses.
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "top.md", "original\n")
	e.CommitAll(proj, "a file at the top of the tree")
	sub := filepath.Join(proj, "sub", "deep")
	e.WriteFile(sub, ".keep", "")
	ledger := filepath.Join(t.TempDir(), "seen")
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"record.sh": harness.RecordScript(ledger)})
	e.CommitAll(proj, "the project before the session")

	// The session reports the subdirectory and edits the file ABOVE it, which is
	// the ordinary thing an agent does after cd'ing somewhere to work.
	e.RunFrom(proj, "sub/deep", "s-025-02", "edit upwards", Turns("done",
		Bash("b1", "printf 'edited from below\n' > ../../top.md"),
	).ThenCommit("the agent's work"))
	judgeFromBelow(e, sub, "s-025-02")

	// The premise: the file is still there and really was changed. Without this
	// the assertion below could pass on a cycle that deleted it for real.
	if !e.Wrote(proj, "top.md") {
		t.Fatalf("the file at the top of the tree is gone, so a reported deletion would be " +
			"correct and this test proves nothing")
	}
	if changed := e.Git(proj, "diff", "--name-only", "HEAD~1", "HEAD", "--", "top.md"); changed == "" {
		t.Fatalf("the file at the top of the tree was not modified, so there is nothing for " +
			"the cycle to report and its silence would be correct")
	}

	got := changesetkit.Files(t, harness.ReadLedgerLines(t, ledger))
	k := changesetkit.Statuses(got, "top.md")
	if len(k) == 0 {
		t.Fatalf("a file modified outside the reported subdirectory was not reported at all: %v — "+
			"the difference is rooted below it, so everything above went unjudged", got)
	}
	for _, kind := range k {
		if kind == "D" {
			t.Fatalf("a file that is still on disk was dispatched as a DELETION (%v) — the "+
				"difference was rooted at the reported subdirectory rather than at the "+
				"repository, so a repository-relative path did not resolve, stat'd as absent, "+
				"and classified as removed; every rule about deleted files is now being told "+
				"the agent removed a file it merely edited", k)
		}
	}
	if k[0] != "M" {
		t.Fatalf("a file present at the baseline and edited during the cycle arrived as %v; "+
			"want M", k)
	}
}

// T025_03 IS DELETED, AND THIS IS THE RECORD OF WHY.
//
// It asserted the workspace ANCHOR end to end: that a cycle reporting the
// repository root and a cycle reporting a subdirectory resolve to one session
// state, so a file settled by the first is not re-judged by the second. That is
// a real invariant — it is what workspaceAnchor exists for — but it cannot be
// observed through this harness, and a test that cannot fail is worse than no
// test. It was written, proven unable to discriminate, and removed rather than
// weakened into something green.
//
// WHAT WAS MEASURED, in order, because each step ruled out a different repair:
//
//  1. Reading the recorded baseline back through harness.Meta proves nothing.
//     Meta resolves the database by the same rule the engine does, so an engine
//     keying on the raw cwd and a test reading by the raw cwd agree, and the
//     comparison passes. Confirmed by disabling workspaceAnchor's git-root
//     branch: the version comparing two baselines stayed green.
//
//  2. Rewritten to assert the CONSEQUENCE instead — cycle one settles a file
//     while reporting the root, cycle two reports the subdirectory and must not
//     be handed it again — it stayed green under the same mutant. The mutant
//     genuinely splits the state: with the git-root branch disabled, two
//     databases appear on disk, `…-slop-proj-N/` and `…-slop-proj-N-sub-deep/`,
//     under one conversation id. So the anchor was broken and the test did not
//     notice.
//
//  3. The reason is the BASELINE, not the verdict. A second cycle that opens an
//     empty store has no recorded point, so it takes a fresh one — and that
//     point sits after cycle one's work. `settled.md` is therefore not a
//     difference at all for the second cycle, and never reaches the dispatcher
//     where the exemption is applied. The ledger is identical under the correct
//     anchor and under the mutant: `[sub/deep/.claude/settings.json,
//     sub/deep/other.md]` both times. The file's absence is the diff's doing,
//     and it masks whatever the store did.
//
// So the two mechanisms cannot be separated from outside in this arrangement:
// breaking the anchor also moves the baseline, and the moved baseline hides the
// re-judging that would have been the evidence. Any observation reaching past
// that would have to read the engine's own state through a mapping the test
// derives itself — which is step 1, and agrees with a broken engine by
// construction.
//
// WHAT COVERS IT INSTEAD. The anchor is pinned where it can be seen: the unit
// test at services/sr-session/subagent_test.go:333 asserts that a hook fired
// from a subdirectory does NOT key a different database, naming that exact
// failure. What this directory covers end to end is everything downstream of the
// anchor being right — the paths (T025_01, T025_02) and the refusal (T025_05).

// T025_04: a verdict recorded from the repository root holds for the same range judged
// from a subdirectory, and the other way round.
//
// The anchor's other consequence, and the one a person actually notices. The results
// are keyed by the repository, so a command run from `sub/deep` that opened a store of
// its own would lose every verdict and ask the judge again about a range it has already
// passed — on a judge that is a fresh model call, free to come back with a different
// answer about work the agent has moved on from. (A script check runs every time by
// design, so this is a judge's property.)
//
// The shape: judge the session's range from the subdirectory (the judge is asked); judge
// it again from the subdirectory and then from the root: nothing is asked. The control
// is that the first run did ask, with the committed file in front of it.
func TestT025_04_AVerdictRecordedEarlierHoldsForASubdirectoryCycle(t *testing.T) {
	e, proj, sub, _ := subProject(t)
	e.FileGuard(proj, "verdict", "match: \"**/*.md\"\nchecks:\n  - judge: ./rubric.md.j2\n",
		map[string]string{"rubric.md.j2": "Does this change hold up?\n{{ change }}\n"})
	e.CommitAll(proj, "the judged rule")
	const promptFile = ".git/judge-prompt"
	e.InstallJudgeClaudeCapturing(proj, promptFile, `{"pass": true, "reasoning": "fine"}`)

	const sess = "s-025-04"
	e.RunFrom(proj, "sub/deep", sess, "settle a file", Turns("done",
		Write("w1", "settled.md", "judged and passed\n"),
	).ThenCommit("the agent's work"))
	if refusals := judgeFromBelow(e, sub, sess); len(refusals) != 0 {
		t.Fatalf("the judge refused a passing verdict: %v", refusals)
	}
	asked := e.JudgeCalls(proj, promptFile, "")
	if asked == 0 {
		t.Fatalf("the judge was never asked about the file, so there is no verdict for the " +
			"re-run below to inherit and its silence would hold for the wrong reason")
	}
	if p := e.JudgePrompt(proj, promptFile); !strings.Contains(p, "judged and passed") {
		t.Fatalf("the judge was not shown the file:\n%s", p)
	}

	judgeFromBelow(e, sub, sess)
	if n := e.JudgeCalls(proj, promptFile, ""); n != asked {
		t.Fatalf("a verdict recorded from the subdirectory was not found by the same range judged "+
			"from the subdirectory again (%d judge calls, was %d)", n, asked)
	}
	e.CheckRunRaw(proj, sess, e.RunBase(sess), "HEAD")
	if n := e.JudgeCalls(proj, promptFile, ""); n != asked {
		t.Fatalf("a verdict recorded from the subdirectory was not found from the repository root "+
			"(%d judge calls, was %d): the results are not keyed by the repository", n, asked)
	}
}

// T025_05: an unfixed violation still refuses when judged from a subdirectory,
// after a later cycle that did not touch it.
//
// The direction that loses ENFORCEMENT. The second cycle does not touch the
// offending file, so only the range — the session's commits, judged from below —
// can put it in front of the rule again.
func TestT025_05_ARefusalStillRefusesInASubdirectoryCycle(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	sub := filepath.Join(proj, "sub", "deep")

	// Refuses any path containing "bad", and records everything it is handed
	// BEFORE deciding — so arrival is observable independently of the verdict.
	// `match: "**/*.md"`
	// selects the repository-relative markdown paths the cycle produces
	// (`sub/deep/bad-file.md`) at the root or any depth; the ledger (`seen`) has no
	// `.md` suffix and is not matched, so the guard cannot re-observe it.
	const refuseNamed = `match: "**/*.md"
checks:
  - script: ./judge.sh
`
	// New-format refusal contract: exit non-zero refuses and a `{"reason":…}` on
	// stdout is the reason the agent is told, replacing the old exit-2-with-stderr.
	// The whole flat payload still carries `"path":"…bad…"`, so a `*bad*` match on it
	// works unchanged. The ledger is a file outside the project (LEDGER below).
	const judgeTemplate = `#!/bin/sh
payload="$(cat)"
printf '%s\n' "$payload" >> "LEDGER"
case "$payload" in
  *bad*) echo '{"reason":"this file is not acceptable"}'; exit 1 ;;
esac
exit 0
`
	ledger := filepath.Join(t.TempDir(), "seen")
	judgeScript := strings.Replace(judgeTemplate, "LEDGER", ledger, 1)
	e.WriteFile(sub, ".keep", "")
	e.FileGuard(proj, "watcher", refuseNamed, map[string]string{"judge.sh": judgeScript})
	e.CommitAll(proj, "the project before the session")

	const sess = "s-025-05"

	e.RunFrom(proj, "sub/deep", sess, "write a bad file", Turns("done",
		Write("w1", "bad-file.md", "violates\n"),
	).ThenCommit("the agent's work"))
	refusals := judgeFromBelow(e, sub, sess)
	first := harness.ReadLedgerLines(t, ledger)
	if len(changesetkit.Statuses(changesetkit.Files(t, first), "sub/deep/bad-file.md")) == 0 {
		t.Fatalf("the offending file never reached the rule in the first cycle (%v), so there "+
			"is no refusal on record and nothing for the second cycle to carry",
			changesetkit.Files(t, first))
	}
	// The premise: it really was REFUSED, not merely seen. A pass would leave
	// nothing outstanding, and the second cycle's silence would be correct.
	if len(refusals) == 0 || !strings.Contains(strings.Join(refusals, "\n"), "not acceptable") {
		t.Fatalf("the first cycle was not refused (refusals: %v), so there is no unfixed "+
			"violation under test here", refusals)
	}

	// A second cycle that does NOT touch the bad file. Only the retained refusal
	// can bring it back.
	e.RunFrom(proj, "sub/deep", sess, "work elsewhere from below", Turns("done",
		Write("w2", "fine.md", "acceptable\n"),
	).ThenCommit("the agent's work"))
	if again := judgeFromBelow(e, sub, sess); len(again) == 0 {
		t.Fatalf("the range still holds the unfixed bad file and was not refused")
	}

	all := harness.ReadLedgerLines(t, ledger)
	if len(all) <= len(first) {
		t.Fatalf("the second cycle dispatched nothing at all (%d lines, was %d)", len(all), len(first))
	}
	second := changesetkit.Files(t, all[len(first):])

	// The control: the cycle is live and dispatching from down there.
	if !changesetkit.Saw(second, "sub/deep/fine.md") {
		t.Fatalf("the second cycle's own file never reached the rule: %v — nothing was "+
			"dispatched, so the absence below proves nothing", second)
	}
	if len(changesetkit.Statuses(second, "sub/deep/bad-file.md")) == 0 {
		t.Fatalf("an unfixed violation was dropped when judged from a subdirectory: %v\n"+
			"the file is still broken and nothing is left to report it", second)
	}
}

// T025_06: the Stop of a session whose hooks report a subdirectory verifies the
// session's range, found from below the repository root.
//
// The part of the subdirectory arrangement a file-guard's `run` cannot reach: the Stop
// is a hook, its payload names the subdirectory, and the tracked range, the stored
// verdicts and the registry all have to be found from there. The Stop reports failures
// only: unjudged, the range passes; once judged from below and refused, the same Stop
// refuses it with the stored reason.
func TestT025_06_AStopFromASubdirectoryVerifiesTheSessionsRange(t *testing.T) {
	e, proj, sub, _ := subProject(t)
	const sess = "s-025-06"
	e.FileGuard(proj, "deny", "match: \"**/inner.md\"\nchecks:\n  - script: ./deny.sh\n",
		map[string]string{"deny.sh": "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"inner.md is denied from below\"}'\nexit 1\n"})
	e.CommitAll(proj, "a rule that refuses inner.md")

	e.RunFrom(proj, "sub/deep", sess, "work from below", Turns("done",
		Bash("b1", "printf 'written from the subdirectory\n' > inner.md"),
	).ThenCommit("the agent's work"))

	// Nothing has judged the range (the harness's own pre-Stop run is off here): the Stop
	// has no failure to report.
	if res := e.StopNow(sub, sess, false); harness.Blocked(res) {
		t.Fatalf("a Stop from a subdirectory refused a range nobody had judged:\n%s", res.Output)
	}

	// Judged from below, the Stop's verify finds the stored FAIL.
	judgeFromBelow(e, sub, sess)
	res := e.StopNow(sub, sess, false)
	if !harness.Blocked(res) || !strings.Contains(res.Output, "inner.md is denied from below") {
		t.Fatalf("a Stop from a subdirectory did not refuse a range with a stored FAIL "+
			"— it never found the session's tracked range from below the root:\n%s", res.Output)
	}
}
