// Package harness drives end-to-end tests the way a session actually runs.
//
// The agent is a10n-claude-mock, a drop-in `claude` that streams Claude Code
// JSONL and fires the lifecycle hooks it finds in the project's settings. The
// hooks it fires are THIS repo's plugin, loaded from THIS repo's marketplace.
//
// A test therefore controls only what the agent tries to do. Everything after
// that — the hook firing, the plugin reaching our subcommand, the engine
// deciding, the refusal travelling back — runs as a user would get it. A test
// that invoked our binary directly would prove the engine decides correctly
// while proving nothing about whether anything ever asks it.
//
// The isolation and transcript-seeding here are ported from a10n's harness,
// which learned them the hard way: a "sandboxed" run must never read or write
// the host's own claude data.
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

const (
	marketplaceName = "sloprail-marketplace"
	pluginName      = "sloprail"
	pluginKey       = pluginName + "@" + marketplaceName
)

// Services are the directories under services/. Each is named for the binary it
// builds, so `go install ./services/...` produces binaries that can find each
// other — Go names an installed binary after its directory, and a directory
// called `session` would install as `session` while the proxy looked for
// `sr-session`. Listed once here so a new service is added in one place.
var Services = []string{"sr", "sr-session", "sr-file", "sr-mark", "sr-agent"}

// Env is one isolated end-to-end environment.
type Env struct {
	t         *testing.T
	binDir    string // holds every built service binary, prepended to PATH so the plugin finds them
	home      string
	configDir string // an isolated stand-in for ~/.claude
	pluginDir string
	repoRoot  string
	mock      string
	shimDir   string // a `claude` that is really the mock, ahead of the real one on PATH
}

var (
	buildOnce sync.Once
	builtDir  string
	buildErr  error
)

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("harness: locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// gitDir is the repository's git directory — a real directory even when the
// worktree's own .git is a file pointing at it.
func gitDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatalf("harness: locate git dir: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// Cleanup removes what build left behind. Call it from TestMain after m.Run,
// which is the only place that can: the binary is shared by every test in the
// process, so it must outlive each of them, and t.Cleanup would delete it out
// from under the second test to ask for it.
//
// Skipping this leaks 15M per test process, and there is one process per e2e
// package. That reached 337 directories and 5GB during one wave of work, and
// filled the disk mid-run — which fails as a build error in whichever test is
// unlucky, not as anything that names the real cause.
func Cleanup() {
	if builtDir != "" {
		os.RemoveAll(builtDir)
	}
}

// build compiles the sloprail binary once per test process.
func build(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		root := repoRoot(t)
		// Not the system temp dir. macOS reaps /var/folders/.../T/ on its own
		// schedule, and it does so mid-run: a suite that builds once and then
		// executes that binary across several minutes of tests finds it gone
		// partway through. That surfaces as `fork/exec ...: no such file or
		// directory` in whichever test was unlucky — an error that names the
		// binary and says nothing about the reaper, which cost one agent eight
		// failures in twenty-two runs before the cause was found.
		//
		// Under the git directory because it is inside the repo (so nothing
		// reaps it) but outside the working tree (so it cannot be mistaken for
		// a project file, and a test that walks the tree does not find a binary
		// in it).
		//
		// Asked of git rather than joined onto the root as ".git": in a linked
		// worktree that path is a FILE, and every agent on this project works
		// in one, so building the path by hand fails for all of them and
		// succeeds only in the main checkout.
		parent := filepath.Join(gitDir(t), "sloprail-e2e")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			buildErr = err
			return
		}
		dir, err := os.MkdirTemp(parent, "bin-")
		if err != nil {
			buildErr = err
			return
		}
		// Recorded before the build, not after. A build that fails still leaves
		// the directory behind, and the failure that matters here is a full
		// disk — so the path that leaks is the one that runs when leaking is
		// already the problem.
		builtDir = dir
		// Every binary a session can reach, built into one directory so they
		// are siblings — which is how the root proxy finds them, and how a
		// service finds a service it calls (internal/subbin).
		//
		// The whole set rather than the ones a given test needs: they are built
		// once per run behind a sync.Once and shared, so selecting per test
		// would mean either rebuilding or teaching every test which binaries
		// its scenario reaches. sr-agent in particular is needed because a
		// guardrail hook that launches an agent runs it BY NAME off PATH, and a
		// test driving that must reach the build under test rather than
		// whatever is installed on the machine — the same now goes for every
		// service a hook might name.
		for _, svc := range Services {
			out := filepath.Join(dir, svc)
			cmd := exec.Command("go", "build", "-o", out, "./services/"+svc)
			cmd.Dir = root
			if o, err := cmd.CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build %s: %v\n%s", svc, err, o)
				return
			}
		}
	})
	if buildErr != nil {
		t.Fatalf("harness: %v", buildErr)
	}
	return builtDir
}

// New stands up an isolated environment.
func New(t *testing.T) *Env {
	t.Helper()
	mock, err := exec.LookPath("a10n-claude-mock")
	if err != nil {
		t.Skip("harness: a10n-claude-mock not on PATH — driving it is the whole point")
	}
	// A short root, not t.TempDir(): the encoded project-dir path below is a
	// 1:1 non-alphanumeric substitution with no shortening, and a long test
	// name pushes a single component past the filename limit.
	root, err := os.MkdirTemp("", "slop-e2e-")
	if err != nil {
		t.Fatalf("harness: temp root: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	e := &Env{
		t:         t,
		binDir:    build(t),
		home:      filepath.Join(root, "home"),
		configDir: filepath.Join(root, "claude-cfg"),
		pluginDir: filepath.Join(root, "plugins"),
		repoRoot:  repoRoot(t),
		mock:      mock,
	}
	for _, d := range []string{e.home, e.configDir, e.pluginDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("harness: mkdir %s: %v", d, err)
		}
	}
	e.shimDir = filepath.Join(root, "shim")
	if err := os.MkdirAll(e.shimDir, 0o755); err != nil {
		t.Fatalf("harness: mkdir shim: %v", err)
	}
	return e
}

// InstallClaudeShim puts a `claude` on PATH that is really the mock.
//
// Needed because sr-agent runs the harness BY NAME: claudeCodeSpec.binary is
// "claude", resolved through PATH in the hook's own child process. Nothing in
// the e2e wiring redirects that today — Run invokes the mock by ABSOLUTE path,
// so a hook that launches an agent inside a test would reach the operator's
// real, billed `claude` and drive it against a temporary project. The
// investigation into this hit exactly that and worked around it with a shim;
// this is that shim, made part of the harness so no test has to remember.
//
// The shim runs the same scenario script the outer session runs, because a
// launched agent in these tests exists to DO something file-shaped — that is
// the whole case worth protecting. It gets its own session id so its transcript
// and state do not land under the parent's.
//
// It also carries SLOP_TEST_DEPTH through unchanged. exec passes the
// environment down on its own; naming it here is what makes the ledger's depth
// column a measurement of nesting rather than of luck.
func (e *Env) InstallClaudeShim(projDir string) {
	e.t.Helper()
	script := "#!/bin/sh\n" +
		"exec " + shellQuote(e.mock) + " \\\n" +
		"  --output-format stream-json \\\n" +
		"  --script " + shellQuote(filepath.Join(projDir, ".inner-scenario.sh")) + " \\\n" +
		"  --project-dir " + shellQuote(projDir) + " \\\n" +
		"  --config-dir " + shellQuote(e.configDir) + " \\\n" +
		"  --plugin-cache-dir " + shellQuote(e.pluginDir) + " \\\n" +
		"  --session-id \"inner-$$\" \\\n" +
		"  \"launched agent\"\n"
	if err := os.WriteFile(filepath.Join(e.shimDir, "claude"), []byte(script), 0o755); err != nil {
		e.t.Fatalf("harness: write claude shim: %v", err)
	}
}

