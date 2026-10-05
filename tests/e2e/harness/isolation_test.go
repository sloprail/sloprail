package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A spawned process must resolve its data directory from the sandbox HOME alone. An operator's
// ambient XDG_DATA_HOME (or LocalAppData) names their REAL session stores; letting it through
// would hand a build under test the developer's own state.db to migrate.
func TestSpawnedProcessCannotReachTheOperatorsStores(t *testing.T) {
	for _, k := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "LocalAppData"} {
		t.Setenv(k, "/real/operator/"+k)
	}
	e := New(t)
	if !strings.HasPrefix(resolved(e.HomeDir()), resolved(os.TempDir())) {
		t.Fatalf("the harness HOME %s is not under the temp dir %s", e.HomeDir(), os.TempDir())
	}
	probe := filepath.Join(e.BinDir(), "envprobe")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nenv\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := e.CLIDirectStdinEnv(t.TempDir(), "", nil, "envprobe")
	for _, line := range strings.Split(res.Output, "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "LocalAppData":
			t.Errorf("ambient %s=%s reached the spawned process", k, v)
		case "HOME":
			if v != e.HomeDir() {
				t.Errorf("spawned HOME = %s, want the sandbox %s", v, e.HomeDir())
			}
		}
	}
}

// Setting a project up and committing in it (what every e2e does) writes git configuration into
// that project's own .git only: the machine's global config is neither created nor changed, and
// neither is one in the sandbox HOME.
func TestEnvWritesNoGitConfigOutsideTheProject(t *testing.T) {
	realHome, _ := os.UserHomeDir()
	watched := []string{filepath.Join(realHome, ".gitconfig"), filepath.Join(realHome, ".config", "git", "config")}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		watched = append(watched, filepath.Join(xdg, "git", "config"))
	}
	before := snapshotFiles(watched)

	e := New(t)
	proj := t.TempDir()
	e.GitInit(proj)
	e.WriteFile(proj, "f.txt", "x")
	e.CommitAll(proj, "second")

	if after := snapshotFiles(watched); after != before {
		t.Fatalf("the machine's git config changed during an e2e setup:\nbefore: %s\nafter:  %s", before, after)
	}
	for _, f := range []string{".gitconfig", filepath.Join(".config", "git", "config")} {
		if _, err := os.Stat(filepath.Join(e.HomeDir(), f)); err == nil {
			t.Errorf("a git config %s was written into the sandbox HOME", f)
		}
	}
}

func snapshotFiles(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			b.WriteString(p + ": absent\n")
			continue
		}
		b.WriteString(p + ": " + string(body) + "\n")
	}
	return b.String()
}

func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
