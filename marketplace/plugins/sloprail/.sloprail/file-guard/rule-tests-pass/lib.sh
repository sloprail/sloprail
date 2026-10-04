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

# classify <root> on stdin the changed paths of that root (one per line) -> lines of what the change touches
# inside the root's `.sloprail/`, relative to it:
#   case <dir>\t<owner>:<case>   a file of one case: <nature>/<rule>/tests/<case>/ or file-guard/structure.tests/<case>/
#                                 (<owner> is "<nature>/<rule>", "file-guard/structure" for the structure gate)
#   rule <nature>\t<rule>         a file of a rule's folder (a case's included), or of structure.yaml ("structure")
#   other                         any other file: a rule's declaration or scripts, config, structure, ...
# A path outside the root's `.sloprail/` is skipped.
classify() {
  awk -v root="$1" '{
    p = $0
    if (root != ".") { if (index(p, root "/") != 1) next; p = substr(p, length(root) + 2) }
    if (index(p, ".sloprail/") != 1) next
    s = substr(p, 11)
    n = split(s, a, "/")
    if (n >= 4 && a[1] == "file-guard" && a[2] == "structure.tests") {
      print "case\tfile-guard/structure.tests/" a[3] "\tfile-guard/structure:" a[3]; print "rule\tstructure\tstructure"; next
    }
    if (n >= 5 && (a[1] == "gate" || a[1] == "file-guard" || a[1] == "context") && a[3] == "tests") {
      print "case\t" a[1] "/" a[2] "/tests/" a[4] "\t" a[1] "/" a[2] ":" a[4]; print "rule\t" a[1] "\t" a[2]; next
    }
    if (n == 2 && a[1] == "file-guard" && a[2] == "structure.yaml") { print "rule\tstructure\tstructure"; print "other"; next }
    if (n >= 3 && (a[1] == "gate" || a[1] == "file-guard" || a[1] == "context")) { print "rule\t" a[1] "\t" a[2] }
    print "other"
  }'
}

# tree_sha <abs dir>... -> one sha256 over the tree-relative names (relative to SR_TREE: the snapshot's own
# path differs per run and must not reach the fingerprint) and the contents of every file under the dirs, sorted
tree_sha() {
  local d f
  {
    for d in "$@"; do
      [ -d "$d" ] || continue
      find "$d" -type f | LC_ALL=C sort | while IFS= read -r f; do
        printf '%s\n' "${f#"$SR_TREE"/}"
        cat "$f"
      done
    done
  } | { shasum -a 256 2>/dev/null || sha256sum; } | cut -d' ' -f1
}

rule_tests_pass_lib_loaded=1
