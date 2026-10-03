// Package ghresults renders what `sr-checks verify` concluded in the forms CI systems show
// natively: GitHub workflow-command annotations, a markdown job summary and JUnit XML. It only
// formats; deciding what passed is checkrun's job.
package ghresults

import (
	"encoding/xml"
	"fmt"
	"sort"
	"strings"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkrun"
)

// MaxAnnotations caps the ::error lines: GitHub shows only the first ten per step and
// drops past 50 per job, so more would be noise. A summary line says how many were left out.
const MaxAnnotations = 50

// Row statuses, as the job summary names them.
const (
	Pass      = "pass"
	Fail      = "fail"
	NotJudged = "not judged"
	Cached    = "cached"
	Skipped   = "skipped"
)

// Status is the row status of an outcome: a stored pass that was reused reads "cached".
func Status(o checkrun.CheckOutcome) string {
	switch o.Status {
	case "pass":
		if o.Source == "cached" || o.Source == "stored" {
			return Cached
		}
		return Pass
	case "missing":
		return NotJudged
	case "skipped":
		return Skipped
	default: // fail, error
		return Fail
	}
}

func bad(o checkrun.CheckOutcome) bool {
	s := Status(o)
	return s == Fail || s == NotJudged
}

// Fix is the exact local command that makes a red verify green, and what to do after it.
func Fix(base, head string) string {
	return fmt.Sprintf("To fix: run `sr-checks run --base %s --head %s` locally (it judges what has no verdict and pushes the results branch sloprail/checks to origin; if the push failed, `git push origin sloprail/checks`), then re-run this job.", base, head)
}

