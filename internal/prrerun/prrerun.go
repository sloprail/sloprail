// Package prrerun re-runs the CI `sr-checks verify` job of the pull request a head belongs to,
// once `sr-checks run` has stored the verdicts that job was waiting for.
//
// The judges need the Claude subscription, which CI cannot use, so `sr-checks run` stays on the
// developer's machine and CI only reads what it stored. A pull request opened before the run
// therefore has a red verify job that is stale the moment the verdicts are pushed; this package
// asks GitHub (through `gh`) for that job and re-runs it, so nobody has to click. It is
// best-effort: a missing or unauthenticated `gh` costs one line saying how to do it by hand and
// never changes the run's own exit status.
package prrerun

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

// CheckName is the name of the CI job whose failure is re-run (the job of .github/workflows/verify.yml
// and of the snippets the ci-verify-required gate prints).
const CheckName = "sr-checks verify"

// Exec runs a command in dir and returns its stdout. It is the seam the tests fake.
type Exec func(dir, name string, args ...string) (string, error)

// OS is the real Exec.
func OS(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// Rerun re-runs the failed `sr-checks verify` job of the open pull request whose head is the
// current branch of dir, when head (a full sha) is that branch's tip. It writes at most one line
// per outcome to w.
func Rerun(w io.Writer, run Exec, dir, head string) {
	if _, err := run(dir, "git", "remote", "get-url", "origin"); err != nil {
		return // no origin: nothing a pull request could be on
	}
	branch, err := run(dir, "git", "branch", "--show-current")
	branch = strings.TrimSpace(branch)
	if err != nil || branch == "" {
		return
	}
	if tip, err := run(dir, "git", "rev-parse", "HEAD"); err != nil || strings.TrimSpace(tip) != head {
		return // the judged head is not this branch's tip: the pull request holds other commits
	}

	listed, err := run(dir, "gh", "pr", "list", "--head", branch, "--state", "open", "--json", "number")
	if err != nil {
		unavailable(w)
		return
	}
	numbers, err := PRNumbers(listed)
	if err != nil {
		unavailable(w)
		return
	}
	for _, n := range numbers {
		checks, err := run(dir, "gh", "pr", "checks", fmt.Sprint(n), "--json", "name,bucket,link")
		if err != nil && strings.TrimSpace(checks) == "" {
			// `gh pr checks` exits non-zero while a check is failing or pending but still prints;
			// empty output is the failure to ask.
			unavailable(w)
			return
		}
		id, ok := FailedVerifyRun(checks)
		if !ok {
			continue
		}
		if _, err := run(dir, "gh", "run", "rerun", id, "--failed"); err != nil {
			unavailable(w)
			return
		}
		fmt.Fprintf(w, "sloprail: re-running the failed %q job of pull request #%d (gh run rerun %s --failed)\n", CheckName, n, id)
	}
}

func unavailable(w io.Writer) {
	fmt.Fprintf(w, "sloprail: could not ask GitHub for a failed %q job (gh missing or not logged in); re-run it from the pull request's checks page or with `gh run rerun <run-id> --failed`\n", CheckName)
}

// PRNumbers reads the numbers out of `gh pr list --json number`.
func PRNumbers(listJSON string) ([]int, error) {
	var rows []struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal([]byte(listJSON), &rows); err != nil {
		return nil, err
	}
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Number)
	}
	return out, nil
}

var runID = regexp.MustCompile(`/actions/runs/(\d+)`)

// FailedVerifyRun reads `gh pr checks --json name,bucket,link` and returns the workflow run id of
// the `sr-checks verify` check when it failed. A passing, pending or skipped one, an absent
// one, and a failing check with no run behind its link all return false.
func FailedVerifyRun(checksJSON string) (string, bool) {
	var rows []struct {
		Name   string `json:"name"`
		Bucket string `json:"bucket"`
		Link   string `json:"link"`
	}
	if err := json.Unmarshal([]byte(checksJSON), &rows); err != nil {
		return "", false
	}
	for _, r := range rows {
		if r.Name != CheckName || r.Bucket != "fail" {
			continue
		}
		if m := runID.FindStringSubmatch(r.Link); m != nil {
			return m[1], true
		}
	}
	return "", false
}
