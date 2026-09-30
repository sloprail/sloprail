-- One rule's verdict on one changeset (or one subject of it), and how far each
-- rule has passed.
--
-- The key is everything a verdict depends on and nothing it does not:
--
--   rule, rule_hash  the rule, and a hash over its WHOLE folder (yaml, scripts,
--                    templates). Edit any of them and every old verdict stops
--                    applying. a10n's key left the rubric out and kept serving
--                    stale passes.
--   check            which of the rule's checks reached the verdict, so two
--                    checks on one changeset do not overwrite each other.
--   subject          the unit judged: "changeset" for the whole range, or a
--                    subject id once `subjects:` exists. Part of the key now so
--                    adding subjects reshapes nothing.
--   model            the judge model ("" for a script), so a model change misses.
--   fingerprint      exactly what the check was given. Never a commit SHA: a
--                    rebase changes SHAs and not content, and must still hit.
--
-- A failing verdict is TERMINAL: it is replayed on every Stop and never re-judged
-- until the input, and so the fingerprint, changes.
--
-- stale marks a failure whose input is no longer the live one. It is kept, not
-- deleted, because the input can come back (an edit reverted) and identity is
-- content; it just stops being outstanding while it is not live.
CREATE TABLE changeset_verdicts (
    rule        TEXT NOT NULL,
    rule_hash   TEXT NOT NULL,
    "check"     TEXT NOT NULL,
    subject     TEXT NOT NULL,
    model       TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    passed      INTEGER NOT NULL,
    reasoning   TEXT NOT NULL,
    -- JSON array of the paths the verdict names; "[]" when it names none.
    files       TEXT NOT NULL,
    stale       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (rule, rule_hash, "check", subject, model, fingerprint)
) WITHOUT ROWID;

-- The last head a rule passed, per definition hash. A rule whose definition
-- changed has a new hash and so no watermark: it starts again from its floor.
CREATE TABLE rule_watermarks (
    rule      TEXT NOT NULL,
    rule_hash TEXT NOT NULL,
    head      TEXT NOT NULL,
    PRIMARY KEY (rule, rule_hash)
) WITHOUT ROWID;
