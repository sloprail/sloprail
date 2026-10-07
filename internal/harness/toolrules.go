package harness

import (
	"errors"
	"fmt"
	"strings"
)

// The allowed-tools vocabulary.
//
// A rule's `allowed_tools` and sr-agent's `--allowed-tools` / `--disallowed-tools` name
// what an agent run may use. They use ONE vocabulary, whatever harness runs: the
// canonical tool names of tools.go (Claude Code's spelling) plus the capabilities a
// judge asks for beyond the file tools:
//
//	Read  Write  Edit  MultiEdit  NotebookEdit  Bash  Bash(<prefix>:*)   (tools.go)
//	Grep  Glob                                      search the tree
//	WebFetch  WebSearch                             reach the web
//	Agent                                           spawn a sub-agent
//	mcp__<server>__<tool>                           an MCP tool, the same name on every harness
//
// A bare name grants the tool; `Bash(git:*)` grants the shell for commands starting
// with that prefix; any other `Name(...)` form (Claude's path and domain rules,
// `Edit(//dir/**)`, `WebFetch(domain:x)`) is Claude-specific and rides through only
// where a harness takes it (Claude Code does, verbatim).
//
// Each harness maps the vocabulary onto what it has. Claude Code passes the rules
// through. A harness whose permission model is not Claude's implements ToolPolicy to
// translate them, and REFUSES (ErrToolUnsupported) what it cannot express rather than
// silently granting more: a rule that asks for less than the harness gives is the one
// failure that looks like it worked.
const (
	ToolGrep      = "Grep"
	ToolGlob      = "Glob"
	ToolWebFetch  = "WebFetch"
	ToolWebSearch = "WebSearch"
	ToolAgent     = "Agent"
)

// ToolRule is one entry of an allowed-tools list, parsed.
type ToolRule struct {
	// Raw is the entry as written.
	Raw string
	// Name is the tool: Read, Bash, WebFetch, mcp__srv__tool ...
	Name string
	// Prefix is the command prefix of a `Bash(<prefix>:*)` rule; "" for a bare name.
	Prefix string
	// Scoped is true for a `Name(...)` entry that is not a Bash prefix rule: a
	// path or domain scope only some harnesses can express.
	Scoped bool
}

// ParseToolRule reads one allowed-tools entry.
func ParseToolRule(s string) (ToolRule, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return ToolRule{}, errors.New("empty tool rule")
	}
	name, rest, scoped := strings.Cut(raw, "(")
	r := ToolRule{Raw: raw, Name: name}
	if !scoped {
		return r, nil
	}
	if !strings.HasSuffix(rest, ")") {
		return ToolRule{}, fmt.Errorf("tool rule %q: unbalanced parenthesis", raw)
	}
	inner := strings.TrimSuffix(rest, ")")
	if name == ToolBash {
		if p, ok := strings.CutSuffix(inner, ":*"); ok && p != "" {
			r.Prefix = p
			return r, nil
		}
	}
	r.Scoped = true
	return r, nil
}

// ParseToolRules reads a list of entries.
func ParseToolRules(entries []string) ([]ToolRule, error) {
	out := make([]ToolRule, 0, len(entries))
	for _, e := range entries {
		r, err := ParseToolRule(e)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// ErrToolUnsupported is returned when a harness cannot express a tool rule. The run
// does not start: asking for less than the harness would give is refused, not rounded
// up.
var ErrToolUnsupported = errors.New("tool rule not expressible in this harness")

// ToolContext is what a harness needs to know about the run besides the rules.
type ToolContext struct {
	// WritableDirs is how many directories the run may write (the answer folder, the
	// caller's `--add-dir`s). A write tool with none to write to asks for nothing.
	WritableDirs int
}

// ToolPolicy is what a Harness MAY implement to translate the allowed-tools
// vocabulary into its own run settings: the extra command-line arguments an agent
// run is started with (sandbox, network, search and the like). allow and deny are the
// parsed `--allowed-tools` and `--disallowed-tools`. A harness without it takes the
// rules through verbatim as its own allow and deny lists (Claude Code).
//
// An implementation returns ErrToolUnsupported (wrapped, naming the rule and why)
// for a rule it cannot honour at least as tightly as written.
type ToolPolicy interface {
	MapToolRules(allow, deny []ToolRule, ctx ToolContext) ([]string, error)
}
