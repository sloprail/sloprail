# Writing a file-guard

A file-guard judges **one file's state**: what a file holds after a change, and —
when it is `preventive` — what a write is about to make it hold. Its whole job is
to answer "is this file OK?", and to keep re-firing until it is.

```
.sloprail/file-guard/<name>/file-guard.yaml
```

```yaml
# preserves-unasked-content — an edit must not silently drop content nobody
# asked to remove.
match: 'path startsWith "memories/" and path endsWith ".md"'
preventive: true
deletions: include
checks:
  - script: ./removal-has-a-grounded-ask.sh
  - judge: ./change-is-clean-and-absolute.md.j2
    prepare: ./collect-quote-and-diff.sh
```

`match` narrows to the files this rule is about (a glob or an expression —
[matchers.md](matchers.md)). `checks` is the list of checks, run in order, first
refusal ending it — each a script ([script-checks.md](script-checks.md)) or a
judge ([judge-checks.md](judge-checks.md)). `preventive` and `deletions` are the
two nature-specific knobs, below; both are optional.

A file-guard's match sees the file's own facts **bare**: `path`, `markers`,
`context` — not `event.path`. It reasons about a settled file, so `markers` is
the one set of markers that file carries; test them with a quantifier,
`any(markers, .kind == "invariant")`.

## After-check (default) vs preventive

A file-guard fires at the **end of a turn**, on the file's **settled** state,
established by diffing the tree against the baseline taken at session start —
never by trusting what any action announced. This is the **after-check**, and it
is the default. The change is already on disk, so refusing does not undo it; it
tells the agent the cycle is not finished and it must fix what it did. That makes
the after-check right for a rule about the **result** of a turn ("every new file
under `memories/` has frontmatter").

Add `preventive: true` and the guard **also** fires on the **pre-write**, before
the bytes land, so it can refuse the write outright — the useful moment for a
rule you would rather enforce *before* the loss than report *after* it. A
preventive guard therefore fires at both moments: the pre-write to prevent, and
the after-check at Stop on the settled file as a backstop.

```yaml
preventive: true
```

The two moments are one script's job, and the event's `kind` tells them apart —
a `Pre*` kind at the pre-write (read the pending bytes off the event), a
`Post*` kind at Stop (the bytes are on disk). See "The pending bytes" below.

Why keep the after-check even when preventive: a command / `sed` / `git` edit
whose result the engine **cannot derive** reaches the pre-write stage
unverifiable. For a preventive guard the engine **fails closed on that pre-write
itself, before the check runs**, and re-judges the settled file at Stop instead
— so the after-check is the backstop no unusual writer slips past. Dropping
`preventive` loses the prevention; the after-check you get either way.

## Deleted files: `deletions`

A deleted file has no end state — no `newContent`, no `newMarkers` — so most
guards have nothing to judge once it is gone. `deletions` says whether a delete
is this guard's business:

| `deletions:` | the guard runs on | use it for |
|---|---|---|
| `skip` (**default**, also when absent) | creates and updates | a rule about what a file **holds** — frontmatter, citations, a rubric. A deleted file holds nothing. |
| `include` | creates, updates **and** deletes | a rule that also covers **losing** the file — "no content under `memories/` is removed unasked", "an invariant-pinned file may not quietly disappear". |
| `only` | deletes only | a rule that exists purely to catch a file **going away**. |

```yaml
deletions: include
```

It applies at both moments: with `preventive: true`, a guard that includes
deletions is also asked on the `PreFileDelete`, so it can refuse the delete
before it happens; every guard that includes them is asked on the
`PostFileDelete` at Stop. A guard on the default never sees either — do not
write a script branch to wave deletes through, leave the key off.

On a delete, a check reads what was lost: `oldContent` and `oldMarkers`. The
guard's own `match` sees the deleted file's markers too — for a delete, the
scope's `markers` is the file's `oldMarkers` — so a marker-scoped guard
(`any(markers, .kind == "invariant")`) that includes deletions still selects the
file it is about.

One key with three values, not a list of events: a file-guard binds to a file's
state, and "is a file that no longer exists my business" is the one place that
question forks. Anything other than the three values is refused when the rule
loads (`sr-file declarations .sloprail` reports it).

## Re-fire and revalidation

A refused after-check does not advance the cycle's read mark. The engine
re-judges the same span on the next Stop — deliberately, so the agent can fix
what was refused and a rule that stays unsatisfied stays reported rather than
scrolling away.

**Revalidation** keeps this from re-judging content that has not changed: a file
already judged against the same content fingerprint is skipped. This is why a
rewrite that produces **byte-identical** content fires **no event at all** —
worth remembering when a cross-cycle rule records what it saw
([state-management.md](state-management.md)).

## The pending bytes

Create and update differ in how they answer "what will this file hold
afterwards", and the difference decides which field a check reads. ([events.md](events.md)
has the exact field set each file kind carries; this section is how a file-guard
*uses* them.)

