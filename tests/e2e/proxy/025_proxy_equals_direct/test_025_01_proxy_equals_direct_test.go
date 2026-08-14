package e2e

import (
	"testing"
)

// T025_01: `sr <command> ...` and `sr-<command> ...` produce the same thing.
//
// The proxy's entire justification is that it is transparent. If the two ever
// disagree, a user following the documentation would get different behaviour
// from the two spellings the docs present as equivalent — and the hooks name the
// service binary while a person types the proxy, so a divergence would be a
// difference between what is tested by hand and what actually runs in a session.
//
// Both stdout+stderr and the exit code are compared. Comparing output alone
// would miss the failure that matters most here: sloprail's verdict is carried
// in the exit status, so a proxy that printed the right thing and exited 1
// instead of 2 would look correct to any test reading only text.
//
// WHAT THIS DOES NOT COVER, measured rather than assumed. Every command below
// exits 0 or 1, because no service in the tree exits anything else today — the
// engine refuses through a JSON permissionDecision on exit 0 (hookio.go's deny),
// and sr-file follows `cue vet`'s 0/1. So a proxy that flattened all non-zero
// statuses to 1 would still pass this test. That mutation was applied and
// confirmed to pass here.
//
// It is caught one level down, by TestExecPreservesExitCode in
// internal/subbin, which drives the real `sr` against a child exiting 0, 1, 2,
// 3 and 42. That is the right home for it: the property belongs to the proxy
// mechanism, and pinning it needs a child with a chosen exit code rather than
// one of today's services. Should a service ever exit 2 — the code Claude Code
// treats as a refusal, per the measured table in
// services/sr-session/session_pre_tool.go — add it here too.
func TestT025_01_ProxyMatchesDirectInvocation(t *testing.T) {
	e := New(t)
	dir := t.TempDir()

	cases := []struct {
		name   string
		binary string   // the service binary, invoked directly
		direct []string // args to the service binary
		proxy  []string // args to `sr`, which should be the same command
	}{
		{
			// A command that SUCCEEDS and prints a substantial screen. This case
			// used to be `guardrail help`, which was the only subcommand of a
			// binary that no longer exists — with the command gone, the case was
			// asserting that two spellings of nothing agree. `mark --help` is the
			// nearest surviving equivalent: a zero-exit command whose whole answer
			// is its stdout, so a proxy that truncated or reordered output fails
			// here the way the old case would have.
			name:   "mark help",
			binary: "sr-mark",
			direct: []string{"--help"},
			proxy:  []string{"mark", "--help"},
		},
		{
			// A command that FAILS. The error path is where a proxy is most
			// likely to differ, because that is where an intermediate process
			// has an error of its own to report and can substitute it for the
			// child's.
			name:   "session state without a guardrail in scope",
			binary: "sr-session",
			direct: []string{"state", "get", "anything"},
			proxy:  []string{"session", "state", "get", "anything"},
		},
		{
			// An unknown SUBCOMMAND of a known service must be answered by the
			// service, identically either way — cobra's suggestion text and all.
			name:   "unknown subcommand of a real service",
			binary: "sr-session",
			direct: []string{"nosuchthing"},
			proxy:  []string{"session", "nosuchthing"},
		},
		{
			// --help must reach the service rather than being claimed by the
			// proxy. Without DisableFlagParsing the proxy prints its own help
			// here and the two disagree completely.
			name:   "help flag reaches the service",
			binary: "sr-file",
			direct: []string{"--help"},
			proxy:  []string{"file", "--help"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			direct := e.CLIDirect(dir, tc.binary, tc.direct...)
			proxied := e.CLI(dir, tc.proxy...)

			if direct.Output != proxied.Output {
				t.Errorf("output differs between `%s %v` and `sr %v`:\n--- direct ---\n%s\n--- proxied ---\n%s",
					tc.binary, tc.direct, tc.proxy, direct.Output, proxied.Output)
			}
			if direct.Code != proxied.Code {
				t.Errorf("exit code differs: `%s %v` exited %d, `sr %v` exited %d — the verdict is carried in this number",
					tc.binary, tc.direct, direct.Code, tc.proxy, proxied.Code)
			}
		})
	}
}

// T025_02: the proxy forwards stdin to the service.
//
// Every hook point reads its payload from stdin, so a proxy that did not
// connect it would leave the engine reading an empty payload — which fails in a
// way that looks like a malformed harness rather than like a broken proxy.
//
// `session id` is the smallest command that must read stdin to answer: it
// derives the session's stable identity from the transcript named on the
// payload. Given no payload it reports that, and the two spellings must report
// it identically.
func TestT025_02_ProxyForwardsStdin(t *testing.T) {
	e := New(t)
	dir := t.TempDir()

	const payload = `{"type":"user","message":{"role":"user","content":"hello"}}`

	direct := e.CLIDirectStdin(dir, payload, "sr-session", "query", "--where", `type == "user"`)
	proxied := e.CLIStdin(dir, payload, "session", "query", "--where", `type == "user"`)

	if direct.Output != proxied.Output {
		t.Errorf("stdin did not reach the service the same way:\n--- direct ---\n%s\n--- proxied ---\n%s",
			direct.Output, proxied.Output)
	}
	if direct.Code != proxied.Code {
		t.Errorf("exit code differs with stdin: direct %d, proxied %d", direct.Code, proxied.Code)
	}
}
