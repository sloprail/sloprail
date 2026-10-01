package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	marketplaceName = "sloprail-marketplace"
	pluginName      = "sloprail"
	pluginKey       = pluginName + "@" + marketplaceName
)

// workspace is one isolated run: a copy of the fixture's seed tree, wired to
// load THIS repo's sloprail plugin exactly as a real install would.
//
// CLAUDE_CONFIG_DIR is deliberately NOT isolated (see run.go's launchAgent):
// on macOS, Claude Code's auth is a host-auth refresh scoped to the real
// ~/.claude, so isolating it loses auth entirely. Only the project tree is
// isolated — a fresh temp directory, so its transcripts cannot collide with a
// real project's.
type workspace struct {
	root    string // temp dir: root/project is the agent's cwd
	project string

	// seedCommit and rulesCommit are the shas of the two setup commits
	// commitSetup made (rulesCommit empty when there were no rules). Recorded
	// here, and handed to the scorer, rather than found later by message: the
	// agent can write a commit with any message.
	seedCommit, rulesCommit string
}

// newWorkspace creates an isolated workspace and populates project/ from the
// fixture's base (a local Seed directory, copied, or a real Repo, cloned at
// its pinned Ref) and then, if the fixture declares one, its Overlay on top.
//
// Everything is copied or cloned fresh, never symlinked: the agent-under-test
// may write into the tree (that is the point — a gate blocks a write, the
// agent recovers), and neither a fixture's local Seed under examples/ nor the
// operator's own clone of a Repo may be mutated by a run.
func newWorkspace(ctx context.Context, fx Fixture) (*workspace, error) {
	root, err := os.MkdirTemp("", "sr-eval-")
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	w := &workspace{
		root:    root,
		project: filepath.Join(root, "project"),
	}

	if fx.Repo != "" {
		if err := cloneRepoAt(ctx, fx.Repo, fx.Ref, w.project); err != nil {
			os.RemoveAll(root)
			return nil, fmt.Errorf("seed project from %s@%s: %w", fx.Repo, fx.Ref, err)
		}
	} else {
		if err := copyTree(fx.SeedDir(), w.project); err != nil {
			os.RemoveAll(root)
			return nil, fmt.Errorf("seed project from %s: %w", fx.SeedDir(), err)
		}
		// A Seed tree carries no .git — but the engine's own change detection
		// (internal/gitrepo.Root/Changed) resolves a write's repository-relative
		// path via `git rev-parse --show-toplevel`, and a file-guard is judged entirely against the git-observed diff at Stop
		// (see cloneRepoAt's doc comment: stripping .git from a Repo fixture was
		// measured to leave a gate silently never firing). Without a .git here,
		// EVERY after-only file-guard is unreachable regardless of what the
		// agent does — measured on a real run (content-de-layering's
		// one-fact-one-home, an after-only file-guard) where the guardrail
		// never fired even though the agent's write plainly matched its rule.
		// `git init` gives a Seed fixture the same repository presence a Repo
		// fixture already has; commitSetup below establishes the baseline
		// exactly as it does for Repo.
		initCmd := exec.CommandContext(ctx, "git", "-C", w.project, "init", "--quiet")
		if out, err := initCmd.CombinedOutput(); err != nil {
			os.RemoveAll(root)
			return nil, fmt.Errorf("git init seed project: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	// ExampleSloprail, when declared, goes on BEFORE Overlay — a fixture's own
	// overlay/.sloprail/ (a variant, or new rules the fixture adds) must win
	// on any path collision with the shipped example, never the reverse.
	//
	// The destination is project/.sloprail EXPLICITLY, not project/ — unlike
	// OverlayDir (whose source already contains a .sloprail/ child to land at
	// project/.sloprail/), ExampleSloprailDir IS .sloprail/ itself, so
	// copyTree-ing it straight into w.project would flatten its own children
	// into the project root instead of nesting them under .sloprail/. See
	// ExampleSloprailDir's doc comment.
	if exampleSloprail := fx.ExampleSloprailDir(); exampleSloprail != "" {
		dest := filepath.Join(w.project, ".sloprail")
		if err := copyTree(exampleSloprail, dest); err != nil {
			os.RemoveAll(root)
			return nil, fmt.Errorf("apply example .sloprail/ from %s: %w", exampleSloprail, err)
		}
	}

	if overlay := fx.OverlayDir(); overlay != "" {
		if err := copyTree(overlay, w.project); err != nil {
			os.RemoveAll(root)
			return nil, fmt.Errorf("apply overlay from %s: %w", overlay, err)
		}
	}

	return w, nil
}

// cloneRepoAt clones url into dest and checks out ref (expected to be a
// commit SHA — see Fixture.Ref). --depth 1 after the checkout is not possible
// (a shallow clone cannot fetch an arbitrary historical commit directly), so
// this fetches normally and checks out; fixtures pin small-to-medium repos,
// not ones where full history depth is itself a cost worth optimizing yet.
//
// .git is KEPT, deliberately — the engine's own change detection
// (internal/gitrepo.Root/Changed) resolves a PreFileWrite's repository-relative
// `path` via `git rev-parse --show-toplevel`, so a tree with .git stripped has
// no repository for the gate to anchor its match against, and the gate silently
// never fires. (Confirmed empirically: stripping .git produced a run where a
// write under tests/ landed with no denial at all.) The origin remote is
// removed instead, which is the actual isolation this needed — the
// agent-under-test's local commits must never be pushable to the real
// upstream, but the guardrail engine needs the repository itself intact.
func cloneRepoAt(ctx context.Context, url, ref, dest string) error {
	clone := exec.CommandContext(ctx, "git", "clone", "--quiet", url, dest)
	if out, err := clone.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone %s: %w: %s", url, err, strings.TrimSpace(string(out)))
	}

	checkout := exec.CommandContext(ctx, "git", "-C", dest, "checkout", "--quiet", ref)
	if out, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout %s: %w: %s", ref, err, strings.TrimSpace(string(out)))
	}

	removeRemote := exec.CommandContext(ctx, "git", "-C", dest, "remote", "remove", "origin")
	if out, err := removeRemote.CombinedOutput(); err != nil {
		return fmt.Errorf("git remote remove origin: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return nil
}

func (w *workspace) Close() error { return os.RemoveAll(w.root) }

// setUp runs the fixture's setup script, then commits everything the harness
// and the setup left in the tree as the baseline the agent starts from. In that
// order: a setup may leave its work uncommitted, and it must land in the
// baseline commit rather than read as the agent's own first change.
func (w *workspace) setUp(ctx context.Context, fx Fixture, env []string) error {
	if err := w.runSetup(ctx, fx, env); err != nil {
		return fmt.Errorf("fixture setup: %w", err)
	}
	if err := w.commitSetup(); err != nil {
		return fmt.Errorf("commit harness setup: %w", err)
	}
	return nil
}

// runSetup runs the fixture's setup script, if it names one, in the project
// directory, in env — the agent's own environment (agentHome), not the
// operator's: its HOME is the workspace's, so nothing the setup does reaches the
// operator's real one.
//
// Git is pinned for it (setupEnv): a git identity, for the same reason
// commitSetup passes one, and no signing and no hooks, for the reason
// commitSetup passes --no-gpg-sign. The agent's HOME carries a copy of the
// operator's ~/.gitconfig, so an operator with commit.gpgsign or core.hooksPath
// set would otherwise see a setup's `git commit` ask for a key or run a hook,
// and the eval fail for a reason that has nothing to do with it.
func (w *workspace) runSetup(ctx context.Context, fx Fixture, env []string) error {
	if fx.Setup == "" {
		return nil
	}
	script := filepath.Join(fx.Dir, fx.Setup)
	c := exec.CommandContext(ctx, script)
	c.Dir = w.project
	c.Env = setupEnv(env, w.project)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", script, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// setupEnv is env, with the project named (SR_EVAL_PROJECT_DIR), a git identity,
// and git's signing and hooks turned off through GIT_CONFIG_COUNT — which
// outranks every config file, global or not. Any GIT_CONFIG_COUNT env already
// carries is dropped rather than extended: its keys are the operator's.
func setupEnv(env []string, project string) []string {
	out := make([]string, 0, len(env)+14)
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if key == "GIT_CONFIG_COUNT" || strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"SR_EVAL_PROJECT_DIR="+project,
		"GIT_AUTHOR_NAME=sr-eval", "GIT_AUTHOR_EMAIL=sr-eval@localhost",
		"GIT_COMMITTER_NAME=sr-eval", "GIT_COMMITTER_EMAIL=sr-eval@localhost",
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=commit.gpgsign", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=tag.gpgsign", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_2=/dev/null",
	)
}

// commitSetup commits every change sr-eval itself made to the tree, so the
// agent-under-test's first turn starts on a clean tree — see the call site in
// run.go for why this matters to the engine's own baseline diff.
//
// Two commits, in this order: the project's own files (seed, the overlay's
// non-rule files, .claude/settings.json, whatever a setup left), then .sloprail
// alone. A file-guard judges committed changesets and a rule's range starts at
// the parent of the commit that added the rule folder; one commit holding both
// makes that parent the empty tree, so every seeded file is judged as the
// agent's own addition (citations demanded on a seeded CHANGELOG, a template's
// marker read as a real one, a removal that shows as status A). Mirrors the e2e
// harness's Env.CommitSeedThenRules. The first commit may be empty so a fixture
// that is only rules still has a seed commit to be that parent.
//
// No signing and no hooks, for the reason runSetup's setupEnv turns them off:
// the operator's global config must not decide whether an eval can start.
//
// Committer identity is passed explicitly via -c rather than relying on the
// operator's global git config, which a machine running this for the first
// time may not have set at all — git refuses to commit with no identity
// configured anywhere, which would fail every fixture run on such a machine
// for a reason that has nothing to do with the eval itself.
func (w *workspace) commitSetup() error {
	if err := w.commitPaths("sr-eval: seed and overlay (project files, .claude/settings.json)", true, ".", ":(exclude).sloprail"); err != nil {
		return err
	}
	var err error
	if w.seedCommit, err = w.head(); err != nil {
		return err
	}
	status := exec.Command("git", "-C", w.project, "status", "--porcelain", "--", ".sloprail")
	out, err := status.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git status .sloprail: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil
	}
	if err := w.commitPaths("sr-eval: install the rules (.sloprail)", false, ".sloprail"); err != nil {
		return err
	}
	w.rulesCommit, err = w.head()
	return err
}

func (w *workspace) head() (string, error) {
	out, err := exec.Command("git", "-C", w.project, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// commitPaths stages every change under the pathspecs and commits it.
func (w *workspace) commitPaths(message string, allowEmpty bool, pathspecs ...string) error {
	add := exec.Command("git", append([]string{"-C", w.project, "add", "-A", "--"}, pathspecs...)...)
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	args := []string{
		"-c", "user.name=sr-eval",
		"-c", "user.email=sr-eval@localhost",
		"-c", "core.hooksPath=/dev/null",
		"-C", w.project, "commit", "--quiet", "--no-gpg-sign", "--no-verify",
	}
	if allowEmpty {
		args = append(args, "--allow-empty")
	}
	commit := exec.Command("git", append(args, "-m", message)...)
	if out, err := commit.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// writeSettings wires the project to load this repo's sloprail plugin the way
// a user actually installs it: `claude plugin marketplace add` +
// `claude plugin install --scope project`, not a hand-written
// .claude/settings.json.
//
// A hand-written settings.json (this function's original form) LOOKS right —
// `claude plugin list` reports the plugin enabled — but does not actually
// make its hooks fire on a genuinely fresh project path. Confirmed with a
// minimal, isolated reproduction outside this repo: on a brand-new temp
// project, writing enabledPlugins+extraKnownMarketplaces by hand leaves
// Claude Code's own Stop hookCount at 1 (only some OTHER, ambient
// user-scope plugin's hook runs; sr-session stop never fires, no error,
// nothing in hookErrors) — while the exact same project, set up instead via
// `claude plugin install --scope project -y`, gets hookCount 2 with
// sr-session stop firing correctly. The difference is
// ~/.claude/plugins/installed_plugins.json, a project-path-keyed install
// registry that only the CLI install flow populates; settings.json alone
// declares intent but does not register the install. Without this, every
// context/gate-nature guardrail (which lives entirely on the Stop dispatch,
// unlike a file-guard's git-diff path) silently never fires in a real
// sr-eval run — first mistaken for a SessionStart baseline-timing race,
// then (wrongly) for an ambient-plugin Stop-hook collision, before being
// traced to this.
//
// The marketplace added is a SNAPSHOT of this checkout's (snapshotMarketplace),
// not the checkout itself: a directory-sourced plugin is served from its source
// path, so adding the live checkout put the real repository's path in front of
// every agent-under-test — every onboarding transcript named it, and one agent
// read its skill from there — and let an agent edit sloprail itself.
//
// There is deliberately no way to add a lifecycle hook from here. Wiring one
// by hand would test sr-eval's own arrangement rather than the product: the
// whole point is that what fires is the plugin a real install gets, discovered
// through hooks.json, not a hook this binary invented for the occasion.
func (w *workspace) writeSettings(repoRoot string, env []string, plugins []string) error {
	source, err := w.snapshotMarketplace(repoRoot)
	if err != nil {
		return fmt.Errorf("snapshot the marketplace: %w", err)
	}
	add := exec.Command("claude", "plugin", "marketplace", "add", source)
	add.Dir = w.project
	add.Env = env
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("claude plugin marketplace add: %w: %s", err, strings.TrimSpace(string(out)))
	}

	for _, key := range append([]string{pluginKey}, pluginKeys(plugins)...) {
		install := exec.Command("claude", "plugin", "install", key, "--scope", "project", "-y")
		install.Dir = w.project
		install.Env = env
		if out, err := install.CombinedOutput(); err != nil {
			return fmt.Errorf("claude plugin install %s: %w: %s", key, err, strings.TrimSpace(string(out)))
		}
	}

	return w.disableAutoMemory()
}

// pluginKeys names each of a fixture's further plugins in this marketplace.
func pluginKeys(plugins []string) []string {
	keys := make([]string, len(plugins))
	for i, p := range plugins {
		keys[i] = p + "@" + marketplaceName
	}
	return keys
}

// disableAutoMemory sets autoMemoryEnabled: false in the project's
// .claude/settings.json (the CLI install just wrote), merged in rather than
// overwritten. Without this, the agent-under-test's cross-session
// auto-memory can write real memories about a fixture's own throwaway
// content into the operator's ~/.claude/projects memory store — the same
// pattern that already caused two real fixtures this session (interlinking,
// no-unasked-deletion) to have the agent confidently claim work it never
// actually did in the project tree, because it wrote to its OWN memory tool
// instead. The setting name and shape are the ones already in use for the
// same purpose in this org's other repos (e.g. strategy's own
// .claude/settings.json: "autoMemoryEnabled": false).
func (w *workspace) disableAutoMemory() error {
	settingsPath := filepath.Join(w.project, ".claude", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("read settings.json written by claude plugin install: %w", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("parse settings.json written by claude plugin install: %w", err)
	}
	settings["autoMemoryEnabled"] = false
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings.json with autoMemoryEnabled: %w", err)
	}
	return os.WriteFile(settingsPath, body, 0o644)
}

// repoRoot finds the sloprail checkout that is running this binary — the
// directory containing .claude-plugin/marketplace.json, which is what
// writeSettings points the directory-sourced marketplace at. Resolved via git
// rather than assumed relative to the binary, because sr-eval, like every
// service here, is installed beside its siblings and may run from anywhere;
// `git rev-parse --show-toplevel` answers correctly from inside a worktree
// too, returning the worktree's own root rather than the main checkout's.
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("locate sloprail checkout (git rev-parse --show-toplevel): %w", err)
	}
	root := strings.TrimSpace(string(out))
	if _, err := os.Stat(filepath.Join(root, ".claude-plugin", "marketplace.json")); err != nil {
		return "", fmt.Errorf("%s is not a sloprail checkout (no .claude-plugin/marketplace.json): %w", root, err)
	}
	return root, nil
}

// copyTree recursively copies src into dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// snapshotMarketplace copies what `claude plugin marketplace add` reads — the
// marketplace manifest and the plugins it points at — into the workspace, and
// returns that copy's root. Uncommitted edits are included, as they were when
// the checkout itself was added; only the location changes.
func (w *workspace) snapshotMarketplace(repoRoot string) (string, error) {
	dst := filepath.Join(w.root, "sloprail-marketplace")
	for _, dir := range []string{".claude-plugin", "marketplace"} {
		if err := copyTree(filepath.Join(repoRoot, dir), filepath.Join(dst, dir)); err != nil {
			return "", err
		}
	}
	return dst, nil
}