// InstallJudgeClaude puts a `claude` on PATH that stands in for the model a JUDGE
// check invokes through sr-agent, writing a fixed verdict to the output file
// sr-agent named.
//
// A gate's judge check runs `sr-agent --verify … --prompt …`, and sr-agent tells
// the agent (in the prompt) which file to write its answer to — the same two-route
// path the real thing uses. This shim reads that prompt, recovers the output path
// from it (the "Write your answer to the file <path>" line sr-agent appends), and
// writes the verdict there, exactly as a model that followed the instruction would.
// So the whole judge path is exercised — the template rendered, sr-agent invoked,
// the verify script run, the verdict parsed — with only the model's own text
// replaced by a fixed answer, the same substitution the old-format judge tests make
// through A10N_CLAUDE_BIN.
//
// The shim must sit ahead of the real `claude` on PATH; it shares shimDir with
// InstallClaudeShim (a test uses one or the other), which run() already places
// first. The verdict is the JSON object the judge's verify script reads, e.g.
// `{"pass": false, "reasoning": "…"}`.
//
// # Why this is a bespoke shim rather than the a10n-claude-mock
//
// It is a fair question — the rest of the e2e drives the mock, and the mock DOES
// execute a Write tool call, so a scenario that wrote the verdict to sr-agent's
// output file could in principle stand in for the model here. It was tried and it
// does not work, for a reason that is about the INVOCATION, not the verdict.
//
// sr-agent builds the harness command line itself (services/sr-agent, Build-
// Invocation) as `claude -p --model <model> <harness-args> -- <prompt>`, and a
// judge check adds more claude flags on top — the gate/skill judges pass
// `--allowedTools "Write"` and `--settings '{…}'`. The mock accepts only the small
// flag set a harness-driven run uses (--script, --session-id, --output-format,
// --project-dir, --config-dir, --add-dir, …); it has NO --model, --allowedTools or
// --settings, and REFUSES an unknown flag with exit 1 rather than ignoring it.
// Measured against a10n-claude-mock: `--model`, `--allowedTools` and `--settings`
// each exit 1. So a mock invoked as sr-agent's `claude` dies on `--model` before it
// ever reads a scenario, and never writes a verdict.
//
// Fixing that would mean either teaching the mock sr-agent's whole claude-flag
// surface (a change in a different repo, the a10n-cli one) or changing how sr-agent
// invokes the harness (the production judge path, out of a test's remit). This shim
// sidesteps both: it tolerates whatever argv sr-agent builds and needs only the one
// fact sr-agent puts in the prompt — the output path — which is the minimal, honest
// stand-in for a judge verdict until the mock grows that flag surface.
func (e *Env) InstallJudgeClaude(verdict string) {
	e.t.Helper()
	// The prompt arrives as the LAST argument (sr-agent passes it positionally
	// after `--`). The shim scans every argument for sr-agent's own
	// "Write your answer to the file <path>" line and writes the verdict there.
	// A here-doc keeps the verdict body intact regardless of its punctuation.
	script := `#!/bin/sh
# Recover the output path sr-agent told the agent to write, from the prompt in
# the arguments. sr-agent appends "Write your answer to the file <path>."
out=""
for arg in "$@"; do
  case "$arg" in
    *"Write your answer to the file "*)
      out="$(printf '%s' "$arg" | sed -n 's/.*Write your answer to the file \([^ ]*\)\. .*/\1/p' | head -1)"
      ;;
  esac
done
if [ -n "$out" ]; then
  cat > "$out" <<'JUDGE_VERDICT_EOF'
` + verdict + `
JUDGE_VERDICT_EOF
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(e.shimDir, "claude"), []byte(script), 0o755); err != nil {
		e.t.Fatalf("harness: write judge claude shim: %v", err)
	}
}

// InstallJudgeClaudeRecordingArgv is InstallJudgeClaude plus a recording of the
// argv the harness (`claude`) was invoked with, written one-argument-per-line to
// argvFile. It exists to let a test assert the judge's own `model` reached the
// harness invocation: sr-agent builds `claude -p --model <resolved> …`, so the
// resolved model (an alias's harness-native name) appears in this argv. The
// verdict is still written exactly as InstallJudgeClaude does, so the judge path
// runs to a real verdict; the recording is a side effect for the assertion.
//
// The file is truncated and rewritten on each invocation, so after a run it holds
// the LAST `claude` call's argv — a single judge run makes exactly one.
func (e *Env) InstallJudgeClaudeRecordingArgv(argvFile, verdict string) {
	e.t.Helper()
	script := `#!/bin/sh
# Record the argv this harness was invoked with, one argument per line, so a test
# can assert the judge's --model reached here.
: > ` + shellQuote(argvFile) + `
for arg in "$@"; do
  printf '%s\n' "$arg" >> ` + shellQuote(argvFile) + `
done
# Then behave as the ordinary judge shim: write the verdict to the file sr-agent
# named in the prompt.
out=""
for arg in "$@"; do
  case "$arg" in
    *"Write your answer to the file "*)
      out="$(printf '%s' "$arg" | sed -n 's/.*Write your answer to the file \([^ ]*\)\. .*/\1/p' | head -1)"
      ;;
  esac
done
if [ -n "$out" ]; then
  cat > "$out" <<'JUDGE_VERDICT_EOF'
` + verdict + `
JUDGE_VERDICT_EOF
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(e.shimDir, "claude"), []byte(script), 0o755); err != nil {
		e.t.Fatalf("harness: write recording judge claude shim: %v", err)
	}
}

