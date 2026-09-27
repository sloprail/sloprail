package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeAt writes a transcript at <configDir>/projects/<projectDir>/<name>.
func writeAt(t *testing.T, configDir, projectDir, name string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(configDir, "projects", projectDir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func sessionRecordLine(sessionID, uuid, parent string) string {
	p := "null"
	if parent != "" {
		p = `"` + parent + `"`
	}
	return `{"parentUuid":` + p + `,"type":"user","uuid":"` + uuid + `","sessionId":"` + sessionID +
		`","timestamp":"2026-09-24T09:51:50.053Z","message":{"role":"user","content":"x"}}`
}

// TestRelocateRecordFindsAResumeFromAnotherDirectory is the real shape: a
// session begun in a worktree, resumed from the main checkout. The payload
// names the main checkout's project directory; the record is still in the
// worktree's.
func TestRelocateRecordFindsAResumeFromAnotherDirectory(t *testing.T) {
	cfg := t.TempDir()
	const sid = "06f7418e-0000-4000-8000-000000000001"
	real := writeAt(t, cfg, "-repo--claude-worktrees-feature", sid+".jsonl",
		sessionRecordLine(sid, "origin", ""),
		sessionRecordLine(sid, "turn", "origin"),
	)
	reported := filepath.Join(cfg, "projects", "-repo", sid+".jsonl")

	assert.Equal(t, real, RelocateRecord(cfg, reported))

	id, err := StableSessionID(filepath.Dir(RelocateRecord(cfg, reported)), RelocateRecord(cfg, reported))
	require.NoError(t, err)
	assert.Equal(t, "origin", id)
}

// TestRelocateRecordLeavesAnUnwrittenRecordAlone: a fresh session at
// SessionStart has no record anywhere yet, and must still read as not written.
func TestRelocateRecordLeavesAnUnwrittenRecordAlone(t *testing.T) {
	cfg := t.TempDir()
	writeAt(t, cfg, "-elsewhere", "other.jsonl", sessionRecordLine("other", "o", ""))
	reported := filepath.Join(cfg, "projects", "-repo", "fresh-session.jsonl")
	assert.Equal(t, reported, RelocateRecord(cfg, reported))
}

// TestRelocateRecordRefusesAFileOfAnotherSession: a file that merely shares
// the name but whose records name a different session is not this session's
// record, and taking it would key this session on someone else's identity.
func TestRelocateRecordRefusesAFileOfAnotherSession(t *testing.T) {
	cfg := t.TempDir()
	const sid = "same-name"
	writeAt(t, cfg, "-elsewhere", sid+".jsonl", sessionRecordLine("a-different-session", "o", ""))
	reported := filepath.Join(cfg, "projects", "-repo", sid+".jsonl")
	assert.Equal(t, reported, RelocateRecord(cfg, reported))
}

// TestRelocateRecordKeepsAPresentPath: the common case costs a stat and
// changes nothing, even when another directory holds a same-named file.
func TestRelocateRecordKeepsAPresentPath(t *testing.T) {
	cfg := t.TempDir()
	const sid = "s"
	here := writeAt(t, cfg, "-repo", sid+".jsonl", sessionRecordLine(sid, "o", ""))
	writeAt(t, cfg, "-elsewhere", sid+".jsonl", sessionRecordLine(sid, "o2", ""))
	assert.Equal(t, here, RelocateRecord(cfg, here))
}

// TestRelocateRecordRefusesAFileNamingNoSession: a file whose records carry no
// sessionId at all says nothing about whose it is, and is not taken.
func TestRelocateRecordRefusesAFileNamingNoSession(t *testing.T) {
	cfg := t.TempDir()
	const sid = "no-ids"
	writeAt(t, cfg, "-elsewhere", sid+".jsonl", `{"type":"user","uuid":"o","parentUuid":null}`)
	reported := filepath.Join(cfg, "projects", "-repo", sid+".jsonl")
	assert.Equal(t, reported, RelocateRecord(cfg, reported))
}

// TestRelocateRecordRefusesAnUnreadableFile: a file that cannot be read before
// its own session id is seen is not taken either.
func TestRelocateRecordRefusesAnUnreadableFile(t *testing.T) {
	cfg := t.TempDir()
	const sid = "huge"
	writeAt(t, cfg, "-elsewhere", sid+".jsonl",
		`{"type":"user","uuid":"o","parentUuid":null,"message":{"content":"`+strings.Repeat("x", maxRecordBytes+1)+`"}}`,
		sessionRecordLine(sid, "t", "o"))
	reported := filepath.Join(cfg, "projects", "-repo", sid+".jsonl")
	assert.Equal(t, reported, RelocateRecord(cfg, reported))
}

// TestRelocateRecordFindsASubagentsRecord: a sub-agent's record nests under its
// session's, so it moves with it; the lookup finds it at the same place under
// the directory the session began in. Its records carry the SESSION's id.
func TestRelocateRecordFindsASubagentsRecord(t *testing.T) {
	cfg := t.TempDir()
	const sid = "06f7418e-0000-4000-8000-000000000003"
	real := writeAt(t, cfg, "-repo--claude-worktrees-feature", filepath.Join(sid, "subagents", "agent-a1.jsonl"),
		`{"parentUuid":null,"type":"user","uuid":"sub-origin","sessionId":"`+sid+`","isSidechain":true,"agentId":"a1"}`)
	reported := filepath.Join(cfg, "projects", "-repo", sid, "subagents", "agent-a1.jsonl")
	assert.Equal(t, real, RelocateRecord(cfg, reported))
}
