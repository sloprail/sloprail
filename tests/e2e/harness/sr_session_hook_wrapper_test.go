package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sr_session_hook_wrapper_test.go proves the fix for the clean-install smoke
// test's headline finding (strategy repo,
// memories/tasks/distribution/soft-launch-pain-priorities/02_smoke-test.md):
// `/plugin install sloprail@sloprail-marketplace` registers hooks.json's
// commands, but nothing on that path puts the sr* binaries on $PATH. Before
// this fix, hooks.json called `sr-session <subcommand>` bare, so a stranger who
// followed only the documented install step got a session that ran completely
// unguarded — no error, no warning, sloprail silently never invoked.
//
// The fix moves the call behind
// marketplace/plugins/sloprail/hooks/sr-session-hook.sh, which checks for
// sr-session before dispatching to it. These tests drive THAT SCRIPT DIRECTLY
// with sr-session removed from PATH — the exact condition the smoke test
// reproduced ("a /usr/bin:/bin-only PATH, i.e. what a stranger has before any Go
// tooling") — rather than through the full mock harness, because the mock's Env
// always prepends a binDir containing every service binary onto PATH, which
// would make sr-session "missing" impossible to construct without weakening the
// harness for everyone else.

// hookScriptPath resolves the wrapper this repo ships, from the module root
// tests already know how to find.
func hookScriptPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(repoRoot(t), "marketplace", "plugins", "sloprail", "hooks", "sr-session-hook.sh")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("hook wrapper script not found at %s: %v", p, err)
	}
	return p
}

// goEnvPassthrough is the Go toolchain's own RESOLVED environment variables
// (GOPATH, GOMODCACHE, GOCACHE, GOENV, GOTOOLCHAIN), carried through as
// explicit KEY=value pairs into every hook-script subprocess this file runs.
//
// WHY THIS EXISTS: the wrapper script's fallback lookup (added for the
// reviewer's ~/.local/bin gap) runs `go env GOPATH` to find go install's
// default bin directory. A test here isolates HOME to a t.TempDir() so it can
// construct a fake install location — but a bare HOME override with nothing
// else makes the Go toolchain fall back to ITS OWN defaults relative to that
// fake HOME (GOPATH defaults to $HOME/go, GOMODCACHE to $GOPATH/pkg/mod, and
// so on), and a subprocess `go` invocation that needs a toolchain matching
// this repo's go.mod directive can then populate a REAL module cache —
// toolchain binaries included — inside the very t.TempDir() being torn down.
// Measured directly in CI: "TempDir RemoveAll cleanup: unlinkat
// .../go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.0.linux-amd64/lib/wasm/
// go_wasip1_wasm_exec: permission denied" — Go's module cache ships read-only
// files, and t.TempDir()'s cleanup cannot remove those on every
// filesystem/runner, hence a CI-only failure with no test assertion ever
// actually failing.
//
// This is resolved via `go env`, NOT read from os.Environ(): these variables
// are normally unset in a real environment (Go computes them from GOPATH/HOME
// on the fly), so a plain os.Environ() filter would find nothing to pass
// through and the bug would persist. Calling `go env` here, in the OUTER test
// process — before any HOME override — gets the values this machine's Go
// toolchain is ACTUALLY using, and setting them explicitly in the subprocess
// pins the module cache there regardless of what HOME says. The subprocess's
// Go toolchain, if invoked at all, then keeps using the outer test run's
// already-warm, real cache — never a location inside a t.TempDir() this file
// created. Only HOME (and PATH, the property under test) are isolated.
var goEnvPassthrough = resolveGoEnv(
	"GOPATH", "GOMODCACHE", "GOCACHE", "GOENV", "GOTOOLCHAIN")

// resolveGoEnv runs `go env <names...>` once and returns each as a "KEY=value"
// pair, in the same order. A name `go env` reports empty for is included as
// "KEY=" (explicitly empty, not omitted) so it still overrides whatever the
// subprocess's own defaulting would otherwise compute from an isolated HOME.
// If `go` itself cannot be found or run — this package's own tests already
// require it, so this is not expected — the zero-value (nil) is used and
// runHookScript's isolated-HOME tests fall back to whatever the subprocess's
// Go toolchain would compute on its own, same as before this fix.
func resolveGoEnv(names ...string) []string {
	out, err := exec.Command("go", append([]string{"env"}, names...)...).Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != len(names) {
		return nil
	}
	pairs := make([]string, len(names))
	for i, name := range names {
		pairs[i] = name + "=" + strings.Trim(lines[i], `"`)
	}
	return pairs
}

