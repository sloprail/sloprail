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

It fires when any file under `**/.sloprail/**` changes in the range, the root project's and a nested
plugin's alike. One subject per touched `.sloprail` root; one script check, `check.sh`, no model:

1. **The cases it can break pass.** The scope depends on what the range touched in that root:
   - only files of cases (`<nature>/<rule>/tests/<case>/...`, `structure.tests/<case>/...`): only those
     cases run, by exact `owner:case` subject. A case another commit left broken and this range did not
     touch is not run, so it does not block an unrelated test edit. A case the range deleted is skipped;
   - any other file of the root (a rule's declaration, README or script, the config, `structure.yaml`):
     ALL the root's cases run, since a rule edit can break a case nobody touched.

   A case that does not pass refuses, with its subject, status and the tail of its output.
2. **A changed rule has a case.** For each rule folder `<nature>/<rule>/` (and `structure.yaml`) the range
   adds or edits (including deleting a case) and that still stands at head, `sr-test doctor` must not list
   it as uncovered: at least one case must live in its folder. A rule nobody touched is **not** refused for
   having no case: existing rules without a test pass (legacy pass), until they are changed.

The verdict is cached per root on the change plus the rules and the tests: `subjects.sh` fingerprints every
file under the touched `.sloprail/`, so a changed rule or case runs the cases again and an unchanged one
does not.

Turn it off from the project's `.sloprail/config.yaml`:

    disabled:
      - sloprail/file-guard/rule-tests-pass
