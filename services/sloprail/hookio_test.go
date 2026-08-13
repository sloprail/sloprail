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
