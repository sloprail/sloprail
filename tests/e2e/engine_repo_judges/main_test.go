// The engine repo's OWN judges — rule-quality and skill-quality, the two
// file-guards in this repo's .sloprail/ that judge the repo's own rules and skills.
//
// Each test here maps to an invariant those judges must uphold, driven through
// the claude-MOCK exactly as the rest of the e2e is: the harness runs
// a10n-claude-mock, whose tool calls fire this repo's real plugin, which reaches
// the real file-guard files out of .sloprail/file-guard/. Only the MODEL the
// judge itself invokes is replaced — by stubJudge, which writes a fixed verdict to
// the path the judge's prompt names — because a check whose whole subject is the
// SCRIPT's behaviour (which field it reads, how it parses the verdict, which stage
// it fires at) must be a deterministic reproduction, not a race against a model's
// output.
//
// Nothing here restates a declaration or a check: a test carrying its own copy
// would prove the copy works and say nothing about the files that are actually
// enforcing. The file-guards are installed FROM this repo's own .sloprail/ and
// committed before the cycle runs.
//
// (These invariants were first written as a round-3 adversarial review of the two
// judges — the tests formerly under tests/e2e/review3. They are ordinary e2e now,
// named for the invariant each proves rather than for the review that found it.
// The judges were migrated from the deprecated GUARDRAIL.md hooks format to the
// file-guard nature in Wave-3; these tests were retargeted to install and drive
// the new .sloprail/file-guard/<name> form, proving the SAME invariants against
// it: the Pre stage reads the pending body from the flat event, the narrow
// verdict parse refuses a flagged verdict carrying two objects, and the Post
// after-check judges a create the engine could not derive.)
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

var (
	New   = harness.New
	Turns = harness.Turns
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// installGuardrail copies one of THIS repo's own .sloprail/file-guard/<name>
// into a test project, verbatim and recursively — the rules/ subtree is the
// whole standard for these two judges, so a copier that skipped directories
// would install a judge with an empty rules/ and every test would observe the
// empty-rules refusal instead of the behaviour under test. The file-guard.yaml,
// RUBRIC.md, judge-*.sh and the whole rules/ tree all come across, so the
// installed guard is byte-for-byte the one enforcing in this repo.
func installGuardrail(t *testing.T, projDir, name string) string {
	t.Helper()

	src := filepath.Join(repoRoot(t), ".sloprail", "file-guard", name)
	dst := filepath.Join(projDir, ".sloprail", "file-guard", name)
	copyTree(t, src, dst)
	return dst
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()

	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("copyTree: mkdir %s: %v", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("copyTree: read %s: %v", src, err)
	}
	if len(entries) == 0 {
		t.Fatalf("copyTree: %s is empty", src)
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyTree(t, s, d)
			continue
		}
		body, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("copyTree: read %s: %v", s, err)
		}
		info, err := e.Info()
		if err != nil {
			t.Fatalf("copyTree: stat %s: %v", s, err)
		}
		// The mode carries over. A file-guard check script arriving without its
		// execute bit is refused by the engine for being unrunnable (fail-closed,
		// internal/dispatch runScriptCheck), and a test would then pass its
		// refusal assertions for entirely the wrong reason.
		if err := os.WriteFile(d, body, info.Mode().Perm()); err != nil {
			t.Fatalf("copyTree: write %s: %v", d, err)
		}
	}
}

// project builds a git-backed project with one of this repo's own file-guards
// installed and committed.
//
// The commit is not incidental. The engine reads a git diff to decide which
// paths a cycle changed, so a file-guard's own files must already be in history
// before the cycle under test runs — otherwise the guard's own rules/*/RULE.md
// files (which `match: path endsWith "RULE.md"` selects) are part of the cycle's
// changes and the judge fires on them, which turns every assertion below into a
// statement about the wrong file.
func project(t *testing.T, e *harness.Env, guardrail string) string {
	t.Helper()

	proj := e.Project()
	e.GitInit(proj)
	installGuardrail(t, proj, guardrail)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install "+guardrail)
	return proj
}

// stubJudge writes a fixed verdict body to the path the prompt names, standing
// in for `claude` via A10N_CLAUDE_BIN.
//
// It reads the prompt on stdin and recovers the verdict path from it, exactly
// as the real judge does — so the isolation flags, the /tmp cwd and the path
// the script chose are all still exercised. Only the model's judgement is
// replaced.
func stubJudge(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	script := `#!/bin/sh
# Consume the prompt and recover the verdict path from it: the last /tmp/*.json
# token the prompt names. The real judge is told the path the same way.
prompt="$(cat)"
verdict="$(printf '%s' "$prompt" | tr ' ' '\n' | grep '^/tmp/.*\.json$' | tail -1)"
[ -n "$verdict" ] || exit 1
cat > "$verdict" <<'VERDICT_EOF'
` + body + `
VERDICT_EOF
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("stubJudge: write: %v", err)
	}
	return path
}

// sawRefusal reports whether any recorded refusal carries the given text.
func sawRefusal(errs []string, want string) bool {
	for _, e := range errs {
		if strings.Contains(e, want) {
			return true
		}
	}
	return false
}

// underivableWrite is a shell command whose output bytes the engine will not
// predict, so no Pre event is emitted for the file it creates. This is the exact
// tier a create the engine cannot see escapes through — so it is the case the
// Post binding has to cover.
func underivableWrite(path, body string) harness.Turn {
	return harness.Bash("w1", "printf '%s\\n' "+shqLit(body)+" | tr -d '\\r' > "+path)
}

// shqLit single-quotes for the shell, for embedding in a Bash turn.
func shqLit(s string) string {
	out := "'"
	for _, r := range s {
		if r == '\'' {
			out += `'\''`
			continue
		}
		out += string(r)
	}
	return out + "'"
}

// shell runs a shell line and returns its trimmed stdout, for the few
// assertions whose subject IS a shell pipeline rather than a session.
func shell(t *testing.T, line string) string {
	t.Helper()
	out, err := exec.Command("sh", "-c", line).Output()
	if err != nil {
		t.Fatalf("shell %q: %v", line, err)
	}
	return strings.TrimSpace(string(out))
}

// shq single-quotes a string for the shell.
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
