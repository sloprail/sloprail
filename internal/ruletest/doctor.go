package ruletest

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/declaration"
)

// RuleReport is what testing one rule found.
type RuleReport struct {
	Rule Rule
	// Cases are the rule's cases; CaseErrors the cases that could not be read.
	Cases      []Case
	CaseErrors []error
	// Missing are the coverage a rule must have and does not: no refuse case, no
	// permit case, a judge stubbed only one way, a rule that does not load.
	Missing []string
	// Untested is true for a rule with no cases at all.
	Untested bool
	// Results are the cases' outcomes, in order.
	Results []CaseResult
}

// Failed is true when the rule has anything wrong: a case that fails or cannot be
// read, or coverage missing. An untested rule is wrong only unless allowUntested.
func (rr RuleReport) Failed(allowUntested bool) bool {
	if len(rr.Rule.Invalid) > 0 || len(rr.CaseErrors) > 0 {
		return true
	}
	if rr.Untested {
		return !allowUntested
	}
	if len(rr.Missing) > 0 {
		return true
	}
	for _, r := range rr.Results {
		if !r.Pass {
			return true
		}
	}
	return false
}

// Coverage is what a rule's cases must cover and do not: at least one refuse and
// one permit case (a context: one case that leaves it active and one that leaves it
// inactive), and each of its judges stubbed both to pass and to fail. A rule that
// refuses nothing in any case, or permits nothing, is a rule nobody has seen fire,
// or one that has never been seen to let a legitimate action through.
func Coverage(r Rule, cases []Case) []string {
	var missing []string
	if r.Nature == declaration.NatureContext {
		active, inactive := false, false
		note := func(m map[string]string) {
			switch m[r.Name] {
			case "active":
				active = true
			case "inactive":
				inactive = true
			}
		}
		for _, c := range cases {
			note(c.Contexts)
			for _, s := range c.Trajectory {
				note(s.Contexts)
			}
		}
		if !active {
			missing = append(missing, "no case leaves the context active (assert `contexts: {"+r.Name+": active}`)")
		}
		if !inactive {
			missing = append(missing, "no case leaves the context inactive (assert `contexts: {"+r.Name+": inactive}`)")
		}
		return missing
	}
	refuse, permit := false, false
	for _, c := range cases {
		if c.Expect == ExpectRefuse {
			refuse = true
		}
		if c.Expect == ExpectPermit {
			permit = true
		}
		for _, s := range c.Trajectory {
			if s.Expect == ExpectRefuse {
				refuse = true
			}
			if s.Expect == ExpectPermit {
				permit = true
			}
		}
	}
	if !refuse {
		missing = append(missing, "no case expects the rule to refuse: a rule nobody has seen refuse may refuse nothing")
	}
	if !permit {
		missing = append(missing, "no case expects the rule to permit: a rule nobody has seen permit may refuse everything")
	}
	for _, j := range r.Judges {
		key := JudgeKey(string(r.Nature), r.Name, j)
		pass, fail := false, false
		for _, c := range cases {
			for k, st := range ExpandJudgeKeys(r, c.Judges) {
				if k != key {
					continue
				}
				if st.Pass {
					pass = true
				} else {
					fail = true
				}
			}
		}
		if !pass {
			missing = append(missing, fmt.Sprintf("judge %s is never stubbed to pass: no case shows what the rule permits when the judge approves", j))
		}
		if !fail {
			missing = append(missing, fmt.Sprintf("judge %s is never stubbed to fail: no case shows the rule refusing on the judge's verdict", j))
		}
	}
	return missing
}

// Inspect reads a rule's cases and checks their coverage, without running them.
func Inspect(r Rule) RuleReport {
	rr := RuleReport{Rule: r}
	rr.Cases, rr.CaseErrors = LoadCases(r.Dir)
	if len(rr.Cases) == 0 && len(rr.CaseErrors) == 0 {
		rr.Untested = true
		return rr
	}
	if len(rr.CaseErrors) == 0 {
		rr.Missing = Coverage(r, rr.Cases)
	}
	return rr
}

// RunRule runs every case of a rule.
func (r *Runner) RunRule(rr *RuleReport, all []Rule) {
	for _, c := range rr.Cases {
		rr.Results = append(rr.Results, r.RunCase(rr.Rule, all, c))
	}
}

// ruleDirOf names where a rule lives, relative to its tree, for messages.
func ruleDirOf(r Rule) string {
	rel, err := filepath.Rel(filepath.Dir(r.Root), r.Dir)
	if err != nil {
		return r.Dir
	}
	return strings.TrimPrefix(filepath.ToSlash(rel), "./")
}

// Where is the rule's folder relative to its project, for a report.
func (r Rule) Where() string { return ruleDirOf(r) }
