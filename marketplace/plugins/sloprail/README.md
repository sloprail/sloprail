# The sloprail plugin

This plugin is the whole of what is Claude-Code-specific about sloprail. It maps
that harness's lifecycle names onto `sr-session`'s subcommands, so nothing inside
the engine has to know whose lifecycle it is running under.

It also ships guardrails — new-format ones under `.sloprail/file-guard/` (and,
when it grows them, `.sloprail/gate/` and `.sloprail/context/`), the same layout
a project uses for its own — and this file records what a reader of `hooks.json`
would otherwise have to guess, since JSON carries no comments: how a shipped rule
reaches a project, and why that no longer has anything to do with the hooks below
it.

## How a shipped guardrail is found

Nothing in this file makes it happen, and that is the point.

The engine reads the PROJECT's `.claude/settings.json` and
`.claude/settings.local.json`, takes whatever `enabledPlugins` turns on, and
resolves each plugin to its installation directory itself. The nature loader then
reads each enabled plugin's own `.sloprail/` the same way it reads the project's
(see `internal/declaration` `NewWithPlugins` and `pluginDotDir`). A project that
has enabled `sloprail@sloprail-marketplace` gets
`.sloprail/file-guard/authoring-slop/` from inside this installation, without
copying anything and without this plugin telling the engine where it lives.

So `hooks.json` maps lifecycle names to subcommands and does nothing else,
through a small wrapper (`hooks/sr-session-hook.sh`, resolved via
`${CLAUDE_PLUGIN_ROOT}` since a plugin's hook command is not run from its own
directory) that exists for exactly one reason: `/plugin install` registers
these hooks whether or not the `sr*` binaries are anywhere on the consumer's
machine, and calling `sr-session` bare made that gap silent — the session ran
completely unguarded, no error, no warning. So the wrapper checks for
`sr-session` first. If it is missing, `start` installs it: the release matching
this plugin's version, checksum-verified, via the `hooks/install.sh` copy of
the repository's installer, announced in the session (`SLOPRAIL_NO_AUTO_INSTALL=1`
opts out). If it is still missing, `pre-tool` refuses file writes with the
install command (exit 2, the only code Claude Code treats as a refusal) and
lets Bash through so it can be installed, and `start`/`stop`/`subagent-stop`/`worktree-remove`
warn rather than brick the session outright. See the script's own header
comment for the full reasoning, and
`../../../docs/getting-started/install.mdx` for the consumer-facing install
sequence this exists to make unmissable if it's ever skipped.

    "${CLAUDE_PLUGIN_ROOT}/hooks/sr-session-hook.sh" pre-tool

### Why discovery is not done from here

**This `${CLAUDE_PLUGIN_ROOT}` use is unrelated to the one rejected below** — it
only locates a file inside this plugin's own installation to exec, and is never
passed to the engine or used to discover anything. The design rejected here is
a different one: an earlier attempt had each hook pass `${CLAUDE_PLUGIN_ROOT}`
*to the engine* in an environment variable, as the mechanism for finding a
plugin's *guardrails*. It kept the engine free of any Claude Code paths, and it
was wrong for a reason no amount of isolation fixes: **it only discovers a
plugin that fired a hook.**

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
      - sloprail/file-guard/authoring-slop

The name is qualified by the plugin AND the nature (`<plugin>/<nature>/<name>`),
so disabling a shipped rule cannot also disable a project rule that happens to
share its name — nor a differently-natured rule of the same name.

## Opt-in rules

Three gates ship off (`enabled: false`) and a project turns them on from its own
`.sloprail/config.yaml`:

    enabled:
      - sloprail/gate/judge-before-push
      - sloprail/gate/no-merge-over-refusals
      - sloprail/gate/no-destroying-owed-work

`no-merge-over-refusals` refuses `gh pr merge` (and a push to the default branch) over
refusals or unjudged commits; `no-destroying-owed-work` refuses the git commands that
would destroy such commits (`branch -D`, `worktree remove`, hard resets, prunes).

## Changes to a project's rules

`sloprail/file-guard/grounded-rule-changes` (and a `PreFileWrite` gate of the same name on
`.sloprail/config.yaml`) judge a change to what already stands in the project's
`.sloprail/`, in the commit's `Sloprail-Cites-User:` / `Sloprail-Cites-Tool:` trailers.
The rule's folder has the full account, and how to switch it off:

    disabled:
      - sloprail/file-guard/grounded-rule-changes
      - sloprail/gate/grounded-rule-changes
