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
