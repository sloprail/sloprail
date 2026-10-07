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
#   - `gh issue|pr|label list … --search/-S` (a filter, run by GitHub search),
#     short flags read as gh bundles them (`-lSecurity` is a label, not -S);
#   - a first word that is not a gh built-in: an alias or an extension, which
#     can do anything (gh does not let an alias shadow a built-in — but `co` is
#     itself only a default alias, and can be redefined), and `gh extension
#     exec <name>`;
#   - a gh the line names that the parse does not account for: not a parsed gh
#     invocation, and not inside an argument of a program that does not run
#     code — `eval "gh search …"`, `python3 -c "os.system('gh search …')"`.
#     `git commit -m "fix gh auth"`, `which gh`, `grep -c gh` are accounted for,
#     and so is a heredoc or here-string whose consumer runs no code
#     (`cat > NOTES.md <<EOF … gh search … EOF`); one fed to bash, python3 -,
#     or piped on to sh is code, and is not. Both passes hold only while NOTHING
#     in the command runs code: `echo "gh search …" | bash`, or a heredoc
#     written to s.sh and then `bash s.sh` / `./s.sh`, turn text into commands.
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
  # gh built-ins an alias cannot shadow. Not `co`: it is a DEFAULT ALIAS (for
  # pr checkout), and `gh alias set co "search issues" --clobber` redefines it.
  def builtins: ["accessibility","agent-task","alias","api","attestation","auth","browse","cache","codespace","completion","config","extension","ext","gist","gpg-key","help","issue","label","licenses","org","pr","preview","project","release","repo","ruleset","run","search","secret","ssh-key","status","variable","version","workflow"];
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
  # --search/-S among gh issue|pr|label list flags, read the way gh parses them:
  # a short flag that takes a value ends its cluster and, at the cluster end,
  # takes the next word — so `-lSecurity` is --label Security, not -S.
  def value_short: ["A","a","B","H","l","L","m","s","q","t","R"];
  def value_long: ["--author","--assignee","--base","--head","--label","--limit","--milestone","--state","--jq","--template","--repo","--app","--json"];
  def search_flag:
    reduce .[] as $x ({skip: false, hit: false};
      if .hit then .
      elif .skip then .skip = false
      elif ($x | test("^--search(=|$)")) then .hit = true
      elif ($x | startswith("--")) then (if ($x | IN(value_long[])) then .skip = true else . end)
      elif ($x | test("^-[A-Za-z]")) then
        ($x[1:] | explode | map([.] | implode)) as $cs
        | (reduce range(0; $cs | length) as $i ({done: false, hit: false, skip: false};
             if .done then .
             elif $cs[$i] == "S" then .hit = true | .done = true
             elif ($cs[$i] | IN(value_short[])) then .done = true | .skip = ($i == ($cs | length) - 1)
             else . end)) as $r
        | .hit = $r.hit | .skip = $r.skip
      else . end)
    | .hit;
  # A gh call with no subcommand the parser could see is a search, unless the
  # whole command is literally `gh` (help): its words came from somewhere the
  # parse cannot follow — `echo search issues x | xargs gh`, `gh $(echo search
  # …)`, `A="search issues x"; gh $A`.
  def searches($raw):
    (.argv // []) as $a
    | if ($a | length) < 2 then (($raw | gsub("^\\s+|\\s+$"; "")) != "gh")
      else $a[1] as $sub
      | if ($sub | IN("--version","--help","-h")) then false
        elif ($sub | startswith("-")) then true
        elif $sub == "search" then ($a[2:] | any(.[]; . == "--help" or . == "-h") | not)
        elif $sub == "api" then ($a[2:] | endpoint | search_endpoint)
        elif ($sub | IN("issue","pr","label")) then ($a[2:] | search_flag)
        # `gh extension exec <name>` runs an extension, which can do anything.
        elif ($sub | IN("extension","ext")) then (($a[2] // "") == "exec")
        elif ($sub | IN(builtins[])) then false
        else true end
      end;
  # A word-bounded `gh` in a string.
  # A backslash escape (`\tgh`, `\ngh` in printf or echo -e) is a boundary too:
  # it becomes a tab or a newline before the text runs anywhere.
  def mentions: [match("(^|[^A-Za-z0-9_./-]|\\\\[a-z])gh(?=[^A-Za-z0-9_.-]|$)"; "g")] | length;
  # Programs whose arguments are CODE: a gh inside them may run.
  def code_runner_name:
    (split("/") | last) as $b
    | ($b | IN("eval","sh","bash","zsh","dash","ksh","fish","su","runuser","flock","script","ssh","watch","node","nodejs","deno","bun","perl","ruby","php","lua","osascript","awk","gawk","mawk","nawk","tclsh","expect","source","."))
      or ($b | test("^(python|pypy)[0-9.]*$"));
  # Programs that never execute their input or arguments as code. The data
  # pass below needs EVERY program on the line to be one of these: a fixed list
  # of code-RUNNERS was always one short (a script at `/tmp/zzs` with no
  # extension, `PATH=.:$PATH zzs`, `git -c alias.zz="!gh …" zz`, `make -f -`).
  # Not here, deliberately: awk and sed (system(), the `e` command), xargs,
  # make, less, env -S, any interpreter, and anything run by a path that is not
  # a system bin directory.
  def data_safe_names: ["echo","printf","cat","tee","grep","egrep","fgrep","rg","head","tail","wc","sort","uniq","cut","tr","jq","which","type","command","gh","git","sr-file","sr-session","cd","pwd","ls","mkdir","touch","cp","mv","rm","ln","chmod","true","false","test","[","date","basename","dirname","diff","sleep","curl","wget","find","export","unset","nohup","nice","timeout","time","stdbuf"];
  # The git subcommands that run nothing the caller chose. No `-c` (an
  # alias or a hook path can be set there) and no alias: any other subcommand
  # may be one.
  def git_safe_subs: ["add","commit","status","log","diff","show","rm","mv","restore","switch","checkout","branch","tag","blame","grep","ls-files","rev-parse","init","fetch","push","pull","remote","stash","merge","reset","clean","clone","describe","shortlog","reflog","worktree"];
  def git_data_safe:
    (.argv // []) as $a
    | ([$a[1:][] | select(startswith("-") | not)][0] // "") as $sub
    | ([$a[1:][] | select(. == "-c" or startswith("--config-env") or startswith("--exec-path"))] | length == 0)
      and ($sub | IN(git_safe_subs[]));
  def data_safe:
    ((.argv // [])[0] // "") as $p
    | ((.bin // "") | IN(data_safe_names[]))
      and (($p | contains("/") | not) or ($p | test("^/(usr/(local/)?|opt/homebrew/)?s?bin/")))
      and (if .bin == "git" then git_data_safe
           elif .bin == "env" then false
           else true end);
  # An invocation that may run code: anything not data-safe.
  def runs_code: data_safe | not;
  # The command text a heredoc or here-string at offset $o of line $l feeds:
  # its own pipeline stage and every later stage of that pipeline, up to the
  # next ; && || — so `cat <<EOF | sh` feeds sh.
  def consumer($l; $o):
    ($l[0:$o] | sub("^.*(;|&&|\\|)"; "")) + ($l[$o:] | sub("(;|&&|\\|\\|).*$"; ""));
  # Whether that text runs code: in any stage, the FIRST word is `.`/`source`,
  # or any word (quotes stripped) names another code-running program.
  def feeds_code:
    [splits("\\|") | [splits("[\\s]+") | select(length > 0) | gsub("^[\"\\x27]+|[\"\\x27]+$"; "")]]
    | any(.[]; (.[0] // "" | IN(".", "source")) or any(.[]; . != "." and . != "source" and code_runner_name));
  # Text the shell substitutes before the consumer sees it — `$(…)` and
  # backticks in an unquoted heredoc or a double-quoted/bare here-string — is a
  # parsed invocation already; it is not data too.
  def unsubstituted: gsub("\\$\\([^)]*\\)|\\x60[^\\x60]*\\x60"; "");
  # The gh mentions in heredoc bodies and here-strings whose consumer runs NO
  # code: those are data (`cat > NOTES.md <<EOF`, `git commit -F - <<EOF`,
  # `sr-file write X.md <<EOF`), accounted for like an argument.
  def data_mentions:
    split("\n") as $lines
    | reduce range(0; $lines | length) as $i ({cur: null, queue: [], acc: 0};
        $lines[$i] as $l
        | if .cur != null then
            (if .cur.strip then ($l | sub("^\t+"; "")) else $l end) as $t
            | if $t == .cur.delim then (.cur = (.queue[0] // null) | .queue = .queue[1:])
              elif .cur.data then .acc += (if .cur.quoted then $l else ($l | unsubstituted) end | mentions)
              else . end
          else
            ([$l | match("(?<!<)<<(?!<)(-?)[ \\t]*([\"\\x27]?)([A-Za-z0-9_.-]+)[\"\\x27]?"; "g")
              | {strip: (.captures[0].string == "-"), quoted: (.captures[1].string != ""),
                 delim: .captures[2].string,
                 data: (consumer($l; .offset) | feeds_code | not)}]) as $ops
            | ([$l | match("<<<[ \\t]*(\"[^\"]*\"|\\x27[^\\x27]*\\x27|[^ \\t;|&]+)"; "g")
                | select(consumer($l; .offset) | feeds_code | not)
                | .captures[0].string
                | (if startswith("\u0027") then . else unsubstituted end) | mentions] | add // 0) as $strings
            | .acc += $strings
            | .queue = ($ops[1:])
            | .cur = ($ops[0] // null)
          end)
    | .acc;
  ([.event.invocations[]? | select(.bin == "gh")]) as $gh
  # Every gh the line names, minus the ones the parse accounts for: each parsed
  # gh invocation — and, ONLY when nothing in the whole command runs code, each
  # mention inside an argument (`git commit -m "fix gh auth"`, `which gh`,
  # `grep -c gh`, `gh pr create --title "Update gh workflow"`) or inside a
  # heredoc or here-string (data_mentions). Once anything runs code, text can
  # become a command: `echo "gh search …" | bash`, `printf … | sh`, `… | xargs
  # sh -c`, a heredoc written to s.sh and then `bash s.sh` or `./s.sh`. What is
  # left over is a gh call this rule cannot see.
  | ((.event.raw // "") | mentions) as $named
  | any(.event.invocations[]?; runs_code) as $code
  | (($gh | length)
     + (if $code then 0 else
          ([.event.invocations[]? | (.argv // [])[1:][] | mentions] | add // 0)
          + ((.event.raw // "") | data_mentions)
        end)) as $accounted
  | (.event.raw // "") as $raw
  | ($gh[] | select(searches($raw)) | (.argv | join(" "))),
    (if $named > $accounted then
       ([.event.invocations[]? | select(runs_code) | .bin] | unique | join(", ")) as $runners
       | "HIDDEN a gh call this rule cannot see into, because code runs on this line ("
       + (if $runners == "" then "text fed to code" else $runners end)
       + "): " + ($raw | .[0:120])
     else empty end)
' 2>/dev/null)" || searches="an unreadable command line"

# No search on this line: nothing to hold to a scanner.
[ -n "$searches" ] || exit 0

[ -n "${SR_GUARDRAIL_DIR:-}" ] || refuse_plumbing "SR_GUARDRAIL_DIR is not set, so the shared scanner-lib.sh could not be found"
# A helper stopped early runs only partly (whether the `.` then fails depends
# on the bash version); only its last-line sentinel proves it loaded whole.
unset scanner_lib_loaded
# shellcheck source=../../context/scanner-declared/scanner-lib.sh
. "$SR_GUARDRAIL_DIR/../../context/scanner-declared/scanner-lib.sh" 2>/dev/null \
  || refuse_plumbing "the shared scanner-lib.sh beside scanner-declared could not be loaded"
[ "${scanner_lib_loaded:-}" = 1 ] \
  || refuse_plumbing "the shared scanner-lib.sh did not load whole (its last-line sentinel scanner_lib_loaded is unset)"

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

# A line where gh is only TEXT beside a command that runs code (`git commit -m
# "fix gh auth" && ./gradlew test`) is refused too, because this rule cannot
# tell text from a command once code runs; say how to get past that honestly.
separately=""
case "$searches" in
  *"HIDDEN "*)
    separately=" If gh only appears here as text (a commit message, a note, a pattern) and nothing on this line runs it, run the code-running command in a separate call from the one that mentions gh — each then runs without a scanner."
    ;;
esac

jq -n --arg hint "$hint" --arg calls "$searches" --arg separately "$separately" '{reason: (
  "No scanner is declared in this session, and GitHub searches in this project run against one (this line searches: " + ($calls | gsub("HIDDEN "; "") | split("\n") | join("; ")) + ")."
  + $separately
  + $hint + " "
  + "Declare one with the Write tool at scanners/<short-name>/scanner.yaml — for example scanners/token-leaks/scanner.yaml — containing:\n"
  + "  active: true\n  keywords:\n    - <keyword>\n    - <keyword>\n"
  + "naming every keyword this research must cover, then run ONE gh search whose query contains all of them "
  + "(e.g. gh search issues \"<keyword> <keyword>\"); narrower searches besides it are fine. "
  + "Only searching needs a scanner — gh search, gh api search/… or graphql, a list with --search, or an alias or extension this rule cannot see into; gh issue view, gh pr create, gh repo clone and the like run without one."
)}'
exit 1
