package tuidrive

import (
	"context"
	"errors"
	"fmt"
	"github.com/hinshun/vt10x"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// ErrExited is returned when the harness ended before the driver asked it to.
var ErrExited = errors.New("tuidrive: the harness exited before the session was over")

// ErrBlocked is returned when the agent has stopped mid-turn and is waiting at the terminal
// for the person: a question widget, an approval. Nothing will happen until a key is pressed,
// so waiting longer is not an answer. The caller reads the screen (Frame) and either answers
// it (Key, Type, then AwaitIdle again) or gives the turn up.
var ErrBlocked = errors.New("tuidrive: the agent is waiting for the user at the terminal")

// Session is one interactive run on a pseudo-terminal.
type Session struct {
	opts     Options
	ready    *regexp.Regexp
	quit     []*regexp.Regexp
	cmd      *exec.Cmd
	ptmx     *os.File
	screen   *screen
	wmu      sync.Mutex
	done     chan struct{} // closed when the process has exited and its output is read
	drained  chan struct{} // closed when the pty has nothing more to read
	status   error         // the process's exit status, valid once done is closed
	baseline map[int]bool  // the processes below the harness when its input went live
	emuMu    sync.Mutex
	emu      vt10x.Terminal
}

// Start runs cmd (not yet started) on a new pseudo-terminal. raw, when set, gets everything
// the program prints, as it printed it.
func Start(cmd *exec.Cmd, opts Options, raw io.Writer) (*Session, error) {
	opts.defaults()
	ready, quit, err := checkSpec(opts.Spec)
	if err != nil {
		return nil, err
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: opts.Spec.Rows, Cols: opts.Spec.Cols})
	if err != nil {
		return nil, fmt.Errorf("tuidrive: start %s on a terminal: %w", cmd.Path, err)
	}
	s := &Session{emu: newEmu(opts.Spec.Rows, opts.Spec.Cols), opts: opts, ready: ready, quit: quit, cmd: cmd, ptmx: ptmx,
		done: make(chan struct{}), drained: make(chan struct{})}
	s.screen = newScreen(s.write)
	go func() { // done only once what the program printed has been read (bounded: a child may keep the pty open)
		s.status = cmd.Wait()
		select {
		case <-s.drained:
		case <-time.After(2 * time.Second):
		}
		close(s.done)
	}()
	go s.read(raw)
	return s, nil
}

func (s *Session) write(text string) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, _ = s.ptmx.WriteString(text)
}

func (s *Session) read(raw io.Writer) {
	defer close(s.drained)
	buf := make([]byte, 32<<10)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			if raw != nil {
				_, _ = raw.Write(buf[:n])
			}
			s.screen.write(buf[:n])
			s.feedEmu(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// Tail is the last of what the screen showed, for a failure to say where the run was.
func (s *Session) Tail() string { return s.screen.tail(1500) }

// fail wraps an error with the screen's last text.
func (s *Session) fail(err error) error {
	return fmt.Errorf("%w\n--- the screen's last text ---\n%s", err, s.Tail())
}

// waitScreen waits for re to match the text after mark.
func (s *Session) waitScreen(ctx context.Context, re *regexp.Regexp, mark int, timeout time.Duration, what string) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for gone := false; ; {
		text, changed := s.screen.since(mark)
		if re.MatchString(text) {
			return nil
		}
		if gone {
			return s.fail(fmt.Errorf("%w: the screen never showed %s", ErrExited, what))
		}
		select {
		case <-changed:
		case <-s.done:
			gone = true
		case <-ctx.Done():
			return s.fail(fmt.Errorf("tuidrive: timed out (%s) waiting for %s", timeout, what))
		}
	}
}

// Ready waits until the harness has drawn a live input.
func (s *Session) Ready(ctx context.Context) error {
	if err := s.waitScreen(ctx, s.ready, 0, s.opts.ReadyTimeout, "a live input ("+s.opts.Spec.Ready+")"); err != nil {
		return err
	}
	s.baseline = descendants(s.cmd.Process.Pid)
	return nil
}

// AwaitIdle waits for the turn in progress to end (see the package doc), for a caller that
// did not start it through Turn: the processes the harness had when its input went live are
// the ones that do not count as work.
func (s *Session) AwaitIdle(ctx context.Context) error {
	return s.ended(ctx, s.baseline)
}

func (s *Session) progress() string {
	if s.opts.Progress == nil {
		return ""
	}
	return s.opts.Progress()
}

// Turn types one prompt and returns when the turn it started has ended (see the package
// doc). A prompt of several lines is pasted, as a person's would be, where the harness
// has switched bracketed paste on; it is submitted only once the screen has settled on it.
func (s *Session) Turn(ctx context.Context, prompt string) error {
	select {
	case <-s.done:
		return s.fail(ErrExited)
	default:
	}
	before := s.progress()
	known := descendants(s.cmd.Process.Pid)

	if s.screen.pasteOn() {
		s.write("\x1b[200~" + prompt + "\x1b[201~")
	} else {
		s.write(prompt)
	}
	// What was typed is drawn before Enter is pressed: a key sent with the text can be read
	// as part of one paste.
	if err := s.quiet(ctx, 300*time.Millisecond, 15*time.Second); err != nil {
		return err
	}
	s.write(keyBytes["enter"])

	if err := s.accepted(ctx, before); err != nil {
		return err
	}
	return s.ended(ctx, known)
}

// quiet waits until the screen has not changed for d.
func (s *Session) quiet(ctx context.Context, d, bound time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	for {
		if time.Since(s.screen.lastChange()) >= d {
			return nil
		}
		select {
		case <-time.After(d / 3):
		case <-s.done:
			return s.fail(ErrExited)
		case <-ctx.Done():
			return s.fail(errors.New("tuidrive: the screen never settled after the prompt was typed"))
		}
	}
}

// accepted waits for the record to show the prompt was taken (the fingerprint changed).
// Without a watched record the prompt is taken to be accepted: the quiet that follows is
// then all there is.
func (s *Session) accepted(ctx context.Context, before string) error {
	if s.opts.Progress == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.opts.SubmitTimeout)
	defer cancel()
	for s.progress() == before {
		select {
		case <-time.After(s.opts.Poll):
		case <-s.done:
			return s.fail(ErrExited)
		case <-ctx.Done():
			return s.fail(fmt.Errorf("tuidrive: the prompt was not accepted: the session record did not change in %s", s.opts.SubmitTimeout))
		}
	}
	return nil
}

