# sloprail

Your agents slop. Take control.

Skills get skipped. Prompts get ignored. Everything drifts into slop and
dies there. sloprail is the layer that holds the line — structure the agent
can't wander out of, checked against what actually happened, not what it
claimed.

<!-- HERO GIF -->

## Skills aren't enough

A skill is a request, not a guarantee — and it might not even
[load](https://sloprail.com/docs/use-cases/knowledge/acted-without-context).
sloprail is the check that runs no matter what the agent does, and reads the
real result, not the agent's word for it.

## Install

sloprail installs as a plugin. Once it's in, every tool call and turn-end
runs through it — you don't run anything by hand.

| Harness | Command |
|---|---|
| Claude Code | `/plugin install sloprail@sloprail-marketplace` |
| Cursor | soon |
| Codex | soon |

Don't see your harness? [We integrate it fast](https://github.com/sloprail/sloprail/issues/new?title=Harness+request%3A+%3Cyour+harness%3E&labels=harness-request).

Get started: [Quickstart](https://sloprail.com/docs/getting-started/quickstart).

## You've seen these happen

<!-- USE-CASE GRID -->

- [The task gets rewritten to match the work](https://sloprail.com/docs/use-cases/tasks/task-rewritten)
- [After enough compaction, it games the score instead of doing the work](https://sloprail.com/docs/use-cases/long-runs/compaction-gaming)
- [A request fell through the cracks](https://sloprail.com/docs/use-cases/tasks/request-fell-through)
- [The same fact, copied into two files, now disagreeing](https://sloprail.com/docs/use-cases/knowledge/duplicated-knowledge)
- [It acted without loading what it needed first](https://sloprail.com/docs/use-cases/knowledge/acted-without-context)
- [The "mechanical" refactor silently rewrote your code](https://sloprail.com/docs/use-cases/coding/refactor-regenerated)

[View all use cases](https://sloprail.com/use-cases).

## Guardrails that arrive with a plugin

A guardrail does not have to be written by the project it governs. A plugin ships
them the way it already ships hooks and skills — a `guardrails/` directory at its
root — and installing that plugin puts those rules into force without the project
copying anything. A team's lint conventions, a framework's "do not edit generated
files", sloprail's own `authoring-slop`: their natural home is the tool, not each
consumer's repo, because every copy is a fork that drifts.

**The repo is what decides.** The engine reads the project's own
`.claude/settings.json` and `.claude/settings.local.json`, takes the plugins
their `enabledPlugins` block turns on, and resolves each to its installation.
Installing a plugin is a decision the repository made and wrote down, so the
repository's settings are the truth about it — not which plugins happened to fire
a hook, which is a smaller set that silently omits any plugin shipping guardrails
without hooks.

The two settings files layer the way Claude Code layers them, which was measured
rather than assumed: **`settings.local.json` wins**, in both directions, so the
gitignored personal layer can switch a plugin off that the committed one turned
on, and back on again.

All of that knowledge — the filenames, the `enabledPlugins` shape, the
`<marketplace>/<plugin>/<version>/` cache layout, the `installed_plugins.json`
schema — lives in exactly one file, `internal/harness/claudecode.go`, named for
the harness it is about. A second harness gets a second file beside it. The
precedent is `internal/transcript/claudecode.go`, which has always been the only
place that names Claude Code's own JSONL spellings.

Because those assumptions can go stale, **an enabled plugin that cannot be
located is reported, never skipped**:

    sloprail: enabled plugin "acme@acme-marketplace" could not be located, so any
    guardrails it ships are NOT enforcing: no installation directory. Looked in: …

That line is the difference between this and a silent break. If a cache layout
moves or the manifest schema is bumped, the user is told which plugin went
missing on the next tool call — rather than the guardrails quietly ceasing to
fire while everything looks correct. It warns rather than refuses: sloprail does
not know whether the missing plugin shipped any guardrails at all, and blocking
every action over a rule that may not exist is a loud failure that is usually
wrong. A rule that *exists* and cannot be checked still refuses, unchanged.

Resolution, when both a project and a plugin have a rule of one name:

- **the project wins**, so a project can always override a rule it did not
  write — and the shadowing is **reported**, because a project that displaced a
  rule and was never told believes it has two protections and has one;
- a refusal from a shipped rule **names the plugin** — `("authoring-slop" from
  plugin "sloprail")` — since the name alone would point at the project's own
  `.sloprail/`, where there is nothing;
- a consumer switches one off from their **own** side, in `.sloprail/config.yaml`,
  because `enabled: false` lives in a declaration they do not own and an edit
  inside an install cache is undone by the next reinstall:

      disabled:
        - sloprail/file-guard/authoring-slop

  The name is qualified by the plugin, so this cannot also switch off a rule of
  your own that happens to share it. It works on a shipped rule that will not
  load, too — otherwise one broken shipped rule wedges every consuming project
  with no remedy but uninstalling the plugin.

A plugin's guardrail is loaded, validated and dispatched by exactly the same code
as a project's; it is the same declaration in a different place. Its hook runs
with its working directory inside the installation, so a shipped
`./check-rules.sh` resolves to the copy that was installed. A shipped hook must
not assume anything on the consumer's `$PATH` silently — sloprail's own checks
for `jq` by name and refuses with a message that says which plugin needs it.

An ordinary install is a frozen **copy** in the plugin cache, so a plugin
author's edits reach a consumer at reinstall rather than immediately — the rules
in force are the ones they installed, and do not change under them because an
author pushed. A marketplace sourced from a local **directory** is the exception,
loaded straight from that directory: it is how a plugin author works on their own
rules, and it is resolved first for that reason.

## The binaries

One binary per high-level command, plus a root that proxies to them. Each
directory under `services/` is named for the binary it builds, so
`go install ./services/...` installs the set — see Installing below.

    sr              the root — `sr session start` runs sr-session
    sr-session      the hook points a harness calls, and what a hook asks
    sr-file         check a file against a CUE schema
    sr-mark         write `// sr:<kind>` markers into source files
    sr-agent        run an agent, whichever harness is running

`sr <command> ...` and `sr-<command> ...` are the same run of the same binary:
stdin, stdout, stderr, signals and the exit status pass through untouched. The
proxy exists so there is one name to learn. The hooks a project installs name
the service binaries directly instead, because a hook fires on every tool call
and has no use for discoverability.

The services find each other as siblings of the running binary, then on `$PATH`;
`SLOP_SUBBIN_DIR` overrides both and is what the e2e harness sets to point at a
temporary build. See `internal/subbin`.

## Installing

    go install ./services/...     # the ordinary path
    make distribute-local         # when a copy is already installed

Both land the whole set in one directory, which is what sibling resolution needs.
Prefer `go install` for a first install: it is the Go-standard path and puts
them in `GOBIN`.

`make distribute-local` exists for the case `go install` handles badly — an
already-installed copy. It builds into `bin/`, then copies over the existing
install and re-signs each binary with `codesign --sign -`, without which macOS
kills a binary copied over a signed one. It installs beside the `sr` already on
`$PATH`, so an upgrade lands where the last install did rather than wherever
`GOBIN` currently points.

    make where                    # print the destination, touch nothing
    make distribute-local PREFIX=~/bin

With no `sr` on `$PATH` and no `PREFIX`, it falls back to `GOBIN` (else
`GOPATH/bin`) — the directory `go install` would have used. It prints the
destination before copying, and warns when that directory is not on `$PATH`,
because a hook that cannot find `sr-session` is the failure this causes.

    make build   the whole set into bin/
    make check   build, vet, gofmt
    make test    unit, then services, then e2e — `-p 1` throughout

`-p 1` is a constraint, not a preference: each e2e package builds the binaries
and drives a mock agent, and a parallel `-race ./...` across the 46 of them ran
the disk out of space.

## How it works

### What sloprail is made of

| Piece | What it does |
|---|---|
| [Grounding](https://sloprail.com/docs/concepts/grounding) | The claim, checked against the artifact. Did the diff really contain the change; does the cited source line actually resolve — not whether a rule merely fired. |
| [Reconciliation](https://sloprail.com/docs/concepts/reconciliation) | A move that has to add up to nothing. The before and after cancel out once known-legit differences are set aside — the empty residue is the proof it was mechanical, not regenerated. |
| [File-guard](https://sloprail.com/docs/concepts/file-guard) | Bound to a file, not a moment. It keeps failing and feeding the error back until the code actually satisfies it — not a one-shot reject. |
| [Gate](https://sloprail.com/docs/concepts/gate) | A checkpoint on one action. It reads what the action requires and either lets it through or blocks it — once, at the moment it matters. |
| [Context](https://sloprail.com/docs/concepts/context) | A scope the agent enters on its own — detected from what's happening, not a step it has to remember. Enforced only while it's active. |
| [Structure-gate](https://sloprail.com/docs/concepts/structure-gate) | A standing map of where writes are even allowed. Deny by default — a path outside the structure never lands. |
| [Marker](https://sloprail.com/docs/concepts/marker) | A durable label pinned on the artifact — a comment in the code that later checks anchor to, surviving renames and moves. |
| [Tag](https://sloprail.com/docs/concepts/tag) | The same idea in the run — a label the agent drops in its own trajectory, that a later check can read back. |

### The engine

A project declares guardrails under `.sloprail/guardrails/`; the harness calls
the session hook points, and the engine runs whichever guardrails bind to what
is about to happen.

    sr-session start < /dev/null    the load check — and the event vocabulary

The event kinds a guardrail may bind to are per-build, and the load check is
what reports them: bind to a kind this build does not produce and it names every
kind it does; misspell a field and it names that kind's real fields with their
types. Both come from the same module registry the enforcement runs on, so they
cannot drift from what the engine does. Ask it rather than trusting a list
written here.

That is the vocabulary, not the format. How to write a guardrail — the
declaration's shape, the matcher operators, the hook contract — is the
`authoring-guardrails` skill the plugin ships.

## Adding a module

A module is a domain the engine understands — files, commands, later markers.
It declares the event kinds it can produce and turns what a harness reported
into events.

1. Write a type satisfying `module.Module` (`internal/module`). It can live
   anywhere and be called anything; `Name()` is the module's own and need not
   resemble its package.
2. Add it to `modules.All` in `internal/module/modules`.

The second step is not optional. A module absent from that list is dead code —
the binary cannot produce its events — and
`TestAll_HoldsEveryModuleInTheRepo` fails until it is there. That test finds
your type by type-checking the repo for implementations of the interface, not
by matching a file or package name.

There is exactly one module list in a build, and `modules.Registry()` is the
only registry a hook point should be handed. A hook point that builds its own
enforces against a vocabulary the load check never reported — invisible to
every other test, and it has reached main twice. `TestOnlyModulesPackageBuildsARegistry`
fails if anything outside `internal/module/modules` calls `module.NewRegistry`.
