package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T015_01: an isolated sub-agent's own cycle judges the work IT did, in the tree
// IT was bound to, in a tree the parent does not share.
//
// This is the test 014 says is unwritable. Its note records that an isolated
// sub-agent's tool calls "are never APPLIED", that the bound worktree is
// "EMPTY", and that a test asserting "the sub-agent's own file was judged" was
// written against that and removed rather than weakened.
//
// The measurement behind that was taken with the Write tool, which the mock
// really does not apply. It does apply Bash — and it applies it INSIDE the bound
// worktree, which is the fact that makes the whole scenario reachable. So the
// sub-agent here does its work with Bash, and everything the earlier note ruled
// out follows: there is a tree difference, the worktree's own checkout of
// .sloprail/guardrails makes the project's rule live in it, and the rule fires.
//
// The sub-agent's work is judged where it landed (`sr check run` from inside the
// worktree), and the root's own range never holds that file: the tree is genuinely
// separate, so a root that judged it would be judging another agent's work as its own.
func TestT015_01_AnIsolatedSubagentJudgesItsOwnWorkAsItself(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
	// A file-guard's `run` is a command the agent types, so it carries no identity of its
	// own; the identity a cycle ran as is seen where hooks run. This gate records, for each
	// command naming a markdown file, the session it was run as.
	idLed := e.NewLedger("identities")
	e.Gate(proj, "who", memoGate, map[string]string{"record.sh": memoScript(idLed.Path())})
	e.GitInit(proj)

	// Bash, not Write. The mock executes Bash and applies it in whatever tree the
	// agent is bound to; a Write in a sub-agent's scenario creates no file at
	// all, which is what made this look untestable.
	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo 'the sub-agent did this' > only-the-sub-made-this.md"),
	).ThenCommit("the sub-agent's work"))

	res := e.Run(proj, "s-015-01", "delegate into isolation", Turns("root done",
		Bash("rb1", "echo 'the root did this' > only-the-root-made-this.md"),
		Dispatch("d1", "do the delegated job", sub, "worktree"),
	).ThenCommit("the root's work"))

	if !res.Saw("root done") {
		t.Fatalf("the delegated cycle did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent was driven to the retry cap — a trapped cycle, not a judged one:\n%s",
			res.Output)
	}

	wt := theWorktree(t, proj)

	// The sub-agent's work really landed in ITS tree and nowhere else. Without
	// this the rest could be true of a sub-agent that was never isolated.
	if e.Exists(proj, "only-the-sub-made-this.md") {
		t.Fatalf("the sub-agent's file landed in the DISPATCHING session's tree, so the isolation " +
			"was not real and this test is about a shared tree under another name")
	}

	// (1) The sub-agent's own cycle judged the file the sub-agent made.
	e.CheckRunRange(filepath.Join(proj, ".claude", "worktrees", wt), "s-015-01", e.RunBase("s-015-01"), "HEAD")
	subLines := subLedger(t, proj, wt, "recorder", "log")
	if len(subLines) == 0 {
		t.Fatalf("nothing was judged at the isolated sub-agent's own cycle. Its work is in its "+
			"worktree and the project's rule is checked out there with it, so a cycle that judged "+
			"nothing means delegated work went unguarded — the exact hole subagent-stop exists to "+
			"close:\n%s", res.Output)
	}
	if _, ok := lineAbout(subLines, "only-the-sub-made-this.md"); !ok {
		t.Fatalf("the sub-agent's own file was not among what its cycle judged (%v) — a cycle "+
			"that ran but did not see the work it was run for", subLines)
	}

	rootLines := e.FileGuardLedgerLines(proj, "recorder", "log")
	if _, ok := lineAbout(rootLines, "only-the-root-made-this.md"); !ok {
		t.Fatalf("the root's own write was not judged (%v), so the check below would pass vacuously", rootLines)
	}

	// (1b) The FILE-GUARD itself ran as the sub-agent: it is handed the sub-agent's agent id,
	// and the root's own run is handed none.
	subJudged, _ := lineAbout(subLines, "only-the-sub-made-this.md")
	if agentOf(subJudged) == "" {
		t.Fatalf("the file-guard judged the sub-agent's file without the sub-agent's identity: %s", subJudged)
	}
	if rootJudged, _ := lineAbout(rootLines, "only-the-root-made-this.md"); agentOf(rootJudged) != "" {
		t.Fatalf("the root's file-guard run carried a sub-agent identity: %s", rootJudged)
	}

	// (2) The sub-agent's cycle ran as ITSELF. The identity is the whole invariant: an
	// engine routing the sub-agent's cycle through the parent's record would resolve the
	// parent's identity here, and every other assertion in this test would still hold.
	idLines := memoLines(t, idLed.Path())
	subLine, ok := lineAbout(idLines, "only-the-sub-made-this.md")
	if !ok {
		t.Fatalf("the sub-agent's own command was not seen by the gate (%v), so there is no identity "+
			"to compare", idLines)
	}
	rootLine, ok := lineAbout(idLines, "only-the-root-made-this.md")
	if !ok {
		t.Fatalf("the root's own command was not seen by the gate (%v), so there is no identity to "+
			"compare the sub-agent's against", idLines)
	}
	subID, rootID := sessionOf(subLine), sessionOf(rootLine)
	if subID == "" {
		t.Fatalf("the sub-agent's cycle recorded no session identity at all (%q)", subLine)
	}
	if subID == rootID {
		t.Fatalf("the sub-agent's cycle ran as the DISPATCHING session (%s). A sub-agent's cycle "+
			"judged against its parent's record hands every rule another agent's actions as though "+
			"this one had taken them.\n  sub-agent: %s\n  root:      %s", subID, subLine, rootLine)
	}

	// (3) And the reverse error: the root's cycle never saw the sub-agent's file.
	if containsPath(rootLines, "only-the-sub-made-this.md") {
		t.Fatalf("the DISPATCHING session's own cycle judged the sub-agent's file (%v). The two "+
			"are separate trees, so this is the parent being handed another session's work as its "+
			"own", rootLines)
	}
}

