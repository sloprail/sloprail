#!/bin/sh
# Permits a .sloprail/ YAML only where the engine reads one; refuses anything
# else with the place it belongs. Reads only the event's path, never its
# content, so it decides the same whether or not the new content is known.
set -u

if ! command -v jq >/dev/null 2>&1; then
  echo '{"reason":"sloprail/file-guard/misplaced-declaration needs jq, which is not on PATH. Install jq, or disable this rule in .sloprail/config.yaml: disabled: [sloprail/file-guard/misplaced-declaration]"}'
  exit 1
fi

path="$(jq -r '.event.path // ""')"

case "$path" in
.sloprail/config.yaml | .sloprail/config.yml) exit 0 ;;
.sloprail/file-guard/structure.yaml | .sloprail/file-guard/structure.yml) exit 0 ;;
.sloprail/file-guard/*/file-guard.yaml | .sloprail/gate/*/gate.yaml | .sloprail/context/*/context.yaml) exit 0 ;;
esac

rest="${path#.sloprail/}"
case "$path" in
*/structure.yaml | */structure.yml)
  want=".sloprail/file-guard/structure.yaml" ;;
*/file-guard.yaml)
  want=".sloprail/file-guard/<rule-name>/file-guard.yaml" ;;
*/gate.yaml)
  want=".sloprail/gate/<rule-name>/gate.yaml" ;;
*/context.yaml)
  want=".sloprail/context/<rule-name>/context.yaml" ;;
*)
  case "$rest" in
  */*)
    # Any other YAML inside a folder (a data file a check reads, say) is
    # the author's business, not a misplaced declaration.
    exit 0 ;;
  esac
  # A YAML straight under .sloprail/ that is not config.yaml: a declaration
  # with no nature folder.
  want="a rule's own folder: .sloprail/<file-guard|gate|context>/<rule-name>/<file-guard|gate|context>.yaml, or .sloprail/file-guard/structure.yaml for where files may land" ;;
esac

jq -n --arg path "$path" --arg want "$want" \
  '{reason: ("The engine never reads " + $path + ", so a rule written there would silently not be in force. It belongs at " + $want + " (see the sloprail:authoring-guardrails skill).")}'
exit 1
