# spec

What sloprail promises, whatever harness runs it. One folder per DOMAIN; the path is the
natural key (the a10n-project layout):

- `<domain>/entities/<Entity>.yaml` — one entity: `{doc, fields}`. A field has `name`, `type`
  and optionally `doc`, `constraint_expr`, `enum`. Rules: `.claude/skills/document-entity`.
- `<domain>/invariants/<id>.yaml` — one invariant: `{predicate, why?, needs?}`, with typed
  `{@fld:<domain>:<Entity>.<field>}` / `{@ent:<domain>:<Entity>}` mentions in the predicate.
  Rules: `.claude/skills/document-invariant`.
- `needs` lists the harness capabilities an invariant relies on, by id in harness-mocks
  `spec/capabilities/`. No `needs`: it holds on any harness, or with none.

Writing either is refused until its skill is loaded (`.sloprail/gate/spec-*-skill`), and every
change is judged against it in buckets of at most 25 (`.sloprail/file-guard/spec-quality`).
Tests are linked from stage S3: `// sr:proves <domain>/<id>`, `// sr:invariant <domain>/<id>`.

## Domains

`loading` `events` `matching` `checks` `judges` `gates` `structure` `contexts` `fileguard`
`cache` `citations` `session` `subagents` `cli` `install` `authoring-tools`

## Baselines

The code each domain was last read from. When sloprail moves past a baseline,
`git diff <baseline>..main -- <the domain's code>` is what to re-read.

| domains | sloprail | harness-mocks registry |
|---|---|---|
| all (first draft) | `1cfd34b` (2026-10-07) | `54784f0` |
