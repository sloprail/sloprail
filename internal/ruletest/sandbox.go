package ruletest

import (
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
	"time"

	"github.com/sloprail/sloprail/internal/ambientenv"
	"github.com/sloprail/sloprail/internal/sessionpath"
)

// MarkerFile is the file whose presence makes a directory a rule-test sandbox. The
// seams `sr-checks test` uses (stubbed judges, injected events) act ONLY inside a
// directory that holds it, and the sandbox it names holds the repository, the
// session record and the engine's state: nothing a real session reads.
const MarkerFile = ".sloprail-rule-test"

// SessionID is the id of the one root session every case runs as.
const SessionID = "rule-test-session"

// Sandbox is one case's throwaway world: a git repository, a home, an engine
// state directory and a session record, all under one temp directory.
type Sandbox struct {
	Dir     string // the sandbox; holds MarkerFile
	Repo    string // the repository the case builds
	Home    string
	Data    string // XDG_DATA_HOME: the engine's state
	Config  string // CLAUDE_CONFIG_DIR: where the session record lives
	Tmp     string
	BinDir  string // the directory the engine's own binaries are in
	Live    bool   // judges are real: keep the user's HOME, which holds the harness's login
	Session string // the session record's path
}

// NewSandbox makes an empty sandbox. binDir is where the sr-* binaries under test
// are; it is put first on PATH so a rule's scripts reach the build they belong to.
func NewSandbox(binDir string, live bool) (*Sandbox, error) {
	dir, err := os.MkdirTemp("", "sr-rule-test-")
	if err != nil {
		return nil, err
	}
	// Symlinks resolved: macOS reports /var where the filesystem holds /private/var,
	// and the engine keys its state on the resolved path.
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	s := &Sandbox{
		Dir: dir, Repo: filepath.Join(dir, "repo"), Home: filepath.Join(dir, "home"),
		Data: filepath.Join(dir, "data"), Config: filepath.Join(dir, "claude"), Tmp: filepath.Join(dir, "tmp"),
		BinDir: binDir, Live: live,
	}
	if live {
		if h, err := os.UserHomeDir(); err == nil {
			s.Home = h
		}
	}
	for _, d := range []string{s.Repo, s.Data, s.Config, s.Tmp, filepath.Join(dir, "home")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	liveMu.Lock()
	standing[dir] = true
	liveMu.Unlock()
	if err := os.WriteFile(filepath.Join(dir, MarkerFile), []byte("a throwaway sandbox made by sr-checks test; safe to delete\n"), 0o644); err != nil {
		return nil, err
	}
	gitcfg := "[user]\n\tname = Rule Test\n\temail = rule-test@example.invalid\n[init]\n\tdefaultBranch = main\n[commit]\n\tgpgsign = false\n[tag]\n\tgpgsign = false\n"
	if err := os.WriteFile(filepath.Join(dir, "home", ".gitconfig"), []byte(gitcfg), 0o644); err != nil {
		return nil, err
	}
	return s, nil
}

// OpenSandbox is the handle on an existing sandbox directory (what a replay process
// is told). It refuses a directory that is not one.
func OpenSandbox(dir string) (*Sandbox, error) {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	if !IsSandbox(dir) {
		return nil, fmt.Errorf("%s is not a rule-test sandbox (no %s): this command acts only inside one", dir, MarkerFile)
	}
	return &Sandbox{
		Dir: dir, Repo: filepath.Join(dir, "repo"), Home: filepath.Join(dir, "home"),
		Data: filepath.Join(dir, "data"), Config: filepath.Join(dir, "claude"), Tmp: filepath.Join(dir, "tmp"),
	}, nil
}

// Cleanup removes the sandbox.
func (s *Sandbox) Cleanup() {
	liveMu.Lock()
	delete(standing, s.Dir)
	liveMu.Unlock()
	_ = os.RemoveAll(s.Dir)
}

// Keep stops the sandbox being swept by CleanupAll: the caller asked to look at it.
func (s *Sandbox) Keep() {
	liveMu.Lock()
	delete(standing, s.Dir)
	liveMu.Unlock()
}

var (
	liveMu   sync.Mutex
	standing = map[string]bool{}
)

// CleanupAll removes every sandbox still standing: what a command that is killed calls, so a
// Ctrl-C leaves no temp directories behind.
func CleanupAll() {
	liveMu.Lock()
	defer liveMu.Unlock()
	for dir := range standing {
		_ = os.RemoveAll(dir)
	}
	standing = map[string]bool{}
}

// IsSandbox reports whether dir is a sandbox made by NewSandbox.
func IsSandbox(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, MarkerFile))
	return err == nil && !fi.IsDir()
}

// Env is the environment every process of a case runs in: the caller's, minus
// whatever identifies an enclosing Claude Code session or an outer sloprail run,
// plus the sandbox's own home, state and record.
func (s *Sandbox) Env(extra ...string) []string {
	// No GIT_* of the caller's either: run from a git hook (a pre-push gate, a CI step) the
	// process holds GIT_DIR / GIT_INDEX_FILE, which would point every git a case runs at the
	// caller's repository instead of the sandbox's.
	var env []string
	for _, kv := range ambientenv.Hermetic(os.Environ()) {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"XDG_DATA_HOME="+s.Data,
		"TMPDIR="+s.Tmp,
		"GIT_CONFIG_GLOBAL="+filepath.Join(s.Dir, "home", ".gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	if !s.Live {
		env = append(env, "HOME="+s.Home, "CLAUDE_CONFIG_DIR="+s.Config)
	} else {
		// A live judge runs the harness through sr-agent, which finds the harness in the
		// environment: keep the two variables that name it. The session's own identity stays out.
		for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"} {
			if v := os.Getenv(k); v != "" {
				env = append(env, k+"="+v)
			}
		}
	}
	path := os.Getenv("PATH")
	if s.BinDir != "" {
		path = s.BinDir + string(os.PathListSeparator) + path
	}
	env = append(env, "PATH="+path)
	return append(env, extra...)
}

