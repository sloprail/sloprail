-- The citation history and the cycle record are bounded (see citations_compact.go): the
-- engine-run step for this version (migrate_data.go) rewrites a store written before the bound in
-- place — repeated stretches folded, commands named by "by" cut, each path's points capped — and
-- gives the freed space back, before this version is recorded. Nothing in the schema changes.
SELECT 1;
