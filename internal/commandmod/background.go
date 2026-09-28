package commandmod

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// detachers are programs whose purpose is to run work that outlives the call
// that started it.
var detachers = map[string]bool{
	"nohup": true, "setsid": true, "disown": true, "at": true, "batch": true,
	"crontab": true, "daemonize": true, "screen": true, "tmux": true,
	"systemd-run": true, "launchctl": true, "start-stop-daemon": true,
}

// shells are the programs whose `-c` argument is itself a command line.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

// Detaches reports whether running raw may leave work running after the line
// itself returns, and names that work: a program that detaches work (nohup,
// setsid, disown, at, crontab, tmux, screen, …), a coprocess, or a statement
// sent to the background (`&`) that nothing in the same line waits for. A
// `cmd & … wait` ends its work inside the call, and so does `&` that is only
// text — in a quoted argument, a URL, a here-document. A bare `wait` (no
// arguments) reaps every job backgrounded so far. `wait` given an argument —
// `wait $!`, `wait -n`, `wait 1234` — reaps at most one, so it is only weighed
// against how many jobs the line backgrounded, not assumed to cover them all:
// one job and one targeted wait clears it, but `job1 & job2 & wait $!` leaves
// job1 running (bash's `$!` names the last background PID) and is reported as
// detaching by job1. A shell's `-c` command line (`bash -c 'x &'`) is parsed
// the same way, as a line of its own.
//
// A line that does not parse is reported as detaching: whether it could is
// then unknown, and the answer decides whether a change that lands later can be
// set aside as not the agent's.
func Detaches(raw string) (bool, string) {
	return detaches(raw, 0)
}

func detaches(raw string, depth int) (bool, string) {
	f, err := syntax.NewParser().Parse(strings.NewReader(raw), "")
	if err != nil {
		return true, clipCommand(raw)
	}
	var background []*syntax.Stmt
	waitedAll := false
	targetedWaits := 0
	found, what := false, ""
	syntax.Walk(f, func(n syntax.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *syntax.Stmt:
			if n.Background {
				background = append(background, n)
			}
			if n.Coprocess {
				found, what = true, stmtText(raw, n)
			}
		case *syntax.CoprocClause:
			found, what = true, "coproc"
		case *syntax.CallExpr:
			args := literalArgs(n)
			if len(args) == 0 {
				return true
			}
			bin := filepath.Base(args[0])
			switch {
			case detachers[bin]:
				found, what = true, clipCommand(strings.Join(args, " "))
			case bin == "wait":
				// A bare `wait` reaps every job backgrounded so far. A
				// targeted one (a PID, `$!`, `-n`, a job spec) reaps at
				// most one, so it cannot be assumed to cover every `&`
				// on the line — only tallied against how many there are.
				if len(args) == 1 {
					waitedAll = true
				} else {
					targetedWaits++
				}
			case shells[bin] && depth < 3:
				for i := 1; i+1 < len(args); i++ {
					if args[i] == "-c" {
						if ok, w := detaches(args[i+1], depth+1); ok {
							found, what = true, w
						}
						break
					}
				}
			}
		}
		return true
	})
	if found {
		return true, what
	}
	if len(background) > 0 && !waitedAll && targetedWaits < len(background) {
		return true, stmtText(raw, background[0]) + " &"
	}
	return false, ""
}

// literalArgs is a call's words as far as each is literal text.
func literalArgs(c *syntax.CallExpr) []string {
	var out []string
	for _, w := range c.Args {
		lit, ok := plainWord(w)
		if !ok {
			lit = ""
		}
		out = append(out, lit)
	}
	return out
}

func stmtText(raw string, st *syntax.Stmt) string {
	start, end := int(st.Pos().Offset()), int(st.End().Offset())
	if start < 0 || end > len(raw) || start >= end {
		return clipCommand(raw)
	}
	return clipCommand(strings.TrimSuffix(strings.TrimSpace(raw[start:end]), "&"))
}

func clipCommand(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
