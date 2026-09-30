# grounding-citations (gate + file-guard)

A markdown file in this project restates what other files say — a summary, a
migration note — so every claim in it must be referenced, not regenerated: the
write has to cite the tool output the agent actually read, and a judge checks
the file says what that output says.

## Install

Make sure sloprail is installed — see the docs
[install page](https://sloprail.com/docs/getting-started/install/).

Then copy this example into your project:

```bash
git clone --depth 1 https://github.com/sloprail/sloprail /tmp/sloprail-clone && mkdir -p .sloprail && cp -R /tmp/sloprail-clone/examples/grounding-citations/.sloprail/. .sloprail/ && rm -rf /tmp/sloprail-clone
```

Then have the agent write a markdown summary without citing what it read, and
confirm you see the refusal below.

## The rule

An agent summarising a changelog, or writing a migration note, is tempted to
write what sounds right rather than what the source says: a plausible default,
a version number off by one, a claim the source hedges stated flatly. The rule
makes the source a precondition. The agent reads it (a Read, a `cat`), then
writes the markdown with `sr-file`, quoting the output exactly:

```bash
sr-file write MIGRATION.md --cite:tool_result '<exact words from the output>' --content '<the file>'
```

The citation rides on the command, never in the file, so the file holds only its
own content. The engine resolves each quote against the session's record, in the
`tool_result` pool only — the user's words or the agent's own summary never
resolve as source output — so a citation on the event *exists*. A quote that
resolves nowhere is not a citation at all.

## Why a gate, and a file-guard of the same name

The two halves are split by what each is for, and each is a separate folder:

- **`gate/citations-resolve`** is the prevention. It triggers on `PreFileWrite`
  of any `*.md` file (a create or an update), so an ungrounded claim is refused
  *before it lands*, while the agent still has the source in view and can fix the
  write. It requires the citation and runs the judge.
- **`file-guard/citations-resolve`** is the after-check. It acts only at Stop, on
  the settled file, with the same `require` and the same judge. It sees what the
  gate cannot: a markdown file changed by a script the engine did not see as a
  write.

A file-guard alone would only report a bad claim after it was written; a gate
alone would miss a write the engine cannot see ahead. Each carries its own copy
of the judge prompt, since a rule's folder is self-contained.

## The mechanism

`require: citation: {source_types: [tool_result]}` is **unconditional** — every
markdown write here restates a source, so there is no write that needs no
grounding. The engine refuses an uncited write (the Write tool, a shell
redirect) before the judge is paid for, and the refusal names the `sr-file`
form. That is the cheap, deterministic half: does the write carry a citation that
resolved.

The judge (`claims-match-cited-output.md.j2`) answers what existence cannot: does
the file say what the cited output says? It is handed the change (a unified
diff, so it judges only the lines the write adds or alters), the whole file for
context, and each citation — the quote, the whole tool output it came from, and
the call that produced it. It fails a claim no cited output supports, one that
overstates or sharpens the source, one resting on a source the write does not
cite, and a citation whose call merely printed the words the file now claims (an
`echo`, a heredoc).

**Fails closed on an underivable write.** The judge reads the bytes the write is
about to leave. When the engine cannot work them out ahead (a `>` redirect, a
`cp`, an `sr-file` line it could not resolve) `resultKnown` is false and
`newContent` is empty, which reads like an emptied file and would be judged with
no claims in it. The gate's `require-known-result.sh` runs first and refuses it,
telling the agent to write the content directly with `sr-file write`.

A gate is asked about every file a command writes: one command writing two
markdown files is refused if either is ungrounded, and the refusal names it.

## What it does not catch

It checks that what the file claims is in what was read, not that what was read
is true — a source that is wrong grounds a wrong claim. And it watches markdown
only: a claim written into a `.txt` or a source file needs no citation.
