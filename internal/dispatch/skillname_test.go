package dispatch

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/transcript"
)

// This file pins the plugin-qualified skill name bug: a real Skill tool_use
// for a PLUGIN's skill is invoked plugin-qualified (`sloprail:
// authoring-guardrails`), never bare, while a `require: [{skill: <name>}]`
// declaration always names the bare skill — the same name SkillFilePaths
// resolves against both a project's own `.claude/skills/<name>/` and a
// plugin's `<pluginRoot>/skills/<name>/`. Before this fix, checkSkill/
// skillLoadedInTrajectory compared the two literally and never matched, so a
// plugin-shipped `require: [{skill}]` refused an agent that had genuinely
// just invoked the Skill tool — twice, in the real trajectory that found
// this (a real haiku sr-eval run against fresh-plugin-rules-first).
//
// A project's OWN skill is invoked bare, confirmed against real transcripts
// (both forms occur; a project skill is never seen prefixed) — so the fix
// must accept EITHER spelling, not switch to requiring the qualified one.

// TestSkillNameMatches_BareNameMatchesBare pins the ordinary, still-working
// case: a project's own skill, invoked and declared bare.
func TestSkillNameMatches_BareNameMatchesBare(t *testing.T) {
	call := toolCallWithSkill(t, "document-decision")
	assert.True(t, skillNameMatches(call, "document-decision"))
}

// TestSkillNameMatches_PluginQualifiedMatchesBareDeclaration is the fix: a
// plugin-qualified Skill tool_use (`sloprail:authoring-guardrails`) matches a
// `require: [{skill: authoring-guardrails}]` naming the bare skill.
func TestSkillNameMatches_PluginQualifiedMatchesBareDeclaration(t *testing.T) {
	call := toolCallWithSkill(t, "sloprail:authoring-guardrails")
	assert.True(t, skillNameMatches(call, "authoring-guardrails"),
		"a plugin-qualified Skill tool_use must satisfy a require naming the bare skill")
}

// TestSkillNameMatches_UnrelatedSkillDoesNotMatch is the negative control: a
// DIFFERENT skill, plugin-qualified or not, must not match — the fix accepts
// the plugin prefix, it does not stop checking the skill name.
func TestSkillNameMatches_UnrelatedSkillDoesNotMatch(t *testing.T) {
	assert.False(t, skillNameMatches(toolCallWithSkill(t, "sloprail:some-other-skill"), "authoring-guardrails"))
	assert.False(t, skillNameMatches(toolCallWithSkill(t, "authoring-guardrails-lookalike"), "authoring-guardrails"))
}

// TestCheckSkill_PluginQualifiedSkillCallEndToEnd drives checkSkill (the
// Runner's own entry point) with a REAL plugin-qualified Skill tool_use in
// the trajectory — the exact shape the sr-eval run that found this bug
// produced — and requires it to satisfy a bare `require: [{skill}]`.
func TestCheckSkill_PluginQualifiedSkillCallEndToEnd(t *testing.T) {
	workspace := t.TempDir()
	writeSkillFile(t, workspace, "authoring-guardrails")

	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, transcriptPath,
		originEntry("origin", false),
		skillToolUseEntry("skill-1", "origin", "sloprail:authoring-guardrails", false),
	)

	req := Request{
		Nature:         NatureFileGuard,
		Require:        []declaration.Prerequisite{{Skill: "authoring-guardrails"}},
		TranscriptPath: transcriptPath,
		Workspace:      workspace,
		Dir:            "/guard",
		GuardName:      "read-structure-gate-doc",
	}

	v, err := Runner{}.Run(req)
	require.NoError(t, err)
	assert.False(t, v.Refused, "a real plugin-qualified Skill tool_use must satisfy a bare require: [{skill}], reason: %s", v.Reason)
}

// toolCallWithSkill builds a transcript.ToolCall for a Skill invocation named
// skillName, the unit under skillNameMatches — reusing skillToolUseEntry's
// own JSONL shape via a throwaway transcript so both tests read the exact
// entry format a real trajectory carries.
func toolCallWithSkill(t *testing.T, skillName string) transcript.ToolCall {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	writeJSONL(t, path, originEntry("origin", false), skillToolUseEntry("s1", "origin", skillName, false))
	entries, err := transcript.Read(path)
	require.NoError(t, err)
	for _, e := range entries {
		for _, call := range transcript.ToolCalls(e) {
			if call.Name == "Skill" {
				return call
			}
		}
	}
	t.Fatalf("no Skill tool_use found in the fixture transcript")
	return transcript.ToolCall{}
}
