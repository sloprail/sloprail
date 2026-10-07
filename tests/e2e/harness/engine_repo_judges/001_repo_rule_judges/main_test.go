// The engine repo's OWN judges — rule-quality and skill-quality, the two rules in
// this repo's .sloprail/ that judge the repo's own rules and skills. Each is a
// pair of natures under one name: a plain FILE-GUARD (.sloprail/file-guard/<name>/)
// that judges the committed changeset at Stop, and a PreFileWrite GATE
// (.sloprail/gate/<name>/) whose only check is the deterministic size cap (the
// judge never runs in a gate), sharing size-cap-lib.sh with the file-guard.
// A test installs only the nature it is about, except the size-cap test, which
// needs both (the gate sources the lib from the file-guard folder).
//
// Each test here maps to an invariant those judges must uphold, driven through
// the claude-MOCK exactly as the rest of the e2e is: the harness runs
// a10n-claude-mock, whose tool calls fire this repo's real plugin, which reaches
// the real gate and file-guard files out of .sloprail/. Only the MODEL the
// judge itself invokes is replaced — by InstallJudgeClaude, which puts a `claude`
// on PATH that reads the prompt sr-agent hands it and writes a fixed verdict to
// the output file sr-agent named — because a check whose whole subject is the
// judge's behaviour (which field the prompt carries, how the verdict is read,
// which stage it fires at) must be a deterministic reproduction, not a race
// against a model's output.
//
// # These are JUDGE checks now
//
// The two guardrails were SCRIPT checks that called `claude` directly, stubbed
// through A10N_CLAUDE_BIN. They are JUDGE checks now (prepare.sh assembles the
// rubric; the engine, via sr-agent, runs the model and constrains the verdict).
// So the stub is InstallJudgeClaude, the same mechanism every other judge e2e in
// this repo uses, and the verdict shape is the engine's `{"pass": ..., "reasoning": ...}`
// rather than the old `{"has_issues": ...}` — a FAILING verdict is now
// `pass: false`, where it was `has_issues: true`. The invariants are unchanged;
// only the substrate the verdict travels through is.
//
// Nothing here restates a declaration or a check: a test carrying its own copy
// would prove the copy works and say nothing about the files that are actually
// enforcing. The rules are installed FROM this repo's own .sloprail/ and
// committed before the cycle runs.
//
// (These invariants were first written as a round-3 adversarial review of the two
// judges — the tests formerly under tests/e2e/review3. They are ordinary e2e now,
// named for the invariant each proves rather than for the review that found it.
// The judges were migrated from the deprecated GUARDRAIL.md hooks format to the
// file-guard nature in Wave-3, from a hand-rolled script check to a judge: check
// in the PR-19 review, and split into gate + file-guard when a file-guard lost its
// Pre binding; these tests were retargeted each time, proving the SAME invariants:
// the GATE reads the pending body from the flat event, the narrow verdict parse
// refuses a flagged verdict carrying two objects, and the FILE-GUARD judges a
// create the engine could not derive.)
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

// The natures a test can install one of this repo's rules as.
const (
	gateNature      = "gate"
	fileGuardNature = "file-guard"
)

// installGuardrail copies one of THIS repo's own .sloprail/<nature>/<name>
// into a test project, verbatim and recursively — the rules/ subtree is the
// whole standard for these two judges, so a copier that skipped directories
// would install a judge with an empty rules/ and every test would observe the
// empty-rules refusal instead of the behaviour under test. The gate.yaml or
// file-guard.yaml, prepare.sh, judge-*.md.j2 and the whole rules/ tree all come
// across, so the installed rule is byte-for-byte the one enforcing in this repo.
func installGuardrail(t *testing.T, projDir, nature, name string) string {
	t.Helper()

	src := filepath.Join(repoRoot(t), ".sloprail", nature, name)
	dst := filepath.Join(projDir, ".sloprail", nature, name)
	copyTree(t, src, dst)
	installSizeCapLib(t, projDir, name)
	return dst
}

// installSizeCapLib places the one size-cap-lib.sh (rule-quality's, which skill-quality
// sources too) beside an installed rule-quality or skill-quality, where the rule's
// scripts look for it (../rule-quality/ from the file-guard, ../../file-guard/rule-quality/
// from the gate).
func installSizeCapLib(t *testing.T, projDir, name string) {
	t.Helper()
	if name != "rule-quality" && name != "skill-quality" {
		return
	}
	rel := filepath.Join(".sloprail", "file-guard", "rule-quality", "size-cap-lib.sh")
	dst := filepath.Join(projDir, rel)
	if _, err := os.Stat(dst); err == nil {
		return
	}
	body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("installSizeCapLib: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("installSizeCapLib: %v", err)
	}
	if err := os.WriteFile(dst, body, 0o644); err != nil {
		t.Fatalf("installSizeCapLib: %v", err)
	}
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

// project builds a git-backed project with one of this repo's own GATES
// installed and committed (the write-time judge: it refuses before the write lands).
// guardProject is the same for the FILE-GUARD of that name (the Stop after-check).
//
// The commit is not incidental. The engine reads a git diff to decide which
// paths a cycle changed, so the rule's own files must already be in history
// before the cycle under test runs — otherwise the rule's own rules/*/RULE.md
// files (which `match: path endsWith "RULE.md"` selects) are part of the cycle's
// changes and the judge fires on them, which turns every assertion below into a
// statement about the wrong file.
func project(t *testing.T, e *harness.Env, guardrail string) string {
	t.Helper()
	return projectOf(t, e, gateNature, guardrail)
}

// guardProject is project for the plain file-guard nature.
func guardProject(t *testing.T, e *harness.Env, guardrail string) string {
	t.Helper()
	return projectOf(t, e, fileGuardNature, guardrail)
}

func projectOf(t *testing.T, e *harness.Env, nature, guardrail string) string {
	t.Helper()

	proj := e.Project()
	e.GitInit(proj)
	installGuardrail(t, proj, nature, guardrail)
	e.CommitAll(proj, "install "+nature+" "+guardrail)
	return proj
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
