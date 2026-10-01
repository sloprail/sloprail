---
enforced: true
---

# The event's fields are FLAT under `.event`, and per-kind

**Flag** a read of `.event.fields.*` (the OLD `{kind, fields}` envelope) instead of
the flat `.event.<field>`, a read of a field the handled kind does not carry per the
table below, or a file-guard script reading a file event's fields instead of
`.changeset.files[]`.

A new-format check receives a `CheckPayload` whose event is FLAT: the file's own
facts are direct members of `.event`. The fields are

    .event.path        .event.kind         .event.resultKnown
    .event.newContent  .event.oldContent
    .event.newMarkers  .event.oldMarkers   (each element: .kind, .fqn, .line)

See
`internal/declaration/payload.go` (`FlatEvent`) and
`internal/filemod/module.go` (`Kinds()`), the authoritative registry.

**Which field each kind carries** (reading a field the kind omits gets the zero
value, silently):

| kind | carries |
| --- | --- |
| `PreFileCreate` | `path`, `newContent`, `resultKnown`, `newMarkers` |
| `PreFileUpdate` | `path`, `oldContent`, `newContent`, `resultKnown`, `oldMarkers`, `newMarkers` |
| `PreFileDelete` | `path`, `oldContent`, `oldContentKnown`, `oldMarkers` |
| `PostFileCreate` | `path`, `newContent`, `newContentKnown`, `newMarkers` |
| `PostFileUpdate` | `path`, `oldContent`, `newContent`, `newContentKnown`, `oldMarkers`, `newMarkers` |
| `PostFileDelete` | `path`, `oldContent`, `oldMarkers` |

A **file-guard's** check does not receive a file event at all. Its event is
`{"kind": "Changeset"}` and the files are under `.changeset.files[]`, each
`{path, status, oldPath, oldContent, newContent, oldMarkers, newMarkers, diff}`
(committed content, always known), with `.changeset.others`, `.changeset.commits`
and `.changeset.citations` beside it.
