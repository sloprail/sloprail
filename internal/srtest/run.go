// Package srtest runs a project's end-to-end rule tests and reports one result per case. A case
// lives in its owning rule's folder: .sloprail/<nature>/<rule>/tests/<case>/test.sh, or
// .sloprail/file-guard/structure.tests/<case>/test.sh for the structure gate. See services/sr-test.
package srtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sloprail/sloprail/internal/procgroup"
	"github.com/sloprail/sloprail/internal/reap"
	"github.com/sloprail/sloprail/internal/scriptexec"
)

// Status of one case.
const (
	Pass  = "pass"
	Fail  = "fail"
	Error = "error"
)

// DefaultJobs is how many cases run in parallel when --jobs is not given.
const DefaultJobs = 5

// Options configure a run.
type Options struct {
	Root    string        // project root; every case below it (see Discover) is run
	Jobs    int           // parallel cases (<=0: DefaultJobs)
	Timeout time.Duration // per case (<=0: 5m)
	Only    []string      // run only cases whose subject contains one of these (empty: all)
	Owners  []string      // run only cases owned by one of these ("gate/<rule>", "file-guard/structure"; empty: all)
	Keep    bool          // keep temp dirs
	Stderr  io.Writer     // where kept paths are printed
	// Rules lists the loaded rules ("<nature>:<rule>") active for a case's context. Nil: none.
	Rules func(c Context, stderr io.Writer) []string
	// CorePluginDir is the core sloprail plugin folder, installed beside any other plugin under test.
	CorePluginDir string
	// BinDirs are prepended to the case PATH (the sloprail binaries).
	BinDirs []string

	toolDirs []string // resolved once per Run (see toolDirs)
}

// Metadata is a result's metadata.
type Metadata struct {
	DurationMS int64             `json:"duration_ms"`
	Owner      string            `json:"owner"`
	Rules      []string          `json:"rules"`
	Events     []json.RawMessage `json:"events"`
}

// Result is one JSONL line.
type Result struct {
	CheckID   string   `json:"check_id"`
	Subject   string   `json:"subject"` // "<owner>:<case>" in the root .sloprail/, "<dir of the .sloprail's parent>:<owner>:<case>" below it
	Owner     string   `json:"owner"`   // the owning rule's folder within its .sloprail/: "gate/<rule>", "file-guard/structure"
	Kind      string   `json:"kind"`
	Status    string   `json:"status"`
	Output    string   `json:"output"`
	Metadata  Metadata `json:"metadata"`
	CheckedAt string   `json:"checked_at"`
}

// StatusFor maps an exit code and a timeout to a status.
// sr:invariant authoring-tools/test-run-passes-only-when-every-case-passes
func StatusFor(exit int, timedOut bool) string {
	switch {
	case timedOut:
		return Error
	case exit == 0:
		return Pass
	case exit == 2:
		return Error
	}
	return Fail
}

// Run runs every case and returns the results in case order.
func Run(root string, opt Options) ([]Result, error) {
	cases, err := Discover(root, opt.CorePluginDir)
	if err != nil {
		return nil, err
	}
	if len(opt.Only) > 0 || len(opt.Owners) > 0 {
		var keep []Case
		for _, c := range cases {
			if selected(c, opt) {
				keep = append(keep, c)
			}
		}
		cases = keep
	}
	opt.toolDirs = toolDirs()
	jobs := opt.Jobs
	if jobs <= 0 {
		jobs = DefaultJobs
	}
	if opt.Stderr == nil {
		opt.Stderr = io.Discard
	}
	res := make([]Result, len(cases))
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, n := range cases {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			res[i] = runCase(root, n, opt, &mu)
		}()
	}
	wg.Wait()
	return res, nil
}

