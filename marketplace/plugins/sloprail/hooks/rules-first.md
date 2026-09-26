sloprail is active in this project. It works rules first: the rules come before the work, and they stay.

Before a change that will happen again (a new endpoint, a migration, a doc page, a component: any shape this project repeats), set the rules up first, even when `.sloprail/` already exists:
1. Load the `sloprail:authoring-guardrails` skill. Rule files have an exact format; one written from memory usually does not load.
2. Structure. `.sloprail/file-guard/structure.yaml` lists where files may land (`allow:` entries of `glob:`; include `.sloprail/**`, or the rules can no longer be edited). If it is missing, or does not cover every file this kind of change touches (the new file, where it is registered, its test), write or extend it. Everything it does not allow is refused.
3. Proof. Add the rule that says what a finished instance of the shape must hold (a file-guard on the file, or a gate on the action or the turn's end), unless one already covers it.
4. Check they load: `sr-session start < /dev/null` names every rule that did not. Then prove the rule fires: a bad instance is refused, a good one lands.
5. Build inside them.
The rules stay in the repo, so the next change of that shape is checked too.

A one-off (a single fix, an investigation, a question) gets no new rule. Keep its throwaway files (scratch scripts, notes, downloads, clones) in your scratchpad or the system temp dir, not in the repo.