A **create** carries the pending body in `newContent`. The file does not exist
yet, so a check that wants to look at what would be written has nowhere else to
look. A create's `newContent` is its whole result — there are no prior bytes, so
a create has **no `oldContent`**.

An **update** carries the file's current bytes in `oldContent` and the post-edit
bytes in `newContent`, paired with a **`resultKnown`** boolean saying whether the
engine could work `newContent` out. The pair exists because absence cannot say
it: a declared field the event omits is filled with its type's zero value, so an
uncomputable `newContent` and a genuinely emptied file would be the same
observation. A `sed -i` whose outcome is unknowable would otherwise look like a
command that empties the file.

**A delete** carries `oldContent` and `oldMarkers` — the bytes about to be lost
and their markers — but no `newContent` or `newMarkers`. Nothing remains, so
there is no result to read. A delete reaches only a guard whose `deletions:` is
`include` or `only` (above).

### The resultKnown discipline

This is the trap that makes a preventive file-guard silently permissive. On a
`PreFileUpdate` an **absent `newContent` reads as the empty string**, which is
indistinguishable from a write that empties the file. So:

**Guard on `resultKnown` before you read `newContent`.** In a match:

```
resultKnown and not (newContent contains "---")
```

refuses a write that would strip the frontmatter, **and says nothing where the
engine cannot see** — because the `resultKnown &&` short-circuits false when the
result is underivable. To deliberately catch the underivable cases instead:

```
not resultKnown
```

In a **script** that reads the bytes, check presence first:

```bash
if ! printf '%s' "$input" | jq -e '.event | has("newContent")' >/dev/null 2>&1; then
  echo "Refusing the write to $path: its result cannot be computed (an in-place or environment-dependent command), so it cannot be checked. Write the file directly." >&2
  exit 1
fi
```

A **judge** bound to an update should **defer** when the result is not known
(`resultKnown` is false / `newContent` absent) and let the after-check judge what
actually landed at Stop — `exit 0` silently, because that is the designed path,
not an anomaly. Neither moment alone covers the ground: the pre-write catches
the derivable writes early, the after-check catches everything on the settled
file.

For a `preventive` guard the engine already fails closed on an underivable
pre-write before the script runs (so the script never sees it), but writing the
guard anyway keeps the script correct on its own and readable to the next
author.

A create needs none of this: `PreFileCreate` always carries `newContent`, so a
create-only check can read it directly.

### Which bytes, at which moment

Since a preventive guard fires at both moments, a script dispatches on
`.event.kind`:

```bash
kind="$(printf '%s' "$event" | jq -r '.event.kind // empty')"
case "$kind" in
  PreFileCreate) body="$(printf '%s' "$event" | jq -r '.event.newContent // ""')" ;;
  PreFileUpdate)
    known="$(printf '%s' "$event" | jq -r '.event.resultKnown // false')"
    [ "$known" = "true" ] || exit 0            # defer to the after-check
    body="$(printf '%s' "$event" | jq -r '.event.newContent // ""')" ;;
  PostFileCreate|PostFileUpdate)
    abs="${SR_WORKSPACE:-.}/$path"             # settled: read disk
    [ -f "$abs" ] || exit 0
    body="$(cat "$abs")" ;;
esac
```

The `Post` kinds carry `newContent` too, but reading disk keeps that branch
identical whatever a Post event happens to carry, and the bytes on disk **are**
what the cycle produced. `SR_WORKSPACE` is set on the check's environment by the
engine ([environment.md](environment.md)).

## Markers

A file event carries the `// sr:<kind>` markers as a **list** of `{kind, fqn,
line}` — `newMarkers` (the result's markers) on the create and update kinds,
`oldMarkers` (the file's current markers) on the update and delete kinds. The
per-kind field set and the element shape are in [events.md](events.md); read them
in a check with a quantifier:

```
any(newMarkers, .kind == "decision")     would the result carry one
len(newMarkers) == 0                      does the result carry none
any(oldMarkers, .kind == "asked")         does the file already carry one
```

Note the distinction from a **file-guard's own match scope**, which exposes the
settled file's markers under the single name `markers` (`any(markers, .kind ==
"invariant")`) — on a delete, the markers the deleted file carried. `newMarkers`/`oldMarkers` are the **event's** fields — what a
`Pre`/`Post` file event carries, read by a check off `.event.newMarkers`. In a
script, a marker's quote is on `.fqn`:

```bash
quote="$(printf '%s' "$input" \
  | jq -r '(.event.newMarkers // [])[] | select(.kind == "asked") | .fqn' | head -1)"
```

Write markers with `sr-mark`; see its `--help`.

## Turning one off

Keep the folder and set the guard inert. A file-guard has no per-rule enable
flag in the way the old format did — disable it from `.sloprail/config.yaml` by
its qualified name, which is also how you disable a plugin's:

```yaml
disabled:
  - <plugin-or-project>/file-guard/<name>
```

The sibling prose (a `RUBRIC.md`, a `README.md`, comments in the YAML) holds the
reasoning that produced the rule — keep it, so the next person deciding whether
to switch it back on has the argument in front of them.
