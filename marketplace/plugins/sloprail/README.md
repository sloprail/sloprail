# The sloprail plugin

This plugin is the whole of what is harness-specific about sloprail's hooks. It
maps a harness's lifecycle names (Claude Code, Cursor and Codex each spell them
their own way) onto `sr-session`'s subcommands, so nothing inside the engine has
to know whose lifecycle it is running under. What holds for every harness comes
first; what holds for only one is under its own heading.

It also ships guardrails — new-format ones under `.sloprail/file-guard/` (and,
when it grows them, `.sloprail/gate/` and `.sloprail/context/`), the same layout
a project uses for its own — and this file records what a reader of the hook
manifests would otherwise have to guess, since JSON carries no comments: how a
shipped rule reaches a project, and why that no longer has anything to do with
the hooks below it.

## How a shipped guardrail is found

Nothing in the hook manifests makes it happen, and that is the point.

The engine asks the running harness's adapter (`internal/harness`) which plugins
are enabled for the project and where each is installed, then the nature loader
reads each enabled plugin's own `.sloprail/` the same way it reads the project's
(see `internal/declaration` `NewWithPlugins` and `pluginDotDir`). A project that
has enabled the sloprail plugin gets `.sloprail/file-guard/authoring-slop/` from
inside this installation, without copying anything and without this plugin
telling the engine where it lives.

So the hook manifests map lifecycle names to subcommands and do nothing else,
through a small wrapper (`hooks/sr-session-hook.sh`; a plugin's hook command is
not run from its own directory, so each manifest locates the wrapper through its
harness's plugin-root variable or working directory) that exists for exactly one
reason: installing the plugin registers these hooks whether or not the `sr*`
binaries are anywhere on the consumer's machine, and calling `sr-session` bare
made that gap silent — the session ran completely unguarded, no error, no
warning. So the wrapper checks for `sr-session` first. If it is missing, `start`
installs it: the release matching this plugin's version, checksum-verified, via
the `hooks/install.sh` copy of the repository's installer, announced in the
session (`SLOPRAIL_NO_AUTO_INSTALL=1` opts out). If it is still missing,
`pre-tool` refuses file writes with the install command (exit 2, the refusal
code every supported harness honours) and lets Bash through so it can be
installed, and `start`/`stop`/`subagent-stop`/`worktree-remove` warn rather than
brick the session outright. See the script's own header comment for the full
reasoning, and `../../../docs/getting-started/install.mdx` for the consumer-facing
install sequence this exists to make unmissable if it's ever skipped.

### Per harness

**Claude Code.** `hooks/hooks.json` carries the lifecycle mapping, and each
command is `"${CLAUDE_PLUGIN_ROOT}/hooks/sr-session-hook.sh" <subcommand>`. The
engine reads the PROJECT's `.claude/settings.json` and
`.claude/settings.local.json`, takes whatever `enabledPlugins` turns on, and
resolves each plugin to its installation directory itself. The manifest is
`.claude-plugin/plugin.json`. This Claude Code knowledge — the settings
filenames, the `enabledPlugins` shape, the cache layout, the manifest schema — is
quarantined in `internal/harness/claudecode/plugins.go`.

**Cursor.** `hooks/cursor-hooks.json` carries the mapping for every event except `stop` and `sessionStart`, which a Cursor plugin cannot receive (its `stop` never fires and a local plugin loads after `sessionStart`; measured in harness-mocks #287). Those two are written into the project's `.cursor/hooks.json` by the install (`sr-session project-hooks install`, merged with the entries already there, `remove` to undo; the code is `internal/harness/cursor/projecthooks.go`); its commands run from
the plugin directory and go through `hooks/sr-session-hook-cursor.sh`, which sets
`SLOPRAIL_HARNESS=cursor`, delegates to the shared wrapper, and adapts only what
Cursor's hook contract does differently (`sessionStart` output must be a JSON
document). The manifest is `.cursor-plugin/plugin.json`. Plugin discovery
(`CURSOR_PLUGIN_ROOT`, `~/.cursor/plugins/local`) is in
`internal/harness/cursor/plugins.go`.

**Codex.** The same shape: an adapter package beside the others under
`internal/harness/`, a hook manifest mapping onto the same `sr-session`
subcommands, and the shared wrapper unchanged. Nothing in the engine or in the
rest of this file changes for it.

A harness's quirks live in its adapter package, the way
`internal/transcript/claudecode.go` has always been the only place naming Claude
Code's JSONL spellings.

### Why discovery is not done from the hook

**The plugin-root variable a manifest uses to find the wrapper is unrelated to
the design rejected here** — it only locates a file inside this plugin's own
installation to exec, and is never passed to the engine or used to discover
anything. The rejected design is a different one: an earlier attempt had each
hook pass its plugin's root (`${CLAUDE_PLUGIN_ROOT}` in Claude Code) *to the
engine* in an environment variable, as the mechanism for finding a plugin's
*guardrails*. It kept the engine free of any harness paths, and it was wrong for
a reason no amount of isolation fixes: **it only discovers a plugin that fired a
hook.**

A plugin that ships guardrails and registers no hooks would be invisible. Worse,
discovery became a property of what happened to RUN rather than of what the repo
INSTALLED — and those are different sets. The repo is the thing that made the
decision to install a plugin, and it wrote that decision down in its harness
configuration, so that configuration is the answer to "which plugins are in force
here". Anything else is an inference that is sometimes right. (Where a harness
records nothing a hook can read, as with Cursor's marketplace installs, the
adapter says what it cannot know instead of guessing.)

