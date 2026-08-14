-- Where the session began, and anything else it must remember beyond its
-- verdicts. A key-value shape so a second such fact costs no migration.
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;

-- One guardrail's verdict on one file's content. The grain is a check, not a
-- file: keying by file alone would make a newly added guardrail skip everything
-- already judged, and would leave nowhere to record that a file satisfies one
-- rule while violating another.
--
-- The CONTENT is part of the key, not just a column, so a verdict is remembered
-- per (path, guardrail, content) rather than one row being overwritten by the
-- next thing written to the path. Keying on (path, guardrail) alone rests on
-- the assumption that superseded content "cannot come back under the same path"
-- — and it can: an agent that edits a file and then reverts it restores exactly
-- the bytes an earlier cycle judged. With a single row the revert finds a
-- fingerprint that no longer matches and the settled question is re-opened,
-- which identity_is_content forbids.
--
-- path stays in the key, and that is what keeps a MOVE a new question: the same
-- content arriving at a path it has never been checked at finds no row and is
-- judged there. See T020_01 and T020_02, which fail in opposite directions if
-- either column is dropped from the key.
CREATE TABLE file_checks (
    path        TEXT NOT NULL,
    guardrail   TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    passed      INTEGER NOT NULL,
    -- Monotonic per write, so "the verdict that stands" has an answer when a
    -- path has been judged at several contents. Nothing reads it as a time.
    seq         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (path, guardrail, fingerprint)
) WITHOUT ROWID;

-- What one guardrail remembers within one session. Keyed per entry rather than
-- one document per guardrail, so a rule holding many subjects can write one
-- without rewriting the rest; the prefix scan recovers the grouping.
CREATE TABLE guardrail_state (
    guardrail TEXT NOT NULL,
    key       TEXT NOT NULL,
    value     TEXT NOT NULL,
    PRIMARY KEY (guardrail, key)
) WITHOUT ROWID;
