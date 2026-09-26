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
// stranger's machine has it.
var sloprailBinaries = []string{"sr", "sr-session", "sr-file", "sr-mark", "sr-agent", "sr-eval"}

// sloprailRemotes are the spellings of sloprail's own GitHub remote an agent
// (or `claude plugin marketplace add`) may clone. Each is redirected to the
// checkout under test in the fresh HOME's gitconfig. Every spelling is listed
// with and without `.git`, since url.insteadOf is a plain prefix match and
// the bare form alone would also catch sloprail/sloprail-eval.
var sloprailRemotes = []string{
	"git@github.com:sloprail/sloprail",
	"ssh://git@github.com/sloprail/sloprail",
	"https://github.com/sloprail/sloprail",
}

// freshEnv is how a FreshMachine agent-under-test is launched: its HOME, the
// environment it runs in, and where its harness keeps transcripts.
type freshEnv struct {
	home       string
	env        []string
	configDir  string
	releaseURL string
}

// freshHome builds a HOME on which sloprail was never installed, and the
// environment that runs the agent-under-test inside it.
//
// What a developer's machine has is carried over, because an onboarding a
// stranger could not complete without it would be testing the sandbox:
//
//   - ~/Library is linked (macOS): the login keychain lives there, and it
//     holds both Claude Code's own login and gh's token. Without it the agent
//     cannot even start ("Not logged in"), measured.
//   - ~/.ssh is linked: SSH to GitHub. (OpenSSH reads the passwd home, not
//     $HOME, so it works either way; linked so `ls ~/.ssh` agrees.)
//   - ~/.gitconfig and ~/.config/gh are COPIED, not linked: an agent that runs
//     `git config --global` or re-logs gh must not reach the real ones.
//
// The plugin is installed into this HOME afterwards, by writeSettings with
// this environment — the one thing a newcomer has done. What an install of the
// binaries leaves behind is not there: no ~/.local/bin, no ~/go/bin, no
// ~/.claude (so no known marketplaces, no installed plugins, no memory), and
// PATH loses every directory that holds a sloprail binary. The real HOME is
// never written.
func (w *workspace) freshHome(ctx context.Context, repoRoot string) (freshEnv, error) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		return freshEnv{}, fmt.Errorf("locate the real HOME: %w", err)
	}
	home := filepath.Join(w.root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return freshEnv{}, err
	}

	for _, name := range []string{"Library", ".ssh"} {
		src := filepath.Join(realHome, name)
		if _, err := os.Stat(src); err == nil {
			if err := os.Symlink(src, filepath.Join(home, name)); err != nil {
				return freshEnv{}, fmt.Errorf("link ~/%s: %w", name, err)
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
			return freshEnv{}, fmt.Errorf("copy ~/%s: %w", name, err)
		}
	}

	gitconfig := filepath.Join(home, ".gitconfig")
	for _, remote := range sloprailRemotes {
		for _, spelling := range []string{remote, remote + ".git"} {
			set := exec.Command("git", "config", "--file", gitconfig, "--add",
				"url."+repoRoot+".insteadOf", spelling)
			if out, err := set.CombinedOutput(); err != nil {
				return freshEnv{}, fmt.Errorf("redirect %s: %w: %s", spelling, err, strings.TrimSpace(string(out)))
			}
		}
	}

	path, err := freshPath(home)
	if err != nil {
		return freshEnv{}, err
	}

	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch {
		case key == "HOME", key == "PATH", key == "TMPDIR", key == "CLAUDE_CONFIG_DIR",
			key == "XDG_CONFIG_HOME", key == "XDG_DATA_HOME", key == "GOBIN", key == "GOPATH",
			strings.HasPrefix(key, "SLOPRAIL_"):
			continue
		}
		env = append(env, kv)
	}
	// Its own temp dir too, so a clone or download made through mktemp or
	// $TMPDIR is removed with the workspace instead of outliving the run.
	tmp := filepath.Join(w.root, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return freshEnv{}, err
	}
	releaseDir := filepath.Join(w.root, "release")
	if err := buildRelease(ctx, repoRoot, releaseDir); err != nil {
		return freshEnv{}, fmt.Errorf("build this checkout's release: %w", err)
	}
	releaseURL := "file://" + releaseDir
	env = append(env, "HOME="+home, "PATH="+path, "TMPDIR="+tmp,
		"SLOPRAIL_RELEASE_URL="+releaseURL, "SLOPRAIL_INSTALL_TAG=checkout")

	return freshEnv{home: home, env: env, configDir: filepath.Join(home, ".claude"), releaseURL: releaseURL}, nil
}

// buildRelease builds this host's release archive from the checkout into dir,
// in exactly the shape `make release` publishes and install.sh consumes:
// sloprail-<os>-<arch>.tar.gz unpacking to sloprail-<os>-<arch>/<binaries>,
// plus checksums.txt. Host platform only — the agent runs here.
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
	if err := os.RemoveAll(stage); err != nil {
		return err
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
