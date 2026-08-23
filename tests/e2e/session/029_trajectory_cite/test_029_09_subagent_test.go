package e2e

import (
	"strings"
	"testing"
)

// The subagent guard (PR comment #5a). cite finds a USER message an agent may cite
// as the grounding for a written claim — but inside a SUB-AGENT the "user" messages
// are the PARENT agent's Task/dispatch prompt, not the end user's own words, so a
// citation into them would ground a claim in something the person never said. cite
// refuses in a sub-agent with a distinct exit 3, rather than mint that false
// citation.
//
// The discriminator is the RESOLVED TRAJECTORY, not the environment: cite is an
// ordinary tool call the agent makes mid-work, with no Stop/SubagentStop invocation
// to carry whose cycle it is, and no env var tells a sub-agent apart from a root
// (the fields that name a sub-agent, agent_id / agent_type, arrive only on a HOOK's
// JSON stdin, never to a tool call; CLAUDE_CODE_SESSION_ID names the session but not
// whether a sub-agent is asking). So the fact is read from the file itself — a
// sub-agent's record carries isSidechain / a meta companion — which is exactly what
// `describe` reports as isSubagent. cite's environment fallback resolves the CURRENT
// session (the root, which holds the user's words) from CLAUDE_CODE_SESSION_ID; if a
// sub-agent's own transcript is ever what resolves, this same file-based guard
// catches it.

// T029_09: cite refuses inside a sub-agent — the default no-path case, where the
// resolved trajectory is the sub-agent's own record. Exit 3, distinct from the
// 0/1/2 citation codes, with the reason on stderr; nothing citable on stdout even
// though the substring is literally present in the (sidechain) user record.
func TestT029_09_CiteRefusesInSubagent(t *testing.T) {
	e := New(t)
	// A sub-agent's own transcript: its origin record is a sidechain, and its
	// "user" content is the parent's dispatch prompt. The quote below is present in
	// that record — cite must still refuse, because it is the parent's words.
	path := writeSubagentTranscript(t, "abc",
		sidechainUserMsg("s1", "abc", "go and refactor the AUTH module as instructed"),
	)

	res := cite(e, dirOf(path), path, "AUTH module")
	if res.Code != 3 {
		t.Fatalf("cite in a sub-agent exited %d, want 3 (the subagent refusal):\n%s", res.Code, res.Output)
	}
	// The refusal is a real one, not a citation: no <path>:<line> on stdout.
	if strings.Contains(res.Output, path+":") {
		t.Fatalf("cite in a sub-agent printed a citation, must refuse instead:\n%s", res.Output)
	}
	if !res.Saw("sub-agent") || !res.Saw("not available") {
		t.Fatalf("the refusal did not say why cite is unavailable in a sub-agent:\n%s", res.Output)
	}
}

// T029_10: exit 3 is distinct from "no match" (1) and "ambiguous" (2). A script
// branching on the code must be able to tell "cite must not answer here" from "the
// user did not say that" — collapsing them would make a script retry on a narrower
// quote against a command that will refuse every time. Proven by contrast: the SAME
// quote that yields exit 3 in a sub-agent yields exit 0 in a root.
func TestT029_10_SubagentRefusalIsDistinctFromNoMatch(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// Root: the quote is the end user's own words (the seeded prompt), one line —
	// exit 0. Driven through the mock, whose prompt IS the user's words.
	e.Run(proj, "s-029-10", "please tidy the SHARED helper", Turns("done",
		Bash("b1", "echo ok > ok.md"),
	))
	rootPath := e.TranscriptPath(proj, "s-029-10")
	root := cite(e, proj, rootPath, "SHARED helper")
	if root.Code != 0 {
		t.Fatalf("cite in a root exited %d, want 0:\n%s", root.Code, root.Output)
	}

	// Sub-agent: the same phrase, but it is the parent's dispatch — exit 3, not 1.
	subPath := writeSubagentTranscript(t, "def",
		sidechainUserMsg("s1", "def", "please tidy the SHARED helper"),
	)
	sub := cite(e, dirOf(subPath), subPath, "SHARED helper")
	if sub.Code != 3 {
		t.Fatalf("cite in a sub-agent exited %d, want 3 — and NOT 1 (no match):\n%s", sub.Code, sub.Output)
	}
}

// T029_11: a ROOT citing a sub-agent's trajectory by explicit --path is refused
// too. Whoever runs cite, a sub-agent's user-words are the parent's dispatch, so
// they are never a citable grounding for the end user's intent — the refusal is
// about the TRAJECTORY, not about who is asking. (Reading a sub-agent's trajectory
// for other purposes is `normalize --path`, which does not refuse; this only blocks
// minting a citation into it.)
func TestT029_11_RootCitingASubagentTrajectoryIsRefused(t *testing.T) {
	e := New(t)
	subPath := writeSubagentTranscript(t, "ghi",
		sidechainUserMsg("s1", "ghi", "do the delegated WIDGET work"),
	)

	// A root explicitly points --path at the sub-agent's file.
	res := cite(e, dirOf(subPath), subPath, "WIDGET work")
	if res.Code != 3 {
		t.Fatalf("citing a sub-agent's trajectory by --path exited %d, want 3:\n%s", res.Code, res.Output)
	}
	if !res.Saw("sub-agent") {
		t.Fatalf("the refusal did not name the sub-agent reason:\n%s", res.Output)
	}
}

// T029_12: the meta companion alone marks a sub-agent — the primary mark Claude
// Code leaves. Here the record's own lines carry isSidechain FALSE, so only the
// agent-<id>.meta.json beside it identifies the trajectory as a sub-agent's; cite
// must still refuse. This is the detection mechanism a real Claude Code sub-agent
// always has (it writes the meta file for every sub-agent it dispatches to its own
// file), so a fixture proving the meta path is exercised matters as much as the
// isSidechain one.
func TestT029_12_MetaCompanionMarksSubagent(t *testing.T) {
	e := New(t)
	path := writeSubagentWithMeta(t, "jkl",
		// isSidechain false on the record itself — the META FILE is what settles it.
		userMsg("u1", "carry out the delegated MIGRATION"),
	)

	res := cite(e, dirOf(path), path, "MIGRATION")
	if res.Code != 3 {
		t.Fatalf("a meta-companion sub-agent exited %d, want 3:\n%s", res.Code, res.Output)
	}
	if !res.Saw("sub-agent") {
		t.Fatalf("the refusal did not name the sub-agent reason:\n%s", res.Output)
	}
}
