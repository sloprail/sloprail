# File-guard

A file-guard answers "is this change right?" about committed work. It is handed the net
change of a range of commits and judges that; it never sees the working tree, so a
half-finished edit is never judged. It acts only after the fact: to refuse a write or a
delete before it happens, write a [gate](gate.md#preventing-a-write-or-a-delete).

```yaml
# .sloprail/file-guard/preserves-unasked-content/file-guard.yaml
# An edit to memories/ must not drop content nobody asked to remove.
match: 'path startsWith "memories/" and path endsWith ".md"'
deletions: include
require:
  - citation: {source_types: [user]}
    when: ./removes-content.sh
checks:
  - judge: ./change-is-clean.md.j2
```

| key | |
|---|---|
| `match` | which files the rule is about, by path, status, markers or trailers ([matchers.md](matchers.md)); required |
| `require` | what must hold before any check runs: here, a citation of the user's words when the change removes content ([grounding.md](grounding.md#requiring-one)) |
| `checks` | the [scripts](script-checks.md) and [judges](judge-checks.md) that decide, in order |
| `deletions` | whether deleted files are the rule's business ([below](#deleted-files)) |
| `subjects` | a script that splits the change into units judged and cached apart ([below](#subjects-units-judged-apart)) |

## Running it

A file-guard judges an explicit range of commits, `merge-base(base, head)..head`, as one net
diff:

```bash
sr-checks changeset --rule <name> --base origin/main --head HEAD   # what the checks will get; runs nothing
sr-checks run       --base origin/main --head HEAD                 # judges, and stores the verdicts
sr-checks verify    --base origin/main --head HEAD                 # reads stored verdicts; exit 1 on a failure or a gap
sr-checks show      --base origin/main --head HEAD                 # each subject's latest result
```

Run `sr-checks run` in the foreground and wait; it is safe to run several at once. Only `run`
asks a model; `verify` never does. Both `--base` and `--head` are required, and the same
content in the range gets the same verdict however it got there (a rebase, a squash).

Where the verdicts are enforced:

- **At Stop**, an uncommitted change to a file some file-guard selects refuses the Stop until
  it is committed, and a stored failure in the session's work is reported. A range nobody has
  judged yet is not reported at Stop.
- **In CI**, `sr-checks verify` fails on any subject without a stored pass, so run `sr-checks
  run` before a pull request is ready. When the project has file-guards but no CI step, the
  plugin's own rule says how to add one.

A rule judges only work made after it came into force: within the range, it starts from the
commit that added or last changed it, unless it already stood at the base.

## What a check receives

A file-guard's checks get one `Changeset` on stdin instead of an event:

```json
{"event": {"kind": "Changeset"},
 "changeset": {
   "base": "…", "head": "…",
   "commits": [{"sha": "…", "subject": "…", "body": "…", "trailers": {"Sloprail-Cites-User": ["…"]}}],
   "files": [{"path": "…", "status": "M", "oldPath": "", "oldContent": "…", "newContent": "…",
              "oldMarkers": [], "newMarkers": [], "diff": "…"}],
   "others": [{"path": "README.md", "status": "M"}],
   "citations": [{"quote": "…", "sourceTypes": ["user"], "path": "…", "line": 3, "message": "…"}]},
 "subject": {"id": "changeset", "files": ["…"]},
 "transcriptPath": "…"}
```

- `files` are the files `match` selected. `status` is `A` (added), `M` (modified), `R`
  (renamed; `oldPath` is set) or `D` (deleted; only `oldContent` and `oldMarkers`).
  `oldContent` is the file at the base, `newContent` at the head, `diff` its part of the diff.
- `others` are the rest of the range's files, path and status only.
- `citations` are the range's resolved `Sloprail-Cites-*` trailers ([grounding.md](grounding.md)).
- `subject` is the unit being decided. A check's subject is, by default, the whole changeset,
  so a script loops over `.changeset.files[]`, as [check-template.sh](check-template.sh) does.
  A `require` is decided per file: its subject is one file, and the rest of the changeset is
  context.

A check that needs a file `match` did not select reads it from `$SR_TREE`, the committed head
([script-checks.md](script-checks.md#the-environment)), never from the working tree.

## Verdicts are cached by content

A verdict is stored per rule, check and subject, keyed on the content of the subject's files
and its fingerprint. The same content is never judged twice: a stored pass or failure is
reused, in another session, on another branch, and in CI. Editing the rule's own scripts or
template does not re-judge what was already judged.

So a check that reads anything besides its subject's files, such as a sibling file under
`$SR_TREE` or the version of an external spec, must declare it as the subject's
`fingerprint` through `subjects:`. Otherwise it is served a stale verdict when that thing
changes.

## `subjects`: units judged apart

```yaml
match: "specs/**"
subjects: ./subjects.sh
checks:
  - judge: ./review.md.j2
```

The `subjects:` script reads the changeset on stdin and prints a JSON array:

```json
[{"id": "billing", "files": ["specs/billing.md"], "fingerprint": "9f2c"},
 {"id": "auth", "files": ["specs/auth.md"]}]
```

Each subject is judged, cached and reported on its own, and its checks find it under
`.subject`. `files` are selected files; `fingerprint` names whatever else its verdict depends
on, and changing it re-judges that subject only. A subject that is not files (a spec entry, a
name) gives a `fingerprint` and no `files`. The script runs with no session, in CI too, so it
must depend on nothing but the changeset and the repository. To judge each file on its own,
return one subject per file.

## Deleted files

| `deletions:` | the rule sees | for a rule about |
|---|---|---|
| `skip` (the default) | added and changed files | what a file holds; a deleted file holds nothing |
| `include` | deleted files too, as `D` entries | content that must not be lost |
| `only` | deleted files only | a file going away |

A deleted file's `oldContent` and `oldMarkers` are what was lost, and its `markers` in the
match are the ones it carried, so a marker-based match still selects it.

## Reading markers

A check reads each file's `newMarkers` (at the head) and `oldMarkers` (at the base), each a
list of `{kind, fqn, line}`; a marker's text is its `fqn`:

```bash
quote="$(printf '%s' "$payload" \
  | jq -r '.changeset.files[0].newMarkers[] | select(.kind == "asked") | .fqn' | head -1)"
```
