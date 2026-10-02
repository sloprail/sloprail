# grounded-rule-changes

Refuses a change to what already stands in the project's `.sloprail/` unless it is
grounded in the user's words or in a tool output showing the rule misfiring.

In the evals, agents stuck behind a refusal edited the rule's script, wrote
`disabled:` entries into `.sloprail/config.yaml`, and ran `rm -rf .sloprail/context/...`.
A rule its own subject can switch off is a suggestion. This one makes the cheap way
out cost a citation that has to be true.

## What it does

A commit-based file-guard over `.sloprail/**` (`config.yaml` included), with
`deletions: include`. Three steps, cheapest first:

1. `require: citation` (user or tool_result). A change citing nothing is refused by the
   engine before any check runs.
2. `own-refusal-is-no-ground.sh`. If every citation is a tool output carrying this
   rule's name (its own refusal, quoted back) or is not the user's words, refuse.
3. A judge (`judge.md.j2`). Does the cited quote ask for, or justify, THIS change? A user
   message asking for rule work counts. A tool output showing the rule refusing correct
   work counts. The rule's own refusal of the agent's bad work does not, nor does a generic
   "please continue", nor a misfire of a different rule.

The refusal says what to do: ask the user, or cite the tool output that shows the
misfire, and never disable, loosen or delete a rule to get unstuck.

## What it leaves alone

A file the agent **added in this session** needs no grounding (`needs-grounding.sh`; it did
not exist at `$SR_SESSION_START`, so fixing a rule written earlier in the session is free
too): setting rules up first, a new rule, a new structure, weakens nothing, and the plugin's rules-first hook asks
for exactly that. A rule-authoring session, the onboarding flow and a user who asked for
rules all pass. The judge is skipped for such a range. `.sloprail/config.yaml` is the
exception: a new one can carry `disabled:`.

A file **restored to a version the default branch had** (byte-identical to its content at an
earlier commit on origin/HEAD, else main/master) needs no grounding when every commit on the
default branch that changed it since carried no `Sloprail-Cites-*` trailer: only uncited changes
are being undone (`is_landed_revert` in `needs-grounding-lib.sh`). Undoing a cited change, or
writing content the default branch never had, still needs the user's words.

A rename is judged by the path it came from: moving a rule out of `.sloprail/` removes it.

## It cannot disable itself

The engine reads `disabled:` from the working-tree `config.yaml`, which any writer no gate
models (a script, `yq -i`, `git apply`) can edit. So a `disabled:` entry naming
`sloprail/*/grounded-rule-changes` is honoured only when the config committed when the
session began lists it (`internal/declaration` `trustProtected`): neither the working tree
nor the agent's own commits switch it off. To turn it off, commit the entry first, in an
earlier session.

A project's own `.sloprail/<nature>/grounded-rule-changes` never takes this rule's place: the
plugin's claims its name first and the project's is reported as shadowed.

## Out of scope

- `.claude/settings.json` (`enabledPlugins` set to false for sloprail) turns off the whole
  plugin, hooks and every rule, not this rule. That is Claude Code's own permission
  surface, and covering it here would make every settings change (a permission, a model)
  need a citation. It takes effect from the next session, not the running one.
- `stop_hook_block_cap` in `config.yaml` lowers how often a refused Stop is replayed, for
  every rule. Writes to `config.yaml` are gated and judged like any other change to it; a
  writer no gate models is only seen at Stop.

## The gate

A file-guard judges commits at Stop, against the config the commit itself wrote, so a
change that adds this rule to `disabled:` would switch off the one rule that could refuse
it. `gate/grounded-rule-changes` therefore refuses a write or a delete of
`.sloprail/config.yaml` before it lands, unless it cites. Cite it with `sr-file`:

    sr-file write .sloprail/config.yaml --cite:user 'turn the demo rule off' <<'BODY'
    ...
    BODY

There is no gate on the rest of `.sloprail/`: the Write tool carries no citation, so a gate
there would refuse every rule an author writes and route each through `sr-file`. A write
no gate models (a script, say) still reaches the file-guard.

Working on sloprail's own repo, which edits `.sloprail/` often: amend the commit with
`git commit --amend --no-edit --trailer 'Sloprail-Cites-User: <exact quote>'`.

Turn it off from the project's `.sloprail/config.yaml`, committed before the session starts
(a change that itself needs the user's request):

    disabled:
      - sloprail/file-guard/grounded-rule-changes
      - sloprail/gate/grounded-rule-changes
