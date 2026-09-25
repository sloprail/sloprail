---
name: link-people
description: Use whenever creating or removing a file under people/ in this repo — every person record must be linked from at least one updates/ or decisions/ file.
---

# Linking Person Records

This repo keeps one file per person under `people/<name>.md`. A person
record on its own, with nothing else in the repo referring to them, is
treated as orphaned.

- **Creating** `people/<name>.md`: in the same turn, make sure at least
  one file under `updates/` or `decisions/` mentions that person's name
  (e.g. a short note about why they were added).
- **Removing** `people/<name>.md`: in the same turn, make sure nothing
  under `updates/` or `decisions/` still mentions their name — clean up
  any references, or the deletion will be flagged as leaving dangling
  links.
