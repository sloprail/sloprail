package dispatch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// This file pins the SubagentStop trajectory bug: a sub-agent whose OWN record
// is what a `{skill}` prerequisite reads had every entry in that record excluded,
// because every entry a harness writes into a sub-agent's own transcript carries
// isSidechain true (see transcript.SubagentTranscriptPath's survey), and
// skillLoadedInTrajectory used to drop every IsSidechain entry unconditionally.
// The result: a sub-agent that itself invoked `Skill: document-decision` and
// then wrote under a guarded prefix was refused at SubagentStop no matter how
// many times it retried, because the very entries the check needed to find were
// the ones it was throwing away.
//
// The fix keys the exclusion on WHICH FILE is being read (transcript.
// IsSubagentTranscript), not on the flag alone: a root's own transcript still
// excludes a delegated child's sidechain entries (they are not this line of
// work), but a sub-agent's own transcript — where every entry is marked
// sidechain by convention — excludes nothing, because there every entry IS this
// line of work.

// writeJSONL writes one JSONL line per record to path, creating parent
// directories as needed.
func writeJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

// skillToolUseEntry is one assistant record invoking the Skill tool for name,
// marked sidechain or not as the caller asks.
func skillToolUseEntry(uuid, parentUUID, skillName string, sidechain bool) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parentUUID + `","isSidechain":` +
		boolStr(sidechain) + `,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Skill","input":{"skill":"` + skillName + `"}}]}}`
}

// originEntry is a bare origin record (no tool call), the first line of a
// transcript file.
func originEntry(uuid string, sidechain bool) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":null,"isSidechain":` + boolStr(sidechain) + `}`
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestSkillLoadedInTrajectory_SubagentOwnRecord_SkillCounts is the repro-then-fix
// case: a sub-agent's OWN transcript, where the harness marks every entry
// isSidechain true, still finds the Skill tool_use that record itself contains.
//
// Before the fix this returned false unconditionally — every entry in a
// sub-agent's own file is sidechain, so the old "skip IsSidechain" loop skipped
// the one entry that mattered, on every read, no matter how many times the
// sub-agent invoked the skill.
// sr:proves checks/skill-requirement-reads-the-record
func TestSkillLoadedInTrajectory_SubagentOwnRecord_SkillCounts(t *testing.T) {
	dir := t.TempDir()
	subPath := filepath.Join(dir, "parent-session", "subagents", "agent-abc.jsonl")
	writeJSONL(t, subPath,
		originEntry("sub-origin", true),
		skillToolUseEntry("sub-skill", "sub-origin", "document-decision", true),
	)

	loaded, err := skillLoadedInTrajectory(subPath, "", "document-decision")
	require.NoError(t, err)
	assert.True(t, loaded,
		"a sub-agent's own Skill tool_use, in its own transcript, must count toward its own require: [{skill}] check")
}

// TestSkillLoadedInTrajectory_SubagentOwnRecord_NoSkillRefuses is the negative
// half: a sub-agent's own transcript with no Skill tool_use at all correctly
// finds nothing (proves the fix does not simply always return true for a
// sub-agent file).
// sr:proves checks/skill-requirement-reads-the-record
func TestSkillLoadedInTrajectory_SubagentOwnRecord_NoSkillRefuses(t *testing.T) {
	dir := t.TempDir()
	subPath := filepath.Join(dir, "parent-session", "subagents", "agent-abc.jsonl")
	writeJSONL(t, subPath,
		originEntry("sub-origin", true),
	)

	loaded, err := skillLoadedInTrajectory(subPath, "", "document-decision")
	require.NoError(t, err)
	assert.False(t, loaded, "no Skill tool_use anywhere in the record: the requirement is not met")
}

// TestSkillLoadedInTrajectory_RootExcludesExitedChildSubagent pins the half of
// the design that was already correct and must keep holding: a ROOT session's
// own transcript does not itself contain isSidechain entries in real harness
// output (measured in transcript.SubagentTranscriptPath's survey — 0 of 8,119),
// but should the field ever be present there, an entry marked sidechain in the
// ROOT's own file is a delegated child's — a different line of work — and must
// stay excluded from what the ROOT's own Stop check reads.
// sr:proves checks/skill-requirement-reads-the-record
func TestSkillLoadedInTrajectory_RootExcludesExitedChildSubagent(t *testing.T) {
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "parent-session.jsonl")
	writeJSONL(t, rootPath,
		originEntry("root-origin", false),
		// A sidechain entry inlined into the ROOT's own file: the shape that must
		// still be excluded there, because transcript.IsSubagentTranscript decides
		// "is this a sub-agent's OWN record" from the FIRST record (root-origin,
		// isSidechain:false) and correctly calls this a root file.
		skillToolUseEntry("child-skill", "root-origin", "document-decision", true),
	)

	loaded, err := skillLoadedInTrajectory(rootPath, "", "document-decision")
	require.NoError(t, err)
	assert.False(t, loaded,
		"a skill loaded on a delegated child's line of work, inside the ROOT's own transcript, "+
			"must not satisfy the root's own require: [{skill}] check")
}

// TestSkillLoadedInTrajectory_RootOwnSkillStillCounts: the ordinary root case is
// unaffected by the fix — a root's own, non-sidechain Skill tool_use still
// counts.
// sr:proves checks/skill-requirement-reads-the-record
func TestSkillLoadedInTrajectory_RootOwnSkillStillCounts(t *testing.T) {
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "parent-session.jsonl")
	writeJSONL(t, rootPath,
		originEntry("root-origin", false),
		skillToolUseEntry("root-skill", "root-origin", "document-decision", false),
	)

	loaded, err := skillLoadedInTrajectory(rootPath, "", "document-decision")
	require.NoError(t, err)
	assert.True(t, loaded, "the root's own Skill tool_use in its own transcript must count")
}

// TestCheckSkill_SubagentOwnRecord_EndToEnd drives checkSkill (the Runner's own
// entry point, as require.go's checkRequire calls it) rather than the bare
// trajectory reader, so the fix is pinned at the same seam a real SubagentStop
// dispatch uses.
// sr:proves checks/skill-requirement-reads-the-record
func TestCheckSkill_SubagentOwnRecord_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	subPath := filepath.Join(dir, "parent-session", "subagents", "agent-abc.jsonl")
	writeJSONL(t, subPath,
		originEntry("sub-origin", true),
		skillToolUseEntry("sub-skill", "sub-origin", "document-decision", true),
	)

	req := Request{
		Nature:         NatureFileGuard,
		Require:        []declaration.Prerequisite{{Skill: "document-decision"}},
		TranscriptPath: subPath,
		Dir:            "/guard",
		GuardName:      "require-skill-decisions",
	}

	v, err := Runner{}.Run(req)
	require.NoError(t, err)
	assert.False(t, v.Refused, "reason: %s", v.Reason)
}
