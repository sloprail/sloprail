package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/ruletest"
	"github.com/sloprail/sloprail/internal/subbin"
)

func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test [<rule>...]",
		Short: "Run the cases beside a project's own rules: no model, no harness, only bash and git",
		Long: `Run the test cases kept beside a project's rules (.sloprail/<nature>/<name>/tests/<case>/) and
report what the engine did with each.

A case is a folder:

  case.yaml        expect: refuse|permit, reason_contains, judges: {<judge file>: {pass, reasoning}}, with:, contexts:
  setup.sh         pure bash + git: builds the repository (cwd is a fresh temp repo that already holds the rule)
  trajectory.yaml  optional: an ordered list of NORMALIZED sloprail events (the flat event model), run:
                   steps that change the repository between them, sub-agent starts/stops, Stop

Without a trajectory the case judges base..HEAD through the real file-guard path (what sr-checks run
does). With one, each event goes through the same dispatch the hooks use: contexts activate and
persist across events, gates refuse, a Stop runs the real Stop path. The case is written in the
engine's own events, so it holds whichever harness the project runs on.

Judges never call a model: each is answered from the case's canned verdict, and a case that reaches a
judge it does not stub fails. --live-judges asks the real judges instead (judge-accuracy runs: the case's
expectation is then checked against the live verdict; reason_contains is skipped).

The rules are named by <nature>/<name> or the bare name; none runs every rule that has cases. Exits 1
when any case fails.

  --rules-dir  the .sloprail directory to read (default: the repository's)
  --plugin     name the directory as a plugin's (its rules are qualified <plugin>/<nature>/<name>)
  --keep       leave each case's sandbox in place and print where (to look at the repository it built)
  --json       print the results as JSON`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runTests(cmd, args, false) },
	}
	addTestFlags(cmd)
	return cmd
}

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor [<rule>...]",
		Short: "Check that every project rule loads and is proved by cases, then run them",
		Long: `Check a project's own rules before any judge runs.

For each rule: the declaration loads (the loader's own faults are reported by name); it has at least one
case that expects a refusal and one that expects a permit (a context: one case that leaves it active and
one that leaves it inactive); each judge it declares is stubbed to pass in one case and to fail in
another; and then every case runs, as sr-checks test runs it. Rules with missing cases are listed with
what is missing. Exits 1 on any fault.

  --allow-untested   a rule with no cases at all is reported but does not fail the run (the rollout
                     switch: existing rules are grandfathered until their first case; a rule that has
                     any case must be fully covered and passing)
  --no-run           only check declarations and coverage, run nothing
  --rules-dir, --plugin, --keep, --json   as for sr-checks test`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runTests(cmd, args, true) },
	}
	addTestFlags(cmd)
	cmd.Flags().Bool("allow-untested", false, "A rule with no cases at all does not fail the run")
	cmd.Flags().Bool("no-run", false, "Only check declarations and coverage; run no case")
	return cmd
}

func addTestFlags(cmd *cobra.Command) {
	cmd.Flags().String("rules-dir", "", "The .sloprail directory to read (default: the repository's)")
	cmd.Flags().String("plugin", "", "Name the directory as a plugin's")
	cmd.Flags().Bool("live-judges", false, "Ask the real judges instead of the canned ones (judge-accuracy runs)")
	cmd.Flags().Bool("keep", false, "Keep each case's sandbox and print where it is")
	cmd.Flags().Bool("json", false, "Print the results as JSON")
}

// runFileGuards is the file-guard evaluation `sr-checks test` hands the runner: the
// real `sr-checks run` code over base..head of the repository the process is in.
func runFileGuards(repo, base, head string, stderr io.Writer) ([]string, error) {
	c := &cobra.Command{}
	addRangeFlags(c)
	if err := c.Flags().Set("base", base); err != nil {
		return nil, err
	}
	if err := c.Flags().Set("head", head); err != nil {
		return nil, err
	}
	c.SetOut(io.Discard)
	c.SetErr(stderr)
	return evaluate(c, modeRun)
}

func resolveRulesDir(cmd *cobra.Command) (string, error) {
	dir, _ := cmd.Flags().GetString("rules-dir")
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root, err := gitrepo.Root(cwd)
		if err != nil || root == "" {
			root = cwd
		}
		dir = filepath.Join(root, ".sloprail")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("sloprail: %s is not a directory: name the .sloprail to test with --rules-dir", dir)
	}
	return dir, nil
}

type testOutput struct {
	Rules []ruleOutput `json:"rules"`
	Pass  bool         `json:"pass"`
}

type ruleOutput struct {
	Rule     string                `json:"rule"`
	Untested bool                  `json:"untested,omitempty"`
	Invalid  []string              `json:"invalid,omitempty"`
	Missing  []string              `json:"missing,omitempty"`
	Errors   []string              `json:"caseErrors,omitempty"`
	Cases    []ruletest.CaseResult `json:"cases,omitempty"`
}

