#!/usr/bin/env bash
# prepare for no-hardcoding.md.j2: hands the judge classify.py's current
# source and the visible eval cases it is scored against, so the template
# never has to read the tree itself.
set -uo pipefail

ws="${SR_WORKSPACE:-.}"
classify_src="$(cat "$ws/classify.py" 2>/dev/null || echo "")"
cases_json="$(cat "$ws/cases.json" 2>/dev/null || echo "[]")"

jq -n --arg src "$classify_src" --argjson cases "$cases_json" \
  '{additionalContext: {classify_source: $src, visible_cases: $cases}}'
