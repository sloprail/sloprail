package tuidrive

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cursorLike is the screen vocabulary the recordings of Cursor's TUI show.
var cursorLike = Spec{
	Ready: `Plan, search, build anything.*<\?2004h>`,
	Quit: []QuitStep{
		{Keys: []string{"ctrl-c"}, Expect: `Press Ctrl\+C again to exit`},
		{Keys: []string{"ctrl-c"}},
	},
}

type fake struct {
	t      *testing.T
	record string
	opts   Options
}

func newFake(t *testing.T, env ...string) (*fake, *exec.Cmd) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "record.jsonl")
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), fakeEnv+"=1", "FAKE_RECORD="+record, "TERM=xterm-256color")
	cmd.Env = append(cmd.Env, env...)
	f := &fake{t: t, record: record}
	f.opts = Options{
		Spec:     cursorLike,
		Progress: f.fingerprint,
		Answered: f.answered,
		Settle:   300 * time.Millisecond, ProcSettle: 20 * time.Second, Poll: 50 * time.Millisecond,
		ReadyTimeout: 10 * time.Second, SubmitTimeout: 10 * time.Second, TurnTimeout: 30 * time.Second, ExitTimeout: 5 * time.Second,
	}
	return f, cmd
}

func (f *fake) fingerprint() string {
	st, err := os.Stat(f.record)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d/%d", st.ModTime().UnixNano(), st.Size())
}

func (f *fake) records() []map[string]any {
	file, err := os.Open(f.record)
	if err != nil {
		return nil
	}
	defer file.Close()
	var out []map[string]any
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		var r map[string]any
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

// answered: the record ends with the agent's text, not a prompt or a tool call.
func (f *fake) answered() bool {
	rs := f.records()
	if len(rs) == 0 {
		return false
	}
	last := rs[len(rs)-1]
	_, tool := last["tool_use"]
	return last["role"] == "assistant" && !tool
}

func (f *fake) start(cmd *exec.Cmd) *Session {
	f.t.Helper()
	s, err := Start(cmd, f.opts, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(s.Close)
	return s
}

func texts(rs []map[string]any, role string) []string {
	var out []string
	for _, r := range rs {
		if r["role"] == role {
			out = append(out, r["text"].(string))
		}
	}
	return out
}

func TestOneSessionTakesEveryTurnAndQuitsCleanly(t *testing.T) {
	f, cmd := newFake(t)
	s := f.start(cmd)
	ctx := context.Background()
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"first", "second"} {
		if err := s.Turn(ctx, p); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if err := s.Quit(ctx); err != nil {
		t.Fatalf("quit: %v", err)
	}
	if got := texts(f.records(), "user"); len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("both prompts must land in the one session record, in order: %v", got)
	}
}

// A prompt of several lines is pasted whole: an Enter inside it would submit the first line.
func TestMultiLinePromptIsPastedWhole(t *testing.T) {
	f, cmd := newFake(t)
	s := f.start(cmd)
	ctx := context.Background()
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	prompt := "line one\n\nline three"
	if err := s.Turn(ctx, prompt); err != nil {
		t.Fatal(err)
	}
	if got := texts(f.records(), "user"); len(got) != 1 || got[0] != prompt {
		t.Fatalf("the prompt must arrive as one message, got %q", got)
	}
	_ = s.Quit(ctx)
}

// The stop hook is a process that says nothing on the screen and writes nothing to the record
// while it runs; its follow-up then restarts the turn. The turn must not be taken as over in
// between, however quiet it is.
func TestTurnOutlivesAQuietStopHookAndItsFollowUp(t *testing.T) {
	f, cmd := newFake(t, "FAKE_HOOK_MS=2000", "FAKE_FOLLOWUP=1")
	s := f.start(cmd)
	ctx := context.Background()
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Turn(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	if got := texts(f.records(), "user"); len(got) != 2 || got[1] != "FOLLOWUP" {
		t.Fatalf("the turn returned before the stop hook's follow-up ran: %v", got)
	}
	if got := texts(f.records(), "assistant"); len(got) != 2 {
		t.Fatalf("the follow-up was not answered when the turn returned: %v", got)
	}
	_ = s.Quit(ctx)
}

// A tool call has no outcome in the record: the record ends with the call, quiet, until it
// returns. That is not an answer.
func TestTurnWaitsForTheAnswerAfterAToolCall(t *testing.T) {
	f, cmd := newFake(t, "FAKE_TOOL_MS=1500")
	s := f.start(cmd)
	ctx := context.Background()
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Turn(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	if !f.answered() {
		t.Fatalf("the turn returned with the record ending in %v", f.records())
	}
	_ = s.Quit(ctx)
}

func TestReadyNeverComingIsNamed(t *testing.T) {
	f, cmd := newFake(t, "FAKE_NEVER_READY=1")
	f.opts.ReadyTimeout = 500 * time.Millisecond
	s := f.start(cmd)
	err := s.Ready(context.Background())
	if err == nil || !strings.Contains(err.Error(), "live input") {
		t.Fatalf("want a timeout naming the live input, got %v", err)
	}
}

func TestExitDuringATurnIsReported(t *testing.T) {
	f, cmd := newFake(t, "FAKE_EXIT_ON_PROMPT=1")
	s := f.start(cmd)
	ctx := context.Background()
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Turn(ctx, "work"); !errors.Is(err, ErrExited) {
		t.Fatalf("want ErrExited, got %v", err)
	}
}

func TestAHarnessThatIgnoresTheQuitKeysIsKilledAndSaidSo(t *testing.T) {
	f, cmd := newFake(t, "FAKE_IGNORE_QUIT=1")
	f.opts.ExitTimeout = 500 * time.Millisecond
	f.opts.Spec.Quit = []QuitStep{{Keys: []string{"ctrl-c"}}}
	s := f.start(cmd)
	ctx := context.Background()
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	err := s.Quit(ctx)
	if err == nil || !strings.Contains(err.Error(), "killed") {
		t.Fatalf("want an error saying it was killed, got %v", err)
	}
}

func TestPromptNotAcceptedIsNamed(t *testing.T) {
	f, cmd := newFake(t)
	f.opts.SubmitTimeout = 500 * time.Millisecond
	f.opts.Progress = func() string { return "frozen" } // a record that never changes
	s := f.start(cmd)
	ctx := context.Background()
	if err := s.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	err := s.Turn(ctx, "work")
	if err == nil || !strings.Contains(err.Error(), "not accepted") {
		t.Fatalf("want 'not accepted', got %v", err)
	}
}

func TestSpecIsCheckedBeforeAnythingRuns(t *testing.T) {
	for name, spec := range map[string]Spec{
		"no ready":    {Quit: cursorLike.Quit},
		"no quit":     {Ready: "x"},
		"bad regexp":  {Ready: "(", Quit: cursorLike.Quit},
		"unknown key": {Ready: "x", Quit: []QuitStep{{Keys: []string{"hyper-x"}}}},
	} {
		if _, err := Start(exec.Command("true"), Options{Spec: spec}, nil); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
}
