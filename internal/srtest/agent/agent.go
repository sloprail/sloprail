// Package agent implements `sr-test agent`: run a scripted agent (a10n-claude-mock)
// in the current directory with sloprail's plugins enabled from the local
// marketplace build, and report what the guardrails decided.
package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/ambientenv"
	"github.com/sloprail/sloprail/internal/harnessmock"
)

const (
	marketplaceName = "sloprail-marketplace"
	pluginKey       = "sloprail@" + marketplaceName
)

// Result is the JSON object `sr-test agent` prints.
type Result struct {
	Exit    int               `json:"exit"`
	Session string            `json:"session"`
	Stream  string            `json:"stream"`
	Events  []json.RawMessage `json:"events"`
}

// Command returns `agent <agent.sh> [--prompt P] [--session ID]`.
func Command() *cobra.Command {
	var prompt, session string
	cmd := &cobra.Command{
		Use:   "agent <agent.sh>",
		Short: "Run a scripted agent (claude mock) here, with sloprail's plugins, and print what the rules decided",
		Long: "Runs a10n-claude-mock in the current directory, executing <agent.sh> once per turn, with a hermetic\n" +
			"config dir and sloprail's plugins enabled from the local marketplace. Prints one JSON object\n" +
			"{exit, session, stream, events}: the mock's exit code, the transcript path, the stream output path and\n" +
			"this run's events. Appends the events to $SR_EVENTS_FILE when set. Exits 0 whenever the agent ran, 2\n" +
			"when it could not run.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, args []string) error {
			res, err := Run(Options{Script: args[0], Prompt: prompt, Session: session})
			if err != nil {
				fmt.Fprintln(c.ErrOrStderr(), "sr-test agent:", err)
				os.Exit(2)
			}
			return json.NewEncoder(c.OutOrStdout()).Encode(res)
		},
	}
	cmd.Flags().StringVar(&prompt, "prompt", "", "the prompt for the agent's turn")
	cmd.Flags().StringVar(&session, "session", "", "session id; a repeat of one id in the same directory continues it (default: a fresh id)")
	return cmd
}

// Options of one run.
type Options struct {
	Script  string
	Prompt  string
	Session string
}

// Run executes one mock turn.
func Run(o Options) (*Result, error) {
	mock, err := harnessmock.Path()
	if err != nil {
		return nil, err
	}
	script, err := filepath.Abs(o.Script)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("agent script: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root, err := checkoutRoot()
	if err != nil {
		return nil, err
	}

	// A stable home per working directory, so a repeat of a session id resumes it.
	sum := sha256.Sum256([]byte(cwd))
	home := filepath.Join(os.TempDir(), "sr-test-"+hex.EncodeToString(sum[:6]))
	cfg, plugins, mkt, tmp := filepath.Join(home, "config"), filepath.Join(home, "plugins"), filepath.Join(home, "marketplace"), filepath.Join(home, "tmp")
	for _, d := range []string{cfg, plugins, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.RemoveAll(mkt); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(mkt, 0o755); err != nil {
		return nil, err
	}
	if err := harnessmock.LocalMarketplace(root, mkt); err != nil {
		return nil, err
	}
	settings, err := harnessmock.Settings([]string{pluginKey}, map[string]string{marketplaceName: mkt})
	if err != nil {
		return nil, err
	}
	if err := writeProjectSettings(cwd, settings); err != nil {
		return nil, err
	}

	run, err := os.MkdirTemp(home, "run-")
	if err != nil {
		return nil, err
	}
	streamPath := filepath.Join(run, "stream.jsonl")
	eventsPath := filepath.Join(run, "events.jsonl")

	if o.Session == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		o.Session = "srt-" + hex.EncodeToString(b)
	}
	flag := "--session-id"
	if findSession(cfg, o.Session) != "" {
		flag = "--resume"
	}
	stream, err := os.Create(streamPath)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	cmd := exec.Command(mock, "-p", "--output-format", "stream-json",
		"--script", script, "--project-dir", cwd,
		"--config-dir", cfg, "--plugin-cache-dir", plugins,
		flag, o.Session, o.Prompt)
	cmd.Dir = cwd
	cmd.Stdout = stream
	cmd.Stderr = stream
	cmd.Env = append(ambientenv.Session(os.Environ()),
		"CLAUDE_CONFIG_DIR="+cfg,
		"CLAUDE_CODE_PLUGIN_CACHE_DIR="+plugins,
		"CLAUDE_CODE_TMPDIR="+tmp,
		"SR_EVENTS_FILE="+eventsPath,
	)
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return nil, fmt.Errorf("run a10n-claude-mock: %w", err)
		}
		code = ee.ExitCode()
		if code < 0 {
			return nil, fmt.Errorf("a10n-claude-mock crashed: %v (see %s)", err, streamPath)
		}
	}

	events, raw, err := readEvents(eventsPath)
	if err != nil {
		return nil, err
	}
	if outer := os.Getenv("SR_EVENTS_FILE"); outer != "" && len(raw) > 0 {
		f, err := os.OpenFile(outer, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, fmt.Errorf("append to SR_EVENTS_FILE: %w", err)
		}
		_, werr := f.WriteString(strings.Join(raw, "\n") + "\n")
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return nil, fmt.Errorf("append to SR_EVENTS_FILE: %w", werr)
		}
	}
	return &Result{Exit: code, Session: findSession(cfg, o.Session), Stream: streamPath, Events: events}, nil
}

