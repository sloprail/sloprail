#!/usr/bin/env bash
# Stage 1 of unit-satisfies-rules: the DETERMINISTIC half, no model. Collects
# every rule that applies to this unit (rules-lib.sh, both project-wide
# .sloprail/content-rules/ and the unit's own topic's constraints/), and for
# every SCRIPT rule among them (one carrying a `script:` key), execs the named
# script with its args, piping the SAME CheckPayload this check received. A
# script rule's own exit code is authoritative — 0 permits it, non-zero refuses
# with whatever reason the script gave, named by the rule folder that bound it.
#
# JUDGE rules (no `script:` key) are NOT run here — that is stage 2
# (check-judge-rules.sh's prepare), reached only once this stage passes, so a
# deterministic failure never pays for a model call.
#
# resultKnown is consulted on BOTH Pre kinds before newContent is read, exactly
# like the sloprail-tasks guards — see rules-lib.sh's own header on why the
# unit's bytes are never read from the event without that check first. An
# underivable Pre result defers to the Post kind, checked at the Stop
# after-check (unit-satisfies-rules is not preventive — see file-guard.yaml).
#
# REFUSAL CONTRACT: exit 0 permits; non-zero refuses with `{"reason": "..."}` on
# stdout. `set -uo pipefail`, never `set -e`.
set -uo pipefail

refuse() {
  jq -n --arg reason "$1" '{reason: $reason}'
  exit 1
}

event="$(cat)"

path="$(printf '%s' "$event" | jq -r '.event.path // empty' 2>/dev/null)"
if [ -z "$path" ]; then
  refuse "unit-satisfies-rules: the event named no path, so there is nothing to check"
fi

root="${SR_WORKSPACE:-.}"
gdir="${SR_GUARDRAIL_DIR:-.}"

lib="$gdir/rules-lib.sh"
if [ ! -f "$lib" ]; then
  refuse "unit-satisfies-rules: rules-lib.sh not found at $lib — the rule set cannot be collected without it"
fi
# shellcheck source=rules-lib.sh
. "$lib"

kind="$(printf '%s' "$event" | jq -r '.event.kind // ""' 2>/dev/null)"
case "$kind" in
  PostFileCreate|PostFileUpdate)
    abs="$root/$path"
    [ -f "$abs" ] || exit 0
    content="$(cat "$abs" 2>/dev/null)" || exit 0
    ;;
  PreFileCreate|PreFileUpdate)
    known="$(printf '%s' "$event" | jq -r '.event.resultKnown // false' 2>/dev/null)"
    if [ "$known" != "true" ]; then
      exit 0
    fi
    content="$(printf '%s' "$event" | jq -r '.event.newContent // ""' 2>/dev/null)"
    ;;
  *)
    exit 0
    ;;
esac

rule_files="$(collect_applicable_rules "$path" "$root" "$content")"
if [ -z "$rule_files" ]; then
  # No rule applies to this unit at all (no global rules configured, no
  # matching channel/type/tag rule, no topic constraints) — nothing to check.
  exit 0
fi

rule_schema="$root/.sloprail/schemas/rule.cue"
scripts_dir_plugin="$gdir/../../../scripts"
scripts_dir_project="$root/.sloprail/content-rules/scripts"

problems=""
while IFS= read -r rf; do
  [ -n "$rf" ] || continue
  rdoc="$(sr-file validate "$rf" --schema "$rule_schema" --emit 2>/dev/null)" || continue
  sname="$(printf '%s' "$rdoc" | jq -r '.script.name // empty' 2>/dev/null)"
  [ -n "$sname" ] || continue   # a judge rule — stage 2's business

  # Resolve the named script: this plugin's scripts/ first, then the project's
  # own .sloprail/content-rules/scripts/ (see scripts/README.md).
  spath="$scripts_dir_plugin/$sname.sh"
  [ -x "$spath" ] || spath="$scripts_dir_project/$sname.sh"
  rule_name="$(basename "$(dirname "$rf")")"
  if [ ! -x "$spath" ]; then
    problems="${problems}  ${rule_name}: script '${sname}' not found (looked under this plugin's scripts/ and the project's .sloprail/content-rules/scripts/)
"
    continue
  fi

  # Args, one per line so a value containing a space survives; readarray/mapfile
  # is not POSIX sh but this project's scripts already assume bash (#!/usr/bin/env
  # bash, matching every sibling script).
  mapfile -t sargs < <(printf '%s' "$rdoc" | jq -r '(.script.args // [])[]' 2>/dev/null)

  level="$(printf '%s' "$rdoc" | jq -r '.level // "must"' 2>/dev/null)"

  if ! out="$(printf '%s' "$event" | "$spath" "${sargs[@]}" 2>&1)"; then
    reason="$(printf '%s' "$out" | jq -r '.reason // empty' 2>/dev/null)"
    [ -n "$reason" ] || reason="$out"
    problems="${problems}  ${rule_name} [${level}]: ${reason}
"
  fi
done <<EOF
$rule_files
EOF

if [ -n "$problems" ]; then
  refuse "SCRIPT RULE VIOLATION: $path fails one or more deterministic writing rules —

$problems
Fix the content to satisfy each named rule."
fi

exit 0
