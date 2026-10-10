package main

import (
	"context"
	"os/exec"
	"strings"
)

// sourceRef names the exact source a run used, so a result can be traced to
// the code that produced it and the run repeated: the commit, where the
// repository comes from, and whether the tree had changes no commit holds.
// A dirty tree means the commit alone does not reproduce the run.
type sourceRef struct {
	Commit   string `json:"commit"`
	Describe string `json:"describe,omitempty"` // git describe --tags --always: the nearest release tag
	Remote   string `json:"remote,omitempty"`   // origin's URL
	Dirty    bool   `json:"dirty"`
}

// refOf reads the sourceRef of the repository holding dir. Nil when dir is in
// no repository (a fixture kept outside one): there is no ref to record, and
// that is not a reason to fail a run.
func refOf(ctx context.Context, dir string) *sourceRef {
	git := func(args ...string) (string, bool) {
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
		return strings.TrimSpace(string(out)), err == nil
	}
	commit, ok := git("rev-parse", "HEAD")
	if !ok {
		return nil
	}
	ref := &sourceRef{Commit: commit}
	ref.Describe, _ = git("describe", "--tags", "--always")
	ref.Remote, _ = git("remote", "get-url", "origin")
	status, _ := git("status", "--porcelain")
	ref.Dirty = status != ""
	return ref
}
