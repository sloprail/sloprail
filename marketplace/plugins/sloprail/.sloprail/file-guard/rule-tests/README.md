# rule-tests

A rule of the project's own does not change without its tests.

When a commit touches `.sloprail/<nature>/<name>/**` (the rule's YAML, a script, a judge
template, a case), this file-guard runs `sr-checks doctor` on that rule, over the rule as committed:

- the declaration loads (the loader's own faults, by name);
- at least one case expects a refusal and one expects a permit (for a context: one case leaves it
  active and one leaves it inactive), and each judge the rule declares is stubbed to pass in one
  case and to fail in another;
- every case passes, the judges answered from the case's canned verdicts. No model runs.

The cases are the author's: `.sloprail/<nature>/<name>/tests/<case>/{case.yaml,setup.sh,trajectory.yaml}`
(see the authoring-guardrails skill, `testing.md`). A change that breaks one is refused with the
doctor's report, which names the rule, the case and what it did instead.

## Why a file-guard, and where it sits

It judges the committed rule, so a half-edited rule is never doctored. It is a script check, and a
run's cheap checks all finish before the first judge starts: a rule change whose tests fail is
refused here, before `grounded-rule-changes` (or any judge) spends a model call on it.

One subject per rule, fingerprinted by the rule's whole folder as committed (git's tree hash): the
doctor also reads the files the change did not touch (an unchanged check a changed case exercises),
and the fingerprint is how a change to them is seen. Deleting a rule's tests is a change to it.

## The rollout

Strict where it costs nothing, gentle where it would only block:

| the rule at the range's base | what is required |
|---|---|
| did not exist (a new rule) | tests: a refuse case, a permit case, judges stubbed both ways, all passing |
| existed with tests | the same: the cases cannot be deleted or let rot to get a change through |
| existed with no tests | grandfathered until its first case: its change passes untested. The moment it has any case, they must pass (and coverage must be complete) |
| removed | nothing to prove (removing a rule is judged by `grounded-rule-changes`) |

So a project that installs the plugin is not blocked on rules it wrote before tests existed, and
no new rule can land untested. `sr-checks doctor` (no arguments) lists what is still untested.
Switch it off with `disabled: [sloprail/file-guard/rule-tests]` in `.sloprail/config.yaml`.

It checks `.sloprail/` at the repository root, the project's own rules. The rules a plugin ships
are tested in the plugin's own repository, with `sr-checks doctor --rules-dir <plugin>/.sloprail`.
