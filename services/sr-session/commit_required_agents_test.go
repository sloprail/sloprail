package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// stageBusyFolder sets up a root session in a repository, with one other folder it registered
// (adhoc) that holds an uncommitted guarded x.md. set receives the root's registry to arrange the
// sub-agents.
func stageBusyFolder(t *testing.T, set func(reg sessionstate.Store, sessionID, adhoc string)) (p HookPayload, adhoc string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root := initRepo(t)
	commitFile(t, root, "README", "r")
	adhoc = initRepo(t)
	writeFileGuardYAML(t, adhoc, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n", map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	require.NoError(t, os.WriteFile(filepath.Join(adhoc, "x.md"), []byte("half done merge"), 0o644))

	record := filepath.Join(t.TempDir(), "sess.jsonl")
	require.NoError(t, os.WriteFile(record, []byte(`{"type":"user","uuid":"o","parentUuid":null,"message":{"role":"user","content":"hi"}}`+"\n"), 0o644))
	p = HookPayload{Cwd: root, SessionID: "sess", TranscriptPath: record}
	rs, err := resolveRootSession(p)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(rs.Path), 0o755))
	reg, err := sessionstate.Open(rs.Path)
	require.NoError(t, err)
	defer reg.Close()
	_, err = reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: adhoc, Role: sessionstate.FolderAdHoc, GitRoot: adhoc, BaseRef: runGit(t, adhoc, "rev-parse", "HEAD")})
	require.NoError(t, err)
	set(reg, rs.ID, adhoc)
	return p, adhoc
}

func runningBackground(reg sessionstate.Store, sid, agent, folder string) {
	now := time.Now()
	_ = reg.StartAgent(sessionstate.AgentSignal{SessionID: sid, AgentID: agent, At: now})
	_ = reg.NoteAgentLaunch(sessionstate.AgentSignal{SessionID: sid, AgentID: agent, At: now})
	_ = reg.NoteAgentFolder(sid, agent, folder)
}

func stopAsRoot(t *testing.T, p HookPayload, guards []declaration.FileGuard) (refusal, notice string) {
	t.Helper()
	modReg, err := loadRegistry()
	require.NoError(t, err)
	cmd, _ := capturing()
	notes := withStopNotices(cmd)
	refusal = commitRequired(cmd, p, guards, nil, modReg)
	return refusal, notes.text()
}

func TestCommitRequired_AFolderARunningBackgroundSubagentWorksInIsNamedNotRefused(t *testing.T) {
	p, adhoc := stageBusyFolder(t, func(reg sessionstate.Store, sid, adhoc string) { runningBackground(reg, sid, "bg1", adhoc) })
	refusal, notice := stopAsRoot(t, p, nil)
	assert.Empty(t, refusal, "the root must not commit a running sub-agent's half-done work")
	assert.Contains(t, notice, "being worked on by sub-agent bg1")
	assert.Contains(t, notice, adhoc)
}

func TestCommitRequired_TheFolderIsOwedAgainOnceTheSubagentStops(t *testing.T) {
	p, _ := stageBusyFolder(t, func(reg sessionstate.Store, sid, adhoc string) {
		runningBackground(reg, sid, "bg1", adhoc)
		require.NoError(t, reg.EndAgent(sid, "bg1", sessionstate.AgentCompleted, time.Now()))
	})
	refusal, notice := stopAsRoot(t, p, nil)
	assert.Contains(t, refusal, "x.md")
	assert.Empty(t, notice)
}

func TestCommitRequired_AgentsTheRegistryDoesNotHoldRunningInTheBackgroundAreJudged(t *testing.T) {
	cases := map[string]func(reg sessionstate.Store, sid, adhoc string){
		"foreground": func(reg sessionstate.Store, sid, adhoc string) {
			require.NoError(t, reg.StartAgent(sessionstate.AgentSignal{SessionID: sid, AgentID: "fg", At: time.Now()}))
			require.NoError(t, reg.NoteAgentFolder(sid, "fg", adhoc))
		},
		"unknown to the registry": func(reg sessionstate.Store, sid, adhoc string) {
			require.NoError(t, reg.NoteAgentFolder(sid, "ghost", adhoc))
		},
		"working in another folder": func(reg sessionstate.Store, sid, adhoc string) {
			runningBackground(reg, sid, "bg1", initRepo(t))
		},
		"killed": func(reg sessionstate.Store, sid, adhoc string) {
			runningBackground(reg, sid, "bg1", adhoc)
			require.NoError(t, reg.EndAgent(sid, "bg1", sessionstate.AgentKilled, time.Now()))
		},
		"stale": func(reg sessionstate.Store, sid, adhoc string) {
			runningBackground(reg, sid, "bg1", adhoc)
			require.NoError(t, reg.MarkAgentStale(sid, "bg1", time.Now()))
		},
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			p, _ := stageBusyFolder(t, set)
			refusal, notice := stopAsRoot(t, p, nil)
			assert.Contains(t, refusal, "x.md")
			assert.Empty(t, notice)
		})
	}
}

func TestCommitRequired_ASilentAgentPastTheStaleThresholdIsJudged(t *testing.T) {
	p, _ := stageBusyFolder(t, func(reg sessionstate.Store, sid, adhoc string) { runningBackground(reg, sid, "bg1", adhoc) })
	old := agentClock
	agentClock = func() time.Time { return time.Now().Add(1000 * time.Hour) }
	t.Cleanup(func() { agentClock = old })
	refusal, _ := stopAsRoot(t, p, nil)
	assert.Contains(t, refusal, "x.md")
}

func TestCommitRequired_ARunningAgentsFolderDoesNotExcuseTheRootsOwnTree(t *testing.T) {
	p, adhoc := stageBusyFolder(t, func(reg sessionstate.Store, sid, adhoc string) { runningBackground(reg, sid, "bg1", adhoc) })
	require.NoError(t, os.WriteFile(filepath.Join(p.Cwd, "y.md"), []byte("mine"), 0o644))
	refusal, notice := stopAsRoot(t, p, []declaration.FileGuard{{Name: "g", Match: `path == "y.md"`}})
	assert.Contains(t, refusal, "y.md", "the root's own uncommitted file is still refused")
	assert.NotContains(t, refusal, "x.md")
	assert.Contains(t, notice, adhoc)
}

func TestNoteAgentFolders_ASubagentsCallRecordsTheRepositoriesItWorksIn(t *testing.T) {
	root := initRepo(t)
	other := initRepo(t)
	reg := openStore(t)
	rs := rootSession{ID: "s", Cwd: root}
	noteAgentFolders(reg, rs, HookPayload{Cwd: other, AgentID: "a1", AgentType: "x", TranscriptPath: "t"})
	noteAgentFolders(reg, rs, HookPayload{Cwd: root}) // the root's own call records nothing
	require.NoError(t, reg.StartAgent(sessionstate.AgentSignal{SessionID: "s", AgentID: "a1", At: time.Now()}))
	as, err := reg.Agents("s")
	require.NoError(t, err)
	require.Len(t, as, 1)
	assert.Equal(t, []string{treeKey(other)}, as[0].Folders)
}
