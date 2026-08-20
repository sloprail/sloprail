package e2e

import (
	"testing"
)

// T028_01: describe on a ROOT trajectory — not a sub-agent, and it names the
// sub-agents it spawned.
//
// The root case the spec calls for: isSubagent false, subagentPaths populated. The
// mock is driven to dispatch one sub-agent, which writes its record under
// <session>/subagents; describe on the mock's own root transcript must report
// itself as the main line and enumerate that record. The expected sub-agent path
// is read from disk with the harness's own directory glob — NOT from the command
// under test — so a bug shared by both could not hide.
func TestT028_01_DescribeRootHasSubagentPaths(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	sub := writeScenario(t, proj, Turns("sub done",
		Bash("s1", "echo delegated > from-sub.md"),
	))
	e.Run(proj, "s-028-01", "start the work", Turns("root done",
		Bash("b0", "echo root > root.md"),
		Dispatch("d1", "delegated prompt", sub, ""),
	))

	rootPath := e.TranscriptPath(proj, "s-028-01")
	recs := e.SubagentRecordPaths(proj, "s-028-01")
	if len(recs) != 1 {
		t.Fatalf("the mock should have written exactly one sub-agent record, wrote %d (%v)", len(recs), recs)
	}

	res := e.CLIDirect(proj, "sr-session", "trajectory", "describe", "--path", rootPath)
	if res.Code != 0 {
		t.Fatalf("describe on a root exited %d, want 0:\n%s", res.Code, res.Output)
	}
	got := decodeDescribe(t, res.Output)

	if got["isSubagent"] != false {
		t.Fatalf("a root reported isSubagent=%v, want false:\n%s", got["isSubagent"], res.Output)
	}
	if _, present := got["parentPath"]; present {
		t.Fatalf("a root carried a parentPath, which it must not:\n%s", res.Output)
	}
	subs, ok := got["subagentPaths"].([]any)
	if !ok || len(subs) != 1 {
		t.Fatalf("subagentPaths=%v, want exactly the one sub-agent record:\n%s", got["subagentPaths"], res.Output)
	}
	if subs[0] != recs[0] {
		t.Fatalf("subagentPaths[0]=%v, want the record the mock wrote %s", subs[0], recs[0])
	}
}

// T028_02: describe on a SUB-AGENT trajectory — it is a sub-agent, and its
// immediate parent is derived.
//
// The sub-agent case the spec calls for: isSubagent true, parentPath set. The
// link is DERIVED — the sub-agent's meta names the toolUseId that dispatched it,
// and describe finds the trajectory holding a tool_use with that id. This is the
// case the mock cannot reach: it writes an EMPTY toolUseId for every sub-agent it
// seeds (see the package note), so no mock-produced record could ever set
// parentPath. It runs here against the one hand-authored fixture in this package,
// carrying the id Claude Code actually writes.
func TestT028_02_DescribeSubagentHasParentPath(t *testing.T) {
	e := New(t)
	f := newFixtureParent(t)

	const dispatchID = "toolu_01parentdispatch"
	rootPath := f.root("s-root", dispatchID)
	subPath := f.subagent("s-root", "agentone", dispatchID)

	res := e.CLIDirect(f.dir, "sr-session", "trajectory", "describe", "--path", subPath)
	if res.Code != 0 {
		t.Fatalf("describe on a sub-agent exited %d, want 0:\n%s", res.Code, res.Output)
	}
	got := decodeDescribe(t, res.Output)

	if got["isSubagent"] != true {
		t.Fatalf("a sub-agent reported isSubagent=%v, want true:\n%s", got["isSubagent"], res.Output)
	}
	if got["parentPath"] != rootPath {
		t.Fatalf("parentPath=%v, want the dispatching root %s (the toolUseId correlation):\n%s",
			got["parentPath"], rootPath, res.Output)
	}
	// A leaf sub-agent spawned nothing of its own.
	subs, ok := got["subagentPaths"].([]any)
	if !ok || len(subs) != 0 {
		t.Fatalf("subagentPaths=%v, want empty for a leaf sub-agent:\n%s", got["subagentPaths"], res.Output)
	}
}

