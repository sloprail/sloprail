package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// goEnvPassthrough is the Go toolchain's own RESOLVED environment variables,
// carried through as explicit KEY=value pairs into every hook-script
// subprocess this file runs.
//
// WHY THIS EXISTS: the wrapper script's fallback lookup (added for the
// reviewer's ~/.local/bin gap) runs `go env GOPATH` to find go install's
// default bin directory. A test here isolates HOME to a t.TempDir() so it can
// construct a fake install location — but a bare HOME override with nothing
// else makes the Go toolchain fall back to ITS OWN defaults relative to that
// fake HOME, and Go keeps more than one kind of state under HOME: the module
// cache (GOPATH/GOMODCACHE — GOPATH defaults to $HOME/go, GOMODCACHE to
// $GOPATH/pkg/mod), the build cache (GOCACHE), its own config file (GOENV),
// and toolchain selection (GOTOOLCHAIN). A subprocess `go` invocation that
// writes ANY of these into the fake HOME populates a REAL, persistent
// directory inside the very t.TempDir() being torn down. Measured in CI:
// "unlinkat .../go/pkg/mod/golang.org/toolchain@.../lib/wasm/
// go_wasip1_wasm_exec: permission denied" — the module cache ships read-only
// files, so t.TempDir()'s cleanup cannot remove it.
//
// This is resolved via `go env`, NOT read from os.Environ(): these variables
// are normally unset in a real environment (Go computes them from GOPATH/HOME
// on the fly), so a plain os.Environ() filter would find nothing to pass
// through and the bug would persist. Calling `go env` here, in the OUTER test
// process — before any HOME override — gets the values this machine's Go
// toolchain is ACTUALLY using, and setting them explicitly in the subprocess
// pins every one of these locations there regardless of what HOME says. The
// subprocess's Go toolchain, if invoked at all, then keeps using the outer
// test run's already-warm, real state — never a location inside a
// t.TempDir() this file created. Paired with cmd.Dir (see noModuleDir):
// together they remove both what a subprocess `go` command would read (this
// repo's go.mod, invisible from outside the module) and where it would write
// (every HOME-relative state directory, pinned to the real ones).
//
// # Why GOTELEMETRY/GOTELEMETRYDIR are NOT here
//
// They used to be, and it was a confidently wrong fix rather than a working
// one — `go env` lists both under the read-only "non-settable" set
// (cmd/go/internal/envcmd/env.go's switch naming GOTOOLDIR, GOVERSION,
// GOTELEMETRY, GOTELEMETRYDIR and others as computed, never accepted as
// input), and the actual telemetry directory a running `go` binary writes to
// is a package-level global computed ONCE, at process init, straight from
// os.UserConfigDir() (golang.org/x/telemetry/internal/telemetry's `init()`).
// Nothing in that path consults an environment variable named
// GOTELEMETRYDIR at all. So setting it here changed what `go env
// GOTELEMETRYDIR` PRINTS in a later `go env` call, and changed nothing about
// where telemetry's counter files, upload token, and mode-tracking file are
// actually written — which is exactly the directory-not-empty failure this
// task chased down: a `go env GOPATH` subprocess quietly wrote real
// telemetry state under the fake, about-to-be-removed HOME regardless of
// this slice, because the slice's GOTELEMETRYDIR entry was never read by
// anything that mattered. Verified directly: even `HOME=<fake>
// GOTELEMETRY=off GOTELEMETRYDIR=<real> go env GOPATH` still creates
// <fake>/Library/Application Support/go/telemetry/local/*.count (darwin) —
// the env vars are accepted by the shell and ignored by the toolchain.
//
// xdgConfigHomeOverride (below) is what actually redirects it, on the one
// platform this fix can reach at all — see its own doc comment for why
// darwin is a separate story and hookHOME's retrying cleanup is the backstop
// for it.
var goEnvPassthrough = resolveGoEnv(
	"GOPATH", "GOMODCACHE", "GOCACHE", "GOENV", "GOTOOLCHAIN")

