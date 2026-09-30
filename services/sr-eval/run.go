package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/transcript"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run --fixture <dir>",
		Short: "Run one evaluation fixture against a real agent-under-test",
		Long: `Run one evaluation fixture: seed an isolated project, launch a real
agent-under-test through sr-agent, and score what its transcript shows.

Exit status carries the verdict, the same fail-closed contract a guardrail
check uses: 0 is a passing score, 1 is a failing one, 2 is the run itself
could not be completed (nothing to score at all).`,
		Args: cobra.NoArgs,
		RunE: runFixture,
	}
	cmd.Flags().String("fixture", "", "Path to the fixture directory (fixture.yaml + prompt.md + score script)")
	cmd.Flags().String("model", "", "Override the fixture's own model set — run the same fixture against a different model without editing fixture.yaml")
	cmd.Flags().Bool("keep", false, "Do not remove the isolated workspace after scoring — print its path instead")
	cmd.Flags().Bool("no-archive", false, "Do not record this run in the local eval-run archive (~/.local/share/sloprail/eval-runs, or $SLOPRAIL_EVAL_RUNS_DIR)")
	_ = cmd.MarkFlagRequired("fixture")
	return cmd
}

func runFixture(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	startedAt := time.Now()

	fixtureDir, _ := cmd.Flags().GetString("fixture")
	modelOverride, _ := cmd.Flags().GetString("model")
	keep, _ := cmd.Flags().GetBool("keep")
	noArchive, _ := cmd.Flags().GetBool("no-archive")

	fx, err := LoadFixture(fixtureDir)
	if err != nil {
		return err
	}
	if modelOverride != "" {
		fx.Model = modelOverride
	}
	prompt, err := fx.Prompt()
	if err != nil {
		return err
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}

	ws, err := newWorkspace(ctx, fx)
	if err != nil {
		return err
	}
	if keep {
		fmt.Fprintf(out, "workspace: %s (kept)\n", ws.root)
	} else {
		defer ws.Close()
	}

	// Every run gets a HOME of its own (see agentHome); a FreshMachine run's
	// also has no sr* binaries, only the plugin installed below. Every run
	// also gets THIS checkout built fresh (agent.binDir) — the caller no
	// longer trusts whatever sr-agent happens to be first on ITS OWN PATH
	// (the old siblingBinDir), which silently tested a stale install when one
	// existed. See agentHome's doc comment for the measured gap this closes.
	agent, err := ws.agentHome(ctx, root, fx.FreshMachine)
	if err != nil {
		return fmt.Errorf("build the agent's HOME: %w", err)
	}
	binDir := agent.binDir
	if fx.FreshMachine {
		fmt.Fprintf(out, "sr-eval: fresh machine: HOME %s — plugin installed, no sr binaries; install.sh's release is this checkout's build (%s)\n",
			agent.home, agent.releaseURL)
	} else {
		fmt.Fprintf(out, "sr-eval: agent HOME %s (isolated; the real one is never written)\n", agent.home)
	}
	if err := ws.writeSettings(root, agent.env, fx.Plugins); err != nil {
		return fmt.Errorf("wire project settings: %w", err)
	}

	// Commit the harness-written setup (.claude/settings.json, and whatever
	// the overlay added — .sloprail/, .claude/skills/) so the tree is CLEAN
	// before the agent's first turn. Without this, `git status` shows these
	// as the agent's own uncommitted changes from the first tool call, and
	// the engine's own baseline diff (which the gate's path matching and the
	// settled-file judging both read off) would count sr-eval's setup as
	// part of what the agent did. Unconditional now: newWorkspace's Seed
	// branch runs `git init` too (a Seed tree used to have no .git at all,
	// which left every file-guard unreachable — measured on a
	// real run where a guardrail never fired despite the agent's write
	// plainly matching its rule), so both branches now have a repository to
	// commit into.
	if err := ws.setUp(ctx, fx, agent.env); err != nil {
		return err
	}

	fmt.Fprintf(out, "sr-eval: fixture %s\n", fx.Dir)
	fmt.Fprintf(out, "sr-eval: project %s\n", ws.project)
	fmt.Fprintf(out, "sr-eval: launching agent-under-test (model %q)...\n", fx.Model)

	configDir := agent.configDir

	sessionID, err := newSessionID()
	if err != nil {
		return fmt.Errorf("make a session id: %w", err)
	}
	brief, err := fx.UserBrief()
	if err != nil {
		return err
	}
	maxTurns := 1
	if fx.User != nil {
		maxTurns = fx.User.MaxTurns
	}

	var agentErrs []string
	var dialogue []exchange
	message := prompt
	for turn := 1; turn <= maxTurns; turn++ {
		if turn > 1 {
			next, done, userErr := simulateUser(ctx, binDir, fx.User.UserModel(), brief, dialogue)
			if userErr != nil {
				// The conversation cannot go on, but what already happened is
				// a transcript worth scoring — same reasoning as an agent
				// error below.
				agentErrs = append(agentErrs, userErr.Error())
				fmt.Fprintf(cmd.ErrOrStderr(), "sr-eval: %v\n", userErr)
				break
			}
			if done {
				fmt.Fprintf(out, "sr-eval: simulated user is done after %d turn(s)\n", turn-1)
				break
			}
			message = next
			fmt.Fprintf(out, "sr-eval: turn %d, simulated user says: %s\n", turn, message)
		}
		var reply bytes.Buffer
		if agentErr := launchAgent(ctx, io.MultiWriter(out, &reply), cmd.ErrOrStderr(), ws, binDir,
			agentArgs(fx.Model, message, sessionID, turn > 1, fx.DisallowedTools), agent.env); agentErr != nil {
			agentErrs = append(agentErrs, fmt.Sprintf("turn %d: %v", turn, agentErr))
			fmt.Fprintf(cmd.ErrOrStderr(), "sr-eval: agent-under-test exited with error on turn %d: %v\n", turn, agentErr)
			// Not returned yet: a refusal or a crash mid-run still leaves a
			// transcript worth scoring — the scorer is what decides whether an
			// early stop is itself the pass condition (a gate that never let the
			// agent past its first refusal, say). Only the ABSENCE of a
			// transcript below is unrecoverable.
		}
		dialogue = append(dialogue, exchange{User: message, Agent: reply.String()})
	}
	agentErrText := strings.Join(agentErrs, "; ")

	transcriptPath := findTranscript(ws.project, configDir)
	if transcriptPath == "" {
		return fmt.Errorf("no transcript found under %s/projects — the agent-under-test never wrote one", configDir)
	}
	fmt.Fprintf(out, "sr-eval: transcript %s\n", transcriptPath)

	sr, scoreErr := score(ctx, fx, ws, transcriptPath, binDir, agent.home)

	rec := runRecord{
		Fixture:    filepath.Base(fx.Dir),
		FixtureDir: fx.Dir,
		Model:      fx.Model,
		Harness:    "claude-code",
		Passed:     sr.Passed,
		Reason:     sr.Reason,
		AgentError: agentErrText,
		Transcript: transcriptPath,
		StartedAt:  startedAt,
		FinishedAt: time.Now(),
	}
	if scoreErr != nil {
		rec.Reason = fmt.Sprintf("scorer could not run: %v", scoreErr)
	}

	if !noArchive {
		if archiveDir, archErr := archiveRun(rec, transcriptPath, sr.Stdout, sr.Stderr, sr.Verdict); archErr != nil {
			// Archiving failure is reported, not fatal — the scorer's own
			// verdict already ran and is the thing exit status carries.
			// Losing the archive of a run is a worse day than losing the
			// run itself.
			fmt.Fprintf(cmd.ErrOrStderr(), "sr-eval: warning: could not archive this run: %v\n", archErr)
		} else {
			fmt.Fprintf(out, "sr-eval: archived %s\n", archiveDir)
		}
	}

	if scoreErr != nil {
		fmt.Fprintf(out, "sr-eval: FAIL (scorer could not run: %v)\n", scoreErr)
		return &evalFailure{code: 2}
	}
	if sr.Passed {
		fmt.Fprintf(out, "sr-eval: PASS\n")
		return nil
	}
	fmt.Fprintf(out, "sr-eval: FAIL — %s\n", sr.Reason)
	return &evalFailure{code: 1}
}

