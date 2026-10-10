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

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/procgroup"
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
	cmd.Flags().String("harness", "", "The harness the agent-under-test runs under: "+strings.Join(harness.Names(), ", ")+" (default: $"+harness.SelectEnv+", else "+defaultHarness+")")
	cmd.Flags().String("model", "", "Override the fixture's own model set — run the same fixture against a different model without editing fixture.yaml")
	cmd.Flags().String("variant", "", "Run one of the fixture's declared variants (fixture.yaml variants:) — its name reaches the setup and score scripts as SR_EVAL_VARIANT")
	cmd.Flags().Bool("keep", false, "Do not remove the isolated workspace after scoring — print its path instead")
	cmd.Flags().Bool("no-archive", false, "Do not record this run in the local eval-run archive (~/.local/share/sloprail/eval-runs, or $SLOPRAIL_EVAL_RUNS_DIR)")
	_ = cmd.MarkFlagRequired("fixture")
	return cmd
}

// runFixture is runFixtureSteps with every failure that is not a scored verdict
// turned into exit 2 and said, loudly, on BOTH streams. A run that could not
// even start (no disk, a fixture that does not load, a build that fails, an
// agent that wrote no transcript) is "could not be completed", not a failed
// eval, and must never end silent: measured, runs that died on ENOSPC left
// empty stdout and stderr and an exit status that read like an ordinary FAIL.
// sr:invariant authoring-tools/eval-unrunnable-is-not-a-failure
func runFixture(cmd *cobra.Command, _ []string) error {
	err := runFixtureSteps(cmd)
	if err == nil {
		return nil
	}
	if _, ok := err.(*evalFailure); ok {
		return err
	}
	msg := fmt.Sprintf("sr-eval: ERROR (the run could not be completed; nothing was scored): %v", err)
	// Best effort: the stream that is out of space may refuse the write, so
	// both are tried, and the message is also the returned error.
	fmt.Fprintln(cmd.OutOrStdout(), msg)
	return &evalFailure{code: 2, msg: msg}
}

