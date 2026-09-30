---
enforced: true
---

# Consult `resultKnown` on BOTH Pre kinds before reading `newContent`

**Mistake:** reading `.event.newContent` on a `PreFileCreate` in a branch that
assumes a create always carries derivable bytes — the belief that "a create is
emitted only when the resulting bytes are known".

That belief is false: `PreFileCreate` carries `resultKnown` exactly as
`PreFileUpdate` does, and it is **false** on a create the engine could not
compute — a `NotebookEdit` creating a fresh `.ipynb` (the tool's cell source is not
the JSON document), or an `sr-file` line it could not resolve. `newContent` is then
`""`, which is not "an empty file". The engine does not fail a gate closed on an
unknown result: the gate must refuse it itself, or the write lands unchecked.

**What a correct script looks like** — dispatch on `.event.kind`, and:

- `PostFileCreate` / `PostFileUpdate`: settled bytes, no `resultKnown` — but
  consult `newContentKnown` first (false: the engine could not READ the settled
  file, so `newContent` is `""`). Fail closed there; read `newContent` only when it
  is true.
- `PreFileCreate` / `PreFileUpdate` (a gate): consult `resultKnown` FIRST, on both
  kinds. When it is not `true`, a gate whose job is to prevent **refuses** (exit 1,
  tell the agent to write the file content directly); a gate that only supplements
  a file-guard of the same name may `exit 0` and let the file-guard judge what
  landed at Stop. Read `newContent` only when `resultKnown` is true.
- A delete, or a kind the guard is not about: nothing to read.