// selected: a case passes the filters when its subject contains one of Only AND its owner is one of Owners
// (an empty filter passes every case).
func selected(c Case, opt Options) bool {
	ok := len(opt.Only) == 0
	for _, o := range opt.Only {
		if strings.Contains(c.Subject, o) {
			ok = true
		}
	}
	if !ok {
		return false
	}
	if len(opt.Owners) == 0 {
		return true
	}
	for _, o := range opt.Owners {
		if c.Owner() == o {
			return true
		}
	}
	return false
}

func runCase(root string, c Case, opt Options, mu *sync.Mutex) Result {
	start := time.Now()
	r := Result{CheckID: "sr-test", Subject: c.Subject, Owner: c.Owner(), Kind: "test", Metadata: Metadata{Owner: c.Owner(), Rules: []string{}, Events: []json.RawMessage{}}}
	finish := func(status, out string) Result {
		r.Status, r.Output = status, out
		r.Metadata.DurationMS = time.Since(start).Milliseconds()
		r.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		return r
	}
	dir, err := os.MkdirTemp("", "sr-test-case-*")
	if err != nil {
		return finish(Error, err.Error())
	}
	if opt.Keep {
		reap.Keep(dir) // asked for: no later run may reap it
		mu.Lock()
		fmt.Fprintf(opt.Stderr, "sr-test: kept %s (%s)\n", dir, c.Subject)
		mu.Unlock()
	} else {
		reap.Mark(dir) // a killed run leaves the directory: the next run reaps it by this owner
		defer os.RemoveAll(dir)
	}
	// The project the agent works in is dir/project; the case's own folder, the events log and HOME sit
	// beside it, so none of them is an uncommitted change in the project a Stop hook would ask to commit.
	proj := filepath.Join(dir, "project")
	casePath := filepath.Join(dir, "case")
	eventsFile := filepath.Join(dir, "events.jsonl")
	// sr:invariant authoring-tools/test-case-environment-is-hermetic
	if err := os.MkdirAll(proj, 0o755); err != nil {
		return finish(Error, err.Error())
	}
	if err := copyTree(c.Dir, casePath, nil); err != nil {
		return finish(Error, "copy the case: "+err.Error())
	}
	if len(c.Plugins) == 0 {
		// A project case runs against a copy of the .sloprail/ it belongs to (its rules, not its cases).
		if err := copyTree(c.SloprailDir, filepath.Join(proj, ".sloprail"), isCaseDir); err != nil {
			return finish(Error, "copy .sloprail: "+err.Error())
		}
	}
	// A plugin case copies nothing: its rules load from the installed plugin (named <plugin>/<rule>);
	// copying them into the project too would shadow them with bare-named in-repo copies.
	if err := os.WriteFile(eventsFile, nil, 0o644); err != nil {
		return finish(Error, err.Error())
	}
	if opt.Rules != nil {
		var errBuf bytes.Buffer
		if rs := opt.Rules(Context{Dir: proj, Plugins: c.Plugins}, &errBuf); rs != nil {
			r.Metadata.Rules = rs
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), orDefault(opt.Timeout))
	defer cancel()
	// Run the file itself, by its shebang: `sh test.sh` is a different shell (on macOS bash in POSIX
	// mode, whose echo interprets backslashes), so a case would pass or fail by platform.
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return finish(Error, err.Error())
	}
	// The case's whole configuration of git: the machine's own is not read (GIT_CONFIG_NOSYSTEM, GIT_CONFIG_GLOBAL). It sits beside HOME, which stays empty.
	gitconfig := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[init]\n\tdefaultBranch = main\n"), 0o644); err != nil {
		return finish(Error, err.Error())
	}
	tmp := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return finish(Error, err.Error())
	}
	// The runner provisions the project's repository and the commit identity, so a case sets up
	// neither. A case may still re-run `git init` (harmless) or override the identity (its own
	// GIT_AUTHOR_* exports, `git -c user.name=...`).
	env := caseEnv(os.Environ(), envSpec{Home: home, GitConfig: gitconfig, Tmp: tmp, CaseDir: casePath, EventsFile: eventsFile,
		Target: c.Target, SloprailDir: c.SloprailDir, Plugins: c.Plugins, BinDirs: opt.BinDirs, ToolDirs: opt.toolDirs})
	initCmd := exec.CommandContext(ctx, "git", "init", "-q")
	initCmd.Dir, initCmd.Env = proj, env
	if out, err := initCmd.CombinedOutput(); err != nil {
		return finish(Error, "git init in the project: "+err.Error()+"\n"+strings.TrimSpace(string(out)))
	}
	var buf bytes.Buffer
	var runErr error
	// The case was just written (copied) by this process, and cases run in parallel: on Linux an exec of a
	// file another goroutine's fork still holds open for writing fails with ETXTBSY. It is transient.
	for attempt := 0; attempt < 20; attempt++ {
		cmd, err := scriptexec.Command(ctx, filepath.Join(casePath, "test.sh"))
		if err != nil {
			return finish(Error, "test.sh cannot be run: "+err.Error())
		}
		cmd.Dir = proj
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return procgroup.KillGroup(cmd.Process.Pid) }
		cmd.WaitDelay = 2 * time.Second
		cmd.Env = env
		buf.Reset()
		cmd.Stdout, cmd.Stderr = &buf, &buf
		runErr = procgroup.Run(cmd, true) // a signal to sr-test reaches the case's whole group
		if !errors.Is(runErr, syscall.ETXTBSY) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	exit, timedOut := 0, ctx.Err() == context.DeadlineExceeded
	var ee *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &ee):
		exit = ee.ExitCode()
	default:
		exit = 2
		buf.WriteString("\nsr-test: could not run test.sh: " + runErr.Error() + "\n")
	}
	if data, err := os.ReadFile(eventsFile); err == nil {
		for _, ln := range strings.Split(string(data), "\n") {
			if ln = strings.TrimSpace(ln); ln != "" && json.Valid([]byte(ln)) {
				r.Metadata.Events = append(r.Metadata.Events, json.RawMessage(ln))
			}
		}
	}
	status := StatusFor(exit, timedOut)
	out := ""
	if status != Pass {
		out = tail(buf.String(), 4000)
		if timedOut {
			out = "timed out after " + orDefault(opt.Timeout).String() + "\n" + out
		}
	}
	return finish(status, out)
}

