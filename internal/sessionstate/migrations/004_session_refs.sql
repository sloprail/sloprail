-- The refs a session (or sub-agent) left commits on, per folder: what it touched, as
-- data. A file-guard judges every recorded tip at Stop, not only HEAD, so a branch the
-- agent committed on and then checked out away from is still judged.
--
-- ref is the full ref name ('refs/heads/feat-a') or 'detached/<sha12>' for commits made
-- on a detached HEAD. first_tip is the tip when the ref was first recorded; tip is the
-- latest one seen (refreshed from the ref at each Stop). folder is a session_folders path
-- (a git root). agent_id names the sub-agent that owns the row; '' for the root.
CREATE TABLE session_refs (
    session_id TEXT NOT NULL,
    folder     TEXT NOT NULL,
    ref        TEXT NOT NULL,
    first_tip  TEXT NOT NULL,
    tip        TEXT NOT NULL,
    agent_id   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, folder, ref)
) WITHOUT ROWID;
