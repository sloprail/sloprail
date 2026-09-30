package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T015_01: an isolated sub-agent's own cycle judges the work IT did, in the tree
// IT was bound to, under an identity that is not the parent's.
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
// Three things are asserted, and each fails on its own kind of engine defect:
//
//  1. The sub-agent's file was judged AT ALL. Fails against an engine whose
//     subagent-stop judges nothing — the stub this feature shipped with.
//  2. It was judged under an identity that is NOT the dispatching session's.
//     This is judged_on_its_own_record with teeth: an engine routing the
//     sub-agent's cycle through the parent's record resolves the parent's
//     identity, and only this assertion separates the two.
//  3. The root's own cycle never saw that file. The tree is genuinely separate,
//     so a root that judged it would be judging another session's work as its
//     own — the reverse error, and the one nobody checks for.
func TestT015_01_AnIsolatedSubagentJudgesItsOwnWorkAsItself(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
	e.GitInit(proj)

	// Bash, not Write. The mock executes Bash and applies it in whatever tree the
	// agent is bound to; a Write in a sub-agent's scenario creates no file at
	// all, which is what made this look untestable.
	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo 'the sub-agent did this' > only-the-sub-made-this.md"),
	))

	res := e.Run(proj, "s-015-01", "delegate into isolation", Turns("root done",
		Bash("rb1", "echo 'the root did this' > only-the-root-made-this.md"),
		Dispatch("d1", "do the delegated job", sub, "worktree"),
	))

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
	subLines := subLedger(t, proj, wt, "recorder", "log")
	if len(subLines) == 0 {
		t.Fatalf("nothing was judged at the isolated sub-agent's own cycle. Its work is in its "+
			"worktree and the project's rule is checked out there with it, so a cycle that judged "+
			"nothing means delegated work went unguarded — the exact hole subagent-stop exists to "+
			"close:\n%s", res.Output)
	}
	subLine, ok := lineAbout(subLines, "only-the-sub-made-this.md")
	if !ok {
		t.Fatalf("the sub-agent's own file was not among what its cycle judged (%v) — a cycle "+
			"that ran but did not see the work it was run for", subLines)
	}

	// (2) Judged as ITSELF. The identity is the whole invariant: an engine
	// judging the sub-agent's cycle against the dispatching session's record
	// would resolve the parent's identity here, and every other assertion in
	// this test would still hold.
	rootLines := e.FileGuardLedgerLines(proj, "recorder", "log")
	rootLine, ok := lineAbout(rootLines, "only-the-root-made-this.md")
	if !ok {
		t.Fatalf("the root's own write was not judged (%v), so there is no identity to compare "+
			"the sub-agent's against and the comparison below would pass vacuously", rootLines)
	}
	subID, rootID := sessionOf(subLine), sessionOf(rootLine)
	if subID == "" {
		t.Fatalf("the sub-agent's cycle recorded no session identity at all (%q) — nothing can be "+
			"concluded about whose record it was judged against", subLine)
	}
	if subID == rootID {
		t.Fatalf("the sub-agent's cycle was judged as the DISPATCHING session (%s). A sub-agent's "+
			"cycle judged against its parent's record hands every rule another agent's actions as "+
			"though this one had taken them.\n  sub-agent: %s\n  root:      %s", subID, subLine, rootLine)
	}

	// (3) And the reverse error: the root's cycle never saw the sub-agent's file.
	if containsPath(rootLines, "only-the-sub-made-this.md") {
		t.Fatalf("the DISPATCHING session's own cycle judged the sub-agent's file (%v). The two "+
			"are separate trees, so this is the parent being handed another session's work as its "+
			"own", rootLines)
	}
}

