#!/usr/bin/env bash
# gate.yaml's match already decided this call is GitHub research outside gh (any
# WebSearch; a WebFetch — or a curl/wget/httpie from the shell — of a GitHub
# content host). Refuse it with the remedy: the same research through gh,
# against a declared scanner.
set -uo pipefail

payload="$(cat)"
field() { printf '%s' "$payload" | jq -r "$1" 2>/dev/null; }

kind="$(field '.event.kind // ""')"
case "$kind" in
  PreCommandInvoke)
    # The fetching program and the first argument naming a GitHub URL — only to
    # word the reason; the match already decided.
    tool="$(field '[.event.invocations[]? | select(.bin | IN("curl","wget","http","https","xh","xhs"))][0].bin // "the shell"')"
    url="$(field '[.event.invocations[]?.argv[1:][]? | select(test("github(usercontent)?\\.com"; "i"))][0] // ""')"
    # No argument names the host: the URL was built from a variable.
    [ -n "$url" ] || url="a URL built from a variable, on a line naming a GitHub host"
    ;;
  *)
    tool="$(field '.event.tool // "this tool"')"
    url="$(field '.event.input.url // ""')"
    ;;
esac

case "$tool" in
  WebSearch)
    why="WebSearch is not used in this project, whatever the query — every WebSearch is refused here, so do not retry it: this is a GitHub-research registry, and GitHub research runs through gh so every search can be checked against a declared scanner."
    ;;
  *)
    why="Fetching GitHub content (${url}) with ${tool} bypasses gh, so it cannot be checked against a declared scanner. Read what you found through gh instead: gh issue view <n> -R <owner>/<repo>, gh pr view, gh repo view, or gh api repos/<owner>/<repo>/... (a file: gh api repos/<owner>/<repo>/contents/<path> -H 'Accept: application/vnd.github.raw')."
    ;;
esac

jq -n --arg why "$why" '{reason: (
  $why + " "
  + "To search GitHub: declare the scanner first — with the Write tool, write a file named exactly scanner.yaml in its own folder under scanners/ (e.g. scanners/token-leaks/scanner.yaml) holding active: true and a keywords: list naming every keyword the topic requires — "
  + "then run ONE gh search whose query contains all of those keywords, e.g. gh search issues \"<keyword> <keyword>\" (or gh search code / gh search repos)."
)}'
exit 1
