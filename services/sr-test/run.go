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
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/srtest"
	"github.com/sloprail/sloprail/internal/srtest/agent"
)

type flags struct {
	jobs       int
	timeout    time.Duration
	liveJudges bool
	keep       bool
}

func addFlags(c *cobra.Command, f *flags) {
	c.Flags().IntVar(&f.jobs, "jobs", 4, "cases run in parallel")
	c.Flags().DurationVar(&f.timeout, "timeout", 5*time.Minute, "per-case timeout")
	c.Flags().BoolVar(&f.liveJudges, "live-judges", false, "let judges call a real model (SR_CHECKS_JUDGE_MOCKS stays unset)")
	c.Flags().BoolVar(&f.keep, "keep", false, "keep each case's temp dir and print its path on stderr")
}

func newRunCmd() *cobra.Command {
	var f flags
	c := &cobra.Command{
		Use:   "run [path]",
		Short: "Run every **/.sloprail/tests/*/test.sh below path; one JSONL result per case on stdout",
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
	var f flags
	c := &cobra.Command{
		Use:   "doctor [path]",
		Short: "Run the cases, then list the rules no case exercised",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := execute(cmd, args, f)
			if err != nil {
				return err
			}
			un := srtest.Uncovered(results)
			for _, u := range un {
				fmt.Fprintln(cmd.OutOrStdout(), "uncovered: "+u)
			}
			if len(un) > 0 {
				return fmt.Errorf("sr-test: %d rule(s) no case exercises", len(un))
			}
			fmt.Fprintln(cmd.OutOrStdout(), "every rule is covered")
			return nil
		},
	}
	addFlags(c, &f)
	return c
}

func execute(cmd *cobra.Command, args []string, f flags) ([]srtest.Result, error) {
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
		Root: root, Jobs: f.jobs, Timeout: f.timeout, LiveJudges: f.liveJudges, Keep: f.keep,
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
			origins = append(origins, declaration.Origin{Plugin: pluginName(p), Root: p})
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

func pluginName(dir string) string {
	if raw, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json")); err == nil {
		var m struct{ Name string }
		if json.Unmarshal(raw, &m) == nil && m.Name != "" {
			return m.Name
		}
	}
	return filepath.Base(dir)
}

func qual(plugin, name string) string {
	if plugin == "" {
		return name
	}
	return plugin + "/" + name
}
