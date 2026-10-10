package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// judgeSessionSep is what the judges' project directories add to the project's own: a judge
// runs in <project>/.sloprail/<nature>/<rule>, which the harness encodes as
// <project dir>--sloprail-<nature>-<rule>.
const judgeSessionSep = "--sloprail-"

// judgeFiles is the transcripts of the judges the run's checks started (kept under
// judges/<rule>/), found beside the agent's own project directory. A judge is a session of
// its own, so `sr-eval archive` of the project does not hold it.
func judgeFiles(transcriptPath string) map[string][]byte {
	files := map[string][]byte{}
	projDir := filepath.Dir(transcriptPath)
	dirs, _ := filepath.Glob(projDir + judgeSessionSep + "*")
	for _, d := range dirs {
		rule := strings.TrimPrefix(filepath.Base(d), filepath.Base(projDir)+judgeSessionSep)
		records, _ := filepath.Glob(filepath.Join(d, "*.jsonl"))
		for _, r := range records {
			if body, err := os.ReadFile(r); err == nil {
				files[filepath.Join("judges", rule, filepath.Base(r))] = body
			}
		}
	}
	return files
}

// repoBundle is the project's whole repository as one git bundle (every branch, the check
// results' one included): the end state the run left, restorable with `git clone repo.bundle`.
func repoBundle(ctx context.Context, project string) ([]byte, error) {
	tmp, err := os.CreateTemp("", "sr-eval-repo-*.bundle")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if out, err := exec.CommandContext(ctx, "git", "-C", project, "bundle", "create", "--quiet", tmp.Name(), "--all").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git bundle: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return os.ReadFile(tmp.Name())
}
