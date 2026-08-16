# Steps format, measured against two live rules

Spike only. No engine change, no rule replaced — two `.check.yaml` written
beside the rules they would replace, so "cleaner or not" stops being a feeling.

The format is not invented here. It is `check.yaml` from a10n
(`marketplace/plugins/a10n-impl-checks/checks/*/check.yaml`), which already runs
`matcher` + `group_by` + `steps: [bash|cache|agent]` with `template_file`,
`model` and `validate_file`. The only thing added for sloprail is a `matcher`
that selects on a FILE rather than on a context link.

## The measurement

Counted as CODE lines (comments and blanks excluded), because this repo's scripts
are more than half comment and counting those would flatter the result:

| rule | declaration today | script today | of that, runtime | rule proper |
|---|---|---|---|---|
| task-evidence-resolves | 23 | 101 code (198 raw) | 43 | 58 |
| task-review | 23 | ~200 code (420 raw) | ~170 | ~30 |

Two different answers, and the difference is the finding.

For the simple rule the runtime share is 43 of 101 — real, but the rule is still
the larger half. For the judged rule it inverts completely: ~30 lines of rule
under ~170 of machinery.

## What it buys, concretely

**The 4× binding disappears.** Every rule here carries `PreFileCreate`,
`PreFileUpdate`, `PostFileCreate`, `PostFileUpdate` with an identical matcher and
an identical command. Ten guardrails in the strategy repo do the same. That
repetition is not expressive — no rule has ever wanted the four to differ — and
it is what an author copies wrong.

**The verdict-parsing bug becomes unrepeatable.** `review-task.sh` uses a greedy
`{.*}` and is right to, because its verdict nests. `judge-skill.sh` uses a narrow
`{[^{}]*}` and is right to, because its verdict is flat. Both spellings are
correct for their rule and catastrophic for the other, and skill-quality shipped
the wrong one: a verdict that had FLAGGED the file permitted it, measured, in
both object orders. Three more judged rules exist, each with its own copy. Under
`type: agent` there is one parser and one place to be wrong.

**Isolation stops being per-author.** `cd /tmp`, `--settings
{"hooks":{},"mcpServers":{},"enabledPlugins":{}}`, the model pin, the timeout
that must stay under the engine's own 30s — all of it is recursion-and-cost
protection that no rule author should be deciding. Two judges in the sibling
project carried 60s and 90s timeouts, so their fail-open branches could never
run: the engine killed them first and read the kill as a refusal.

**`type: cache` is a capability that does not exist today.** The engine
revalidates on the judged file's own fingerprint, which is right for content
rules. `task-review` judges CITED files — those can change while the task's own
bytes do not, and today that is simply not noticed.

**`group_by` answers the corpus question.** A rule about every file of a kind
currently has to be a `TurnEnd` rule that walks the tree itself and, if it needs
per-file evidence, invents a turn-stamp protocol in `sr-session state` —
`tag-the-turn` is 385 lines of GUARDRAIL.md largely about getting that stamp
right, and its own body records two measured bugs in it. `group_by` absent means
one run over everything matched; that is the same capability, declared.

## What it costs, and this is the part that decides it

**The simple rule does not get shorter.** 23 → 26 lines of declaration, and the
script sheds 43 of its 101 code lines — the byte-fetching and the schema call,
not the rule. The 58 lines that ask the citation questions stay exactly as they
are, because they ARE the rule. Someone writing "this file must satisfy a schema"
now writes a step list to say it.

That is a real tax and it should not be argued away. It is the tax on the FREQUENT
case to make the RARE case possible, which is the trade every generic format
makes and the reason to be suspicious of them.

**Two things stop it sliding into a framework**, and both must hold:

1. **The step types are FIXED and few** — `schema`, `bash`, `cache`, `agent`.
   A rule cannot introduce a type. The moment an author can, this is a plugin
   system with no tooling, maintained forever.
2. **No `on:` and no event names.** `PreFileCreate` etc. never appear. The
   engine expands to Pre where it can derive the bytes and Post always, because
   whether Pre is available is a property of how the write was made, not a choice
   an author can make correctly. `group_by` absent already implies one run at
   cycle end; nothing else needs saying.

If a short form for the frequent case is wanted later, it desugars INTO this. But
that is worth doing only after enough rules exist to know which case is actually
frequent — writing the sugar now is guessing.

## What this spike does NOT settle

- **`group_by` over markers.** `matcher: file:` gives named groups from a path
  regex. A marker-based matcher (unit 03) has N markers in one file, and whether
  the subject is the file or the marker is unanswered. It should be decided
  against a real marker rule, not here.
- **Whether the engine should own fail-open policy.** Today each script decides,
  and they have already diverged. Under `type: agent` the engine would decide,
  with an override. That is a behaviour change, not a format change, and it is
  the one that needs an argument of its own.
- **Migration.** Nothing here replaces a rule. Both formats can be read at once —
  a `GUARDRAIL.md` and a `check.yaml` in the same folder — so this can land one
  rule at a time.
