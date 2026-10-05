# rule-tests-rigorous

Refuses a rule whose sr-test cases would pass whatever the rule does.

A case lives in its owning rule's folder, `.sloprail/<nature>/<rule>/tests/<case>/` (nature is `gate`,
`file-guard` or `context`; the structure gate's cases are in `.sloprail/file-guard/structure.tests/<case>/`),
and the rule's cases together are the proof that the rule fires. A suite that only checks that the agent ran,
asserts a refusal without saying why, swallows its own failures, mocks the judge with a fixed answer, or
refuses everything and never permits anything is the sharpest form of the problem sloprail exists to catch, one
layer down: it loads, it goes green, it sits in the project looking like coverage, and it checks nothing.

A file-guard. Its subject is ONE RULE (`subjects:`), not one case: a rule is what a suite proves, and the
refusal may sit in one case with its permitted neighbour in another. The subject is every rule touched by the
change, a changed case of it or a changed file of the rule itself (its declaration, README, scripts,
templates), matched at any depth: the root project's `.sloprail/` and a nested plugin's alike. The owner is the
folder: nothing is inferred from what a case mentions. A rule with no case standing is a subject with nothing to judge, and passes (a rule
without tests is `sr-test doctor`'s business); a case deleted in the range is not a changed file (deleted files are not selected), though the rule's remaining cases
are judged when anything else of the rule changed. Two checks, in order, first refusal ending it:

1. **A script, `check-case.sh`**, the structural floor, still per case: it runs over every case folder of the
   rule, reads each committed `test.sh`, and any case failing it refuses the rule, naming the case:
   - the first line is a shebang;
   - it asserts on the events (`jq -e` over `.events` of the `sr-test agent` result, or over
     `$SR_EVENTS_FILE`), not only on an exit code;
   - one of those `jq -e` assertions is about its owning rule: it selects an event whose `.rule` is the
     owner's name (the bare `<rule>` for a rule in the project, `<plugin>/<rule>` for a plugin's, the plugin
     being the nearest `.claude-plugin/plugin.json` above the `.sloprail`; `structure` or `<plugin>/structure`
     for the structure gate, whose case builds the project that holds `structure.yaml`) and whose `.kind` is its nature's (`GateChecked`, `FileGuardChecked`,
     `ContextActivated`, `StructureChecked`), on the same assertion. Events of other rules do not count;
   - every asserted refusal also checks its reason (`.reason` with `contains`/`test`/...);
   - no `|| true` (nor `|| :`, `|| exit 0`), and no assertion that can never fail (a line that is
     only `true`, `[ 1 ]`, `[ 1 -eq 1 ]`, `jq -e .`);
   - a mock judge script (`judge*.sh`) reads its stdin: a mock that prints one fixed verdict decides
     nothing.
2. **A judge, `judge.md.j2`**, ONE call per rule. `prepare.sh` hands it an index of paths, not pasted file
   contents: the rule's own files and every case folder's files, which the judge opens with its Read tool
   (only the rule's name, nature and event kind are inline). Across all the rule's cases, it fails the rule
   unless:
   - there is a refusal AND a permit, each asserted on the rule's decision, in any of the cases;
   - the refusal is for the rule's actual reason, not an incidental word;
   - recovery is shown where the rule tells the agent what to do instead;
   - a permit sits at the rule's boundary, not far away from it;
   - a mocked judge decides from its input and gives different verdicts for refusal and permit;
   - a scenario a case claims to cover (by its folder name or comments) and the rule's README names is really
     exercised by that case; a README scenario no case claims is not demanded.

   The judge lists EVERY gap in one verdict, one per line, each naming the rule, the criterion, the case(s)
   and the quoted text or the missing scenario, so the agent fixes them all in one pass. The case's rule is the
   folder: events of other rules it may mention are ignored.

The verdict is cached per rule on the rule's own files, all its case folders and the plugin's `plugin.json` (its name is in the event name a
case must assert) (`subjects.sh` fingerprint),
so editing any case or the rule judges that rule once.

Turn it off from the project's `.sloprail/config.yaml`:

    disabled:
      - sloprail/file-guard/rule-tests-rigorous
