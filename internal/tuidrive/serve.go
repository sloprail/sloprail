package tuidrive

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sync"
	"time"
)

// A TUI is operated by an agent through three tools, as a person operates a terminal:
//
//	type   types text at the input (no Enter)
//	key    presses a key (enter, esc, tab, up, ctrl-c, ...), to select, answer, approve
//	wait   waits for a pattern to appear on the screen, or for the screen to settle
//
// Each returns the frame as it is after the action. Only these reach the TUI: whatever the
// agent says in prose goes nowhere. They are served on a unix socket, so the agent reaches
// them through a command it runs (sr-eval tui ...), which every harness's agent can do; the
// server keeps a log of every call and its result, for the run's archive.

// Tool names.
const (
	ToolType = "type"
	ToolKey  = "key"
	ToolWait = "wait"
)

// Call is one tool call.
type Call struct {
	Tool string `json:"tool"`
	// Text is what `type` types.
	Text string `json:"text,omitempty"`
	// Key is what `key` presses.
	Key string `json:"key,omitempty"`
	// Pattern is a regexp `wait` waits for on the frame; empty waits for the screen to settle.
	Pattern string `json:"pattern,omitempty"`
	// TimeoutMs bounds `wait` (default 30s).
	TimeoutMs int `json:"timeout_ms,omitempty"`
}

// Result is what a call returns: the frame after the action, and for a `wait` on a pattern
// whether it appeared (false: it timed out).
type Result struct {
	Frame   string `json:"frame"`
	Matched *bool  `json:"matched,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Step is one logged call.
type Step struct {
	At     time.Time `json:"at"`
	Call   Call      `json:"call"`
	Result Result    `json:"result"`
}

// ServerOptions tune a Server; zero values take the defaults.
type ServerOptions struct {
	// MaxSteps is how many calls are served before the rest are refused (default 60).
	MaxSteps int
	// Idle is how long the screen must be still after type or key (default 1s), and Bound how
	// long type and key wait for that at most (default 10s).
	Idle, Bound time.Duration
	// DefaultWait is a `wait`'s timeout when the call gives none (default 30s) and MaxWait
	// the longest it may ask for (default 10m).
	DefaultWait, MaxWait time.Duration
}

func (o *ServerOptions) defaults() {
	if o.MaxSteps == 0 {
		o.MaxSteps = 60
	}
	for _, d := range []struct {
		p *time.Duration
		v time.Duration
	}{{&o.Idle, time.Second}, {&o.Bound, 10 * time.Second}, {&o.DefaultWait, 30 * time.Second}, {&o.MaxWait, 10 * time.Minute}} {
		if *d.p == 0 {
			*d.p = d.v
		}
	}
}

// Server serves the tools of one session on a unix socket.
type Server struct {
	sess *Session
	opts ServerOptions
	ln   net.Listener
	mu   sync.Mutex // one call at a time: a terminal has one pair of hands
	log  []Step
	wg   sync.WaitGroup
}

// Serve starts serving sess on the unix socket at path.
func Serve(sess *Session, path string, opts ServerOptions) (*Server, error) {
	opts.defaults()
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("tuidrive: serve the tools: %w", err)
	}
	s := &Server{sess: sess, opts: opts, ln: ln}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Close stops serving and waits for the call in flight.
func (s *Server) Close() {
	_ = s.ln.Close()
	s.wg.Wait()
}

// Log is every call served so far, in order.
func (s *Server) Log() []Step {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Step(nil), s.log...)
}

// Steps is how many calls have been served.
func (s *Server) Steps() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.log)
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			var c Call
			if json.NewDecoder(bufio.NewReader(conn)).Decode(&c) != nil {
				_ = json.NewEncoder(conn).Encode(Result{Error: "tuidrive: the call is not a JSON object"})
				return
			}
			_ = json.NewEncoder(conn).Encode(s.do(c))
		}()
	}
}

func (s *Server) do(c Call) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r Result
	if len(s.log) >= s.opts.MaxSteps {
		r = Result{Frame: s.sess.Frame(), Error: fmt.Sprintf("step limit reached: %d calls were allowed and are used; stop", s.opts.MaxSteps)}
		return r // not logged: it did nothing
	}
	ctx := context.Background()
	switch c.Tool {
	case ToolType:
		s.sess.Type(c.Text)
		r.Frame = s.sess.Settled(ctx, s.opts.Idle, s.opts.Bound)
	case ToolKey:
		if err := s.sess.Key(c.Key); err != nil {
			r.Error = err.Error()
			r.Frame = s.sess.Frame()
		} else {
			r.Frame = s.sess.Settled(ctx, s.opts.Idle, s.opts.Bound)
		}
	case ToolWait:
		r = s.wait(ctx, c)
	default:
		r = Result{Frame: s.sess.Frame(), Error: fmt.Sprintf("unknown tool %q: the tools are %s, %s, %s", c.Tool, ToolType, ToolKey, ToolWait)}
	}
	s.log = append(s.log, Step{At: time.Now(), Call: c, Result: r})
	return r
}

func (s *Server) wait(ctx context.Context, c Call) Result {
	timeout := s.opts.DefaultWait
	if c.TimeoutMs > 0 {
		timeout = time.Duration(c.TimeoutMs) * time.Millisecond
	}
	if timeout > s.opts.MaxWait {
		timeout = s.opts.MaxWait
	}
	if c.Pattern == "" {
		return Result{Frame: s.sess.Settled(ctx, s.opts.Idle, timeout)}
	}
	re, err := regexp.Compile(c.Pattern)
	if err != nil {
		return Result{Frame: s.sess.Frame(), Error: "the pattern is not a regexp: " + err.Error()}
	}
	frame, ok := s.sess.WaitFor(ctx, re, timeout)
	return Result{Frame: frame, Matched: &ok}
}

// Do makes one call on the socket at path.
func Do(path string, c Call) (Result, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return Result{}, fmt.Errorf("tuidrive: no TUI session to operate at %s: %w", path, err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(c); err != nil {
		return Result{}, err
	}
	var r Result
	if err := json.NewDecoder(conn).Decode(&r); err != nil {
		return Result{}, errors.New("tuidrive: the session ended before it answered")
	}
	return r, nil
}