And because those assumptions can go stale, an enabled plugin that cannot be
located is REPORTED by name, at every hook point, rather than skipped. A schema
move produces "enabled plugin X could not be located" instead of guardrails that
quietly stop firing — which is the failure this product exists to prevent, and
would be an embarrassing one to ship inside it.

### Precedence

The project's own rules win over any plugin's, and between plugins the order is
the order the harness lists them — stable, and predictable from something the consumer can
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

## Judging file-guards

File-guards are judged over an explicit range, never by the Stop hook:

    sr-checks run    --base origin/main --head HEAD   # runs what has no stored verdict; stores and pushes
    sr-checks verify --base origin/main --head HEAD   # only reads stored verdicts, runs nothing; exit 1 on anything failing or unjudged

A verdict is also reused when a range's base and head trees equal those of a range already judged (a squash merge of a verified, up-to-date PR); if main moved under the PR, run `sr-checks run --base <before> --head <after>` for the push.

Every check (script, judge, requirement) is cached; a verdict per guard and subject is keyed by content (rule, check, subject, fingerprint; not the rule's own definition), not by
commit or session, and kept on the orphan branch `sloprail/checks` on `origin`, so a
rebase, another clone or CI reads the same results. The Stop hook only verifies: it
refuses uncommitted work on guarded paths, then verifies each range the session
tracks (`sr-session refs list|track|untrack`). Nothing is tracked automatically unless
`SR_AUTO_WATCH_GIT_REFS=1` is set in the environment (then a folder's current branch is
tracked on its own); the push gate below (when enabled) does not depend on tracking. CI runs `sr-checks verify` as a required check. Around it, `sloprail/gate/checks-ref-sr-only` ships on, and `sloprail/gate/verify-before-push` ships off: a project opts in with `enabled: [sloprail/gate/verify-before-push]` in `.sloprail/config.yaml`; CI's required `sr-checks verify` is the guarantee, so run `sr-checks run` before a PR is ready. Once enabled, `verify-before-push` refuses an agent's `git push` until
`sr-checks verify` passes for the commits it would send, and `sloprail/gate/checks-ref-sr-only`
refuses any agent git write to the `sloprail/checks` results branch (only `sr-checks` writes
it; reading stays allowed; it guards against an agent's accidental write, not a determined forger, since a ref name the shell builds at run time is not in the command's argv). The push gate sees the command line the agent runs, not the commands inside a script it runs; CI verify is the backstop for those. `checks-ref-sr-only` is switched off under `disabled:` in `.sloprail/config.yaml`. The setup, a
`pre-push` hook and the CI job are in
`skills/authoring-guardrails/file-guard.md`.

## Changes to a project's rules

`sloprail/file-guard/grounded-rule-changes` (and a `PreFileWrite` gate of the same name on
`.sloprail/config.yaml`) judge a change to what already stands in the project's
`.sloprail/`, in the commit's `Sloprail-Cites-User:` / `Sloprail-Cites-Tool:` trailers.
The rule's folder has the full account, and how to switch it off:

    disabled:
      - sloprail/file-guard/grounded-rule-changes
      - sloprail/gate/grounded-rule-changes