// xdgConfigHomeOverride is the REAL user config directory, resolved once in
// the outer test process, to be passed into every hook-script subprocess as
// an explicit $XDG_CONFIG_HOME.
//
// On Linux (the CI runner every one of these tests actually runs on),
// os.UserConfigDir() — which is what golang.org/x/telemetry's package-level
// `Default` is built from at process init — checks $XDG_CONFIG_HOME BEFORE
// $HOME/.config (os/file.go's UserConfigDir, the `default:` / Unix case).
// Pinning it to the value this machine's REAL config directory already
// resolves to means a subprocess whose $HOME points at a fake, about-to-be-
// removed t.TempDir() still computes its telemetry directory (and anything
// else that goes through os.UserConfigDir) from the real, already-existing,
// never-torn-down location — so nothing gets WRITTEN under the fake HOME for
// this to race against in the first place. This is the actual fix for the
// measured CI failure ("unlinkat .../.config/go/telemetry/local: directory
// not empty"): .config is exactly the path XDG_CONFIG_HOME redirects away
// from HOME on Linux.
//
// On darwin, os.UserConfigDir() is hard-wired to
// "$HOME/Library/Application Support" (file.go's `darwin, ios` case) and
// does not consult XDG_CONFIG_HOME at all — there is no environment variable
// that redirects it, which is the same dead end GOTELEMETRYDIR turned out to
// be. This override is therefore a real fix on Linux and a harmless no-op
// everywhere else; hookHOME's retry is what covers the residual local-dev
// risk on darwin, where this whole class of bug was always going to be
// possible to hit at low frequency rather than something an environment
// variable can close off entirely.
func resolveXDGConfigHomeOverride() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return dir
	}
	return ""
}

var xdgConfigHomeOverride = resolveXDGConfigHomeOverride()

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

// hookHOME returns a fresh directory for a subprocess's isolated $HOME, with
// a cleanup that tolerates the one race none of the environment-variable
// pinning above can close off on every platform.
//
// WHY A RETRY, on top of goEnvPassthrough and xdgConfigHomeOverride rather
// than instead of them: those two remove the CAUSE for everything they can
// reach — the module cache, build cache, GOENV, GOTOOLCHAIN resolution
// outright, and (via XDG_CONFIG_HOME) the telemetry directory on Linux, which
// is where CI actually runs and where this bug was actually measured. What
// remains is darwin's telemetry path specifically: os.UserConfigDir() is
// hard-wired there to $HOME/Library/Application Support with no environment
// variable able to redirect it (see xdgConfigHomeOverride's own doc comment),
// and golang.org/x/telemetry's counter file is mmap'd by the `go` subprocess
// and can still be mid-write, via a background goroutine, in the brief window
// around that subprocess's own exit — a genuine, narrow TOCTOU race between
// "a new file appears in a directory" and "RemoveAll's readdir already
// finished listing it," not something any input this test controls can
// sequence away.
//
// A single retry is the right size for that race rather than a symptom of
// giving up on finding the real cause: a file that appeared mid-teardown is
// simply THERE by the time a second attempt walks the directory a moment
// later, so removing it needs no more than trying again once. Not spent on
// Linux, where xdgConfigHomeOverride already means nothing gets written under
// the fake HOME for a retry to ever be needed.
func hookHOME(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sr-hook-home-")
	if err != nil {
		t.Fatalf("hookHOME: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			// One retry, after giving whatever async writer is still mid-flight
			// a brief moment to finish. See this function's own doc comment for
			// why a SECOND attempt is expected to succeed rather than needing a
			// loop: the write that raced this one is not itself repeating.
			time.Sleep(20 * time.Millisecond)
			if err := os.RemoveAll(dir); err != nil {
				t.Errorf("hookHOME cleanup: %v", err)
			}
		}
	})
	return dir
}

// runHookScript runs sr-session-hook.sh with the given subcommand and PATH,
// returning combined output and the exit code. It never has sr-session's real
// directory on PATH unless withBinDir is set, so "missing" is constructed by
// omission rather than by hiding a real binary.
//
// The subprocess's environment isolates only PATH (the property under test)
// and HOME (so a fake ~/.local/bin can be constructed without touching the
// real one) — see goEnvPassthrough and xdgConfigHomeOverride for why the Go
// toolchain's own variables and its telemetry directory ride along pinned to
// the real ones rather than being isolated too, and hookHOME for the retry
// that covers what neither can reach. cmd.Dir is likewise moved OUT of this
// module (see noModuleDir) so a `go env` the wrapper script runs never sees
// this repo's go.mod at all, which is the second half of that same isolation:
// with no go.mod in view there is nothing for GOTOOLCHAIN's auto-resolution
// to react to, module directive or not.
func runHookScript(t *testing.T, subcommand, path string) (output string, code int) {
	t.Helper()
	return runHookScriptWith(t, subcommand, path, "")
}

