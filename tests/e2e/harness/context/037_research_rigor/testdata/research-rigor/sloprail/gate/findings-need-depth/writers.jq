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
def only_version_or_help: length > 0 and (map(select(IN("--version", "-V", "-v", "--help", "-h") | not)) | length == 0);

# Build and task runners run whatever their recipe says.
def runner($a):
  ($a[1:] | map(select(startswith("-") | not))) as $ops
  | (.bin as $b
     | if ($b | IN("make", "gmake", "just", "task", "rake", "tox", "nox", "npx", "pnpx", "bunx")) then true
       elif ($b | IN("npm", "pnpm", "yarn", "bun")) then
         ($ops[0] // "") | IN("run", "run-script", "exec", "x", "start", "test", "dlx", "")
                          or ($b == "yarn" and (IN("add", "install", "remove", "upgrade", "info", "list", "why", "config", "cache") | not))
       elif ($b | IN("cargo", "go", "deno", "uv", "poetry", "pipx", "gradle", "gradlew", "mvn", "dotnet")) then
         ($ops[0] // "") | IN("run", "task", "exec", "test", "script")
       else false end);

# One invocation that could write without naming what it writes. $githooks:
# the repository has an executable git hook, so any git command may run it.
def unnamed_writer($githooks):
  .bin as $b | (.argv // []) as $a
  | ($a[1:] | map(select(. != ""))) as $args
  | if ($b | IN("awk", "gawk")) then
      # awk only with a program that could write (a print redirection or
      # system()).
      ($args | any(test(">|system\\s*\\(")))
    elif ($b | interpreter) then
      # With code, a script — or nothing, when it reads its program from
      # stdin (`python3 <<EOF`, `cat w.py | python3`). Only a version or help
      # flag runs nothing.
      ($a[1:] | only_version_or_help) | not
    elif ($b | shell) then
      # A -c payload the engine could read is parsed, and its programs are
      # judged on their own; an unreadable one (`bash -c "$P"`) is not. A
      # script file, or a program read from stdin (`sh < w.sh`, `xargs sh`),
      # always could write.
      (($a[1:] | map(test("^-[a-zA-Z]*c[a-zA-Z]*$"))) | index(true)) as $ci
      | if $ci == null then (($a[1:] | only_version_or_help) | not)
        else (($a[$ci + 2] // "") == "") end
    elif $b == "eval" then
      # A literal payload is parsed (its programs are separate invocations);
      # one the engine could not read arrives as a bare or empty word.
      ($a | length) == 1 or ($a[1:] | any(. == ""))
    elif ($b | IN("patch", "dd")) then true
    elif $b == "git" then
      # The subcommand, past git's own options (-C dir, -c k=v, …).
      ([ range(0; $args | length) as $i | $args[$i] as $t
         | select(($t | startswith("-") | not)
                  and ((if $i > 0 then $args[$i - 1] else "" end) | IN("-C", "-c", "--git-dir", "--work-tree") | not))
         | $t ] | .[0] // "") as $sub
      | $sub == "apply"
        # An executable hook runs only on the subcommands that fire hooks —
        # never on diff, log, status or show.
        or ($githooks and ($sub | IN("commit", "merge", "pull", "rebase", "checkout", "switch", "am",
                                     "cherry-pick", "revert", "push", "clone", "worktree")))
    elif runner($a) then true
    else
      # A program run by path that is not a system tool: ./gen, tools/gen.
      (($a[0] // "") | test("/")) and (($a[0] // "") | test("^/(usr|bin|sbin|opt|System|Library|nix)/") | not)
    end;
def unnamed_writer: unnamed_writer(false);

def runs_unnamed_writer($githooks):
  any(.events[]?; .kind == "PreCommandInvoke" and any(.invocations[]?; unnamed_writer($githooks)));
def runs_unnamed_writer: runs_unnamed_writer(false);

# A call that leaves something running past its own end: `cmd &`, nohup,
# setsid, disown, or the Bash tool's own run_in_background. What it writes may
# land in a LATER cycle, with nothing of that cycle to show for it.
def starts_background:
  any(.message.content[]?; .type == "tool_use"
      and (.input.run_in_background == true
           or ((.input.command // "") | test("(^|[^&|>])&[ \t]*($|;|\\)|\\n)|\\b(nohup|setsid|disown)\\b"))));

# A background start that could itself write the file: it names the path, or
# it runs something that writes unseen. `sleep 1 &` could not.
# A job handed to a scheduler — at, batch, crontab, launchctl, systemd-run —
# runs later from text the engine does not read: it could write anything.
def schedules_job:
  any(.events[]?; .kind == "PreCommandInvoke"
      and any(.invocations[]?; .bin | IN("at", "batch", "crontab", "launchctl", "systemd-run")));

def starts_background_writer($p; $githooks):
  (starts_background and (names_write($p) or runs_unnamed_writer($githooks))) or schedules_job;