// evalFailure carries the exit code a failed or unrunnable eval reports,
// following the same reasoning sr-agent's harnessRunError does: a caller
// scripting around this needs to tell "ran and failed" (1) apart from
// "could not be run at all" (2), and collapsing both to a bare error loses
// that distinction.
type evalFailure struct{ code int }

func (e *evalFailure) Error() string { return "" }

// exitCode is read by main() the same way sr-agent's is.
func exitCode(err error) int {
	if ef, ok := err.(*evalFailure); ok {
		return ef.code
	}
	return 1
}

// launchAgent runs the agent-under-test through sr-agent, in the seeded
// project, with the project's real .sloprail/ guardrails and the sloprail
// plugin's hooks ACTIVE — the opposite of sr-agent's own default, which
// isolates a judge call from exactly those. See main.go's doc comment for why
// going through sr-agent (rather than execing `claude` here) is what keeps
// this harness-agnostic.
//
// `settings: "{}"` overrides sr-agent's baseArgs isolation: sr-agent appends
// the caller's --claude-args AFTER its own baseArgs, and a later --settings
// wins over an earlier one (claude's own flag semantics, confirmed in
// sr-agent's BuildInvocation doc comment) — so this is the documented escape
// hatch, not a workaround.
//
// `permission-mode: "bypassPermissions"` is what makes this an UNATTENDED
// run: sr-agent's -p (print, non-interactive) mode still asks for per-tool
// approval by default, and nothing here can answer that prompt. This does
// NOT weaken what the eval proves — a gate's PreToolUse denial (sr-session's
// deny()) fires as its own hook decision independent of Claude Code's
// permission system, so the gate still refuses the agent's first write
// exactly as it would under any permission mode. What bypassPermissions
// removes is only Claude Code's OWN "may I write this file" question, which
// exists for an interactive human, not for a fixture proving a guardrail.
// env is the agent's own environment (agentHome): a HOME of its own inside
// the workspace, logged in through the linked ~/Library keychain, with this
// build's siblings first on PATH — or, for a FreshMachine run, nothing of
// sloprail on PATH at all, not even the directory sr-agent was found in
// (which is why sr-agent is exec'd by absolute path).
//
// args is one turn's sr-agent argv, from agentArgs (user.go), which also pins
// every turn of a run to one session.
func launchAgent(ctx context.Context, stdout, stderr io.Writer, ws *workspace, binDir string, args []string, env []string) error {
	agentBin := filepath.Join(binDir, "sr-agent")

	c := exec.CommandContext(ctx, agentBin, args...)
	c.Dir = ws.project
	c.Stdout = stdout
	c.Stderr = stderr
	c.Env = env
	return c.Run()
}

// findTranscript locates the .jsonl the agent-under-test wrote, via the same
// project-dir encoding the harness itself uses (internal/transcript.ProjectDir)
// — the one place that rule is defined. configDir is the agent's own
// ~/.claude, inside its isolated HOME (agentHome). There is exactly
// one project dir for the (fresh, temp) project path and exactly one session
// transcript in it — a multi-turn run resumes the SAME session on every turn
// (agentArgs), and Claude Code appends each resumed turn to that one file — so
// the newest .jsonl is the one to score.
func findTranscript(projectDir, configDir string) string {
	projDir := transcript.ProjectDir(configDir, projectDir)
	if projDir == "" {
		return ""
	}

	entries, err := os.ReadDir(projDir)
	if err != nil {
		return ""
	}
	var newest string
	var newestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = filepath.Join(projDir, e.Name())
		}
	}
	return newest
}
