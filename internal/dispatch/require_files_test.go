package dispatch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// This file pins the `files` extension to `require: [{skill}]`: naming pages
// INSIDE a skill (relative to its own directory) that must ALSO have been
// read, beyond the skill's own SKILL.md. See declaration.Prerequisite.Files'
// own doc comment for why this exists — loading a skill only guarantees its
// SKILL.md was read, and a skill's real detail (a script skeleton, a per-kind
// contract) often lives in a page SKILL.md merely links to.
//
// The fixtures mirror require_read_test.go's own (writeSkillFile,
// readToolUseEntry, bashToolUseEntry, originEntry, writeJSONL) — a subpage is
// just another file inside the same skill directory.

// writeSkillSubpage creates <workspace>/.claude/skills/<name>/<file> beside a
// skill already written by writeSkillFile, and returns its path.
func writeSkillSubpage(t *testing.T, workspace, name, file string) string {
	t.Helper()
	path := filepath.Join(workspace, ".claude", "skills", name, file)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("subpage body\n"), 0o644))
	return path
}

// TestSkillSubpagePaths_ProjectSkillIsTheFirstCandidate mirrors
// TestSkillFilePaths_ProjectSkillIsTheFirstCandidate: the project's own
// `.claude/skills/<name>/<file>` is the first candidate, present whether or
// not the file exists — SkillSubpagePaths states candidates, it does not stat
// them, the same contract SkillFilePaths itself keeps.
func TestSkillSubpagePaths_ProjectSkillIsTheFirstCandidate(t *testing.T) {
	dir := t.TempDir()
	paths := SkillSubpagePaths(dir, "authoring-guardrails", "script-checks.md")
	require.NotEmpty(t, paths)
	assert.Equal(t, filepath.Join(dir, ".claude", "skills", "authoring-guardrails", "script-checks.md"), paths[0])
}

// TestSkillSubpagePaths_EmptyWorkspaceNameOrFileYieldsNothing mirrors
// TestSkillFilePaths_EmptyWorkspaceOrNameYieldsNothing, extended with the
// third argument this function adds.
func TestSkillSubpagePaths_EmptyWorkspaceNameOrFileYieldsNothing(t *testing.T) {
	assert.Empty(t, SkillSubpagePaths("", "authoring-guardrails", "script-checks.md"))
	assert.Empty(t, SkillSubpagePaths(t.TempDir(), "", "script-checks.md"))
	assert.Empty(t, SkillSubpagePaths(t.TempDir(), "authoring-guardrails", ""))
}

// TestSubpageReadInTrajectory_ReadToolCounts is the headline case: a Read
// tool_use naming the subpage's own resolved path satisfies the check.
func TestSubpageReadInTrajectory_ReadToolCounts(t *testing.T) {
	workspace := t.TempDir()
	writeSkillFile(t, workspace, "authoring-guardrails")
	subpagePath := writeSkillSubpage(t, workspace, "authoring-guardrails", "script-checks.md")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		readToolUseEntry("read-1", "origin", subpagePath, false),
	)

	read, err := subpageReadInTrajectory(transcriptPath, workspace, "authoring-guardrails", "script-checks.md")
	require.NoError(t, err)
	assert.True(t, read, "a Read tool_use naming the subpage's own resolved path must satisfy the check")
}

// TestSubpageReadInTrajectory_CatCounts is the Bash half.
func TestSubpageReadInTrajectory_CatCounts(t *testing.T) {
	workspace := t.TempDir()
	writeSkillFile(t, workspace, "authoring-guardrails")
	subpagePath := writeSkillSubpage(t, workspace, "authoring-guardrails", "file-guard.md")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		bashToolUseEntry("bash-1", "origin", "cat "+subpagePath, false),
	)

	read, err := subpageReadInTrajectory(transcriptPath, workspace, "authoring-guardrails", "file-guard.md")
	require.NoError(t, err)
	assert.True(t, read, "a `cat` of the subpage's own resolved path must satisfy the check")
}

