package main

import (
	"encoding/json"
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
		path, err := sessionDBPath(p.stateCwd(), id)
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

// folderToRegister is the folder a hook's first tool call defines, or false when it
// defines none: not a repository, a sub-agent that works in the root's own tree
// (judged with the root, owns nothing), a sub-agent the harness did not name, or a
// root that has since stood somewhere other than where it began (a repository it
// merely touched).
//
// A sub-agent's worktree is registered only when it is POSITIVELY a tree of its own:
// both the root's starting tree and the sub-agent's can be read and they differ. Any
// doubt registers nothing (unlike ownsTree, which gates on doubt) — a row is a claim
// that a range may start there, and a guess would start one at the wrong place. With
// no row the sub-agent's range fails closed.
func folderToRegister(p HookPayload, rs rootSession) (path, role string, ok bool) {
	tree, err := gitrepo.Root(p.Cwd)
	if err != nil || tree == "" {
		return "", "", false
	}
	rootTree, err := gitrepo.Root(rs.Cwd)
	if err != nil || rootTree == "" {
		return "", "", false
	}
	if p.IsSubagent() {
		if p.AgentID == "" || sameDir(rootTree, tree) {
			return "", "", false
		}
		return tree, sessionstate.FolderSubagentWorktree, true
	}
	if !sameDir(rootTree, tree) {
		return "", "", false
	}
	return tree, sessionstate.FolderRoot, true
}

// registerStartFolder records the folder this hook's agent began work in, with the
// HEAD it began at, if it has not been recorded yet. Called on every tool call, and a
// no-op once the agent's folder is there.
//
// A sub-agent's first folder is the worktree it was dispatched into. A later call from
// another repository (the sub-agent cd'd) registers that repository as its own
// ad-hoc folder, started at its HEAD at that call, so nothing from before the agent
// touched it is ever judged. (Auto-registration of touched repositories, for
// sub-agents.)
//
// The start is the agent's own recorded start when its store has one (a retry after a
// failed first attempt would otherwise record a HEAD its own commits have since
// moved), and the HEAD of the tree at this call otherwise. The registry does not
// depend on the agent's own store: a call whose identity cannot be resolved yet (its
// record is not written) still records where the agent began, in the root's store,
// where the Stop finds it by the folder's path.
func registerStartFolder(own sessionstate.Store, p HookPayload) error {
	rs, err := resolveRootSession(p)
	if err != nil {
		return err
	}
	path, role, ok := folderToRegister(p, rs)
	if !ok {
		// Not a folder this agent starts in, but a command it runs may still move
		// history in another repository.
		if _, err := os.Stat(rs.Path); err != nil {
			return nil
		}
		reg, err := sessionstate.Open(rs.Path)
		if err != nil {
			return err
		}
		defer reg.Close()
		return registerCommandFolders(reg, rs, p)
	}
	var start string
	if own != nil {
		if v, had, err := own.Meta(sessionstate.MetaSessionStart); err != nil {
			return err
		} else if had {
			start = v
		}
	}
	pos, headErr := gitrepo.Head(p.Cwd)
	if start == "" {
		if headErr != nil {
			return headErr
		}
		start = pos.Commit
		if start == "" {
			start = sessionstate.FolderBaseUnborn
		}
	}
	reg, err := sessionstate.Open(rs.Path)
	if err != nil {
		return err
	}
	defer reg.Close()

	if role == sessionstate.FolderSubagentWorktree {
		folders, err := reg.Folders(rs.ID)
		if err != nil {
			return err
		}
		for _, f := range folders {
			if f.AgentID == p.AgentID && f.Role == sessionstate.FolderSubagentWorktree {
				// The agent's worktree is already registered, so this is some other
				// repository it stood in: its own row, started at its HEAD now.
				role = sessionstate.FolderAdHoc
				if headErr != nil {
					return headErr
				}
				start = pos.Commit
				if start == "" {
					start = sessionstate.FolderBaseUnborn
				}
				break
			}
		}
	}
	f := sessionstate.Folder{
		SessionID: rs.ID, Path: path, Role: role, GitRoot: path, BaseRef: start, AgentID: p.AgentID,
	}
	if role == sessionstate.FolderRoot {
		f.AgentID = ""
	}
	if headErr == nil {
		f.Branch, f.HeadRef = pos.Branch, pos.Commit
	}
	if id, err := gitrepo.RootCommit(p.Cwd); err == nil {
		f.RepoID = id
	}
	wrote, err := reg.RegisterFolder(f)
	if err != nil {
		return err
	}
	if wrote && role == sessionstate.FolderRoot {
		// What every branch held when the session began: not the session's work.
		if tips, terr := gitrepo.RefTips(path); terr == nil {
			if b, merr := json.Marshal(tips); merr == nil {
				_ = reg.SetMeta(sessionstate.MetaRefsAtStart, string(b))
			}
		}
	}
	if err := registerCommandFolders(reg, rs, p); err != nil {
		return err
	}
	// What this agent has touched in the folder so far, at every hook.
	return observeRefs(reg, rs.ID, path, path, f.AgentID)
}

// sessionFolderFor is the registered folder a hook's tree is, or nil when it is not
// one (nothing registered it, or the registry cannot be read — the caller then falls
// back to the agent's own start, which is what it used before there was a registry).
// root is the git root the range is computed in.
//
// A registry that EXISTS but cannot be read is an error, not "no folder": the caller then
// falls back to the agent's own start, which for a sub-agent's worktree is the wrong
// (wider) range, and nothing would say so. Only a session with no identity or no store
// yet is "not registered".
func sessionFolderFor(p HookPayload, root string) (*sessionstate.Folder, error) {
	rs, err := resolveRootSession(p)
	if err != nil {
		return nil, nil // no session identity, so no registry to read
	}
	if _, err := os.Stat(rs.Path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil // a lookup never creates the root's store
		}
		return nil, fmt.Errorf("the session's folder registry could not be read: %w", err)
	}
	reg, err := sessionstate.Open(rs.Path)
	if err != nil {
		return nil, fmt.Errorf("the session's folder registry could not be read: %w", err)
	}
	defer reg.Close()
	f, found, err := reg.Folder(rs.ID, filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("the session's folder registry could not be read: %w", err)
	}
	if !found || f.BaseRef == "" {
		return nil, nil
	}
	return &f, nil
}
