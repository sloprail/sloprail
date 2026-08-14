# sloprail

Declarative contracts that keep an agent's output honest.

A project declares guardrails under `.sloprail/guardrails/`; the harness calls
the session hook points, and the engine runs whichever guardrails bind to what
is about to happen.

    sr-guardrail help    the event kinds this build can produce

`guardrail help` is generated from the modules the build registered, so it is
the vocabulary this binary actually has rather than a document written beside
it. Read it there rather than here.

It prints the vocabulary, not the format. How to write a guardrail — the
declaration's shape, the matcher operators, the hook contract — is the
`authoring-guardrails` skill the plugin ships.

There is no setup command. `.sloprail/guardrails/` is created by whatever writes
the first declaration, and a project with none is an ordinary project.

## The binaries

One binary per high-level command, plus a root that proxies to them. Each
directory under `services/` is named for the binary it builds, so
`go install ./services/...` installs the set.

    sr              the root — `sr session start` runs sr-session
    sr-session      the hook points a harness calls, and what a hook asks
    sr-guardrail    the event kinds this build can produce
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
enforces against a vocabulary `guardrail help` never printed — invisible to
every other test, and it has reached main twice. `TestOnlyModulesPackageBuildsARegistry`
fails if anything outside `internal/module/modules` calls `module.NewRegistry`.
