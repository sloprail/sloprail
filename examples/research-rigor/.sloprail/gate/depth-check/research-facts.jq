# research-facts.jq — what ONE trajectory's research did, read off
# `sr-session trajectory normalize` output (an array of normalized entries).
#
# Emits {clones, failedClones, unresolvedClones, reads}:
#   clones            [{dest, repo, seen}] — `git clone`s that did not fail and
#                     whose destination directory is known (absolute,
#                     canonical). seen: git's own `Cloning into '<dest>'...`
#                     line is in the result — positive evidence the clone ran.
#                     A clone whose output hides that (-q, 2>/dev/null, a
#                     filter) is left to verify-depth.sh to confirm on disk.
#   failedClones      [dest] — `git clone`s that failed (an error result, or a
#                     `fatal:` naming them): a directory already there is not
#                     this run's clone
#   unresolvedClones  how many `git clone`s ran whose destination could not be
#                     placed (a directory named through a variable, or after a
#                     `cd` the engine could not resolve)
#   reads             [path] — files or directories whose CONTENT a successful
#                     tool call read: the Read tool, the Grep tool, and shell
#                     readers (cat, head, tail, sed, awk, grep, rg, …). A
#                     search counts only if it printed something and was not
#                     restricted to documentation files.
#
# Nothing here decides depth; verify-depth.sh does, over every trajectory of
# the research run at once (a sub-agent's clone and the root's reads are one
# run's research).
#
# Inputs: --arg ws  the workspace root, the directory a record with no `cwd`
#                   of its own started in (the mock harness writes cwd only on
#                   a transcript's first record; Claude Code writes it on all)
#         --arg home  $HOME, for a leading `~`

# ---- paths ------------------------------------------------------------------

# Collapse `.`, `..` and repeated slashes in an absolute path, and drop macOS's
# /private prefix, so `/tmp/x`, `/private/tmp/x` and `/tmp/./y/../x` are one
# directory — an agent spells the same clone all of those ways.
def canon:
  (split("/") | reduce .[] as $s ([];
      if $s == "" or $s == "." then .
      elif $s == ".." then (if length > 0 then .[:-1] else . end)
      else . + [$s] end)
  | "/" + join("/"))
  | sub("^/private(?<rest>/(tmp|var|etc)(/.*)?)$"; "\(.rest)");

# Resolve a path as written against the directory it was written in. null when
# it cannot be placed — no base to put a relative path on.
def resolve($base):
  if . == null or . == "" then null
  elif startswith("/") then canon
  elif . == "~" then ($home | canon)
  elif startswith("~/") then ($home + .[1:] | canon)
  elif $base == null then null
  else ($base + "/" + . | canon)
  end;