// InstallJudgeClaudeCapturing is InstallJudgeClaude that ALSO records the prompt
// the judge was asked, so a test can assert what the template actually rendered.
//
// # Why a capturing variant exists
//
// The model verdict is a stub either way — that is what lets a test drive the
// pass/fail path. But a stubbed verdict alone cannot show that a check's PREPARE
// step reached the judge TEMPLATE: the renderer treats an undefined variable as
// empty rather than an error (internal/dispatch, TestTemplate_UndefinedIsEmptyAndFalsy),
// so a template that reads `additionalContext.foo` renders fine whether prepare
// produced `foo` or produced nothing at all. A test that only flipped the stub
// would pass against an engine that never ran prepare and never rendered its
// output into the prompt.
//
// So this shim tees the rendered prompt to a file the test names. sr-agent passes
// that prompt as the LAST argument (positionally, after `--`), so it is the
// longest argument and the one carrying the "Write your answer to the file" line;
// the shim writes exactly that argument out. A test then reads it back with
// JudgePrompt and asserts the PREPARED FACT'S VALUE appears in it — a value that
// is present only if prepare extracted it from the trajectory AND the template
// interpolated it. That is the prepare -> template wiring, proven directly rather
// than inferred from a verdict the stub decided.
//
// Everything else matches InstallJudgeClaude: the same output-path recovery and
// the same verdict write, so the verdict path is identical and only the prompt
// capture is added. relPromptFile is written under the project dir (JudgePrompt
// reads it from there).
func (e *Env) InstallJudgeClaudeCapturing(projDir, relPromptFile, verdict string) {
	e.t.Helper()
	promptPath := filepath.Join(projDir, relPromptFile)
	// The prompt argument is the one sr-agent appends its answer-file line to and
	// is the whole rendered template; the shim picks that argument and writes it
	// out verbatim, then recovers the output path from it and writes the verdict —
	// exactly as InstallJudgeClaude does.
	script := `#!/bin/sh
out=""
for arg in "$@"; do
  case "$arg" in
    *"Write your answer to the file "*)
      # tail -1, not head -1: if sr-agent retried, the argument carries several
      # "Write your answer to the file <path>" lines (the accumulated attempts),
      # and the CURRENT attempt's path is the LAST one. Writing the verdict to the
      # last path means the current output file is satisfied on the first try, so
      # a well-formed verdict is honored without a retry storm and the captured
      # prompt is a single clean render.
      out="$(printf '%s' "$arg" | sed -n 's/.*Write your answer to the file \([^ ]*\)\. .*/\1/p' | tail -1)"
      printf '%s' "$arg" > ` + shellQuote(promptPath) + `
      ;;
  esac
done
if [ -n "$out" ]; then
  cat > "$out" <<'JUDGE_VERDICT_EOF'
` + verdict + `
JUDGE_VERDICT_EOF
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(e.shimDir, "claude"), []byte(script), 0o755); err != nil {
		e.t.Fatalf("harness: write capturing judge claude shim: %v", err)
	}
}

// JudgePrompt returns the rendered judge prompt a capturing shim recorded, or ""
// when no judge ran (the file was never written).
//
// This is how a test reads back what the template rendered — see
// InstallJudgeClaudeCapturing. An empty string means the judge check never
// reached the shim (no matching action, a script tier refused first, the guard
// did not fire), which is itself an answer a test may assert on.
func (e *Env) JudgePrompt(projDir, relPromptFile string) string {
	e.t.Helper()
	body, err := os.ReadFile(filepath.Join(projDir, relPromptFile))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		e.t.Fatalf("harness: read judge prompt %s: %v", relPromptFile, err)
	}
	return string(body)
}

// InnerScenario is what the agent a hook LAUNCHES does once it is running.
//
// Written beside the project rather than passed as an argument because the shim
// is a fixed script: sr-agent controls the harness's argv, and a test cannot
// reach through it to add a --script of its own.
func (e *Env) InnerScenario(projDir string, s Scenario) {
	e.t.Helper()
	path := filepath.Join(projDir, ".inner-scenario.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+s.script()+"\n"), 0o755); err != nil {
		e.t.Fatalf("harness: write inner scenario: %v", err)
	}
}

// shellQuote renders a path as one single-quoted shell word.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Project creates a project with this repo's plugin enabled, exactly as a user
// would have it: a marketplace source and an enabled plugin, not a hand-written
// hooks block. What fires during a test is the same wiring anyone installing
// this would get.
func (e *Env) Project() string {
	e.t.Helper()
	dir, err := os.MkdirTemp("", "slop-proj-")
	if err != nil {
		e.t.Fatalf("harness: temp project: %v", err)
	}
	e.t.Cleanup(func() { os.RemoveAll(dir) })

	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		e.t.Fatalf("harness: mkdir .claude: %v", err)
	}
	e.writeSettings(dir)
	return dir
}

// writeSettings writes the project's settings: the plugin as a user would
// install it, and nothing else.
//
// There is deliberately no way to add a lifecycle hook from here. A test that
// hand-wired one into settings.json would be arranging wiring no user has, and
// whatever it then proved would be about the harness's arrangement rather than
// about the product — the whole point of driving the mock is that what fires is
// the plugin someone installs. A property that needs a hook point the plugin
// does not register is a gap in the plugin, and belongs in hooks.json.
func (e *Env) writeSettings(dir string) {
	e.t.Helper()
	settings := map[string]any{
		"enabledPlugins": map[string]any{pluginKey: true},
		"extraKnownMarketplaces": map[string]any{
			marketplaceName: map[string]any{
				"source": map[string]any{"source": "directory", "path": e.repoRoot},
			},
		},
	}
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		e.t.Fatalf("harness: encode settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), body, 0o644); err != nil {
		e.t.Fatalf("harness: write settings: %v", err)
	}
}

// CLI runs the root `sr` proxy and returns what it produced.
//
// The session subcommands are not tested this way — those are invoked by a
// harness, and a test that called them itself would prove the engine decides
// correctly while proving nothing about whether anything ever asks it, which is
// the whole reason Run drives the mock instead.
//
// This is for the commands a person types rather than a harness: the root help,
// and the load check an author runs by hand. Nothing in a session invokes them
// that way, so there is no wiring for driving the mock to prove.
//
// Through the proxy rather than straight at the service, because the proxy is
// what a person types and so it is the path worth covering. It also means every
// one of these tests would catch a proxy that mangled output or lost an exit
// code. CLIDirect drives a service binary without the proxy, for the tests that
// exist to show the two agree.
func (e *Env) CLI(dir string, args ...string) Result {
	e.t.Helper()
	return e.runBin(dir, "", "sr", args...)
}

// CLIDirect runs one service binary by name, bypassing the proxy.
func (e *Env) CLIDirect(dir, binary string, args ...string) Result {
	e.t.Helper()
	return e.runBin(dir, "", binary, args...)
}

// CLIStdin runs the proxy with a payload on standard input.
func (e *Env) CLIStdin(dir, stdin string, args ...string) Result {
	e.t.Helper()
	return e.runBin(dir, stdin, "sr", args...)
}

// CLIDirectStdin runs one service binary with a payload on standard input,
// bypassing the proxy.
func (e *Env) CLIDirectStdin(dir, stdin, binary string, args ...string) Result {
	e.t.Helper()
	return e.runBin(dir, stdin, binary, args...)
}

func (e *Env) runBin(dir, stdin, binary string, args ...string) Result {
	e.t.Helper()
	cmd := exec.Command(filepath.Join(e.binDir, binary), args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	// SLOP_SUBBIN_DIR so the proxy dispatches to the binaries built for this
	// run. They are already siblings, which subbin finds on its own, but naming
	// it makes the test independent of that layout rather than quietly relying
	// on it.
	cmd.Env = append(os.Environ(), "HOME="+e.home, "SLOP_SUBBIN_DIR="+e.binDir)
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		e.t.Fatalf("harness: run %s %v: %v\n%s", binary, args, err, out)
	}
	return Result{Output: string(out), Code: code}
}

// GitInit makes a project a repository with one commit on `main`.
//
// A test about the baseline needs a real one: what is recorded is what git
// reports, and the branch is the whole mechanism by which a switch to another
// line of history is noticed. Its identity is set locally so the run does not
// depend on whatever the machine has configured.
func (e *Env) GitInit(dir string) {
	e.t.Helper()
	e.Git(dir, "init", "--initial-branch=main")
	e.Git(dir, "config", "user.email", "e2e@example.invalid")
	e.Git(dir, "config", "user.name", "E2E")
	e.Git(dir, "add", "-A")
	e.Git(dir, "commit", "--allow-empty", "-m", "initial")
}

// Git runs a git command in dir and returns its trimmed output.
func (e *Env) Git(dir string, args ...string) string {
	e.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("harness: git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Meta reads one of the engine's own per-session facts — the baseline commit,
// the branch it was taken on, the read mark.
//
// Opened directly, because there is no command that prints these: `session
// state get` serves a guardrail's own keys, and meta is the engine's. Adding a
// command to make this test convenient would be adding product surface for a
// test's benefit.
//
// Where the database sits is still not guessed. The session's identity is
// resolved by asking the binary under test — `sr-session id`, the same
// walk every hook uses — so a test cannot pass against a database the engine
// itself would never have written to.
//
// Returns "" for a key never written, which is the same answer the store gives.
func (e *Env) Meta(projDir, sessionID, key string) string {
	e.t.Helper()

	db, err := sessionstate.Open(e.sessionDBPath(projDir, sessionID))
	if err != nil {
		e.t.Fatalf("harness: open session state: %v", err)
	}
	defer db.Close()

	value, _, err := db.Meta(key)
	if err != nil {
		e.t.Fatalf("harness: read meta %s: %v", key, err)
	}
	return value
}

// sessionDBPath mirrors where the engine puts a session's state, having asked
// the engine itself for the only part a test could get wrong: the conversation
// identity, which is not the id the harness reports.
func (e *Env) sessionDBPath(projDir, sessionID string) string {
	e.t.Helper()

	payload := fmt.Sprintf(`{"transcript_path":%q,"cwd":%q}`,
		e.transcriptPath(projDir, sessionID), projDir)

	cmd := exec.Command(filepath.Join(e.binDir, "sr-session"), "id")
	cmd.Dir = projDir
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = append(os.Environ(), "HOME="+e.home, "CLAUDE_CONFIG_DIR="+e.configDir)
	out, err := cmd.Output()
	if err != nil {
		e.t.Fatalf("harness: resolve session id: %v", err)
	}
	stableID := strings.TrimSpace(string(out))

	// The rest is the platform data directory and the encoded workspace, which
	// the engine derives the same way. HOME is the harness's own, so this stays
	// inside the sandbox.
	return filepath.Join(dataHome(e.home), "sloprail", "sessions",
		encodeProjectDir(resolveWorkDir(projDir)), stableID, "state.db")
}

// transcriptPath is where the harness's transcript for a session sits.
func (e *Env) transcriptPath(projDir, sessionID string) string {
	return filepath.Join(e.configDir, "projects",
		encodeProjectDir(resolveWorkDir(projDir)), sessionID+".jsonl")
}

// TranscriptPath is where the mock wrote a session's root transcript on disk,
// for a test that runs a `trajectory` command against a mock-PRODUCED record
// rather than a hand-authored one.
//
// This is the mechanism the trajectory tests use to keep their fixtures the
// mock's: drive a scenario with Run, then hand this path to `trajectory
// describe/cite/normalize --path`. What the command reads is then a transcript
// the mock streamed, deterministic and centralised, not a shape re-derived by
// hand in each test — the only shapes that stay hand-authored are the ones the
// mock provably cannot emit (an AskUserQuestion answer envelope, a sub-agent
// meta naming its dispatching tool_use), each kept with a note saying so.
//
// The file must already exist — Run seeds it and the mock appends to it — so a
// path returned for a session that never ran is a test asking to read a record
// that was never written, and the caller's own read will say so.
func (e *Env) TranscriptPath(projDir, sessionID string) string {
	e.t.Helper()
	return e.transcriptPath(projDir, sessionID)
}

// SubagentRecordPaths lists the sub-agent transcript files the mock wrote for a
// session, as a plain directory listing of <session>/subagents/agent-*.jsonl.
//
// Deliberately a filesystem glob rather than a call to transcript.SubagentPaths:
// a test asserting that `describe` enumerates the sub-agents must compare its
// output against something derived WITHOUT the code under test, or a bug shared
// by both would hide. This is that independent witness — the records the mock
// actually left on disk, sorted for a stable comparison.
//
// Empty when the session dispatched no sub-agent (there is no subagents
// directory), which is a plain "none" rather than a fault.
func (e *Env) SubagentRecordPaths(projDir, sessionID string) []string {
	e.t.Helper()
	dir := filepath.Join(strings.TrimSuffix(e.transcriptPath(projDir, sessionID), ".jsonl"), "subagents")
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatalf("harness: read sub-agent records %s: %v", dir, err)
	}
	var paths []string
	for _, ent := range ents {
		name := ent.Name()
		if ent.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	sort.Strings(paths)
	return paths
}

// dataHome mirrors the engine's own platform data directory, for the sandboxed
// home the mock ran under.
func dataHome(home string) string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support")
	case "windows":
		if dir := os.Getenv("LocalAppData"); dir != "" {
			return dir
		}
		return filepath.Join(home, "AppData", "Local")
	default:
		return filepath.Join(home, ".local", "share")
	}
}

