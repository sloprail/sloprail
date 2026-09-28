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
#                          way, WHOLE first: if it parses, a mapping claiming
#                          published → undecidable (a reader that tolerates a
#                          missing close would publish it; blank lines inside a
#                          YAML block do not end it), anything else → no. Only
#                          when the whole does not parse is its FIRST PARAGRAPH
#                          (blank lines skipped, up to the next blank line) read
#                          — where a forgotten frontmatter would be: a mapping
#                          decides by its status; text that does not parse is
#                          undecidable only when a line of it names a status key
#                          (or a quoted key with an escape, which could spell
#                          one); otherwise it is prose under a horizontal rule
#                          → no.
#   64, or an sr-file with no `field` command (older than this plugin needs)
#                        → unsupported: nothing here can be read, and the answer
#                          is an upgrade, not "fix the frontmatter".
#   anything else        → undecidable: the frontmatter cannot be read, so it is
#                          never taken as "not published"
#
# A caller treats undecidable as it would yes (the `when` applies the approval,
# the check refuses), and unsupported as a refusal naming the upgrade.

# The minimum is what this code needs, not this plugin's own version: the
# marketplace serves the plugin from the default branch while users run the
# latest RELEASED binaries, so the plugin can be ahead of every release. `sr-file
# field` first ships in the release after 0.2.1, so that is the minimum — a
# constant that stays true, never bumped.
publish_claim_field_after="0.2.1"

# publish_claim_upgrade_message — the refusal for a missing `sr-file field`,
# naming the sr-file actually installed (asked only when needed).
publish_claim_upgrade_message() {
  local installed
  installed="$(sr-file --version 2>/dev/null | awk '{ print $NF; exit }')"
  printf '%s' "sloprail-content needs \`sr-file field\`, which is in sloprail releases newer than ${publish_claim_field_after}; the installed sr-file is ${installed:-missing (not on PATH)} — upgrade the sloprail binaries to a newer release (install.sh, or make distribute-local)."
}

# claim_of_unclosed TEXT — TEXT is what follows an opening fence never closed
# (or one sr-file did not see): the whole of it, then its first paragraph, as
# the header says.
claim_of_unclosed() {
  local para out code
  out="$(printf '%s' "$1" | sr-file field - status --as .yaml 2>&1)"
  code=$?
  if [ "$code" -ne 0 ]; then
    para="$(printf '%s' "$1" | tr -d '\r' | awk '/^[[:space:]]*$/ { if (started) exit; next } { started = 1; print }')"
    out="$(printf '%s\n' "$para" | sr-file field - status --as .yaml 2>&1)"
    code=$?
  else
    para=""
  fi
  claim="no"
  if [ "$code" -eq 0 ]; then
    if [ "$(publish_claim_norm "$out")" = "published" ]; then
      claim="undecidable"
      claim_why="its frontmatter fence is not where a reader looks for one (never closed, or after a byte-order mark or blank lines), and the block under it claims status: published; start the file with --- and close the frontmatter with ---"
    fi
    return 0
  fi
  # Does not parse: only a line naming a status key (bare, quoted, or behind
  # `?`), or a quoted key carrying an escape that could spell one, makes it a
  # possible frontmatter; anything else is prose.
  if printf '%s\n' "$para" | grep -Eiq -e '^[[:space:]]*(\?[[:space:]]*)?["'"'"']?stat' \
       -e '^[[:space:]]*(\?[[:space:]]*)?["'"'"'][^"'"'"']*\\'; then
    claim="undecidable" claim_why="$out"
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
          3) claim_of_unclosed "${rest#*$'\n'}" ;;
          *) claim="undecidable" claim_why="$out" ;;
        esac
      fi
      ;;
    3)
      # The opening fence's line dropped, the rest read as YAML.
      claim_of_unclosed "${content#*$'\n'}"
      ;;
    *)
      if [ "$code" -eq 64 ] || ! sr-file field --help >/dev/null 2>&1; then
        claim="unsupported" claim_why="$(publish_claim_upgrade_message)"
      else
        claim="undecidable" claim_why="$out"
      fi
      ;;
  esac
  return 0
}

# LOADED SENTINEL — keep this the LAST line. bash runs a sourced file up to its
# first syntax error, so a helper can load partly: publish_claim defined, a
# function it calls missing. A caller unsets this, sources, and checks it,
# which proves the whole file ran.
publish_claim_loaded=1
