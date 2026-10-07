// Package codex is the OpenAI Codex CLI implementation of internal/harness: every
// path, file schema, hook wire format and environment variable specific to Codex
// lives here. Importing it registers it (harness.Register); a process runs under it
// when SLOPRAIL_HARNESS=codex or the environment says so (Detect).
//
// The behaviour here is grounded in harness-mocks' recorded Codex runs
// (codex-mock/snapshots/runs/*) and the docs the capability specs cite, not in
// guesses; where the two leave a gap it is said at the code.
//
// What Codex does not have, and so what this adapter cannot give:
//
//   - no worktree hooks (WorktreeRemove never fires);
//   - no ask-user-question tool in `codex exec`;
//   - no skill tool: skills are directories the agent reads (SKILL.md), so there is no
//     "skill loaded" event. A skill counts as loaded when the record shows a shell
//     command reading its SKILL.md (`cat`, `sed`, `head` ..., through the rollout's
//     exec wrapper, internal/harness/codex/record), the check dispatch already makes for
//     Claude Code beside its Skill tool;
//   - no env file a SessionStart hook can export to;
//   - no process registry (Claude's ~/.claude/sessions/<pid>.json), so a session's
//     process is never found and never reported gone (unknown, which callers must not
//     read as gone).
package codex

import (
	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/harness/codex/record"
	"strings"
)

// Harness is the Codex implementation of harness.Harness.
type Harness struct{}

// New returns the Codex harness.
func New() harness.Harness { return Harness{} }

func init() { harness.Register(New()) }

// Name implements harness.Harness.
func (Harness) Name() string { return "codex" }

// Transcripts implements harness.Harness: Codex's rollout files.
func (Harness) Transcripts() harness.Transcripts { return record.Transcripts{} }

// ProcessOfSession implements harness.Harness. Codex records no live-process file
// per session, so none is ever found.
func (Harness) ProcessOfSession(home, sessionID string) (harness.Process, bool) {
	return harness.Process{}, false
}

// ProcessGone implements harness.Harness: it cannot be told.
func (Harness) ProcessGone(home string, p harness.Process) (gone, known bool) { return false, false }

// SessionEnv implements harness.Harness.
func (Harness) SessionEnv(environ []string) []string { return Session(environ) }

// HermeticEnv implements harness.Harness.
func (Harness) HermeticEnv(environ []string) []string { return Hermetic(environ) }

// Detect implements harness.Detector.
func (Harness) Detect(environ []string) bool { return Detect(environ) }

// LocateTranscript implements harness.TranscriptLocator: the session's rollout, found
// by its id, for a payload that names none (a sub-agent's hook names the sub-agent's
// own rollout and not the parent's; an ephemeral session has none at all).
func (Harness) LocateTranscript(in harness.HookInput) string {
	return record.FindRollout(record.ConfigDir(), in.SessionID)
}

// CurrentSessionPath implements harness.CurrentSessionLocator: a shell command Codex
// runs sees its session as CODEX_THREAD_ID (and CODEX_SESSION_ID), and the rollout is
// found by that id.
func (Harness) CurrentSessionPath(_ string, getenv func(string) string) string {
	id := strings.TrimSpace(getenv("CODEX_THREAD_ID"))
	if id == "" {
		id = strings.TrimSpace(getenv("CODEX_SESSION_ID"))
	}
	return record.FindRollout(record.ConfigDir(), id)
}

// ProjectSkillDirs implements harness.SkillDirs: Codex reads a project's skills from
// .agents/skills (a skill is a directory the agent opens, SKILL.md and all).
func (Harness) ProjectSkillDirs() []string { return []string{".agents/skills"} }

// ChildEnvBlocklist implements harness.ChildEnvBlocklist: the session variables a
// judge launched from inside a Codex session must not inherit.
func (Harness) ChildEnvBlocklist() []string {
	return []string{"CODEX_THREAD_ID", "CODEX_SESSION_ID", "CODEX_CI", "CODEX_VERSION", "PLUGIN_ROOT", "PLUGIN_DATA"}
}
