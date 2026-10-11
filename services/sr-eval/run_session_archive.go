package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sessionArchiveDir is where a run's archive keeps what the agent's sessions left behind.
const sessionArchiveDir = "session"

// runSessionFiles is the session archive of the run's project (the state store, the tracked
// ranges and the check results of every repository the sessions tracked: what `sr-eval archive`
// saves), as files to keep under session/ in the run's own archive. It is taken by this binary
// run in the agent's environment, where the agent's sessions and its sloprail state live.
func runSessionFiles(ctx context.Context, agent agentEnv, project, harnessID string) (map[string][]byte, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate sr-eval: %w", err)
	}
	into, err := os.MkdirTemp("", "sr-eval-session-archive-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(into)
	c := exec.CommandContext(ctx, self, "archive", "--all-sessions", "--no-commit", "--into", into, "--label", "run", "--harness", harnessID)
	c.Dir = project
	c.Env = withPathFallback(agent.env, agent.binDir)
	out, err := c.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sr-eval archive: %v: %s", err, strings.TrimSpace(string(out)))
	}
	entries, err := filepath.Glob(filepath.Join(into, "run", "*"))
	if err != nil || len(entries) != 1 {
		return nil, fmt.Errorf("sr-eval archive left %d entries under %s", len(entries), into)
	}
	files := map[string][]byte{}
	err = filepath.WalkDir(entries[0], func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(entries[0], p)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[filepath.Join(sessionArchiveDir, rel)] = body
		return nil
	})
	return files, err
}

// withPathFallback is env with dir added at the END of its PATH: the tools the agent's own
// install put there come first, and the run's build answers when a session installed none.
func withPathFallback(env []string, dir string) []string {
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") && dir != "" {
			kv += string(os.PathListSeparator) + dir
			found = true
		}
		out = append(out, kv)
	}
	if !found && dir != "" {
		out = append(out, "PATH="+dir)
	}
	return out
}
