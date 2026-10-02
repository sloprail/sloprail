-- The ranges a session has asked to have judged: per folder, the commits from base to head.
-- NOT a record of what passed (the check results, on the branch sloprail/checks, say that):
-- only which line of work the agent is answerable for, so the Stop can verify it.
--
-- head is a branch name (so the range follows the branch as commits are added) or a commit
-- sha (a detached HEAD); head_sha is the commit it last pointed at, so a branch that is gone
-- can still be verified at the commit it was. base is a revision: merge-base(origin's default branch, head) when it
-- was tracked automatically (added_by = 'auto'), whatever the agent stated otherwise.
-- untracked_reason is empty while the range is tracked; the agent that drops one says why, and
-- the Stop lists it.
CREATE TABLE session_ranges (
    session_id       TEXT NOT NULL,
    folder           TEXT NOT NULL,
    head             TEXT NOT NULL,
    base             TEXT NOT NULL DEFAULT '',
    head_sha         TEXT NOT NULL DEFAULT '',
    added_by         TEXT NOT NULL DEFAULT 'auto',
    untracked_reason TEXT NOT NULL DEFAULT '',
    agent_id         TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, folder, head)
) WITHOUT ROWID;
