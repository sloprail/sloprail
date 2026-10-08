#!/usr/bin/env bash
# The one line cap for a file of a skill in the sloprail plugin, shared by the gate and the
# file-guard of the same name. Sourced, never run.
line_cap_max=200

# line_cap_fine CONTENT — 0 when CONTENT fits; otherwise prints why and returns 1.
line_cap_fine() {
  local n
  n="$(printf '%s\n' "$1" | wc -l | tr -d ' ')"
  [ "$n" -le "$line_cap_max" ] && return 0
  echo "$n lines, over the cap of $line_cap_max. Cut what the reader (an agent writing rules in its own project) does not need, or move a topic into a file of its own and link it from SKILL.md."
  return 1
}
