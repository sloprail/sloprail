package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/transcript"
	"github.com/sloprail/sloprail/internal/tuidrive"
)

// interactiveOf is the harness's interactive mode when its hooks need it (a harness
// whose hooks fire in the one-shot mode too has none to ask for), else false. This is the
// whole of the choice of mode: nothing here names a harness.
func interactiveOf(h harness.Harness) (harness.Interactive, bool) {
	i, ok := h.(harness.Interactive)
	return i, ok && i.HooksNeedInteractive()
}

// tuiAgentArgs is the sr-agent argv that starts the agent under test in its interactive mode:
// the same unattended settings as a one-shot run, and no prompt, which is typed.
func tuiAgentArgs(harnessID, model string, disallowed []string) []string {
	args := []string{"--harness", harnessID, "--model", model, "--agent-run", "--interactive"}
	if len(disallowed) > 0 {
		args = append(args, "--disallowed-tools", strings.Join(disallowed, ","))
	}
	return args
}

// tuiRun is one interactive run of the agent under test.
type tuiRun struct {
	h         harness.Harness
	inter     harness.Interactive
	harnessID string
	fx        Fixture
	ws        *workspace
	agent     agentEnv
	binDir    string
	prompt    string
	brief     string
	maxTurns  int
	opts      tuidrive.Options // the driver's timings, zero in a real run (tests shorten them)
	out, errs io.Writer
}

// tuiOutcome is what a run leaves besides the transcript: the errors that ended it early
// and the files for the archive (the simulated user's calls with the frames they returned,
// and the last frame).
type tuiOutcome struct {
	errs  []string
	files map[string][]byte
}

// run starts the agent in its interactive mode on a pseudo-terminal, types prompt.md as the
// first user turn once its input is live, and, for a fixture with a simulated user, lets the
// user operate the terminal in rounds until it says it is done, the rounds (maxTurns, prompt.md
// counted) or its calls (a cap per round) run out. Then it quits the way a person does.
func (r *tuiRun) run(ctx context.Context) tuiOutcome {
	out := tuiOutcome{files: map[string][]byte{}}
	fail := func(err error) tuiOutcome {
		out.errs = append(out.errs, err.Error())
		fmt.Fprintf(r.errs, "sr-eval: %v\n", err)
		return out
	}

	dir, err := os.MkdirTemp("", "srt-") // short: a unix socket path is bounded
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s")

	cmd := exec.Command(filepath.Join(r.binDir, "sr-agent"), tuiAgentArgs(r.harnessID, r.fx.Model, r.fx.DisallowedTools)...)
	cmd.Dir = r.ws.project
	cmd.Env = r.agent.env
	rec := recordWatch{t: r.h.Transcripts(), project: r.ws.project, configDir: r.agent.configDir}
	opts := r.opts
	opts.Spec, opts.Progress, opts.Answered = r.inter.TUI(), rec.progress, rec.answered
	sess, err := tuidrive.Start(cmd, opts, nil)
	if err != nil {
		return fail(err)
	}
	defer sess.Close()

	if err := sess.Ready(ctx); err != nil {
		return fail(err)
	}
	fmt.Fprintf(r.out, "sr-eval: interactive session is live; turn 1, prompt.md\n")
	if err := sess.Turn(ctx, r.prompt); err != nil {
		return fail(err)
	}

	var srv *tuidrive.Server
	if r.maxTurns > 1 {
		if srv, err = tuidrive.Serve(sess, sock, tuidrive.ServerOptions{MaxSteps: stepsPerRound * (r.maxTurns - 1)}); err != nil {
			return fail(err)
		}
		defer srv.Close()
	}
	for turn := 2; turn <= r.maxTurns; turn++ {
		done, err := simulateTUIUser(ctx, r.harnessID, r.binDir, sock, r.fx.User.UserModel(), r.brief, sess.Frame())
		if err != nil {
			out.errs = append(out.errs, err.Error())
			fmt.Fprintf(r.errs, "sr-eval: %v\n", err)
			break
		}
		if done {
			fmt.Fprintf(r.out, "sr-eval: simulated user is done after %d turn(s)\n", turn-1)
			break
		}
		fmt.Fprintf(r.out, "sr-eval: turn %d, the simulated user operated the terminal\n", turn)
		// What the user did may have started a turn; it is over before the user looks again.
		if err := sess.AwaitIdle(ctx); err != nil {
			out.errs = append(out.errs, err.Error())
			fmt.Fprintf(r.errs, "sr-eval: %v\n", err)
			break
		}
	}

	out.files["tui/final-screen.txt"] = []byte(sess.Frame() + "\n")
	if srv != nil {
		out.files["tui/calls.jsonl"], out.files["tui/calls.txt"] = renderSteps(srv.Log())
	}
	if err := sess.Quit(ctx); err != nil {
		out.errs = append(out.errs, err.Error())
		fmt.Fprintf(r.errs, "sr-eval: %v\n", err)
	}
	return out
}

// stepsPerRound caps the simulated user's calls in one round.
const stepsPerRound = 40

// renderSteps is the simulated user's calls as JSON lines and as text a person can read: each
// call, then the frame it returned.
func renderSteps(steps []tuidrive.Step) (jsonl, text []byte) {
	var j, t bytes.Buffer
	for i, s := range steps {
		b, _ := json.Marshal(s)
		j.Write(append(b, '\n'))
		fmt.Fprintf(&t, "=== call %d: %s %s\n%s\n", i+1, s.Call.Tool, callArg(s.Call), s.Result.Frame)
		if s.Result.Matched != nil {
			fmt.Fprintf(&t, "[pattern %q matched: %v]\n", s.Call.Pattern, *s.Result.Matched)
		}
		if s.Result.Error != "" {
			fmt.Fprintf(&t, "[error: %s]\n", s.Result.Error)
		}
		t.WriteString("\n")
	}
	return j.Bytes(), t.Bytes()
}

func callArg(c tuidrive.Call) string {
	switch c.Tool {
	case tuidrive.ToolType:
		return fmt.Sprintf("%q", c.Text)
	case tuidrive.ToolKey:
		return c.Key
	}
	return fmt.Sprintf("pattern=%q timeout=%dms", c.Pattern, c.TimeoutMs)
}

// recordWatch is how the driver reads the session record the harness writes, through the
// harness's own layout and format.
type recordWatch struct {
	t                  harness.Transcripts
	project, configDir string
}

// progress fingerprints every session file below the project's record directory (a sub-agent's
// record is deeper): it changes whenever one grows.
func (w recordWatch) progress() string {
	root := w.t.ProjectDir(w.configDir, transcript.ResolveWorkDir(w.project))
	if root == "" {
		return ""
	}
	var b strings.Builder
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if info, err := d.Info(); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", path, info.Size(), info.ModTime().UnixNano())
		}
		return nil
	})
	return b.String()
}

// answered reports whether the session record ends with the agent's own text: the last
// conversation entry is the agent's and calls no tool. A prompt, or a tool call whose outcome
// is still to come, is not an answer.
func (w recordWatch) answered() bool {
	path := findTranscript(w.t, w.project, w.configDir)
	if path == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var last harness.Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		rec, err := w.t.ParseRecord(sc.Bytes())
		if err != nil {
			continue
		}
		if e := rec.Entry(); e.Type == harness.EntryUser || e.Type == harness.EntryAssistant {
			last = e
		}
	}
	return last.Type == harness.EntryAssistant && len(transcript.ToolCalls(last)) == 0
}