// runHookScript runs sr-session-hook.sh with the given subcommand and PATH,
// returning combined output and the exit code. It never has sr-session's real
// directory on PATH unless withBinDir is set, so "missing" is constructed by
// omission rather than by hiding a real binary.
//
// The subprocess's environment isolates only PATH (the property under test)
// and HOME (so a fake ~/.local/bin can be constructed without touching the
// real one) — see goEnvPassthrough for why the Go toolchain's own variables
// ride along unchanged rather than being isolated too. cmd.Dir is likewise
// moved OUT of this module (see noModuleDir) so a `go env` the wrapper script
// runs never sees this repo's go.mod at all, which is the second half of that
// same isolation: with no go.mod in view there is nothing for GOTOOLCHAIN's
// auto-resolution to react to, module directive or not.
func runHookScript(t *testing.T, subcommand, path string) (output string, code int) {
	t.Helper()
	script := hookScriptPath(t)
	cmd := exec.Command(script, subcommand)
	cmd.Dir = noModuleDir(t)
	cmd.Env = append([]string{"PATH=" + path, "HOME=" + t.TempDir()}, goEnvPassthrough...)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	code = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run hook script: %v\n%s", err, out)
	}
	return string(out), code
}

// noModuleDir returns a t.TempDir() with no go.mod in it or above it, for use
// as a subprocess's working directory.
//
// WHY THIS MATTERS, and why goEnvPassthrough's explicit GOPATH/GOMODCACHE/
// GOCACHE/GOENV/GOTOOLCHAIN values were not the whole fix: this repo's own
// go.mod (go 1.25.0) is visible from this test binary's own working
// directory, which a subprocess inherits by default. `go env GOPATH` run
// from THERE still resolves go.mod's directive and, on a runner whose
// installed toolchain does not already satisfy it, can still touch the
// module cache to check/fetch a matching one — even with every cache
// variable pointed at the real, already-populated locations. Measured in CI
// after the first (environment-only) fix: cleanup failed differently,
// "unlinkat .../001: directory not empty" rather than the original
// permission-denied — the write moved, it did not stop. Running the
// subprocess from a directory with no go.mod anywhere above it removes what
// GOTOOLCHAIN=auto would otherwise react to, so `go env` (an operation that
// needs no toolchain resolution at all) has nothing prompting it to try.
func noModuleDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// TestSrSessionHookWrapper_PreToolBlocksLoudlyWhenBinaryMissing is the
// headline property: the refusable moment fails CLOSED, loudly, when sr-session
// cannot be found — never a silent pass-through.
//
// This is a deliberate choice, not the project's default fail-closed-everywhere
// stance applied blindly: only pre-tool (the moment a real guardrail would have
// refused something) blocks. See TestSrSessionHookWrapper_StartWarnsButDoesNotBlock
// for why start/stop/subagent-stop do not.
func TestSrSessionHookWrapper_PreToolBlocksLoudlyWhenBinaryMissing(t *testing.T) {
	// /usr/bin:/bin only — no Go tooling, no dev machine's ~/.local/bin. This is
	// exactly the PATH the smoke test used to reproduce the silent no-op.
	out, code := runHookScript(t, "pre-tool", "/usr/bin:/bin")

	if code == 0 {
		t.Fatalf("pre-tool exited 0 with sr-session missing — this is the exact silent "+
			"no-op the smoke test found: a hook that could not run must not be read as "+
			"having permitted:\n%s", out)
	}
	if !strings.Contains(out, "sr-session") {
		t.Errorf("the refusal does not name the missing binary:\n%s", out)
	}
	if !strings.Contains(out, "install.sh") {
		t.Errorf("the refusal does not give the install command, leaving a stranger with "+
			"a blocked session and no next step:\n%s", out)
	}
}

// TestSrSessionHookWrapper_StartWarnsButDoesNotBlock proves the deliberate
// asymmetry the fix's docs (sr-session-hook.sh's own header comment) explain: a
// missing install must not brick Claude Code entirely, because start, stop, and
// subagent-stop are not themselves guarded actions. Each warns loudly (this is
// the SessionStart message a person reads, and it repeats at every turn boundary
// so it cannot silently scroll by) and lets the session continue.
func TestSrSessionHookWrapper_StartWarnsButDoesNotBlock(t *testing.T) {
	for _, subcommand := range []string{"start", "stop", "subagent-stop"} {
		t.Run(subcommand, func(t *testing.T) {
			out, code := runHookScript(t, subcommand, "/usr/bin:/bin")

			if code != 0 {
				t.Fatalf("%s exited non-zero with sr-session missing — this is not a guarded "+
					"action, so a missing install must warn, not brick the session:\n%s",
					subcommand, out)
			}
			if !strings.Contains(out, "sr-session") || !strings.Contains(out, "install.sh") {
				t.Errorf("%s did not warn loudly about the missing binary and how to fix it — "+
					"this must never be silent:\n%s", subcommand, out)
			}
		})
	}
}

