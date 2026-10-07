package main

import (
	"fmt"
	"strings"

	"github.com/sloprail/sloprail/internal/harness/codex"
)

// codexSpec is the OpenAI Codex CLI, run one-shot as `codex exec`.
//
// Sources, per flag (https://developers.openai.com/codex/cli/reference, `codex exec`):
//
//   - `codex exec [flags] PROMPT`, with `-` as the prompt to read it from standard
//     input: the large-prompt path below.
//   - `--model/-m`, `--sandbox/-s read-only|workspace-write|danger-full-access`,
//     `--ephemeral` (no session file is written), `--ignore-user-config` (do not load
//     $CODEX_HOME/config.toml; auth still comes from CODEX_HOME), `--ignore-rules`,
//     `--skip-git-repo-check`, `--add-dir` (extra writable directories), `--disable
//     <feature>`.
//   - hooks are one feature, `hooks` (alias `codex_hooks`; the config key is
//     `[features] hooks = false`, https://developers.openai.com/codex/hooks), and a
//     plugin's hooks run only when its config.toml enables it.
//
// # Isolation
//
// A judge launched by a hook must not load the hooks and plugins that launched it, or
// a rule whose judge is an agent recurses (see claudeCodeSpec). baseArgs does it two
// ways that do not depend on each other: `--ignore-user-config` drops the user-layer
// config.toml, which is where plugins are enabled and a user's hooks are declared,
// and `--disable hooks` turns the hook feature off whatever a project layer says.
// `--ephemeral` keeps the judge's own rollout from being written (and from being
// mistaken for a session by anything that scans the sessions directory).
//
// MEASURED against codex-cli 0.160.1 (ChatGPT login): `codex exec --ignore-user-config
// --disable hooks --ephemeral -s read-only -` runs and answers, and a hook declared
// inline (`-c hooks.SessionStart=[...]`), which DOES fire without those flags, does
// not fire with them. harness-mocks' codex-mock refuses --disable, --ignore-user-config
// and --sandbox (fail-fast-unimplemented), so the mock cannot run a judge; the unit
// tests assert the argv instead. An unknown flag fails loudly (codex exits non-zero),
// the failure that cannot be mistaken for a verdict.
//
// # File access
//
// Codex's confinement is its sandbox, which is coarser than Claude Code's per-path
// permission rules. With no writable directory asked for, the run is `-s read-only`:
// the agent can read the whole disk and write nothing. With one (a --verify answer
// folder, a caller's `--add-dir`), it is `-s workspace-write --add-dir <dirs>`: the
// agent may write those directories AND its working directory, which Claude's grant
// does not allow. A `--add-dir:readonly` needs nothing (read-only is the default for
// everything not added), but a readonly directory INSIDE the working directory stays
// writable under workspace-write. A caller's `--allowed-tools`/`--disallowed-tools`
// are Claude Code tool rules and have no Codex spelling; they are not passed.
var codexSpec = harnessSpec{
	name:   Codex,
	binary: "codex",

	execArgs:         []string{"exec"},
	modelFlag:        "-m",
	stdinPromptAbove: 64 << 10,
	stdinPromptArgs:  []string{"-"},

	// Codex sets CODEX_THREAD_ID and CODEX_SESSION_ID in every shell command it runs
	// and CODEX_CI beside them (recorded: harness-mocks codex-mock/snapshots/runs/
	// subprocess-session-env); a plugin's hook command gets PLUGIN_ROOT.
	detect: func(getenv func(string) string) bool {
		if getenv("SLOPRAIL_HARNESS") == "codex" {
			return true
		}
		// Claude Code's markers win an ambiguous environment (see codex.Detect).
		if getenv("CLAUDECODE") != "" || getenv("CLAUDE_CODE_ENTRYPOINT") != "" || getenv("CLAUDE_CODE_SESSION_ID") != "" {
			return false
		}
		for _, k := range []string{"CODEX_THREAD_ID", "CODEX_SESSION_ID", "CODEX_CI", "CODEX_MANAGED_BY_NPM", "PLUGIN_ROOT"} {
			if getenv(k) != "" {
				return true
			}
		}
		return false
	},

	// The size aliases name models that RUN. Checked on codex 0.160.1 with a ChatGPT
	// login (the account type `codex login` gives a person): the account's model list
	// (models_cache.json) offers gpt-6-luna (fast), gpt-6.1-sol (workhorse) and
	// gpt-6-astra (frontier), and a `codex exec -m <name>` of each answered. The
	// earlier guesses gpt-5.6 and gpt-5.6-pro are REFUSED there ("not supported when
	// using Codex with a ChatGPT account"); gpt-5.6-luna ran but is "older". An API-key
	// account's list differs, so a caller pins a concrete name with --model; a model
	// a Codex release retires is refused by codex, loudly, rather than substituted.
	sizes: map[SizeAlias]string{
		SizeXS:  "gpt-6-luna",
		SizeSM:  "gpt-6-luna",
		SizeMD:  "gpt-6.1-sol",
		SizeLG:  "gpt-6.1-sol",
		SizeXL:  "gpt-6-astra",
		SizeXXL: "gpt-6-astra",
	},

	// A concrete name is one of OpenAI's when it carries their prefix; the harness
	// is the authority on whether it exists (see claudeCodeSpec.offers).
	offers: func(model string) bool {
		return strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "codex-") ||
			strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") || strings.HasPrefix(model, "o4")
	},
	argsFlag: "--codex-args",

	baseArgs: []string{"--ignore-user-config", "--disable", "hooks", "--ephemeral", "--skip-git-repo-check"},

	// The judge's sandbox is as tight as Claude's per-path rules: the first writable
	// directory (under --verify, the answer folder) is the run's WORKING directory and
	// `-s workspace-write` makes only it writable (the others ride on --add-dir); the
	// project is read by absolute path, which is read-only because it is outside every
	// writable root. With nothing writable the run is `-s read-only`. /tmp and $TMPDIR
	// are writable under workspace-write unless switched off, so both are: a project
	// that lives under /tmp (as every test's does) would otherwise be writable.
	//
	// Verified on codex 0.160.1 (gpt-6-luna, ChatGPT login), a judge started in the
	// project with `-C <scratch>`: reading <project>/secret.txt worked; writes to
	// <project>/pwned.txt and /tmp/probe were refused; <scratch>/answer.txt landed.
	// The control, the same run without the two exclude_* settings, WROTE <project>/pwned.txt
	// and /tmp/probe (the project sat under /private/tmp): the settings are what close it.
	//
	// A caller's tools are not rules here: tools (codex.Harness.MapToolRules) maps them
	// to the sandbox's network, the search mode and sub-agents, and refuses the rest.
	grant: func(g accessGrant) []string {
		var writable []string
		for _, d := range g.Dirs {
			if d.Mode == dirWritable {
				writable = append(writable, d.Path)
			}
		}
		if len(writable) == 0 {
			return []string{"--sandbox", "read-only"}
		}
		args := []string{
			"-C", writable[0],
			"--sandbox", "workspace-write",
			"-c", "sandbox_workspace_write.exclude_slash_tmp=true",
			"-c", "sandbox_workspace_write.exclude_tmpdir_env_var=true",
		}
		for _, d := range writable[1:] {
			args = append(args, "--add-dir", d)
		}
		return args
	},

	// A readonly directory that sits inside a writable root is writable anyway (the
	// sandbox has no "except this sub-directory"), so the grant is refused, as Claude's
	// own cannot express its nested-writable case either.
	checkGrant: func(g accessGrant) error {
		for _, r := range g.Dirs {
			if r.Mode != dirReadonly {
				continue
			}
			for _, w := range g.Dirs {
				if w.Mode == dirWritable && within(r.Path, w.Path) {
					return fmt.Errorf("%w: codex: --add-dir:readonly %s lies inside the writable %s, and a sandbox cannot make an exception inside a writable root",
						ErrModeUnsupported, r.Path, w.Path)
				}
			}
		}
		return nil
	},

	tools: codex.Harness{},
}
