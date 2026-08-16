# The sloprail plugin

This plugin is the whole of what is Claude-Code-specific about sloprail. It maps
that harness's lifecycle names onto `sr-session`'s subcommands, so nothing inside
the engine has to know whose lifecycle it is running under.

It also ships guardrails — see `.sloprail/guardrails/` — and this file records what a
reader of `hooks.json` would otherwise have to guess, since JSON carries no
comments: how a shipped rule reaches a project, and why that no longer has
anything to do with the hooks below it.

## How a shipped guardrail is found

Nothing in this file makes it happen, and that is the point.

The engine reads the PROJECT's `.claude/settings.json` and
`.claude/settings.local.json`, takes whatever `enabledPlugins` turns on, and
resolves each plugin to its installation directory itself. A project that has
enabled `sloprail@sloprail-marketplace` gets `.sloprail/guardrails/authoring-slop/` from
inside this installation, without copying anything and without this plugin
telling the engine where it lives.

So `hooks.json` maps lifecycle names to subcommands and does nothing else:

    sr-session pre-tool

### Why discovery is not done from here

An earlier design had each hook pass its own `${CLAUDE_PLUGIN_ROOT}` to the
engine in an environment variable. It kept the engine free of any Claude Code
paths, and it was wrong for a reason no amount of isolation fixes: **it only
discovers a plugin that fired a hook.**

A plugin that ships guardrails and registers no hooks would be invisible. Worse,
discovery became a property of what happened to RUN rather than of what the repo
INSTALLED — and those are different sets. The repo is the thing that made the
decision to install a plugin, and it wrote that decision down in its settings, so
the settings are the answer to "which plugins are in force here". Anything else
is an inference that is sometimes right.

The Claude Code knowledge that this buys — the settings filenames, the
`enabledPlugins` shape, the cache layout, the manifest schema — is real, and it
is quarantined in one file named for what it is:
`internal/harness/claudecode.go`. A second harness gets a second file beside it,
exactly as `internal/transcript/claudecode.go` has always been the only place
naming Claude Code's JSONL spellings.

And because those assumptions can go stale, an enabled plugin that cannot be
located is REPORTED by name, at every hook point, rather than skipped. A schema
move produces "enabled plugin X could not be located" instead of guardrails that
quietly stop firing — which is the failure this product exists to prevent, and
would be an embarrassing one to ship inside it.

### Precedence

The project's own rules win over any plugin's, and between plugins the order is
the settings key order — stable, and predictable from something the consumer can
see. A displaced rule is reported rather than silently dropped.

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
