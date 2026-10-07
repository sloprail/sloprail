package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T015_11: a sub-agent working in a SUBDIRECTORY is judged on paths relative to
// its own tree's root.
//
// The layout case that decides whether a rule can be written about a path at
// all. A sub-agent bound to a worktree has its own root, and the file it touches
// deep inside that tree must be named the same way a rule's author would name it
// — relative to the root of the tree being judged, not to the sub-agent's
// current directory and not as an absolute path into a temporary worktree.
//
// An absolute path here would be worse than untidy: it embeds the worktree's
// generated name, so a rule matching on path could never be written to fire in a
// sub-agent, and the verdicts recorded under it would key on a path that never
// occurs again.
//
// MEASURED: the path reported is pkg/deep/nested.md — relative to the worktree
// root, exactly as the root's own cycle would report a file in its tree.
func TestT015_11_ASubagentInASubdirectoryIsJudgedOnTreeRelativePaths(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
	e.GitInit(proj)

	sub := harness.SubagentScript(t, harness.Turns("sub done",
		Bash("sb1", "mkdir -p pkg/deep && echo nested > pkg/deep/nested.md"),
	).ThenCommit("the sub-agent's work"))

	res := e.Run(proj, "s-015-11", "delegate work in a subdirectory", Turns("root done",
		Dispatch("d1", "work deep in the tree", sub, "worktree"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the delegated cycle did not complete:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("the sub-agent hit the retry cap:\n%s", res.Output)
	}

	wt := theWorktree(t, proj)
	e.CheckRunRange(filepath.Join(proj, ".claude", "worktrees", wt), "s-015-12", e.RunBase("s-015-12"), "HEAD")
	lines := subLedger(t, proj, wt, "recorder", "log")
	for _, l := range lines {
		if agentOf(l) == "" {
			t.Fatalf("the file-guard judged without the sub-agent's identity: %s", l)
		}
	}
	if len(lines) == 0 {
		t.Fatalf("the sub-agent's cycle judged nothing, though it created a file in its own "+
			"tree:\n%s", res.Output)
	}
	if !containsPath(lines, "pkg/deep/nested.md") {
		var got []string
		for _, l := range lines {
			got = append(got, pathOf(l))
		}
		t.Fatalf("the sub-agent's file was not reported at its tree-relative path. Want "+
			"pkg/deep/nested.md, got %v. A path reported absolutely embeds the worktree's "+
			"generated name, so no rule could ever be written to match it and every verdict "+
			"recorded under it keys on a path that never recurs", got)
	}

	// And specifically NOT absolute. containsPath above would still pass if some
	// other line carried the relative form while the real one was absolute.
	for _, l := range lines {
		if strings.HasPrefix(pathOf(l), "/") {
			t.Fatalf("a path was reported absolutely (%s) — see above for why that makes a rule "+
				"unwritable. Ledger: %v", pathOf(l), lines)
		}
	}
}

// T015_12: NESTING IS REAL — a sub-agent that delegates further produces a
// second, deeper sub-agent whose own cycle is judged as itself.
//
// This overturns 014_07, which records the opposite as a measurement:
//
//	"MEASURED: this harness does not recursively execute a sub-agent's own
//	 Agent call — a probe whose sub-agent dispatched a third scenario reported
//	 exactly one agentId, the outer's, and the inner scenario never ran. So
//	 depth-2 delegation is NOT exercised here whatever this test asserts about
//	 it."
//
// The observation was right and the conclusion wrong, and the flaw is worth
// naming because it is easy to repeat: agentIDs reads the OUTER run's stream.
// A nested dispatch is made by the sub-agent, so its announcement goes to the
// SUB-AGENT'S stream, which the outer run never carries. Counting ids in the
// outer output therefore reports 1 whether nesting happens or not — the count
// cannot distinguish the two cases, so it was never evidence for either.
//
// Looking where the work would land instead settles it. MEASURED here: the
// innermost scenario DOES run, in the middle sub-agent's tree, and its file is
// judged at a cycle of its own under an identity that is neither the middle's
// nor the root's. Depth-2 delegation is exercised, and three sessions are in
// play.
//
// So what this pins is the deeper claim: the invariants hold at depth 2, not
// just depth 1. A sub-agent's sub-agent is a session in its own right by the
// same mechanism, which is what "a sub-agent is a session in every sense that
// matters" has to mean if it means anything.
//
// The middle sub-agent's own work comes AFTER its dispatch, so an engine that
// lost the rest of a cycle on seeing an Agent call would show up here too.
func TestT015_12_ASubagentThatDelegatesFurtherStillHasItsOwnCycleJudged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.FileGuard(proj, "recorder", recordsPathAndSession, map[string]string{"record.sh": recordScript})
	idLed := e.NewLedger("identities")
	e.Gate(proj, "who", memoGate, map[string]string{"record.sh": memoScript(idLed.Path())})
	e.GitInit(proj)

	// What the inner dispatch would run, if the harness executed it.
	innermost := harness.SubagentScript(t, harness.Turns("inner done",
		Bash("i1", "echo innermost > from-the-innermost.md"),
	).ThenCommit("the innermost's work"))
	// The sub-agent that both delegates AND does work of its own. The work comes
	// after the dispatch, so a dispatch that swallowed the rest of the cycle
	// would leave from-the-middle.md unjudged.
	middle := harness.SubagentScript(t, harness.Turns("middle done",
		Dispatch("md1", "delegate further", innermost, ""),
		Bash("m1", "echo middle > from-the-middle.md"),
	).ThenCommit("the middle's work"))

	res := e.Run(proj, "s-015-12", "delegate to a delegator", Turns("root done",
		Bash("r1", "echo root > from-the-root.md"),
		Dispatch("d1", "outer job", middle, "worktree"),
	))

	if !res.Saw("root done") {
		t.Fatalf("the session did not complete when a delegated scenario contained a dispatch — a "+
			"sub-agent asked to delegate further is an ordinary thing to ask, and a cycle that "+
			"never ends is a deadlock:\n%s", res.Output)
	}
	if hitRetryCap(res.Output) {
		t.Fatalf("a cycle hit the harness's retry cap:\n%s", res.Output)
	}

	// The root's own record carries exactly one announcement — its own dispatch. The
	// stream also carries the sub-agents' frames, as real Claude Code's does, each marked with
	// the parent_tool_use_id of the dispatch it ran under: the nested dispatch is announced in
	// one of those, so only the frames with no parent are the root's. Asserted so nobody reads
	// this count as evidence about nesting again: it is 1 whether nesting happens or not,
	// which is precisely why 014_07's conclusion from it does not follow.
	if ids := agentIDs(rootFrames(res.Output)); len(ids) != 1 {
		t.Fatalf("the root's own frames announced %d dispatch(es) (%v), want the root's one. A nested "+
			"dispatch is announced in a sub-agent's frame, which names its parent", len(ids), ids)
	}

	// Everything below reads the MIDDLE sub-agent's worktree, which is where
	// both the middle's work and the innermost's landed.
	lines := subLedger(t, proj, theWorktree(t, proj), "recorder", "log")

	// The delegating sub-agent's OWN work was judged at its own cycle — the work
	// that came after the dispatch it made.
	if !containsPath(lines, "from-the-middle.md") {
		t.Fatalf("the delegating sub-agent's own work was not judged at its own cycle. The work "+
			"came AFTER the dispatch in its scenario, so an engine that lost the rest of a cycle "+
			"on seeing an Agent call would look exactly like this. Ledger: %v", lines)
	}

	// NESTING IS REAL: the innermost scenario ran and its work is here, in the
	// dispatching sub-agent's tree, judged along with the middle's own.
	if _, ok := lineAbout(lines, "from-the-innermost.md"); !ok {
		t.Fatalf("the innermost scenario's work is absent (%v). Measured on this harness, a "+
			"sub-agent's own Agent call IS executed and the deeper sub-agent's work lands in the "+
			"dispatching sub-agent's tree. If that has changed, the claim that these invariants "+
			"hold at depth 2 needs re-deriving rather than this assertion relaxing", lines)
	}

	// And it ran at a cycle of ITS OWN — a third identity, neither the middle
	// sub-agent's nor the root's. This is the whole point: the deeper sub-agent is a
	// session in its own right by the same mechanism. Seen where hooks run: the gate
	// records the session each command was run as.
	idLines := memoLines(t, idLed.Path())
	ids := map[string]string{}
	for _, name := range []string{"from-the-root.md", "from-the-middle.md", "from-the-innermost.md"} {
		l, ok := lineAbout(idLines, name)
		if !ok {
			t.Fatalf("the gate never saw the command that made %s (%v), so no identity can be "+
				"compared", name, idLines)
		}
		if sessionOf(l) == "" {
			t.Fatalf("no identity recorded for the command that made %s: %s", name, l)
		}
		ids[name] = sessionOf(l)
	}
	if ids["from-the-innermost.md"] == ids["from-the-middle.md"] {
		t.Fatalf("the innermost sub-agent ran under the DELEGATING sub-agent's identity (%s). A "+
			"sub-agent's sub-agent is a session in its own right, and collapsing the two hands one "+
			"of them the other's actions as though it had taken them.\n  %v", ids["from-the-innermost.md"], idLines)
	}
	// The root is a third identity again, so all three are distinct.
	if ids["from-the-root.md"] == ids["from-the-middle.md"] || ids["from-the-root.md"] == ids["from-the-innermost.md"] {
		t.Fatalf("the dispatching session ran under a sub-agent's identity — three sessions are in "+
			"play here and each must keep its own. %v", idLines)
	}
}

// agentIDs returns the agent ids the mock announced, in order.
//
// The mock reports "agentId: <id>" in each dispatch's tool result — the
// harness's own statement about how many sub-agents it ran. Read from the stream
// rather than counted from the scenario, so a dispatch that silently did not
// happen shows up as a missing id.
//
// The label travels inside a JSON string, so the newline after the id is the two
// characters \ and n rather than a real one; both it and a literal newline end
// the id.
func agentIDs(output string) []string {
	const label = "agentId: "
	var ids []string
	for rest := output; ; {
		i := strings.Index(rest, label)
		if i < 0 {
			return ids
		}
		rest = rest[i+len(label):]
		id := rest
		if end := strings.IndexAny(id, " \"\n"); end >= 0 {
			id = id[:end]
		}
		if end := strings.Index(id, `\n`); end >= 0 {
			id = id[:end]
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
}

// rootFrames is the stream's lines that are the root agent's own: those with no
// parent_tool_use_id. A sub-agent's frames (the mock streams them, as Claude Code
// does) carry the id of the dispatch they ran under.
func rootFrames(output string) string {
	var keep []string
	for _, line := range strings.Split(output, "\n") {
		var rec struct {
			Parent *string `json:"parent_tool_use_id"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &rec) == nil && rec.Parent != nil {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}
