#!/usr/bin/env bash
# Sourced by enters-published.sh and check-publish.sh: ONE answer to "does this
# UNIT.md claim status: published?", so the `when` and the check cannot read the
# same bytes two ways.
#
# publish_claim CONTENT sets:
#   claim         yes | no | undecidable
#   claim_status  the status the frontmatter holds ("" when none or undecidable)
#   claim_why     for undecidable, why (sr-file's own error)
#
# The status is read by `sr-file field`: a PLAIN YAML reader (no schema, no JSON
# view of the document), with sr-file's own rule for where the frontmatter is.
# Valid YAML a schema would reject — an integer key, a custom tag, `.inf` — still
# answers; only YAML that does not parse, or names status twice, cannot. And
# sr-file's exit status decides, never a second fence rule written here:
#
#   0  read             → yes when the status is published (trimmed, any case),
#                         else no
#   2  no frontmatter   → no: nothing is claimed. Unless the content opens a
#                         fence once a byte-order mark and leading blank lines
#                         are dropped (sr-file itself does not look past them):
#                         then it is undecidable — a reader that does look past
#                         them would find a status there.
#   anything else       → undecidable: the frontmatter cannot be read, so it is
#                         never taken as "not published" (a missing sr-file, too)
#
# A caller treats undecidable as it would yes: the `when` applies the approval,
# the check refuses.
publish_claim() {
  local content="$1" out code rest
  claim="" claim_status="" claim_why=""
  out="$(printf '%s' "$content" | sr-file field - status --as .md 2>&1)"
  code=$?
  case "$code" in
    0)
      claim_status="$out"
      if [ "$(printf '%s' "$out" | tr '[:upper:]' '[:lower:]' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')" = "published" ]; then
        claim="yes"
      else
        claim="no"
      fi
      ;;
    2)
      claim="no"
      # Past a byte-order mark and leading whitespace, does sr-file find a
      # frontmatter? Its own answer again, so the fence rule stays its own.
      rest="${content#$'\xef\xbb\xbf'}"
      rest="${rest#"${rest%%[![:space:]]*}"}"
      if [ "$rest" != "$content" ]; then
        printf '%s' "$rest" | sr-file field - status --as .md >/dev/null 2>&1
        if [ $? -ne 2 ]; then
          claim="undecidable"
          claim_why="the frontmatter fence comes after a byte-order mark or blank lines, where it is not read as frontmatter; start the file with the --- fence"
        fi
      fi
      ;;
    *)
      claim="undecidable" claim_why="$out"
      ;;
  esac
  return 0
}
