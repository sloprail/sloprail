package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func pipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	return r, w
}

// An open pipe nobody writes to — what `claude -p`'s Bash tool hands a command
// — is not a payload: stdinWaiting gives up after the grace instead of
// blocking forever.
func TestStdinWaiting_OpenEmptyPipeIsNotWaiting(t *testing.T) {
	r, _ := pipe(t)
	start := time.Now()
	if stdinWaiting(r, 100*time.Millisecond) {
		t.Fatal("an open, empty pipe reported a payload waiting")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("stdinWaiting blocked %v past its grace", d)
	}
}

func TestStdinWaiting_DataOrEOFIsWaiting(t *testing.T) {
	r, w := pipe(t)
	if _, err := w.WriteString(`{"session_id":"x"}`); err != nil {
		t.Fatal(err)
	}
	if !stdinWaiting(r, time.Second) {
		t.Fatal("a pipe holding a payload reported nothing waiting")
	}

	r2, w2 := pipe(t)
	w2.Close()
	if !stdinWaiting(r2, time.Second) {
		t.Fatal("a pipe whose writer closed (an empty stdin) reported nothing waiting")
	}

	f := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(f, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fh, err := os.Open(f)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if !stdinWaiting(fh, time.Second) {
		t.Fatal("a regular file on stdin reported nothing waiting")
	}
}

// A payload written a moment AFTER the command started — a script's
// `printf … | sr-session trajectory …` racing its own pipeline — is still read.
func TestReadPayloadIfWaiting_LateWriterIsRead(t *testing.T) {
	r, w := pipe(t)
	go func() {
		time.Sleep(50 * time.Millisecond)
		w.WriteString(`{"session_id":"late","cwd":"/tmp"}`)
		w.Close()
	}()
	cmd := &cobra.Command{}
	cmd.SetIn(r)
	if p := readPayloadIfWaiting(cmd); p.SessionID != "late" {
		t.Fatalf("a payload written after start was not read: %+v", p)
	}
}

// The case that hung: an open, empty pipe yields the zero payload, promptly.
func TestReadPayloadIfWaiting_OpenEmptyPipeYieldsNoPayload(t *testing.T) {
	r, _ := pipe(t)
	cmd := &cobra.Command{}
	cmd.SetIn(r)
	done := make(chan HookPayload, 1)
	go func() { done <- readPayloadIfWaiting(cmd) }()
	select {
	case p := <-done:
		if p.SessionID != "" || p.TranscriptPath != "" || p.Cwd != "" {
			t.Fatalf("expected no payload, got %+v", p)
		}
	case <-time.After(payloadGrace + 3*time.Second):
		t.Fatal("readPayloadIfWaiting blocked on an open, empty pipe")
	}
}
