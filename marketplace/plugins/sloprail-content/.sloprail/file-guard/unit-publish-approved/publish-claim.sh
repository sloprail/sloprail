#!/usr/bin/env bash
# Sourced by enters-published.sh and check-publish.sh: ONE answer to "does this
# UNIT.md claim status: published?", so the `when` and the check cannot read the
# same bytes two ways.
#
# publish_claim CONTENT sets:
#   claim         yes | no | undecidable | unsupported
#   claim_status  the status the frontmatter holds ("" when none or undecidable)
#   claim_why     for undecidable or unsupported, why
#
# The status is read by `sr-file field`: a PLAIN YAML reader (no schema, no JSON
# view of the document), with sr-file's own rule for where the frontmatter is.
# Valid YAML a schema would reject — an integer key, a custom tag, `.inf` — still
# answers; only YAML that does not parse, holds two documents, or names status
# (or a merge key) twice cannot. sr-file's exit status decides, never a second
# fence rule written here:
#
#   0  read              → yes when the status is published (trimmed, any case),
#                          else no
#   2  no frontmatter    → no: nothing is claimed. But if a frontmatter opens
#                          once a byte-order mark and leading blank lines are
#                          dropped (sr-file does not look past them), that block
#                          is read: claiming published, or unreadable →
#                          undecidable; otherwise no.
#   3  fence never closed → the text after the opening fence is read the same
#                          way: a mapping claiming published, or text that does
#                          not parse → undecidable (a forgotten close is a
#                          frontmatter to a reader that tolerates one); anything
#                          else — prose under a horizontal rule — no.
#   64, or an sr-file with no `field` command (older than this plugin needs)
#                        → unsupported: nothing here can be read, and the answer
#                          is an upgrade, not "fix the frontmatter".
#   anything else        → undecidable: the frontmatter cannot be read, so it is
#                          never taken as "not published"
#
# A caller treats undecidable as it would yes (the `when` applies the approval,
# the check refuses), and unsupported as a refusal naming the upgrade.

publish_claim_upgrade="sloprail-content needs \`sr-file field\` (a sloprail release newer than 0.2.1); the sr-file on PATH is missing or older — upgrade the sloprail binaries (install.sh, or make distribute-local)."

# claim_of_block TEXT — read TEXT (YAML with no fences) for a status: published
# or unreadable → undecidable, else no. Used on what follows a fence sr-file
# did not read as one.
claim_of_block() {
  local out code
  out="$(printf '%s' "$1" | sr-file field - status --as .yaml 2>&1)"
  code=$?
  if [ "$code" -eq 0 ] && [ "$(publish_claim_norm "$out")" != "published" ]; then
    claim="no"
    return 0
  fi
  claim="undecidable"
  if [ "$code" -eq 0 ]; then
    claim_why="its frontmatter fence is not where a reader looks for one (never closed, or after a byte-order mark or blank lines), and the block under it claims status: published; start the file with --- and close the frontmatter with ---"
  else
    claim_why="$out"
  fi
  return 0
}

publish_claim_norm() {
  printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//'
}

publish_claim() {
  local content="$1" out code rest
  claim="" claim_status="" claim_why=""
  out="$(printf '%s' "$content" | sr-file field - status --as .md 2>&1)"
  code=$?
  case "$code" in
    0)
      claim_status="$out"
      if [ "$(publish_claim_norm "$out")" = "published" ]; then claim="yes"; else claim="no"; fi
      ;;
    2)
      claim="no"
      # Past a byte-order mark and leading whitespace, does a frontmatter open?
      rest="${content#$'\xef\xbb\xbf'}"
      rest="${rest#"${rest%%[![:space:]]*}"}"
      if [ "$rest" != "$content" ]; then
        out="$(printf '%s' "$rest" | sr-file field - status --as .md 2>&1)"
        case $? in
          0)
            # A frontmatter sr-file itself does not see: harmless unless it
            # claims published.
            if [ "$(publish_claim_norm "$out")" = "published" ]; then
              claim="undecidable"
              claim_why="the frontmatter claims status: published but starts after a byte-order mark or blank lines, where it is not read as frontmatter; start the file with the --- fence"
            fi
            ;;
          2) ;;
          3) claim_of_block "${rest#*$'\n'}" ;;
          *) claim="undecidable" claim_why="$out" ;;
        esac
      fi
      ;;
    3)
      # The opening fence's line dropped, the rest read as YAML.
      claim_of_block "${content#*$'\n'}"
      ;;
    *)
      if [ "$code" -eq 64 ] || ! sr-file field --help >/dev/null 2>&1; then
        claim="unsupported" claim_why="$publish_claim_upgrade"
      else
        claim="undecidable" claim_why="$out"
      fi
      ;;
  esac
  return 0
}
