# rule-tests-pass

Runs the sr-test cases a `.sloprail/` change can break, and requires a case for every rule it touches.

A rule change that breaks a case, or a new rule no case exercises, is the failure this catches at
the commit that makes it, not later. A file-guard, so it judges the committed range
(`sr-checks run --base --head`), and Stop and CI verify the stored verdict.

A case lives in its owning rule's folder: `.sloprail/<nature>/<rule>/tests/<case>/` (nature is `gate`,
`file-guard` or `context`), and the structure gate's in `.sloprail/file-guard/structure.tests/<case>/`.
There is no top-level `.sloprail/tests/`.

A file whose only change is its mode (`chmod +x`, identical bytes) is not a change: it picks no root, no
case and no rule, so a legacy rule made executable is not refused for having no case. A content change to the
same file is.

It fires when any file under a `.sloprail/` changes in the range, the root project's and a nested plugin's alike. One subject per touched RULE, as
`rule-tests-rigorous` does it, so a repo with many rules is many small runs, each cached on its own and well
under the check timeout, not one long run that times out under load. One script check, `check.sh`, no model:

1. **The rule's cases pass.** It runs `sr-test run . --rule <nature>/<rule>` in the rule's `.sloprail`
   root: every case of that rule, and only that rule's. A rule the range did not touch is not run, so a case
   another commit left broken does not block an unrelated change. A case the range deleted is not run.
   A case that does not pass refuses, with its subject, status and the tail of its output.

   A file that is no rule's own (a shared `lib/`, `schemas/`, `config.yaml`) can break any rule's case, so a change to one
   makes every rule of its root a subject, with the shared path among its files. A change to a file of a rule
   makes every other rule that uses it subjects too (one of their files contains `/<rule>`, the rule's folder
   name after a path separator: wide on purpose, so a sourced lib is caught however its path is built). Such a change runs the
   cases but is not an edit of the rule, so a legacy rule with no case is not refused for it.
2. **A changed rule has a case.** For a rule folder `<nature>/<rule>/` (or `structure.yaml`) the range
   adds or edits (including deleting a case) and that still stands at head, `sr-test doctor` must not list
   it as uncovered: at least one case must live in its folder. A rule nobody touched is **not** refused for
   having no case: existing rules without a test pass (legacy pass), until they are changed.

The verdict is cached per rule: `subjects.sh` fingerprints the rule's own files, all its cases and the
plugin's manifest, so a changed rule or case runs its cases again and an unchanged one does not. A refusal
because `sr-test` itself could not do its job (it is missing, a case ran to status `error` or timed out, a
run reported no case, `doctor` failed) carries `"error": true`: it is refused but never cached, so the next
run tries again. A case that fails is a verdict and stays cached.

Turn it off from the project's `.sloprail/config.yaml`:

    disabled:
      - sloprail/file-guard/rule-tests-pass