func runFixtureSteps(cmd *cobra.Command) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	startedAt := time.Now()

	if err := preflight(); err != nil {
		return err
	}

	fixtureDir, _ := cmd.Flags().GetString("fixture")
	modelOverride, _ := cmd.Flags().GetString("model")
	keep, _ := cmd.Flags().GetBool("keep")
	noArchive, _ := cmd.Flags().GetBool("no-archive")

	harnessFlag, _ := cmd.Flags().GetString("harness")
	h, err := resolveHarness(harnessFlag, os.Getenv)
	if err != nil {
		return err
	}
	harnessID := h.Name()
	prov, err := harness.ProvisionerOf(h)
	if err != nil {
		return err
	}

	fx, err := LoadFixture(fixtureDir)
	if err != nil {
		return err
	}
	if !fx.Supports(harnessID) {
		// Not a failure and not a pass: nothing ran, nothing is archived.
		fmt.Fprintf(out, "sr-eval: SKIP — fixture %s declares harnesses %s; it does not run under %s\n",
			filepath.Base(fx.Dir), strings.Join(fx.Harnesses, ", "), harnessID)
		return nil
	}
	if modelOverride != "" {
		fx.Model = modelOverride
	}
	variant, _ := cmd.Flags().GetString("variant")
	if fx, err = fx.UseVariant(variant); err != nil {
		return err
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
	// Read before the build and the run: what the run is built from is what stands here now,
	// and a commit made while the agent works must not be recorded as its source.
	sloprailRef, fixtureRef := refOf(ctx, root), refOf(ctx, fx.Dir)
	agent, err := ws.agentHome(ctx, root, fx.FreshMachine, prov, h)
	if err != nil {
		return fmt.Errorf("build the agent's HOME: %w", err)
	}
	defer agent.stopAuthSync()
	binDir := agent.binDir
	if fx.FreshMachine {
		fmt.Fprintf(out, "sr-eval: fresh machine: HOME %s — plugin installed, no sr binaries; install.sh's release is this checkout's build (%s)\n",
			agent.home, agent.releaseURL)
	} else {
		fmt.Fprintf(out, "sr-eval: agent HOME %s (isolated; the real one is never written)\n", agent.home)
	}
	if fx.NoSloprail() {
		// The control of a comparison: no plugin, so no hooks and no rules-first
		// instruction, and (ExampleSloprailDir) none of the example's rules.
		fmt.Fprintf(out, "sr-eval: variant %s runs without sloprail: no plugin installed, no example rules applied\n", fx.Variant)
	} else if err := ws.installPlugins(ctx, prov, root, agent, fx.Plugins); err != nil {
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
	fmt.Fprintf(out, "sr-eval: launching agent-under-test (harness %s, model %q)...\n", harnessID, fx.Model)

	configDir := agent.configDir

	brief, err := fx.UserBrief()
	if err != nil {
		return err
	}
	maxTurns := 1
	if fx.User != nil {
		maxTurns = fx.User.MaxTurns
	}

	var agentErrs []string
	// An agent whose hooks fire only in its interactive mode is run there, on a terminal, in
	// one session for every turn; any other is run one-shot, a process per turn.
	var archiveFiles map[string][]byte
	inter, interactive := interactiveOf(h)
	if interactive {
		fmt.Fprintf(out, "sr-eval: %s's hooks fire only in its interactive mode: the agent-under-test runs there, on a terminal\n", harnessID)
		o := (&tuiRun{h: h, inter: inter, harnessID: harnessID, fx: fx, ws: ws, agent: agent, binDir: binDir,
			prompt: prompt, brief: brief, maxTurns: maxTurns, out: out, errs: cmd.ErrOrStderr()}).run(ctx)
		agentErrs, archiveFiles = o.errs, o.files
	}
	var dialogue []exchange
	var sessionID string // the first turn's session, by which every later turn resumes it
	message := prompt
	for turn := 1; !interactive && turn <= maxTurns; turn++ {
		if turn > 1 {
			next, done, userErr := simulateUser(ctx, harnessID, binDir, fx.User.UserModel(), brief, dialogue)
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
			agentArgs(harnessID, fx.Model, message, sessionID, fx.DisallowedTools), agent.env); agentErr != nil {
			agentErrs = append(agentErrs, fmt.Sprintf("turn %d: %v", turn, agentErr))
			fmt.Fprintf(cmd.ErrOrStderr(), "sr-eval: agent-under-test exited with error on turn %d: %v\n", turn, agentErr)
			// Not returned yet: a refusal or a crash mid-run still leaves a
			// transcript worth scoring — the scorer is what decides whether an
			// early stop is itself the pass condition (a gate that never let the
			// agent past its first refusal, say). Only the ABSENCE of a
			// transcript below is unrecoverable.
		}
		dialogue = append(dialogue, exchange{User: message, Agent: reply.String()})
		if turn == 1 && maxTurns > 1 {
			// Resumed by exact id: read off the record the first turn wrote. No id means
			// no exact resume, and the harness's "latest session" form is not a substitute.
			sessionID = prov.SessionID(findTranscript(h.Transcripts(), ws.project, configDir))
			if sessionID == "" {
				agentErrs = append(agentErrs, "the first turn left no session record to resume by id")
				fmt.Fprintf(cmd.ErrOrStderr(), "sr-eval: %s\n", agentErrs[len(agentErrs)-1])
				break
			}
		}
	}
	agentErrText := strings.Join(agentErrs, "; ")

	transcriptPath := findTranscript(h.Transcripts(), ws.project, configDir)
	if transcriptPath == "" {
		return fmt.Errorf("no transcript found under %s — the agent-under-test never wrote one", h.Transcripts().ProjectDir(configDir, ws.project))
	}
	fmt.Fprintf(out, "sr-eval: transcript %s\n", transcriptPath)

	sr, scoreErr := score(ctx, fx, ws, harnessID, transcriptPath, binDir, agent.home)

	rec := runRecord{
		Fixture:     filepath.Base(fx.Dir),
		FixtureDir:  fx.Dir,
		Model:       fx.Model,
		Variant:     fx.Variant,
		Sloprail:    sloprailRef,
		FixtureRepo: fixtureRef,
		Harness:     harnessID,
		Passed:      sr.Passed,
		Reason:      sr.Reason,
		AgentError:  agentErrText,
		Files:       archiveFiles,
		Transcript:  transcriptPath,
		StartedAt:   startedAt,
		FinishedAt:  time.Now(),
	}
	if scoreErr != nil {
		rec.Reason = fmt.Sprintf("scorer could not run: %v", scoreErr)
	}

	if !noArchive {
		// What the sessions left behind (the check results above all) is part of the run's record.
		// A failure to take it is reported and the run is archived without it.
		if files, serr := runSessionFiles(ctx, agent, ws.project, harnessID); serr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sr-eval: warning: the session archive (state, tracked ranges, check results) was not taken: %v\n", serr)
		} else {
			if rec.Files == nil {
				rec.Files = map[string][]byte{}
			}
			for name, body := range files {
				rec.Files[name] = body
			}
		}
		// The judges' own sessions, what every session cost, and the repository as it ended.
		if rec.Files == nil {
			rec.Files = map[string][]byte{}
		}
		judges := judgeFiles(transcriptPath)
		for name, body := range judges {
			rec.Files[name] = body
		}
		agentBody, _ := os.ReadFile(transcriptPath)
		rec.Files["usage.json"] = usageFile(agentBody, judges)
		if bundle, berr := repoBundle(ctx, ws.project); berr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sr-eval: warning: the end-state repository was not kept: %v\n", berr)
		} else {
			rec.Files["repo.bundle"] = bundle
		}
		if archiveDir, archErr := archiveRun(rec, transcriptPath, subagentFiles(h.Transcripts(), transcriptPath), sr.Stdout, sr.Stderr, sr.Verdict); archErr != nil {
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
type evalFailure struct {
	code int
	msg  string // empty for a scored verdict, already reported on stdout
}

func (e *evalFailure) Error() string { return e.msg }

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
// `--agent-run` is sr-agent's own mode for the agent under test: its judge
// isolation (hooks off, plugins unloaded) is replaced by the harness's unattended
// flags (agentRunArgs in sr-agent's spec: bypassed permissions for Claude Code,
// the approval/sandbox bypass for Codex, --force for Cursor), because nothing here
// can answer a permission prompt. This does NOT weaken what the eval proves — a
// gate's PreToolUse denial fires as its own hook decision independent of the
// harness's permission system; what is removed is only the harness's OWN "may I
// write this file" question, which exists for an interactive human.
// env is the agent's own environment (agentHome): a HOME of its own inside
// the workspace, logged in through the linked ~/Library keychain, with this
// build's siblings first on PATH — or, for a FreshMachine run, nothing of
// sloprail on PATH at all, not even the directory sr-agent was found in
// (which is why sr-agent is exec'd by absolute path).
//
// args is one turn's sr-agent argv, from agentArgs (user.go), which also pins
// every later turn of a run to the first turn's session, by its exact id.
func launchAgent(ctx context.Context, stdout, stderr io.Writer, ws *workspace, binDir string, args []string, env []string) error {
	agentBin := filepath.Join(binDir, "sr-agent")

	c := exec.CommandContext(ctx, agentBin, args...)
	c.Dir = ws.project
	c.Stdout = stdout
	c.Stderr = stderr
	c.Env = env
	return procgroup.Run(c, true)
}

// subagentFiles are the files of the sub-agents the session spawned, asked of the
// harness (nil for one that cannot tie them to their parent).
func subagentFiles(t harness.Transcripts, transcriptPath string) []harness.SubagentFile {
	if l, ok := t.(harness.SubagentLocator); ok {
		return l.SubagentFiles(transcriptPath)
	}
	return nil
}

// defaultHarness is the harness a run uses when neither --harness nor SLOPRAIL_HARNESS names one.
const defaultHarness = "claude"

// resolveHarness is the harness a run's agent-under-test uses: the flag, else
// SLOPRAIL_HARNESS, else Claude Code. Never detected from the operator's own session:
// whichever harness the operator happens to be typing in must not pick the one under test.
func resolveHarness(flag string, getenv func(string) string) (harness.Harness, error) {
	id := flag
	if id == "" {
		id = getenv(harness.SelectEnv)
	}
	if id == "" {
		id = defaultHarness
	}
	h, ok := harness.Lookup(id)
	if !ok {
		return nil, fmt.Errorf("unknown harness %q — one of %s", id, strings.Join(harness.Names(), ", "))
	}
	return h, nil
}

// findTranscript locates the session record the agent-under-test wrote, through the
// harness's own layout (Transcripts.ProjectDir: Claude Code's project directory,
// Cursor's, Codex's date-sharded sessions tree), under configDir, the agent's own
// config directory inside its isolated HOME (agentHome). The run's HOME is fresh, so
// whatever .jsonl lies under that directory is this run's; a multi-turn run continues
// ONE session, so its record is one file. Sub-agent records sit deeper than their
// parent's (Claude's <session>/subagents/, Cursor's alongside), so the shallowest file
// wins and, among equals, the newest.
func findTranscript(t harness.Transcripts, projectDir, configDir string) string {
	root := t.ProjectDir(configDir, transcript.ResolveWorkDir(projectDir))
	if root == "" {
		return ""
	}
	var best string
	var bestDepth int
	var bestTime time.Time
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		depth := strings.Count(rel, string(filepath.Separator))
		if best == "" || depth < bestDepth || (depth == bestDepth && info.ModTime().After(bestTime)) {
			best, bestDepth, bestTime = path, depth, info.ModTime()
		}
		return nil
	})
	return best
}
