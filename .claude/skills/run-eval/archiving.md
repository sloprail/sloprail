# The run archive

## Where it lives

Every run (unless `--no-archive`) is recorded into a local, git-backed
directory: `$XDG_DATA_HOME/sloprail/eval-runs` (or, on a platform with no
`$XDG_DATA_HOME` set, the OS's own data-home convention — see
`services/sr-eval/archive.go`'s `dataHome`), overridable entirely via
`$SLOPRAIL_EVAL_RUNS_DIR`. One git commit per run, holding a structured
`run.json`, the agent-under-test's transcript (and any sub-agent
transcripts), and the scorer's raw stdout/stderr.

```
sr-eval run --fixture <dir>
# archived under: $XDG_DATA_HOME/sloprail/eval-runs/<fixture-name>/<run-id>/
```

This is what turns "did this fixture start failing on this model" into an
ordinary `git log`/`git diff` question against that directory instead of
something only the last run's own stdout could answer.

## Pushing the archive

The archive directory is an ordinary git repo, with `origin` already
pointing at the shared remote:

```
cd "${XDG_DATA_HOME:-$HOME/Library/Application Support}/sloprail/eval-runs"
git remote -v   # origin  git@github.com:sloprail/sloprail-eval.git
git push
```

(The exact fallback path is platform-specific — see `archiving.md`'s "where
it lives" above, or just run `sr-eval run` once and read the `sr-eval:
archived` line it prints, which names the resolved directory directly rather
than requiring the fallback to be reconstructed by hand.)

Nothing pushes automatically — `sr-eval` only commits locally. A batch of
fixture runs worth keeping (a new fixture verified, a regression fixed and
re-run) should be pushed explicitly once the local runs look right, the same
as any other git remote: check `git log`/`git status` in that directory
before pushing, same discipline as any other repo.
