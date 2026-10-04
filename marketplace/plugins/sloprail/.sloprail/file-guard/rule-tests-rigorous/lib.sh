#!/usr/bin/env bash
# Shared by subjects.sh, check-case.sh and prepare.sh: where a case lives and which rules it covers.
# Sourced, never run. Everything reads SR_TREE (the committed head), never the working tree.

# case_root <case dir, tree-relative> -> the tree-relative dir that holds the `.sloprail` ("" for the repo root)
case_root() {
  local r="${1%%.sloprail/tests/*}"
  printf '%s' "${r%/}"
}

# root_abs <case dir, tree-relative> -> the absolute dir that holds the `.sloprail`
root_abs() {
  local r
  r="$(case_root "$1")"
  if [ -n "$r" ]; then printf '%s/%s' "$SR_TREE" "$r"; else printf '%s' "$SR_TREE"; fi
}

# tree_sha <abs dir>... -> one sha256 over the names and contents of every file under the dirs, sorted
tree_sha() {
  local d f
  {
    for d in "$@"; do
      [ -d "$d" ] || continue
      find "$d" -type f | LC_ALL=C sort | while IFS= read -r f; do
        printf '%s\n' "$f"
        cat "$f"
      done
    done
  } | { shasum -a 256 2>/dev/null || sha256sum; } | cut -d' ' -f1
}

# covered_rules <case dir, tree-relative> -> "<nature>/<rule>" per line: every rule folder of the same
# `.sloprail` root whose name the case's code mentions. A rule the case never names is not covered by it.
# A comment is not code, and a line that maps judge ids to mock scripts (SR_CHECKS_JUDGE_MOCKS) names every
# judge the flow meets, not the rule under test, so neither counts.
covered_rules() {
  local case_abs="$SR_TREE/$1" root nature d name code
  root="$(root_abs "$1")"
  code="$(grep -rhv -E '^[[:space:]]*#|SR_CHECKS_JUDGE_MOCKS' "$case_abs" 2>/dev/null)"
  for nature in file-guard gate context; do
    for d in "$root/.sloprail/$nature"/*/; do
      [ -d "$d" ] || continue
      name="$(basename "$d")"
      if printf '%s\n' "$code" | grep -qF -e "$name"; then
        printf '%s/%s\n' "$nature" "$name"
      fi
    done
  done
}
