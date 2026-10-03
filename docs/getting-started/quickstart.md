---
title: Your first guardrail
description: Write a rule that refuses an agent action, and prove it fires — in five minutes.
kind: tutorial
---

By the end of this you'll have a working guardrail that refuses a real
agent action, and you'll have *watched it fire* — because a rule that loads
but never refuses is worse than no rule at all.

## Before you start

You need the plugin — see [Install](/getting-started/install). In short, in
Claude Code:

```bash
/plugin marketplace add sloprail/sloprail
/plugin install sloprail@sloprail-marketplace
```

That registers the hooks, and the next session installs the `sr*` binaries
they call. From now on, every tool call and every turn-end
runs through sloprail.

## 1. Write the rule

A guardrail is a folder. The folder name *is* the rule name. Create one:

```bash
mkdir -p .sloprail/file-guard/no-todo-in-committed-code
```

Add its declaration — a `file-guard`, because we're judging one file's
state:

```yaml
# .sloprail/file-guard/no-todo-in-committed-code/file-guard.yaml
match: path endsWith ".ts"
checks:
  - script: ./check.sh
```

And the check — a script whose exit code is the verdict (0 permits,
non-zero refuses):

```bash
#!/usr/bin/env bash
# .sloprail/file-guard/no-todo-in-committed-code/check.sh  (chmod +x)
# stdin is the payload: .changeset.files[] holds each committed file's path and newContent
if jq -e 'any(.changeset.files[]; (.newContent // "") | contains("TODO(no-ship)"))' >/dev/null; then
  echo '{"reason": "A file in the committed range has a TODO(no-ship) marker — resolve it before committing."}'
  exit 1
fi
```

## 2. Prove it fires

This is the part most people skip, and it's the whole point. Ask your agent
to write and commit a `.ts` file containing `TODO(no-ship)`, then judge the
commits:

```bash
sr-checks run --base origin/main --head HEAD
```

The rule **refuses**, and the reason you wrote is shown back to the agent.
(A file-guard judges the committed result; the Stop hook and CI only verify the
stored verdicts with `sr-checks verify`, without asking a model.)

A file-guard's verdict only binds where it is enforced. Add a CI job that runs
`sr-checks verify` on every pull request (`--base` the target branch, `--head`
the PR head sha) and on every push to the default branch (`--base` the push's
before sha, `--head` its after sha), and put the marker `sr:ci verify` in a comment beside that step (`sr-mark apply ci --verify=<path>:<line>` writes it; any provider:
GitHub Actions, GitLab CI, Azure Pipelines, Jenkins, ...). Until a committed file
carries that marker, the shipped `sloprail/gate/ci-verify-required` refuses the
end of the agent's turn (and `sloprail/file-guard/ci-verify-step` refuses a marked file that does not run `sr-checks verify` on pull requests and on pushes to the default branch) and prints a snippet per provider (it installs `sr-checks` with `go install`, pinned to the revision the plugin was installed from, because no release carries it yet); switch it off with
`disabled: [sloprail/gate/ci-verify-required]` in `.sloprail/config.yaml`.

Now ask it to commit a `.ts` file *without* that marker. The run passes.

If both happened, your guardrail is real. If the bad file passed,
your rule loaded but didn't fire — which is the failure this whole product
exists to prevent.

## What you just learned

- A guardrail is a **folder under `.sloprail/`**, named for the rule.
- It has a **nature** (here, `file-guard`) that decides when it runs.
- Its **check** is a script (exit code = verdict) or a judge.
- **Loading is not firing** — always confirm the refusal actually happens.

## Next

- [The three rule kinds →](/guides/file-guard) — file-guard,
  gate, context, and when to use each.
- [Guides →](/guides) — real failures and the guardrails that catch them.
