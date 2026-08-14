package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/transcript"
)

func TestPayloadTranscript_PathOnThePayloadWins(t *testing.T) {
	// The harness handing the path over beats any assumption about where it
	// puts things, so it is used verbatim even when an id is also present.
	p := HookPayload{TranscriptPath: "/given/path.jsonl", SessionID: "sid", Cwd: "/proj"}
	got, err := p.record()
	require.NoError(t, err)
	assert.Equal(t, "/given/path.jsonl", got)
}

func TestPayloadTranscript_ReconstructedFromTheSessionID(t *testing.T) {
	// The SessionStart payload carries the session id and the working directory
	// and no path at all — and session start is the one moment the baseline
	// most needs recording. Treating the absent field as "no record" left every
	// session taking its starting point at the END of the first cycle instead,
	// by which time an agent that had committed had already moved it.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	p := HookPayload{SessionID: "sess-1", Cwd: "/proj"}
	want := filepath.Join(transcript.ProjectDir(cfg, "/proj"), "sess-1.jsonl")
	got, err := p.record()
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestPayloadTranscript_NothingToGoOnIsEmpty(t *testing.T) {
	// No path and no id is genuinely no record, and the caller says so rather
	// than reading a file it invented.
	got, err := HookPayload{Cwd: "/proj"}.record()
	require.NoError(t, err, "naming nothing is the caller's own omission, not a refusal")
	assert.Empty(t, got)
}

func TestPayloadTranscript_ASessionIDWithASeparatorIsRefused(t *testing.T) {
	// F8. The reported session id is joined onto the project directory to guess
	// a filename, and filepath.Join cleans ".." AFTER the concatenation — so
	// "../-other-project/secret" lands in another project's directory and
	// resolves that conversation's identity with no error at all. The engine
	// keys its baseline and its read mark on what comes back, so the traversal
	// reaches another project's state.
	//
	// Refused rather than repaired. filepath.Base would make the path safe and
	// the anomaly invisible; a session id with a slash in it is not a session
	// id, and saying so is the only honest answer.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	// The file the traversal would have reached, holding another conversation's
	// origin — so a test that passes cannot be passing because nothing is there.
	other := filepath.Join(cfg, "projects", "-some-other-project")
	require.NoError(t, os.MkdirAll(other, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(other, "secret.jsonl"),
		[]byte(`{"type":"user","uuid":"THEIR-ORIGIN","parentUuid":null,"sessionId":"secret"}`+"\n"), 0o644))

	p := HookPayload{SessionID: "../-some-other-project/secret", Cwd: "/proj"}

	_, err := p.record()
	require.Error(t, err)
	assert.ErrorIs(t, err, errNotASessionID)

	// And the identity walk that reads it refuses too, rather than handing back
	// the other conversation's id.
	id, err := stableID(p)
	require.Error(t, err)
	assert.Empty(t, id, "another project's identity must never come back")
	assert.NotEqual(t, "THEIR-ORIGIN", id)
}

func TestPayloadTranscript_ABackslashIsRefusedToo(t *testing.T) {
	// Windows separates on both slashes, and a check on filepath.Separator alone
	// would let one of them through on every other platform.
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, err := HookPayload{SessionID: `..\other\secret`, Cwd: "/proj"}.record()
	assert.ErrorIs(t, err, errNotASessionID)
}

// "." and ".." carry no separator, so they are not refused as names — and they
// need no clause of their own, because the suffix defuses them before the join:
// "." becomes "..jsonl" and ".." becomes "...jsonl", both ordinary filenames
// inside the project directory. This pins that they stay INSIDE it, which is
// the property a traversal guard is actually for; refusing them as well would
// be a clause that can never fire.
func TestPayloadTranscript_DotIDsNameAFileInsideTheProjectDir(t *testing.T) {
	cfg := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	dir := transcript.ProjectDir(cfg, cwd)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	for id, want := range map[string]string{".": "..jsonl", "..": "...jsonl"} {
		// No file is there, so the guess resolves to nothing — but what
		// matters is WHERE it pointed.
		got, err := HookPayload{SessionID: id, Cwd: cwd}.record()
		require.NoError(t, err, "no separator, so not refused as a name")
		assert.Equal(t, filepath.Join(dir, want), got,
			"session id %q must stay inside the project directory", id)
		assert.Equal(t, dir, filepath.Dir(got), "it must not climb out")
	}
}

func TestPayloadTranscript_AGuessLandingOnAnotherConversationIsRefused(t *testing.T) {
	// F8's second half, and the one the "a wrong guess fails loudly" reasoning
	// did not cover: it only ever caught a file that does NOT exist.
	//
	// A guessed filename that exists but was written by a different conversation
	// resolved silently and handed back that conversation's origin — no
	// traversal needed, just a name that collides. Claude Code stamps every
	// record with the session it was written under, so the file is asked who it
	// belongs to before its identity is used.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	dir := transcript.ProjectDir(cfg, "/proj")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "guessed.jsonl"),
		[]byte(`{"type":"user","uuid":"UNRELATED-ORIGIN","parentUuid":null,"sessionId":"someone-else"}`+"\n"), 0o644))

	p := HookPayload{SessionID: "guessed", Cwd: "/proj"}

	_, err := p.record()
	require.Error(t, err)
	assert.ErrorIs(t, err, transcript.ErrWrongSession)

	id, err := stableID(p)
	require.Error(t, err)
	assert.NotEqual(t, "UNRELATED-ORIGIN", id,
		"a guess landing on another conversation must not become this session's identity")
}

