# The sloprail plugin

This plugin is the whole of what is Claude-Code-specific about sloprail. It maps
that harness's lifecycle names onto `sr-session`'s subcommands, so nothing inside
the engine has to know whose lifecycle it is running under.

It also ships guardrails — see `guardrails/` — and the mechanism that puts them
into force is the subject of this file, because `hooks.json` is JSON and cannot
carry the argument in a comment.

## How a shipped guardrail is found

The engine discovers guardrails in two places: `.sloprail/guardrails/` under the
project, and every directory named in `SR_PLUGIN_DIRS`. That variable is the
entire interface, and the engine defines it — see `guardrail.PluginDirsEnv`.

Each hook here sets it to `${CLAUDE_PLUGIN_ROOT}`, which Claude Code substitutes
into the command string before running it. So a session with this plugin
installed runs, in effect:

    SR_PLUGIN_DIRS="/path/to/this/plugin" sr-session pre-tool

and the engine loads `guardrails/authoring-slop/` from inside the installation,
without the project copying anything.

### Why the variable is set here and not read there

Reading `~/.claude/plugins/installed_plugins.json` from the engine would work
today and is the thing this design refuses. It would put three Claude Code facts
— the manifest's path, its JSON shape, and the cache layout — inside a program
whose premise is not knowing which harness it is under, and it would make every
other harness a special case somebody has to add.

Inverting it costs one line per hook and buys the property outright: a plugin
layer for a different harness sets the same variable to wherever that harness
unpacked its plugins, and precedence, disabling and attribution all work
unchanged. Nothing in the engine can tell which harness filled it in.

This file is the right home for that knowledge because this file is already
Claude Code's format, describing Claude Code's lifecycle, installed by Claude
Code's marketplace. It is the one place where being harness-specific is correct.

### Why it appends rather than assigns

    SR_PLUGIN_DIRS="${CLAUDE_PLUGIN_ROOT}${SR_PLUGIN_DIRS:+:$SR_PLUGIN_DIRS}"

A plain assignment would work while sloprail is the only plugin shipping
guardrails, and would silently break the moment a second one did — the second
plugin's hooks would overwrite the first's directory, and whichever ran last
would be the only one whose rules were in force. Nobody would see an error; the
other plugin's rules would simply stop existing.

Prepending its own root and keeping whatever is already there means N plugins
compose, and the `:+` form leaves no trailing colon when the variable was unset
(an empty entry would be a path the engine has to skip, which it does, but
producing garbage and relying on the reader to tolerate it is how the tolerance
becomes load-bearing).

Order is precedence: earlier entries win a name collision. Each plugin putting
itself first means a plugin's own rules take precedence over those of plugins
listed after it, and the PROJECT's rules take precedence over all of them.

## A shipped hook's dependencies

A guardrail's hook command runs with its working directory set to the guardrail's
own folder, which for a shipped rule is inside the installation — so
`./check-rules.sh` resolves to the copy that was installed, and a script can read
what sits beside it.

What it must NOT do is assume anything on the consumer's `$PATH` silently.
`check-rules.sh` is POSIX `sh`, `grep` and `jq`; the first two are on any system
running Claude Code, and `jq` is the one real dependency.

So the script CHECKS for it, by name, as its first act — before any use of it.
That check is not defensive padding, and the reason is specific to how the script
reads its input: every `jq` call is `2>/dev/null`-suppressed, so on a machine
without `jq` the extracted path comes back empty and the next line reports "the
event named no path". That message blames the consumer's event for the plugin's
missing dependency and sends them to debug the engine. The explicit check turns a
misleading failure into a diagnosable one that names the plugin, names `jq`, and
gives the line that switches the rule off.

It refuses rather than permitting, per the rule this guardrail is itself about: a
hook that could not check has not approved. The consumer is blocked, that is the
plugin's fault, and the message says so.

## Switching a shipped rule off

`enabled: false` lives in the declaration, which a consumer does not own — and an
edit inside the install cache is undone by the next reinstall. So a consumer
disables a shipped rule from their own side, in `.sloprail/config.yaml`:

    disabled:
      - sloprail/authoring-slop

The name is qualified by the plugin, so disabling a shipped rule cannot also
disable a project rule that happens to share its name.
