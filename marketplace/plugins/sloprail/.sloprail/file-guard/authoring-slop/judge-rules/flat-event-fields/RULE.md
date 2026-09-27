---
enforced: true
---

# The event's fields are FLAT under `.event`, and per-kind

**Mistake:** reading a file event's fields from the wrong place — `.event.fields.newContent`
(the OLD `{kind, fields}` envelope) instead of the flat `.event.newContent`, or
reading a field the kind does not carry.

A new-format check receives a `CheckPayload` whose event is FLAT: the file's own
facts are direct members of `.event`. The fields are

    .event.path        .event.kind         .event.resultKnown
    .event.newContent  .event.oldContent
    .event.newMarkers  .event.oldMarkers   (each element: .kind, .fqn, .line)

read as `.event.<field>`, never `.event.fields.<field>`. See
`internal/declaration/payload.go` (`FlatEvent`) and
`internal/filemod/module.go` (`Kinds()`), the authoritative registry.

**Which field each kind carries** (reading a field the kind omits gets the zero
value, silently):

| kind | carries |
| --- | --- |
| `PreFileCreate` | `path`, `newContent`, `resultKnown`, `newMarkers` |
| `PreFileUpdate` | `path`, `oldContent`, `newContent`, `resultKnown`, `oldMarkers`, `newMarkers` |
| `PreFileDelete` | `path`, `oldContent`, `oldMarkers` |
| `PostFileCreate` | `path`, `newContent`, `newMarkers` |
| `PostFileUpdate` | `path`, `oldContent`, `newContent`, `oldMarkers`, `newMarkers` |
| `PostFileDelete` | `path`, `oldContent`, `oldMarkers` |

So there is no `oldContent` on a create, no `newContent`/`newMarkers` on a
delete, and `resultKnown` on the two Pre write kinds only. A script that reads
`.event.oldContent` on a create, or `.event.newMarkers` on a delete, is reading a
field that is never there.

**Not a file event's fields.** `.event.invocations` and `.event.tags` belong to a
GATE's command event, not a file-guard's file event; a file-guard script reading
them off `.event` gets nothing. Match the field to the nature.

**The normalized-history exception — not this mistake.** `sr-session trajectory
normalize` emits each *historical* event in the OLD `{kind, fields}` wire form, so
a past command's invocations legitimately sit under `.fields.invocations`, not
`.invocations` — this is a different JSON document from the live check's own
stdin, not a stale read of it. A script that pipes `trajectory normalize`'s output
through `jq` and reads `.fields.*` / matches `.kind == "..."` on THAT piped output
is using the documented normalized-history shape correctly, even in the same
script that also (correctly) reads the live `CheckPayload`'s own event flat as
`.event.*`. Two different payloads, two different shapes — do not flag the
`.fields.*` read on normalize's output as if it were a `.event.fields.*` read on
the live event. See events.md, "The normalized-history exception", and
`sr-session trajectory normalize --help`.

**Flag** a read of `.event.fields.*` (the old envelope) on the check's OWN live
payload, or a read of a field the handled kind does not carry per the table
above. Do **not** flag `.fields.*` (or a bare `{kind, fields}` shape) when it is
read from `sr-session trajectory normalize`'s piped output — check whether the
value being read there came from `normalize` before treating `.fields.*` as a
violation.