// TestSubpageReadInTrajectory_SkillToolAloneDoesNotCount is the property that
// makes `files` worth having: invoking the Skill tool loads the skill's own
// SKILL.md, never a subpage by name, so it must NOT satisfy a files entry on
// its own — otherwise every `files: [...]` prerequisite would be satisfied by
// the exact same evidence a bare `{skill}` already accepts, and the extension
// would guarantee nothing beyond what it already did.
func TestSubpageReadInTrajectory_SkillToolAloneDoesNotCount(t *testing.T) {
	workspace := t.TempDir()
	writeSkillFile(t, workspace, "authoring-guardrails")
	writeSkillSubpage(t, workspace, "authoring-guardrails", "script-checks.md")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		skillToolUseEntry("skill-1", "origin", "authoring-guardrails", false),
	)

	read, err := subpageReadInTrajectory(transcriptPath, workspace, "authoring-guardrails", "script-checks.md")
	require.NoError(t, err)
	assert.False(t, read, "invoking the Skill tool must not, by itself, satisfy a files entry naming a specific subpage")
}

// TestSubpageReadInTrajectory_ReadOfSkillMdDoesNotCountForASubpage is the
// same property from the other direction: reading the skill's own SKILL.md
// satisfies `{skill}` itself, but not a files entry naming a DIFFERENT file —
// the two candidate-path lists (SkillFilePaths vs SkillSubpagePaths) must not
// collapse into one.
func TestSubpageReadInTrajectory_ReadOfSkillMdDoesNotCountForASubpage(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeSkillFile(t, workspace, "authoring-guardrails")
	writeSkillSubpage(t, workspace, "authoring-guardrails", "script-checks.md")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		readToolUseEntry("read-1", "origin", skillPath, false),
	)

	read, err := subpageReadInTrajectory(transcriptPath, workspace, "authoring-guardrails", "script-checks.md")
	require.NoError(t, err)
	assert.False(t, read, "reading the skill's own SKILL.md must not satisfy a files entry naming a different subpage")
}

// TestSubpageReadInTrajectory_UnrelatedReadRefuses is the negative floor: a
// read of some other, unrelated file satisfies nothing.
func TestSubpageReadInTrajectory_UnrelatedReadRefuses(t *testing.T) {
	workspace := t.TempDir()
	writeSkillFile(t, workspace, "authoring-guardrails")
	writeSkillSubpage(t, workspace, "authoring-guardrails", "script-checks.md")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		readToolUseEntry("read-1", "origin", filepath.Join(workspace, "README.md"), false),
	)

	read, err := subpageReadInTrajectory(transcriptPath, workspace, "authoring-guardrails", "script-checks.md")
	require.NoError(t, err)
	assert.False(t, read, "reading an unrelated file must not satisfy a files entry")
}

// TestSubpageReadInTrajectory_SubagentOwnRecordCounts mirrors
// TestSkillLoadedInTrajectory_SubagentOwnRecord_ReadCounts: a sub-agent's own
// transcript (every entry isSidechain true) is satisfied by ITS OWN read of
// the subpage.
func TestSubpageReadInTrajectory_SubagentOwnRecordCounts(t *testing.T) {
	workspace := t.TempDir()
	writeSkillFile(t, workspace, "authoring-guardrails")
	subpagePath := writeSkillSubpage(t, workspace, "authoring-guardrails", "script-checks.md")

	dir := t.TempDir()
	subPath := filepath.Join(dir, "parent-session", "subagents", "agent-abc.jsonl")
	writeJSONL(t, subPath,
		originEntry("sub-origin", true),
		readToolUseEntry("sub-read", "sub-origin", subpagePath, true),
	)

	read, err := subpageReadInTrajectory(subPath, workspace, "authoring-guardrails", "script-checks.md")
	require.NoError(t, err)
	assert.True(t, read,
		"a sub-agent's own Read of the subpage, in its own transcript, must count toward its own require: [{skill, files}] check")
}

