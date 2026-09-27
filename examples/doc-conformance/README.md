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

The marker's URL is remote, so the judge fetches it itself, as exactly
`curl -sL https://code.claude.com/docs/<page>.md` (Claude Code's docs serve
each page as markdown at its path plus `.md`). It pipes that into `grep` or
`head` to cut out the anchor's section and the names the change uses, and
quotes the doc line for any refusal.

WebFetch is only the fallback, for a doc on another host. It returns another
model's summary of the page, not the page. In real precompact-support runs
(2026-09-27), judges that WebFetched the 330 KB hooks page quoted things the
page never says: a `compact_reason` field, and "PreCompact cannot block" where
the page says exit 2 blocks. Three judges ruling on the same code at the same
Stop gave three different field names. The agent rewrote correct code to match
each wrong quote and was refused again, and both runs failed as a stuck loop.
Judges also re-fetched the page 10 to 30 times each, and some ran past the
check timeout with no verdict.

The rubric judges the CHANGE (the diff), not the whole file or the project. A
mock may leave parts of the doc unimplemented. A refusal needs a changed line
that contradicts a quotable doc line.

## Why the curl grant is pinned

`curl` is a network client with dozens of options that write files or send
them, so the grant allows one form only:

```yaml
allowed_tools: ["Bash(curl -sL https://code.claude.com/docs/*)", "Bash(grep:*)", "Bash(head:*)", WebFetch]
disallowed_tools: ["Bash(curl -sL https://code.claude.com/docs/* *)"]
```

The allow pins the command, its flags and the host. The deny takes back any
extra word after the URL (Claude Code's `*` matches across spaces). Measured
through sr-agent (claude 2.1.282, haiku), with the project readonly:

- **ran:** the fetch piped into `grep` or `head`;
- **refused:**
  - `-o` (into the project, into `/tmp`, and attached as `-o/tmp/x`);
  - `-O`, `-sLo`, `--output`;
  - `-d @file`, `-T`, `-F f=@file`, `-H @file`;
  - a second URL, `$(…)` in the URL, `>` redirection;
  - every other host.

An unpinned `Bash(curl:*)` with a deny list over curl's writing flags was
measured to leak: `-sLo`, `--etag-save`, `--stderr`, `--hsts`, `--dump-header`,
`--cookie-jar` and `-H @file` all got through. The pin is the confinement.

`sed` and `awk` are not granted: `sed -n 'w <file>'` and
`awk 'BEGIN{print "x" > "<file>"}'` each wrote a file under their
`Bash(...:*)` grants. `grep` and `head` cannot write.

## Coverage

- e2e: `tests/e2e/examples/044_doc_conformance/` covers the marker match, a
  refusal blocking at Stop and re-firing until fixed, the URL and change
  reaching the prompt, the raw-doc instructions reaching the prompt, and the
  pinned grant and its deny reaching the harness intact.
- eval: `eval/precompact-support/` has a real agent add PreCompact support to
  claude-mock with the rule installed.
