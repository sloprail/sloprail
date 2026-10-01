package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// sloprailBinaries are the names whose presence in a directory makes that
// directory part of an existing sloprail install. A fresh machine's PATH
// keeps none of them: the agent must find sloprail absent, the way a
// stranger's machine has it. It is also what buildRelease stages, so it must be
// everything install.sh copies (TestSloprailBinaries_CoverEverythingInstallShCopies):
// a missing one made install.sh fail on `cp` and sloprail never ran.
var sloprailBinaries = []string{"sr", "sr-session", "sr-file", "sr-mark", "sr-agent", "sr-eval", "sr-checks"}

// agentEnv is how an agent-under-test is launched: its HOME, the environment
// it runs in, and where its harness keeps transcripts. releaseURL is set only
// for a FreshMachine run. binDir is this checkout's freshly built binaries —
// where the CALLER (run.go) finds sr-agent to launch the harness itself, and
// what it exposes to a score script as SR_EVAL_BIN_DIR — set for every run,
// fresh or ordinary, since both now build rather than trust an existing PATH.
type agentEnv struct {
	home       string
	env        []string
	configDir  string
	releaseURL string
	binDir     string
}

// agentHome builds the HOME every agent-under-test runs in, and the
// environment that runs it there. Every run gets one, not only a FreshMachine
// run: the plugin install (writeSettings) and the agent both write Claude
// Code's user-level state — known_marketplaces.json, installed_plugins.json,
// memory, transcripts — and in the operator's real HOME that repointed their
// own sloprail-marketplace at whichever checkout last ran an eval, and let
// their own user-scope plugins load into the agent-under-test. Here all of it
// lands inside the workspace and is removed with it; the real HOME is never
// written.
//
// What a developer's machine has is carried over, because a run that could
// not work without it would be testing the sandbox:
//
//   - ~/Library is linked (macOS): the login keychain lives there, and it
//     holds both Claude Code's own login and gh's token. Without it the agent
//     cannot even start ("Not logged in"), measured. (CLAUDE_CONFIG_DIR is
//     cleared, not repointed: setting it changes the keychain entry Claude
//     Code reads, measured the same way.)
//   - ~/.ssh is linked: SSH to GitHub. (OpenSSH reads the passwd home, not
//     $HOME, so it works either way; linked so `ls ~/.ssh` agrees.)
//   - ~/.gitconfig and ~/.config/gh are COPIED, not linked: an agent that runs
//     `git config --global` or re-logs gh must not reach the real ones.
//
// An ordinary run builds THIS checkout's binaries fresh (into the workspace,
// not overwriting anything installed) and puts them first on PATH. A
// FreshMachine run instead gets a PATH with no sloprail binary on it at all
// and nothing in ~/.local/bin or ~/go/bin, install.sh pointed at this
// checkout's build (SLOPRAIL_RELEASE_URL) — the two paths share buildRelease,
// they differ only in whether an EXISTING install stays reachable.
//
// binDir naming "this build's binaries" used to mean whichever sr-agent
// happened to be first on the CALLER's own PATH (siblingBinDir in run.go) —
// not necessarily this checkout at all. An operator with an older global
// install in ~/.local/bin (from make install, or a previous release) got a
// run that silently tested THAT install's code, not whatever fix the
// checkout was mid-editing — measured directly: a same-day code fix
// (skillNameMatches, PR#44) did not reproduce in an ordinary sr-eval run
// because the run never rebuilt, it just inherited the operator's day-old
// ~/.local/bin. Building fresh here, every ordinary run too, removes that
// gap; buildRelease already stages into the workspace and never touches an
// existing install.
func (w *workspace) agentHome(ctx context.Context, repoRootDir string, fresh bool) (agentEnv, error) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		return agentEnv{}, fmt.Errorf("locate the real HOME: %w", err)
	}
	home := filepath.Join(w.root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return agentEnv{}, err
	}

	for _, name := range []string{"Library", ".ssh"} {
		src := filepath.Join(realHome, name)
		if _, err := os.Stat(src); err == nil {
			if err := os.Symlink(src, filepath.Join(home, name)); err != nil {
				return agentEnv{}, fmt.Errorf("link ~/%s: %w", name, err)
			}
		}
	}
	for _, name := range []string{".gitconfig", filepath.Join(".config", "gh")} {
		src := filepath.Join(realHome, name)
		info, err := os.Stat(src)
		if err != nil {
			continue
		}
		dst := filepath.Join(home, name)
		if info.IsDir() {
			err = copyTree(src, dst)
		} else {
			err = copyFile(src, dst)
		}
		if err != nil {
			return agentEnv{}, fmt.Errorf("copy ~/%s: %w", name, err)
		}
	}

	// Its own temp dir too, so a clone or download made through mktemp or
	// $TMPDIR is removed with the workspace instead of outliving the run.
	// Private (0700): Claude Code refuses a shared temp root for its own
	// per-uid directory.
	tmp := filepath.Join(w.root, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return agentEnv{}, err
	}

	env := baseAgentEnv(os.Environ(), home, tmp, fresh)

	ae := agentEnv{home: home, configDir: filepath.Join(home, ".claude")}

	// Every run — fresh or ordinary — launches THIS checkout's own binaries,
	// built new into the workspace rather than trusted from wherever binDir
	// happened to resolve to on the caller's PATH. buildRelease is what a
	// FreshMachine run already used to get install.sh a real release to fetch;
	// an ordinary run reuses the same build, staged into its own bin/ dir, and
	// puts that dir first on PATH instead.
	releaseDir := filepath.Join(w.root, "release")
	if err := buildRelease(ctx, repoRootDir, releaseDir); err != nil {
		return agentEnv{}, fmt.Errorf("build this checkout's release: %w", err)
	}
	builtBinDir := filepath.Join(releaseDir, "sloprail-"+runtime.GOOS+"-"+runtime.GOARCH)
	ae.binDir = builtBinDir

	if !fresh {
		// Go's module cache defaults to $HOME/go: keep it the real one rather
		// than re-downloading every module into the workspace.
		if os.Getenv("GOPATH") == "" {
			env = append(env, "GOPATH="+filepath.Join(realHome, "go"))
		}
		ae.env = append(env, "PATH="+builtBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		return ae, nil
	}

	path, err := freshPath(home)
	if err != nil {
		return agentEnv{}, err
	}
	ae.releaseURL = "file://" + releaseDir
	ae.env = append(env, "PATH="+path,
		"SLOPRAIL_RELEASE_URL="+ae.releaseURL, "SLOPRAIL_INSTALL_TAG=checkout")
	return ae, nil
}

