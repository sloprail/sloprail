package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

func lifecycleProject(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards())
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "seed.md", "seed\n")
	e.CommitAll(proj, "the project")
	return e, proj
}

// T034_01: an agent running the hook entry points itself is refused, with the way forward;
// the read-only commands, and the documented load check, are not.
func TestT034_01_LifecycleCommandsAreRefusedAndReadOnlyOnesAreNot(t *testing.T) {
	const sess = "s-034-01"
	e, proj := lifecycleProject(t)

	for i, cmd := range []string{
		`echo '{"session_id":"fake-1"}' | sr-session start`,
		`sr-session stop < /dev/null`,
		`cd . && sr-session subagent-stop`,
		`sr session pre-tool`,
	} {
		res := e.Run(proj, sess, "run it", Turns("done", Bash("l"+string(rune('a'+i)), cmd)))
		if !res.Refused() || !res.Saw("no-lifecycle-commands") || !res.Saw("Stop hook judges") {
			t.Fatalf("%q was not refused with the way forward:\n%s", cmd, res.Output)
		}
	}

	for i, cmd := range []string{
		"sr-session start < /dev/null",
		"sr-session refs list --session nothing; sr-session trajectory describe --help",
		"sr-checks status",
	} {
		res := e.Run(proj, sess, "read it", Turns("done", Bash("r"+string(rune('a'+i)), cmd)))
		if res.Saw("no-lifecycle-commands") {
			t.Fatalf("%q was refused by the lifecycle gate:\n%s", cmd, res.Output)
		}
	}
}
