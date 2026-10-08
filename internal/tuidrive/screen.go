package tuidrive

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// screen is what the terminal has shown so far as plain text: every escape sequence dropped
// and every run of whitespace collapsed to one space, so a match does not depend on how the
// program laid out or redrew its screen. The text only grows (a redraw adds its text again),
// so a wait matches from a mark, never from the start.
//
// A terminal query the program makes is answered through reply: a TUI that waits for the
// answer would otherwise stall, as no terminal is there to give it. A private mode the
// program sets (CSI ? 2004 h, bracketed paste on) is put into the text as a token,
// "<?2004h>", so a wait can see it: a TUI turns its modes on once its input is live.
type screen struct {
	mu      sync.Mutex
	text    strings.Builder
	state   int // 0 text, 1 after ESC, 2 in a CSI, 3 in an OSC or another string, 4 after ESC inside one
	csi     []byte
	part    []byte // the bytes of a UTF-8 character the chunk ended in
	reply   func(string)
	changed chan struct{} // closed and replaced each time text is added
	lastAt  time.Time     // when text was last added
	paste   bool          // bracketed paste is on
}

func newScreen(reply func(string)) *screen {
	return &screen{reply: reply, changed: make(chan struct{}), lastAt: time.Now()}
}

// write takes a chunk of the program's output.
func (s *screen) write(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.text.Len()
	data := append(append([]byte{}, s.part...), p...)
	s.part = nil
	for _, b := range data {
		s.feed(b)
	}
	if s.text.Len() != before {
		s.lastAt = time.Now()
		close(s.changed)
		s.changed = make(chan struct{})
	}
}

func (s *screen) feed(b byte) {
	switch s.state {
	case 1:
		s.state = 0
		switch b {
		case '[':
			s.state, s.csi = 2, s.csi[:0]
		case ']', 'P', '_', '^', 'X':
			s.state = 3
		}
	case 2:
		s.csi = append(s.csi, b)
		if b >= 0x40 && b <= 0x7e {
			s.state = 0
			s.answer(string(s.csi))
			s.mode(s.csi)
		}
	case 3:
		if b == 0x07 {
			s.state = 0
		} else if b == 0x1b {
			s.state = 4
		}
	case 4:
		s.state = 3
		if b == '\\' {
			s.state = 0
		}
	default:
		s.char(b)
	}
}

func (s *screen) char(b byte) {
	switch {
	case b == 0x1b:
		s.state = 1
	case b == ' ' || b == '\n' || b == '\r' || b == '\t':
		if t := s.text.String(); t != "" && !strings.HasSuffix(t, " ") {
			s.text.WriteByte(' ')
		}
	case b < 0x20 || b == 0x7f:
	case b < utf8.RuneSelf:
		s.text.WriteByte(b)
	default:
		s.part = append(s.part, b)
		if utf8.FullRune(s.part) {
			s.text.Write(s.part)
			s.part = nil
		}
	}
}

func (s *screen) mode(csi []byte) {
	n := len(csi)
	if n > 2 && csi[0] == '?' && (csi[n-1] == 'h' || csi[n-1] == 'l') {
		switch string(csi) {
		case "?2004h":
			s.paste = true
		case "?2004l":
			s.paste = false
		}
		s.char(' ')
		s.text.WriteString("<" + string(csi) + ">")
		s.char(' ')
	}
}

// answer replies to the queries a TUI makes of its terminal: the cursor position and the
// device attributes. Anything else a CSI says is a drawing instruction.
func (s *screen) answer(csi string) {
	switch csi {
	case "6n":
		s.reply("\x1b[1;1R")
	case "5n":
		s.reply("\x1b[0n")
	case "c", "0c":
		s.reply("\x1b[?62;c")
	}
}

func (s *screen) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text.Len()
}

// since is the text from a mark, and a channel closed when more arrives.
func (s *screen) since(mark int) (string, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text.String()[mark:], s.changed
}

// lastChange is when text was last added.
func (s *screen) lastChange() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAt
}

// pasteOn reports whether the program has bracketed paste switched on.
func (s *screen) pasteOn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paste
}

// tail is the last n bytes of text, for a failure to show where the run was.
func (s *screen) tail(n int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.text.String()
	if len(t) > n {
		t = t[len(t)-n:]
	}
	return t
}
