package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
)

// reportJudgeTemplates is the load check's look at every judge template the
// loaded rules name: one that cannot be read, does not parse, or names a filter
// the engine does not have is reported, naming the rule, the template and why.
// It returns how many were reported.
//
// The rule still LOADS: a judge whose template cannot render refuses whenever it
// runs (fail-closed, dispatch's renderJudgeTemplate), which is a working safety
// mechanism — the report is so the author sees it before an agent meets it.
func reportJudgeTemplates(cmd *cobra.Command, loaded declaration.Loaded) int {
	type judged struct {
		rule   string
		dir    string
		checks []declaration.Check
	}
	var rules []judged
	for _, g := range loaded.FileGuards {
		rules = append(rules, judged{g.Qualified(), g.Dir, g.Checks})
	}
	for _, g := range loaded.Gates {
		rules = append(rules, judged{g.Qualified(), g.Dir, g.Checks})
	}
	broken := 0
	for _, r := range rules {
		for _, c := range r.checks {
			if c.Judge == "" {
				continue
			}
			path := c.Judge
			if !filepath.IsAbs(path) {
				path = filepath.Join(r.dir, path)
			}
			src, err := os.ReadFile(path)
			if err == nil {
				err = dispatchcore.CheckTemplate(string(src))
			}
			if err != nil {
				broken++
				fmt.Fprintf(cmd.ErrOrStderr(),
					"sloprail: judge template %s of %s cannot be rendered, so that judge refuses every time it runs:\n  - %v\n",
					c.Judge, r.rule, err)
			}
		}
	}
	return broken
}