func TestPayloadTranscript_AGuessLandingOnItsOwnRecordResolves(t *testing.T) {
	// The cross-check must not refuse the case it was added around. A SessionStart
	// payload naming a session whose file really is that session's resolves, and
	// its identity comes back.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	dir := transcript.ProjectDir(cfg, "/proj")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	want := filepath.Join(dir, "mine.jsonl")
	require.NoError(t, os.WriteFile(want,
		[]byte(`{"type":"user","uuid":"MY-ORIGIN","parentUuid":null,"sessionId":"mine"}`+"\n"), 0o644))

	p := HookPayload{SessionID: "mine", Cwd: "/proj"}
	got, err := p.record()
	require.NoError(t, err)
	assert.Equal(t, want, got)

	id, err := stableID(p)
	require.NoError(t, err)
	assert.Equal(t, "MY-ORIGIN", id)
}

func TestPayloadTranscript_AGuessLandingOnAForkedRecordResolves(t *testing.T) {
	// F9, driven through the path that actually runs it. The SessionStart payload
	// shape — a session id and a working directory, no transcript path — is the
	// one place session_id.go says failing to resolve costs the session its
	// baseline.
	//
	// The file is a re-forked transcript: Claude Code kept writing into it after
	// the fork, so the records already there carry the OLD id and this session's
	// id appears only at the end. Reading the first id refused it, which broke the
	// re-fork the identity walk exists to survive — the guess was correct, the
	// file was this session's, and the check said otherwise.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	dir := transcript.ProjectDir(cfg, "/proj")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "forked.jsonl"),
		[]byte(`{"type":"user","uuid":"MY-ORIGIN","parentUuid":null,"sessionId":"old-session"}`+"\n"+
			`{"type":"user","uuid":"B","parentUuid":"MY-ORIGIN","sessionId":"old-session"}`+"\n"+
			`{"type":"user","uuid":"C","parentUuid":"B","sessionId":"forked"}`+"\n"), 0o644))

	p := HookPayload{SessionID: "forked", Cwd: "/proj"}

	got, err := p.record()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "forked.jsonl"), got)

	// The identity comes back, and it is the ORIGIN — the thing that survives the
	// fork — rather than either session id.
	id, err := stableID(p)
	require.NoError(t, err)
	assert.Equal(t, "MY-ORIGIN", id)
}

func TestPayloadTranscript_ARecordCarryingNoSessionIDStillResolves(t *testing.T) {
	// The limit of the cross-check, stated as a test rather than only as a
	// comment. The field is observed rather than promised, so a file that names
	// no session is accepted — what is caught is a file naming a DIFFERENT
	// session, which is a positive disagreement, not an absence of evidence.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	dir := transcript.ProjectDir(cfg, "/proj")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plain.jsonl"),
		[]byte(`{"type":"user","uuid":"ORIGIN","parentUuid":null}`+"\n"), 0o644))

	id, err := stableID(HookPayload{SessionID: "plain", Cwd: "/proj"})
	require.NoError(t, err)
	assert.Equal(t, "ORIGIN", id)
}

func TestPayloadTranscript_AGivenPathIsNeverCrossChecked(t *testing.T) {
	// The harness handing the path over is authoritative — it is not a guess, so
	// there is nothing to check it against. Only a name this process invented
	// has to justify itself.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	dir := transcript.ProjectDir(cfg, "/proj")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	given := filepath.Join(dir, "handed-over.jsonl")
	require.NoError(t, os.WriteFile(given,
		[]byte(`{"type":"user","uuid":"ORIGIN","parentUuid":null,"sessionId":"a-different-name"}`+"\n"), 0o644))

	p := HookPayload{TranscriptPath: given, SessionID: "a-different-name", Cwd: "/proj"}
	got, err := p.record()
	require.NoError(t, err)
	assert.Equal(t, given, got)
}

