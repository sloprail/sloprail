#!/bin/sh
# Shared by the misplaced-declaration gate and its file-guard: one library, two thin
# entries. The gate entry checks the pending write's path, the file-guard entry every
# path of the Changeset. Nothing here reads an event: lib_check works on the `path` it
# is given, and only its path, never its content.

lib_setup() {
  set -u

  if ! command -v jq >/dev/null 2>&1; then
    echo '{"reason":"sloprail/file-guard/misplaced-declaration needs jq, which is not on PATH. Install jq, or disable this rule in .sloprail/config.yaml: disabled: [sloprail/file-guard/misplaced-declaration]"}'
    exit 1
  fi
}

# lib_check permits a .sloprail/ YAML only where the engine reads one; it refuses
# anything else with the place it belongs. The folder is the NEAREST `.sloprail/`
# above the file, at any depth (marketplace/plugins/x/.sloprail/ is a root of its
# own), and the place named is under that same folder: a nested folder resolves its
# own relatives, never the repository root's.
lib_check() {
  root=""
  rest="$path"
  case "$rest" in
  .sloprail/*) rest="${rest#.sloprail/}" ;;
  */.sloprail/*) root="${rest%%/.sloprail/*}/"; rest="${rest#*/.sloprail/}" ;;
  *) return 0 ;;
  esac
  # A path can hold several folders (a case's fixture under <rule>/tests/<case>/): the last is the nearest.
  # But a case FOLDER is data: a fixture it holds, a nested `.sloprail/` included, is never a declaration, so
  # the exemption is tested before descending into a nested folder, not only on what is left after it.
  while :; do
    case "$rest" in
    gate/*/tests/*/* | file-guard/*/tests/*/* | context/*/tests/*/* | file-guard/structure.tests/*/*) return 0 ;;
    esac
    case "$rest" in
    .sloprail/*) root="${root}.sloprail/"; rest="${rest#.sloprail/}" ;;
    */.sloprail/*) root="${root}.sloprail/${rest%%/.sloprail/*}/"; rest="${rest#*/.sloprail/}" ;;
    *) break ;;
    esac
  done
  dot="${root}.sloprail"

  case "$rest" in
  # sr-test cases: a case FOLDER under the owning rule's tests/ or the structure gate's structure.tests/ (a file
  # directly in tests/ is no case). A case is data
  # (its own fixtures, declaration-named files included), never a declaration. There is no top-level
  # .sloprail/tests/: that, like any stray folder, is judged by the rules below.
  gate/*/tests/*/* | file-guard/*/tests/*/* | context/*/tests/*/* | file-guard/structure.tests/*/*) return 0 ;;
  config.yaml | config.yml) return 0 ;;
  file-guard/structure.yaml | file-guard/structure.yml) return 0 ;;
  file-guard/*/file-guard.yaml | gate/*/gate.yaml | context/*/context.yaml) return 0 ;;
  esac

  case "$rest" in
  structure.yaml | */structure.yaml | structure.yml | */structure.yml)
    want="$dot/file-guard/structure.yaml" ;;
  file-guard.yaml | */file-guard.yaml)
    want="$dot/file-guard/<rule-name>/file-guard.yaml" ;;
  gate.yaml | */gate.yaml)
    want="$dot/gate/<rule-name>/gate.yaml" ;;
  context.yaml | */context.yaml)
    want="$dot/context/<rule-name>/context.yaml" ;;
  *)
    case "$rest" in
    */*)
      # Any other YAML inside a folder (a data file a check reads, say) is
      # the author's business, not a misplaced declaration.
      return 0 ;;
    esac
    # A YAML straight under .sloprail/ that is not config.yaml: a declaration
    # with no nature folder.
    want="a rule's own folder: $dot/<file-guard|gate|context>/<rule-name>/<file-guard|gate|context>.yaml, or $dot/file-guard/structure.yaml for where files may land" ;;
  esac

  jq -n --arg path "$path" --arg want "$want" \
    '{reason: ("The engine never reads " + $path + ", so a rule written there would silently not be in force. It belongs at " + $want + " (see the sloprail:authoring-guardrails skill).")}'
  exit 1
}

check_path_lib_loaded=1