// T015_02: a sub-agent SHARING the dispatching session's tree is also judged as
// itself — and the parent judges the same file again as itself.
//
// The shared tree is the ordinary path and the one where the two sessions are
// hardest to keep apart: one directory, one set of files, and the only thing
// separating the two cycles is the identity each is judged under.
//
// The double judgement is asserted as the behaviour it is, rather than assumed
// away. A sub-agent's work is in the tree the root's own Stop later diffs, so the
// root sees it too and judges it a second time. That is NOT revalidation
// failing: revalidation exempts a hook from re-judging content ALREADY JUDGED IN
// THE SAME SESSION'S STORE, and these are two different sessions with two
// different stores — which is precisely what the state invariant demands. A
// sub-agent's verdict exempting the parent would be the pooling the spec says
// must not happen.
//
// So the two judgements are the correct consequence of separate state, and this
// test pins them together: two verdicts on one file, under two identities. An
// engine that pooled the state would produce ONE — the parent skipping on the
// strength of the sub-agent's note — and this test is what tells the difference.
func TestT015_02_ASharedTreeSubagentAndItsParentEachJudgeAsThemselves(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
	e.GitInit(proj)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo 'delegated' > from-the-sub.md"),
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

	// Both cycles judged it, and they are the same tree so both ledgers are the
	// project's own.
	lines := e.FileGuardLedgerLines(proj, "recorder", "log")
	var judges []string
	for _, l := range lines {
		if pathOf(l) == "from-the-sub.md" {
			judges = append(judges, sessionOf(l))
		}
	}
	if len(judges) == 0 {
		t.Fatalf("the sub-agent's file was judged by nobody (%v):\n%s", lines, res.Output)
	}
	if len(judges) == 1 {
		t.Fatalf("the sub-agent's file was judged exactly once, by %s. Two sessions changed and "+
			"then ended over this tree — the sub-agent's cycle and the root's — and each keeps its "+
			"own record of what it has judged. One verdict means one of them was exempted by the "+
			"OTHER's note, which is the state pooling the spec forbids: a sub-agent's note that it "+
			"had already judged must never exempt the parent. Ledger: %v", judges[0], lines)
	}
	if judges[0] == judges[1] {
		t.Fatalf("both judgements of the sub-agent's file were made under one identity (%s). The "+
			"sub-agent's cycle and the parent's are different sessions, and an engine collapsing "+
			"them judges delegated work as the dispatching session's own. Ledger: %v",
			judges[0], lines)
	}
}

// T015_03: what a sub-agent's guardrail remembers is not readable by the
// parent's, and what the parent's remembers is not readable by the sub-agent's.
//
// subagent_state_is_its_own, asserted end to end. 014 declares this unobservable
// on the grounds that a sub-agent's tool calls arrive as the parent's — true of
// its PRE events, and irrelevant here, because this reads the state a rule
// writes at the sub-agent's own POST cycle, which is scoped to the sub-agent.
//
// The shape is what makes it conclusive. Each invocation reports what it could
// READ in its own scope before writing its own note. Pooled state shows up as a
// sub-agent reading back the root's note, or the root's later hook reading back
// the sub-agent's — either direction is the defect, and both are checked.
//
// The root writes on BOTH sides of the dispatch, which is what makes the
// positive control part of the test rather than a separate one: the root's
// second hook must read back the root's FIRST note. Without that, "the root did
// not read the sub-agent's note" is equally true of a store that never worked at
// all, and the whole test would be vacuous.
func TestT015_03_ASubagentsStateDoesNotPoolWithItsParents(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "memo", readsBackItsOwnState, map[string]string{"record.sh": readsBackScript})
	e.GitInit(proj)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo sub > the-subs-file.md"),
	))

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

	rootLines := e.FileGuardLedgerLines(proj, "memo", "log")
	subLines := subLedger(t, proj, theWorktree(t, proj), "memo", "log")

	// THE POSITIVE CONTROL. Some hook of the ROOT's read back what an earlier
	// hook of the root's had stored. Without this the isolation assertions below
	// hold just as well against a store that never opened, and nothing here
	// would mean anything.
	//
	// Asserted as "a later invocation saw a note left by an earlier one", not as
	// a specific value. Several files change in the root's cycle — its own two
	// writes and the scenario script the harness drops in the tree — and the
	// order the Post events are dispatched in is the engine's business, not
	// this test's. Pinning one exact predecessor would be asserting a dispatch
	// order that nothing promises, and it fails for a reason that has nothing to
	// do with state pooling.
	//
	// What cannot happen by accident is the thing checked: a value written by
	// one hook process, read back by a later one, in the same session's scope.
	// A store that never opened returns an error; one keyed per invocation
	// returns nothing.
	after, ok := lineAbout(rootLines, "root-after.md")
	if !ok {
		t.Fatalf("the root's write after the delegation was never judged (%v), so the control "+
			"this test rests on did not run", rootLines)
	}
	var carried bool
	for _, l := range rootLines {
		if got := beforeOf(l); got != "" && pathOf(l) != got {
			carried = true
			break
		}
	}
	if !carried {
		t.Fatalf("no hook in the ROOT's own session read back anything a previous hook of that "+
			"session had stored. The session store is not working, so every assertion below about "+
			"state NOT crossing between sessions would pass against an engine that stores nothing "+
			"at all. Ledger: %v", rootLines)
	}

	// The sub-agent's own cycle ran and stored into a scope of its own.
	subLine, ok := lineAbout(subLines, "the-subs-file.md")
	if !ok {
		t.Fatalf("the sub-agent's own cycle judged nothing (%v), so there is no sub-agent state "+
			"to be isolated and this test proves nothing:\n%s", subLines, res.Output)
	}

	// Direction one: the sub-agent did not read the PARENT's note. The root had
	// already stored root-before.md by the time the sub-agent ran, so a pooled
	// scope hands it exactly that.
	if got := beforeOf(subLine); got != "" {
		t.Fatalf("the sub-agent's guardrail read back %q — a note the PARENT's hook wrote. Pooled "+
			"with the parent's, a sub-agent's state means a rule that remembers something "+
			"remembers it about work it was never watching. Sub ledger: %v", got, subLines)
	}

	// Direction two: NO hook of the parent's read back the SUB-AGENT's note. The
	// sub-agent stored the-subs-file.md while the root's cycle was in progress,
	// so a pooled scope surfaces it to one of the root's hooks — checked across
	// all of them rather than only the last, since which one would see it
	// depends on a dispatch order this test does not fix.
	//
	// `after` is used here so the assertion above it cannot be dead code if the
	// ledger were ever empty.
	_ = after
	for _, l := range rootLines {
		if beforeOf(l) == "the-subs-file.md" {
			t.Fatalf("a guardrail in the PARENT's session read back the SUB-AGENT's note (%s). A "+
				"sub-agent's note that it had already refused would exempt the parent from a "+
				"refusal the parent never received. Ledger: %v", l, rootLines)
		}
	}
}