// runHookScriptWith is runHookScript with a hook payload on stdin.
func runHookScriptWith(t *testing.T, subcommand, path, stdin string) (output string, code int) {
	t.Helper()
	script := hookScriptPath(t)
	cmd := exec.Command(script, subcommand)
	cmd.Dir = noModuleDir(t)
	cmd.Env = append([]string{
		"PATH=" + path,
		"HOME=" + hookHOME(t),
		"XDG_CONFIG_HOME=" + xdgConfigHomeOverride,
		// No network from a test: the start-time auto-install has its own
		// test below, against a local release.
		"SLOPRAIL_NO_AUTO_INSTALL=1",
	}, goEnvPassthrough...)
	cmd.Stdin = strings.NewReader(stdin)
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
// sr:proves install/missing-engine-refuses-file-writes
func TestSrSessionHookWrapper_PreToolBlocksLoudlyWhenBinaryMissing(t *testing.T) {
	// /usr/bin:/bin only — no Go tooling, no dev machine's ~/.local/bin. This is
	// exactly the PATH the smoke test used to reproduce the silent no-op.
	out, code := runHookScriptWith(t, "pre-tool", "/usr/bin:/bin", `{"tool_name":"Write","tool_input":{"file_path":"a.ts"}}`)

	// 2 specifically: Claude Code's PreToolUse refuses only on exit 2, and
	// only then shows stderr to the agent. Exit 1 is a "non-blocking error"
	// — the write lands and nobody is told, measured in a real fresh-install
	// eval run where this wrapper used to exit 1.
	if code != 2 {
		t.Fatalf("a write with sr-session missing exited %d, want 2 — anything else is the exact "+
			"silent no-op the smoke test found: the harness lets the write through:\n%s", code, out)
	}
	if !strings.Contains(out, "sr-session") {
		t.Errorf("the refusal does not name the missing binary:\n%s", out)
	}
	if !strings.Contains(out, "install.sh") {
		t.Errorf("the refusal does not give the install command, leaving a stranger with "+
			"a blocked session and no next step:\n%s", out)
	}
}

// TestSrSessionHookWrapper_PreToolLetsBashInstall: with sr-session missing,
// Bash is NOT refused — it is how the agent installs sr-session. Refusing it
// would leave a session that can never repair itself.
// sr:proves install/missing-engine-blocks-nothing-else
func TestSrSessionHookWrapper_PreToolLetsBashInstall(t *testing.T) {
	out, code := runHookScriptWith(t, "pre-tool", "/usr/bin:/bin", `{"tool_name":"Bash","tool_input":{"command":"sh install.sh"}}`)
	if code != 0 {
		t.Fatalf("Bash was refused with sr-session missing (exit %d) — the install itself runs through Bash:\n%s", code, out)
	}
}

// TestSrSessionHookWrapper_StartWarnsButDoesNotBlock proves the deliberate
// asymmetry the fix's docs (sr-session-hook.sh's own header comment) explain: a
// missing install must not brick Claude Code entirely, because start, stop, and
// subagent-stop are not themselves guarded actions. Each warns loudly (this is
// the SessionStart message a person reads, and it repeats at every turn boundary
// so it cannot silently scroll by) and lets the session continue.
// sr:proves install/missing-engine-blocks-nothing-else
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
// sr:proves install/engine-found-outside-the-path
func TestSrSessionHookWrapper_FindsBinaryInLocalBinWhenNotOnPATH(t *testing.T) {
	home := hookHOME(t)
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
	// along unchanged (goEnvPassthrough), XDG_CONFIG_HOME is pinned to the real
	// one (xdgConfigHomeOverride) so nothing under `home` gets telemetry state
	// written into it on Linux, AND cmd.Dir is moved out of this module
	// (noModuleDir) so `go env GOPATH` — which find_sr_session's fallback runs
	// unconditionally, even though this test's ~/.local/bin already satisfies
	// the search before that candidate is ever checked — neither points the Go
	// toolchain's module cache at this HOME-isolated directory nor triggers a
	// toolchain-resolution check against this repo's go.mod. hookHOME's own
	// retrying cleanup is the backstop for whatever none of the above can
	// reach (darwin's telemetry path specifically). See each's own doc comment
	// for the CI failures this prevents.
	cmd.Dir = noModuleDir(t)
	cmd.Env = append([]string{
		"PATH=/usr/bin:/bin",
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + xdgConfigHomeOverride,
	}, goEnvPassthrough...)
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

// TestSrSessionHookWrapper_StartTellsTheAgentRulesFirst pins what the agent,
// not the person, hears at session start. SessionStart's STDOUT is the agent's
// context (stderr is for the person), so the wrapper's rules-first notice must
// be on stdout — installed or not — and a missing install must be on stdout
// too, since the agent is the one that can finish it. Other subcommands'
// stdout stays clean: stop and pre-tool answer the harness there.
func TestSrSessionHookWrapper_StartTellsTheAgentRulesFirst(t *testing.T) {
	stubDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubDir, "sr-session"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write stub sr-session: %v", err)
	}

	stdout := func(subcommand, path string) string {
		t.Helper()
		cmd := exec.Command(hookScriptPath(t), subcommand)
		cmd.Dir = noModuleDir(t)
		cmd.Env = append([]string{"PATH=" + path, "HOME=" + hookHOME(t), "XDG_CONFIG_HOME=" + xdgConfigHomeOverride, "SLOPRAIL_NO_AUTO_INSTALL=1"}, goEnvPassthrough...)
		cmd.Stdin = strings.NewReader("")
		out, _ := cmd.Output()
		return string(out)
	}

	installed := stdout("start", stubDir+":/usr/bin:/bin")
	if !strings.Contains(installed, "rules first") || !strings.Contains(installed, "structure.yaml") {
		t.Errorf("start did not give the agent the rules-first notice on stdout:\n%s", installed)
	}

	missing := stdout("start", "/usr/bin:/bin")
	if !strings.Contains(missing, "rules first") || !strings.Contains(missing, "install.sh") {
		t.Errorf("start with sr-session missing must give the agent both the notice and the install command on stdout:\n%s", missing)
	}

	if got := stdout("stop", stubDir+":/usr/bin:/bin"); strings.Contains(got, "rules first") {
		t.Errorf("stop's stdout is the harness's decision channel and must not carry the notice:\n%s", got)
	}
}

// TestSrSessionHookWrapper_ShipsTheRepositoryInstallScript: the plugin can
// only run what it ships, so it carries a copy of install.sh for the
// start-time install. A copy that drifts is a second installer nobody tests.
func TestSrSessionHookWrapper_ShipsTheRepositoryInstallScript(t *testing.T) {
	hooks := filepath.Dir(hookScriptPath(t))
	shipped, err := os.ReadFile(filepath.Join(hooks, "install.sh"))
	if err != nil {
		t.Fatalf("the plugin ships no install.sh: %v", err)
	}
	root, err := os.ReadFile(filepath.Join(hooks, "..", "..", "..", "..", "install.sh"))
	if err != nil {
		t.Fatalf("read the repository's install.sh: %v", err)
	}
	if string(shipped) != string(root) {
		t.Fatal("marketplace/plugins/sloprail/hooks/install.sh differs from install.sh — copy it over: cp install.sh marketplace/plugins/sloprail/hooks/install.sh")
	}
}

// TestSrSessionHookWrapper_StartInstallsThePinnedRelease: the first session
// after /plugin install installs the binaries itself — the release pinned to
// the plugin's version, checksum-verified by install.sh — tells the agent,
// and then runs the freshly installed sr-session. A local release (stub
// binaries, real archive + checksums shape) stands in for GitHub.
// sr:proves install/session-start-installs-the-matching-release
func TestSrSessionHookWrapper_StartInstallsThePinnedRelease(t *testing.T) {
	platform := "sloprail-" + runtime.GOOS + "-" + runtime.GOARCH
	release := t.TempDir()
	stage := filepath.Join(release, platform)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, bin := range []string{"sr", "sr-session", "sr-file", "sr-mark", "sr-agent", "sr-eval", "sr-checks"} {
		body := "#!/bin/sh\nexit 0\n"
		if bin == "sr-session" {
			body = "#!/bin/sh\necho \"installed sr-session ran: $1\"\n"
		}
		if err := os.WriteFile(filepath.Join(stage, bin), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	archive := platform + ".tar.gz"
	if out, err := exec.Command("tar", "-C", release, "-czf", filepath.Join(release, archive), platform).CombinedOutput(); err != nil {
		t.Fatalf("tar: %v: %s", err, out)
	}
	body, err := os.ReadFile(filepath.Join(release, archive))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	checksums := hex.EncodeToString(sum[:]) + "  " + archive + "\n"
	if err := os.WriteFile(filepath.Join(release, "checksums.txt"), []byte(checksums), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(extra ...string) (string, string) {
		t.Helper()
		home := hookHOME(t)
		cmd := exec.Command(hookScriptPath(t), "start")
		cmd.Dir = noModuleDir(t)
		cmd.Env = append(append([]string{"PATH=/usr/bin:/bin", "HOME=" + home, "XDG_CONFIG_HOME=" + xdgConfigHomeOverride,
			"SLOPRAIL_RELEASE_URL=file://" + release}, extra...), goEnvPassthrough...)
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("start failed: %v\n%s", err, out)
		}
		return string(out), home
	}

	out, home := run()
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "sr-session")); err != nil {
		t.Fatalf("start did not install sr-session into ~/.local/bin: %v\n%s", err, out)
	}
	if !strings.Contains(out, "installing the sr binaries (v") || !strings.Contains(out, "installed v") {
		t.Errorf("the install was not announced to the agent, pinned to the plugin's version:\n%s", out)
	}
	if !strings.Contains(out, "installed sr-session ran: start") {
		t.Errorf("the freshly installed sr-session was not run for this start:\n%s", out)
	}

	out, home = run("SLOPRAIL_NO_AUTO_INSTALL=1")
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "sr-session")); err == nil {
		t.Fatalf("SLOPRAIL_NO_AUTO_INSTALL=1 still installed:\n%s", out)
	}
}