// WriteFile puts a file into a project, creating the directories above it.
//
// For the state a project is in BEFORE a session runs — the files an agent will
// go on to edit or delete. What the agent itself does belongs in a scenario, so
// that it travels through the tool calls a harness reports rather than being
// arranged behind the engine's back.
func (e *Env) WriteFile(projDir, rel, body string) {
	e.t.Helper()
	full := filepath.Join(projDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		e.t.Fatalf("harness: mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		e.t.Fatalf("harness: write %s: %v", rel, err)
	}
}

// WriteExecutable writes a file into a project with the executable bit set — for
// a script a rule will run that lives in the tree rather than beside a rule's own
// declaration (a goal's verify.sh under goal/<name>/, which a gate's check execs).
// WriteFile writes 0644, which a check trying to run the file would refuse; this
// is the same helper for the case where the file IS a program.
func (e *Env) WriteExecutable(projDir, rel, body string) {
	e.t.Helper()
	full := filepath.Join(projDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		e.t.Fatalf("harness: mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o755); err != nil {
		e.t.Fatalf("harness: write executable %s: %v", rel, err)
	}
}

// Exists reports whether a path is present in a project.
//
// How a test asks what actually happened to the tree, as opposed to what came
// back on the stream. Whether a message travelled says nothing about whether a
// write landed, and "the work was prevented" is a claim about the tree.
func (e *Env) Exists(projDir, rel string) bool {
	e.t.Helper()
	_, err := os.Stat(filepath.Join(projDir, rel))
	if err == nil {
		return true
	}
	if !os.IsNotExist(err) {
		e.t.Fatalf("harness: stat %s: %v", rel, err)
	}
	return false
}

// Guardrail writes a declaration and its hook scripts into a project.
func (e *Env) Guardrail(projDir, name, declaration string, scripts map[string]string) {
	e.t.Helper()
	dir := filepath.Join(projDir, ".sloprail", "guardrails", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatalf("harness: mkdir guardrail: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "GUARDRAIL.md"), []byte(declaration), 0o644); err != nil {
		e.t.Fatalf("harness: write declaration: %v", err)
	}
	for file, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o755); err != nil {
			e.t.Fatalf("harness: write script %s: %v", file, err)
		}
	}
}

// Gate writes a NEW-FORMAT gate declaration and its check scripts/templates into
// a project, at `.sloprail/gate/<name>/gate.yaml`.
//
// The gate and the structure gate are the new nature-based dispatch, distinct from
// Guardrail's old GUARDRAIL.md format — the two run alongside each other, so a test
// may use either. Scripts (a check's `./verify.sh`, a `prepare`, a judge template)
// are written as siblings of gate.yaml, executable, exactly where the gate's own
// relative paths resolve them.
func (e *Env) Gate(projDir, name, gateYAML string, files map[string]string) {
	e.t.Helper()
	dir := filepath.Join(projDir, ".sloprail", "gate", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatalf("harness: mkdir gate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gate.yaml"), []byte(gateYAML), 0o644); err != nil {
		e.t.Fatalf("harness: write gate.yaml: %v", err)
	}
	for file, body := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o755); err != nil {
			e.t.Fatalf("harness: write gate file %s: %v", file, err)
		}
	}
}

// StructureGate writes the NEW-FORMAT structure gate — one tree-wide path
// allowlist — at `.sloprail/file-guard/structure.yaml`.
//
// A singleton for the whole project (there is at most one structure.yaml), so this
// takes only the yaml. It sits beside the per-guard subfolders in file-guard/,
// where the loader reads it.
func (e *Env) StructureGate(projDir, structureYAML string) {
	e.t.Helper()
	dir := filepath.Join(projDir, ".sloprail", "file-guard")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatalf("harness: mkdir file-guard: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "structure.yaml"), []byte(structureYAML), 0o644); err != nil {
		e.t.Fatalf("harness: write structure.yaml: %v", err)
	}
}

// GateState reads one gate's recorded verdict from the session store — pass or
// fail, or "" if the gate never ran.
//
// How a test observes that a gate's verdict LANDED in the gates[] map, which a
// context will read next slice. Read directly from the store, because there is no
// command that prints it (the map is engine-owned, like the baseline meta the Meta
// helper reads). The store is located the same way Meta locates it — by asking the
// binary under test for the conversation identity — so a test cannot pass against a
// store the engine would never have written to.
//
// The keyspace and value shape mirror the engine's own (services/sr-session's
// nature dispatch): per-guardrail state under the reserved `!sloprail:gates` name,
// one `gate:<name>` key per gate, value `{"status":"pass|fail"}`.
func (e *Env) GateState(projDir, sessionID, gateName string) string {
	e.t.Helper()

	db, err := sessionstate.Open(e.sessionDBPath(projDir, sessionID))
	if err != nil {
		e.t.Fatalf("harness: open session state: %v", err)
	}
	defer db.Close()

	value, ok, err := db.State("!sloprail:gates", "gate:"+gateName)
	if err != nil {
		e.t.Fatalf("harness: read gate state %s: %v", gateName, err)
	}
	if !ok {
		return ""
	}
	var st struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(value), &st); err != nil {
		e.t.Fatalf("harness: decode gate state %s: %v", gateName, err)
	}
	return st.Status
}

// FileGuard writes a NEW-FORMAT file-guard declaration and its check
// scripts/templates into a project, at `.sloprail/file-guard/<name>/file-guard.yaml`.
//
// A file-guard is bound to a FILE'S STATE (its `match` over path/markers/context),
// checked after a write settles and — when `preventive: true` — before it lands.
// Scripts (a check's `./verify.sh`, a `prepare`, a judge template) are written as
// siblings of file-guard.yaml, executable, where the guard's own relative paths
// resolve them. It shares the file-guard/ directory with the structure singleton.
func (e *Env) FileGuard(projDir, name, guardYAML string, files map[string]string) {
	e.t.Helper()
	dir := filepath.Join(projDir, ".sloprail", "file-guard", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatalf("harness: mkdir file-guard: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file-guard.yaml"), []byte(guardYAML), 0o644); err != nil {
		e.t.Fatalf("harness: write file-guard.yaml: %v", err)
	}
	for file, body := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o755); err != nil {
			e.t.Fatalf("harness: write file-guard file %s: %v", file, err)
		}
	}
}

// Context writes a NEW-FORMAT context declaration and its enter/exit (and any
// require/check) scripts into a project, at `.sloprail/context/<name>/context.yaml`.
//
// A context is an activatable scope: its `enter` runs on every matching `on`
// trigger and its stdout replaces the context's payload; its `exit` runs at Stop
// and flips active/inactive (never blocking the Stop). enter.sh / exit.sh and any
// other scripts are written executable as siblings of context.yaml.
func (e *Env) Context(projDir, name, contextYAML string, files map[string]string) {
	e.t.Helper()
	dir := filepath.Join(projDir, ".sloprail", "context", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatalf("harness: mkdir context: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "context.yaml"), []byte(contextYAML), 0o644); err != nil {
		e.t.Fatalf("harness: write context.yaml: %v", err)
	}
	for file, body := range files {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o755); err != nil {
			e.t.Fatalf("harness: write context file %s: %v", file, err)
		}
	}
}

