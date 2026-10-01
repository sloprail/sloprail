-- A recorded ref the user said to drop. abandoned_tip is the tip it was abandoned AT:
-- the ref is not judged while its tip is still that commit, and is judged again the moment
-- the tip moves, or the commit is pushed or merged.
ALTER TABLE session_refs ADD COLUMN abandoned_tip TEXT NOT NULL DEFAULT '';
