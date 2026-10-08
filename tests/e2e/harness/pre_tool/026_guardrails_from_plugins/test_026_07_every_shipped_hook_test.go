package e2e

import (
	"encoding/json"
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every hook script this repo ships — its own .sloprail, every
// marketplace plugin, the example rule trees kept under tests/**/testdata — must pass authoring-slop's own grep. authoring-slop has a
// PreFileWrite gate half: a shipped script its grep refuses can never be edited again in a
// project that has the plugin (the edit is refused), which is how 16 scripts
// became uneditable when the Post newContentKnown floor landed (issue #91).

func repoRootDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// shippedHookScripts is every `.sh` under a `.sloprail/{file-guard,gate,context}/`
// in the repo, as the workspace-relative paths authoring-slop matches.
func shippedHookScripts(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			switch {
			case rel == ".git", rel == "bin", rel == ".bin", d.Name() == "node_modules",
				strings.HasPrefix(rel, filepath.Join(".claude", "worktrees")):
				return filepath.SkipDir
			}
			return nil
		}
		slash := filepath.ToSlash(rel)
		// The example rule trees the e2e tests keep (no leading dot):
		// tests/**/testdata/<name>/sloprail/{file-guard,gate,context}/**.sh.
		kept := strings.HasPrefix(slash, "tests/") && strings.Contains(slash, "/testdata/") &&
			(strings.Contains(slash, "/sloprail/file-guard/") || strings.Contains(slash, "/sloprail/gate/") || strings.Contains(slash, "/sloprail/context/"))
		if strings.HasSuffix(slash, ".sh") &&
			(strings.Contains(slash, ".sloprail/file-guard/") || strings.Contains(slash, ".sloprail/gate/") || strings.Contains(slash, ".sloprail/context/") || kept) {
			out = append(out, slash)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repo: %v", err)
	}
	return out
}

// T026_07: authoring-slop's grep (check-rules.sh) passes every shipped hook.
func TestT026_07_EveryShippedHookPassesTheGrep(t *testing.T) {
	root := repoRootDir(t)
	check := filepath.Join(root, "marketplace/plugins/sloprail/.sloprail/gate/authoring-slop/check-rules.sh")
	scripts := shippedHookScripts(t, root)
	if len(scripts) < 50 {
		t.Fatalf("found only %d shipped hook scripts — the walk is not reaching them", len(scripts))
	}
	// Plus the fixture plugin's scripts (fixture_hooks_test.go), read from a temp
	// tree and checked under their `.sloprail/...` rel paths.
	fixtures := writeFixtureHooks(t)
	for rel := range fixtureHooks {
		scripts = append(scripts, "fixture:"+rel)
	}
	for _, rel := range scripts {
		t.Run(rel, func(t *testing.T) {
			src, path := root, rel
			if strings.HasPrefix(rel, "fixture:") {
				src, path = fixtures, strings.TrimPrefix(rel, "fixture:")
			}
			body, err := os.ReadFile(filepath.Join(src, path))
			if err != nil {
				t.Fatal(err)
			}
			// A kept example's rule tree is `testdata/<name>/sloprail/…` (no dot);
			// installed in a project it is `.sloprail/…`, and the grep's
			// file-guard exemption is by that location. Check it under that path.
			rel := path
			if i := strings.Index(rel, "/testdata/"); i >= 0 {
				rest := rel[i+len("/testdata/"):]
				if j := strings.Index(rest, "/"); j >= 0 {
					rel = "." + rest[j+1:]
				}
			}
			payload, _ := json.Marshal(map[string]any{"event": map[string]any{
				"kind": "PreFileUpdate", "path": rel, "newContent": string(body), "resultKnown": true,
			}})
			cmd := exec.Command("bash", check)
			cmd.Dir = filepath.Dir(check)
			cmd.Env = append(harness.HostEnv(), "SR_WORKSPACE="+root)
			cmd.Stdin = strings.NewReader(string(payload))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("authoring-slop refuses the shipped %s — it could never be edited again:\n%s", rel, out)
			}
		})
	}
}

