#!/usr/bin/env bash
# A gh call is about to run (gate.yaml matches every one). Permit it when every
# gh invocation on the line is a KNOWN read of something already found, or when
# scanner-declared has a scanner this session still owes a search; otherwise
# refuse, saying what to declare.
#
# The list is of what is allowed, not of what is a search. A list of search
# spellings is always one spelling short — measured: `gh issue list --search`,
# `-S`, `gh api graphql` with whitespace before `search (`, a query read from a
# file (`-F query=@q.graphql`), a gh alias, and `X=search; gh $X …` (the parser
# drops the word it cannot resolve, leaving `gh issues …`) all ran with no
# scanner. So, until one is declared, a gh call runs only if it is:
#   - `gh` alone, `gh --version`/`version`, `gh help`/`--help`, `gh auth status`
#   - `gh issue|pr|repo|release|gist view …`
#   - `gh issue|pr|repo|release|gist list …` with no --search / -S
#   - `gh api <endpoint>` whose endpoint is found and is neither graphql nor a
#     search/… path
# and every other gh call — `gh search …`, graphql, an alias, an extension, an
# endpoint the parser could not see — counts as a search.
#
# Fails closed everywhere: a registry that cannot be read, or does not parse,
# refuses (it cannot say a scanner exists), and so does a payload whose
# invocations cannot be read.
set -uo pipefail

payload="$(cat)"

refuse_plumbing() {
  jq -n --arg why "$1" '{reason: (
    "This gh call could not be checked against a declared scanner: " + $why + ". "
    + "It was refused rather than let through unchecked. If it keeps happening the sloprail install is broken — say so rather than working around it.")}'
  exit 1
}

# Is every gh invocation on this line a known read? (jq exits 1 when not.)
if printf '%s' "$payload" | jq -e '
  def endpoint:
    reduce .[] as $x ({skip: false, ep: null};
      if .ep != null then .
      elif .skip then .skip = false
      elif ($x | IN("-X","--method","-H","--header","-f","--raw-field","-F","--field","-q","--jq","-t","--template","--input","--hostname","--cache","-p","--preview")) then .skip = true
      elif ($x | startswith("-")) then .
      else .ep = $x end)
    | .ep;
  def no_search_flag: all(.[]; (test("^--search(=|$)") or test("^-[A-Za-z]*S")) | not);
  def known_read:
    (.argv // []) as $a
    | if ($a | length) < 2 then true
      else $a[1] as $sub
      | if ($sub | IN("--version","version","help","--help","-h")) then true
        elif $sub == "auth" then ($a[2] // "") == "status"
        elif ($sub | IN("issue","pr","repo","release","gist")) then
          ($a[2] // "") as $verb
          | if $verb == "view" then true
            elif $verb == "list" then ($a[3:] | no_search_flag)
            else false end
        elif $sub == "api" then
          ($a[2:] | endpoint) as $ep
          | $ep != null
            and ($ep | test("(?i)graphql") | not)
            and ($ep | test("(?i)^(https?://[^/]+)?/*search([/?#]|$)") | not)
        else false end
      end;
  [.event.invocations[]? | select(.bin == "gh")]
  | length > 0 and all(.[]; known_read)
' >/dev/null 2>&1; then
  exit 0
fi

# shellcheck source=../../context/scanner-declared/scanner-lib.sh
. "${SR_GUARDRAIL_DIR:-.}/../../context/scanner-declared/scanner-lib.sh" 2>/dev/null \
  || refuse_plumbing "the shared scanner-lib.sh beside scanner-declared could not be loaded"

owed="$(registry_owed)" || refuse_plumbing "scanner-declared's registry could not be read (sr-session state list failed or returned something that is not its JSON lines)"
declared="$(printf '%s' "$owed" | jq 'length' 2>/dev/null)"
case "$declared" in
  '' | *[!0-9]*) refuse_plumbing "scanner-declared's registry did not parse" ;;
esac

if [ "$declared" -gt 0 ]; then
  exit 0
fi

# Near misses: a file the agent evidently meant as a scanner, at a path no
# scanner is read from. Measured on a real unprimed run: the agent wrote
# `.sloprail/scanners/auth-token-logs.yaml`, was refused with the generic text
# three times, rewrote the same wrong file, and gave up on searching. Naming the
# file and the exact path it must be at is what the generic text lacked.
near_misses=""
unregistered=""
if [ -n "${SR_WORKSPACE:-}" ] && [ -d "$SR_WORKSPACE" ]; then
  while IFS= read -r p; do
    [ -n "$p" ] || continue
    rel="${p#"$SR_WORKSPACE"/}"
    if printf '%s' "$rel" | grep -qE '(^|/)scanners/[^/]+/scanner\.yaml$'; then
      unregistered="$unregistered $rel"
    else
      near_misses="$near_misses $rel"
    fi
  done <<EOF
$(find "$SR_WORKSPACE" -maxdepth 6 -path "$SR_WORKSPACE/.git" -prune -o -type f \( -iname '*scanner*.y*ml' -o -ipath '*scanners/*.y*ml' \) -print 2>/dev/null | head -20)
EOF
fi

hint=""
if [ -n "$near_misses" ]; then
  hint="$hint Not a scanner:${near_misses} — a scanner is read ONLY from a file named exactly scanner.yaml inside its own folder under scanners/, e.g. scanners/token-leaks/scanner.yaml (not a <name>.yaml, and not under .sloprail/). Write it there."
fi
if [ -n "$unregistered" ]; then
  hint="$hint${unregistered} is in the right place but was not registered in this session (it predates the session, was written by a shell command, or by another agent): write it again with the Write tool — unchanged is fine."
fi

jq -n --arg hint "$hint" '{reason: (
  "No scanner is declared in this session, and GitHub searches in this project run against one."
  + $hint + " "
  + "Declare one with the Write tool at scanners/<short-name>/scanner.yaml — for example scanners/token-leaks/scanner.yaml — containing:\n"
  + "  active: true\n  keywords:\n    - <keyword>\n    - <keyword>\n"
  + "naming every keyword this research must cover, then run ONE gh search whose query contains all of them "
  + "(e.g. gh search issues \"<keyword> <keyword>\"); narrower searches besides it are fine. "
  + "Until a scanner is declared, the only gh calls that run are reads of a result already found — gh issue view, gh pr view, gh repo view, gh issue list / gh pr list without --search, gh api repos/... — "
  + "and any other gh call (a search, gh api graphql, an alias or extension) counts as a search."
)}'
exit 1