// TestSrSessionHookWrapper_StartRepeatsOnlyProblems pins the filter the wrapper
// uses to decide what of sr-session start's stderr it repeats to the agent under
// "NOT in force until fixed": bookkeeping — which plugin owns which folders, a
// baseline that could not be recorded, the load check's own "N rules loaded"
// summary — is never presented as a rule that failed to load; a real load
// problem is.
// sr:proves install/start-tells-the-agent-which-rules-did-not-load
func TestSrSessionHookWrapper_StartRepeatsOnlyProblems(t *testing.T) {
	stubWith := func(t *testing.T, stderrLines string) string {
		dir := t.TempDir()
		script := "#!/bin/sh\ncat >/dev/null\nprintf '%s' '" + stderrLines + "' >&2\nexit 0\n"
		if err := os.WriteFile(filepath.Join(dir, "sr-session"), []byte(script), 0o755); err != nil {
			t.Fatalf("write stub sr-session: %v", err)
		}
		return dir
	}
	const bookkeeping = "sloprail: plugin mdmap owns .mdmap/**\n" +
		"sloprail: no baseline recorded: no commit yet\n" +
		"sloprail: 3 rules loaded. This only checked that they load: no rule ran against any file or action.\n"

	out, code := runHookScript(t, "start", stubWith(t, bookkeeping)+":/usr/bin:/bin")
	if code != 0 {
		t.Fatalf("start exited %d:\n%s", code, out)
	}
	if strings.Contains(out, "NOT in force") {
		t.Errorf("bookkeeping lines were repeated to the agent as rules not in force:\n%s", out)
	}

	out, _ = runHookScript(t, "start", stubWith(t, bookkeeping+"sloprail: declaration file-guard/typo not loaded\n")+":/usr/bin:/bin")
	header := strings.Index(out, "NOT in force")
	if header < 0 {
		t.Fatalf("a rule that did not load was not repeated to the agent:\n%s", out)
	}
	after := out[header:]
	if !strings.Contains(after, "file-guard/typo not loaded") {
		t.Errorf("the load problem is not under the header:\n%s", out)
	}
	for _, line := range []string{"rules loaded", " owns ", "no baseline recorded"} {
		if strings.Contains(after, line) {
			t.Errorf("bookkeeping %q was repeated under the header:\n%s", line, out)
		}
	}
}
