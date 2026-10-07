package dispatch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// This file pins the extension to `{skill}`: a Skill tool_use naming the skill
// is not the only trajectory evidence that satisfies it. Reading the skill's
// own SKILL.md directly — a Read tool_use on the file, or a Bash command
// (cat, head, …) that reads it whole — counts too, because both are evidence
// the RECORD can verify just as concretely as a Skill tool_use: a path either
// matches or it does not, and commandmod.ReadsFile settles the Bash half the
// same way commandmod's write-detection already settles what a command line
// touches.
//
// A project's skill lives at `<workspace>/.claude/skills/<name>/SKILL.md`
// (SkillFilePaths), so every case here builds that file under a temp workspace
// and threads the workspace through — the same Request.Workspace a real
// dispatch carries (git root, resolved by services/sr-session's
// natureHookScope).

// readToolUseEntry is one assistant record invoking the Read tool on filePath,
// marked sidechain or not as the caller asks — the Read-tool counterpart of
// skillToolUseEntry in require_test.go.
func readToolUseEntry(uuid, parentUUID, filePath string, sidechain bool) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parentUUID + `","isSidechain":` +
		boolStr(sidechain) + `,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{"file_path":"` + filePath + `"}}]}}`
}

// bashToolUseEntry is one assistant record invoking the Bash tool with command,
// marked sidechain or not — the Bash-tool counterpart of skillToolUseEntry.
func bashToolUseEntry(uuid, parentUUID, command string, sidechain bool) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parentUUID + `","isSidechain":` +
		boolStr(sidechain) + `,"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"` + command + `"}}]}}`
}

// writeSkillFile creates <workspace>/.claude/skills/<name>/SKILL.md with some
// body, and returns its path — the one candidate SkillFilePaths resolves
// without any plugin involved, and the path every case in this file reads.
func writeSkillFile(t *testing.T, workspace, name string) string {
	t.Helper()
	path := filepath.Join(workspace, ".claude", "skills", name, "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("---\nname: "+name+"\n---\nbody\n"), 0o644))
	return path
}

// TestSkillFilePaths_ProjectSkillIsTheFirstCandidate pins the one candidate
// that needs no plugin resolution at all: the project's own
// `.claude/skills/<name>/SKILL.md`, always present in the list whether or not
// the file actually exists — SkillFilePaths states candidates, it does not
// stat them (see its own doc comment).
func TestSkillFilePaths_ProjectSkillIsTheFirstCandidate(t *testing.T) {
	dir := t.TempDir()
	paths := SkillFilePaths(dir, "document-decision")
	require.NotEmpty(t, paths)
	assert.Equal(t, filepath.Join(dir, ".claude", "skills", "document-decision", "SKILL.md"), paths[0])
}

// TestSkillFilePaths_EmptyWorkspaceOrNameYieldsNothing pins the fail-safe
// floor: no project root, or no name, is not a reason to guess a path rooted
// at this process's own working directory.
func TestSkillFilePaths_EmptyWorkspaceOrNameYieldsNothing(t *testing.T) {
	assert.Empty(t, SkillFilePaths("", "document-decision"))
	assert.Empty(t, SkillFilePaths(t.TempDir(), ""))
}

// TestSkillLoadedInTrajectory_ReadToolOnSkillFileCounts is the headline case:
// no Skill tool_use anywhere, but a Read tool_use named exactly the skill's own
// SKILL.md — which is the same content loading the skill would have shown —
// satisfies the check.
// sr:proves checks/skill-requirement-reads-the-record
func TestSkillLoadedInTrajectory_ReadToolOnSkillFileCounts(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeSkillFile(t, workspace, "document-decision")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		readToolUseEntry("read-1", "origin", skillPath, false),
	)

	loaded, err := skillLoadedInTrajectory(transcriptPath, workspace, "document-decision")
	require.NoError(t, err)
	assert.True(t, loaded, "a Read tool_use naming the skill's own SKILL.md must satisfy the prerequisite")
}

// TestSkillLoadedInTrajectory_CatOnSkillFileCounts is the Bash half: no Skill
// tool_use, no Read tool_use, only a `cat` of the skill's own file.
// sr:proves checks/skill-requirement-reads-the-record
func TestSkillLoadedInTrajectory_CatOnSkillFileCounts(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeSkillFile(t, workspace, "document-decision")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		bashToolUseEntry("bash-1", "origin", "cat "+skillPath, false),
	)

	loaded, err := skillLoadedInTrajectory(transcriptPath, workspace, "document-decision")
	require.NoError(t, err)
	assert.True(t, loaded, "a `cat` of the skill's own SKILL.md must satisfy the prerequisite")
}

// TestSkillLoadedInTrajectory_HeadOnSkillFileCounts pins a second read-shaped
// binary, so the case above is not read as "only cat is recognised".
// sr:proves checks/skill-requirement-reads-the-record
func TestSkillLoadedInTrajectory_HeadOnSkillFileCounts(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeSkillFile(t, workspace, "document-decision")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		bashToolUseEntry("bash-1", "origin", "head -n 40 "+skillPath, false),
	)

	loaded, err := skillLoadedInTrajectory(transcriptPath, workspace, "document-decision")
	require.NoError(t, err)
	assert.True(t, loaded, "a `head` of the skill's own SKILL.md must satisfy the prerequisite")
}

