package harness

import (
	"encoding/json"
	"os"
	"strings"
)

// JudgeAccess is what a judge's agent was launched with, read out of the recording of
// InstallJudgeClaudeRecordingArgv in the terms every harness can be asked about: which
// model, which tools the rule's allowed_tools reached, and where the judge may write.
// Each Driver reads its own harness's launch (Claude Code: permission flags; Codex: a
// sandbox; Cursor: a private permission config plus the engine's second layer).
type JudgeAccess struct {
	// Model is the value the harness's model flag carried.
	Model string
	// ReadGranted: the judge can read (the rule's `Read` reached the harness, or the harness
	// reads without being granted it).
	ReadGranted bool
	// ReadsAnywhere: the harness gives the judge the whole disk to read, so the project is
	// readable by its absolute path. Otherwise Readable lists the directories it is given.
	ReadsAnywhere bool
	Readable      []string
	// OtherTools are the tool grants beyond reading and the writable directories, as the
	// harness spells them: none for a rule that asked for nothing else.
	OtherTools []string
	// Writable are the directories the judge may write (the answer directory), one entry
	// per spelling of a directory.
	Writable []string
	// Readonly are the directories the harness explicitly denies the judge writes in.
	Readonly []string
	// ConfinedToWritable: the harness stops the judge writing anywhere outside Writable
	// (a sandbox, or a hook the engine installs), so a directory not listed is not writable.
	ConfinedToWritable bool

	// AllowedTools and AddDirs are the launch's own lists, in argv order, and ArgvExact says
	// the harness's argv carries them verbatim (Claude Code: --allowed-tools and --add-dir),
	// so a test can pin the exact grant and not only what each entry means. Empty and false
	// where the harness spells its grants another way.
	AllowedTools []string
	AddDirs      []string
	ArgvExact    bool
}

// argvFlagValues is every value following flag in a recorded argv (one argument per line),
// up to the next argument that starts with "-" when the flag is variadic.
func argvFlagValues(lines []string, flag string) []string {
	var out []string
	for i := 0; i < len(lines); i++ {
		if lines[i] != flag {
			continue
		}
		for _, v := range lines[i+1:] {
			if strings.HasPrefix(v, "-") {
				break
			}
			out = append(out, v)
		}
	}
	return out
}

// argvFlagValue is the single argument after flag, or "".
func argvFlagValue(lines []string, flag string) string {
	for i := 0; i+1 < len(lines); i++ {
		if lines[i] == flag {
			return lines[i+1]
		}
	}
	return ""
}

func recordedLines(argvFile string) ([]string, error) {
	b, err := os.ReadFile(argvFile)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n"), nil
}

// ruleDir is the directory in a scoped rule such as Edit(//abs/**) or Write(/abs/**),
// "" when the token is not a rule over a directory tree of that tool.
func ruleDir(token, tool string) string {
	rest, ok := strings.CutPrefix(token, tool+"(")
	if !ok {
		return ""
	}
	rest, ok = strings.CutSuffix(rest, "/**)")
	if !ok {
		return ""
	}
	return "/" + strings.TrimLeft(rest, "/")
}

func addUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// JudgeAccess for Claude Code: --allowed-tools / --disallowed-tools rules and --add-dir.
func (claudeDriver) JudgeAccess(argvFile string) (JudgeAccess, error) {
	lines, err := recordedLines(argvFile)
	if err != nil {
		return JudgeAccess{}, err
	}
	a := JudgeAccess{Model: argvFlagValue(lines, "--model")}
	for _, t := range argvFlagValues(lines, "--allowed-tools") {
		switch {
		case t == "Read":
			a.ReadGranted = true
		case ruleDir(t, "Edit") != "":
			a.Writable = addUnique(a.Writable, ruleDir(t, "Edit"))
		default:
			a.OtherTools = append(a.OtherTools, t)
		}
	}
	for _, t := range argvFlagValues(lines, "--disallowed-tools") {
		if d := ruleDir(t, "Edit"); d != "" {
			a.Readonly = addUnique(a.Readonly, d)
		}
	}
	a.Readable = argvFlagValues(lines, "--add-dir")
	a.AllowedTools = argvFlagValues(lines, "--allowed-tools")
	a.AddDirs = a.Readable
	a.ArgvExact = true
	return a, nil
}

// JudgeAccess for Codex: a sandbox. The first writable directory is the working directory
// (-C), further ones ride on --add-dir; the project is read by absolute path.
func (codexDriver) JudgeAccess(argvFile string) (JudgeAccess, error) {
	lines, err := recordedLines(argvFile)
	if err != nil {
		return JudgeAccess{}, err
	}
	a := JudgeAccess{Model: argvFlagValue(lines, "-m"), ReadGranted: true, ReadsAnywhere: true}
	switch argvFlagValue(lines, "--sandbox") {
	case "read-only":
		a.ConfinedToWritable = true
	case "workspace-write":
		cfg := strings.Join(lines, "\n")
		a.ConfinedToWritable = strings.Contains(cfg, "sandbox_workspace_write.exclude_slash_tmp=true") &&
			strings.Contains(cfg, "sandbox_workspace_write.exclude_tmpdir_env_var=true")
		if d := argvFlagValue(lines, "-C"); d != "" {
			a.Writable = append(a.Writable, d)
		}
		a.Writable = append(a.Writable, argvFlagValues(lines, "--add-dir")...)
	}
	return a, nil
}

// cursorGrantRecord is what the recording shim keeps beside the argv: the private
// permission config the run was pointed at (it is removed when the run ends) and the
// engine's second-layer grant.
func cursorGrantRecord(argvFile string) string { return argvFile + ".grant" }

// JudgeAccess for Cursor: the argv names the model and the scratch workspace; the grants
// are in the private cli-config.json and the engine's SLOPRAIL_JUDGE_GRANT, both recorded
// by the shim.
func (cursorDriver) JudgeAccess(argvFile string) (JudgeAccess, error) {
	lines, err := recordedLines(argvFile)
	if err != nil {
		return JudgeAccess{}, err
	}
	a := JudgeAccess{Model: argvFlagValue(lines, "--model"), ReadsAnywhere: true, ConfinedToWritable: true}
	rec, err := os.ReadFile(cursorGrantRecord(argvFile))
	if err != nil {
		return JudgeAccess{}, err
	}
	for _, line := range strings.Split(string(rec), "\n") {
		if cfg, ok := strings.CutPrefix(line, "config="); ok {
			var c struct {
				Permissions struct {
					Allow []string `json:"allow"`
					Deny  []string `json:"deny"`
				} `json:"permissions"`
			}
			if json.Unmarshal([]byte(cfg), &c) != nil {
				continue
			}
			for _, t := range c.Permissions.Allow {
				if t == "Read(**)" {
					a.ReadGranted = true
				} else {
					a.OtherTools = append(a.OtherTools, t)
				}
			}
			for _, t := range c.Permissions.Deny {
				if d := ruleDir(t, "Write"); d != "" {
					a.Readonly = addUnique(a.Readonly, d)
				}
			}
		}
		if g, ok := strings.CutPrefix(line, "grant="); ok {
			var jg struct {
				Writable []string `json:"writable"`
			}
			if json.Unmarshal([]byte(strings.TrimPrefix(g, "SLOPRAIL_JUDGE_GRANT=")), &jg) == nil {
				a.Writable = append(a.Writable, jg.Writable...)
			}
		}
	}
	return a, nil
}