// TestSrSessionHookWrapper_DispatchesNormallyWhenBinaryPresent is the negative
// control: once sr-session is genuinely on PATH, the wrapper is invisible —
// it dispatches straight through rather than adding its own opinion about a
// working install.
func TestSrSessionHookWrapper_DispatchesNormallyWhenBinaryPresent(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "sr-session")
	// A stub that proves it was really invoked, with the subcommand forwarded,
	// and echoes something recognizable rather than sloprail's own "missing"
	// wording.
	script := "#!/bin/sh\necho \"real sr-session ran: $1\"\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub sr-session: %v", err)
	}

	out, code := runHookScript(t, "start", dir+":/usr/bin:/bin")

	if code != 0 {
		t.Fatalf("wrapper refused even though sr-session is present:\n%s", out)
	}
	if !strings.Contains(out, "real sr-session ran: start") {
		t.Errorf("the wrapper did not dispatch to the real sr-session with the subcommand "+
			"forwarded:\n%s", out)
	}
	if strings.Contains(out, "not installed") {
		t.Errorf("the wrapper printed its own missing-binary warning even though sr-session "+
			"is present:\n%s", out)
	}
}

// TestSrSessionHookWrapper_FindsBinaryInLocalBinWhenNotOnPATH is the fix for a
// gap a reviewer found in this PR: the wrapper originally looked ONLY at bare
// $PATH. Claude Code's hook environment is not guaranteed to carry everything
// an interactive shell's profile adds to $PATH, and install.sh's own default
// destination — ~/.local/bin — is exactly the kind of directory that can be
// missing from it. Without this fallback, a user who installed CORRECTLY via
// install.sh could still be blocked as if they had never installed at all —
// indistinguishable from the original bug from the user's side, just one layer
// deeper. So the wrapper now also checks $SLOPRAIL_INSTALL_DIR, ~/.local/bin,
// and go's GOPATH/bin (in that order) before concluding sr-session is missing.
//
// This test pins the ~/.local/bin case specifically, since that is install.sh's
// own default and therefore the single most common way a real user hits this:
// the binary sits in $HOME/.local/bin, that directory is NOT on $PATH, and the
// wrapper must still find and run it rather than refusing.
func TestSrSessionHookWrapper_FindsBinaryInLocalBinWhenNotOnPATH(t *testing.T) {
	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatalf("mkdir ~/.local/bin: %v", err)
	}
	stub := filepath.Join(localBin, "sr-session")
	script := "#!/bin/sh\necho \"local-bin sr-session ran: $1\"\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub sr-session: %v", err)
	}

	script2 := hookScriptPath(t)
	cmd := exec.Command(script2, "pre-tool")
	// PATH deliberately excludes localBin — this is the whole point: install.sh
	// put the binary in ~/.local/bin, but the hook's own $PATH does not carry
	// it, the exact gap the reviewer found. GOPATH/GOMODCACHE/GOCACHE/etc ride
	// along unchanged (goEnvPassthrough) AND cmd.Dir is moved out of this
	// module (noModuleDir) so `go env GOPATH` — which find_sr_session's
	// fallback runs unconditionally, even though this test's ~/.local/bin
	// already satisfies the search before that candidate is ever checked —
	// neither points the Go toolchain's module cache at this HOME-isolated
	// t.TempDir() nor triggers a toolchain-resolution check against this
	// repo's go.mod. See both vars' doc comments for the two-stage CI
	// failure this prevents (permission-denied, then directory-not-empty
	// once the first stage alone was fixed).
	cmd.Dir = noModuleDir(t)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + home}, goEnvPassthrough...)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run hook script: %v\n%s", err, out)
	}

	if code != 0 {
		t.Fatalf("a correctly-installed sr-session in ~/.local/bin (not on $PATH) was "+
			"refused as if it were missing entirely:\n%s", out)
	}
	if !strings.Contains(string(out), "local-bin sr-session ran: pre-tool") {
		t.Errorf("the wrapper did not find and dispatch to ~/.local/bin/sr-session:\n%s", out)
	}
	if strings.Contains(string(out), "not installed") {
		t.Errorf("the wrapper printed its own missing-binary warning even though sr-session "+
			"is present in ~/.local/bin:\n%s", out)
	}
}
