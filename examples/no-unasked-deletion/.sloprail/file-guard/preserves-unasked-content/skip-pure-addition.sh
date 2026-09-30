#!/usr/bin/env bash
# prepare (the file-guard's copy, over the committed Changeset): decide whether the
# judge is asked at all. What it rules on — the change's unified diff and the cited
# words — needs no preparing: the template reads `change` and `changeset.citations`
# straight off its input.
#
# SKIPS THE JUDGE on a PURE ADDITION — REQUIRED, not cosmetic. When
# removes-content.sh waives the citation for an append, this judge would still
# run and judge a diff with nothing removed against no citation (correctly none:
# nothing needed authorizing), and refuse a healthy append. Measured against a
# real Haiku run (eval/add-section). `{"skip": true}` abstains instead, and no
# model call is spent on a change that removes nothing.
set -uo pipefail

input="$(cat)"

empty() {
  jq -n '{additionalContext: {}}'
  exit 0
}

# Undecidable inputs are never a skip: ask the judge.
[ "$(printf '%s' "$input" | jq -r '.event.kind // empty')" = "Changeset" ] || empty
n="$(printf '%s' "$input" | jq -r '.changeset.files | length')" || empty
case "$n" in '' | *[!0-9]*) empty ;; esac

# Same removed-lines test removes-content.sh ran — recomputed rather than passed
# through, since a prepare step's only input is this same payload.
removed=0
i=0
while [ "$i" -lt "$n" ]; do
  status="$(printf '%s' "$input" | jq -r --argjson i "$i" '.changeset.files[$i].status')" || empty
  idx="$i"
  i=$((i + 1))
  # A deletion is a removal; an added file removes nothing.
  case "$status" in
    D) empty ;;
    A) continue ;;
  esac
  old="$(printf '%s' "$input" | jq -r --argjson i "$idx" '.changeset.files[$i].oldContent // ""')" || empty
  new="$(printf '%s' "$input" | jq -r --argjson i "$idx" '.changeset.files[$i].newContent // ""')" || empty
  count="$(comm -23 <(printf '%s' "$old" | sort -u) <(printf '%s' "$new" | sort -u) | grep -c . || true)"
  removed=$((removed + ${count:-0}))
done

if [ "$removed" -eq 0 ]; then
  printf '{"skip": true}\n'
  exit 0
fi

jq -n '{additionalContext: {}}'
