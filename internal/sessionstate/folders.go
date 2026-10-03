package sessionstate

import (
	"database/sql"
	"errors"
	"fmt"
)

// The roles a session folder has. Today only these two are written; a folder the
// agent merely touched is a role to add, not a table to change.
const (
	// FolderRoot is the session's own repository.
	FolderRoot = "root"
	// FolderSubagentWorktree is a worktree of its own that the harness dispatched a
	// sub-agent into.
	FolderSubagentWorktree = "subagent-worktree"
	// FolderAdHoc is a repository a sub-agent stood in after its own worktree (it
	// cd'd there). Registered when first seen, its start is its HEAD at that moment:
	// history from before the agent touched it is never judged.
	FolderAdHoc = "ad-hoc"
)

// FolderBaseUnborn is a folder's BaseRef when its repository had no commit yet.
const FolderBaseUnborn = SessionStartUnborn

// Folder is one row of a session's folders. See migrations/003_session_folders.sql.
type Folder struct {
	SessionID string
	// Path is the folder as the session knows it: its git root's absolute path.
	Path string
	Role string
	// GitRoot is the repository's top level; equal to Path for the folders written
	// today, kept apart because a folder need not be a whole repository.
	GitRoot string
	// RepoID is gitrepo.RepoID, the repository's stable identity (normalized remote + initial
	// commit; the git common dir without a remote), which survives a
	// worktree being a different path. Empty when it could not be read.
	RepoID string
	Branch string
	// BaseRef is the folder's start: its HEAD when work in it began, FolderBaseUnborn
	// before the first commit. Written once.
	BaseRef string
	// HeadRef is the last HEAD seen in the folder; informational, advanced freely.
	HeadRef string
	// AgentID names the sub-agent that owns the folder; empty for the root.
	AgentID string
}

// RegisterFolder records a folder the first time it is seen, and reports whether it
// wrote. A folder already registered is left exactly as it is — its BaseRef is the
// point work began, and registering again must not move it (a later call knows only
// where the folder is now). HeadRef is the exception, see SetFolderHead.
func (s *store) RegisterFolder(f Folder) (bool, error) {
	if f.SessionID == "" || f.Path == "" {
		return false, errors.New("sessionstate: a folder needs a session and a path")
	}
	db, err := s.conn()
	if err != nil {
		return false, err
	}
	res, err := db.Exec(`
		INSERT INTO session_folders (session_id, path, role, git_root, repo_id, branch, base_ref, head_ref, agent_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (session_id, path) DO NOTHING`,
		f.SessionID, f.Path, f.Role, f.GitRoot, f.RepoID, f.Branch, f.BaseRef, f.HeadRef, f.AgentID)
	if err != nil {
		return false, fmt.Errorf("sessionstate: register folder %q: %w", f.Path, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sessionstate: register folder %q: %w", f.Path, err)
	}
	return n > 0, nil
}

// Folder reads one folder of a session. Absent is an answer, not a failure.
func (s *store) Folder(sessionID, path string) (Folder, bool, error) {
	db, err := s.conn()
	if err != nil {
		return Folder{}, false, err
	}
	f, err := scanFolder(db.QueryRow(folderSelect+` WHERE session_id = ? AND path = ?`, sessionID, path))
	if errors.Is(err, sql.ErrNoRows) {
		return Folder{}, false, nil
	}
	if err != nil {
		return Folder{}, false, fmt.Errorf("sessionstate: read folder %q: %w", path, err)
	}
	return f, true, nil
}

// Folders lists a session's folders, root first and then by path.
func (s *store) Folders(sessionID string) ([]Folder, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(folderSelect+` WHERE session_id = ? ORDER BY role <> ?, path`, sessionID, FolderRoot)
	if err != nil {
		return nil, fmt.Errorf("sessionstate: list folders: %w", err)
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, fmt.Errorf("sessionstate: list folders: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SetFolderHead records the last HEAD seen in a folder. A folder that is not
// registered is not created by it.
func (s *store) SetFolderHead(sessionID, path, head string) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE session_folders SET head_ref = ? WHERE session_id = ? AND path = ?`, head, sessionID, path); err != nil {
		return fmt.Errorf("sessionstate: update folder %q: %w", path, err)
	}
	return nil
}

const folderSelect = `SELECT session_id, path, role, git_root, repo_id, branch, base_ref, head_ref, agent_id FROM session_folders`

type rowScanner interface{ Scan(dest ...any) error }

func scanFolder(r rowScanner) (Folder, error) {
	var f Folder
	err := r.Scan(&f.SessionID, &f.Path, &f.Role, &f.GitRoot, &f.RepoID, &f.Branch, &f.BaseRef, &f.HeadRef, &f.AgentID)
	return f, err
}
