#!/usr/bin/env bash
# A command line that runs gh is about to run (gate.yaml matches every gh
# invocation, and every line that names gh at all). Permit it when none of its
# gh calls searches GitHub, or when scanner-declared has a scanner this session
# still owes a search; otherwise refuse, saying what to declare.
#
# What counts as a SEARCH — the entry points GitHub's search is reached by, and
# anything this script cannot see into:
#   - `gh search …` (but not `gh search … --help`);
#   - `gh api <endpoint>` whose endpoint is a search/… path or graphql, once
#     resolved: host and query stripped, percent-decoded, `.`/`..` collapsed —
#     `gh api 'repos/../search/issues?q=token'` searched live — and any endpoint
#     holding `..` at all, or none the parser can see (`gh api $E`);
#   - `gh issue|pr|label list … --search/-S` (a filter, run by GitHub search);
#   - a first word that is not a gh built-in: an alias or an extension, which
#     can do anything (gh does not let an alias shadow a built-in);
#   - a line that names gh more often than the parser found gh invocations:
#     `eval "gh search …"`, `python3 -c "os.system('gh search …')"` — a gh call
#     the parser cannot see is treated as a search.
# Everything else — gh pr create, gh repo clone, gh pr diff/checks, gh run list,
# gh issue -R o/r view 1, gh status, gh browse, gh auth status — is not a
# search and runs with or without a scanner. (A first version gated everything
# except a short list of reads, and refused those ordinary calls as searches.)
#
# Fails closed everywhere: a registry that cannot be read, or does not parse,
# refuses a search (it cannot say a scanner exists).
set -uo pipefail

payload="$(cat)"

refuse_plumbing() {
  jq -n --arg why "$1" '{reason: (
    "This gh search could not be checked against a declared scanner: " + $why + ". "
    + "It was refused rather than let through unchecked. If it keeps happening the sloprail install is broken — say so rather than working around it.")}'
  exit 1
}

