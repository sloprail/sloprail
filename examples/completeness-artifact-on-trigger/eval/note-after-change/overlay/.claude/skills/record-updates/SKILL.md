---
name: record-updates
description: Use at the end of any turn in this repo where you changed code, made a decision, or decided there's nothing worth recording — every turn must declare which of the three applies.
---

# Recording What a Turn Did

Every turn in this repo must end by declaring one of three tags, as `#`
text anywhere in your final message:

- `#update` — you changed something. Also write a short note to
  `memories/updates/<short-name>.md` describing what changed and why.
- `#decision` — you made a decision worth recording. Also write a note to
  `memories/decisions/<YYYYMMDD>_<slug>/NOTE.md` explaining the decision.
- `#skip` — nothing this turn is worth recording (e.g. you only answered a
  question, or made no change). No artifact needed for this one.

`#update` and `#decision` both require the matching artifact file to
actually be written in the same turn — declaring the tag alone is not
enough.
