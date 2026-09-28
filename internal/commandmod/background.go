package commandmod

import (
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

// Backgrounds reports whether running raw may leave work running after the
// line itself returns: a statement sent to the background (`&`, a coprocess),
// or a program that detaches work (nohup, setsid, at, crontab, …) anywhere in
// the line, including inside a wrapper's own command string (`bash -c 'x &'`).
//
// It errs toward yes — a line that cannot be parsed, or whose text holds a lone
// `&` outside `&&` and the redirection forms, is reported as backgrounding —
// because the answer decides whether a change that lands later can be set aside
// as not the agent's, and a wrong "no" is the one that launders.
func Backgrounds(raw string) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(raw), "")
	if err != nil {
		return true
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.Stmt:
			found = found || n.Background || n.Coprocess
		case *syntax.CoprocClause:
			found = true
		}
		return !found
	})
	if found {
		return true
	}
	for _, inv := range ExtractCommand(raw).Invocations {
		if detachers[inv.Bin] {
			return true
		}
	}
	// A `&` inside a quoted command string the parser did not look into.
	rest := raw
	for _, form := range []string{"&&", ">&", "&>", "<&", "|&"} {
		rest = strings.ReplaceAll(rest, form, "")
	}
	return strings.Contains(rest, "&")
}
