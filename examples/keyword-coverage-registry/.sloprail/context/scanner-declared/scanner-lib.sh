# Shared by every rule in this example — sourced, never run. One reading of a
# scanner file and one reading of the registry, so the rules cannot disagree:
#
#   - scanner-declared (enter.sh) logs the keywords scanner_keywords reads;
#   - scanner-keywords-hold (drops-keywords.sh, record-admitted.sh) decides and
#     records a drop from the SAME reading. Two parsers used to disagree and fail
#     open: the guard's stopped at a column-0 comment the registry read past, so
#     a keyword after one could be dropped with no citation; and only the
#     registry stripped quotes, so re-quoting a keyword read as dropping it.
#   - search-needs-declared-scanner and verify-scanner-coverage read the
#     scanners still owed a search through registry_owed.
#
# A caller that cannot source this file cannot decide anything, and fails
# closed its own way (see each caller).
#
# The registry, per scanner folder <dir> (e.g. scanners/mine):
#   scanner-declared      scanner:<dir>   the keywords owed (JSON array); only
#                                         ever grows from what a write declares
#                         stamp:<dir>     a token renewed at every declaration
#   scanner-keywords-hold retired:<dir>   the stamp an admitted delete retired
#                         narrowed:<dir>  {stamp, keywords}: an admitted drop
# "Admitted" means the user's words were cited AND judged to ask for it. A
# retirement or a narrowing counts only while its stamp is the declaration's
# current one, so declaring the scanner again makes it owed in full again.

