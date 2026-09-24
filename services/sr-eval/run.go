package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/subbin"
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

	if err := ws.writeSettings(root); err != nil {
		return fmt.Errorf("wire project settings: %w", err)
	}

	if fx.Repo != "" {
		// Commit the harness-written setup (.claude/settings.json, and
		// whatever the overlay added — .sloprail/, .claude/skills/) so the
		// tree is CLEAN before the agent's first turn. Without this, `git
		// status` shows these as the agent's own uncommitted changes from the
		// first tool call, and the engine's own baseline diff (which the
		// gate's path matching and the settled-file judging both read off)
		// would count sr-eval's setup as part of what the agent did. A local
		// Seed fixture has no .git at all, so there is nothing to commit.
		if err := ws.commitSetup(); err != nil {
			return fmt.Errorf("commit harness setup: %w", err)
		}
	}

	binDir, err := siblingBinDir()
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "sr-eval: fixture %s\n", fx.Dir)
	fmt.Fprintf(out, "sr-eval: project %s\n", ws.project)
	fmt.Fprintf(out, "sr-eval: launching agent-under-test (model %q)...\n", fx.Model)

	var agentErrText string
	if agentErr := launchAgent(ctx, out, cmd.ErrOrStderr(), ws, binDir, fx.Model, prompt); agentErr != nil {
		agentErrText = agentErr.Error()
		fmt.Fprintf(cmd.ErrOrStderr(), "sr-eval: agent-under-test exited with error: %v\n", agentErr)
		// Not returned yet: a refusal or a crash mid-run still leaves a
		// transcript worth scoring — the scorer is what decides whether an
		// early stop is itself the pass condition (a gate that never let the
		// agent past its first refusal, say). Only the ABSENCE of a
		// transcript below is unrecoverable.
	}

	configDir := transcript.ConfigDir()
	transcriptPath := findTranscript(ws.project, configDir)
	if transcriptPath == "" {
		return fmt.Errorf("no transcript found under %s/projects — the agent-under-test never wrote one", configDir)
	}
	fmt.Fprintf(out, "sr-eval: transcript %s\n", transcriptPath)

	sr, scoreErr := score(ctx, fx, ws, transcriptPath, binDir)

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
		if archiveDir, archErr := archiveRun(rec, transcriptPath, sr.Stdout, sr.Stderr); archErr != nil {
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

// siblingBinDir is the directory holding sr-eval's own siblings
// (sr-session, sr-file, sr-mark, sr-agent) — resolved once via subbin so the
// agent-under-test's PATH can be given the SAME build under test rather than
// whatever those names resolve to elsewhere on the machine.
func siblingBinDir() (string, error) {
	agentBin, err := subbin.Find("sr-agent")
	if err != nil {
		return "", fmt.Errorf("locate sr-agent (sr-eval launches the agent-under-test through it): %w", err)
	}
	return filepath.Dir(agentBin), nil
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
// CLAUDE_CONFIG_DIR is deliberately left unset, so the agent-under-test uses
// the operator's real one. On macOS, Claude Code's desktop-app auth is a
// host-auth refresh scoped to the REAL ~/.claude — an isolated config dir
// loses it entirely ("Not logged in", confirmed empirically), which is a
// harder failure than the isolation was worth. Everything else about the run
// stays isolated: the project tree is a fresh temp directory, so the
// transcript this writes cannot collide with a real project's, and PATH is
// prepended with this build's own siblings.
func launchAgent(ctx context.Context, stdout, stderr io.Writer, ws *workspace, binDir, model, prompt string) error {
	agentBin := filepath.Join(binDir, "sr-agent")
	args := []string{
		"--model", model,
		"--claude-args", `{"settings":"{}","permission-mode":"bypassPermissions"}`,
		"--prompt", prompt,
	}

	c := exec.CommandContext(ctx, agentBin, args...)
	c.Dir = ws.project
	c.Stdout = stdout
	c.Stderr = stderr
	c.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	return c.Run()
}

// findTranscript locates the .jsonl the agent-under-test wrote, via the same
// project-dir encoding the harness itself uses (internal/transcript.ProjectDir)
// — the one place that rule is defined. configDir is the REAL Claude Code
// config dir (CLAUDE_CONFIG_DIR if the operator's own shell sets it, else
// ~/.claude — internal/transcript.ConfigDir's own resolution), since the
// agent-under-test runs against it for auth; see launchAgent. There is exactly
// one project dir for the (fresh, temp) project path and, for a single-turn
// eval run, exactly one session transcript in it, so the newest .jsonl is the
// one to score.
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