func TestPayloadTranscript_NoRecordIsItsOwnAnswer(t *testing.T) {
	// Distinct from a refusal. Nothing was named, which is the caller's own
	// omission and not a session being pointed somewhere it must not read — and
	// both callers phrase it differently because of that.
	got, err := HookPayload{Cwd: "/proj"}.record()
	require.NoError(t, err, "naming nothing is not a refusal")
	assert.Empty(t, got)
}

func TestPayloadTranscript_AGuessAtAFileNotWrittenYetStillResolves(t *testing.T) {
	// SessionStart fires as the session begins, and the harness may not have
	// written the record yet. The cross-check must not turn "not there yet" into
	// a refusal — the path is returned and whoever reads it reports the missing
	// file with its own path in it, which is the loud failure that was always
	// the answer for a guess landing on nothing.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	p := HookPayload{SessionID: "not-yet", Cwd: "/proj"}
	want := filepath.Join(transcript.ProjectDir(cfg, "/proj"), "not-yet.jsonl")
	got, err := p.record()
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// seedTree writes a one-record transcript carrying both fields the guess checks
// look at: which session wrote it, and which tree it ran in.
func seedTree(t *testing.T, configDir, cwd, sessionFile, wantID string) {
	t.Helper()
	dir := transcript.ProjectDir(configDir, cwd)
	require.NotEmpty(t, dir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	line := `{"type":"user","uuid":"` + wantID + `","parentUuid":null,"cwd":"` + cwd +
		`","sessionId":"` + sessionFile + `","message":{"role":"user","content":"hi"}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, sessionFile+".jsonl"), []byte(line), 0o644))
}

func TestPayloadTranscript_RefusesAGuessFromACollidingSiblingCheckout(t *testing.T) {
	// The collision BelongsToSession cannot see, closed at the caller.
	// EncodeProjectDir maps every non-alphanumeric byte to "-", so the
	// subdirectory "<base>/proj/pkg" and the sibling checkout "<base>/proj-pkg"
	// name the same project directory. The sibling genuinely ran a session with
	// this id, so the file agrees about the SESSION — only the tree disagrees.
	// Two hyphenated sibling checkouts is an ordinary layout, not an attack.
	cfg := t.TempDir()
	base := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	proj := filepath.Join(base, "proj")
	sibling := filepath.Join(base, "proj-pkg")
	sub := filepath.Join(proj, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.MkdirAll(sibling, 0o755))

	// The sibling checkout genuinely ran session "s-1".
	seedTree(t, cfg, sibling, "s-1", "SIBLING-ORIGIN")

	require.Equal(t,
		transcript.ProjectDir(cfg, sub), transcript.ProjectDir(cfg, sibling),
		"the collision this test is about must actually collide")

	got, err := HookPayload{SessionID: "s-1", Cwd: sub}.record()

	assert.ErrorIs(t, err, transcript.ErrWrongTree,
		"the sibling's transcript belongs to another tree and must be refused")
	assert.Empty(t, got)

	// And the identity must not come back either.
	id, err := stableID(HookPayload{SessionID: "s-1", Cwd: sub})
	require.Error(t, err)
	assert.NotEqual(t, "SIBLING-ORIGIN", id,
		"another project's origin must never be this session's identity")
	assert.Empty(t, id)
}

func TestPayloadTranscript_AcceptsAGuessFromTheSameTree(t *testing.T) {
	// The tree check must not refuse the ordinary case: a session whose records
	// carry the very cwd being asked about still resolves.
	cfg := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	seedTree(t, cfg, cwd, "s-1", "OWN-ORIGIN")

	got, err := HookPayload{SessionID: "s-1", Cwd: cwd}.record()

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(transcript.ProjectDir(cfg, cwd), "s-1.jsonl"), got)

	id, err := stableID(HookPayload{SessionID: "s-1", Cwd: cwd})
	require.NoError(t, err)
	assert.Equal(t, "OWN-ORIGIN", id)
}

func TestRecord_AnIsolatedSubagentIsNeverAskedAboutItsTree(t *testing.T) {
	// The interaction between projectDirOf and BelongsToTree, which nothing
	// tested and which is not obvious.
	//
	// A sub-agent dispatched into its own worktree reports THAT worktree as its
	// cwd, while the harness still nests its record under the DISPATCHING
	// session's project directory. So the record's own cwd and the tree the
	// payload names are legitimately different places: of 337 real sub-agent
	// transcripts carrying a cwd, 18 record a sibling worktree that is not under
	// the parent's tree at all.
	//
	// Asking BelongsToTree about those would refuse a sub-agent its own record —
	// the exact orphaning this file exists to prevent. It is never asked, because
	// the tree check guards only the GUESSED branch and a sub-agent's path is
	// always reported. This pins that, with a recorded cwd deliberately in a
	// different tree from the payload's.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	base := t.TempDir()
	parentTree := filepath.Join(base, "proj")
	agentWorktree := filepath.Join(base, "proj-worktrees", "agent-1")
	require.NoError(t, os.MkdirAll(parentTree, 0o755))
	require.NoError(t, os.MkdirAll(agentWorktree, 0o755))

	// The parent's record lives under the parent's project directory; the
	// sub-agent's is nested beside it under subagents/.
	projDir := transcript.ProjectDir(cfg, parentTree)
	parentPath := filepath.Join(projDir, "sess-1.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(parentPath), 0o755))
	require.NoError(t, os.WriteFile(parentPath,
		[]byte(`{"type":"user","uuid":"PARENT-ORIGIN","parentUuid":null,"sessionId":"sess-1","cwd":"`+
			parentTree+`"}`+"\n"), 0o644))

	agentPath, err := transcript.SubagentTranscriptPath(parentPath, "a1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(agentPath), 0o755))
	// Its records carry the WORKTREE as cwd — a different tree from the payload's.
	require.NoError(t, os.WriteFile(agentPath,
		[]byte(`{"type":"user","uuid":"AGENT-ORIGIN","parentUuid":null,"sessionId":"sess-1","cwd":"`+
			agentWorktree+`","isSidechain":true}`+"\n"), 0o644))

	// The payload's cwd is the PARENT's tree while the record's own cwd is the
	// worktree — the disagreement the 18 real files have. A tree check applied
	// here would refuse on exactly that disagreement.
	//
	// Reported directly: returned verbatim, no tree check.
	got, err := HookPayload{AgentTranscriptPath: agentPath, Cwd: parentTree}.record()
	require.NoError(t, err, "a sub-agent's own reported record must never be refused over its tree")
	assert.Equal(t, agentPath, got)

	// Reconstructed from the agent id against the parent's reported path: also
	// no tree check.
	got, err = HookPayload{TranscriptPath: parentPath, AgentID: "a1", Cwd: parentTree}.record()
	require.NoError(t, err, "a reconstructed sub-agent path must never be refused over its tree")
	assert.Equal(t, agentPath, got)

	// And the sub-agent resolves to its OWN identity, not the parent's — which
	// is what projectDirOf's climb out of subagents/ is for. Deriving the project
	// directory from the worktree cwd instead would name somewhere holding no
	// transcripts.
	id, err := stableID(HookPayload{AgentTranscriptPath: agentPath, Cwd: parentTree})
	require.NoError(t, err)
	assert.Equal(t, "AGENT-ORIGIN", id)

	parentID, err := stableID(HookPayload{TranscriptPath: parentPath, Cwd: parentTree})
	require.NoError(t, err)
	assert.Equal(t, "PARENT-ORIGIN", parentID)
	assert.NotEqual(t, parentID, id, "a sub-agent must not inherit the parent's identity")
}

func TestRecord_TheTreeCheckStillGuardsAGuessedRootPath(t *testing.T) {
	// The other half: the tree check must not have been weakened by sitting only
	// on the guessed branch. A ROOT session's guess still gets it.
	cfg := t.TempDir()
	base := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)

	sub := filepath.Join(base, "proj", "pkg")
	sibling := filepath.Join(base, "proj-pkg")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.MkdirAll(sibling, 0o755))
	require.Equal(t, transcript.ProjectDir(cfg, sub), transcript.ProjectDir(cfg, sibling),
		"the collision must actually collide")

	dir := transcript.ProjectDir(cfg, sibling)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s-1.jsonl"),
		[]byte(`{"type":"user","uuid":"SIBLING-ORIGIN","parentUuid":null,"sessionId":"s-1","cwd":"`+
			sibling+`"}`+"\n"), 0o644))

	_, err := HookPayload{SessionID: "s-1", Cwd: sub}.record()
	assert.ErrorIs(t, err, transcript.ErrWrongTree)
}
