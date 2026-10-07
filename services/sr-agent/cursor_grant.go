package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/harness/cursor"
)

// Cursor's permission layer for a launched agent: a private CURSOR_CONFIG_DIR
// holding a cli-config.json.
//
// MEASURED against the real cursor-agent 2026.10.01 (headless `-p`, no --force,
// 2026-10-07), because the docs name config files and not a flag:
//
//   - `.cursor/cli.json` in the project works (Write denies refused the call with
//     "Blocked by permissions configuration"), but writing it means editing the
//     user's project, so it is not used.
//   - CURSOR_CONFIG_DIR=<dir> makes cursor-agent read <dir>/cli-config.json instead of
//     ~/.cursor/cli-config.json, and keeps the login (auth is not in that dir).
//     Write denies there are enforced the same way, and the user's own allow list
//     (e.g. a global `Shell(ls)`) no longer rides into the judge.
//   - A deny with an ABSOLUTE glob (`Write(/abs/dir/**)`) blocks writes under it and
//     leaves writes outside it alone; a relative one (`Write(a.txt)`) did not match.
//   - Deny beats allow (`allow Write(allowed/**)` + `deny Write(**)` refused both), so
//     "writable only here" cannot be said natively: a headless run writes anywhere not
//     denied. That is why the write grant is the readonly denies and nothing more —
//     the same shape as claudeCodeSpec's Edit denies — and why the engine's own
//     preToolUse hook is the second layer under a judge launch.
//   - Without --force a headless run rejects shell commands; `Shell(<cmd>)` in allow
//     runs exactly that command (`Shell(echo)` ran echo, rejected touch), `Shell(*)`
//     runs anything. A `Shell(touch)` deny beside `Shell(*)` did NOT stop touch, so a
//     shell deny beside a shell grant is refused rather than trusted.
//
// Docs: https://cursor.com/docs/cli/reference/permissions.

// cursorGrantEnv builds the private config dir for a run and the env that points
// cursor-agent at it. cleanup removes the dir.
func cursorGrantEnv(g accessGrant) (env, args []string, cleanup func(), err error) {
	if g.AgentRun && len(g.Tools) == 0 && len(g.DenyTools) == 0 {
		return nil, nil, nil, nil // the agent under test keeps its own config; nothing to grant
	}
	allow, deny, err := cursorRules(g)
	if err != nil {
		return nil, nil, nil, err
	}
	dir, err := os.MkdirTemp("", "sr-agent-cursor-*")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("making the permission config dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	cfg, _ := json.Marshal(map[string]any{
		"permissions": map[string]any{"allow": allow, "deny": deny},
	})
	if err := os.WriteFile(filepath.Join(dir, "cli-config.json"), cfg, 0o600); err != nil {
		cleanup()
		return nil, nil, nil, fmt.Errorf("writing the permission config: %w", err)
	}
	// The second layer: the engine's own preToolUse hook, which fires inside the judge
	// too, reads this and refuses a write outside the grant (cursor.JudgeGrant).
	jg := cursor.JudgeGrant{Writable: []string{}, Readonly: []string{}}
	readonlyProject := false
	for _, d := range g.Dirs {
		if d.Mode == dirReadonly {
			jg.Readonly = append(jg.Readonly, d.Path)
			readonlyProject = true
		} else {
			jg.Writable = append(jg.Writable, d.Path)
		}
	}
	env = []string{"CURSOR_CONFIG_DIR=" + dir}
	if !g.AgentRun { // the judge's own write confinement; the agent under test writes its project
		env = append(env, jg.Encode())
	}
	if readonlyProject {
		// A judge reads the project (readonly) by absolute path; it does not need it as
		// its workspace, and the workspace is where cursor-agent discovers project hooks.
		// MEASURED (cursor-agent 2026.10.01, 2026-10-07): with --workspace on an empty
		// scratch dir, a file of a project that has .cursor/hooks.json was read by
		// absolute path and none of that project's hooks fired (control, project as
		// workspace: all fired). So a judge runs in an empty workspace and never runs
		// the user's project hooks.
		ws := filepath.Join(dir, "workspace")
		if err := os.Mkdir(ws, 0o700); err != nil {
			cleanup()
			return nil, nil, nil, fmt.Errorf("making the judge workspace: %w", err)
		}
		args = []string{"--workspace", ws}
	}
	return env, args, cleanup, nil
}

