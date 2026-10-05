-- What a session remembers of folders and branches that no longer exist is dropped (prune.go): the
-- engine-run step for this version (migrate_data.go) removes the untracked ranges, registered
-- folders and observations of gone folders and gone branches, the keys older engines wrote and
-- nothing reads, and a verify memo past its cap, before this version is recorded. Nothing in the
-- schema changes.
SELECT 1;
