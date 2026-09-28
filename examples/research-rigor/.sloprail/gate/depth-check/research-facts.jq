# research-facts.jq — what ONE trajectory's research did, read off
# `sr-session trajectory normalize` output (an array of normalized entries).
#
# Emits {clones, failedClones, unresolvedClones, reads}:
#   clones            [{dest, repo}] — `git clone`s that did not visibly fail
#                     and whose destination directory is known (absolute,
#                     canonical); repo is the repository keyed as paths.jq's
#                     repokey spells it. Whether the clone really happened is
#                     NOT decided from its output here (the agent controls
#                     that — `echo "Cloning into …"`): verify-depth.sh reads
#                     git's own record of it, <dest>/.git/logs/HEAD.
#   failedClones      [dest] — `git clone`s that failed (an error result, or a
#                     `fatal:` naming them): a directory already there is not
#                     this run's clone
#   unresolvedClones  how many `git clone`s ran whose destination could not be
#                     placed (a directory named through a variable, or after a
#                     `cd` the engine could not resolve)
#   reads             [path] — files or directories whose CONTENT a successful
#                     tool call read: the Read tool, the Grep tool, and shell
#                     readers (cat, head, tail, sed, awk, grep, rg, …). A
#                     search counts only if it printed matching lines — not a
#                     count, a file list or nothing — and was not restricted
#                     to documentation files.
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

# canon, repokey — shared with the verdict (run with -L this directory).
include "paths";

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

# The options that make a search print no matching lines — a count, a list of
# file names, or nothing (just an exit status) — per program. A search run that
# way read no content, whatever it matched. Short letters, then long names.
def nocontent: {
  grep: [["c", "l", "L", "q"], ["--count", "--files-with-matches", "--files-without-match", "--quiet", "--silent"]],
  rg: [["c", "l", "q"], ["--count", "--count-matches", "--files-with-matches", "--files-without-match", "--quiet", "--files"]],
  ag: [["c", "l", "L"], ["--count", "--files-with-matches", "--files-without-matches"]]
} | .egrep = .grep | .fgrep = .grep;

# One option's value, folded into the operand scan: -e/-f give the pattern (or
# program), so the first operand is a file; an include filter is recorded.
def optval($o; $v):
  (if ($o | IN("-e", "-f", "--regexp", "--file", "--expression")) then .given = true else . end)
  | (if ($o as $x | include_opts | index($x)) then .incl += [$v] else . end);

# A cluster of short options — `-rn`, `-A3`, `-rnefoo`, `-tmd`. Each letter is
# its own option until one that takes a value, which takes the rest of the word
# (or, when nothing is left, the next word).
def short_cluster($t; $valued; $quiet):
  ($t[1:] | explode | map([.] | implode)) as $cs
  | reduce range(0; $cs | length) as $i (. + {stop: false};
      if .stop then .
      else ("-" + $cs[$i]) as $o
      | if ($valued | index($o)) != null then
          ($cs[$i + 1:] | join("")) as $rest
          | (if $rest == "" then .skip = $o else optval($o; $rest) end)
          | .stop = true
        else (if ($cs[$i] | IN("r", "R")) then .recursive = true else . end)
          | (if ($quiet[0] | index($cs[$i])) != null then .nocontent = true else . end)
        end
      end)
  | del(.stop);

# argv → the operands left once options (and their values) are set aside;
# whether a pattern/program was given by an option (-e/-f), in which case the
# first operand is a file rather than the pattern; and any include filters.
def operands($valued; $quiet):
  reduce .[1:][] as $t ({ops: [], skip: null, dd: false, given: false, recursive: false, incl: [], nocontent: false};
    if .skip != null then optval(.skip; $t) | .skip = null
    elif .dd then .ops += [$t]
    elif $t == "--" then .dd = true
    elif ($t | startswith("--")) then
      ($t | sub("=.*$"; "")) as $name
      | if ($t | contains("=")) then optval($name; $t | sub("^[^=]*="; ""))
        elif ($valued | index($name)) != null then .skip = $name
        elif ($name | IN("--recursive", "--dereference-recursive")) then .recursive = true
        elif ($quiet[1] | index($name)) != null then .nocontent = true
        else . end
    elif ($t | startswith("-")) and ($t | length) > 1 then short_cluster($t; $valued; $quiet)
    else .ops += [$t] end);

