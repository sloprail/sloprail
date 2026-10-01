package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// The session's folders: a registry of the trees the session works in, kept in the
// ROOT session's store, a row per (session, folder path) after a10n's
// session_folders. Each row says where work in that folder began (BaseRef), and a
// file-guard's range for a hook running in the folder starts there.
//
// Why a registry and not the sub-agent's own session start. A sub-agent that owns a
// worktree is dispatched into a tree created from the CURRENT main, hours after its
// parent session began. Its range must start at where its own tree began; the
// parent's start is an ancestor of it and would cover every PR merged to main
// since, which the sub-agent never touched and cannot fix. The sub-agent's start was
// kept in its own agent-keyed store, which a payload that does not name the agent
// (a tool event before its identity resolves) does not reach. A folder is found by
// where it is — the git root of the hook's cwd — in the one store the whole session
// shares, whoever the caller claims to be.
//
// Only properly defined folders are registered: the session's own repository, and a
// worktree of its own that the harness told us a sub-agent was dispatched into
// (agent_id on the payload, a tree different from the root's). A repository the
// agent merely touched is NOT registered here; the table shape (role, repo_id,
// branch, head_ref) is ready for that, and for nothing else yet.

// rootSession is the session every folder belongs to, as the root's own store
// keys it.
type rootSession struct {
	ID   string
	Cwd  string // where the session began
	Path string // its state database
}

// resolveRootSession names the session a hook belongs to at the ROOT: its own for a
// root payload, the dispatching session's for a sub-agent's. The root's directory is
// where its record says it began, never where it last stood.
func resolveRootSession(p HookPayload) (rootSession, error) {
	var rs rootSession
	if !p.IsSubagent() {
		id, err := stableID(p)
		if err != nil {
			return rs, err
		}
		path, err := sessionDBPath(p.Cwd, id)
		if err != nil {
			return rs, err
		}
		return rootSession{ID: id, Cwd: p.Cwd, Path: path}, nil
	}
	record, err := p.sessionRecord()
	if err != nil {
		return rs, err
	}
	if record == "" {
		return rs, fmt.Errorf("sloprail: a sub-agent's hook names no session record, so the session it belongs to is unknown")
	}
	cwd, err := transcript.StartCwd(record)
	if err != nil {
		return rs, err
	}
	if cwd == "" {
		return rs, fmt.Errorf("sloprail: the session record %s names no starting directory", record)
	}
	id, err := sessionpath.StableIdentity(record, cwd)
	if err != nil {
		return rs, err
	}
	path, err := sessionDBPath(cwd, id.ID)
	if err != nil {
		return rs, err
	}
	return rootSession{ID: id.ID, Cwd: cwd, Path: path}, nil
}

// folderToRegister is the folder a hook's first tool call defines, or "" and false
// when it defines none: not a repository, a sub-agent that works in the root's own
// tree (judged with the root, owns nothing), a sub-agent the harness did not name,
// or a root that has since stood somewhere other than where it began (a repository
// it merely touched).
func folderToRegister(p HookPayload, rs rootSession) (path, role string, ok bool) {
	tree, err := gitrepo.Root(p.Cwd)
	if err != nil || tree == "" {
		return "", "", false
	}
	if p.IsSubagent() {
		if p.AgentID == "" || !ownsTree(p) {
			return "", "", false
		}
		return tree, sessionstate.FolderSubagentWorktree, true
	}
	rootTree, err := gitrepo.Root(rs.Cwd)
	if err != nil || !sameDir(rootTree, tree) {
		return "", "", false
	}
	return tree, sessionstate.FolderRoot, true
}

// registerStartFolder records the folder this hook's agent began work in, with the
// HEAD it began at, if it has not been recorded yet. Called on every tool call
// right after the agent's own start is taken (ensureBaselineRecorded), and a no-op
// from the second.
//
// The start is read back from the agent's own store rather than taken from git here:
// a call that follows a failed first attempt would otherwise record a HEAD the
// agent's own commits have since moved, and its range would start after them.
// Failing to register is reported and never refuses the tool call, as the baseline's
// own failure is: with no row the range falls back to the agent's own start.
func registerStartFolder(own sessionstate.Store, p HookPayload) error {
	rs, err := resolveRootSession(p)
	if err != nil {
		return err
	}
	path, role, ok := folderToRegister(p, rs)
	if !ok {
		return nil
	}
	start, had, err := own.Meta(sessionstate.MetaSessionStart)
	if err != nil {
		return err
	}
	if !had || start == "" {
		return nil // nothing recorded as the start: leave the folder unregistered, so the range fails closed
	}
	reg, err := sessionstate.Open(rs.Path)
	if err != nil {
		return err
	}
	defer reg.Close()

	f := sessionstate.Folder{
		SessionID: rs.ID, Path: path, Role: role, GitRoot: path, BaseRef: start, AgentID: p.AgentID,
	}
	if role == sessionstate.FolderRoot {
		f.AgentID = ""
	}
	if pos, err := gitrepo.Head(p.Cwd); err == nil {
		f.Branch, f.HeadRef = pos.Branch, pos.Commit
	}
	if id, err := gitrepo.RootCommit(p.Cwd); err == nil {
		f.RepoID = id
	}
	_, err = reg.RegisterFolder(f)
	return err
}

// sessionFolderFor is the registered folder a hook's tree is, or nil when it is not
// one (nothing registered it, or the registry cannot be read — the caller then falls
// back to the agent's own start, which is what it used before there was a registry).
// root is the git root the range is computed in.
func sessionFolderFor(p HookPayload, root string) *sessionstate.Folder {
	rs, err := resolveRootSession(p)
	if err != nil {
		return nil
	}
	if _, err := os.Stat(rs.Path); err != nil {
		return nil // a lookup never creates the root's store
	}
	reg, err := sessionstate.Open(rs.Path)
	if err != nil {
		return nil
	}
	defer reg.Close()
	f, found, err := reg.Folder(rs.ID, filepath.Clean(root))
	if err != nil || !found || f.BaseRef == "" {
		return nil
	}
	return &f
}