// T026_08: every shipped script that reads a Post kind's newContent fails
// closed when the engine could not read the settled file (newContentKnown
// false): a check refuses (non-zero), a prepare fails (non-zero), a `when`
// applies its requirement (exit 0), a context activates in its stricter state.
// Before, each read the unread "" as the file.
func TestT026_08_EveryPostReaderFailsClosedOnAnUnreadFile(t *testing.T) {
	root := repoRootDir(t)
	const (
		refuses  = "refuses"  // non-zero exit
		applies  = "applies"  // `when`: exit 0
		judges   = "judges"   // prepare: exit 0 with additionalContext, never skip
		activate = "activate" // context enter: exit 0 with a payload
	)
	for _, tc := range []struct {
		script, kind, want string
		// workspace, when set, builds SR_WORKSPACE; otherwise an empty temp dir.
		workspace func(t *testing.T) string
	}{
		{".sloprail/file-guard/rule-quality/prepare.sh", "PostFileUpdate", refuses, nil},
		{".sloprail/file-guard/skill-quality/prepare.sh", "PostFileUpdate", refuses, nil},
		{"marketplace/plugins/sloprail-content/.sloprail/file-guard/unit-satisfies-rules/prepare-judge-rules.sh", "PostFileUpdate", refuses, nil},
		{"marketplace/plugins/sloprail-content/.sloprail/file-guard/unit-publish-approved/enters-published.sh", "PostFileUpdate", applies, nil},
		{"marketplace/plugins/sloprail-content/.sloprail/file-guard/unit-publish-approved/check-publish.sh", "PostFileUpdate", refuses, nil},
		{"marketplace/plugins/sloprail-content/.sloprail/file-guard/content-rule-is-grounded/check-rule.sh", "PostFileUpdate", refuses, nil},
		{"marketplace/plugins/sloprail-content/.sloprail/file-guard/content-rule-is-grounded/resolve-cited-rule-quotes.sh", "PostFileCreate", refuses, nil},
		{"marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-dependencies-resolve/check-dependencies.sh", "PostFileUpdate", refuses, nil},
		{"marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-evidence-resolves/in-review.sh", "PostFileUpdate", applies, nil},
		{"marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-evidence-resolves/check-task.sh", "PostFileUpdate", refuses, nil},
		{"marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-body-is-human-authored/body-changed.sh", "PostFileUpdate", applies, nil},
		{"marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-body-is-human-authored/body-is-stated.sh", "PostFileUpdate", refuses, nil},
		{"marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-body-is-human-authored/resolve-cited-messages.sh", "PostFileUpdate", refuses, nil},
		{"marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-gates-hold/prepare-judgment-gates.sh", "PostFileUpdate", refuses, nil},
		{"marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-gates-hold/gates-hold.sh", "PostFileUpdate", refuses, nil},
		{"marketplace/plugins/sloprail-tasks/.sloprail/file-guard/task-gate-is-grounded/resolve-gate-context.sh", "PostFileUpdate", refuses, nil},
		// A real kept example's context enter (the eval-loop-maxing goal-tracking
		// context, run by its own e2e test): an unread goal.yaml is in force.
		{"tests/e2e/harness/session/059_eval_loop_maxing/testdata/eval-loop-maxing/sloprail/context/goal-tracking/enter.sh", "PostFileUpdate", activate, nil},
		// The fixture plugin's scripts (fixture_hooks_test.go), under a temp tree.
		// A git repository where committed code pins the file: an unread file
		// nothing pins is waived (nothing is at stake), so that fixture must pin it.
		{"fixture:.sloprail/file-guard/when-pinned/pinned.sh", "PostFileUpdate", applies, pinnedTaskRepo},
		{"fixture:.sloprail/file-guard/when-drops/drops.sh", "PostFileUpdate", applies, nil},
		{"fixture:.sloprail/file-guard/prepare-judges/prepare.sh", "PostFileUpdate", judges, nil},
		{"fixture:.sloprail/context/goal/enter.sh", "PostFileUpdate", activate, nil},
	} {
		t.Run(tc.script, func(t *testing.T) {
			base := root
			if rel, ok := strings.CutPrefix(tc.script, "fixture:"); ok {
				tc.script = filepath.Join(writeFixtureHooks(t), rel)
				base = ""
			}
			dir := filepath.Join(base, filepath.Dir(tc.script))
			payload, _ := json.Marshal(map[string]any{
				"event": map[string]any{
					"kind": tc.kind, "path": "memories/tasks/a/b/TASK.md",
					"oldContent": "old line\n", "newContent": "", "newContentKnown": false,
				},
				"transcriptPath": "/nonexistent",
			})
			cmd := exec.Command("bash", "./"+filepath.Base(tc.script))
			cmd.Dir = dir
			ws := t.TempDir()
			if tc.workspace != nil {
				ws = tc.workspace(t)
			}
			cmd.Env = append(harness.HostEnv(), "SR_GUARDRAIL=t", "SR_GUARDRAIL_DIR="+dir, "SR_WORKSPACE="+ws)
			cmd.Stdin = strings.NewReader(string(payload))
			out, err := cmd.Output()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			switch tc.want {
			case refuses:
				if code == 0 {
					t.Errorf("an unread settled file was passed (exit 0): %s", out)
				}
			case applies:
				if code != 0 {
					t.Errorf("an unread settled file waived the requirement (exit %d): %s", code, out)
				}
			case judges:
				if code != 0 || !strings.Contains(string(out), "additionalContext") {
					t.Errorf("an unread settled file skipped the judge (exit %d): %s", code, out)
				}
			case activate:
				if code != 0 || !strings.Contains(string(out), `"goal"`) {
					t.Errorf("an unread goal did not activate the context (exit %d): %s", code, out)
				}
			}
		})
	}
}

// pinnedTaskRepo is a git repository whose committed code carries an
// sr:invariant marker pinning memories/tasks/a/b/TASK.md L1 — the path T026_08's
// payload names — so a rule that asks "does anything pin this path?" finds it
// pinned.
func pinnedTaskRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.MkdirAll(filepath.Join(repo, "memories/tasks/a/b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "memories/tasks/a/b/TASK.md"), []byte("old line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code := "package x\n\n// sr:invariant " + repo + "@0123456789abcdef0123456789abcdef01234567:memories/tasks/a/b/TASK.md#L1-1\nfunc f() {}\n"
	if err := os.WriteFile(filepath.Join(repo, "x.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "pinned")
	return repo
}