# One invocation of a reader → {paths, search, doconly, nocontent}: the paths
# (as written) whose content it reads, whether it is a search (which reads only
# what it prints), whether its include filters name documentation alone, and
# whether it was told to print no matching lines (-c, -l, -q, …). No paths
# for a program that is not a reader. A search with no path searches where it
# runs, returned as ".".
def read_of:
  .bin as $bin
  | (readers[$bin]) as $valued
  | if $valued == null then {paths: []}
    else (.argv | operands($valued; nocontent[$bin] // [[], []])) as $o
    | ($bin | IN("grep", "egrep", "fgrep", "rg", "ag")) as $search
    | {search: $search, nocontent: $o.nocontent,
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

# Whether one reader invocation shows only a GLIMPSE of what it reads — less
# than about 50 lines: `head -n 3`, `head -c 1`, `head -5`, `tail -n 10`, a
# sed printing a short range or one line (`sed -n 1p`, `sed -n '1,20p'`), a
# search capped at a few matches (`grep -m1`). `head -n 80`, `tail -n +5`,
# `sed -n '1,200p'`, `grep -m 100` show enough to count as reads. Such a
# glimpse is still credited by the depth gate — how much a read showed cannot
# be told apart per file (see the README) — but it is reported, so the eval
# does not take it for research.
def glimpse_lines: 50;
def glimpse_bytes: 2000;
# The value of a counting option in argv: `-n 3`, `-n3`, `--lines=3`, `-3`.
def count_of($short; $long):
  . as $a
  | [ range(0; $a | length) as $i
      | $a[$i] as $t
      | if $t == $short then ($a[$i + 1] // "")
        elif ($t | startswith($short)) and ($t | length) > ($short | length) then $t[($short | length):]
        elif ($t | startswith($long + "=")) then $t[($long | length) + 1:]
        else empty end ] | last;
def small($n; $limit): ($n // "") | test("^[0-9]+$") and tonumber < $limit;
def partial_read:
  .bin as $b | (.argv[1:] // []) as $a
  | if ($b | IN("head", "tail")) then
      small($a | count_of("-n"; "--lines"); glimpse_lines)
      or small($a | count_of("-c"; "--bytes"); glimpse_bytes)
      or ($a | any(test("^-[0-9]+$") and (.[1:] | tonumber) < glimpse_lines))
    elif $b == "sed" then
      if ($a | any(test("^-[a-zA-Z]*n[a-zA-Z]*$|^--(quiet|silent)$")) | not) then false
      else ($a | map(select(startswith("-") | not)) | .[0] // "") as $script
        | ([$script | capture("^(?<from>[0-9]+)(,(?<to>[0-9]+))?p$")] | first) as $r
        # A numeric range is measured; any other script that prints only
        # what it is told to (`/re/p`) is taken as a glimpse.
        | if $r == null then true
          else ((($r.to // $r.from) | tonumber) - ($r.from | tonumber) + 1) < glimpse_lines end
      end
    elif ($b | IN("grep", "egrep", "fgrep", "rg", "ag")) then
      small(($a | count_of("-m"; "--max-count")); glimpse_lines)
    else false end;

# Whether a tool result shows nothing: a search that printed nothing read
# nothing. Claude Code says so in words — "(Bash completed with no output)",
# the Grep tool's "No files found" — and appends a note when a `cd` in the
# command was undone ("Shell cwd was reset to …"), which is not output either.
def blank:
  split("\n") | map(select(test("^Shell cwd was reset to ") | not)) | join("\n")
  | (test("\\S") | not) or test("^\\s*\\((Bash|Read) completed with no output\\)\\s*$");
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
                    # git drops a destination's trailing slashes when it quotes
                    # it (`dest/` → 'dest').
                    | if $r.err or ($fatal | any(. as $l | ($cl.repo != null and ($l | contains($cl.repo)))
                                               or ($l | contains("'" + ($cl.dest | sub("(?<k>.)/+$"; "\(.k)")) + "'"))
                                               or ($dest != null and ($l | contains("'" + $dest + "'")))))
                      then (if $dest == null then empty else {failed: $dest} end)
                      elif $dest == null or (($cl.explicit or ($cl.cdirs | length) > 0) and ($c.input.command | dynamic)) then {unresolved: 1}
                      else {clone: {dest: $dest, repo: ($cl.repo | repokey($gdir))}} end
                  end
              elif $r.err then empty
              else
                read_of as $rd
                | if ($rd.paths | length) == 0 then empty
                  elif $rd.nocontent then empty
                  elif $rd.search and ($rd.doconly or ($r.text | blank)) then empty
                  else (partial_read) as $part
                    | $rd.paths[] | resolve($dir) | select(. != null) | {read: ., partial: $part} end
              end)
        | .[]
      elif $r.err then empty
      elif $c.name == "Read" then
        ($c.input.file_path | resolve($c.base)) | select(. != null)
        # A Read with a small limit is a glimpse — unless what it showed ran to
        # the file's end (its last numbered line), which verify-depth checks
        # against the file; with no limit, or a limit of 50 or more, it counts.
        | {read: .,
           partial: (($c.input.limit // null) as $l | $l != null and small($l | tostring; glimpse_lines)),
           last: ($r.text | [splits("\n") | capture("^\\s*(?<n>[0-9]+)\\t")? | .n | tonumber] | last)}
      elif $c.name == "Grep" then
        # The Grep tool's default output_mode is files_with_matches: file
        # names, no content. Only "content" shows what a file says.
        if ($c.input.output_mode // "files_with_matches") != "content"
           or ($r.text | grep_tool_empty)
           or ([ $c.input.glob, $c.input.type ] | map(select(. != null and . != "")) as $f
               | ($f | length) > 0 and all($f[]; doc_filter))
        then empty
        else (($c.input.path // ".") | resolve($c.base)) | select(. != null) | {read: .} end
      else empty end ]
| {clones: [ .[] | .clone // empty ],
   failedClones: [ .[] | .failed // empty ],
   unresolvedClones: ([ .[] | .unresolved // empty ] | add // 0),
   reads: [ .[] | .read // empty ],
   fullReads: [ .[] | select(.read != null and (.partial | not)) | .read ],
   partialEnds: [ .[] | select(.read != null and .partial and .last != null) | {path: .read, last} ]}