// T028_03: a sub-agent whose meta records no toolUseId is still reported as a
// sub-agent, but with no parentPath rather than a guessed one.
//
// This is the honest degradation the spec's "absent when ... cannot be located"
// describes, and it is exactly what a MOCK-produced sub-agent looks like: the mock
// seeds the record with an isSidechain origin and a meta companion carrying an
// empty toolUseId, so the isSubagent fact holds while the parent cannot be derived.
// Driven through the mock rather than hand-authored, because the mock's shape IS
// the subject — a fixture claiming to be "the mock's shape" could drift from it,
// and this asserts against the real thing.
func TestT028_03_SubagentWithoutToolUseIDHasNoParent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	sub := writeScenario(t, proj, Turns("sub done",
		Bash("s1", "echo delegated > from-sub.md"),
	))
	e.Run(proj, "s-028-03", "start the work", Turns("root done",
		Dispatch("d1", "delegated prompt", sub, ""),
	))

	recs := e.SubagentRecordPaths(proj, "s-028-03")
	if len(recs) != 1 {
		t.Fatalf("the mock should have written exactly one sub-agent record, wrote %d (%v)", len(recs), recs)
	}

	res := e.CLIDirect(proj, "sr-session", "trajectory", "describe", "--path", recs[0])
	if res.Code != 0 {
		t.Fatalf("describe exited %d, want 0:\n%s", res.Code, res.Output)
	}
	got := decodeDescribe(t, res.Output)

	if got["isSubagent"] != true {
		t.Fatalf("isSubagent=%v, want true — the meta companion and the sidechain record both mark it:\n%s",
			got["isSubagent"], res.Output)
	}
	if _, present := got["parentPath"]; present {
		t.Fatalf("parentPath was set to %v from a meta with no toolUseId — it must be absent, not guessed:\n%s",
			got["parentPath"], res.Output)
	}
}

// T028_04: describe defaults to the hook-provided trajectory when --path is
// absent, taking it off the payload the way query and id do.
//
// The common case for a hook: no --path, the trajectory named on the payload's
// transcript_path. This proves the default resolution is wired, not only the
// explicit flag — read against a mock-produced root transcript.
func TestT028_04_DescribeDefaultsToPayloadTranscript(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-028-04", "start the work", Turns("root done",
		Bash("b0", "echo root > root.md"),
	))
	rootPath := e.TranscriptPath(proj, "s-028-04")

	payload := `{"transcript_path":"` + rootPath + `","cwd":"` + proj + `"}`
	res := e.CLIDirectStdin(proj, payload, "sr-session", "trajectory", "describe")
	if res.Code != 0 {
		t.Fatalf("describe from a payload exited %d, want 0:\n%s", res.Code, res.Output)
	}
	got := decodeDescribe(t, res.Output)
	if got["isSubagent"] != false {
		t.Fatalf("isSubagent=%v, want false for the payload's root transcript:\n%s", got["isSubagent"], res.Output)
	}
}

// T028_05: with neither --path nor a transcript on the payload, describe refuses
// rather than answering about nothing.
//
// "No trajectory to read" is a refusal, exit non-zero — not an empty description
// that a script might read as "a root with no sub-agents". The message names the
// two ways to supply one. No transcript is needed to exercise the refusal.
func TestT028_05_DescribeWithNoTrajectoryRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()

	res := e.CLIDirectStdin(proj, `{}`, "sr-session", "trajectory", "describe")
	if res.Code == 0 {
		t.Fatalf("describe with no trajectory exited 0, want non-zero:\n%s", res.Output)
	}
	if !res.Saw("no trajectory to read") {
		t.Fatalf("the refusal did not say why:\n%s", res.Output)
	}
}