// Annotations renders one ::error per refused or not-judged file and rule (a subject that is
// not a file is annotated without one), at most MaxAnnotations, then a line saying how many
// were left out. extra are findings with no outcome (a rule that failed to load).
func Annotations(outcomes []checkrun.CheckOutcome, extra []string) string {
	type key struct{ rule, file string }
	seen := map[key]bool{}
	var lines []string
	for _, o := range sorted(outcomes) {
		if !bad(o) {
			continue
		}
		reason := o.Reason
		if reason == "" {
			reason = "refused"
		}
		if Status(o) == NotJudged && !strings.Contains(reason, "not judged") {
			reason = "not judged yet: " + reason
		}
		for _, f := range files(o) {
			k := key{o.Rule, f}
			if seen[k] {
				continue
			}
			seen[k] = true
			props := ""
			if f != "" {
				props = "file=" + escapeProp(f) + ","
			}
			lines = append(lines, "::error "+props+"title="+escapeProp(o.Rule)+"::"+escapeData(reason))
		}
	}
	for _, e := range extra {
		lines = append(lines, "::error title=sloprail::"+escapeData(e))
	}
	var b strings.Builder
	for i, l := range lines {
		if i == MaxAnnotations {
			fmt.Fprintf(&b, "::error title=sloprail::%d more refusals are not annotated; see the job summary\n", len(lines)-MaxAnnotations)
			break
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}

// Summary renders the markdown job summary: a table of rule x subject x status, the reasons
// of what is red collapsed under it, and (when something is red) the local fix.
func Summary(outcomes []checkrun.CheckOutcome, extra []string, fix string) string {
	var b strings.Builder
	rows := sorted(outcomes)
	red := len(extra)
	counts := map[string]int{}
	for _, o := range rows {
		counts[Status(o)]++
		if bad(o) {
			red++
		}
	}
	b.WriteString("## sloprail: sr-checks verify\n\n")
	if red == 0 {
		fmt.Fprintf(&b, "All %d checks pass.\n\n", len(rows))
	} else {
		fmt.Fprintf(&b, "**%d red** of %d checks (%d fail, %d not judged).\n\n", red, len(rows), counts[Fail]+len(extra), counts[NotJudged])
	}
	if len(rows) > 0 {
		b.WriteString("| Rule | Subject | Status |\n| --- | --- | --- |\n")
		for _, o := range rows {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", cell(o.Rule), cell(label(o)), Status(o))
		}
		b.WriteString("\n")
	}
	for _, o := range rows {
		if !bad(o) || o.Reason == "" {
			continue
		}
		fmt.Fprintf(&b, "<details><summary>%s: %s (%s)</summary>\n\n```text\n%s\n```\n\n</details>\n\n",
			Status(o), cell(label(o)), cell(o.Rule), strings.ReplaceAll(o.Reason, "```", "'''"))
	}
	for _, e := range extra {
		fmt.Fprintf(&b, "<details><summary>fail: rule could not be loaded</summary>\n\n```text\n%s\n```\n\n</details>\n\n", strings.ReplaceAll(e, "```", "'''"))
	}
	if red > 0 && fix != "" {
		b.WriteString(fix + "\n")
	}
	return b.String()
}

type suite struct {
	XMLName  xml.Name `xml:"testsuite"`
	Name     string   `xml:"name,attr"`
	Tests    int      `xml:"tests,attr"`
	Failures int      `xml:"failures,attr"`
	Skipped  int      `xml:"skipped,attr"`
	Cases    []tcase  `xml:"testcase"`
}

type tcase struct {
	Name    string    `xml:"name,attr"`
	Class   string    `xml:"classname,attr"`
	Failure *fail     `xml:"failure,omitempty"`
	Skipped *struct{} `xml:"skipped,omitempty"`
}

type fail struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

type suites struct {
	XMLName xml.Name `xml:"testsuites"`
	Suites  []suite  `xml:"testsuite"`
}

// JUnit renders the outcomes as JUnit XML: one testsuite per rule, one testcase per subject
// (a subject's several checks collapse to one case, red if any is red).
func JUnit(outcomes []checkrun.CheckOutcome) ([]byte, error) {
	var out suites
	idx := map[string]int{}
	caseIdx := map[[2]string]int{}
	for _, o := range sorted(outcomes) {
		si, ok := idx[o.Rule]
		if !ok {
			si = len(out.Suites)
			idx[o.Rule] = si
			out.Suites = append(out.Suites, suite{Name: o.Rule})
		}
		s := &out.Suites[si]
		ck := [2]string{o.Rule, o.Subject}
		ci, ok := caseIdx[ck]
		if !ok {
			ci = len(s.Cases)
			caseIdx[ck] = ci
			s.Cases = append(s.Cases, tcase{Name: o.Subject, Class: o.Rule})
		}
		c := &s.Cases[ci]
		switch {
		case bad(o) && c.Failure == nil:
			msg := Status(o)
			c.Failure = &fail{Message: msg, Text: o.Reason}
			c.Skipped = nil
		case Status(o) == Skipped && c.Failure == nil:
			c.Skipped = &struct{}{}
		}
	}
	for i := range out.Suites {
		s := &out.Suites[i]
		s.Tests = len(s.Cases)
		for _, c := range s.Cases {
			if c.Failure != nil {
				s.Failures++
			} else if c.Skipped != nil {
				s.Skipped++
			}
		}
	}
	b, err := xml.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(b, '\n')...), nil
}

// files are what an outcome's annotation points at: the files its subject covers, else the
// subject when it is a path, else nothing (one annotation without a file).
func files(o checkrun.CheckOutcome) []string {
	if len(o.Files) > 0 {
		return o.Files
	}
	if o.Subject != "" && o.Subject != changeset.DefaultSubjectID {
		return []string{o.Subject}
	}
	return []string{""}
}

// label is the subject as the summary names it: a subject covering several files lists them.
func label(o checkrun.CheckOutcome) string {
	if len(o.Files) == 0 || (len(o.Files) == 1 && o.Files[0] == o.Subject) {
		return o.Subject
	}
	return o.Subject + " (" + strings.Join(o.Files, ", ") + ")"
}

func sorted(in []checkrun.CheckOutcome) []checkrun.CheckOutcome {
	out := append([]checkrun.CheckOutcome(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rule != out[j].Rule {
			return out[i].Rule < out[j].Rule
		}
		if out[i].Subject != out[j].Subject {
			return out[i].Subject < out[j].Subject
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func cell(s string) string {
	s = strings.NewReplacer("|", "\\|", "\n", " ", "\r", "").Replace(s)
	return s
}

// escapeData and escapeProp are GitHub's workflow-command escapes.
func escapeData(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func escapeProp(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}
