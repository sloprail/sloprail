# authoring-guardrails

## Reader

An agent working in someone else's project that has sloprail installed, about to add, fix or turn
off a rule under that project's `.sloprail/`; and a person reading the same pages in the sloprail
documentation to learn how to write rules. Neither has sloprail's source code, nor needs it.

## In scope

- Deciding whether a rule is worth writing, and which nature it is: file-guard, gate, context, or
  the structure gate.
- Where each rule lives and the keys of its YAML.
- The events a rule can bind to and the fields each carries; the `match` expression language.
- Checks: the script contract and its templates, the judge template, `prepare`, `subjects:`,
  `model` and `allowed_tools`, and what a judge can read.
- State a rule keeps across cycles, and reading a context from a gate.
- Grounding: citing the user's words or a tool's output on a change, and requiring a citation.
- Proving a rule fires: `sr-test` cases in the rule's folder, and `sr-checks changeset` / `run` /
  `verify` over a range.
- Turning a rule off, the project's own or a plugin's.

## Out of scope

- How sloprail is built: its source files, tests, issues, pull requests and release history.
- How the engine stores, hashes, caches or schedules work, except the parts that change what a
  rule must declare (a `fingerprint` for what a check reads beyond its files).
- Installing sloprail and setting up CI: the refusals of the plugin's own CI rules carry that.
- The inner workings of the rules the plugin ships (cite-before-commit, verify-before-push, …):
  their refusals say what they need; this skill names them only where a rule author must know
  they exist.
- How a particular harness behaves, beyond what a rule author must write differently for it.
