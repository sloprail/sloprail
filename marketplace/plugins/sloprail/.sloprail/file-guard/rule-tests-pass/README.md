# rule-tests-pass

Runs the sr-test cases whenever anything under a `.sloprail/` changes.

A rule change that breaks a case, or a new rule no case exercises, is the failure this catches at
the commit that makes it, not later. A file-guard, so it judges the committed range
(`sr-checks run --base --head`), and Stop and CI verify the stored verdict.

It fires when any file under `**/.sloprail/**` changes in the range, the root project's and a nested
plugin's alike. One script check, `check.sh`, no model:

1. **Every case passes.** It runs `sr-test run` (in parallel, `--jobs`) over all the cases of each
   `.sloprail` root the change touches, not only the cases of the files that changed: a rule
   edit can break a case nobody touched. A case that does not pass refuses, with the case, its
   status and the tail of its output.
2. **A changed rule is exercised.** For each rule folder `<nature>/<rule>` the range adds or
   edits (and that still stands at head), `sr-test doctor` must show some case's events deciding
   it. A new or edited rule no case fires is refused. A rule nobody touched is **not** refused for
   having no case: existing rules without a test pass (legacy pass), until they are changed.

The verdict is cached on the change plus the rules and the tests: `subjects.sh` fingerprints every
file under the touched `.sloprail/` roots, so a changed rule or case runs the cases again and an
unchanged one does not.

Turn it off from the project's `.sloprail/config.yaml`:

    disabled:
      - sloprail/file-guard/rule-tests-pass
