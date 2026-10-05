# rule-tests-rigorous

Refuses an sr-test case that would pass whatever its rule does.

A case lives in its owning rule's folder, `.sloprail/<nature>/<rule>/tests/<case>/` (nature is `gate`,
`file-guard` or `context`; the structure gate's cases are in `.sloprail/file-guard/structure.tests/<case>/`),
and it is the proof that one rule fires. A case that only checks that the agent ran, asserts a refusal
without saying why, swallows its own failures, or mocks the judge with a fixed answer is the sharpest form of
the problem sloprail exists to catch, one layer down: it loads, it goes green, it sits in the project looking
like coverage, and it checks nothing.

A file-guard. Its subject is one case folder (`subjects:`), matched at any depth: the root project's
`.sloprail/` and a nested plugin's alike. The owner is the folder: nothing is inferred from what the case
mentions. A case deleted in the range is not judged (deleted files are not selected). Two checks, in order, first refusal ending it:

1. **A script, `check-case.sh`**, the structural floor, read from the committed `test.sh`:
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
2. **A judge, `judge.md.j2`**: it reads the whole case and the owning rule's files (its declaration, README,
   scripts and templates; not its other cases), and fails the case unless, for that rule:
   - there is a refusal AND a permit, each asserted on the rule's decision;
   - the refusal is for the rule's actual reason, not an incidental word;
   - recovery is shown where the rule tells the agent what to do instead;
   - the permit sits at the rule's boundary, not far away from it;
   - a mocked judge decides from its input and gives different verdicts for refusal and permit;
   - the scenarios the rule's README names, and the case claims, are really exercised.

   The case's rule is the folder: events of other rules it may mention are ignored. The judge cites evidence
   per criterion, so its refusal names the case, the rule, the criterion and the text.

The verdict is cached per case on the case's files plus the owning rule's files (`subjects.sh`
fingerprint), so editing the rule judges its cases again.

Turn it off from the project's `.sloprail/config.yaml`:

    disabled:
      - sloprail/file-guard/rule-tests-rigorous
