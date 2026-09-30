---
enforced: true
---

# Consult `resultKnown` on BOTH Pre kinds before reading `newContent`

**Mistake:** reading `.event.newContent` on a `PreFileCreate` in a branch that
assumes a create always carries derivable bytes — the belief that "a create is
emitted only when the resulting bytes are known".

That belief is false. `PreFileCreate` carries `resultKnown` exactly as
`PreFileUpdate` does, and it can be **false** on a create: a `NotebookEdit`
creating a fresh `.ipynb` emits a `PreFileCreate` whose `newContent` is `""` and
whose `resultKnown` is false, because the tool's cell source is not the JSON
document, so the bytes are not derivable. Both Pre kinds carry
`resultKnown`, and the engine does NOT fail a gate closed on `!resultKnown` for
it — a gate that prevents a write must refuse an unknown result itself, or the
write lands unchecked (`services/sr-session/nature_fileguard.go`
`isUnderivablePreWrite` only flags the case, to quote what sr-file said).

**What a correct script looks like** — dispatch on `.event.kind`, and:

- `PostFileCreate` / `PostFileUpdate`: settled bytes, no `resultKnown` — but
  consult `newContentKnown` first. It is false when the engine could not READ the
  settled file (a link to a FIFO or a device, or past the read cap), and then
  `newContent` is `""`, not the file. Fail closed there (refuse, or apply the
  requirement); read `newContent` only when it is true.
- `PreFileCreate` / `PreFileUpdate` (a gate): consult `resultKnown` FIRST. When it
  is not `true`, a gate whose job is to prevent **refuses** (exit 1, tell the agent
  to write the file content directly) — the engine does not fail it closed. A gate
  that only supplements a file-guard of the same name may instead `exit 0` and let
  the file-guard judge what actually landed at Stop. Only when `resultKnown` is
  true may the branch read `newContent`.
- A delete, or a kind the guard is not about: nothing to read.

**Why this is the judge's and not the grep's.** The grep in `check-rules.sh`
fires when `resultKnown` is named *nowhere* in the script. It cannot catch a
script that *does* name `resultKnown` — for the update case, say — yet still reads
`newContent` on `PreFileCreate` in a branch that assumes the create is derivable.
The word is present, so the grep stays silent; the reasoning about *which kinds*
that branch actually runs on is what catches it.

**Flag** when a `PreFileCreate` (or a Pre branch that includes create) reads
`newContent` without a `resultKnown` guard governing that read, and when a Post
branch reads `newContent` with no `newContentKnown` guard governing that read (or
treats `newContentKnown` false as an empty or passing file). Do NOT flag a Post
branch that consults `newContentKnown` before reading `newContent`.
