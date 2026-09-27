# doc-conformance (file-guard)

## The rule

A mock or emulator must follow the same contract as the real thing. Code that
emulates something documented carries an `sr:docs <URL>` marker naming the doc
section it follows, and a judge checks that each change to that code agrees
with what the doc says now.

## Why file-guard

The question is about the file's state: does the marked code still match its
doc? A file that drifts keeps failing every cycle until it is fixed or its
marker is corrected, whichever event last touched it. The match is the marker
(`any(markers, .kind == "docs")`), not a path, so any file that makes the claim
is held to it and no other file is.

## Why the judge reads the raw doc

The marker's URL is remote, so the judge fetches it itself. It uses `curl` on
the page's markdown form (Claude Code's docs serve it at the page path plus
`.md`). It cuts out the anchor's section and the names the change uses with
`grep`, `sed -n` or `head`, and quotes the doc line for any refusal.
`allowed_tools` grants exactly `Bash(curl:*)`, `Bash(grep:*)`, `Bash(sed:*)`,
`Bash(head:*)` and `WebFetch`.

WebFetch is only the fallback, for a host that serves no markdown. It returns
another model's summary of the page, not the page. In real precompact-support
runs (2026-09-27), judges that WebFetched the 330 KB hooks page quoted things
the page never says: a `compact_reason` field, and "PreCompact cannot block"
where the page says exit 2 blocks. Three judges ruling on the same code at the
same Stop gave three different field names. The agent rewrote correct code to
match each wrong quote and was refused again, and both runs failed as a stuck
loop. Judges also re-fetched the page 10 to 30 times each, and some ran past
the check timeout with no verdict.

The rubric judges the CHANGE (the diff), not the whole file or the project. A
mock may leave parts of the doc unimplemented. A refusal needs a changed line
that contradicts a quotable doc line.

## What the tool grant does not confine

Measured with the real CLI, with these tools granted:

- piping `curl` into `grep`, `sed -n` or `head` is allowed;
- `python3`, `rm` and `sed -i` are refused.

`awk` is left out because `Bash(awk:*)` let `awk 'BEGIN{print "x" > "/any/path"}'`
write a file anywhere.

`Bash(curl:*)` is still a network client. `curl -o <path>` writes wherever the
path says, even inside a directory the judge may otherwise only read, and
`-X POST -d @file` sends a file's bytes out. The rubric tells the judge to pipe
and never write, but that is an instruction, not a permission. Deny rules on
curl's write and upload flags (`Bash(curl * -o *)` and similar) were measured
to close those paths. A judge check has no `disallowed_tools` key to carry them
yet.

## Coverage

- e2e: `tests/e2e/examples/044_doc_conformance/` covers the marker match, a
  refusal blocking at Stop and re-firing until fixed, the URL and change
  reaching the prompt, the raw-doc instructions reaching the prompt, and the
  scoped tools reaching the harness intact.
- eval: `eval/precompact-support/` has a real agent add PreCompact support to
  claude-mock with the rule installed.