// GuardrailState reads the raw key/value entries a NAMED guardrail stored under a
// prefix — the same `sr-session state list` a hook running as that guardrail would
// get, but reached from a test so it can assert what a rule's own enter/check
// actually recorded.
//
// This is how a test observes the registry a CONTEXT writes with `sr-session state
// set` (keyed on the context's own name), independently of whether some sibling
// gate can read it. A composite whose gate reads the wrong scope still has a
// context that logged real entries; this reads those entries under the guardrail
// that wrote them, so "the context logged both people this turn" is checkable even
// when the gate that should consume them cannot.
//
// Located the same way GateState/ContextState locate the store — by asking the
// binary under test for the conversation identity — so a test cannot pass against
// a store the engine would never have written to. An empty map means the guardrail
// stored nothing under that prefix (or never ran).
func (e *Env) GuardrailState(projDir, sessionID, guardrail, prefix string) map[string]string {
	e.t.Helper()

	db, err := sessionstate.Open(e.sessionDBPath(projDir, sessionID))
	if err != nil {
		e.t.Fatalf("harness: open session state: %v", err)
	}
	defer db.Close()

	entries, err := db.ListState(guardrail, prefix)
	if err != nil {
		e.t.Fatalf("harness: list state for %q: %v", guardrail, err)
	}
	out := make(map[string]string, len(entries))
	for _, ent := range entries {
		out[ent.Key] = ent.Value
	}
	return out
}

