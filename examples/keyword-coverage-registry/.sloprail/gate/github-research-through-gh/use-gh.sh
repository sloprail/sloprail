#!/usr/bin/env bash
# Decides whether this call is GitHub research outside gh — any WebSearch; a
# WebFetch whose URL's HOST is a GitHub content host; a curl/wget/httpie that
# fetches one, reads its URLs from a file, or runs on a line naming a GitHub host
# none of its arguments carries — and refuses it with the remedy: the same
# research through gh, against a declared scanner. Anything else permits.
#
# The host is parsed the way a browser parses a URL (see gate.yaml) rather than
# matched by a regex over the raw text, which `https:github.com`,
# `https:\\github.com`, `git%68ub.com` and `github.com.` each evaded.
#
# Fails closed: a payload this script cannot read refuses.
set -uo pipefail

payload="$(cat)"

verdict="$(printf '%s' "$payload" | jq -c '
  def hexval: ascii_downcase | explode | map(if . >= 97 then . - 87 else . - 48 end) | .[0] * 16 + .[1];
  def pdecode: [splits("%")] as $p
    | $p[0] + ([$p[1:][] | if test("^[0-9A-Fa-f]{2}") then ([.[0:2] | hexval] | implode) + .[2:] else "%" + . end] | join(""));
  # The host a URL (or a scheme-less host/path, as curl takes it) names.
  def host:
    gsub("[\t\n\r]"; "") | sub("^[[:cntrl:] ]+"; "") | sub("[[:cntrl:] ]+$"; "")
    | sub("^--url="; "")
    | (if test("^[A-Za-z][A-Za-z0-9+-]*:") then
         capture("^(?<s>[A-Za-z][A-Za-z0-9+-]*):(?<r>.*)$") as $c
         | if ($c.s | ascii_downcase | IN("http","https","ws","wss","ftp")) then ($c.r | sub("^[/\\\\]*"; ""))
           elif ($c.r | startswith("//")) then ($c.r | .[2:])
           else "" end
       else sub("^[/\\\\]*"; "") end)
    | sub("[/\\\\?#].*$"; "")
    | sub("^.*@"; "")
    | pdecode | pdecode
    | ascii_downcase
    | gsub("[。．｡]"; ".")
    | sub(":[0-9]*$"; "")
    | sub("\\.+$"; "");
  def github_host: test("^((www|api|gist|codeload|raw|uploads)\\.)?github\\.com$") or test("(^|\\.)githubusercontent\\.com$");
  def mentions_github: pdecode | ascii_downcase | test("github(usercontent)?\\.com");
  def fetchers: ["curl","wget","http","https","xh","xhs"];

  .event as $e
  | if $e.kind == "PreToolUse" and $e.tool == "WebSearch" then {refuse: "websearch"}
    elif $e.kind == "PreToolUse" and $e.tool == "WebFetch" then
      (($e.input.url // "") | tostring) as $u
      | if ($u | host | github_host) then {refuse: "fetch", tool: "WebFetch", url: $u} else {} end
    elif $e.kind == "PreCommandInvoke" then
      [$e.invocations[]? | select(.bin | IN(fetchers[]))] as $f
      | ([$f[] | .bin as $b | (.argv // [])[1:][] | {bin: $b, arg: .}]) as $args
      | ([$args[] | select(.arg | host | github_host)][0]) as $hit
      | ([$args[] | select((.bin == "curl" and (.arg | test("^--config(=|$)") or test("^-[A-Za-z]*K"))) or
                           (.bin == "wget" and (.arg | test("^--input-file(=|$)") or test("^-[A-Za-z]*i"))))][0]) as $cfg
      | if $hit != null then {refuse: "fetch", tool: $hit.bin, url: $hit.arg}
        elif $cfg != null then {refuse: "config", tool: $cfg.bin, url: $cfg.arg}
        elif ($f | length) > 0 and (($e.raw // "") | mentions_github) and ([$args[] | select(.arg | mentions_github)] | length == 0)
          then {refuse: "hidden", tool: $f[0].bin}
        else {} end
    else {} end
' 2>/dev/null)" || verdict='{"refuse":"unreadable"}'
[ -n "$verdict" ] || verdict='{"refuse":"unreadable"}'

kind="$(printf '%s' "$verdict" | jq -r '.refuse // ""')"
[ -n "$kind" ] || exit 0
tool="$(printf '%s' "$verdict" | jq -r '.tool // "the shell"')"
url="$(printf '%s' "$verdict" | jq -r '.url // ""')"

reads="Read what you found through gh instead: gh issue view <n> -R <owner>/<repo>, gh pr view, gh repo view, or gh api repos/<owner>/<repo>/... (a file: gh api repos/<owner>/<repo>/contents/<path> -H 'Accept: application/vnd.github.raw')."
case "$kind" in
  websearch)
    why="WebSearch is not used in this project, whatever the query — every WebSearch is refused here, so do not retry it: this is a GitHub-research registry, and GitHub research runs through gh so every search can be checked against a declared scanner."
    ;;
  fetch)
    why="Fetching GitHub content (${url}) with ${tool} bypasses gh, so it cannot be checked against a declared scanner. $reads"
    ;;
  config)
    why="${tool} ${url} reads its URLs from a file this project's rules cannot see, so it is refused here: a GitHub URL fetched that way bypasses gh. Pass a URL on the command line instead. $reads"
    ;;
  hidden)
    why="This line names a GitHub host that none of ${tool}'s own arguments carries — the URL is built from a variable, a substitution or piped input — so the fetch cannot be checked and is refused. Fetching GitHub content bypasses gh. $reads"
    ;;
  *)
    why="This call could not be checked (its payload did not parse), so it was refused rather than let through unchecked."
    ;;
esac

jq -n --arg why "$why" '{reason: (
  $why + " "
  + "To search GitHub: declare the scanner first — with the Write tool, write a file named exactly scanner.yaml in its own folder under scanners/ (e.g. scanners/token-leaks/scanner.yaml) holding active: true and a keywords: list naming every keyword the topic requires — "
  + "then run ONE gh search whose query contains all of those keywords, e.g. gh search issues \"<keyword> <keyword>\" (or gh search code / gh search repos)."
)}'
exit 1