// SessionRecordPath is where the root session's record lives.
func (s *Sandbox) SessionRecordPath() string {
	return filepath.Join(s.Config, "projects", sessionpath.EncodeWorkspace(s.Repo), SessionID+".jsonl")
}

// SubagentRecordPath is where a sub-agent's own record lives, beside the session's.
func (s *Sandbox) SubagentRecordPath(agent string) string {
	return filepath.Join(strings.TrimSuffix(s.SessionRecordPath(), ".jsonl"), "subagents", "agent-"+agent+".jsonl")
}

// WriteSessionRecord writes the root session's record: an origin record and one
// user message per entry of userSays, so a citation of the user's words resolves.
func (s *Sandbox) WriteSessionRecord(userSays []string) error {
	s.Session = s.SessionRecordPath()
	return writeRecord(s.Session, SessionID, s.Repo, false, userSays)
}

// EnsureSubagentRecord writes a sub-agent's record when it has none.
func (s *Sandbox) EnsureSubagentRecord(agent string) (string, error) {
	p := s.SubagentRecordPath(agent)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return p, writeRecord(p, SessionID, s.Repo, true, nil)
}

func writeRecord(path, session, cwd string, sidechain bool, userSays []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	parent := "null"
	uid := func(i int) string {
		if sidechain {
			return fmt.Sprintf("rt-agent-%d", i)
		}
		return fmt.Sprintf("rt-%d", i)
	}
	first := "start"
	if sidechain {
		first = "sub-agent task"
	}
	msgs := append([]string{first}, userSays...)
	for i, m := range msgs {
		body, _ := json.Marshal(m)
		fmt.Fprintf(&b, `{"type":"user","uuid":%q,"parentUuid":%s,"isSidechain":%t,"sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":%s}}`+"\n",
			uid(i), parent, sidechain, session, cwd, time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC).Format("2006-01-02T15:04:05.000Z"), body)
		parent = fmt.Sprintf("%q", uid(i))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// AppendToolUse records a tool call in a session record (the root's, or a sub-agent's when
// agent is not ""), as a harness writes it when the model asks for the call: before the
// pre-tool hook runs. A rule that asks the trajectory what the agent did (a skill loaded, a
// file read, a command run) reads it there.
func (s *Sandbox) AppendToolUse(agent, tool string, input map[string]any) error {
	path := s.SessionRecordPath()
	sidechain := false
	if agent != "" {
		path = s.SubagentRecordPath(agent)
		sidechain = true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	last, n := "null", 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec struct {
			UUID string `json:"uuid"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && rec.UUID != "" {
			last = fmt.Sprintf("%q", rec.UUID)
		}
		n++
	}
	if input == nil {
		input = map[string]any{}
	}
	block, _ := json.Marshal([]any{map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_rt_%d", n), "name": tool, "input": input}})
	prefix := "rt-tool"
	if sidechain {
		prefix = "rt-agent-tool"
	}
	rec := fmt.Sprintf(`{"type":"assistant","uuid":"%s-%d","parentUuid":%s,"isSidechain":%t,"sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"assistant","content":%s}}`+"\n",
		prefix, n, last, sidechain, SessionID, s.Repo, time.Date(2026, 1, 1, 1, 0, n, 0, time.UTC).Format("2006-01-02T15:04:05.000Z"), block)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(rec)
	return err
}

// Git runs git in the repository, under the sandbox's environment.
func (s *Sandbox) Git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.Repo
	cmd.Env = s.Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Bash runs a script in the repository, under the sandbox's environment, and
// returns what it printed. A script that fails is an error carrying its output.
func (s *Sandbox) Bash(script string, timeout time.Duration, extra ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Dir = s.Repo
	cmd.Env = s.Env(extra...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("timed out after %s: %s", timeout, strings.TrimSpace(string(out)))
	}
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// InstallRules initialises the repository and commits the rules under test (the
// rule folders without their tests) as its first commit, tagged `rules`: the
// project's rules stand before the case's own history begins.
func (s *Sandbox) InstallRules(ruleDirs map[string]string) error {
	if _, err := s.Git("init", "-q", "-b", "main"); err != nil {
		return err
	}
	for rel, src := range ruleDirs {
		if err := copyTree(src, filepath.Join(s.Repo, ".sloprail", filepath.FromSlash(rel)), "tests"); err != nil {
			return fmt.Errorf("copy rule %s: %w", rel, err)
		}
	}
	if len(ruleDirs) == 0 {
		return errors.New("no rules to install")
	}
	if _, err := s.Git("add", "-A"); err != nil {
		return err
	}
	if _, err := s.Git("commit", "-q", "-m", "the rules under test"); err != nil {
		return err
	}
	_, err := s.Git("tag", "rules")
	return err
}

// copyTree copies src to dst keeping file modes (a rule's scripts must stay
// executable), leaving out a top-level directory named skip.
func copyTree(src, dst, skip string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if skip != "" && strings.Split(filepath.ToSlash(rel), "/")[0] == skip {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode()&os.ModeSymlink != 0:
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(l, target)
		default:
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
	})
}