# The searching gh calls on this line, one description per line; nothing when
# there are none. A payload that does not parse is one search (fail closed).
searches="$(printf '%s' "$payload" | jq -r '
  def builtins: ["accessibility","agent-task","alias","api","attestation","auth","browse","cache","co","codespace","completion","config","extension","ext","gist","gpg-key","help","issue","label","licenses","org","pr","preview","project","release","repo","ruleset","run","search","secret","ssh-key","status","variable","version","workflow"];
  def hexval: ascii_downcase | explode | map(if . >= 97 then . - 87 else . - 48 end) | .[0] * 16 + .[1];
  def pdecode: [splits("%")] as $p
    | $p[0] + ([$p[1:][] | if test("^[0-9A-Fa-f]{2}") then ([.[0:2] | hexval] | implode) + .[2:] else "%" + . end] | join(""));
  # The path an endpoint names on GitHub: scheme and host gone, decoded (a few
  # rounds, for double encoding), query and fragment gone, dot segments resolved.
  def endpoint_path:
    sub("^[A-Za-z][A-Za-z0-9+.-]*://[^/]*"; "")
    | reduce range(3) as $_ (.; pdecode)
    | sub("[?#].*$"; "")
    | [splits("[/\\\\]+")]
    | reduce .[] as $seg ([]; if $seg == "" or $seg == "." then . elif $seg == ".." then .[:-1] else . + [$seg] end)
    | "/" + join("/");
  def endpoint:
    reduce .[] as $x ({skip: false, ep: null};
      if .ep != null then .
      elif .skip then .skip = false
      elif ($x | IN("-X","--method","-H","--header","-f","--raw-field","-F","--field","-q","--jq","-t","--template","--input","--hostname","--cache","-p","--preview")) then .skip = true
      elif ($x | startswith("-")) then .
      else .ep = $x end)
    | .ep;
  def search_endpoint:
    if . == null then true
    else (reduce range(3) as $_ (.; pdecode)) as $d
      | ($d | test("\\.\\.")) or ((endpoint_path) | test("(?i)^/(api/v3/)?search(/|$)|graphql"))
    end;
  def search_flag: any(.[]; test("^--search(=|$)") or test("^-[A-Za-z]*S"));
  def searches:
    (.argv // []) as $a
    | if ($a | length) < 2 then false
      else $a[1] as $sub
      | if ($sub | IN("--version","--help","-h")) then false
        elif ($sub | startswith("-")) then true
        elif $sub == "search" then ($a[2:] | any(.[]; . == "--help" or . == "-h") | not)
        elif $sub == "api" then ($a[2:] | endpoint | search_endpoint)
        elif ($sub | IN("issue","pr","label")) then ($a[2:] | search_flag)
        elif ($sub | IN(builtins[])) then false
        else true end
      end;
  ([.event.invocations[]? | select(.bin == "gh")]) as $gh
  | ([(.event.raw // "") | match("(^|[^A-Za-z0-9_./-])gh(?=[^A-Za-z0-9_.-]|$)"; "g")] | length) as $named
  | ($gh[] | select(searches) | (.argv | join(" "))),
    (if $named > ($gh | length) then "a gh call this rule cannot see into (eval, another language, a script): " + (.event.raw // "" | .[0:120]) else empty end)
' 2>/dev/null)" || searches="an unreadable command line"

# No search on this line: nothing to hold to a scanner.
[ -n "$searches" ] || exit 0

[ -n "${SR_GUARDRAIL_DIR:-}" ] || refuse_plumbing "SR_GUARDRAIL_DIR is not set, so the shared scanner-lib.sh could not be found"
# shellcheck source=../../context/scanner-declared/scanner-lib.sh
. "$SR_GUARDRAIL_DIR/../../context/scanner-declared/scanner-lib.sh" 2>/dev/null \
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
# scanner is read from, or at the right path but never registered. Measured on
# a real unprimed run: the agent wrote `.sloprail/scanners/auth-token-logs.yaml`,
# was refused with the generic text three times, rewrote the same wrong file,
# and gave up on searching. Each is named with ITS cause: "write it again" is
# the fix only for a file this session never logged, and told to one that can
# never register (switched off, keywords this rule cannot read) it is a loop.
hint=""
if [ -n "${SR_WORKSPACE:-}" ] && [ -d "$SR_WORKSPACE" ]; then
  while IFS= read -r p; do
    [ -n "$p" ] || continue
    rel="${p#"$SR_WORKSPACE"/}"
    if ! printf '%s' "$rel" | grep -qE '(^|/)scanners/[^/]+/scanner\.yaml$'; then
      hint="$hint Not a scanner: $rel — a scanner is read ONLY from a file named exactly scanner.yaml inside its own folder under scanners/, e.g. scanners/token-leaks/scanner.yaml (not a <name>.yaml, and not under .sloprail/). Write it there."
      continue
    fi
    body="$(cat "$p" 2>/dev/null)"
    active="$(scanner_active "$body")"
    if [ "$active" != "true" ]; then
      hint="$hint $rel is switched off (active: ${active:-missing}); a scanner counts only with a column-0 \`active: true\`."
    elif [ -z "$(scanner_keywords "$body")" ]; then
      hint="$hint $rel declares no keyword this project can read: write them as a list under a column-0 \`keywords:\` — one \`- keyword\` per line, or \`keywords: [a, b]\`."
    else
      hint="$hint $rel is in the right place but was not registered in this session (it predates the session, was written by a shell command, or by another agent): write it again with the Write tool — unchanged is fine."
    fi
  done <<EOF
$(find "$SR_WORKSPACE" -maxdepth 6 -path "$SR_WORKSPACE/.git" -prune -o -type f \( -iname '*scanner*.y*ml' -o -ipath '*scanners/*.y*ml' \) -print 2>/dev/null | head -20)
EOF
fi

jq -n --arg hint "$hint" --arg calls "$searches" '{reason: (
  "No scanner is declared in this session, and GitHub searches in this project run against one (this line searches: " + ($calls | split("\n") | join("; ")) + ")."
  + $hint + " "
  + "Declare one with the Write tool at scanners/<short-name>/scanner.yaml — for example scanners/token-leaks/scanner.yaml — containing:\n"
  + "  active: true\n  keywords:\n    - <keyword>\n    - <keyword>\n"
  + "naming every keyword this research must cover, then run ONE gh search whose query contains all of them "
  + "(e.g. gh search issues \"<keyword> <keyword>\"); narrower searches besides it are fine. "
  + "Only searching needs a scanner — gh search, gh api search/… or graphql, a list with --search, or an alias or extension this rule cannot see into; gh issue view, gh pr create, gh repo clone and the like run without one."
)}'
exit 1
