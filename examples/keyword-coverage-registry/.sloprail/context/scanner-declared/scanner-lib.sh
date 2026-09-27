# Shared by every rule in this example — sourced, never run. One reading of a
# scanner file and one reading of the registry, so the rules cannot disagree:
#
#   - scanner-declared (enter.sh) logs the keywords scanner_keywords reads;
#   - scanner-keywords-hold (drops-keywords.sh) decides a drop from the SAME
#     reading. Two parsers used to disagree and fail open: the guard's stopped
#     at a column-0 comment the registry read past, so a keyword after one could
#     be dropped with no citation; and only the registry stripped quotes, so
#     re-quoting a keyword read as dropping it.
#   - search-needs-declared-scanner and verify-scanner-coverage read the
#     scanners still owed a search through registry_owed.
#
# A caller that cannot source this file cannot decide anything, and fails
# closed its own way (see each caller).

# scanner_keywords CONTENT — the entries of the `keywords:` block list, one per
# line, in file order.
#   - The list starts at a column-0 `keywords:` and ends at the next column-0
#     line that is neither an item nor a comment — the next key. A comment or a
#     blank line, at any column, does not end it (YAML allows both inside a
#     block sequence), and neither does an item at column 0 (`- x` directly
#     under the key is valid YAML too).
#   - An item is `- value`. A quoted value ("x" or 'x') is what is inside the
#     quotes; a plain value loses a trailing ` # comment` and trailing blanks.
#     CRs are dropped.
#   - A flow list (`keywords: [a, b]`) yields nothing: the registry then logs no
#     keywords for it, and the guard reads a rewrite into one as dropping every
#     keyword — both the fail-closed direction.
scanner_keywords() {
  printf '%s\n' "$1" | tr -d '\r' | awk '
    /^keywords:/ { inlist = 1; next }
    !inlist { next }
    /^[[:space:]]*(#.*)?$/ { next }
    /^[[:space:]]*-([[:space:]]|$)/ {
      v = $0
      sub(/^[[:space:]]*-[[:space:]]*/, "", v)
      if (v ~ /^"/) {
        v = substr(v, 2); i = index(v, "\""); if (i) v = substr(v, 1, i - 1)
      } else if (v ~ /^\047/) {
        v = substr(v, 2); i = index(v, "\047"); if (i) v = substr(v, 1, i - 1)
      } else {
        sub(/[[:space:]]+#.*$/, "", v)
        sub(/[[:space:]]+$/, "", v)
      }
      if (v != "") print v
      next
    }
    /^[^[:space:]]/ { inlist = 0 }
  '
}

# scanner_active CONTENT — the value of the column-0 `active:` key (unquoted,
# comment stripped), or nothing.
scanner_active() {
  printf '%s\n' "$1" | tr -d '\r' | awk '
    /^active:/ {
      v = $0
      sub(/^active:[[:space:]]*/, "", v)
      sub(/[[:space:]]+#.*$/, "", v)
      sub(/[[:space:]]+$/, "", v)
      gsub(/^["\047]|["\047]$/, "", v)
      print v
      exit
    }
  '
}

# scanner_dir PATH — the registry's name for the scanner at PATH: its folder,
# workspace-relative (scanners/mine for scanners/mine/scanner.yaml). The whole
# path, not the folder's last name: scanners/mine and zz/scanners/mine are two
# scanners, and keying both `mine` let the later write overwrite the other's
# obligation.
scanner_dir() {
  dirname "$1"
}

# registry_owed — the scanners still owed a search, as one JSON array of
# {dir, keywords} on stdout (keywords is null when the logged value does not
# parse). Returns non-zero, printing nothing, when the registry cannot be read
# or does not parse: the caller must refuse then, never read the silence as
# "nothing owed".
#
# Owed = every scanner scanner-declared logged (`scanner:<dir>`), except one
# RETIRED by an admitted delete: scanner-keywords-hold records `retired:<dir>` =
# the declaration's stamp (`stamp:<dir>`, which scanner-declared renews at every
# declaration) once the user's cited words were judged to ask for the delete. It
# counts as retired only while the stamp still matches — a later re-declaration
# is owed again — and while the file is really gone: a delete some other rule
# refused leaves it owed.
registry_owed() {
  _declared="$(sr-session state list --owner scanner-declared)" || return 1
  _retired="$(sr-session state list --owner scanner-keywords-hold retired:)" || return 1
  _rows="$(jq -n -c --arg d "$_declared" --arg r "$_retired" '
    def rows($s): [$s | splits("\n") | select(length > 0) | fromjson];
    rows($d) as $decl
    | (rows($r) | map({key: (.key | ltrimstr("retired:")), value}) | from_entries) as $ret
    | ($decl | map(select(.key | startswith("stamp:")) | {key: (.key | ltrimstr("stamp:")), value}) | from_entries) as $stamp
    | [ $decl[] | select(.key | startswith("scanner:"))
        | (.key | ltrimstr("scanner:")) as $dir
        | {dir: $dir,
           keywords: (.value | try fromjson catch null),
           retired: ($ret[$dir] != null and $ret[$dir] == $stamp[$dir])} ]
  ')" || return 1
  [ -n "$_rows" ] || return 1

  # The file-on-disk half of "retired" is the tree's to answer.
  _gone="[]"
  while IFS= read -r _dir; do
    [ -n "$_dir" ] || continue
    if [ ! -e "${SR_WORKSPACE:-.}/$_dir/scanner.yaml" ]; then
      _gone="$(printf '%s' "$_gone" | jq -c --arg d "$_dir" '. + [$d]')" || return 1
    fi
  done <<EOF
$(printf '%s' "$_rows" | jq -r '.[] | select(.retired) | .dir')
EOF

  printf '%s' "$_rows" | jq -c --argjson gone "$_gone" \
    '[.[] | select((.retired and (.dir as $d | any($gone[]; . == $d))) | not) | {dir, keywords}]'
}
