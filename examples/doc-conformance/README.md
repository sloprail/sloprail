# doc-conformance (file-guard)

Code that emulates something documented carries a marker naming the doc
section it follows, and a judge checks each change still matches what that
doc says now.

## Install

Make sure sloprail is installed — see the docs
[install page](https://sloprail.com/docs/getting-started/install/).

Then copy this example into your project:

```bash
git clone --depth 1 https://github.com/sloprail/sloprail /tmp/sloprail-clone && mkdir -p .sloprail && cp -R /tmp/sloprail-clone/examples/doc-conformance/.sloprail/. .sloprail/ && rm -rf /tmp/sloprail-clone
```

Then change marked code so it drifts from the doc it claims to follow, and
confirm you see the refusal below.

## The rule

A mock or emulator must follow the same contract as the real thing. Code that
emulates something documented carries an `sr:docs <URL>` marker naming the doc
section it follows, and a judge checks that each change to that code agrees
with what the doc says now.

## Why file-guard

The question is about the file's state: does the marked code still match its
doc? A file that drifts keeps failing every cycle until a commit fixes it or corrects
its marker. The match is the marker
(`any(markers, .kind == "docs")`), not a path, so any file that makes the claim
is held to it and no other file is.

It judges **commits**: at Stop, uncommitted changes to a file the rule selects
refuse the turn with "commit these" (nothing is committed for the agent), and the
rule then runs once over the range from where it last passed to `HEAD`, handed to
the judge as one squashed diff (`{{ change }}`) and the files (`changeset.files`).
A refused range is never partly passed, so a fix is judged together with the
commit it fixes.

## Why the judge reads the raw doc

The marker's URL is remote, so the judge fetches it itself, as exactly
`curl -sL https://code.claude.com/docs/<page>.md` (Claude Code's docs serve
each page as markdown at its path plus `.md`). It pipes that into `grep` or
`head` to cut out the anchor's section and the names the change uses, and
quotes the doc line for any refusal.

There is no WebFetch, for two reasons.

- It returns another model's summary of the page, not the page.
- An unscoped WebFetch, pointed at a URL the judged agent wrote, by a judge that
  can read the project, is a way to send the project out.

A marker citing a doc on another host is refused as one this rule cannot read.
To support another host, add its own pinned curl pair.

In real precompact-support runs (2026-09-27), judges that WebFetched the 330 KB hooks page quoted things the
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
allowed_tools: ["Bash(curl -sL https://code.claude.com/docs/*)", "Bash(grep:*)", "Bash(head:*)"]
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
  refusal blocking at Stop and judged again with the fix commit until fixed, the URL and change
  reaching the prompt, the raw-doc instructions reaching the prompt, and the
  pinned grant and its deny reaching the harness intact.
- eval: `eval/precompact-support/` has a real agent add PreCompact support to
  claude-mock with the rule installed.