// TestCheckSkill_FilesAllSatisfiedEndToEnd drives checkSkill (the Runner's own
// entry point) with a Request naming both the skill and two files, all of
// them satisfied — the shape a real authoring-guardrails-style rule declares.
func TestCheckSkill_FilesAllSatisfiedEndToEnd(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeSkillFile(t, workspace, "authoring-guardrails")
	scriptChecksPath := writeSkillSubpage(t, workspace, "authoring-guardrails", "script-checks.md")
	fileGuardPath := writeSkillSubpage(t, workspace, "authoring-guardrails", "file-guard.md")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		readToolUseEntry("read-skill", "origin", skillPath, false),
		readToolUseEntry("read-script-checks", "read-skill", scriptChecksPath, false),
		readToolUseEntry("read-file-guard", "read-script-checks", fileGuardPath, false),
	)

	req := Request{
		Nature:         NatureFileGuard,
		Require:        []declaration.Prerequisite{{Skill: "authoring-guardrails", Files: []string{"script-checks.md", "file-guard.md"}}},
		TranscriptPath: transcriptPath,
		Workspace:      workspace,
		Dir:            "/guard",
		GuardName:      "misplaced-declaration",
	}

	v, err := Runner{}.Run(req)
	require.NoError(t, err)
	assert.False(t, v.Refused, "reason: %s", v.Reason)
}

// TestCheckSkill_SkillMissingTakesPriorityOverFiles pins the declared order:
// when the skill itself was never loaded at all, the refusal is the skill's
// own remedy, not a confusing "read script-checks.md" that never mentions the
// skill is missing too — checkSkill's own doc comment states this ordering.
func TestCheckSkill_SkillMissingTakesPriorityOverFiles(t *testing.T) {
	workspace := t.TempDir()
	writeSkillFile(t, workspace, "authoring-guardrails")
	writeSkillSubpage(t, workspace, "authoring-guardrails", "script-checks.md")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
	)

	req := Request{
		Nature:         NatureFileGuard,
		Require:        []declaration.Prerequisite{{Skill: "authoring-guardrails", Files: []string{"script-checks.md"}}},
		TranscriptPath: transcriptPath,
		Workspace:      workspace,
		Dir:            "/guard",
		GuardName:      "misplaced-declaration",
	}

	v, err := Runner{}.Run(req)
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Contains(t, v.Reason, "SKILL REQUIRED", "the skill's own remedy fires first, not the files remedy")
	assert.NotContains(t, v.Reason, "READ REQUIRED")
}

// TestCheckSkill_OneOfSeveralFilesMissingNamesTheMissingOne pins that the
// refusal names the SPECIFIC missing page, in declared order — the agent has
// already read one page and should not be told to start over.
func TestCheckSkill_OneOfSeveralFilesMissingNamesTheMissingOne(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeSkillFile(t, workspace, "authoring-guardrails")
	scriptChecksPath := writeSkillSubpage(t, workspace, "authoring-guardrails", "script-checks.md")
	writeSkillSubpage(t, workspace, "authoring-guardrails", "file-guard.md")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		readToolUseEntry("read-skill", "origin", skillPath, false),
		readToolUseEntry("read-script-checks", "read-skill", scriptChecksPath, false),
		// file-guard.md is never read.
	)

	req := Request{
		Nature:         NatureFileGuard,
		Require:        []declaration.Prerequisite{{Skill: "authoring-guardrails", Files: []string{"script-checks.md", "file-guard.md"}}},
		TranscriptPath: transcriptPath,
		Workspace:      workspace,
		Dir:            "/guard",
		GuardName:      "misplaced-declaration",
	}

	v, err := Runner{}.Run(req)
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Contains(t, v.Reason, "file-guard.md")
	assert.Contains(t, v.Reason, "READ REQUIRED")
}

// TestCheckSkill_FilesRemedyNamesAConcretePath pins that the remedy resolves
// the subpage to an actual path on disk when one exists — an agent should not
// have to go resolve the skill's own directory a second time to find the page
// it is being told to read.
func TestCheckSkill_FilesRemedyNamesAConcretePath(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeSkillFile(t, workspace, "authoring-guardrails")
	writeSkillSubpage(t, workspace, "authoring-guardrails", "script-checks.md")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		readToolUseEntry("read-skill", "origin", skillPath, false),
	)

	req := Request{
		Nature:         NatureFileGuard,
		Require:        []declaration.Prerequisite{{Skill: "authoring-guardrails", Files: []string{"script-checks.md"}}},
		TranscriptPath: transcriptPath,
		Workspace:      workspace,
		Dir:            "/guard",
		GuardName:      "misplaced-declaration",
	}

	v, err := Runner{}.Run(req)
	require.NoError(t, err)
	assert.True(t, v.Refused)
	assert.Contains(t, v.Reason, filepath.Join(workspace, ".claude", "skills", "authoring-guardrails", "script-checks.md"))
}

