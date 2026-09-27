#!/usr/bin/env bash
# Sourced by enters-published.sh and check-publish.sh: ONE answer to "does this
# UNIT.md claim status: published?", so the `when` and the check cannot read the
# same bytes two ways.
#
# publish_claim CONTENT sets:
#   claim         yes | no | undecidable
#   claim_status  the status the frontmatter parses to ("" when none or undecidable)
#   claim_why     for undecidable, why (sr-file's own parse error)
#
# The frontmatter is read AS WRITTEN — parsed by `sr-file validate --emit` against
# no schema (/dev/null), never against unit.cue: a document that breaks the
# schema somewhere else (`type: article`) still claims its status. sr-file runs
# once per call.
#
#   parses                           → yes when its status is published (trimmed,
#                                      any case), else no
#   opens a --- fence, does not parse → undecidable: no reader can say what
#                                      status it holds, so it is never read as
#                                      "not published". The fence is found the way
#                                      sr-file's isFence finds it — the first line
#                                      is `---` once whitespace is trimmed — and
#                                      also past a byte-order mark and leading
#                                      blank lines, which sr-file itself refuses.
#   no fence at all                  → no: a document without frontmatter has no
#                                      status.
#
# A caller treats undecidable as it would yes: the `when` applies the approval,
# the check refuses.
publish_claim() {
  local content="$1" doc err rest first
  claim="" claim_status="" claim_why=""
  if doc="$(printf '%s' "$content" | sr-file validate - --as .md --schema /dev/null --emit 2>&1)"; then
    if ! claim_status="$(printf '%s' "$doc" | jq -r 'if type == "object" then (.status // "" | tostring) else "" end' 2>/dev/null)"; then
      claim="undecidable" claim_why="sr-file emitted a document jq could not read"
      return 0
    fi
    if [ "$(printf '%s' "$claim_status" | tr '[:upper:]' '[:lower:]' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')" = "published" ]; then
      claim="yes"
    else
      claim="no"
    fi
    return 0
  fi
  err="$doc"
  # Does it open a frontmatter fence? Drop a byte-order mark and any leading
  # whitespace (blank lines included), then compare the first line, trimmed.
  rest="${content#$'\xef\xbb\xbf'}"
  rest="${rest#"${rest%%[![:space:]]*}"}"
  first="${rest%%$'\n'*}"
  first="${first%"${first##*[![:space:]]}"}"
  if [ "$first" = "---" ]; then
    claim="undecidable" claim_why="$err"
  else
    claim="no"
  fi
  return 0
}
