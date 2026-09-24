// Command sr is the root sloprail CLI. It owns no behaviour of its own: each
// high-level command is a separate binary, and this dispatches to them.
//
//	sr session ...    → sr-session
//	sr file ...       → sr-file
//	sr mark ...       → sr-mark
//	sr agent ...      → sr-agent
//	sr eval ...       → sr-eval
//
// So `sr session start` and `sr-session start` are the same run of the same
// binary, reached two ways. The proxy adds one exec and changes nothing else:
// stdin, stdout, stderr, signals and the exit status all pass through
// untouched (see internal/subbin.Exec, which explains why the exit status in
// particular has to be exact — it carries the verdict).
//
// WHY A PROXY EXISTS AT ALL, given the services work standalone. It is the one
// name a person has to know. An author who types `sr` and reads the help
// discovers the whole surface without knowing in advance that the commands are
// separate programs, and `sr <tab>` completes across all of them. The services
// are what the machine runs; this is what a human types.
//
// WHY THE HOOKS DO NOT USE IT. The marketplace plugin invokes `sr-session`
// directly rather than `sr session` — see marketplace/plugins/sloprail/hooks/hooks.json.
// A hook runs on every tool call, and going through the proxy would add a
// process spawn to each one to save nothing: hooks.json is generated wiring
// that no person reads for discovery, which is the only thing the proxy buys.
// The proxy is for people; the hooks name the service.
//
// UNKNOWN COMMANDS ARE NOT FORWARDED. Only the commands listed above dispatch.
// A typo'd `sr sessoin` is an error from cobra naming the real commands, not an
// attempt to exec `sr-sessoin` and a confusing not-found from the OS.
//
// NO SHARED CONFIG LAYER, AND NO VIPER. a10n's proxy reads viper for settings
// its sub-binaries share (`server.auth.type` and the rest), so the question of
// copying that came up with the split. It buys sloprail nothing today, and the
// reason is structural rather than a matter of taste.
//
// What passes between these binaries is per-invocation SCOPE, not settings:
// SR_GUARDRAIL, SR_SESSION_ID, SR_WORKSPACE, SR_TRANSCRIPT. Every one of them
// differs per hook call, is set by the engine immediately before the exec, and
// is deliberately NOT something a user may configure — SR_WORKSPACE is appended
// after the inherited environment precisely so an outer value cannot override
// the engine's answer (see services/sr-session/hookenv.go). A config file cannot
// hold values like these, so viper would sit alongside the real mechanism
// rather than replace it.
//
// There is also nothing left for it to hold. `sloprail init` was deleted
// because a file containing only defaults is a file to keep valid for no
// return, and the split did not create a setting: the ONE thing a user might
// point at — where the sibling binaries live — is SLOP_SUBBIN_DIR, which must
// be an env var because it has to be set by whoever launches the process (a
// test harness pointing at a temp build), and which is unset in every normal
// install because sibling resolution already answers.
//
// Adding it would cost the thing the split just bought. sr-file links the file
// checks and nothing else; putting a config layer in the shared path would link
// a config parser into every one of these binaries so that none of them could
// read a setting that does not exist. Revisit if a real user-facing setting
// ever appears — a default schema path, a global disable — and note that such a
// setting would belong to `.sloprail/` beside the guardrails, which is
// project-scoped and already the place this tool looks.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/subbin"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// service is one high-level command and the binary it dispatches to.
type service struct {
	name  string // the word typed after `sr`
	short string // one line, matching the target binary's own Short
}

// services is the dispatch table — the single list of what `sr` proxies.
//
// One list so the help and the dispatch cannot disagree: a command shown in the
// help is by construction a command that dispatches, and adding a service is
// one line rather than an entry here and a matching case elsewhere.
//
// The short lines are COPIES of each service's own, and copies are the thing
// this repo otherwise refuses. Deriving them is not possible without cost: a
// cobra root never prints its own Short (--help prints Long, and Short is shown
// only by a parent listing children), so the only runtime source is exec'ing
// every service to print one help screen. The copy is bounded — one line per
// service — and it is pinned by TestProxyShortsMatchTheServices, which reads
// each service's Short from its source and fails on any disagreement. Three of
// the five services in the table at the time had ALREADY drifted when that test
// was written.
var services = []service{
	{"session", "Session lifecycle — the hook points a harness calls"},
	{"file", "Checks over a file's contents"},
	{"mark", "Write // sr:<kind> <fqn> enforcement markers into impl files"},
	{"agent", "Run an agent, whichever harness is running"},
	{"eval", "Prove a guardrail's use case against a real agent"},
}

// binaryName is the binary a service word dispatches to.
func binaryName(s string) string { return "sr-" + s }

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "sr <command>",
		Short: "Declarative contracts that keep an agent's output honest",
		Long: `Declarative contracts that keep an agent's output honest.

A project declares guardrails under .sloprail/guardrails/; a harness calls the
session hook points, and the engine runs whichever guardrails bind to what is
about to happen.

Each command below is a separate binary — ` + "`sr session start`" + ` and
` + "`sr-session start`" + ` do the same thing. This root exists so there is one name
to learn; the hooks a project installs name the service binaries directly.

The event kinds a guardrail may bind to are per-build and are reported by the
load check: ` + "`sr session start`" + ` names every kind this build produces, and
every field a kind carries, when a declaration binds to one it does not have.
To write a guardrail, use the authoring-guardrails skill.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	for _, s := range services {
		root.AddCommand(newProxyCmd(s))
	}
	return root
}

// newProxyCmd builds the passthrough command for one service.
//
// DisableFlagParsing is what makes the proxy transparent. Without it cobra
// would claim `--help` and any flag it recognised for itself, so `sr file
// validate -c x.md` would fail here rather than reaching sr-file, and `sr
// session start --help` would print this command's help instead of the
// service's. Every token after the service word belongs to the service.
func newProxyCmd(s service) *cobra.Command {
	return &cobra.Command{
		Use:                s.name + " ...",
		Short:              s.short,
		Long:               s.short + ".\n\nRuns " + binaryName(s.name) + ", passing everything after `" + s.name + "` to it unchanged.\nRun `" + binaryName(s.name) + " --help` — or `sr " + s.name + " --help` — for its own help.",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return subbin.Exec(cmd.Context(), binaryName(s.name), args...)
		},
	}
}