// T015_04: two sub-agents in one session do not read each other's state.
//
// The spec's own example of what pooling costs is "a session spawning ten
// sub-agents would have all ten reading each other's notes as their own". Two is
// the smallest case that exhibits it, and unlike the parent/child direction it
// cannot be explained away by a tree boundary: both sub-agents are the same KIND
// of thing, dispatched the same way, by the same session.
//
// Each writes a note keyed to its own work. Neither may see the other's.
func TestT015_04_TwoSubagentsDoNotReadEachOthersState(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "memo", readsBackItsOwnState, map[string]string{"record.sh": readsBackScript})
	e.GitInit(proj)

	first := harness.SubagentScript(t, harness.Turns("one done", Bash("a1", "echo one > first-subs-file.md")))
	second := harness.SubagentScript(t, harness.Turns("two done", Bash("a2", "echo two > second-subs-file.md")))

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

	trees := worktrees(t, proj)
	if len(trees) != 2 {
		t.Fatalf("want a worktree per sub-agent, found %d (%v) — two sub-agents were never in play",
			len(trees), trees)
	}

	// Collect each sub-agent's single judgement, whichever worktree it landed in.
	seen := map[string]string{} // path judged -> the line
	ids := map[string]string{}  // path judged -> session identity
	for _, wt := range trees {
		for _, l := range subLedger(t, proj, wt, "memo", "log") {
			seen[pathOf(l)] = l
			ids[pathOf(l)] = sessionOf(l)
		}
	}

	firstLine, okFirst := seen["first-subs-file.md"]
	secondLine, okSecond := seen["second-subs-file.md"]
	if !okFirst || !okSecond {
		t.Fatalf("both sub-agents' own cycles must have judged their own work for this to be "+
			"about two sub-agents' state; got %v", seen)
	}

	// Distinct identities, or there is only one session here and nothing to keep
	// apart.
	if ids["first-subs-file.md"] == ids["second-subs-file.md"] {
		t.Fatalf("both sub-agents' cycles were judged under one identity (%s) — the two were "+
			"collapsed into a single session, under which their state cannot be separate however "+
			"the store is keyed", ids["first-subs-file.md"])
	}

	// Neither read the other's note. Both scopes were fresh.
	for path, line := range map[string]string{
		"first-subs-file.md":  firstLine,
		"second-subs-file.md": secondLine,
	} {
		if got := beforeOf(line); got != "" {
			t.Fatalf("the sub-agent that judged %s read back %q — a note the OTHER sub-agent (or "+
				"the parent) wrote. Ten sub-agents in one session would have all ten reading each "+
				"other's notes as their own. Line: %s", path, got, line)
		}
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
	e.GitInit(proj)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "echo one > sub-one.md"),
		Bash("sb2", "echo two > sub-two.md"),
		Bash("sb3", "echo three > sub-three.md"),
	))

	res := e.Run(proj, "s-015-05", "delegate several turns", Turns("root done",
		Dispatch("d1", "do three things", sub, "worktree"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the multi-turn delegated cycle did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent hit the retry cap:\n%s", res.Output)
	}

	lines := subLedger(t, proj, theWorktree(t, proj), "recorder", "log")
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
	for _, l := range lines {
		got := sessionOf(l)
		if id == "" {
			id = got
			continue
		}
		if got != id {
			t.Fatalf("one sub-agent's cycle judged its files under two different identities (%s "+
				"and %s) — a session that changes identity mid-cycle keeps its state in two "+
				"places. Ledger: %v", id, got, lines)
		}
	}
	if strings.TrimSpace(id) == "" {
		t.Fatalf("the sub-agent's cycle recorded no identity at all. Ledger: %v", lines)
	}
}
