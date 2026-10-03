-- The sub-agents of a session, as a registry that outlives any one transcript file. Until now
-- "is this sub-agent still running" was read back from the dispatching transcript at each Stop,
-- which a compaction, a /clear or a resume with a new file forgets.
--
-- status is running | completed | failed | killed | stopped | stale. It changes only on a signal
-- (a SubagentStart / SubagentStop hook, the agent's own tool calls, a task-notification in the
-- dispatching transcript); a timestamp is never what says an agent finished. stale is the one
-- status sloprail itself sets: a running agent that went silent for too long, or whose session
-- process is gone, so its ranges are judged rather than waited for.
--
-- The times are unix seconds, for display and for the silence policy only. last_seen_at is the
-- last hook of the agent's own. background is 1 once the dispatching record showed the agent was
-- launched in the background; only a background agent's ranges are ever left unjudged.
-- owner_pid / owner_proc_start name the harness process the agent was seen under ('' / 0 when
-- unknown), so a session whose process is gone is known to have no running agent.
-- The ranges an agent owns are the session_refs rows with its agent_id.
CREATE TABLE session_agents (
    session_id       TEXT    NOT NULL,
    agent_id         TEXT    NOT NULL,
    status           TEXT    NOT NULL DEFAULT 'running',
    agent_type       TEXT    NOT NULL DEFAULT '',
    background       INTEGER NOT NULL DEFAULT 0,
    started_at       INTEGER NOT NULL DEFAULT 0,
    ended_at         INTEGER NOT NULL DEFAULT 0,
    last_seen_at     INTEGER NOT NULL DEFAULT 0,
    transcript_path  TEXT    NOT NULL DEFAULT '',
    owner_pid        INTEGER NOT NULL DEFAULT 0,
    owner_proc_start TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, agent_id)
) WITHOUT ROWID;
-- The task-notifications of the dispatching transcript already applied, by the record that
-- carried them: a transcript read again at every Stop (or a new file that repeats the old
-- records) must not end a run that began after them.
CREATE TABLE session_agent_signals (
    session_id TEXT NOT NULL,
    signal     TEXT NOT NULL,
    PRIMARY KEY (session_id, signal)
) WITHOUT ROWID;
