// Package srtest runs a project's end-to-end rule tests (.sloprail/tests/<case>/test.sh) and
// reports one result per case. See services/sr-test.
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

	"github.com/sloprail/sloprail/internal/ambientenv"
	"github.com/sloprail/sloprail/internal/scriptexec"
)

// Status of one case.
const (
	Pass  = "pass"
	Fail  = "fail"
	Error = "error"
)

// Options configure a run.
type Options struct {
	Root       string        // project root; every .sloprail/tests/*/test.sh below it is a case
	Jobs       int           // parallel cases (<=0: 4)
	Timeout    time.Duration // per case (<=0: 5m)
	Only       []string      // run only cases whose subject contains one of these (empty: all)
	LiveJudges bool          // leave SR_CHECKS_JUDGE_MOCKS unset
	Keep       bool          // keep temp dirs
	Stderr     io.Writer     // where kept paths are printed
	// Rules lists the loaded rules ("<nature>:<rule>") active for a case's context. Nil: none.
	Rules func(c Context, stderr io.Writer) []string
	// CorePluginDir is the core sloprail plugin folder, installed beside any other plugin under test.
	CorePluginDir string
	// BinDirs are prepended to the case PATH (the sloprail binaries).
	BinDirs []string
}

// Metadata is a result's metadata.
type Metadata struct {
	DurationMS int64             `json:"duration_ms"`
	Rules      []string          `json:"rules"`
	Events     []json.RawMessage `json:"events"`
}

// Result is one JSONL line.
type Result struct {
	CheckID   string   `json:"check_id"`
	Subject   string   `json:"subject"`
	Kind      string   `json:"kind"`
	Status    string   `json:"status"`
	Output    string   `json:"output"`
	Metadata  Metadata `json:"metadata"`
	CheckedAt string   `json:"checked_at"`
}

// StatusFor maps an exit code and a timeout to a status.
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
	if len(opt.Only) > 0 {
		var keep []Case
		for _, c := range cases {
			for _, o := range opt.Only {
				if strings.Contains(c.Subject, o) {
					keep = append(keep, c)
					break
				}
			}
		}
		cases = keep
	}
	jobs := opt.Jobs
	if jobs <= 0 {
		jobs = 4
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

func runCase(root string, c Case, opt Options, mu *sync.Mutex) Result {
	start := time.Now()
	r := Result{CheckID: "sr-test", Subject: c.Subject, Kind: "test", Metadata: Metadata{Rules: []string{}, Events: []json.RawMessage{}}}
	finish := func(status, out string) Result {
		r.Status, r.Output = status, out
		r.Metadata.DurationMS = time.Since(start).Milliseconds()
		r.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		return r
	}
	dir, err := os.MkdirTemp("", "sr-test-*")
	if err != nil {
		return finish(Error, err.Error())
	}
	if opt.Keep {
		mu.Lock()
		fmt.Fprintf(opt.Stderr, "sr-test: kept %s (%s)\n", dir, c.Subject)
		mu.Unlock()
	} else {
		defer os.RemoveAll(dir)
	}
	// The project the agent works in is dir/project; the case's own folder, the events log and HOME sit
	// beside it, so none of them is an uncommitted change in the project a Stop hook would ask to commit.
	proj := filepath.Join(dir, "project")
	casePath := filepath.Join(dir, "case")
	eventsFile := filepath.Join(dir, "events.jsonl")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		return finish(Error, err.Error())
	}
	if err := copyTree(c.Dir, casePath); err != nil {
		return finish(Error, "copy the case: "+err.Error())
	}
	if len(c.Plugins) == 0 {
		// A project case runs against a copy of the .sloprail/ it belongs to (its rules, not its cases).
		if err := copyTree(c.SloprailDir, filepath.Join(proj, ".sloprail"), "tests"); err != nil {
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
	cmd, err := scriptexec.Command(ctx, filepath.Join(casePath, "test.sh"))
	if err != nil {
		return finish(Error, "test.sh cannot be run: "+err.Error())
	}
	cmd.Dir = proj
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return finish(Error, err.Error())
	}
	env := append(caseEnv(os.Environ()), "HOME="+home, "SR_TEST_CASE_DIR="+casePath, "SR_EVENTS_FILE="+eventsFile, "PATH="+pathWith(opt.BinDirs),
		"SR_TEST_TARGET_DIR="+c.Target, "SR_TEST_SLOPRAIL_DIR="+c.SloprailDir)
	if len(c.Plugins) > 0 {
		env = append(env, "SR_TEST_PLUGIN_DIR="+strings.Join(c.Plugins, string(os.PathListSeparator)))
	}
	if !opt.LiveJudges {
		env = append(env, "SR_CHECKS_JUDGE_MOCKS={}")
	}
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	runErr := cmd.Run()

	exit, timedOut := 0, ctx.Err() == context.DeadlineExceeded
	var ee *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &ee):
		exit = ee.ExitCode()
	default:
		exit = 2
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

// caseEnv is the ambient environment a case starts from: the enclosing session's identity, every
// CLAUDE_CODE_*, SR_* and SLOPRAIL_* variable and HOME are dropped, so a case sees only what sr-test
// sets for it (and passes the same locally and in CI).
func caseEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range ambientenv.Hermetic(environ) {
		key, _, _ := strings.Cut(kv, "=")
		if key == "HOME" || strings.HasPrefix(key, "CLAUDE_CODE_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func orDefault(d time.Duration) time.Duration {
	if d <= 0 {
		return 5 * time.Minute
	}
	return d
}

func pathWith(dirs []string) string {
	return strings.Join(append(append([]string{}, dirs...), os.Getenv("PATH")), string(os.PathListSeparator))
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// copyTree copies src to dst, leaving out the top-level entries named in skip.
func copyTree(src, dst string, skip ...string) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		for _, sk := range skip {
			if rel == sk {
				return fs.SkipDir
			}
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
