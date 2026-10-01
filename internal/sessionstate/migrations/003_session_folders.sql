-- The folders a session works in: its own repository (the root row) and the
-- worktree of each sub-agent the harness dispatched into one. The shape is a10n's
-- session_folders (keyed by session and path, one row per folder, each with its own
-- refs), so a folder that a session merely touches can be added later without a
-- migration; today only the root and sub-agent worktrees are written.
--
-- base_ref is the folder's START: the HEAD it had when the session (or the
-- sub-agent) first worked in it, written once and never moved. 'unborn' says the
-- repository had no commit yet. A file-guard's range starts there, so a sub-agent
-- that owns a tree is judged from the commit its own tree began at, not from where
-- its parent session began.
CREATE TABLE session_folders (
    session_id TEXT NOT NULL,
    path       TEXT NOT NULL,
    role       TEXT NOT NULL DEFAULT '',
    git_root   TEXT NOT NULL DEFAULT '',
    repo_id    TEXT NOT NULL DEFAULT '',
    branch     TEXT NOT NULL DEFAULT '',
    base_ref   TEXT NOT NULL DEFAULT '',
    head_ref   TEXT NOT NULL DEFAULT '',
    agent_id   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, path)
) WITHOUT ROWID;
