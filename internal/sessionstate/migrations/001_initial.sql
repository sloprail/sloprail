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
CREATE TABLE file_checks (
    path        TEXT NOT NULL,
    guardrail   TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    passed      INTEGER NOT NULL,
    PRIMARY KEY (path, guardrail)
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
