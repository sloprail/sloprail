package e2e

import (
	"fmt"
	"testing"
)

// T034_02: an agent that wants a verdict of its own does not type the command the plain
// way: an env prefix, a shell wrapper, the binary's path, the `sr` front, a subshell, a
// chain, a pipe. Every spelling that RUNS a lifecycle entry point is refused. And the
// words alone (an echo, a grep, a commit message) run nothing, so they are not.
func TestT034_02_EverySpellingThatRunsALifecycleCommandIsRefused(t *testing.T) {
	e, proj := lifecycleProject(t)

	refused := []string{
		`SR_X=1 sr-session start`,
		`FOO=bar BAZ=qux sr-session stop`,
		`env SR_X=1 sr-session pre-tool`,
		`bash -c 'sr-session start'`,
		`sh -c "sr-session subagent-stop"`,
		`bash -lc 'cd /tmp && sr-session stop'`,
		`/usr/local/bin/sr-session stop`,
		`./bin/sr-session start`,
		`sr session start`,
		`sr session subagent-stop`,
		`sr-session subagent-start`,
		`sr session subagent-start`,
		`echo '{"agent_id":"x"}' | sr-session subagent-start`,
		`sr-session worktree-remove < /dev/null`,
		`sr session worktree-remove`,
		`(sr-session pre-tool)`,
		`true && sr-session stop`,
		`false || sr-session start`,
		`echo '{}' | sr-session pre-tool`,
		`cd /tmp && sr-session stop`,
		`time sr-session start`,
		`command sr-session stop`,
		`nohup sr-session stop &`,
		`{ sr-session start; }`,
		`if true; then sr-session stop; fi`,
	}
	for i, cmd := range refused {
		res := e.Run(proj, "s-034-02", "run it", Turns("done", Bash(fmt.Sprintf("v%d", i), cmd)))
		if !res.Refused() || !res.Saw("no-lifecycle-commands") {
			t.Errorf("%q runs a lifecycle command and was not refused:\n%s", cmd, res.Output)
		}
	}

	allowed := []string{
		`echo "sr-session start is the harness's"`,
		`grep -n "sr-session stop" README.md || true`,
		`sr-session trajectory describe --help`,
		`sr-session changeset --help`,
		`sr-session refs list --session nothing; true`,
		`sr-checks show --help`,
		`git commit -q --allow-empty -m "document sr-session start"`,
	}
	for i, cmd := range allowed {
		res := e.Run(proj, "s-034-02", "read it", Turns("done", Bash(fmt.Sprintf("a%d", i), cmd)))
		if res.Saw("no-lifecycle-commands") {
			t.Errorf("%q runs nothing of the kind but was refused:\n%s", cmd, res.Output)
		}
	}
}