// baseAgentEnv is the environment an agent-under-test starts from: the
// caller's, minus what would point it at the operator's own state, with HOME
// and every temp root moved into the workspace. PATH is left to the caller.
//
// Two temp roots, because Claude Code keeps two. TMPDIR is what the agent's
// tools (mktemp, most languages' temp APIs) honour. CLAUDE_CODE_TMPDIR is
// where Claude Code puts its own per-uid directory — including the
// "scratchpad" it tells the agent to clone and write into — and on macOS it
// IGNORES TMPDIR for that, using /tmp/claude-<uid>. Left there, every run's
// scratchpad clones outlive the workspace and sit where the next run can find
// them: a real run found /tmp repositories earlier runs had cloned and
// "researched" those instead of its own (research-rigor, 2026-09-27).
//
// What this cannot isolate is a literal /tmp: an agent that writes
// `git clone … /tmp/x` or runs `ls /tmp` reaches the machine's shared /tmp,
// and only a sandbox could stop that. A guardrail that must not credit stale
// state has to tell this run's work from what was already on disk itself —
// research-rigor's depth gate counts only directories the run cloned.
func baseAgentEnv(environ []string, home, tmp string, fresh bool) []string {
	env := make([]string, 0, len(environ)+3)
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		switch {
		case key == "HOME", key == "PATH", key == "TMPDIR", key == "CLAUDE_CONFIG_DIR",
			key == "CLAUDE_CODE_TMPDIR", key == "XDG_CONFIG_HOME", key == "XDG_DATA_HOME":
			continue
		case fresh && (key == "GOBIN" || key == "GOPATH" || strings.HasPrefix(key, "SLOPRAIL_")):
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+home, "TMPDIR="+tmp, "CLAUDE_CODE_TMPDIR="+tmp)
}

// buildRelease builds this host's release archive from the checkout into dir,
// in exactly the shape `make release` publishes and install.sh consumes:
// sloprail-<os>-<arch>.tar.gz unpacking to sloprail-<os>-<arch>/<binaries>,
// plus checksums.txt. Host platform only — the agent runs here.
//
// The staged binaries (dir/sloprail-<os>-<arch>/) are kept on disk after the
// archive is built, not removed — an ordinary (non-FreshMachine) run's
// agentHome hands this same directory back as agentEnv.binDir, for the HOST
// to launch sr-agent through and for a score script's SR_EVAL_BIN_DIR, so it
// must still exist once buildRelease returns. Earlier this removed the stage
// right after tarring, which was correct while only the FreshMachine path
// (which only ever needs the .tar.gz, unpacked freshly inside the sandbox)
// called this — deleting it broke an ordinary run's own launch the moment it
// started reusing the same build (fork/exec: no such file or directory).
func buildRelease(ctx context.Context, repoRoot, dir string) error {
	platform := "sloprail-" + runtime.GOOS + "-" + runtime.GOARCH
	stage := filepath.Join(dir, platform)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	for _, name := range sloprailBinaries {
		build := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(stage, name), "./services/"+name)
		build.Dir = repoRoot
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("go build %s: %w: %s", name, err, strings.TrimSpace(string(out)))
		}
	}
	archive := platform + ".tar.gz"
	tar := exec.CommandContext(ctx, "tar", "-C", dir, "-czf", filepath.Join(dir, archive), platform)
	if out, err := tar.CombinedOutput(); err != nil {
		return fmt.Errorf("tar: %w: %s", err, strings.TrimSpace(string(out)))
	}
	body, err := os.ReadFile(filepath.Join(dir, archive))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	return os.WriteFile(filepath.Join(dir, "checksums.txt"),
		[]byte(hex.EncodeToString(sum[:])+"  "+archive+"\n"), 0o644)
}

// freshPath is the caller's PATH minus every directory holding a sloprail
// binary. The harness itself (claude) often shares such a directory
// (~/.local/bin holds both), so when dropping those directories loses it, a
// directory holding only a link to it is put first.
func freshPath(home string) (string, error) {
	claudeBin, _ := exec.LookPath("claude")

	var kept []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || holdsSloprail(dir) {
			continue
		}
		kept = append(kept, dir)
	}

	if claudeBin != "" && !onPath("claude", kept) {
		shim := filepath.Join(home, ".sr-eval-harness-bin")
		if err := os.MkdirAll(shim, 0o755); err != nil {
			return "", err
		}
		if err := os.Symlink(claudeBin, filepath.Join(shim, "claude")); err != nil {
			return "", fmt.Errorf("link the harness binary: %w", err)
		}
		kept = append([]string{shim}, kept...)
	}
	return strings.Join(kept, string(os.PathListSeparator)), nil
}

func holdsSloprail(dir string) bool {
	for _, name := range sloprailBinaries {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func onPath(name string, dirs []string) bool {
	for _, dir := range dirs {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}
