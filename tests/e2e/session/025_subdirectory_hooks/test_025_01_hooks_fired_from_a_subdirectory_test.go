package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
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
// A consequence worth stating, because it decides how these tests are built: the
// engine loads guardrails from `<cwd>/.sloprail`, so a cycle reporting a
// subdirectory looks for its rules THERE. That is the engine's real behaviour
// rather than an artefact of the harness, and the rule is placed accordingly —
// in the subdirectory for the cycles that report it, with its own ledger.
//
// The transcript follows the reported directory too, so a cycle driven with Run
// and one driven with RunFrom are different CONVERSATIONS. Every test below
// therefore keeps all of its cycles on one reported directory. Crossing the two
// is what T025_03 tried to do, and the note where it used to sit records why
// that cannot be observed from outside.
//
// Every test asserts a POSITIVE before any absence: a differ that fell over on
// this arrangement reports nothing at all, and "no spurious delete arrived" is
// satisfied perfectly by that.

// recordEverything is a NEW-FORMAT file-guard that records every after-the-fact
// file event it is handed (re-vehicled from the old GUARDRAIL.md hooks per
// tests/e2e/REVEHICLE-PATTERN.md). It is installed in the SUBDIRECTORY the session
// reports, because the engine loads rules from `<cwd>/.sloprail` and every cycle
// here reports that subdirectory. `match: "**/*.md"` fires on whichever Post kind
// each change produced; every path this directory drives is `.md`, and — crucially
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

const recordScript = `#!/bin/sh
cat >> "$SR_GUARDRAIL_DIR/seen"
echo >> "$SR_GUARDRAIL_DIR/seen"
exit 0
`

type observed struct {
	Kind string
	Path string
}

