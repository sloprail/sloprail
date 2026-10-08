#!/usr/bin/env bash
# subjects: one per skill the change touches, id the skill's folder name, files its changed (or
# deleted) files. The judge reads only their diff and the citations, both part of the key.
set -uo pipefail
payload="$(cat)"
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/changeset.sh"
slim_payload
. "${SR_GUARDRAIL_DIR:-.}/../../_lib/subjects.sh"
arr="$(cs_json '
  [.changeset.files[].path | select(test("^marketplace/plugins/sloprail/skills/[^/]+/"))]
  | group_by(split("/")[4])
  | map({id: (.[0] | split("/")[4]), files: .})')"
sub_finish unclaimed "$arr"