# scanner_keywords CONTENT — the keywords a scanner declares, one per line, in
# file order. `keywords` is a column-0 key (`keywords:` or `keywords :`) holding:
#   - a flow list on the key's line, which may run onto following lines until
#     its `]`: `keywords: [auth token, "leak", 'it''s']`;
#   - or a block list: `- value` items, at any indentation (column 0 is valid
#     YAML too). It ends at the next column-0 line that is neither an item nor a
#     comment — the next key. Comments and blank lines, at any column, do not
#     end it. An item's value is:
#       "double quoted" (the inside), 'single quoted' ('' is one quote),
#       a plain scalar — trailing ` # comment` dropped — continued by
#       more-indented lines that are not items (joined with one space),
#       or a block scalar `>`/`|` (its more-indented lines, joined with spaces).
# CRs are dropped. Anything else yields nothing — the registry then logs no
# keywords and the guard reads the rewrite as dropping every keyword, both the
# fail-closed direction.
scanner_keywords() {
  printf '%s\n' "$1" | tr -d '\r' | awk '
    function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
    function nocomment(s) { sub(/(^|[ \t]+)#.*$/, "", s); return s }
    # One scalar, as written after "- " or between commas.
    function scalar(v,   out, c) {
      v = trim(v)
      if (v ~ /^"/) {
        v = substr(v, 2); out = ""
        while (length(v) > 0) {
          c = substr(v, 1, 1)
          if (c == "\\" && length(v) > 1) { out = out substr(v, 2, 1); v = substr(v, 3); continue }
          if (c == "\"") break
          out = out c; v = substr(v, 2)
        }
        return out
      }
      if (v ~ /^\047/) {
        v = substr(v, 2); out = ""
        while (length(v) > 0) {
          c = substr(v, 1, 1)
          if (c == "\047") {
            if (substr(v, 2, 1) == "\047") { out = out "\047"; v = substr(v, 3); continue }
            break
          }
          out = out c; v = substr(v, 2)
        }
        return out
      }
      return trim(nocomment(v))
    }
    function emit(v) { v = trim(v); if (v != "") print v }
    function flush() { if (have) emit(pending); have = 0; pending = ""; mode = "" }
    # The items of a flow list body (between [ and ]), split on commas outside quotes.
    function flow(body,   i, c, q, item) {
      q = ""; item = ""
      for (i = 1; i <= length(body); i++) {
        c = substr(body, i, 1)
        if (q != "") { item = item c; if (c == q) q = ""; continue }
        if (c == "\"" || c == "\047") { q = c; item = item c; continue }
        if (c == ",") { emit(scalar(item)); item = ""; continue }
        item = item c
      }
      emit(scalar(item))
    }
    BEGIN { state = "out" }
    state == "flow" {
      buf = buf " " $0
      if (index(buf, "]")) { flow(substr(buf, 1, index(buf, "]") - 1)); state = "done" }
      next
    }
    state == "out" && /^keywords[ \t]*:/ {
      rest = $0; sub(/^keywords[ \t]*:[ \t]*/, "", rest)
      if (rest ~ /^\[/) {
        buf = substr(rest, 2)
        if (index(buf, "]")) { flow(substr(buf, 1, index(buf, "]") - 1)); state = "done" }
        else state = "flow"
        next
      }
      state = "block"; next
    }
    state != "block" { next }
    /^[ \t]*(#.*)?$/ { next }
    /^[ \t]*-([ \t]|$)/ {
      flush()
      match($0, /^[ \t]*-/); indent = RLENGTH
      v = $0; sub(/^[ \t]*-[ \t]*/, "", v)
      if (v ~ /^[>|][+-]?[0-9]*[ \t]*(#.*)?$/) { have = 1; mode = "scalar"; next }
      if (v ~ /^["\047]/) { have = 1; pending = scalar(v); mode = "quoted"; next }
      have = 1; pending = scalar(v); mode = "plain"; next
    }
    /^[^ \t]/ { flush(); state = "done"; next }
    {
      # A more-indented line that is not an item continues the item above.
      match($0, /^[ \t]*/)
      if (have && RLENGTH >= indent && mode != "quoted") {
        line = trim(mode == "plain" ? nocomment($0) : $0)
        pending = (pending == "" ? line : pending " " line)
      }
    }
    END { if (state == "block") flush() }
  '
}

# scanner_active CONTENT — "true" when the column-0 `active` key is true (true,
# yes or on, any case, quoted or not); otherwise its value as written, or
# nothing when there is no such key.
scanner_active() {
  printf '%s\n' "$1" | tr -d '\r' | awk '
    /^active[ \t]*:/ {
      v = $0
      sub(/^active[ \t]*:[ \t]*/, "", v)
      sub(/[ \t]+#.*$/, "", v)
      sub(/[ \t]+$/, "", v)
      gsub(/^["\047]|["\047]$/, "", v)
      l = tolower(v)
      print ((l == "true" || l == "yes" || l == "on") ? "true" : v)
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

# registry_rows — every scanner scanner-declared logged, as one JSON array of
# {dir, keywords, stamp, retired}: keywords are the ones owed — the admitted
# narrowing when one matches the current stamp, else the logged set (null when
# that does not parse); retired is whether an admitted delete matches the
# current stamp. Returns non-zero, printing nothing, when either owner's state
# cannot be read or does not parse.
registry_rows() {
  _declared="$(sr-session state list --owner scanner-declared)" || return 1
  _held="$(sr-session state list --owner scanner-keywords-hold)" || return 1
  jq -n -c --arg d "$_declared" --arg h "$_held" '
    def rows($s): [$s | splits("\n") | select(length > 0) | fromjson];
    def by($rows; $prefix): $rows | map(select(.key | startswith($prefix)) | {key: (.key | ltrimstr($prefix)), value}) | from_entries;
    rows($d) as $decl
    | rows($h) as $held
    | by($decl; "stamp:") as $stamp
    | by($held; "retired:") as $retired
    | by($held; "narrowed:") as $narrowed
    | [ $decl[] | select(.key | startswith("scanner:"))
        | (.key | ltrimstr("scanner:")) as $dir
        | ($narrowed[$dir] // null | if . == null then null else (try fromjson catch null) end) as $n
        | {dir: $dir,
           stamp: $stamp[$dir],
           keywords: (if $n != null and $n.stamp == $stamp[$dir] and ($n.keywords | type) == "array"
                      then $n.keywords
                      else (.value | try fromjson catch null) end),
           retired: ($retired[$dir] != null and $retired[$dir] == $stamp[$dir])} ]
  '
}

# registry_keywords DIR — the keywords the registry holds owed for one scanner
# (a JSON array; [] when it holds none, or when an admitted delete retired the
# current declaration — declaring it again starts from what that declares).
# Non-zero when the registry cannot be read.
registry_keywords() {
  _rows="$(registry_rows)" || return 1
  printf '%s' "$_rows" | jq -c --arg d "$1" \
    '[.[] | select(.dir == $d) | if .retired then [] else (.keywords // []) end][0] // []'
}

# registry_owed — the scanners still owed a search, as one JSON array of
# {dir, keywords} on stdout (keywords null when the logged value does not
# parse). Returns non-zero, printing nothing, when the registry cannot be read
# or does not parse: the caller must refuse then, never read the silence as
# "nothing owed". A retired scanner is left out only while its file is really
# gone: a delete some other rule refused leaves it owed.
registry_owed() {
  _rows="$(registry_rows)" || return 1
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

# has_user_citation PAYLOAD — whether the event carries a resolved citation of
# the user's own words (one the engine resolved against the user's messages).
has_user_citation() {
  printf '%s' "$1" | jq -e '[.event.citations[]? | select((.sourceTypes // []) | index("user"))] | length > 0' >/dev/null 2>&1
}
