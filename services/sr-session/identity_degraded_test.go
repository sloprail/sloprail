package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeProjectRecord writes a transcript under <configDir>/projects/<dir>/<name>.jsonl.
func writeProjectRecord(t *testing.T, configDir, dir, name string, lines ...string) string {
	t.Helper()
	d := filepath.Join(configDir, "projects", dir)
	require.NoError(t, os.MkdirAll(d, 0o755))
	path := filepath.Join(d, name+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return path
}

// orphanedContinuation writes a continuation whose predecessor is gone and
// returns its path. sid is the harness session id the records carry.
func orphanedContinuation(t *testing.T, cfg, name, sid, lp string) string {
	t.Helper()
	return writeProjectRecord(t, cfg, "-p", name,
		`{"parentUuid":null,"logicalParentUuid":"`+lp+`","type":"system","subtype":"compact_boundary","uuid":"continuation-root","timestamp":"2026-07-18T16:34:36.604Z","sessionId":"`+sid+`"}`,
		`{"parentUuid":"continuation-root","type":"assistant","uuid":"a","timestamp":"2026-07-18T16:35:00.000Z","sessionId":"`+sid+`"}`,
	)
}

// TestDegradedIdentityIsReportedAtSessionStartOncePerSession: a continuation
// whose predecessor is gone keeps working under its own root; the person is
// told at SessionStart — on stdout, the channel that is seen — once per
// harness session. A pre-tool or Stop hook says nothing and leaves no marker,
// so it cannot use up the one report on a channel nobody reads.
func TestDegradedIdentityIsReportedAtSessionStartOncePerSession(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	tree := initRepo(t)
	commitFile(t, tree, "seed.txt", "seed")

	path := orphanedContinuation(t, cfg, "resumed", "resumed", "deleted-long-ago")
	p := HookPayload{TranscriptPath: path, SessionID: "resumed", Cwd: tree, Source: "resume"}

	id, err := stableIdentity(p)
	require.NoError(t, err, "a deleted predecessor must leave the session an identity, not none")
	assert.Equal(t, "continuation-root", id.ID)
	require.Error(t, id.Degraded)

	// A tool call first: silent, and it must not consume the report.
	_, preErr := runHook(t, newSessionPreToolCmd(), HookPayload{TranscriptPath: path, SessionID: "resumed", Cwd: tree,
		ToolName: "Bash", ToolInput: []byte(`{"command":"true"}`)})
	assert.NotContains(t, preErr, "sloprail: identity:")

	out, errOut := runHook(t, newSessionStartCmd(), p)
	assert.Contains(t, out, "sloprail: identity:", "SessionStart reports on stdout, which is seen")
	assert.Contains(t, out, "earlier transcript is gone")
	assert.Contains(t, out, "continuation-root")
	assert.Contains(t, errOut, "sloprail: identity:")

	again, _ := runHook(t, newSessionStartCmd(), HookPayload{TranscriptPath: path, SessionID: "resumed", Cwd: tree, Source: "compact"})
	assert.NotContains(t, again, "sloprail: identity:", "the same session is told once")

	// A fork of the same continuation shares the fallback id and its state
	// directory, but is a different session, and is told too.
	fork := orphanedContinuation(t, cfg, "a-fork", "a-fork", "deleted-long-ago")
	forkOut, _ := runHook(t, newSessionStartCmd(), HookPayload{TranscriptPath: fork, SessionID: "a-fork", Cwd: tree, Source: "resume"})
	assert.Contains(t, forkOut, "sloprail: identity:", "each harness session is told, not each identity")

	// And the store opens under it, so state persists across hooks.
	store, err := openEngineState(p)
	require.NoError(t, err)
	require.NoError(t, store.Close())
}

// TestDegradedByARingDoesNotSayGone: a malformed chain that loops is not a
// predecessor that was deleted, and the notice does not say it was.
func TestDegradedByARingDoesNotSayGone(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	a := writeProjectRecord(t, cfg, "-p", "ring-a",
		`{"parentUuid":null,"logicalParentUuid":"in-b","type":"system","uuid":"root-a","sessionId":"ring-a"}`,
		`{"parentUuid":"root-a","type":"assistant","uuid":"in-a","sessionId":"ring-a"}`)
	writeProjectRecord(t, cfg, "-p", "ring-b",
		`{"parentUuid":null,"logicalParentUuid":"in-a","type":"system","uuid":"root-b","sessionId":"ring-b"}`,
		`{"parentUuid":"root-b","type":"assistant","uuid":"in-b","sessionId":"ring-b"}`)
	p := HookPayload{TranscriptPath: a, SessionID: "ring-a", Cwd: t.TempDir()}
	id, err := stableIdentity(p)
	require.NoError(t, err)
	var out, errOut bytes.Buffer
	noteDegradedIdentity(&out, &errOut, p, id)
	assert.Contains(t, out.String(), "loops")
	assert.NotContains(t, out.String(), "gone")
}

// TestResolvedIdentityIsNotReported: the ordinary case says nothing.
func TestResolvedIdentityIsNotReported(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	path := writeProjectRecord(t, cfg, "-p", "fresh",
		`{"parentUuid":null,"type":"user","uuid":"origin","sessionId":"fresh"}`)
	p := HookPayload{TranscriptPath: path, Cwd: t.TempDir()}

	id, err := stableIdentity(p)
	require.NoError(t, err)
	var out, errOut bytes.Buffer
	noteDegradedIdentity(&out, &errOut, p, id)
	assert.Empty(t, out.String())
	assert.Empty(t, errOut.String())
}

// TestResumeFromAnotherDirectoryResolves is the SessionStart:resume payload a
// real session resumed from a different directory got: transcript_path under
// the NEW directory's project folder, where nothing was ever written, while the
// record stayed where the session began.
func TestResumeFromAnotherDirectoryResolves(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	const sid = "06f7418e-0000-4000-8000-000000000002"
	writeProjectRecord(t, cfg, "-repo--claude-worktrees-feature", sid,
		`{"parentUuid":null,"type":"attachment","uuid":"origin","sessionId":"`+sid+`"}`)
	reported := filepath.Join(cfg, "projects", "-repo", sid+".jsonl")

	id, err := stableID(HookPayload{TranscriptPath: reported, SessionID: sid, Cwd: t.TempDir()})
	require.NoError(t, err, "a resumed session's record is where it began, whatever directory it was resumed from")
	assert.Equal(t, "origin", id)
}

// TestStartupDoesNotRelocate: a fresh session's record does not exist at
// SessionStart by design, and a fixed --session-id reused across directories
// would otherwise find another project's transcript by the same name.
func TestStartupDoesNotRelocate(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	const sid = "fixed-session-id"
	writeProjectRecord(t, cfg, "-another-project", sid,
		`{"parentUuid":null,"type":"user","uuid":"someone-elses","sessionId":"`+sid+`"}`)
	reported := filepath.Join(cfg, "projects", "-this-project", sid+".jsonl")

	got, err := HookPayload{TranscriptPath: reported, SessionID: sid, Source: "startup"}.Record()
	require.NoError(t, err)
	assert.Equal(t, reported, got, "startup must not adopt another project's transcript")

	got, err = HookPayload{TranscriptPath: reported, SessionID: sid, Source: "resume"}.Record()
	require.NoError(t, err)
	assert.NotEqual(t, reported, got, "a resume does look for the record where the session began")
}

// TestClearDoesNotRelocate: "clear" is a fresh session too — the previous
// conversation is discarded — so it carries the identical fixed---session-id
// hazard "startup" does, and must be skipped the same way.
func TestClearDoesNotRelocate(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	const sid = "fixed-session-id-2"
	writeProjectRecord(t, cfg, "-another-project", sid,
		`{"parentUuid":null,"type":"user","uuid":"someone-elses","sessionId":"`+sid+`"}`)
	reported := filepath.Join(cfg, "projects", "-this-project", sid+".jsonl")

	got, err := HookPayload{TranscriptPath: reported, SessionID: sid, Source: "clear"}.Record()
	require.NoError(t, err)
	assert.Equal(t, reported, got, "clear must not adopt another project's transcript")
}

// TestSubagentOfAResumedSessionIsRelocated: a sub-agent's record nests under
// its session's, so in a session resumed from another directory the reported
// agent_transcript_path is missing too. It resolves to the same identity as the
// sub-agent's calls reconstructed from agent_id against the relocated session
// record — one sub-agent, one identity.
func TestSubagentOfAResumedSessionIsRelocated(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	const sid = "06f7418e-0000-4000-8000-000000000004"
	writeProjectRecord(t, cfg, "-repo--wt", sid,
		`{"parentUuid":null,"type":"user","uuid":"origin","sessionId":"`+sid+`"}`)
	writeProjectRecord(t, cfg, "-repo--wt", sid+"/subagents/agent-a1",
		`{"parentUuid":null,"type":"user","uuid":"sub-origin","sessionId":"`+sid+`","isSidechain":true,"agentId":"a1"}`)
	reportedRoot := filepath.Join(cfg, "projects", "-repo", sid+".jsonl")
	reportedSub := filepath.Join(cfg, "projects", "-repo", sid, "subagents", "agent-a1.jsonl")

	byPath, err := stableID(HookPayload{TranscriptPath: reportedRoot, AgentTranscriptPath: reportedSub, AgentID: "a1", SessionID: sid, Cwd: t.TempDir()})
	require.NoError(t, err)
	byID, err := stableID(HookPayload{TranscriptPath: reportedRoot, AgentID: "a1", SessionID: sid, Cwd: t.TempDir()})
	require.NoError(t, err)
	assert.Equal(t, "sub-origin", byPath)
	assert.Equal(t, byPath, byID)
}

// TestDegradedByAnUnreadableSiblingIsReportedNotAsGone: a continuation whose
// predecessor is genuinely unreachable because an unrelated file in its
// project directory could not be read (not because anything was deleted)
// degrades and is reported the same way — once, at SessionStart, on stdout —
// but the notice says its predecessor could not be REACHED, not that it is
// gone: those are different situations, and a person reading it should not be
// told a file was deleted when it was actually sitting there unreadable.
func TestDegradedByAnUnreadableSiblingIsReportedNotAsGone(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	tree := initRepo(t)
	commitFile(t, tree, "seed.txt", "seed")

	const sid = "unreadable-sib"
	huge := `{"type":"assistant","uuid":"big","parentUuid":"whatever","sessionId":"other","message":{"content":"` +
		strings.Repeat("x", 16*1024*1024+1) + `"}}`
	writeProjectRecord(t, cfg, "-p", "the-only-predecessor",
		huge,
		`{"parentUuid":null,"type":"user","uuid":"origin","sessionId":"other"}`,
	)
	path := writeProjectRecord(t, cfg, "-p", sid,
		`{"parentUuid":null,"logicalParentUuid":"origin","type":"system","subtype":"compact_boundary","uuid":"continuation-root","sessionId":"`+sid+`"}`,
	)
	p := HookPayload{TranscriptPath: path, SessionID: sid, Cwd: tree, Source: "resume"}

	id, err := stableIdentity(p)
	require.NoError(t, err, "an unreadable sibling must not abort the resolution")
	assert.Equal(t, "continuation-root", id.ID)
	require.Error(t, id.Degraded)
	assert.NotEmpty(t, id.UnreadableSiblings)

	var out, errOut bytes.Buffer
	noteDegradedIdentity(&out, &errOut, p, id)
	assert.Contains(t, out.String(), "could not be reached")
	assert.NotContains(t, out.String(), "is gone")
}