// T015_02: a sub-agent SHARING the dispatching session's tree is not judged
// separately — the dispatching session's own Stop judges its committed work.
//
// The shared tree is the ordinary path and the one where the two sessions are
// hardest to keep apart: one directory, one HEAD, one range of commits. A
// file-guard judges commits, and a sub-agent that shares the tree owns nothing of
// it: it commits into the very history the dispatching session's Stop judges, so
// judging the same range at its own stop as well would be one verdict twice, under
// an identity that owns none of the tree. So the sub-agent's own Stop judges
// nothing, and the root's judges its work — once, as itself.
// recordScriptOutsideRules is recordScript with its ledger at the root of the tree, outside the
// `.sloprail` whose hash keys every verdict.
const recordScriptOutsideRules = `#!/bin/sh
payload=$(cat)
for path in $(` + pathsOfPayload + `); do
  echo "judged path=[$path] session=[$SR_SESSION_ID]" >> "$SR_GUARDRAIL_DIR/../../../.recorder.log"
done
exit 0
`

func TestT015_02_ASharedTreeSubagentsWorkIsJudgedAtTheRootsStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScriptOutsideRules})
	// A judge beside the recorder: "judged exactly once" is a judge's count. The recorder keeps
	// its ledger outside `.sloprail`, because a verdict is keyed by a hash of everything under
	// it: a check writing there would change every key between one run and the next.
	e.FileGuard(proj, "verdict", "match: \"**/*.md\"\nchecks:\n  - judge: ./rubric.md.j2\n",
		map[string]string{"rubric.md.j2": "Does this change hold up?\n{{ change }}\n"})
	e.GitInit(proj)
	const promptFile = ".git/judge-prompt"
	e.InstallJudgeClaudeCapturing(proj, promptFile, `{"pass": true, "reasoning": "fine"}`)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		harness.CommitFile("sb1", "from-the-sub.md", "delegated\n", "the sub-agent's work"),
	))

	res := e.Run(proj, "s-015-02", "delegate in the same tree", Turns("root done",
		Dispatch("d1", "do it here", sub, ""),
	))

	if !res.Saw("root done") {
		t.Fatalf("the shared-tree delegated cycle did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent hit the retry cap:\n%s", res.Output)
	}

	// The tree really was shared, or this is T015_01 again.
	if trees := worktrees(t, proj); len(trees) != 0 {
		t.Fatalf("a worktree was bound (%v) for a dispatch that asked for none", trees)
	}
	if !e.Exists(proj, "from-the-sub.md") {
		t.Fatalf("the sub-agent's work never reached the shared tree, so nothing here is about a "+
			"cycle judging delegated work:\n%s", res.Output)
	}

	// The root's Stop judges the file the sub-agent committed.
	lines := e.FileGuardLedgerLines(proj, "recorder", "../../../.recorder.log")
	if !containsPath(lines, "from-the-sub.md") {
		t.Fatalf("the sub-agent's committed file was judged by nobody (%v):\n%s", lines, res.Output)
	}
	// And once: the sub-agent's own Stop did not judge a range it does not own, so the
	// range's one verdict is the root's. Judging it at the sub-agent's stop too would be
	// one verdict twice.
	if n := e.JudgeCalls(proj, promptFile, ""); n != 1 {
		t.Fatalf("the sub-agent's file was put to the judge %d times (want exactly 1: the root's):\n%s", n, res.Output)
	}
	if p := e.JudgePrompt(proj, promptFile); !strings.Contains(p, "from-the-sub.md") {
		t.Fatalf("the judge was not shown the sub-agent's file:\n%s", p)
	}
}

