package e2e

import (
	"strings"
	"testing"
)

// T042_07: setting rules up needs no grounding. A rule ADDED in the session (a new folder,
// a new structure) weakens nothing, so a rules-first session that cites nothing passes,
// and the judge is never asked (a failing stub cannot refuse it).
func TestT042_07_NewRuleNeedsNoGrounding(t *testing.T) {
	e := New(t)
	proj := project(t, e)
	e.InstallJudgeClaude(`{"pass": false, "reasoning": "SR042 the judge ran on an added rule"}`)
	e.Run(proj, "s-042-07", "add GET /invoices", Turns("done",
		Write("w1", ".sloprail/file-guard/invoices/file-guard.yaml", "match: \"invoices/*.md\"\nchecks:\n  - script: ./check.sh\n"),
		Write("w2", ".sloprail/file-guard/invoices/check.sh", demoScript),
		Write("w3", ".sloprail/file-guard/structure.yaml", "allow:\n  - glob: \".sloprail/**\"\n  - glob: \"src/**\"\n"),
	).ThenCommit("rules first"))
	res := e.StopNow(proj, "s-042-07", false)
	if strings.Contains(res.Output, "grounded-rule-changes") || strings.Contains(res.Output, "SR042") {
		t.Fatalf("adding rules was refused or judged:\n%s", res.Output)
	}
}

// T042_08: a NEW config.yaml is still a change that needs grounding: a `disabled:` entry
// switches a rule off whether the file is new or not.
func TestT042_08_NewConfigYamlStillNeedsGrounding(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the cited words cover the change"}`)
	// The initial commit already wrote a config.yaml (the harness disables the other
	// shipped rules there), so remove it in a base commit and let the session add it.
	e.Git(proj, "rm", "-q", ".sloprail/config.yaml")
	e.CommitAll(proj, "no config")
	// Written by a script, which no pre-write gate models: only the file-guard sees it,
	// at Stop, in the commit.
	e.Run(proj, "s-042-08", "turn off the invoices rule", Turns("done",
		Bash("w1", `mkdir -p .sloprail && python3 -c "open('.sloprail/config.yaml','w').write('disabled:\\n  - sloprail/file-guard/invoices\\n')"`),
	).ThenCommit("disable"))
	if got, out := blocked(e, proj, "s-042-08"); !got {
		t.Fatalf("a newly added config.yaml with a disabled: entry was not refused:\n%s", out)
	}
}