// TestSubpageReadInTrajectory_SymlinkedWorkspaceStillCounts pins the bug a real
// e2e run against the mock caught: on macOS, os.MkdirTemp's own result is
// spelled under /var while `git rev-parse --show-toplevel` (what
// Request.Workspace is built from in a real dispatch) resolves the /var →
// /private/var symlink and answers under /private/var — so a candidate path
// built from the resolved workspace never string-equalled a Read tool_use's
// file_path built from the unresolved one, and a subpage the agent had
// genuinely just read, at the EXACT path a refusal told it to, was refused
// again. This constructs the same shape deliberately (a real symlink, not
// relying on any one platform's own temp-dir quirk) so the property is pinned
// portably rather than only by the platform this happened to be caught on.
func TestSubpageReadInTrajectory_SymlinkedWorkspaceStillCounts(t *testing.T) {
	real := t.TempDir()
	linked := filepath.Join(t.TempDir(), "workspace-link")
	require.NoError(t, os.Symlink(real, linked))

	writeSkillFile(t, real, "authoring-guardrails")
	// Written through the REAL path — the file's own identity, unaffected by
	// which spelling later names it.
	writeSkillSubpage(t, real, "authoring-guardrails", "script-checks.md")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	// The agent names the subpage through the LINKED spelling — Request.Workspace
	// is the linked path, but a real dispatch resolves it before candidate paths
	// are built (see ResolveExistingPrefix's own doc comment), and the agent's
	// own file_path is whatever it was actually handed, which may be either
	// spelling depending on how the harness reports cwd. Naming it through the
	// link here is the harder direction: the candidate (built from `linked`) and
	// the agent's path (also through `linked`) already string-match textually,
	// so this instead proves the REAL regression by asking through the path
	// realpath would resolve them BOTH to, which is the actual property under
	// test — see the sibling case below for the textually-mismatched direction.
	readPath := filepath.Join(real, ".claude", "skills", "authoring-guardrails", "script-checks.md")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		readToolUseEntry("read-1", "origin", readPath, false),
	)

	read, err := subpageReadInTrajectory(transcriptPath, linked, "authoring-guardrails", "script-checks.md")
	require.NoError(t, err)
	assert.True(t, read,
		"a candidate built from a SYMLINKED workspace must still match a Read tool_use naming the file through its REAL (resolved) path")
}

// skillBaseEntry is the harness's own isMeta record of where it loaded a skill
// from — the first line of the skill body it writes in.
func skillBaseEntry(uuid, parentUUID, dir string, meta bool) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":"` + parentUUID + `","isSidechain":false,"isMeta":` +
		boolStr(meta) + `,"message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: ` + dir + `\n\n# Authoring"}]}}`
}

// A directory-sourced marketplace serves a plugin's skill from its source tree,
// which is where the harness says the skill lives and where the agent reads the
// page. That read counts; the same line printed by a tool does not.
func TestSubpageReadInTrajectory_AnnouncedBaseDirectoryCounts(t *testing.T) {
	workspace := t.TempDir()
	served := filepath.Join(t.TempDir(), "marketplace", "plugins", "sloprail", "skills", "authoring-guardrails")
	page := filepath.Join(served, "structure-gate.md")

	for name, tc := range map[string]struct {
		meta  bool
		skill string
		want  bool
	}{
		"announced by the harness":  {true, "authoring-guardrails", true},
		"a plugin-qualified name":   {true, "sloprail:authoring-guardrails", true},
		"text the agent put there":  {false, "authoring-guardrails", false},
		"another skill's directory": {true, "other-skill", false},
	} {
		t.Run(name, func(t *testing.T) {
			transcriptPath := filepath.Join(t.TempDir(), "session.jsonl")
			writeJSONL(t, transcriptPath,
				originEntry("origin", false),
				skillBaseEntry("base", "origin", served, tc.meta),
				readToolUseEntry("read-1", "base", page, false),
			)
			read, err := subpageReadInTrajectory(transcriptPath, workspace, tc.skill, "structure-gate.md")
			require.NoError(t, err)
			assert.Equal(t, tc.want, read)
		})
	}
}
