# Example guardrails

Rules you can copy into your own project. Each folder here is a complete
guardrail — a `GUARDRAIL.md` and the hook scripts it runs — in exactly the shape
the engine reads.

To use one, copy the folder into your project:

    cp -R examples/guardrails/<name> .sloprail/guardrails/

The folder name becomes the guardrail's name, so rename it if you want a
different one. Read the prose in its `GUARDRAIL.md` before enabling it: the body
is the rule, not a description of the rule, and every example here documents
which paths it guards and what it refuses.

## Why they live here rather than in `.sloprail/guardrails/`

This repo's own `.sloprail/guardrails/` is where rules that govern THIS
codebase would go — rules the engine loads and enforces on anyone working here.
An example is not that. It is a file to be read and lifted, and putting it there
would arm it against the repo that ships it, which is a different decision from
publishing it.

Keeping them under `examples/` also means they are outside the tree the engine
scans, so an example that is deliberately not yet runnable — see
`required-context-precondition` — can ship as documentation without refusing
anybody's writes.

## They are held to the same bar as the engine

Each example is driven end to end under `tests/e2e/examples/`, against the file
in this directory rather than a copy pasted into a test. An example whose
declaration stopped loading, or whose hook stopped refusing what it says it
refuses, fails a test — which is the only way a "copyable" rule stays copyable.
