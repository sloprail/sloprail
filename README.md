# sloprail

Declarative contracts that keep an agent's output honest.

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

There is no setup command. `.sloprail/guardrails/` is created by whatever writes
the first declaration, and a project with none is an ordinary project.

## Guardrails that arrive with a plugin

A guardrail does not have to be written by the project it governs. A plugin ships
them the way it already ships hooks and skills — a `guardrails/` directory at its
root — and installing that plugin puts those rules into force without the project
copying anything. A team's lint conventions, a framework's "do not edit generated
files", sloprail's own `authoring-slop`: their natural home is the tool, not each
consumer's repo, because every copy is a fork that drifts.

The engine finds them through one environment variable, `SR_PLUGIN_DIRS`, a
list of plugin installation directories. It never reads any harness's plugin
manifest: knowing which plugins are installed is the harness's job, so the
harness-specific layer — for Claude Code, the plugin's own `hooks.json`, which is
that harness's format by definition — fills the variable in. A plugin layer for a
different harness sets the same variable and nothing in the engine changes. See
`marketplace/plugins/sloprail/README.md` for that side of it.

Resolution, when both a project and a plugin have a rule of one name:

- **the project wins**, so a project can always override a rule it did not
  write — and the shadowing is **reported**, because a project that displaced a
  rule and was never told believes it has two protections and has one;
- a refusal from a shipped rule **names the plugin** — `("authoring-slop" from
  plugin "sloprail")` — since the name alone would point at
  `.sloprail/guardrails/`, where there is nothing;
- a consumer switches one off from their **own** side, in `.sloprail/config.yaml`,
  because `enabled: false` lives in a declaration they do not own and an edit
  inside an install cache is undone by the next reinstall:

      disabled:
        - sloprail/authoring-slop

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

The install is a frozen **copy**, not a symlink, so a plugin author's edits reach
a consumer at reinstall rather than immediately. That is the right default for a
consumer — the rules in force are the ones they installed, and do not change
under them because an author pushed — and it is why a plugin author testing a
rule should run it from a project checkout rather than expecting the cache to
follow their edits.

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
