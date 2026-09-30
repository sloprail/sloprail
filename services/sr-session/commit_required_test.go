package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ownsTree is a10n's rule: a sub-agent owns its tree iff its git top level differs
// from the one the session began in.
func TestOwnsTree(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "one")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "pkg"), 0o755))
	worktree := filepath.Join(t.TempDir(), "agent-1")
	runGit(t, repo, "worktree", "add", "-q", "--detach", worktree)

	// The root's record: it began in the repo, and later stood in the worktree.
	dir := t.TempDir()
	rootRecord := filepath.Join(dir, "root.jsonl")
	require.NoError(t, os.WriteFile(rootRecord, []byte(
		`{"type":"user","uuid":"u1","parentUuid":null,"cwd":"`+repo+`","message":{"role":"user","content":"hi"}}`+"\n"+
			`{"type":"assistant","uuid":"a1","parentUuid":"u1","cwd":"`+worktree+`","message":{"role":"assistant","content":"x"}}`+"\n"), 0o644))
	noCwd := filepath.Join(dir, "nocwd.jsonl")
	require.NoError(t, os.WriteFile(noCwd, []byte(`{"type":"user","uuid":"u1","parentUuid":null,"message":{"role":"user","content":"hi"}}`+"\n"), 0o644))

	sub := func(record, cwd string) HookPayload {
		return HookPayload{TranscriptPath: record, Cwd: cwd, AgentID: "agent-1"}
	}

	assert.True(t, ownsTree(HookPayload{Cwd: repo}), "the session's root owns its tree")
	assert.False(t, ownsTree(sub(rootRecord, repo)), "a sub-agent in the root's own tree does not")
	assert.False(t, ownsTree(sub(rootRecord, filepath.Join(repo, "pkg"))), "nor one that stands in a subdirectory of it")
	assert.True(t, ownsTree(sub(rootRecord, worktree)), "a sub-agent in a worktree of its own does")

	// The root having cd'd INTO the worktree later changes nothing: the root's tree
	// is where it began.
	assert.True(t, ownsTree(sub(rootRecord, worktree)))
	assert.False(t, ownsTree(sub(rootRecord, repo)))

	// Every doubt is a gate.
	assert.True(t, ownsTree(sub(noCwd, repo)), "a root directory that cannot be determined gates")
	assert.True(t, ownsTree(sub(filepath.Join(dir, "missing.jsonl"), repo)), "a root record that is not there gates")
	assert.True(t, ownsTree(sub("", repo)), "no root record at all gates")
	assert.True(t, ownsTree(sub(rootRecord, t.TempDir())), "a sub-agent tree that is not a repository gates")
}
