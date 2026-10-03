-- session_refs becomes the table of the RANGES a session answers for, per folder: ref is the
-- head (a branch name, which the range follows, or a commit sha), tip the commit it last
-- pointed at, base the revision the range starts from.
--
-- A file-guard is no longer judged over what this table says passed (the check results, on the
-- branch sloprail/checks, say that); the table says which line of work the agent is answerable
-- for, so the Stop can verify it. Rows an older engine recorded stay and become tracked ranges
-- (added_by = 'auto'), their base computed when they are first read (it is '' until then).
--
-- untracked_reason is empty while the range is tracked; the agent that drops one says why, and
-- the Stop lists it. A ref an older engine had abandoned reads as untracked.
ALTER TABLE session_refs ADD COLUMN base TEXT NOT NULL DEFAULT '';
ALTER TABLE session_refs ADD COLUMN added_by TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE session_refs ADD COLUMN untracked_reason TEXT NOT NULL DEFAULT '';
UPDATE session_refs SET untracked_reason = 'abandoned by an older engine' WHERE abandoned_tip <> '';
