# writers.jq — which tool calls could have written a file, read off the
# engine's own events for them (`sr-session trajectory normalize --events
# PreCommandInvoke,PreFileCreate,PreFileUpdate`), never a regex over the raw
# command: `cat NOTES.md 2>/dev/null` has a `>` and writes nothing,
# `cd node-retry` has a `node` and runs nothing.
#
# Two kinds of call:
#   named    the engine derived a file write to the path from it (the Write
#            and Edit tools, a redirect, tee, sed -i, cp/mv/rsync, a literal
#            eval, …) — the call is the file's writer.
#   unnamed  it runs something whose writes the engine cannot see: an
#            interpreter with code or a script (python/node/perl/ruby/…; not
#            `node --version`), a shell running a script file (`bash w.sh`,
#            `./w.sh`, `tools/gen`), `eval` of a word it cannot read, or a tool
#            that writes from input it does not name (patch, dd, git apply).
# Git bringing in committed content (merge, pull, checkout, switch, restore,
# stash, rebase, cherry-pick, reset, am, revert) is neither: it is not the
# agent writing a proposal, and the engine derives no file write from it.

# The trajectory entry names $p (a project-relative path, lower-cased) as a
# file it writes.
def names_write($p):
  any(.events[]?; (.kind | IN("PreFileCreate", "PreFileUpdate"))
      and ((.path // "") | ascii_downcase | . == $p or endswith("/" + $p)));

def interpreter: test("^(python[0-9.]*|pypy[0-9.]*|node(js)?|perl[0-9.]*|ruby|php|deno|bun|osascript|rscript|lua|tclsh|awk|gawk)$"; "i");
def shell: IN("sh", "bash", "zsh", "dash", "ksh", "fish");

# One invocation that could write without naming what it writes.
def unnamed_writer:
  .bin as $b | (.argv // []) as $a
  | ($a[1:] | map(select(. != ""))) as $args
  | if ($b | interpreter) then
      # awk only with a program that could write (a print redirection or
      # system()); an interpreter with nothing but a version/help flag runs
      # nothing.
      if ($b | IN("awk", "gawk")) then ($args | any(test(">|system\\s*\\(")))
      else ($args | map(select(IN("--version", "-V", "-v", "--help", "-h") | not)) | length) > 0 end
    elif ($b | shell) then
      # A script file: a first operand, with no -c payload (that payload is
      # parsed, and its own invocations are judged on their own).
      (($a[1:] | any(test("^-[a-zA-Z]*c[a-zA-Z]*$"))) | not)
      and ($args | map(select(startswith("-") | not)) | length) > 0
    elif $b == "eval" then
      # A literal payload is parsed (its programs are separate invocations);
      # one the engine could not read arrives as a bare or empty word.
      ($a | length) == 1 or ($a[1:] | any(. == ""))
    elif ($b | IN("patch", "dd")) then true
    elif $b == "git" then ($args | map(select(startswith("-") | not)) | .[0]) == "apply"
    else
      # A program run by path that is not a system tool: ./gen, tools/gen.
      (($a[0] // "") | test("/")) and (($a[0] // "") | test("^/(usr|bin|sbin|opt|System|Library|nix)/") | not)
    end;

def runs_unnamed_writer:
  any(.events[]?; .kind == "PreCommandInvoke" and any(.invocations[]?; unnamed_writer));
