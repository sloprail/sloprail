-- What file-guards concluded about commits.
--
-- The three tables, and their names and columns, are a10n's check-results store
-- (a10n services/checks, a10n-specs a10n-project-relational-store: check-runs,
-- checks, check-items), so what reads one reads the other. Only the data inside
-- is sloprail's. A file-guard is commit-based, so only file-guards write here;
-- gates and contexts are about events and leave no rows.
--
-- check_runs   one row per rule evaluated once: check_id is the rule's qualified
--              name, base_ref..head_ref the commit range it judged. A run that
--              failed as an engine (git error, a range that could not be computed)
--              carries exit_code/error and no checks: it is a failure, never an
--              empty range.
-- checks       one row per (run, subject, kind). kind names the check inside the
--              rule (check[0]:script:./size.sh, check[1]:judge:./rubric.md.j2,
--              require:citation); subject is the unit judged ("changeset" until
--              `subjects:` exists). fingerprint is the cache key — a hash of the
--              rule's whole folder, the model and the exact input, never a SHA —
--              and NULL for a script, which always re-runs.
-- check_items  one row per finding inside a check (a file a judge named, a
--              prerequisite of `require:`).
--
-- The identity columns (repo_id, branch, session_id, agent_id) say whose run it is. This
-- database is one per session FAMILY (the root session's checks.db beside its state.db,
-- written by the root and by every sub-agent it dispatches), so what a rule passed or refused
-- on a commit range is read once, whichever agent ran it, and the rows could move to one global store
-- without a change of shape.
--
-- Not carried over from a10n: check_contexts (sloprail's contexts live in the
-- session's own guardrail state) and schema_version (the schema is applied with
-- IF NOT EXISTS on every open, as a10n's is, so it is idempotent by construction).

CREATE TABLE IF NOT EXISTS check_runs (
    id           TEXT PRIMARY KEY,
    run_batch_id TEXT NOT NULL,
    run_at       TEXT NOT NULL,
    check_id     TEXT NOT NULL,
    repo_id      TEXT NOT NULL,
    branch       TEXT NOT NULL,
    session_id   TEXT NOT NULL,
    -- The sub-agent that ran it, '' for the root session itself. One database serves the whole
    -- session family (the root's, written by the root and every sub-agent), so this is what
    -- says whose run it is.
    agent_id     TEXT NOT NULL DEFAULT '',
    base_ref     TEXT NOT NULL DEFAULT '',
    head_ref     TEXT NOT NULL DEFAULT '',
    exit_code    INTEGER NOT NULL DEFAULT 0,
    error        TEXT,
    metadata     TEXT NOT NULL DEFAULT '{}',
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_check_runs_batch_run_at ON check_runs(run_batch_id, run_at);
CREATE INDEX IF NOT EXISTS idx_check_runs_run_at       ON check_runs(run_at);
CREATE INDEX IF NOT EXISTS idx_check_runs_lineage      ON check_runs(repo_id, branch, session_id, run_at);

CREATE TABLE IF NOT EXISTS checks (
    id          TEXT PRIMARY KEY,
    run_id      TEXT NOT NULL REFERENCES check_runs(id),
    subject     TEXT NOT NULL,
    kind        TEXT NOT NULL,
    status      TEXT NOT NULL CHECK(status IN ('pass', 'fail', 'skip', 'error', 'interrupted')),
    fingerprint TEXT,
    -- Kept for a10n's shape (a parked check resumes after last_step from output);
    -- nothing writes them yet.
    last_step   TEXT NOT NULL DEFAULT '',
    output      TEXT NOT NULL DEFAULT '',
    metadata    TEXT NOT NULL DEFAULT '{}',
    checked_at  TEXT NOT NULL,
    UNIQUE(run_id, subject, kind)
);

CREATE INDEX IF NOT EXISTS idx_checks_run              ON checks(run_id);
CREATE INDEX IF NOT EXISTS idx_checks_run_subject_kind ON checks(run_id, subject, kind);
CREATE INDEX IF NOT EXISTS idx_checks_status           ON checks(status);

CREATE TABLE IF NOT EXISTS check_items (
    id         TEXT PRIMARY KEY,
    check_id   TEXT NOT NULL REFERENCES checks(id),
    key        TEXT,
    passed     INTEGER NOT NULL DEFAULT 0,
    metadata   TEXT NOT NULL DEFAULT '{}',
    checked_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_check_items_check ON check_items(check_id);
