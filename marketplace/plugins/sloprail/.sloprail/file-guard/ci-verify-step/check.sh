#!/usr/bin/env bash
# ci-verify-step: every changed file carrying `sr:ci verify` must run `sr-checks verify` and
# trigger on pull requests. Contract: stdin is the Changeset
# payload; exit 1 with {"reason": ...} refuses. Fails closed: whatever cannot be judged is refused.
set -uo pipefail
payload="$(cat)"

refuse() {
  jq -n --arg r "$1" '{reason: $r}'
  exit 1
}

n="$(printf '%s' "$payload" | jq -r '.changeset.files | length')" || n=""
case "$n" in '' | *[!0-9]*) refuse "ci-verify-step could not read the changeset on stdin, so the CI files marked 'sr:ci verify' could not be checked" ;; esac

for i in $(seq 0 $((n - 1))); do
  f="$(printf '%s' "$payload" | jq -c --argjson i "$i" '.changeset.files[$i]')" || refuse "ci-verify-step could not read file $i of the changeset"
  status="$(printf '%s' "$f" | jq -r '.status // ""')"
  [ "$status" = D ] && continue
  carries="$(printf '%s' "$f" | jq -r '[(.newMarkers // [])[] | select(.kind == "ci" and .fqn == "verify")] | length')" || refuse "ci-verify-step could not read the markers of file $i of the changeset"
  [ "$carries" = 0 ] && continue
  path="$(printf '%s' "$f" | jq -r '.path')"
  # Comments do not count: a commented-out step or trigger runs nothing.
  body="$(printf '%s' "$f" | jq -r '.newContent // ""' | grep -v -E '^[[:space:]]*(#|//)')"

  how="Fix the CI file as the snippets in the 'sloprail/gate/ci-verify-required' refusal show, and commit it."
  missing=""
  if ! printf '%s\n' "$body" | grep -q -E 'sr-checks[[:space:]]+verify'; then
    missing="a step that runs 'sr-checks verify' (it carries the marker but nothing runs it)"
  fi

  base="${path##*/}"
  case "$path" in
    .github/workflows/*.yml | .github/workflows/*.yaml)
      printf '%s\n' "$body" | grep -q -E '^(on:.*[[:space:],{[]pull_request([[:space:],}:]|\]|$)|[[:space:]]*(-[[:space:]]*)?pull_request[[:space:]]*(:|$))' \
        || missing="${missing:+$missing; }a 'pull_request' trigger under 'on:'"
      ;;
    *.gitlab-ci.yml | *.gitlab-ci.yaml)
      printf '%s\n' "$body" | grep -q -E '^[[:space:]]*(-[[:space:]]*)?if:.*merge_request_event' \
        || missing="${missing:+$missing; }a rule '- if: \$CI_PIPELINE_SOURCE == \"merge_request_event\"'"
      ;;
    *)
      case "$base" in
        azure-pipelines*.yml | azure-pipelines*.yaml)
          printf '%s\n' "$body" | grep -q -E '^pr[[:space:]]*:' \
            || missing="${missing:+$missing; }a top-level 'pr:' trigger (for example 'pr: [main]')"
          ;;
        *) : ;; # any other provider: only the verify step (checked above) can be required
      esac
      ;;
  esac
  [ -z "$missing" ] || refuse "$path carries 'sr:ci verify' but lacks: $missing. $how The job must run 'sr-checks verify' on every pull request."
done
exit 0