// ContextState reads one context's recorded {active, payload} from the session
// store — how a test observes that a context ENTERED or EXITED, and what payload
// its enter left. Returns active and the payload map; active is false and the
// payload nil when the context has no recorded state.
//
// Read directly from the store the same way GateState reads the gates[] map, from
// the reserved `!sloprail:contexts` keyspace the engine's context dispatch writes:
// one `context:<name>` key per context, value `{"active":…,"payload":{…}}`. The
// store is located by asking the binary for the conversation identity, so a test
// cannot pass against a store the engine would never have written to.
func (e *Env) ContextState(projDir, sessionID, contextName string) (bool, map[string]any) {
	e.t.Helper()

	db, err := sessionstate.Open(e.sessionDBPath(projDir, sessionID))
	if err != nil {
		e.t.Fatalf("harness: open session state: %v", err)
	}
	defer db.Close()

	value, ok, err := db.State("!sloprail:contexts", "context:"+contextName)
	if err != nil {
		e.t.Fatalf("harness: read context state %s: %v", contextName, err)
	}
	if !ok {
		return false, nil
	}
	var st struct {
		Active  bool           `json:"active"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(value), &st); err != nil {
		e.t.Fatalf("harness: decode context state %s: %v", contextName, err)
	}
	return st.Active, st.Payload
}

// RemoveGuardrail takes a guardrail out of a project mid-session, the way a
// user removes a rule: the whole folder goes.
//
// The WHOLE folder, and that is not a convenience. Deleting only GUARDRAIL.md
// leaves a folder the project still keeps as a guardrail and which can no
// longer be read — and the engine refuses every action while a declaration
// cannot be parsed, deliberately, because an unreadable rule must not be read
// as approval merely for being unreadable. So a half-removal does not remove a
// rule; it disarms the session. That behaviour is pinned by
// pre_tool/013_broken_declaration_is_not_silent, and this helper exists to keep
// tests about REMOVAL from accidentally exercising it.
//
// It returns the rule's ledger lines as they stood at removal, because the
// ledger lives inside the folder that is about to go. A test asking whether a
// removed rule kept firing compares this against what it finds afterwards: with
// the folder gone, a rule that somehow still ran would recreate the file, and an
// absent file is the answer that nothing did.
func (e *Env) RemoveGuardrail(projDir, name, ledgerFile string) []string {
	e.t.Helper()
	before := e.Ledger(projDir, name, ledgerFile)
	dir := filepath.Join(projDir, ".sloprail", "guardrails", name)
	if err := os.RemoveAll(dir); err != nil {
		e.t.Fatalf("harness: remove guardrail %s: %v", name, err)
	}
	return before
}

// DisableGuardrail turns a rule off the other way a user can: the declaration
// stays and says so.
//
// A distinct mechanism from removal rather than a synonym for it — the folder,
// the scripts and the LEDGER all remain, so a disabled rule that kept firing
// appends a line to a file that is still there, which removal cannot observe.
// Written by replacing the declaration wholesale, because the frontmatter is
// what the engine parses and a test that patched a line would be asserting
// something about yaml editing.
func (e *Env) DisableGuardrail(projDir, name, declaration string) {
	e.t.Helper()
	disabled := strings.Replace(declaration, "---\n", "---\nenabled: false\n", 1)
	if disabled == declaration {
		e.t.Fatalf("harness: disable guardrail %s: the declaration has no frontmatter to add "+
			"`enabled: false` to, so nothing was turned off and a test resting on this would "+
			"pass against a rule that is still live", name)
	}
	path := filepath.Join(projDir, ".sloprail", "guardrails", name, "GUARDRAIL.md")
	if err := os.WriteFile(path, []byte(disabled), 0o644); err != nil {
		e.t.Fatalf("harness: disable guardrail %s: %v", name, err)
	}
}

// DisablePluginGuardrail switches off a rule the project did not write, the only
// way a consumer can: from the project's own config, naming the rule
// `<plugin>/<guardrail>`.
//
// Deliberately NOT a variant of DisableGuardrail. That one edits the
// declaration's frontmatter, which for a shipped rule would mean writing inside
// the plugin installation — the thing a consumer must never do, since the next
// reinstall silently undoes it. A test that disabled a plugin's rule that way
// would be proving a mechanism no user has.
func (e *Env) DisablePluginGuardrail(projDir string, qualified ...string) {
	e.t.Helper()
	dir := filepath.Join(projDir, ".sloprail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatalf("harness: mkdir .sloprail: %v", err)
	}
	body := "disabled:\n"
	for _, name := range qualified {
		body += "  - " + name + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		e.t.Fatalf("harness: write config: %v", err)
	}
}

// Ledger returns the lines a guardrail's hooks appended to a file in their own
// folder, or nothing when the file was never created.
//
// This is how a test observes what DID NOT happen. A refusal travels back
// through the tool result and can be read off the stream, but "this hook never
// ran", "this extractor produced nothing" and "these two hooks ran in this
// order" leave no trace there — a hook that stays silent and a hook that never
// ran look identical from outside.
//
// So the hooks write. A hook is an ordinary shell script run with its working
// directory set to the guardrail's folder, so appending a line to a file there
// is the one channel that records a run without the engine's cooperation and
// without a test reaching inside the binary. An absent file is a real answer:
// nothing ran.
func (e *Env) Ledger(projDir, guardrail, file string) []string {
	e.t.Helper()
	body, err := os.ReadFile(filepath.Join(projDir, ".sloprail", "guardrails", guardrail, file))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatalf("harness: read ledger %s/%s: %v", guardrail, file, err)
	}
	var lines []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// Wrote reports whether a path exists in the project tree.
//
// How a test observes an INNER session's outcome. A refusal delivered to the
// agent a hook launched travels back through that agent's own tool result,
// which the launching hook consumed and the outer stream never carried — so
// scanning the outer output for a refusal marker would answer "no refusal" no
// matter what happened. The file either exists or it does not, and that is the
// same question the rule was asked.
func (e *Env) Wrote(projDir, relPath string) bool {
	e.t.Helper()
	_, err := os.Stat(filepath.Join(projDir, relPath))
	return err == nil
}

var nonAlnumRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

// encodeProjectDir mirrors how a harness encodes a working directory into a
// transcript path. Ported from a10n, which ported it from claude's own.
func encodeProjectDir(dir string) string { return nonAlnumRe.ReplaceAllString(dir, "-") }

// resolveWorkDir resolves symlinks so the encoded path matches what the mock
// itself will compute — macOS resolves /var to /private/var, and a mismatch
// puts the seeded transcript somewhere nothing looks.
func resolveWorkDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// seedTranscript writes a minimal valid transcript for (cwd, sessionID): a root
// record with a uuid and a null parentUuid, which is the shape anything looking
// for a conversation's origin scans for.
func (e *Env) seedTranscript(cwd, sessionID, prompt string) {
	e.t.Helper()
	dir := filepath.Join(e.configDir, "projects", encodeProjectDir(resolveWorkDir(cwd)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatalf("harness: seed transcript: %v", err)
	}
	line := fmt.Sprintf(`{"type":"user","uuid":%q,"parentUuid":null,"cwd":%q,"message":{"role":"user","content":%q}}`+"\n",
		"e2e-root-"+sessionID, cwd, prompt)
	path := filepath.Join(dir, sessionID+".jsonl")
	if _, err := os.Stat(path); err == nil {
		return // already seeded, or the mock has started writing — never overwrite
	}
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		e.t.Fatalf("harness: seed transcript: %v", err)
	}
}

// RootMessageID is the uuid seedTranscript gives a session's root user message —
// the human prompt every Run starts from — so a test can REFERENCE that message by
// id without hardcoding the seeding scheme.
//
// This is what a task's ASK.md cites when it names the authorising human message by
// `message_id=<uuid>` (task-management). The reference has to sit in the ASK.md
// content the agent writes, which is authored before the run, so the id must be
// known up front — this exposes it as the one fact a test would otherwise have to
// duplicate from the harness internals. The message's TEXT is the `prompt` passed to
// Run, and the two together are what the guard's prepare resolves and the judge reads.
func (e *Env) RootMessageID(sessionID string) string {
	return "e2e-root-" + sessionID
}

// ControlDecl and ControlScript are the positive control every revalidation
// test rests on: a hook that stores something under its own scope and reads it
// back on its next invocation.
//
// Here rather than in one scenario package because every scenario needs it and
// a Go test package cannot import another's helpers. Exported so the scenario
// that reports the control as a test of its own runs the SAME rule this package
// gates on — two copies could drift, and the copy the gate used would be the
// one nobody was reading. See RequireSessionStore.
const ControlDecl = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./probe.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./probe.sh
---

# Reads its own state back, and says what it found.
`

const ControlScript = `#!/bin/sh
cat >/dev/null
echo "before=[$(sr-session state get seen 2>&1)]" >> "$PWD/log"
sr-session state set seen yes >/dev/null 2>&1
exit 0
`

// SessionStoreOpens reports whether the engine can identify this session and
// open its state at all.
//
// This is the positive control, and it is why every skip test in this tree can
// be believed. A revalidation test asserts a hook did NOT run again — a claim
// that passes trivially when the hook never ran, and just as trivially when
// the engine could not identify the session and so had no record to skip
// against. Both failures are silent, and both make a green suite mean nothing.
//
// The branch that first wrote an e2e for this feature hit exactly that: the
// mock's main-session PreToolUse payload carries no transcript_path, session
// identity failed, no store was opened, every hook re-judged — and the test
// passed whether the skip worked or not. It deleted the test rather than bank
// a vacuous one.
//
// So this establishes the capability POSITIVELY: something is written in one
// hook invocation and read back in the next. Nothing about that can pass by
// accident. A store that never opened returns an error to the hook; one keyed
// differently per invocation returns "not found"; only a store that really
// opened, under one identity, across two separate hook processes, hands back
// what the earlier one put there.
//
// Deliberately a check of the observable capability rather than of the branch
// or the harness, so it keeps working unchanged when the missing piece lands
// rather than needing to be told that it did.
func (e *Env) SessionStoreOpens() (bool, string) {
	e.t.Helper()

	proj := e.Project()
	e.Guardrail(proj, "control", ControlDecl, map[string]string{"probe.sh": ControlScript})

	// Two DIFFERENT paths, so neither invocation can be exempted by the other.
	// The control must not be silenced by the very mechanism it exists to make
	// testable: two writes of one path would let the second be legitimately
	// skipped, and the control would report "unreachable" on a build where the
	// store works perfectly.
	e.Run(proj, "s-control", "write twice", Turns("done",
		Write("c1", "one.md", "first"),
		Write("c2", "two.md", "second"),
	))

	lines := e.Ledger(proj, "control", "log")
	if len(lines) != 2 {
		return false, "the control guardrail's hook did not run twice (got " +
			strings.Join(lines, " | ") + ") — nothing about session state can be concluded"
	}
	// The SECOND invocation is the one that matters. The first legitimately
	// finds nothing: it is what wrote the mark.
	if !strings.Contains(lines[1], "before=[yes]") {
		return false, "a hook could not read back what the previous hook in the same session stored: " +
			strings.Join(lines, " | ")
	}
	return true, ""
}

// RequireSessionStore skips the calling test, naming what is missing, when the
// session store cannot be shown to open.
//
// Skipped rather than failed, and skipped rather than left to pass: a test
// asserting "the hook did not run again" while the store is unreachable would be
// green and worthless, which is the precise failure the control exists to
// prevent.
//
// The two pieces the store needs — a transcript path on the PreToolUse payload
// and the hook environment `session state` resolves its scope from — landed with
// impl/hook-env, so this now passes rather than skips. It is kept because it is
// a real precondition rather than a note about a branch: it fails loudly if
// either piece regresses, and the tests that depend on it would otherwise go
// quietly vacuous again.
func RequireSessionStore(t *testing.T) {
	t.Helper()
	e := New(t)
	if ok, why := e.SessionStoreOpens(); !ok {
		t.Skipf("the session store does not open on this branch, so a skip cannot be observed "+
			"and a passing skip test would be vacuous — this needs a transcript path on the "+
			"PreToolUse payload and c.Env on the hook process, both of which impl/hook-env "+
			"provides: %s", why)
	}
}

// Fork makes a NEW session id that a conversation continues under, the way a
// harness re-forks one mid-conversation.
//
// This is the only way to test that state survives a re-fork, and it has to be
// built rather than asked for: the mock has no compaction or retry path that
// changes the id of a running session, so the transcript a fork would leave is
// written here instead. What is written is the shape the identity walk actually
// looks for — nothing about it is invented for the test's convenience:
//
//   - the new transcript's own root record is parentless, so it IS a root
//     within its file, exactly like any other transcript's first record;
//   - it carries logicalParentUuid naming a record in the OLD transcript,
//     which is what marks it a continuation rather than a new conversation;
//   - the record it names is really in the old file, so the walk crossing the
//     restart finds it where it says.
//
// The conversation's identity is therefore the OLD transcript's root uuid,
// reached by one hop, while the id the harness reports is the new one. That is
// precisely the situation the invariant is about: a store keyed on the reported
// id opens an empty database, and a store keyed on the origin finds the
// verdicts already recorded.
//
// The old session must have been Run (or seeded) first — a fork continuing a
// file that does not exist is not a fork, and the walk would fail rather than
// resolve to the wrong thing, which would make the test pass for the wrong
// reason.
func (e *Env) Fork(cwd, oldSessionID, newSessionID string) {
	e.t.Helper()

	dir := filepath.Join(e.configDir, "projects", encodeProjectDir(resolveWorkDir(cwd)))
	oldPath := filepath.Join(dir, oldSessionID+".jsonl")
	if _, err := os.Stat(oldPath); err != nil {
		e.t.Fatalf("harness: fork %s: the session being continued has no transcript at %s: %v",
			oldSessionID, oldPath, err)
	}

	// The record the new file continues FROM. seedTranscript's root is the one
	// record every seeded session is guaranteed to have, and it is genuinely in
	// the old file — asserted below rather than assumed, because a fork pointing
	// at a record that is not there resolves to nothing and the test would fail
	// for a reason that has nothing to do with the invariant.
	continued := "e2e-root-" + oldSessionID
	body, err := os.ReadFile(oldPath)
	if err != nil {
		e.t.Fatalf("harness: fork %s: read %s: %v", oldSessionID, oldPath, err)
	}
	if !strings.Contains(string(body), `"uuid":"`+continued+`"`) {
		e.t.Fatalf("harness: fork %s: %s does not hold the record %q the fork would continue from",
			oldSessionID, oldPath, continued)
	}

	// Parentless within its own file AND naming what it continues: both, which
	// is what a real re-forked transcript looks like and what makes the walk
	// take its second hop instead of stopping here.
	line := fmt.Sprintf(
		`{"type":"user","uuid":%q,"parentUuid":null,"logicalParentUuid":%q,"cwd":%q,"message":{"role":"user","content":"continued"}}`+"\n",
		"e2e-fork-"+newSessionID, continued, cwd)

	path := filepath.Join(dir, newSessionID+".jsonl")
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		e.t.Fatalf("harness: fork %s: %v", oldSessionID, err)
	}
}

// SessionIdentity is the identity the engine resolves for a session's
// transcript — the conversation's own origin, not the id the harness reports.
//
// Asked of the binary under test rather than derived here. The walk that
// crosses a re-fork is the thing under test, so a test computing it a second
// way would be comparing its own reimplementation against itself and would
// agree with a broken engine.
//
// Returns "" when the engine cannot resolve one, which is an answer rather than
// a failure: a test asserting that two transcripts resolve alike needs to be
// able to say that neither did.
func (e *Env) SessionIdentity(projDir, sessionID string) string {
	e.t.Helper()

	transcript := filepath.Join(e.configDir, "projects",
		encodeProjectDir(resolveWorkDir(projDir)), sessionID+".jsonl")
	payload := fmt.Sprintf(`{"transcript_path":%q,"cwd":%q}`, transcript, projDir)

	cmd := exec.Command(filepath.Join(e.binDir, "sr-session"), "id")
	cmd.Dir = projDir
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = append(os.Environ(),
		"HOME="+e.home,
		"CLAUDE_CONFIG_DIR="+e.configDir,
	)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// BlockingErrors returns the text of every blocking hook error the harness
// recorded for a session, in order.
//
// Read from the conversation record rather than from the stream, because that
// is where the text actually lands. Measured on this harness, of the ways a
// Stop hook can refuse:
//
//	exit 2 with text on stderr          blocks, and the text arrives
//	exit 0 with {"decision":"block"}    blocks, and the reason arrives
//	exit 1 with text on stderr          does not block, and nothing arrives
//	exit 0 silent                       does not block
//
// Both blocking forms deliver their words the same way: an attachment record of
// type hook_blocking_error, never a line on the result stream. A test asserting
// on Result.Output would therefore be asserting on a channel the text never
// travels, and would fail for a working engine.
//
// Only the attachment's own text is returned, NOT the record as a whole. A
// guardrail's folder path travels on every hook payload, so searching the whole
// transcript for a rule's name finds it whether or not the refusal ever named
// it — an assertion that cannot fail.
func (e *Env) BlockingErrors(projDir, sessionID string) []string {
	e.t.Helper()
	return e.blockingErrors(projDir, sessionID, "")
}

// BlockingErrorsFrom returns the text of every blocking hook error recorded for
// a session AT ONE LIFECYCLE EVENT — "Stop", "SubagentStop".
//
// Which hook refused is not a detail when a sub-agent is in play. A sub-agent
// sharing the dispatching session's tree leaves its work where the ROOT's own
// Stop will see it too, so a rule bound to created files refuses twice: once at
// the sub-agent's cycle and once at the root's. A test asserting only that some
// refusal reached the record therefore passes whether or not the sub-agent's
// cycle judged anything at all — measured, and the reason this exists: the first
// version of the sub-agent refusal test passed against an engine whose
// subagent-stop was a stub returning nil.
//
// Everything else is BlockingErrors' behaviour, including the de-duplication;
// see there for why the record rather than the stream.
func (e *Env) BlockingErrorsFrom(projDir, sessionID, hookEvent string) []string {
	e.t.Helper()
	return e.blockingErrors(projDir, sessionID, hookEvent)
}

// blockingErrors reads refusals out of the record, optionally narrowed to one
// lifecycle event. An empty hookEvent means every event.
func (e *Env) blockingErrors(projDir, sessionID, hookEvent string) []string {
	e.t.Helper()

	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(e.transcript(projDir, sessionID), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec struct {
			Attachment struct {
				Type          string `json:"type"`
				HookEvent     string `json:"hookEvent"`
				BlockingError struct {
					BlockingError string `json:"blockingError"`
				} `json:"blockingError"`
			} `json:"attachment"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Attachment.Type != "hook_blocking_error" {
			continue
		}
		if hookEvent != "" && rec.Attachment.HookEvent != hookEvent {
			continue
		}
		text := rec.Attachment.BlockingError.BlockingError
		// A blocked stop is retried, so the same refusal is recorded once per
		// attempt. What a test asks is which refusals arrived, not how many
		// times the agent was driven round.
		if text != "" && !seen[text] {
			seen[text] = true
			out = append(out, text)
		}
	}
	return out
}

// transcript returns the whole conversation record the harness wrote.
func (e *Env) transcript(projDir, sessionID string) string {
	e.t.Helper()
	b, err := os.ReadFile(e.transcriptPath(projDir, sessionID))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		e.t.Fatalf("harness: read transcript: %v", err)
	}
	return string(b)
}

// Result is what a run produced.
type Result struct {
	Output string
	Code   int
}

// Saw reports whether text appears anywhere in the stream — the tool results a
// refusal travels back in, or the agent's own output.
func (r Result) Saw(text string) bool { return strings.Contains(r.Output, text) }

// blockedMarkers are what the harness emits when a PreToolUse hook refused the
// call. Measured through this harness, one channel per run, rather than guessed:
// the two delivering channels do NOT share a marker.
//
//   - `permissionDecision: "deny"` at exit 0 — the channel this engine uses, see
//     deny in hookio.go — turns the tool call into a tool_result with is_error
//     true whose content is
//
//     [{"text":"Tool call blocked by a PreToolUse hook: <reason>","type":"text"}]
//
//   - Exiting 2 with the reason on stderr never becomes a tool_result at all.
//     The harness reports it on its own line, "claude-mock: PreToolUse hook
//     blocked: ...", and the run carries no tool result for that call.
//
// Both are listed because a refusal is a refusal whichever channel carried it,
// and a predicate that knew only the engine's current channel would silently
// start answering "permitted" the day that changed. See refuseForBroken in
// services/sr-session for the full measured table, including the channels that
// deliver nothing.
var blockedMarkers = []string{
	"Tool call blocked by a PreToolUse hook",
	"PreToolUse hook blocked",
}

// Refused reports whether the action was stopped before it happened.
//
// It reads the harness's own refusal marker rather than scanning the stream for
// words. Two copies of a helper that scanned for "deny"/"denied"/"block"/
// "blocked" anywhere in the output shipped in 013 and 014, and the stream
// contains the agent's own tool input — the path it asked to write, and the
// content. So a guardrail permitting EVERYTHING, writing to `deny/notes.md`,
// produced "File written successfully" and a helper that answered "refused".
//
// That is not a cosmetic flaw. Every test asserting a refusal would pass on a
// fully permitted write as soon as a trigger word appeared in the fixture, which
// is precisely the reading a suite about fail-open must never get wrong. The
// eight refusal-asserting tests in 013 and 014 were non-vacuous only by the
// accident of using clean paths.
//
// One definition, in the harness, because both packages need the same answer and
// two copies of a predicate are two chances to be wrong about it.
func (r Result) Refused() bool {
	for _, marker := range blockedMarkers {
		if strings.Contains(r.Output, marker) {
			return true
		}
	}
	return false
}

// Permitted reports whether the action went through.
//
// The complement of Refused, named separately because that is how the assertions
// read at the call sites and a negation there is easy to misread.
func (r Result) Permitted() bool { return !r.Refused() }

// Run drives a scenario through the mock as a real session.
func (e *Env) Run(projDir, sessionID, prompt string, s Scenario) Result {
	e.t.Helper()
	return e.run(projDir, projDir, sessionID, prompt, s)
}

// RunFrom drives a scenario as a session whose hooks fire from a SUBDIRECTORY
// of the repository, the way a session working under `cd internal/foo` runs.
//
// WHAT IS BEING MOVED, because only one thing can be and the other was measured
// rather than assumed. The mock reports its `--project-dir` as the `cwd` on
// every hook payload, and it runs the agent's own Bash turns there too — the
// PROCESS working directory is ignored for both. Measured against
// a10n-claude-mock: launching it from a subdirectory with --project-dir at the
// root yields `"cwd":"<root>"` on the Stop payload, so a harness that moved only
// cmd.Dir would change nothing the engine can see, and every test resting on it
// would be driving an ordinary root session while claiming otherwise.
//
// So the subdirectory is passed as the project dir. That is exactly the
// arrangement the defect is about: `cwd` on the payload is a directory BELOW the
// repository root, which is the one fact the engine has to reconcile.
//
// Why it matters. Everything that identifies a session derives from that
// reported directory. The state database is keyed by the workspace anchor, and
// the difference is rooted where the hook was invoked. Keyed on the RAW cwd, a
// hook reporting a subdirectory opens a DIFFERENT database — an empty baseline,
// and every verdict the session recorded unreachable, silently and mid-session.
// The anchor now resolves to the git root (services/sr-session/statedir.go,
// workspaceAnchor), which is what makes a root cycle and a subdirectory cycle
// agree they are in one tree.
//
// The TRANSCRIPT follows the reported directory, and it has to. A harness keys
// a conversation's record by the project dir it was given, so the mock writes
// this session's record under the SUBDIRECTORY's encoding and puts that path on
// every hook payload. Seeding at the repository root instead produces two files
// for one session: the seeded one nothing reads, and the mock's own, which has
// no parentless root record for the identity walk to land on — so the walk falls
// through to a tool_use uuid and the session keys its state under that. Measured:
// state.db appeared under `e2e-turn-w1` rather than `e2e-root-<session>`, the
// baseline read back empty, and every verdict was re-judged the next cycle.
//
// The consequence for a caller is worth stating plainly: a cycle run with Run
// and a cycle run with RunFrom are DIFFERENT conversations, because their
// records sit in different directories. A test needing one session across
// several cycles must therefore drive all of them the same way. Whether the
// engine keys ONE session's state alike from the root and from a subdirectory —
// the workspace anchor — cannot be observed through this harness for that
// reason; the note where T025_03 used to sit records what was measured, and the
// claim is pinned as a unit test instead.
//
// The GUARDRAIL is likewise loaded from the reported directory: the engine looks
// for `.sloprail/` beside the cwd it was given. A test using this must put the
// rule where the cycle will look for it, and read that rule's ledger from the
// same place.
func (e *Env) RunFrom(projDir, subRel, sessionID, prompt string, s Scenario) Result {
	e.t.Helper()
	workDir := filepath.Join(projDir, subRel)
	if info, err := os.Stat(workDir); err != nil || !info.IsDir() {
		e.t.Fatalf("harness: RunFrom %q: not a directory in the project (%v) — "+
			"the agent cannot work from somewhere that is not there, and a test resting on "+
			"this would be driving the session from the project root without saying so", subRel, err)
	}
	// The mock resolves `.claude/settings.json` from the project dir it is given,
	// so the plugin has to be enabled where this session will look for it.
	// Without this the mock finds no settings, fires no lifecycle hooks at all,
	// and the cycle is silent — which a test reading an empty ledger would
	// happily report as the engine correctly staying quiet.
	//
	// It is the SAME settings Project() writes: a marketplace source and an
	// enabled plugin, exactly as a user would install them. Nothing about the
	// wiring differs — only where it sits, which is what a session reporting this
	// directory requires.
	if err := os.MkdirAll(filepath.Join(workDir, ".claude"), 0o755); err != nil {
		e.t.Fatalf("harness: RunFrom %q: mkdir .claude: %v", subRel, err)
	}
	e.writeSettings(workDir)
	return e.run(projDir, workDir, sessionID, prompt, s)
}

// RunReal drives the operator's ACTUAL `claude` against a project, and is the
// one thing in this harness that is not sandboxed.
//
// It exists for a single property that the mock is definitionally unable to
// show: that SLOPRAIL_LAUNCHED_BY, set by the engine on an outer hook, survives
// the exec into a real harness and is loaded by that harness into its own hooks'
// environment. Everything after the first link in that chain belongs to a
// program this repo does not own, so a mock asserting it would be asserting its
// own construction. See test_015_06_real_agent_test.go.
//
// # Why HOME is NOT overridden here
//
// Every other path in this file replaces HOME and CLAUDE_CONFIG_DIR so a run
// cannot touch the host's claude data. This one cannot: a real `claude` reads
// its credentials from the operator's own config, and under the isolated HOME it
// has none and exits without running. The isolation and the property are
// mutually exclusive, and the property is the one that was reopened as P1.
//
// So the trade is made explicitly rather than by accident: the caller's real
// environment is inherited, only the PATH is prepended so the plugin's hooks
// reach the binaries under test, and the caller must opt in through an
// environment variable because this spends money. No test may call this without
// that gate.
//
// The project's own settings.json still points at THIS repo's marketplace, so
// what fires inside the session is the plugin under test even though the
// harness's config directory is not in play.
func (e *Env) RunReal(projDir, prompt string) Result {
	e.t.Helper()
	if os.Getenv("SLOPRAIL_REAL_AGENT") != "1" {
		e.t.Fatalf("harness: RunReal without SLOPRAIL_REAL_AGENT=1 — it bills the operator")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		e.t.Fatalf("harness: RunReal: no `claude` on PATH: %v", err)
	}
	cmd := exec.Command(bin,
		"-p", "--model", "haiku",
		// The outer cap. The inner agent carries its own, passed by the
		// guardrail's script, because that is where a runaway would spend.
		"--max-budget-usd", "0.20",
		"--allowed-tools", "Write",
		"--", prompt,
	)
	cmd.Dir = projDir
	cmd.Env = append(os.Environ(),
		"PATH="+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		e.t.Fatalf("harness: run real claude: %v\n%s", err, out)
	}
	e.t.Logf("real claude:\n%s", out)
	return Result{Output: string(out), Code: code}
}

// run drives the mock with the transcript's project root and the directory the
// session reports given separately. They are the same for an ordinary session;
// RunFrom is what separates them.
func (e *Env) run(projDir, workDir, sessionID, prompt string, s Scenario) Result {
	e.t.Helper()

	// Seeded where the MOCK will write it, which is keyed on the directory the
	// session reports rather than on the repository root. See RunFrom: seeding
	// elsewhere leaves the mock's own record without a parentless root, and the
	// identity walk then keys the session on a tool_use uuid.
	e.seedTranscript(workDir, sessionID, prompt)

	scriptPath := filepath.Join(projDir, ".scenario.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"+s.script()+"\n"), 0o755); err != nil {
		e.t.Fatalf("harness: write scenario: %v", err)
	}

	cmd := exec.Command(e.mock,
		"-p", "--output-format", "stream-json",
		"--script", scriptPath,
		// The directory the session reports, which RunFrom may place below the
		// repository root. The mock echoes this as `cwd` on every hook payload.
		"--project-dir", workDir,
		"--config-dir", e.configDir,
		"--plugin-cache-dir", e.pluginDir,
		"--session-id", sessionID,
		prompt,
	)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"HOME="+e.home,
		"CLAUDE_CONFIG_DIR="+e.configDir,
		"CLAUDE_CODE_SESSION_ID="+sessionID,
		"CLAUDE_CODE_PLUGIN_CACHE_DIR="+e.pluginDir,
		// The mock STANDS IN FOR Claude Code, so it must present the environment
		// Claude Code presents — including the two variables that name the running
		// harness. Real Claude Code sets CLAUDECODE=1 on every session (and
		// CLAUDE_CODE_ENTRYPOINT names how it launched); sr-agent reads exactly
		// these to detect its harness (services/sr-agent/harness.go: "CLAUDECODE is
		// what Claude Code sets on every session"), and a judge's `sr-agent
		// --model … --verify` REFUSES with ErrNoHarness when neither is set. A
		// developer running the suite inside Claude Code inherits CLAUDECODE from
		// their own session and never sees this; CI does not, so a judge test that
		// passed locally failed in CI with "no supported harness detected" until
		// the mock's env carried these explicitly. Set here (not left to
		// os.Environ inheritance) so the two environments behave identically.
		"CLAUDECODE=1",
		"CLAUDE_CODE_ENTRYPOINT=cli",
		// The plugin invokes `sloprail`; this is how the hook subprocess finds
		// the build under test rather than whatever happens to be installed.
		//
		// The shim dir goes FIRST, ahead of both the build dir and the real
		// PATH. A test whose hook launches an agent must not reach the
		// operator's actual `claude` — see InstallClaudeShim. When no shim was
		// installed the directory is simply empty and this changes nothing.
		"PATH="+e.shimDir+string(os.PathListSeparator)+
			e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		e.t.Fatalf("harness: run mock: %v\n%s", err, out)
	}
	e.t.Logf("mock:\n%s", out)
	return Result{Output: string(out), Code: code}
}