// observedFiles decodes what a file-guard's check was handed — the FLAT event,
// whose fields spread directly under `event` (`.event.kind`, `.event.path`), not
// the old nested `event.fields` envelope.
func observedFiles(t *testing.T, lines []string) []observed {
	t.Helper()
	var got []observed
	for _, line := range lines {
		var p struct {
			Event struct {
				Kind string `json:"kind"`
				Path string `json:"path"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("the check was handed something that is not an event payload: %v\n%s", err, line)
		}
		got = append(got, observed{Kind: p.Event.Kind, Path: p.Event.Path})
	}
	return got
}

func kindsFor(got []observed, path string) []string {
	var out []string
	for _, o := range got {
		if o.Path == path {
			out = append(out, o.Kind)
		}
	}
	return out
}

func sawPath(got []observed, path string) bool { return len(kindsFor(got, path)) > 0 }

// subProject is a repository whose guardrail lives in a subdirectory two levels
// down, with the whole arrangement committed so none of it is any cycle's work.
//
// Two levels rather than one. One would separate the cwd from the root, but two
// is what distinguishes an engine that walks up a single step from one that asks
// git for the root — and `cd internal/foo` is the ordinary shape of the bug.
//
// Returns the repository root and the subdirectory the session reports. Callers
// read the ledger from the SUBDIRECTORY, because that is where the rule the
// cycle loaded actually lives.
func subProject(t *testing.T) (e *harness.Env, proj, sub string) {
	t.Helper()
	e = New(t)
	proj = e.Project()
	e.GitInit(proj)
	sub = filepath.Join(proj, "sub", "deep")
	e.FileGuard(sub, "watcher", recordEverything, map[string]string{"record.sh": recordScript})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")
	return e, proj, sub
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
	e, proj, sub := subProject(t)

	e.RunFrom(proj, "sub/deep", "s-025-01", "work from below", Turns("done",
		Bash("b1", "printf 'written from the subdirectory\n' > inner.md"),
	))

	// The premise: the file really landed inside the subdirectory. The mock runs
	// Bash turns in the directory it was given, and a test that assumed otherwise
	// would be asserting about a path at the top of the tree.
	if !e.Wrote(proj, "sub/deep/inner.md") {
		t.Fatalf("the cycle's file is not where the subdirectory session should have written " +
			"it, so this is not testing the arrangement it claims to")
	}

	got := observedFiles(t, e.FileGuardLedgerLines(sub, "watcher", "seen"))
	if !sawPath(got, "sub/deep/inner.md") {
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
// A rule bound to PostFileDelete is exactly the rule that must not be told this:
// "the agent removed a file" is the claim, and it is false.
//
// The assertion is on the KIND rather than on mere presence. An engine with this
// defect still reports the path — it reports it wrongly — so a test checking
// only that `top.md` arrived passes against the broken build.
func TestT025_02_AFileOutsideTheSubdirectoryIsNotReportedAsDeleted(t *testing.T) {
	e, proj, sub := subProject(t)

	// A file at the top of the tree, committed before the session so it is
	// unambiguously at the baseline. It is the one a cwd-rooted differ loses.
	e.WriteFile(proj, "top.md", "original\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "a file at the top of the tree")

	// The session reports the subdirectory and edits the file ABOVE it, which is
	// the ordinary thing an agent does after cd'ing somewhere to work.
	e.RunFrom(proj, "sub/deep", "s-025-02", "edit upwards", Turns("done",
		Bash("b1", "printf 'edited from below\n' > ../../top.md"),
	))

	// The premise: the file is still there and really was changed. Without this
	// the assertion below could pass on a cycle that deleted it for real.
	if !e.Wrote(proj, "top.md") {
		t.Fatalf("the file at the top of the tree is gone, so a reported deletion would be " +
			"correct and this test proves nothing")
	}
	if status := e.Git(proj, "status", "--porcelain", "--", "top.md"); status == "" {
		t.Fatalf("the file at the top of the tree was not modified, so there is nothing for " +
			"the cycle to report and its silence would be correct")
	}

	got := observedFiles(t, e.FileGuardLedgerLines(sub, "watcher", "seen"))
	k := kindsFor(got, "top.md")
	if len(k) == 0 {
		t.Fatalf("a file modified outside the reported subdirectory was not reported at all: %v — "+
			"the difference is rooted below it, so everything above went unjudged", got)
	}
	for _, kind := range k {
		if kind == "PostFileDelete" {
			t.Fatalf("a file that is still on disk was dispatched as a DELETION (%v) — the "+
				"difference was rooted at the reported subdirectory rather than at the "+
				"repository, so a repository-relative path did not resolve, stat'd as absent, "+
				"and classified as removed; every rule about deleted files is now being told "+
				"the agent removed a file it merely edited", k)
		}
	}
	if k[0] != "PostFileUpdate" {
		t.Fatalf("a file present at the baseline and edited during the cycle arrived as %v; "+
			"want PostFileUpdate", k)
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
// anchor being right — the paths (T025_01, T025_02), the verdict record
// (T025_04) and the retained refusal (T025_05) — each of which does discriminate,
// and each of which is mutation-checked.

// T025_04: a verdict recorded from the root exempts the same content from a
// subdirectory cycle.
//
// The anchor's other consequence, and the one a person actually notices. The
// revalidation record lives in the session's store, so an engine keying on the
// raw cwd does not merely lose the baseline — it loses every verdict, and the
// rule is asked again about content it has already passed. On a judge hook that
// is a fresh model call, free to come back with a different answer about work
// the agent has moved on from.
//
// The shape: pass a file in cycle one from the root; touch something else in
// cycle two from the subdirectory. The settled file must NOT be re-judged. The
// control is the second cycle's own file, which must be — otherwise the silence
// is an engine that dispatched nothing from down there.
//
// One rule, in the subdirectory, loaded by BOTH cycles. The root cycle is driven
// with RunFrom too — reporting the subdirectory — so that the guardrail and its
// single ledger are the same in both, and the only thing under test is whether
// the store follows. The two cycles being in one conversation is what the
// transcript's root keying provides.
func TestT025_04_AVerdictRecordedEarlierHoldsForASubdirectoryCycle(t *testing.T) {
	e, proj, sub := subProject(t)

	const sess = "s-025-04"

	e.RunFrom(proj, "sub/deep", sess, "settle a file", Turns("done",
		Write("w1", "settled.md", "judged and passed\n"),
	))
	first := e.FileGuardLedgerLines(sub, "watcher", "seen")
	if len(kindsFor(observedFiles(t, first), "sub/deep/settled.md")) == 0 {
		t.Fatalf("the file was never judged in the first cycle (%v), so there is no verdict for "+
			"the second cycle to inherit and the skip below would hold for the wrong reason",
			observedFiles(t, first))
	}

	e.RunFrom(proj, "sub/deep", sess, "work elsewhere from below", Turns("done",
		Write("w2", "other.md", "cycle two\n"),
	))

	all := e.FileGuardLedgerLines(sub, "watcher", "seen")
	if len(all) <= len(first) {
		t.Fatalf("the second cycle dispatched nothing at all (%d lines, was %d) — a session "+
			"whose hooks report a subdirectory judged nothing", len(all), len(first))
	}
	second := observedFiles(t, all[len(first):])

	// The control: this cycle's own work reached the rule.
	if !sawPath(second, "sub/deep/other.md") {
		t.Fatalf("the second cycle's own file never reached the rule: %v — nothing was "+
			"dispatched, so the exemption asserted below is vacuous", second)
	}
	if k := kindsFor(second, "sub/deep/settled.md"); len(k) > 0 {
		t.Fatalf("content already judged and passed was put in front of the rule again (%v): %v\n"+
			"the verdict was recorded in one database and looked for in another, so the session "+
			"re-judges everything it had settled — and a judge hook is a model call, free to "+
			"answer differently about work the agent has moved on from", k, second)
	}
}

// T025_05: a refusal recorded earlier still refuses in a subdirectory cycle.
//
// The direction that loses ENFORCEMENT rather than merely repeating work, which
// makes it the more serious half of T025_04. A refusal lives in the same record
// as a pass, so an engine that cannot find the record finds no refusal either —
// and `refusal_outlives_baseline` quietly stops holding. The file stays broken
// with nothing left to say so.
//
// The second cycle does not touch the offending file, so nothing but the
// RETAINED refusal — read back by readdOutstanding, out of the session's store —
// can put it in front of the rule again.
func TestT025_05_ARefusalStillRefusesInASubdirectoryCycle(t *testing.T) {
	e := New(t)
	// This test's Stop block is PERMANENT: the retained refusal for bad-file.md
	// never clears across the two cycles, so the mock re-runs the agent to its
	// blocked-Stop cap every time, and each re-run re-fires the (subdirectory,
	// tree-diffing) Stop hook — with the default cap of 8 that is ~56s of wasted
	// retries for a fact one re-run already establishes. The assertions read the
	// RETAINED refusal out of the store (BlockingErrors), not the count of
	// re-prompts, so one re-run is enough. Cap it at 1.
	e.SetStopBlockCap(1)
	proj := e.Project()
	e.GitInit(proj)
	sub := filepath.Join(proj, "sub", "deep")

	// Refuses any path containing "bad", and records everything it is handed
	// BEFORE deciding — so arrival is observable independently of the verdict.
	// A NEW-FORMAT file-guard, after-check: it observes at Stop and RE-FIRES next
	// cycle, which is exactly where a retained refusal is measured. `match: "**/*.md"`
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
	// works unchanged. The ledger is $SR_GUARDRAIL_DIR/seen, the folder the engine
	// sets for the check.
	const judgeScript = `#!/bin/sh
payload="$(cat)"
printf '%s\n' "$payload" >> "$SR_GUARDRAIL_DIR/seen"
case "$payload" in
  *bad*) echo '{"reason":"this file is not acceptable"}'; exit 1 ;;
esac
exit 0
`
	e.FileGuard(sub, "watcher", refuseNamed, map[string]string{"judge.sh": judgeScript})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	const sess = "s-025-05"

	e.RunFrom(proj, "sub/deep", sess, "write a bad file", Turns("done",
		Write("w1", "bad-file.md", "violates\n"),
	))
	first := e.FileGuardLedgerLines(sub, "watcher", "seen")
	if len(kindsFor(observedFiles(t, first), "sub/deep/bad-file.md")) == 0 {
		t.Fatalf("the offending file never reached the rule in the first cycle (%v), so there "+
			"is no refusal on record and nothing for the second cycle to carry",
			observedFiles(t, first))
	}
	// The premise: it really was REFUSED, not merely seen. A pass would leave
	// nothing outstanding, and the second cycle's silence would be correct.
	//
	// Read against the SUBDIRECTORY, because that is where this session's record
	// lives: a harness keys a conversation's transcript by the directory the
	// session reports, and every cycle here reports the subdirectory. Asking at
	// the repository root reads a file this session never wrote, finds no
	// blocking attachments, and reports "not refused" for a cycle that was.
	if blocking := e.BlockingErrors(sub, sess); len(blocking) == 0 ||
		!strings.Contains(strings.Join(blocking, "\n"), "not acceptable") {
		t.Fatalf("the first cycle was not refused (blocking: %v), so there is no retained "+
			"refusal under test here", blocking)
	}

	// A second cycle that does NOT touch the bad file. Only the retained refusal
	// can bring it back.
	e.RunFrom(proj, "sub/deep", sess, "work elsewhere from below", Turns("done",
		Write("w2", "fine.md", "acceptable\n"),
	))

	all := e.FileGuardLedgerLines(sub, "watcher", "seen")
	if len(all) <= len(first) {
		t.Fatalf("the second cycle dispatched nothing at all (%d lines, was %d)", len(all), len(first))
	}
	second := observedFiles(t, all[len(first):])

	// The control: the cycle is live and dispatching from down there.
	if !sawPath(second, "sub/deep/fine.md") {
		t.Fatalf("the second cycle's own file never reached the rule: %v — nothing was "+
			"dispatched, so the absence below proves nothing", second)
	}
	if len(kindsFor(second, "sub/deep/bad-file.md")) == 0 {
		t.Fatalf("an unfixed refusal was dropped in a cycle reporting a subdirectory: %v\n"+
			"the refusal is recorded in the session's store, and the store was looked for in "+
			"another place — so the file is still broken and nothing is left to report it", second)
	}
}
