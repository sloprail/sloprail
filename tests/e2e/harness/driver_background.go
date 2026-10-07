package harness

import "strings"

// Codex and Cursor give a background command no task to name and no output file in its
// receipt (harness-mocks background-bash). What they can do is send a command's output to a
// file that a later call reads. That is the equivalent the drivers render a Background Bash (and
// the ReadLaunchedOutput after it) as: the output is read from a file the launch wrote, by the
// harness's own read, and so it reaches the record as that read's output. The command runs to its
// end inside the call, not detached with `&`: a detached one races the read (measured: the read
// came first on Codex, and the file was empty). The receipt a Claude background launch leaves
// ("Command running in background with ID") has no counterpart, and a test that asserts it
// branches on CapBackgroundTasks.

// backgroundTmpMark stands for the run's temporary directory in a path a rendered step names: the
// shell expands it where the step runs, so it is the run's own (the drivers set TMPDIR to the
// Env's).
const backgroundTmpMark = "@@TMPDIR@@"

// backgroundShellTmp is the shell's spelling of the run's temporary directory.
const backgroundShellTmp = "${TMPDIR:-/tmp}"

// backgroundOutputFile is where the background command of the step id writes its output.
func backgroundOutputFile(id string) string { return backgroundTmpMark + "/sr-bg-" + id + ".output" }

// backgroundShell is the shell command that runs command with its output going to
// the step id's output file.
func backgroundShell(id, command string) string {
	out := strings.ReplaceAll(backgroundOutputFile(id), backgroundTmpMark, backgroundShellTmp)
	return "( " + command + "\n) > \"" + out + "\" 2>&1"
}

// launchedOutputActions are the scenario's actions with each ReadLaunchedOutput naming the output
// file of the most recent background launch before it, for the harnesses that make the file
// themselves.
func (s Scenario) launchedOutputActions() []Action {
	out := make([]Action, 0, len(s.turns))
	last := ""
	for _, t := range s.turns {
		a := t.act
		if a.Kind == ActToolUse && a.Background {
			last = a.ID
		}
		if t.launchedOutput {
			a.Input = map[string]string{"file_path": backgroundOutputFile(last)}
		}
		out = append(out, a)
	}
	return out
}