# The directory one invocation runs in: the engine's `.cwd` (see events.md) is
# "." for where the line started, relative to that, absolute, or "" when a `cd`
# could not be resolved — which stays unknown rather than being guessed.
def invdir($base):
  (.cwd // ".") as $c
  | if $c == "" then null
    elif $c == "." then $base
    else ($c | resolve($base))
    end;

# ---- git clone --------------------------------------------------------------

# git's own options that take the NEXT word as their value, before the
# subcommand. `-C <dir>` also moves where the clone lands.
def git_global_valued: ["-C", "-c", "--git-dir", "--work-tree", "--namespace", "--config-env"];

# `git clone` options that take the NEXT word as their value — so that value is
# not read as the repository or the destination.
def clone_valued: ["-o", "--origin", "-b", "--branch", "-u", "--upload-pack",
  "--reference", "--reference-if-able", "--separate-git-dir", "--depth",
  "--shallow-since", "--shallow-exclude", "-c", "--config", "--server-option",
  "-j", "--jobs", "--template", "--filter", "--bundle-uri", "--revision",
  "--ref-format"];

# The directory git derives from a repository when no destination is given:
# `https://github.com/a/node-retry.git` → node-retry, `git@h:a/b.git` → b.
def humanish:
  sub("/+$"; "") | sub("/\\.git$"; "") | sub("^.*[/:]"; "") | sub("\\.git$"; "");

# argv of one `git` invocation → {cdirs, repo, dest} when it is a clone, else
# empty. dest is as written (null → git's humanish name of the repo).
def clone_of:
  . as $a
  | {i: 1, cdirs: []}
  | until(.i >= ($a | length) or (($a[.i] | startswith("-")) | not);
      if $a[.i] == "-C" then .cdirs += [$a[.i + 1]] | .i += 2
      elif ($a[.i] as $t | git_global_valued | index($t)) then .i += 2
      else .i += 1 end)
  | select($a[.i] == "clone")
  | .cdirs as $cdirs
  | reduce $a[(.i + 1):][] as $t ({pos: [], skip: false, dd: false};
      if .skip then .skip = false
      elif .dd then .pos += [$t]
      elif $t == "--" then .dd = true
      elif ($t | startswith("-")) and $t != "-" then
        (if ($t as $x | clone_valued | index($x)) then .skip = true else . end)
      else .pos += [$t] end)
  | select(.pos | length > 0)
  | {cdirs: $cdirs, repo: .pos[0], explicit: (.pos | length > 1),
     dest: (.pos[1] // (.pos[0] | humanish))};

# Whether a command line expands a variable or a substitution. The engine
# expands every word against an EMPTY environment (it never guesses at one), so
# `git clone url "$TMPDIR/x"` reaches argv as `/x` — a directory the clone did
# not land in. A destination written on such a line is not trusted.
def dynamic: test("\\$[{(A-Za-z_]|`");

# ---- reading ----------------------------------------------------------------

# Shell programs whose operands are files they read, and the options of each
# that take the next word as a value (so the value is not read as a file).
def readers: {
  cat: [], less: [], more: [], nl: [], view: [],
  bat: ["-l", "--language", "-r", "--line-range", "-H", "--highlight-line", "--theme", "--style", "-m", "--map-syntax"],
  head: ["-n", "-c", "--lines", "--bytes"],
  tail: ["-n", "-c", "--lines", "--bytes"],
  sed: ["-e", "-f", "-l", "--expression", "--file", "--line-length"],
  awk: ["-f", "-v", "-F", "--file", "--assign", "--field-separator"],
  grep: ["-e", "-f", "-m", "-A", "-B", "-C", "-d", "-D", "--regexp", "--file", "--max-count",
         "--after-context", "--before-context", "--context", "--devices", "--directories", "--label",
         "--include", "--exclude", "--exclude-dir", "--exclude-from"],
  rg: ["-e", "-f", "-g", "-t", "-T", "-m", "-A", "-B", "-C", "-M", "-j", "-E", "-d", "-r",
       "--regexp", "--file", "--glob", "--iglob", "--type", "--type-not", "--max-count",
       "--after-context", "--before-context", "--context", "--max-columns", "--threads",
       "--encoding", "--max-depth", "--max-filesize", "--sort", "--sortr", "--type-add",
       "--colors", "--pre", "--pre-glob", "--replace", "--context-separator"],
  ag: ["-A", "-B", "-C", "-G", "-m", "--file-search-regex", "--max-count", "--ignore", "--depth"]
} | .egrep = .grep | .fgrep = .grep | .gawk = .awk;

# Options whose value restricts a search to the files it names: grep's
# --include, rg's -g/--glob/--iglob and -t/--type, ag's -G.
def include_opts: ["--include", "-g", "--glob", "--iglob", "-t", "--type", "-G", "--file-search-regex"];

# Whether a search's file filter (a glob, an rg type name, an ag regex) names
# only documentation — `*.md`, `*.{md,rst}`, `md`, `README*`. A negated rg glob
# (`!*.md`) excludes rather than selects, and is never documentation-only.
def doc_filter:
  "(md|markdown|mdx|rst|txt|adoc|asciidoc|org)" as $ext
  | ascii_downcase | gsub("[\\\\$]"; "")
  | (startswith("!") | not)
    and (test("\\." + $ext + "$") or test("\\.\\{(" + $ext + ",?)+\\}$") or test("^" + $ext + "$")
         or test("(^|/)\\*?(readme|changelog|license|licence)"));

# One option's value, folded into the operand scan: -e/-f give the pattern (or
# program), so the first operand is a file; an include filter is recorded.
def optval($o; $v):
  (if ($o | IN("-e", "-f", "--regexp", "--file", "--expression")) then .given = true else . end)
  | (if ($o as $x | include_opts | index($x)) then .incl += [$v] else . end);

# A cluster of short options — `-rn`, `-A3`, `-rnefoo`, `-tmd`. Each letter is
# its own option until one that takes a value, which takes the rest of the word
# (or, when nothing is left, the next word).
def short_cluster($t; $valued):
  ($t[1:] | explode | map([.] | implode)) as $cs
  | reduce range(0; $cs | length) as $i (. + {stop: false};
      if .stop then .
      else ("-" + $cs[$i]) as $o
      | if ($valued | index($o)) != null then
          ($cs[$i + 1:] | join("")) as $rest
          | (if $rest == "" then .skip = $o else optval($o; $rest) end)
          | .stop = true
        elif ($cs[$i] | IN("r", "R")) then .recursive = true
        else . end
      end)
  | del(.stop);

# argv → the operands left once options (and their values) are set aside;
# whether a pattern/program was given by an option (-e/-f), in which case the
# first operand is a file rather than the pattern; and any include filters.
def operands($valued):
  reduce .[1:][] as $t ({ops: [], skip: null, dd: false, given: false, recursive: false, incl: []};
    if .skip != null then optval(.skip; $t) | .skip = null
    elif .dd then .ops += [$t]
    elif $t == "--" then .dd = true
    elif ($t | startswith("--")) then
      ($t | sub("=.*$"; "")) as $name
      | if ($t | contains("=")) then optval($name; $t | sub("^[^=]*="; ""))
        elif ($valued | index($name)) != null then .skip = $name
        elif ($name | IN("--recursive", "--dereference-recursive")) then .recursive = true
        else . end
    elif ($t | startswith("-")) and ($t | length) > 1 then short_cluster($t; $valued)
    else .ops += [$t] end);

# One invocation of a reader → {paths, search, doconly}: the paths (as written)
# whose content it reads, whether it is a search (which reads only what it
# prints), and whether its include filters name documentation alone. No paths
# for a program that is not a reader. A search with no path searches where it
# runs, returned as ".".
def read_of:
  .bin as $bin
  | (readers[$bin]) as $valued
  | if $valued == null then {paths: []}
    else (.argv | operands($valued)) as $o
    | ($bin | IN("grep", "egrep", "fgrep", "rg", "ag")) as $search
    | {search: $search,
       doconly: (($o.incl | length) > 0 and all($o.incl[]; doc_filter)),
       paths: (
         if ($bin | IN("sed", "awk", "gawk")) then
           (if $o.given then $o.ops else $o.ops[1:] end)
           | map(select(test("^[A-Za-z_][A-Za-z0-9_]*=") | not))
         elif $search then
           (if $o.given then $o.ops else $o.ops[1:] end) as $files
           | if ($files | length) > 0 then $files
             elif ($bin | IN("rg", "ag")) or ($bin != "rg" and $o.recursive) then ["."]
             else [] end
         elif ($bin | IN("less", "more", "view")) then
           # `less +G f`: a +command is not a file.
           $o.ops | map(select(startswith("+") | not))
         else $o.ops
         end
         | map(select(. != "-")))}
    end;

# Whether a tool result shows nothing: a search that printed nothing read
# nothing (the Grep tool says so in words).
def blank: test("\\S") | not;
def grep_tool_empty: blank or test("^\\s*No (files|matches) found");

# ---- tool results -----------------------------------------------------------

# tool_use id → {err, text} of its result. The LAST result for an id wins.
def results:
  [ .[] | select(.type == "user") | (.message.content? // empty) | arrays | .[]
    | select(.type == "tool_result")
    | {key: .tool_use_id,
       value: {err: (.is_error == true),
               text: (if (.content | type) == "string" then .content
                      elif (.content | type) == "array" then ([.content[] | .text? // empty] | join("\n"))
                      else "" end)}} ]
  | from_entries;

# ---- the trajectory ---------------------------------------------------------

. as $entries
| ($entries | results) as $res
# Every tool call, with the directory its record ran in. A record without its
# own cwd inherits the last one seen (the harness's shell cwd persists).
| (reduce $entries[] as $e ({base: (if $ws == "" then null else ($ws | canon) end), calls: []};
    .base = (if ($e.cwd // "") != "" then ($e.cwd | canon) else .base end)
    | . as $st
    | if $e.type == "assistant" then
        .calls += [ ($e.message.content? // []) | arrays | .[] | select(.type == "tool_use")
                    | {id, name, input, base: $st.base, events: ($e.events // [])} ]
      else . end)
  | .calls) as $calls
| [ $calls[]
    | . as $c
    | ($res[$c.id] // {err: false, text: ""}) as $r
    | if $c.name == "Bash" then
        [ first($c.events[] | select(.kind == "PreCommandInvoke" and .raw == $c.input.command)) | .invocations[] ]
        | map(
            invdir($c.base) as $dir
            | if .bin == "git" then
                (.argv | clone_of) as $cl
                | if $cl == null then empty
                  else
                    (reduce $cl.cdirs[] as $d ($dir; . as $acc | $d | resolve($acc))) as $gdir
                    | ($cl.dest | resolve($gdir)) as $dest
                    | ($r.text | split("\n") | map(split("\r")[])) as $lines
                    # git reports a clone it refused (destination exists, repo
                    # not found) as `fatal:` quoting the destination as written
                    # or naming the repository. A pipeline (`git clone … |
                    # head`) exits 0 anyway, so the output is read too.
                    | ($lines | map(select(startswith("fatal:")))) as $fatal
                    # git announces a clone it is making as `Cloning into
                    # '<dest>'...` — the positive evidence. Absent (-q,
                    # 2>/dev/null, a filter), verify-depth.sh looks on disk.
                    | ([ $lines[] | capture("^Cloning into '(?<p>.*)'\\.\\.\\.") | .p | resolve($gdir) ]
                       | index($dest) != null) as $seen
                    | if $r.err or ($fatal | any(. as $l | ($cl.repo != null and ($l | contains($cl.repo)))
                                               or ($l | contains("'" + $cl.dest + "'"))
                                               or ($dest != null and ($l | contains("'" + $dest + "'")))))
                      then (if $dest == null then empty else {failed: $dest} end)
                      elif $dest == null or (($cl.explicit or ($cl.cdirs | length) > 0) and ($c.input.command | dynamic)) then {unresolved: 1}
                      else {clone: {dest: $dest, repo: $cl.repo, seen: $seen}} end
                  end
              elif $r.err then empty
              else
                read_of as $rd
                | if ($rd.paths | length) == 0 then empty
                  elif $rd.search and ($rd.doconly or ($r.text | blank)) then empty
                  else $rd.paths[] | resolve($dir) | select(. != null) | {read: .} end
              end)
        | .[]
      elif $r.err then empty
      elif $c.name == "Read" then
        ($c.input.file_path | resolve($c.base)) | select(. != null) | {read: .}
      elif $c.name == "Grep" then
        if ($r.text | grep_tool_empty)
           or ([ $c.input.glob, $c.input.type ] | map(select(. != null and . != "")) as $f
               | ($f | length) > 0 and all($f[]; doc_filter))
        then empty
        else (($c.input.path // ".") | resolve($c.base)) | select(. != null) | {read: .} end
      else empty end ]
| {clones: [ .[] | .clone // empty ],
   failedClones: [ .[] | .failed // empty ],
   unresolvedClones: ([ .[] | .unresolved // empty ] | add // 0),
   reads: [ .[] | .read // empty ]}