// T015_05: a sub-agent's own cycle judges every file it created, not just one.
//
// A cycle that judged exactly one thing could be an engine judging "the first
// difference it found" or a harness that applies one tool call. Several files in
// one delegated cycle separate "the cycle was judged" from "something fired
// once".
//
// It also pins the multi-turn shape: the sub-agent takes THREE turns, so the
// mock re-runs its script twice and the cycle spans more than a single action.
func TestT015_05_ASubagentsCycleJudgesEverythingItChanged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
	idLed := e.NewLedger("identities")
	e.Gate(proj, "who", memoGate, map[string]string{"record.sh": memoScript(idLed.Path())})
	e.GitInit(proj)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo one > sub-one.md"),
		Bash("sb2", "echo two > sub-two.md"),
		Bash("sb3", "echo three > sub-three.md"),
	).ThenCommit("the sub-agent's work"))

	res := e.Run(proj, "s-015-05", "delegate several turns", Turns("root done",
		Dispatch("d1", "do three things", sub, "worktree"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the multi-turn delegated cycle did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent hit the retry cap:\n%s", res.Output)
	}

	wt := theWorktree(t, proj)
	e.CheckRunRange(filepath.Join(proj, ".claude", "worktrees", wt), "s-015-05", e.RunBase("s-015-05"), "HEAD")
	lines := subLedger(t, proj, wt, "recorder", "log")
	for _, l := range lines {
		if agentOf(l) == "" || agentOf(l) != agentOf(lines[0]) {
			t.Fatalf("the file-guard did not judge everything as one sub-agent identity: %v", lines)
		}
	}
	for _, want := range []string{"sub-one.md", "sub-two.md", "sub-three.md"} {
		if !containsPath(lines, want) {
			t.Fatalf("the sub-agent's cycle did not judge %s. A cycle judging only some of what it "+
				"changed leaves the rest unguarded, and a sub-agent taking several turns is the "+
				"ordinary case rather than an unusual one. Ledger: %v", want, lines)
		}
	}

	// All of it under ONE identity — the sub-agent's own. Several turns must not
	// mean several sessions.
	var id string
	n := 0
	for _, l := range memoLines(t, idLed.Path()) {
		if !strings.Contains(l, "sub-") {
			continue
		}
		n++
		got := sessionOf(l)
		if id == "" {
			id = got
			continue
		}
		if got != id {
			t.Fatalf("one sub-agent's cycle ran under two different identities (%s and %s) — a "+
				"session that changes identity mid-cycle keeps its state in two places. Ledger: %v",
				id, got, memoLines(t, idLed.Path()))
		}
	}
	if n < 3 || strings.TrimSpace(id) == "" {
		t.Fatalf("the gate saw %d of the sub-agent's three commands with identity %q", n, id)
	}
}

// T015_03: what a sub-agent's guardrail remembers is not readable by the parent's, and
// what the parent's remembers is not readable by the sub-agent's.
//
// subagent_state_is_its_own, asserted end to end. Each invocation reports what it could
// READ in its own scope before writing its own note. Pooled state shows up as a sub-agent
// reading back the root's note, or the root's later hook reading back the sub-agent's —
// either direction is the defect, and both are checked.
//
// The root writes on BOTH sides of the dispatch, which is what makes the positive control
// part of the test rather than a separate one: the root's second hook must read back the
// root's FIRST note. Without that, "the root did not read the sub-agent's note" is equally
// true of a store that never worked at all, and the whole test would be vacuous.
// sr:proves subagents/own-session
func TestT015_03_ASubagentsStateDoesNotPoolWithItsParents(t *testing.T) {
	e := New(t)
	proj := e.Project()
	led := e.NewLedger("memo")
	e.Gate(proj, "memo", memoGate, map[string]string{"record.sh": memoScript(led.Path())})
	e.GitInit(proj)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo sub > the-subs-file.md"),
	).ThenCommit("the sub-agent's work"))

	res := e.Run(proj, "s-015-03", "remember across a delegation", Turns("root done",
		Bash("rb1", "echo root > root-before.md"),
		Dispatch("d1", "delegate", sub, "worktree"),
		Bash("rb2", "echo root > root-after.md"),
	))
	if !res.Saw("root done") {
		t.Fatalf("the session did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent hit the retry cap:\n%s", res.Output)
	}

	lines := memoLines(t, led.Path())
	// THE POSITIVE CONTROL. The root's second call read back the note its first stored.
	after, ok := lineAbout(lines, "root-after.md")
	if !ok {
		t.Fatalf("the root's write after the delegation was never judged (%v), so the control "+
			"this test rests on did not run", lines)
	}
	if got := beforeOf(after); !strings.Contains(got, "root-before.md") {
		t.Fatalf("the root's second call read back %q, not the note its first stored (which names "+
			"root-before.md). The session store is not working, so every assertion below about "+
			"state NOT crossing between sessions would pass against an engine that stores nothing "+
			"at all. Ledger: %v", got, lines)
	}

	// The sub-agent's own call ran and stored into a scope of its own.
	subLine, ok := lineAbout(lines, "the-subs-file.md")
	if !ok {
		t.Fatalf("the sub-agent's own call was never judged (%v), so there is no sub-agent state "+
			"to be isolated and this test proves nothing:\n%s", lines, res.Output)
	}
	if sessionOf(subLine) == sessionOf(after) {
		t.Fatalf("the sub-agent's call ran as the parent's session (%s)", sessionOf(after))
	}

	// Direction one: the sub-agent did not read the PARENT's note. The root had already
	// stored its first note by the time the sub-agent ran, so a pooled scope hands it that.
	if got := beforeOf(subLine); got != "" {
		t.Fatalf("the sub-agent's guardrail read back %q — a note the PARENT's hook wrote. Pooled "+
			"with the parent's, a sub-agent's state means a rule that remembers something "+
			"remembers it about work it was never watching. Ledger: %v", got, lines)
	}

	// Direction two: the root's later call did not read the SUB-AGENT's note.
	for _, l := range lines {
		if sessionOf(l) == sessionOf(after) && strings.Contains(beforeOf(l), "the-subs-file.md") {
			t.Fatalf("a guardrail in the PARENT's session read back the SUB-AGENT's note (%s). Ledger: %v", l, lines)
		}
	}
}