// readEvents returns the file's JSON lines (none if the file was never created).
func readEvents(path string) ([]json.RawMessage, []string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []json.RawMessage{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	events := []json.RawMessage{}
	var raw []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if !json.Valid([]byte(l)) {
			return nil, nil, fmt.Errorf("events file %s: not JSON: %q", path, l)
		}
		events = append(events, json.RawMessage(l))
		raw = append(raw, l)
	}
	return events, raw, nil
}

// findSession returns the transcript of a session in a config dir, or "".
func findSession(cfg, id string) string {
	m, _ := filepath.Glob(filepath.Join(cfg, "projects", "*", id+".jsonl"))
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

// checkoutRoot is the sloprail checkout whose marketplace/ the plugins come
// from: $SR_TEST_CHECKOUT, else the checkout the running binary sits in or was
// built from.
func checkoutRoot() (string, error) {
	if r := os.Getenv("SR_TEST_CHECKOUT"); r != "" {
		if isCheckout(r) {
			return r, nil
		}
		return "", fmt.Errorf("SR_TEST_CHECKOUT=%s has no .claude-plugin/marketplace.json", r)
	}
	var starts []string
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		starts = append(starts, filepath.Dir(exe))
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		starts = append(starts, filepath.Dir(file))
	}
	for _, s := range starts {
		if r := up(s); r != "" {
			return r, nil
		}
	}
	return "", errors.New("cannot find the sloprail checkout (.claude-plugin/marketplace.json above the sr-test binary or its sources); set SR_TEST_CHECKOUT")
}

func up(dir string) string {
	for {
		if isCheckout(dir) {
			return dir
		}
		p := filepath.Dir(dir)
		if p == dir {
			return ""
		}
		dir = p
	}
}

func isCheckout(dir string) bool {
	_, e1 := os.Stat(filepath.Join(dir, ".claude-plugin", "marketplace.json"))
	_, e2 := os.Stat(filepath.Join(dir, "marketplace"))
	return e1 == nil && e2 == nil
}

// writeProjectSettings enables the plugins for the mock, which reads only the
// project's settings (<project>/.claude/settings.json and settings.local.json),
// never the config dir's. It writes the local one, which is by convention not
// committed, and in a git repository also lists it in .git/info/exclude so it
// never shows up as an untracked file a rule would judge.
func writeProjectSettings(cwd string, body []byte) error {
	dir := filepath.Join(cwd, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.local.json"), body, 0o644); err != nil {
		return err
	}
	const entry = ".claude/settings.local.json"
	exclude := filepath.Join(cwd, ".git", "info", "exclude")
	if fi, err := os.Stat(filepath.Join(cwd, ".git")); err != nil || !fi.IsDir() {
		return nil
	}
	cur, _ := os.ReadFile(exclude)
	for _, l := range strings.Split(string(cur), "\n") {
		if strings.TrimSpace(l) == entry {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := ""
	if len(cur) > 0 && !strings.HasSuffix(string(cur), "\n") {
		prefix = "\n"
	}
	_, err = f.WriteString(prefix + entry + "\n")
	return err
}
