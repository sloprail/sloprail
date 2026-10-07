package cursor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// JudgeGrantEnv is the variable sr-agent sets on a judge it launches under Cursor:
// the file access that run was granted, as JSON (JudgeGrant). It is the second
// permission layer beside the run's private cli-config.json.
//
// Why a second layer. Cursor's own permissions are deny rules and a deny beats an
// allow (measured), so "writable only here" cannot be said natively: the config can
// only deny the readonly directories, and a headless run writes anywhere not denied.
// The engine's own preToolUse hook fires inside the judge too (hooks cannot be turned
// off), sees every pending write, and can deny what lies outside the grant. The
// variable travels with the launch, so the hook needs no other channel.
const JudgeGrantEnv = "SLOPRAIL_JUDGE_GRANT"

// JudgeGrant is what a launched judge may write: only inside Writable, never inside
// Readonly (which wins where they nest).
type JudgeGrant struct {
	Writable []string `json:"writable"`
	Readonly []string `json:"readonly"`
}

// Encode renders the grant for JudgeGrantEnv.
func (g JudgeGrant) Encode() string {
	b, _ := json.Marshal(g)
	return JudgeGrantEnv + "=" + string(b)
}

// ParseJudgeGrant reads the grant from an environment lookup. ok is false when the
// run is not a launched judge. A value that does not parse is a grant that allows
// nothing, not no grant: a judge whose limits cannot be read must not be unlimited.
func ParseJudgeGrant(getenv func(string) string) (g JudgeGrant, ok bool) {
	raw := getenv(JudgeGrantEnv)
	if raw == "" {
		return JudgeGrant{}, false
	}
	if json.Unmarshal([]byte(raw), &g) != nil {
		return JudgeGrant{}, true
	}
	return g, true
}

// writeTools are the Cursor tools that change a file, and the key each names its
// target by (recorded: Write's tool_input.file_path; the others are the tool names
// Cursor's transcripts and hook matchers use).
var writeTools = map[string]bool{"Write": true, "Delete": true, "StrReplace": true, "EditNotebook": true, "MultiEdit": true}

// Refusal reports why a pending call is outside the grant, "" when it is inside.
// Only file-changing tools are judged here: shell commands are rejected by Cursor
// itself in a headless run without --force, and everything else reads.
func (g JudgeGrant) Refusal(p Payload) string {
	if p.HookEventName != PreToolUse || !writeTools[p.ToolName] {
		return ""
	}
	var in struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	}
	_ = json.Unmarshal(p.ToolInput, &in)
	target := in.FilePath
	if target == "" {
		target = in.Path
	}
	if target == "" {
		return fmt.Sprintf("this judge may not run %s: the file it changes is not named, so it cannot be checked against what the judge may write", p.ToolName)
	}
	if !filepath.IsAbs(target) {
		if f := p.Folder(); f != "" {
			target = filepath.Join(f, target)
		}
	}
	for _, t := range resolveForms(target) {
		for _, ro := range g.Readonly {
			if within(t, ro) {
				return fmt.Sprintf("this judge may read %s but not change it", ro)
			}
		}
	}
	for _, t := range resolveForms(target) {
		for _, w := range g.Writable {
			if within(t, w) {
				return ""
			}
		}
	}
	return fmt.Sprintf("this judge may write only its answer file, not %s", target)
}

// resolveForms is a path as given and with the deepest existing ancestor's symlinks
// resolved (the file itself may not exist yet), because the agent's spelling and the
// grant's may differ by a symlink (/var and /private/var).
func resolveForms(p string) []string {
	p = filepath.Clean(p)
	forms := []string{p}
	dir, rest := p, ""
	for dir != "/" && dir != "." {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			if r2 := filepath.Join(r, rest); r2 != p {
				forms = append(forms, r2)
			}
			break
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = filepath.Dir(dir)
	}
	return forms
}

func within(path, dir string) bool {
	for _, d := range resolveForms(dir) {
		if rel, err := filepath.Rel(d, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}
