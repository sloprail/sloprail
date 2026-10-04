#!/usr/bin/env bash
# Shared by subjects.sh and check.sh. Sourced, never run.

# roots_of <changed path>... on stdin, one per line -> the distinct tree-relative dirs holding the `.sloprail`
# the path lives under ("" is the repo root, printed as a line holding a single dot).
roots_of() {
  awk '{
    p = "/" $0
    i = index(p, "/.sloprail/")
    if (i > 0) {
      r = substr(p, 2, i - 2)
      print (r == "" ? "." : r)
    }
  }' | LC_ALL=C sort -u
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
