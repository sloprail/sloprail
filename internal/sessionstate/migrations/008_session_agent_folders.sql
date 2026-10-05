-- The folders a sub-agent has worked in: where it stood for a tool call, a repository its command
-- ran in, a file it wrote. session_folders names ONE owner per folder (the first to register it),
-- so a folder the root registered and a sub-agent then worked in was attributed to no sub-agent.
-- The root's Stop reads this to leave a running background agent's folders' uncommitted work to
-- that agent instead of refusing the root for it.
CREATE TABLE session_agent_folders (
    session_id TEXT NOT NULL,
    agent_id   TEXT NOT NULL,
    folder     TEXT NOT NULL,
    PRIMARY KEY (session_id, agent_id, folder)
) WITHOUT ROWID;
