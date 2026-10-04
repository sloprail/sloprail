# rule-tests-rigorous

Refuses an sr-test case that would pass whatever its rule does.

A case under `.sloprail/tests/<case>/` is the proof a guardrail fires. A case that only checks
that the agent ran, asserts a refusal without saying why, swallows its own failures, or mocks the
judge with a fixed answer is the sharpest form of the problem sloprail exists to catch, one layer
down: it loads, it goes green, it sits in the project looking like coverage, and it checks nothing.

A file-guard. Its subject is one case folder (`subjects:`), matched at any depth: the root
project's `.sloprail/tests/<case>/` and a nested plugin's alike. Two checks, in order, first
refusal ending it:

1. **A script, `check-case.sh`** — the structural floor, read from the committed `test.sh`:
   - the first line is a shebang;
   - it asserts on the events (`jq -e` over `.events` of the `sr-test agent` result, or over
     `$SR_EVENTS_FILE`), not only on an exit code;
   - every asserted refusal also checks its reason (`.reason` with `contains`/`test`/...);
   - no `|| true` (nor `|| :`, `|| exit 0`), and no assertion that can never fail (a line that is
     only `true`, `[ 1 ]`, `[ 1 -eq 1 ]`, `jq -e .`);
   - a mock judge script (`judge*.sh`) reads its stdin: a mock that prints one fixed verdict decides
     nothing.
2. **A judge, `judge.md.j2`** — it reads the whole case and the rules it covers (every rule folder
   of the same `.sloprail` whose name the case mentions, in its own nature: a gate is covered by a case that
   asserts `GateChecked` events, a file-guard by `FileGuardChecked`, a context by `ContextActivated`, since a
   gate and a file-guard can share a name), and fails the case unless, per rule:
   - there is a refusal AND a permit, each asserted on the rule's decision;
   - the refusal is for the rule's actual reason, not an incidental word;
   - recovery is shown where the rule tells the agent what to do instead;
   - the permit sits at the rule's boundary, not far away from it;
   - a mocked judge decides from its input and gives different verdicts for refusal and permit;
   - the scenarios the rule's README names, and the case claims, are really exercised.

   The judge cites evidence per criterion, so its refusal names the case, the rule, the criterion
   and the text.

The verdict is cached per case on the case's files plus the covered rules' files (`subjects.sh`
fingerprint), so editing a covered rule judges its cases again.

Turn it off from the project's `.sloprail/config.yaml`:

    disabled:
      - sloprail/file-guard/rule-tests-rigorous
