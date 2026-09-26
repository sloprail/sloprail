sloprail is active in this project. It works rules first: the rules come before the work, and they stay.

Before a change that will happen again (a new endpoint, a migration, a doc page, a component: any shape this project repeats), set the rules up first:
1. Structure. `.sloprail/file-guard/structure.yaml` lists where files may land. If it is missing, or does not cover where this work belongs, write or extend it first. Everything it does not allow is refused.
2. Proof. Add the rule that says what a finished instance must hold (a file-guard on the file, or a gate on the action or the turn's end). Then prove it fires: a bad instance is refused, a good one lands.
3. Build inside them.
Load the `sloprail:authoring-guardrails` skill before writing any rule. The rules stay in the repo, so the next change of that shape is checked too.

A one-off (a single fix, an investigation, a question) gets no new rule. Keep its throwaway files (scratch scripts, notes, downloaded output) in your scratchpad or the system temp dir, not in the repo.
