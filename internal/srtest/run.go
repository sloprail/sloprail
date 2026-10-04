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
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sloprail/sloprail/internal/ambientenv"
)

// Status of one case.
const (
	Pass  = "pass"
	Fail  = "fail"
	Error = "error"
)

// Options configure a run.
type Options struct {
	Root       string        // project root holding .sloprail/
	Jobs       int           // parallel cases (<=0: 4)
	Timeout    time.Duration // per case (<=0: 5m)
	LiveJudges bool          // leave SR_CHECKS_JUDGE_MOCKS unset
	Keep       bool          // keep temp dirs
	Stderr     io.Writer     // where kept paths are printed
	// Rules lists the loaded rules ("<nature>:<rule>") of a case workspace. Nil: none.
	Rules func(dir string, stderr io.Writer) []string
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

// Cases returns the case names under root/.sloprail/tests that have a test.sh, sorted.
func Cases(root string) ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(root, ".sloprail", "tests"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, ".sloprail", "tests", e.Name(), "test.sh")); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
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
	names, err := Cases(root)
	if err != nil {
		return nil, err
	}
	jobs := opt.Jobs
	if jobs <= 0 {
		jobs = 4
	}
	if opt.Stderr == nil {
		opt.Stderr = io.Discard
	}
	res := make([]Result, len(names))
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, n := range names {
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

func runCase(root, name string, opt Options, mu *sync.Mutex) Result {
	start := time.Now()
	r := Result{CheckID: "sr-test", Subject: name, Kind: "test", Metadata: Metadata{Rules: []string{}, Events: []json.RawMessage{}}}
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
		fmt.Fprintf(opt.Stderr, "sr-test: kept %s (%s)\n", dir, name)
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
	if err := copyTree(filepath.Join(root, ".sloprail", "tests", name), casePath); err != nil {
		return finish(Error, "copy the case: "+err.Error())
	}
	// A plugin root's rules load from the installed plugin (named <plugin>/<rule>): copying them into the
	// project as well would shadow them with in-repo copies, bare-named. A project's own rules (everything
	// but its cases) are copied in.
	plugin := pluginName(root)
	if plugin == "" {
		if err := copyTree(filepath.Join(root, ".sloprail"), filepath.Join(proj, ".sloprail"), "tests"); err != nil {
			return finish(Error, "copy .sloprail: "+err.Error())
		}
	}
	if err := os.WriteFile(eventsFile, nil, 0o644); err != nil {
		return finish(Error, err.Error())
	}
	if opt.Rules != nil {
		var errBuf bytes.Buffer
		rulesDir := proj
		if plugin != "" {
			rulesDir = root
		}
		if rs := opt.Rules(rulesDir, &errBuf); rs != nil {
			if plugin != "" {
				// the loader also reads the machine's enabled plugins: only this plugin's own rules (bare
				// in its folder) belong to it
				own := rs[:0:0]
				for _, rule := range rs {
					nat, name, _ := strings.Cut(rule, ":")
					if !strings.Contains(name, "/") {
						own = append(own, nat+":"+plugin+"/"+name)
					}
				}
				rs = own
			}
			r.Metadata.Rules = rs
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), orDefault(opt.Timeout))
	defer cancel()
	// Run the file itself, by its shebang: `sh test.sh` is a different shell (on macOS bash in POSIX
	// mode, whose echo interprets backslashes), so a case would pass or fail by platform.
	cmd := exec.CommandContext(ctx, filepath.Join(casePath, "test.sh"))
	cmd.Dir = proj
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return finish(Error, err.Error())
	}
	env := append(caseEnv(os.Environ()), "HOME="+home, "SR_TEST_CASE_DIR="+casePath, "SR_EVENTS_FILE="+eventsFile, "PATH="+pathWith(opt.BinDirs))
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

// pluginName is the name in root's .claude-plugin/plugin.json, or "" when root is not a plugin.
func pluginName(root string) string {
	data, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if err != nil {
		return ""
	}
	var p struct{ Name string }
	if json.Unmarshal(data, &p) != nil {
		return ""
	}
	return p.Name
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