// The identity the runner gives every case's git commits.
const (
	TestGitName  = "sr-test"
	TestGitEmail = "sr-test@sloprail.invalid"
)

// TestIdentityEnv is the author and committer identity of a case's commits, as environment.
func TestIdentityEnv() []string {
	return []string{
		"GIT_AUTHOR_NAME=" + TestGitName, "GIT_AUTHOR_EMAIL=" + TestGitEmail,
		"GIT_COMMITTER_NAME=" + TestGitName, "GIT_COMMITTER_EMAIL=" + TestGitEmail,
	}
}

func orDefault(d time.Duration) time.Duration {
	if d <= 0 {
		return 5 * time.Minute
	}
	return d
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// isCaseDir reports whether rel (a path inside a .sloprail/) is a rule's tests/ folder or the
// structure gate's structure.tests/ folder: cases are not part of the rules a case runs against.
func isCaseDir(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch len(parts) {
	case 2:
		return parts[0] == NatureFileGuard && parts[1] == structureTests
	case 3:
		return parts[2] == "tests" && (parts[0] == NatureGate || parts[0] == NatureFileGuard || parts[0] == NatureContext)
	}
	return false
}

// copyTree copies src to dst, leaving out every entry whose path relative to src skip reports true for.
func copyTree(src, dst string, skip func(rel string) bool) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if skip != nil && rel != "." && skip(rel) {
			return fs.SkipDir
		}
		t := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(t, 0o755)
		case info.Mode()&fs.ModeSymlink != 0:
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(l, t)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(t, data, info.Mode().Perm())
	})
}
