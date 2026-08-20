package e2e

import (
	"testing"
)

// T028_01: describe on a ROOT trajectory — not a sub-agent, and it names the
// sub-agents it spawned.
//
// The root case the spec calls for: isSubagent false, subagentPaths populated. A
// root dispatches one sub-agent, whose record sits in <session>/subagents; describe
// on the root must report itself as the main line and enumerate that record.
func TestT028_01_DescribeRootHasSubagentPaths(t *testing.T) {
	e := New(t)
	tr := newTraj(t)

	const dispatchID = "toolu_01dispatch"
	rootPath := tr.root("s-root",
		rootLine("root-origin"),
		dispatchLine("root-dispatch", "root-origin", dispatchID),
	)
	subPath := tr.subagent("s-root", "agentone", sidechainRootLine("sub-origin", "agentone"))
	tr.meta("s-root", "agentone", map[string]any{
		"agentType": "general-purpose", "description": "delegated", "toolUseId": dispatchID, "spawnDepth": 1,
	})

	res := e.CLIDirect(tr.dir, "sr-session", "trajectory", "describe", "--path", rootPath)
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
	if subs[0] != subPath {
		t.Fatalf("subagentPaths[0]=%v, want %s", subs[0], subPath)
	}
}

// T028_02: describe on a SUB-AGENT trajectory — it is a sub-agent, and its
// immediate parent is derived.
//
// The sub-agent case the spec calls for: isSubagent true, parentPath set. The
// link is DERIVED — the sub-agent's meta names the toolUseId that dispatched it,
// and describe finds the trajectory holding a tool_use with that id. This is the
// case the mock cannot reach (it writes an empty toolUseId), so it runs here
// against a fixture carrying the id Claude Code actually writes.
func TestT028_02_DescribeSubagentHasParentPath(t *testing.T) {
	e := New(t)
	tr := newTraj(t)

	const dispatchID = "toolu_01parentdispatch"
	rootPath := tr.root("s-root",
		rootLine("root-origin"),
		dispatchLine("root-dispatch", "root-origin", dispatchID),
	)
	subPath := tr.subagent("s-root", "agentone",
		sidechainRootLine("sub-origin", "agentone"),
	)
	tr.meta("s-root", "agentone", map[string]any{
		"agentType": "Explore", "description": "look into X", "toolUseId": dispatchID, "spawnDepth": 1,
	})

	res := e.CLIDirect(tr.dir, "sr-session", "trajectory", "describe", "--path", subPath)
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

// T028_03: a sub-agent whose meta records no toolUseId — the mock's shape, and
// some harnesses' — is still reported as a sub-agent, but with no parentPath
// rather than a guessed one.
//
// This is the honest degradation the spec's "absent when ... cannot be located"
// describes, and it is exactly what a mock-produced sub-agent looks like: the
// isSubagent fact still holds (the meta companion is present, and the record is a
// sidechain), while the parent cannot be derived and is left out.
func TestT028_03_SubagentWithoutToolUseIDHasNoParent(t *testing.T) {
	e := New(t)
	tr := newTraj(t)

	tr.root("s-root", rootLine("root-origin"), dispatchLine("root-dispatch", "root-origin", "toolu_x"))
	subPath := tr.subagent("s-root", "agentone", sidechainRootLine("sub-origin", "agentone"))
	// The mock's meta shape: an empty toolUseId.
	tr.meta("s-root", "agentone", map[string]any{
		"agentType": "general-purpose", "description": "", "toolUseId": "", "worktreePath": "",
	})

	res := e.CLIDirect(tr.dir, "sr-session", "trajectory", "describe", "--path", subPath)
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
// explicit flag.
func TestT028_04_DescribeDefaultsToPayloadTranscript(t *testing.T) {
	e := New(t)
	tr := newTraj(t)

	rootPath := tr.root("s-root", rootLine("root-origin"))

	payload := `{"transcript_path":"` + rootPath + `","cwd":"` + tr.dir + `"}`
	res := e.CLIDirectStdin(tr.dir, payload, "sr-session", "trajectory", "describe")
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
// two ways to supply one.
func TestT028_05_DescribeWithNoTrajectoryRefuses(t *testing.T) {
	e := New(t)
	tr := newTraj(t)

	res := e.CLIDirectStdin(tr.dir, `{}`, "sr-session", "trajectory", "describe")
	if res.Code == 0 {
		t.Fatalf("describe with no trajectory exited 0, want non-zero:\n%s", res.Output)
	}
	if !res.Saw("no trajectory to read") {
		t.Fatalf("the refusal did not say why:\n%s", res.Output)
	}
}