// ended waits for the quiet that ends a turn.
func (s *Session) ended(ctx context.Context, known map[int]bool) error {
	ctx, cancel := context.WithTimeout(ctx, s.opts.TurnTimeout)
	defer cancel()
	last, lastAt := s.progress(), time.Now()
	for {
		select {
		case <-time.After(s.opts.Poll):
		case <-s.done:
			return s.fail(ErrExited)
		case <-ctx.Done():
			return s.fail(fmt.Errorf("tuidrive: the turn did not end within %s", s.opts.TurnTimeout))
		}
		if p := s.progress(); p != last {
			last, lastAt = p, time.Now()
		}
		if c := s.screen.lastChange(); c.After(lastAt) {
			lastAt = c
		}
		quiet := time.Since(lastAt)
		if quiet < s.opts.Settle {
			continue
		}
		busy := newSince(s.cmd.Process.Pid, known)
		if busy && quiet < s.opts.ProcSettle {
			continue
		}
		if s.opts.Answered != nil && !s.opts.Answered() {
			if !busy && quiet >= s.opts.BlockedAfter {
				return s.fail(fmt.Errorf("%w: quiet for %s with the session record not ending in its answer", ErrBlocked, quiet.Round(time.Second)))
			}
			continue
		}
		return nil
	}
}

// Quit ends the session the way the harness's Spec says and waits for the harness to exit.
// A harness that does not exit in time is killed, which is an error: its session-end hooks
// may not have run. A non-zero exit status is an error too.
func (s *Session) Quit(ctx context.Context) error {
	defer s.Close()
	for i, q := range s.opts.Spec.Quit {
		mark := s.screen.len()
		for _, k := range q.Keys {
			s.write(keyBytes[k])
		}
		if s.quit[i] != nil {
			if err := s.waitScreen(ctx, s.quit[i], mark, 15*time.Second, "the screen "+q.Expect+" after "+fmt.Sprint(q.Keys)); err != nil {
				return err
			}
		}
	}
	select {
	case <-s.done:
	case <-time.After(s.opts.ExitTimeout):
		return s.fail(fmt.Errorf("tuidrive: the harness did not exit within %s of the quit keys; it was killed", s.opts.ExitTimeout))
	case <-ctx.Done():
		return ctx.Err()
	}
	if s.status != nil {
		return fmt.Errorf("tuidrive: the harness ended with %w", s.status)
	}
	return nil
}

// Close kills the harness if it is still running and releases the terminal.
func (s *Session) Close() {
	select {
	case <-s.done:
	default:
		if s.cmd.Process != nil {
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		}
		<-s.done
	}
	_ = s.ptmx.Close()
}