// cursorRules turns an accessGrant into Cursor's allow and deny tokens.
func cursorRules(g accessGrant) (allow, deny []string, err error) {
	allow, deny = []string{}, []string{}
	for _, d := range g.Dirs {
		if d.Mode != dirReadonly {
			continue // writable is the headless default; nothing to say
		}
		for _, form := range pathForms(d.Path) {
			deny = append(deny, "Write("+form+"/**)")
		}
	}
	shellAllowed := false
	for _, t := range g.Tools {
		toks, err := cursorToken(t)
		if err != nil {
			return nil, nil, err
		}
		for _, tok := range toks {
			if strings.HasPrefix(tok, "Shell(") {
				shellAllowed = true
			}
			allow = append(allow, tok)
		}
	}
	for _, t := range g.DenyTools {
		toks, err := cursorToken(t)
		if err != nil {
			return nil, nil, err
		}
		for _, tok := range toks {
			if strings.HasPrefix(tok, "Shell(") && shellAllowed {
				return nil, nil, fmt.Errorf("%w: %w: cursor does not enforce a shell deny beside a shell grant (measured), so %q cannot be promised; drop it or the shell grant",
					ErrModeUnsupported, harness.ErrToolUnsupported, t)
			}
			deny = append(deny, tok)
		}
	}
	return allow, deny, nil
}

// pathForms is dir as given and, when different, with symlinks resolved: Cursor
// matches the path as the agent spells it, and macOS's /var is /private/var.
func pathForms(dir string) []string {
	forms := []string{filepath.Clean(dir)}
	if r, err := filepath.EvalSymlinks(dir); err == nil && r != forms[0] {
		forms = append(forms, r)
	}
	return forms
}

var claudeToolRule = regexp.MustCompile(`^([A-Za-z]+)(?:\((.*)\))?$`)

// cursorToken maps one tool rule as callers spell it (Claude's vocabulary:
// `Read`, `Write`, `Edit`, `WebFetch`, `Bash(curl:*)`) to Cursor's tokens
// (Read/Write/Shell/WebFetch/Mcp with a path, command base or domain). A rule
// already in Cursor's spelling (`Shell(ls)`) passes through. A bare Read or Write
// means everything; a bare Bash means any command.
//
// Cursor's Shell(...) names a command base, not a command line, so a Bash rule scoped
// below the base (`Bash(git show:*)`, `Bash(curl * -o *)`) cannot be said: mapping it to
// `Shell(git)` would grant, or deny, more than the rule asked. That is refused with
// harness.ErrToolUnsupported, as Codex refuses the same rule.
func cursorToken(rule string) ([]string, error) {
	rule = strings.TrimSpace(rule)
	m := claudeToolRule.FindStringSubmatch(rule)
	if m == nil {
		return []string{rule}, nil
	}
	name, arg := m[1], m[2]
	switch name {
	case "Bash", "Shell":
		if arg == "" {
			return []string{"Shell(*)"}, nil
		}
		base := strings.TrimSuffix(strings.TrimSuffix(arg, ":*"), " *")
		if strings.ContainsAny(base, " \t*") {
			return nil, fmt.Errorf("%w: cursor names a shell command by its base only, so %s cannot be scoped below it", harness.ErrToolUnsupported, rule)
		}
		return []string{"Shell(" + base + ")"}, nil
	case "Read", "Write":
		if arg == "" {
			return []string{name + "(**)"}, nil
		}
		return []string{name + "(" + strings.TrimPrefix(arg, "/") + ")"}, nil
	case "Edit", "MultiEdit", "NotebookEdit":
		if arg == "" {
			return []string{"Write(**)"}, nil
		}
		return []string{"Write(" + strings.TrimPrefix(arg, "/") + ")"}, nil
	case "WebFetch":
		if arg == "" {
			return []string{"WebFetch(*)"}, nil
		}
	}
	return []string{rule}, nil
}
