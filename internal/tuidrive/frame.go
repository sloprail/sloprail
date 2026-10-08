package tuidrive

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hinshun/vt10x"
)

// A person at a terminal sees a frame: the screen as it is drawn now, not the stream of
// everything drawn so far (which screen keeps for matching). The frame comes from a terminal
// emulator fed the same bytes the program printed.

// Frame is the screen as it is drawn now, as text: one line per terminal row, trailing
// blanks and empty rows at the bottom dropped.
func (s *Session) Frame() string {
	s.emuMu.Lock()
	defer s.emuMu.Unlock()
	rows := strings.Split(s.emu.String(), "\n")
	for i, r := range rows {
		rows[i] = strings.TrimRight(r, " ")
	}
	return strings.TrimRight(strings.Join(rows, "\n"), "\n")
}

func (s *Session) feedEmu(p []byte) {
	s.emuMu.Lock()
	defer s.emuMu.Unlock()
	_, _ = s.emu.Write(p)
}

func newEmu(rows, cols uint16) vt10x.Terminal {
	return vt10x.New(vt10x.WithSize(int(cols), int(rows)))
}

// Type types text at the input as it is, with no Enter: a person's keystrokes (bracketed
// paste is not used, so a newline in it is a key press like any other).
func (s *Session) Type(text string) { s.write(text) }

// Key presses a named key (enter, esc, tab, up, ctrl-c, ...), refusing a name it has no
// bytes for.
func (s *Session) Key(name string) error {
	b, ok := keyBytes[name]
	if !ok {
		return &UnknownKeyError{Name: name}
	}
	s.write(b)
	return nil
}

// UnknownKeyError names a key the driver cannot press.
type UnknownKeyError struct{ Name string }

func (e *UnknownKeyError) Error() string {
	return "tuidrive: " + e.Name + " is not a key the driver can press (" + KeyNames() + ")"
}

// KeyNames lists the keys Key accepts, sorted.
func KeyNames() string {
	names := make([]string, 0, len(keyBytes))
	for k := range keyBytes {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// Settled waits until the screen has stopped changing for idle, or timeout has passed since
// the call, whichever comes first, and returns the frame then. A program that has exited
// ends the wait at once.
func (s *Session) Settled(ctx context.Context, idle, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Whatever the action just sent has not been drawn yet: look from the first change, or
	// from idle after the call when nothing changes at all.
	start := time.Now()
	for {
		last := s.screen.lastChange()
		if last.Before(start) {
			last = start
		}
		if time.Since(last) >= idle {
			break
		}
		select {
		case <-time.After(idle / 4):
		case <-s.done:
			return s.Frame()
		case <-ctx.Done():
			return s.Frame()
		}
	}
	return s.Frame()
}

// WaitFor waits until pattern matches the frame, or timeout passes, and returns the frame
// then and whether it matched. A program that has exited ends the wait at once.
func (s *Session) WaitFor(ctx context.Context, pattern *regexp.Regexp, timeout time.Duration) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		frame := s.Frame()
		if pattern.MatchString(frame) {
			return frame, true
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-s.done:
			frame = s.Frame()
			return frame, pattern.MatchString(frame)
		case <-ctx.Done():
			frame = s.Frame()
			return frame, pattern.MatchString(frame)
		}
	}
}
