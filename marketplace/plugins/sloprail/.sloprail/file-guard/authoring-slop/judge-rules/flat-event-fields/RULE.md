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

**Flag** a read of `.event.fields.*` (the old envelope), or a read of a field the
handled kind does not carry per the table above.
