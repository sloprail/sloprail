package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This package is the end-to-end for the research-rigor USE CASE: a research
// run declared with a `#research` tag (or handed to a sub-agent whose prompt
// carries it) must show depth — a `git clone` this run made, and at least two
// of that clone's source files read (not its README or docs) — and, if it ran
// as a sub-agent, no sibling trajectories. A context (`research-run`) activates
// on the tag; a gate (`depth-check`, Stop, `match: context["research-run"].active`,
// `require: context: research-run`) runs verify-depth.sh and blocks the Stop on a
// shortfall.
//
// The gate reads the trajectory (sr-session trajectory normalize), so these
// scenarios make it real: the mock EXECUTES each Bash and Read turn, so a clone
// is a real `git clone` of a local repository (sourceRepo) and a read is a real
// read of a file on disk. A clone that failed or a read that errored is not
// credited, exactly as for a real agent.
//
// Coverage: activation (and not on other tags); a shallow run refused; clone +
// source reads admitted; clone + README/docs only refused; reads of a directory
// this run did not clone refused (the stale-/tmp leak a real run hit); a failed
// clone into an existing directory refused; sub-agent research aggregated for
// the dispatcher; and the command shapes the clone destination and the reads
// are parsed from (git -C, --depth=1 / --depth 1, cd && git clone, a subshell,
// a scratch dir outside the project, Read vs cat/sed/head/grep, the Grep tool,
// a destination built from a variable); and what a review found admitted: a
// clone whose failure was hidden, searches that read no source, an unreadable
// trajectory reported as "not cloned", Markdown writes by letter case, and the
// eval scorer's claim about gates that never ran.
var (
	New     = harness.New
	Turns   = harness.Turns
	Bash    = harness.Bash
	Say     = harness.Say
	SayBash = harness.SayBash
)

// Read is a turn where the agent reads a file with the Read tool.
func Read(id, path string) harness.Turn {
	return harness.ToolUse(id, "Read", map[string]string{"file_path": path})
}

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const exampleName = "research-rigor"

func installExampleTree(t *testing.T, projDir, name string) {
	t.Helper()
	src := filepath.Join(repoRoot(t), "examples", name, ".sloprail")
	dst := filepath.Join(projDir, ".sloprail")
	copyExampleTree(t, src, dst)
}

func copyExampleTree(t *testing.T, src, dst string) {
	t.Helper()
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("install example: read %s: %v", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("install example: mkdir %s: %v", dst, err)
	}
	for _, ent := range ents {
		s := filepath.Join(src, ent.Name())
		d := filepath.Join(dst, ent.Name())
		if ent.IsDir() {
			copyExampleTree(t, s, d)
			continue
		}
		body, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("install example: read %s: %v", s, err)
		}
		info, err := ent.Info()
		if err != nil {
			t.Fatalf("install example: stat %s: %v", s, err)
		}
		if err := os.WriteFile(d, body, info.Mode().Perm()); err != nil {
			t.Fatalf("install example: write %s: %v", d, err)
		}
	}
}

// research returns an environment and a project with the shipped example
// installed and committed.
func research(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install")
	return e, proj
}

// sourceRepo creates a local git repository named name that implements a small
// retry library — a README, a docs page, and three source files — for a scenario
// to clone. Local, so a clone is real and deterministic without the network.
func sourceRepo(t *testing.T, e *harness.Env, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	files := map[string]string{
		"README.md":      "# retry-lib\n\nRetries with exponential backoff.\n",
		"docs/guide.md":  "# Guide\n\nCall retry(fn).\n",
		"index.js":       "module.exports = require('./lib/retry');\n",
		"lib/retry.js":   "const backoff = require('./backoff');\nmodule.exports = async function retry(fn, n = 5) {\n  for (let i = 0; ; i++) {\n    try { return await fn(); } catch (e) { if (i >= n) throw e; await backoff(i); }\n  }\n};\n",
		"lib/backoff.js": "module.exports = (i) => new Promise((r) => setTimeout(r, Math.min(1000 * 2 ** i, 30000) * Math.random()));\n",
		"package.json":   "{\"name\": \"retry-lib\", \"main\": \"index.js\"}\n",
	}
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("source repo: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("source repo: %v", err)
		}
	}
	e.GitInit(dir)
	return dir
}

// staleClone makes dst a checkout of src left over from an EARLIER session: git
// dates the clone in its reflog (.git/logs/HEAD) with the committer date, so it
// is set well before this session began — as a real leftover's would be. (A
// clone made seconds before the session, in the same second as its first
// record, would read as made during it.)
func staleClone(t *testing.T, src, dst string) {
	t.Helper()
	cmd := exec.Command("git", "clone", "-q", src, dst)
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE=2020-01-01T00:00:00Z")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stale clone: %v\n%s", err, out)
	}
}

// scratch is a directory outside the project — where a real agent clones (its
// scratchpad, /tmp). On macOS it sits under /var, a link to /private/var; the
// gate reports both spellings as /var, which is how a refusal quotes it.
func scratch(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}
