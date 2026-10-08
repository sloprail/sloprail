#!/usr/bin/env bash
# subjects: one per skill the change touches, id the skill's folder name.
#   files        the changed files of that skill
#   fingerprint  what the judge reads beyond them: every file of the skill and its brief
set -uo pipefail
payload="$(cat)"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/changeset.sh"
slim_payload
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/subjects.sh"
arr="$(cs_json '
  [.changeset.files[].path | select(test("^marketplace/plugins/sloprail/skills/[^/]+/"))]
  | group_by(split("/")[4])
  | map((.[0] | split("/")[4]) as $s | {id: $s, files: .,
      deps: ["marketplace/plugins/sloprail/skills/\($s)", ".sloprail/file-guard/plugin-skill-content/briefs/\($s).md"]})')"
sub_finish unclaimed "$arr"