// TestSkillLoadedInTrajectory_NeitherSkillNorReadRefuses is the negative half
// that keeps the extension honest: a trajectory with no Skill tool_use AND no
// read of the skill's own file — not even a read of some OTHER file — still
// finds nothing. Unchanged behaviour, and the case that would catch an
// over-broad match (e.g. matching on skill NAME rather than the resolved path).
// sr:proves checks/skill-requirement-reads-the-record
func TestSkillLoadedInTrajectory_NeitherSkillNorReadRefuses(t *testing.T) {
	workspace := t.TempDir()
	writeSkillFile(t, workspace, "document-decision")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		readToolUseEntry("read-1", "origin", filepath.Join(workspace, "memories", "notes.md"), false),
		bashToolUseEntry("bash-1", "origin", "cat "+filepath.Join(workspace, "README.md"), false),
	)

	loaded, err := skillLoadedInTrajectory(transcriptPath, workspace, "document-decision")
	require.NoError(t, err)
	assert.False(t, loaded, "reading unrelated files must not satisfy a skill prerequisite")
}

// TestSkillLoadedInTrajectory_SubagentOwnRecord_ReadCounts combines the two
// fixes: a sub-agent's OWN transcript — where every entry carries isSidechain
// true by convention (see require_test.go's own comment on
// IsSubagentTranscript) — is satisfied by a Read of the skill's own file on
// that record, the same way it is satisfied by the sub-agent's own Skill
// tool_use (TestSkillLoadedInTrajectory_SubagentOwnRecord_SkillCounts).
func TestSkillLoadedInTrajectory_SubagentOwnRecord_ReadCounts(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeSkillFile(t, workspace, "document-decision")

	dir := t.TempDir()
	subPath := filepath.Join(dir, "parent-session", "subagents", "agent-abc.jsonl")
	writeJSONL(t, subPath,
		originEntry("sub-origin", true),
		readToolUseEntry("sub-read", "sub-origin", skillPath, true),
	)

	loaded, err := skillLoadedInTrajectory(subPath, workspace, "document-decision")
	require.NoError(t, err)
	assert.True(t, loaded,
		"a sub-agent's own Read of the skill's own SKILL.md, in its own transcript, must count toward its own require: [{skill}] check")
}

// TestSkillLoadedInTrajectory_SubagentOwnRecord_CatCounts is the Bash-command
// half of the same combination.
func TestSkillLoadedInTrajectory_SubagentOwnRecord_CatCounts(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeSkillFile(t, workspace, "document-decision")

	dir := t.TempDir()
	subPath := filepath.Join(dir, "parent-session", "subagents", "agent-abc.jsonl")
	writeJSONL(t, subPath,
		originEntry("sub-origin", true),
		bashToolUseEntry("sub-bash", "sub-origin", "cat "+skillPath, true),
	)

	loaded, err := skillLoadedInTrajectory(subPath, workspace, "document-decision")
	require.NoError(t, err)
	assert.True(t, loaded,
		"a sub-agent's own `cat` of the skill's own SKILL.md, in its own transcript, must count toward its own require: [{skill}] check")
}

// TestCheckSkill_ReadOnly_EndToEnd drives checkSkill (the Runner's own entry
// point) with a Request carrying a real Workspace, so the path resolution half
// (SkillFilePaths, threaded through Request.Workspace) is exercised at the same
// seam a real dispatch uses — not just the bare trajectory reader.
// sr:proves checks/skill-requirement-reads-the-record
func TestCheckSkill_ReadOnly_EndToEnd(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeSkillFile(t, workspace, "document-decision")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		bashToolUseEntry("bash-1", "origin", "cat "+skillPath, false),
	)

	req := Request{
		Nature:         NatureFileGuard,
		Require:        []declaration.Prerequisite{{Skill: "document-decision"}},
		TranscriptPath: transcriptPath,
		Workspace:      workspace,
		Dir:            "/guard",
		GuardName:      "require-skill-decisions",
	}

	v, err := Runner{}.Run(req)
	require.NoError(t, err)
	assert.False(t, v.Refused, "reason: %s", v.Reason)
}

// TestCheckSkill_NoSkillNoReadEndToEnd_StillRefuses is the unchanged-refusal
// pin at the same end-to-end seam: neither a Skill tool_use nor a read of the
// skill's own file anywhere in the trajectory still refuses, with the combined
// remedy naming both ways to clear it.
// sr:proves checks/skill-requirement-reads-the-record
func TestCheckSkill_NoSkillNoReadEndToEnd_StillRefuses(t *testing.T) {
	workspace := t.TempDir()
	writeSkillFile(t, workspace, "document-decision")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
	)

	req := Request{
		Nature:         NatureFileGuard,
		Require:        []declaration.Prerequisite{{Skill: "document-decision"}},
		TranscriptPath: transcriptPath,
		Workspace:      workspace,
		Dir:            "/guard",
		GuardName:      "require-skill-decisions",
	}

	v, err := Runner{}.Run(req)
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Contains(t, v.Reason, "document-decision")
	assert.Contains(t, v.Reason, "Read")
}
