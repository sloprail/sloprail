# misplaced-declaration

Refuses a `.sloprail/` YAML written where the engine never reads one, and says
where it belongs.

The engine loads declarations from exact places: `.sloprail/file-guard/structure.yaml`,
and `.sloprail/<file-guard|gate|context>/<rule-name>/<nature>.yaml`. A file
anywhere else loads nothing and reports nothing, so its author believes a rule is
in force that is not. In the onboarding eval, fresh agents wrote
`.sloprail/structure.yaml` (one level too high) and `.sloprail/guardrails/...`
(an old layout), and nothing told them.

Preventive, script-only: `check-path.sh` reads the event's path and nothing else.
It refuses a `structure.yaml`, `file-guard.yaml`, `gate.yaml` or `context.yaml`
outside its place, and any YAML straight under `.sloprail/` other than
`config.yaml`. Any other YAML inside a folder (a data file a check reads) is left
alone.

Turn it off from the project's `.sloprail/config.yaml`:

    disabled:
      - sloprail/file-guard/misplaced-declaration
