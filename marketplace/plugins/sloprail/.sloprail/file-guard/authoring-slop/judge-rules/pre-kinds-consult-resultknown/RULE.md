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
document, so the bytes are not derivable. The engine gates BOTH Pre kinds on
`!resultKnown` and fails a preventive guard closed on either
(`services/sr-session/nature_fileguard.go` `isUnderivablePreWrite`).

**What a correct script looks like** — dispatch on `.event.kind`, and:

- `PostFileCreate` / `PostFileUpdate`: settled bytes, no `resultKnown` — but
  consult `newContentKnown` first. It is false when the engine could not READ the
  settled file (a link to a FIFO or a device, or past the read cap), and then
  `newContent` is `""`, not the file. Fail closed there (refuse, or apply the
  requirement); read `newContent` only when it is true.
- `PreFileCreate` / `PreFileUpdate`: consult `resultKnown` FIRST. When it is not
  `true`, **defer to the Post kind** — `exit 0` at Pre and let the Stop
  after-check judge what actually landed. Only when `resultKnown` is true may the
  branch read `newContent`.
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
