package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/judgelimit"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/srtest"
	"github.com/sloprail/sloprail/internal/srtest/agent"
)

type flags struct {
	jobs    int
	timeout time.Duration
	only    []string
	rule    []string
	keep    bool
}

func addFlags(c *cobra.Command, f *flags) {
	c.Flags().IntVar(&f.jobs, "jobs", srtest.DefaultJobs, "cases run in parallel")
	c.Flags().DurationVar(&f.timeout, "timeout", 5*time.Minute, "per-case timeout")
	c.Flags().StringSliceVar(&f.only, "only", nil, "run only cases whose subject contains one of these")
	c.Flags().StringSliceVar(&f.rule, "rule", nil, "run only the cases of these rules: <nature>/<rule> (gate/cite-before-commit, file-guard/structure)")
	c.Flags().BoolVar(&f.keep, "keep", false, "keep each case's temp dir and print its path on stderr")
}

func newRunCmd() *cobra.Command {
	var f flags
	c := &cobra.Command{
		Use:   "run [path]",
		Short: "Run every case below path; one JSONL result per case on stdout",
		Long:  caseLayout,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := execute(cmd, args, f)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			bad := 0
			for _, r := range results {
				_ = enc.Encode(r)
				if r.Status != srtest.Pass {
					bad++
				}
			}
			if bad > 0 {
				return fmt.Errorf("sr-test: %d of %d cases did not pass", bad, len(results))
			}
			return nil
		},
	}
	addFlags(c, &f)
	return c
}

func newDoctorCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "doctor [path]",
		Short: "List the rules that have no case",
		Long: "List every rule folder holding a declaration (and the structure gate) whose tests/ (structure.tests/) holds no case, as\n" +
			"\"uncovered: <nature>:<rule>\". Deterministic: nothing is run; a case's owner is the folder it sits in.\n\n" +
			"--json prints one JSON object per uncovered rule instead ({\"nature\", \"rule\", \"dir\", \"qualified\": dir is the\n" +
			".sloprail's parent relative to the path, \".\" for its own) and exits 0 whether or not any is uncovered: a non-zero\n" +
			"status then means the scan itself failed, and no output means every rule is covered.\n\n" + caseLayout,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) > 0 {
				root = args[0]
			}
			root, err := filepath.Abs(root)
			if err != nil {
				return err
			}
			if asJSON {
				rules, err := srtest.UncoveredRules(root)
				if err != nil {
					return err
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				for _, r := range rules {
					if err := enc.Encode(r); err != nil {
						return err
					}
				}
				return nil
			}
			un, err := srtest.Uncovered(root)
			if err != nil {
				return err
			}
			for _, u := range un {
				fmt.Fprintln(cmd.OutOrStdout(), "uncovered: "+u)
			}
			if len(un) > 0 {
				return fmt.Errorf("sr-test: %d rule(s) have no case", len(un))
			}
			fmt.Fprintln(cmd.OutOrStdout(), "every rule is covered")
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "one JSON object per uncovered rule (nature, rule, dir, qualified); exit 0 unless the scan failed")
	return c
}

// caseLayout documents where a case lives and how its result is named (shared by the commands' help).
const caseLayout = `A case lives in its OWNING rule's folder, and has exactly one owner (the folder it sits in):

  .sloprail/<gate|file-guard|context>/<rule>/tests/<case>/test.sh
  .sloprail/file-guard/structure.tests/<case>/test.sh        (the structure gate, one file)

Any .sloprail/ below the path counts (a plugin's included). Each result line carries
  owner    "<nature>/<rule>" within its .sloprail/ ("gate/cite-before-commit", "file-guard/structure")
  subject  "<owner>:<case>" in the root .sloprail/, "<dir of the .sloprail's parent>:<owner>:<case>" below it
           (marketplace/plugins/sloprail:gate/cite-before-commit:<case>)
In a case: SR_TEST_CASE_DIR is a copy of the case folder, SR_TEST_SLOPRAIL_DIR the original .sloprail/.
The environment is built from scratch, not inherited: a fake HOME, its own TMPDIR, a PATH of the sloprail binaries, jq, git, bash and
the mock claude plus /usr/bin:/bin, git reading only the case's own config, and nothing else from the caller (credentials and proxy settings
included: a judge is always a mock script, see SR_CHECKS_JUDGE_MOCKS).`

func execute(cmd *cobra.Command, args []string, f flags) ([]srtest.Result, error) {
	// Shared with `sr-checks run`: the machine runs only a few of them at once, the rest queue.
	if release, err := judgelimit.New(cmd.ErrOrStderr()).HoldRunSlot(); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sr-test: run slots unavailable, running anyway:", err)
	} else {
		defer release()
	}
	root := "."
	if len(args) > 0 {
		root = args[0]
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var bins []string
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			bins = append(bins, filepath.Dir(exe))
		}
	}
	core := ""
	if co, err := agent.CheckoutRoot(); err == nil {
		core = filepath.Join(co, "marketplace", "plugins", "sloprail")
	}
	return srtest.Run(root, srtest.Options{CorePluginDir: core,
		Root: root, Jobs: f.jobs, Only: f.only, Owners: f.rule, Timeout: f.timeout, Keep: f.keep,
		Stderr: cmd.ErrOrStderr(), BinDirs: bins, Rules: loadRules,
	})
}

// loadRules lists the rules in force for a case's context. A project case: the declaration loader on its
// workspace, plugins included. A plugin case: the plugins under test, loaded from their local folders.
func loadRules(c srtest.Context, w io.Writer) []string {
	reg, err := modules.Registry()
	if err != nil {
		return nil
	}
	var l declaration.Loaded
	if len(c.Plugins) == 0 {
		l = checkrun.LoadDeclarations(w, c.Dir, reg)
	} else {
		var origins []declaration.Origin
		for _, p := range c.Plugins {
			origins = append(origins, declaration.Origin{Plugin: srtest.PluginName(p), Root: p})
		}
		l, err = declaration.NewWithPlugins(checkrun.DotDir(c.Dir), origins).Load(reg)
		if err != nil {
			fmt.Fprintf(w, "sr-test: load plugin declarations: %v\n", err)
			return nil
		}
	}
	var out []string
	for _, g := range l.FileGuards {
		out = append(out, "file-guard:"+qual(g.Origin.Plugin, g.Name))
	}
	for _, g := range l.Gates {
		out = append(out, "gate:"+qual(g.Origin.Plugin, g.Name))
	}
	for _, c := range l.Contexts {
		out = append(out, "context:"+qual(c.Origin.Plugin, c.Name))
	}
	for _, s := range l.Structures {
		out = append(out, "structure:"+qual(s.Origin.Plugin, "structure"))
	}
	return out
}

func qual(plugin, name string) string {
	if plugin == "" {
		return name
	}
	return plugin + "/" + name
}
