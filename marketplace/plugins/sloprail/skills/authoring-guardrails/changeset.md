# Seeing a changeset: `sr-session changeset`

A file-guard judges **commits**, not the working tree: the net change from a base
to `HEAD`, as one squashed diff. Before the rule refuses anything, you can see
exactly what it will be handed.

```bash
sr-session changeset --rule size-limit
```

It prints JSON and **runs nothing**: no check, no judge, no verdict recorded, no
watermark moved. Use it to write a rule's `match`, script and rubric against real
input, and to find out why a rule selected (or missed) a file.

`--rule` is the rule's folder name (`size-limit`) or its qualified name as a
refusal cites it (`file-guard/size-limit`, `<plugin>/file-guard/size-limit`).

## What it prints

| key | meaning |
|---|---|
| `rule` | the qualified name |
| `origin` | which base was used: `watermark`, `floor` or `session-start` |
| `base`, `head` | the range, as SHAs |
| `droppedWatermark` | a watermark that was no longer reachable (an amend or rebase), when there was one |
| `ruleHash` | a hash of the rule's whole folder; edit anything in it and old verdicts stop applying |
| `unresolvedCitations` | `Sloprail-Cites-*` trailers whose quote did not resolve |
| `payload` | exactly what a check receives on stdin: `event`, `changeset`, `subject` |

`payload.changeset` is `{base, head, commits, files, others, citations}`:

- `commits` — every commit in the range, oldest first: `sha`, `subject`, `body`,
  `trailers` (trailer key in canonical case, e.g. `Sloprail-Cites-User`, to its values).
- `files` — the files the rule's `match` selected, in full: `path`, `status`
  (`A` `M` `D` `R`), `oldPath` (a rename), `oldContent`, `newContent`,
  `oldMarkers`, `newMarkers`, `diff` (that file's part of the squashed diff).
- `others` — the rest of the range as `{path, status}` only.
- `citations` — the quotes the range's commits cite, resolved (see below).

## Where the range starts

The base is the first of these that exists and is still an ancestor of `HEAD`:

1. **the rule's watermark** — the last head it passed, at its current definition;
2. **the last commit that touched the rule's folder** — for a rule that lives in
   this repository. A rule applies going forward from the commit that added or
   changed it, so files already on `main` are not judged until a change touches them;
3. **the HEAD recorded when the session began** — for a plugin's rule (its folder
   is in the plugin cache, not the repo) and for a repo rule not committed yet.

Every base is a SHA, never a branch name, and is checked with
`git merge-base --is-ancestor` on every run. An amend, rebase or branch switch
that orphans a base drops it and the next one is used. If nothing usable is left —
the session start was never recorded, or the tree left its history — the command
**fails** rather than guess, exactly as a Stop would refuse. A range where `match`
selects nothing is a success with no `files`; a range that could not be computed
is an error. The two are never the same.

## Selecting files: what `match` sees

`path`, `status`, `markers`, `oldMarkers`, `context`, and `trailers`
(each trailer key to its values across the range's commits):

```yaml
match: 'status == "A" and "move-only" in (trailers["Sloprail-Refactor"] ?? [])'
```

`deletions:` is a filter on `status`: `skip` (the default) leaves deleted files out
of `files` and names them in `others`; `include` lets them in (with `oldContent` and
`oldMarkers`, no `newContent`, and `match` sees their `oldMarkers` as `markers`);
`only` keeps just the deleted files. A rename is not a deletion.

## Citations come from the commits

`Sloprail-Cites-User: <quote>` and `Sloprail-Cites-Tool: <quote>` trailers are
resolved like `sr-file --cite:user`: the quote must match exactly one real user
message (or tool output), model text is never citable, and a quote that resolves
nowhere is not a citation. The current session's transcript is searched first,
then the project's other sessions newest to oldest; the first session containing
the quote must match it exactly once. Outside a session (no
`CLAUDE_CODE_SESSION_ID`) there is nothing to resolve against and `citations` is empty.