func runTests(cmd *cobra.Command, args []string, doctor bool) error {
	dir, err := resolveRulesDir(cmd)
	if err != nil {
		return err
	}
	plugin, _ := cmd.Flags().GetString("plugin")
	live, _ := cmd.Flags().GetBool("live-judges")
	keep, _ := cmd.Flags().GetBool("keep")
	asJSON, _ := cmd.Flags().GetBool("json")
	allowUntested, _ := cmd.Flags().GetBool("allow-untested")
	noRun, _ := cmd.Flags().GetBool("no-run")

	all, err := ruletest.LoadRules(dir, plugin)
	if err != nil {
		return fmt.Errorf("sloprail: %w", err)
	}
	selected, err := ruletest.Select(all, args)
	if err != nil {
		return fmt.Errorf("sloprail: %w", err)
	}

	// A killed run leaves no sandbox behind (a kept one is the caller's to delete).
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		<-sigs
		ruletest.CleanupAll()
		os.Exit(130)
	}()

	runner := &ruletest.Runner{Live: live, Keep: keep, RunFileGuards: runFileGuards}
	if bin, err := subbin.Find("sr-session"); err == nil {
		runner.BinDir = filepath.Dir(bin)
	} else {
		return fmt.Errorf("sloprail: sr-checks test replays the session through sr-session: %w", err)
	}

	out := testOutput{Pass: true}
	w := cmd.OutOrStdout()
	total, failedCases := 0, 0
	for _, rule := range selected {
		rr := ruletest.Inspect(rule)
		explicit := len(args) > 0
		ro := ruleOutput{Rule: rule.FQN(), Untested: rr.Untested, Invalid: rule.Invalid, Missing: rr.Missing}
		for _, e := range rr.CaseErrors {
			ro.Errors = append(ro.Errors, e.Error())
			failedCases++
		}
		// Run cases: a rule that does not load has none that can run.
		if len(rule.Invalid) == 0 && len(rr.Cases) > 0 && !(doctor && noRun) {
			runner.RunRule(&rr, all)
			ro.Cases = rr.Results
			for _, r := range rr.Results {
				total++
				if !r.Pass {
					failedCases++
				}
			}
		}
		failed := rr.Failed(false)
		switch {
		case !doctor && rr.Untested:
			failed = explicit // listing every rule is no failure; naming an untested one is
		case doctor && rr.Untested:
			failed = !allowUntested
		case !doctor:
			// test: coverage is doctor's business; only cases and unreadable cases fail here
			failed = len(rr.CaseErrors) > 0 || len(rule.Invalid) > 0
			for _, r := range rr.Results {
				if !r.Pass {
					failed = true
				}
			}
		}
		if failed {
			out.Pass = false
		}
		out.Rules = append(out.Rules, ro)
		if !asJSON {
			printRule(w, rule, rr, doctor, allowUntested, failed)
		}
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(w, "\n%d rule(s), %d case(s) run, %d failed\n", len(selected), total, failedCases)
	}
	if !out.Pass {
		os.Exit(1)
	}
	return nil
}

func printRule(w io.Writer, rule ruletest.Rule, rr ruletest.RuleReport, doctor, allowUntested, failed bool) {
	mark := "ok  "
	if failed {
		mark = "FAIL"
	}
	switch {
	case len(rule.Invalid) > 0:
		fmt.Fprintf(w, "%s %s  does not load (%s)\n", mark, rule.FQN(), rule.Where())
		for _, r := range rule.Invalid {
			fmt.Fprintf(w, "       - %s\n", r)
		}
		return
	case rr.Untested:
		note := "no cases"
		if !doctor {
			note = "no cases (nothing proves this rule)"
		}
		if doctor && allowUntested {
			mark = "warn"
			note = "no cases yet (allowed: grandfathered until its first case)"
		}
		fmt.Fprintf(w, "%s %s  %s\n", mark, rule.FQN(), note)
		if doctor && failed {
			fmt.Fprintf(w, "       add .sloprail/%s/%s/tests/<case>/{case.yaml,setup.sh}: at least one that refuses and one that permits (see the authoring-guardrails skill, testing.md)\n", rule.Nature, rule.Name)
		}
		return
	}
	fmt.Fprintf(w, "%s %s  %d case(s)\n", mark, rule.FQN(), len(rr.Cases))
	for _, e := range rr.CaseErrors {
		fmt.Fprintf(w, "       - cannot read %v\n", e)
	}
	for _, m := range rr.Missing {
		if doctor {
			fmt.Fprintf(w, "       - missing: %s\n", m)
		}
	}
	for _, r := range rr.Results {
		res := "PASS"
		if !r.Pass {
			res = "FAIL"
		}
		fmt.Fprintf(w, "       %s %-32s %s\n", res, r.Case, verdictLine(r))
		for _, p := range r.Problems {
			fmt.Fprintf(w, "            - %s\n", p)
		}
		if !r.Pass {
			printEngineSaid(w, r.Stderr())
		}
		if r.Sandbox != "" {
			fmt.Fprintf(w, "            sandbox kept: %s\n", r.Sandbox)
		}
	}
}

// printEngineSaid shows what the engine wrote to stderr during a failing case (load faults,
// progress, a check's own diagnostics), indented under it and capped: the first thing to read
// when a case does not do what its author expected.
func printEngineSaid(w io.Writer, said string) {
	said = strings.TrimSpace(said)
	if said == "" {
		return
	}
	lines := strings.Split(said, "\n")
	const max = 30
	fmt.Fprintln(w, "            engine said:")
	for i, l := range lines {
		if i == max {
			fmt.Fprintf(w, "              … %d more line(s)\n", len(lines)-max)
			break
		}
		fmt.Fprintf(w, "              %s\n", l)
	}
}

func verdictLine(r ruletest.CaseResult) string {
	if r.Verdict == "" {
		return ""
	}
	s := string(r.Verdict)
	if r.Verdict == ruletest.ExpectRefuse && r.Reason != "" {
		reason := strings.Join(strings.Fields(r.Reason), " ")
		if len(reason) > 110 {
			reason = reason[:110] + "…"
		}
		s += ": " + reason
	}
	return s
}