// T015_04: two sub-agents in one session do not read each other's state.
//
// The spec's own example of what pooling costs is "a session spawning ten sub-agents would
// have all ten reading each other's notes as their own". Two is the smallest case that
// exhibits it, and unlike the parent/child direction it cannot be explained away by a tree
// boundary: both sub-agents are the same KIND of thing, dispatched the same way, by the
// same session. Each writes a note keyed to its own work. Neither may see the other's.
// sr:proves subagents/own-session
func TestT015_04_TwoSubagentsDoNotReadEachOthersState(t *testing.T) {
	e := New(t)
	proj := e.Project()
	led := e.NewLedger("memo")
	e.Gate(proj, "memo", memoGate, map[string]string{"record.sh": memoScript(led.Path())})
	e.GitInit(proj)

	first := harness.SubagentScript(t, harness.Turns("one done", Bash("a1", "echo one > first-subs-file.md")).ThenCommit("the first sub-agent's work"))
	second := harness.SubagentScript(t, harness.Turns("two done", Bash("a2", "echo two > second-subs-file.md")).ThenCommit("the second sub-agent's work"))

	res := e.Run(proj, "s-015-04", "delegate twice", Turns("root done",
		Dispatch("d1", "first job", first, "worktree"),
		Dispatch("d2", "second job", second, "worktree"),
	))
	if !res.Saw("root done") {
		t.Fatalf("a session dispatching two sub-agents did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("a sub-agent hit the retry cap:\n%s", res.Output)
	}
	if trees := worktrees(t, proj); len(trees) != 2 {
		t.Fatalf("want a worktree per sub-agent, found %d (%v) — two sub-agents were never in play", len(trees), trees)
	}

	lines := memoLines(t, led.Path())
	firstLine, okFirst := lineAbout(lines, "first-subs-file.md")
	secondLine, okSecond := lineAbout(lines, "second-subs-file.md")
	if !okFirst || !okSecond {
		t.Fatalf("both sub-agents' own calls must have been judged for this to be about two "+
			"sub-agents' state; got %v", lines)
	}
	// Distinct identities, or there is only one session here and nothing to keep apart.
	if sessionOf(firstLine) == sessionOf(secondLine) {
		t.Fatalf("both sub-agents' calls ran under one identity (%s) — the two were collapsed into "+
			"a single session, under which their state cannot be separate however the store is keyed",
			sessionOf(firstLine))
	}
	// Neither read the other's note. Both scopes were fresh.
	for path, line := range map[string]string{"first-subs-file.md": firstLine, "second-subs-file.md": secondLine} {
		if got := beforeOf(line); got != "" {
			t.Fatalf("the sub-agent that ran %s read back %q — a note the OTHER sub-agent (or the "+
				"parent) wrote. Line: %s", path, got, line)
		}
	}
}
