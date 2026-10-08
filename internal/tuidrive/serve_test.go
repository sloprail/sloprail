package tuidrive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func serveFake(t *testing.T, opts ServerOptions, env ...string) (*fake, *Session, *Server, string) {
	t.Helper()
	f, cmd := newFake(t, env...)
	s := f.start(cmd)
	if err := s.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "srt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	opts.Idle = 200 * time.Millisecond
	srv, err := Serve(s, sock, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return f, s, srv, sock
}

func TestToolsOperateTheTUIAsAPersonWould(t *testing.T) {
	f, _, srv, sock := serveFake(t, ServerOptions{})

	r, err := Do(sock, Call{Tool: ToolType, Text: "hello there"})
	if err != nil || r.Error != "" {
		t.Fatalf("type: %v %q", err, r.Error)
	}
	if !strings.Contains(r.Frame, "hello there") {
		t.Fatalf("type must return the frame with the text typed:\n%s", r.Frame)
	}
	if len(f.records()) != 0 {
		t.Fatalf("typing is not submitting: %v", f.records())
	}

	r, _ = Do(sock, Call{Tool: ToolKey, Key: "enter"})
	if got := texts(f.records(), "user"); len(got) != 1 || got[0] != "hello there" {
		t.Fatalf("enter must submit what was typed: %v", got)
	}
	_ = r

	r, _ = Do(sock, Call{Tool: ToolWait, Pattern: "ANSWERED", TimeoutMs: 5000})
	if r.Matched == nil || !*r.Matched || !strings.Contains(r.Frame, "ANSWERED") {
		t.Fatalf("wait must return once the pattern is on the frame:\n%+v", r)
	}

	r, _ = Do(sock, Call{Tool: ToolWait, Pattern: "NEVER-ON-SCREEN", TimeoutMs: 300})
	if r.Matched == nil || *r.Matched {
		t.Fatalf("a pattern that never appears must say it timed out: %+v", r)
	}

	r, _ = Do(sock, Call{Tool: ToolWait}) // no pattern: the screen settling, a look at the frame
	if r.Matched != nil || r.Frame == "" {
		t.Fatalf("a wait with no pattern returns the settled frame: %+v", r)
	}

	if n := srv.Steps(); n != 5 {
		t.Fatalf("every call is logged, got %d", n)
	}
	if log := srv.Log(); log[0].Call.Text != "hello there" || log[1].Call.Key != "enter" {
		t.Fatalf("the log keeps the calls in order: %+v", log)
	}
}

func TestToolErrorsAreAnswersNotCrashes(t *testing.T) {
	_, _, _, sock := serveFake(t, ServerOptions{})
	for name, c := range map[string]Call{
		"unknown key":  {Tool: ToolKey, Key: "hyper-x"},
		"bad pattern":  {Tool: ToolWait, Pattern: "("},
		"unknown tool": {Tool: "screen"},
	} {
		r, err := Do(sock, c)
		if err != nil || r.Error == "" {
			t.Errorf("%s: want an error in the result, got %+v, %v", name, r, err)
		}
	}
}

func TestStepLimitRefusesTheRest(t *testing.T) {
	_, _, srv, sock := serveFake(t, ServerOptions{MaxSteps: 2})
	for i := 0; i < 2; i++ {
		if r, _ := Do(sock, Call{Tool: ToolWait, TimeoutMs: 100}); r.Error != "" {
			t.Fatalf("call %d: %s", i, r.Error)
		}
	}
	r, _ := Do(sock, Call{Tool: ToolType, Text: "more"})
	if !strings.Contains(r.Error, "step limit") {
		t.Fatalf("want the step limit, got %+v", r)
	}
	if srv.Steps() != 2 {
		t.Fatalf("a refused call is not a step: %d", srv.Steps())
	}
}
