// Package ghresults renders what `sr-checks verify` concluded in the forms CI systems show
// natively: GitHub workflow-command annotations, a markdown job summary and JUnit XML. It only
// formats; deciding what passed is checkrun's job.
package ghresults

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
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

// Range is what the results were read over: the resolved shas the exact local fix names, and
// the first changed line of each file in it (from the diff), so an annotation lands inline in
// "Files changed". A file with no known line is annotated at file level.
type Range struct {
	Base, Head string
	FirstLine  map[string]int
}

// RunCommand is the exact local command that judges what has no verdict.
func (r Range) RunCommand() string {
	return fmt.Sprintf("sr-checks run --base %s --head %s", r.Base, r.Head)
}

// Fix is what to do about a red verify: the exact local command, then push and re-run.
func (r Range) Fix() string {
	return fmt.Sprintf("To fix: run `%s` locally (it judges what has no verdict and pushes the results branch sloprail/checks to origin; if the push failed, `git push origin sloprail/checks`), then re-run this job.", r.RunCommand())
}

// firstLine is the first line of a reason, trimmed and cut to n runes.
func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}

// Annotations renders one ::error per refused or not-judged file and rule, on the file's first
// changed line when known (file level otherwise; a subject that is not a file has none), at most
// MaxAnnotations, then a line saying how many were left out. The title is "<rule>: not judged
// yet" or "<rule>: <first line of the reason>". extra are findings with no outcome (a rule
// that failed to load).
func Annotations(outcomes []checkrun.CheckOutcome, extra []string, rng Range) string {
	type key struct{ rule, file string }
	seen := map[key]bool{}
	var lines []string
	for _, o := range sorted(outcomes) {
		if !bad(o) {
			continue
		}
		title, msg := o.Rule+": not judged yet", "Not judged yet: no stored verdict covers this content. Run `"+rng.RunCommand()+"`, push the checks ref and re-run the job."
		if Status(o) == Fail {
			reason := o.Reason
			if strings.TrimSpace(reason) == "" {
				reason = "refused"
			}
			title, msg = o.Rule+": "+firstLine(reason, 100), reason
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
				if n := rng.FirstLine[f]; n > 0 {
					props += fmt.Sprintf("line=%d,", n)
				}
			}
			lines = append(lines, "::error "+props+"title="+escapeProp(title)+"::"+escapeData(msg))
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

// row is one rule x subject: its worst status, the files it matched and the reasons.
type row struct {
	rule, subject string
	status        string
	files         []string
	reason        string
}

func rank(s string) int {
	switch s {
	case Fail:
		return 4
	case NotJudged:
		return 3
	case Skipped:
		return 2
	case Pass:
		return 1
	}
	return 0 // cached
}

func rows(outcomes []checkrun.CheckOutcome) []row {
	var out []row
	idx := map[[2]string]int{}
	for _, o := range sorted(outcomes) {
		k := [2]string{o.Rule, o.Subject}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, row{rule: o.Rule, subject: o.Subject, status: Status(o)})
		}
		r := &out[i]
		if st := Status(o); rank(st) > rank(r.status) || (st == r.status && r.reason == "") {
			r.status = st
			if o.Reason != "" && (st == Fail || r.reason == "") {
				r.reason = o.Reason
			}
		}
		for _, f := range files(o) {
			if f != "" && !contains(r.files, f) {
				r.files = append(r.files, f)
			}
		}
	}
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// shorten keeps a path readable in a table cell: a long one is cut to its last two segments.
func shorten(p string) string {
	if len(p) <= 40 {
		return p
	}
	parts := strings.Split(p, "/")
	if len(parts) <= 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

func fileCell(fs []string) string {
	const max = 5
	var out []string
	for i, f := range fs {
		if i == max {
			out = append(out, fmt.Sprintf("+%d more", len(fs)-max))
			break
		}
		out = append(out, text(shorten(f)))
	}
	return strings.Join(out, "<br>")
}

// Summary renders the markdown job summary: a header with the counts, the one command that
// judges everything not yet judged, one table row per rule x subject (rule | subject | files |
// status | what to do) and the full reason of every failure collapsed under it.
func Summary(outcomes []checkrun.CheckOutcome, extra []string, rng Range) string {
	rs := rows(outcomes)
	counts := map[string]int{}
	for _, r := range rs {
		counts[r.status]++
	}
	red := counts[Fail] + counts[NotJudged] + len(extra)
	var b strings.Builder
	b.WriteString("## sloprail: sr-checks verify\n\n")
	fmt.Fprintf(&b, "%d pass, %d fail, %d not judged, %d cached", counts[Pass], counts[Fail]+len(extra), counts[NotJudged], counts[Cached])
	if counts[Skipped] > 0 {
		fmt.Fprintf(&b, ", %d skipped", counts[Skipped])
	}
	b.WriteString("\n\n")
	if counts[NotJudged] > 0 {
		fmt.Fprintf(&b, "Not judged yet: run `%s` locally, push the checks ref and re-run the job.\n\n", rng.RunCommand())
	}
	if len(rs) > 0 {
		b.WriteString("| Rule | Subject | Files | Status | What to do |\n| --- | --- | --- | --- | --- |\n")
		for _, r := range rs {
			todo := ""
			switch r.status {
			case NotJudged:
				todo = "`" + rng.RunCommand() + "`"
			case Fail:
				todo = cell(firstLine(r.reason, 120))
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", cell(r.rule), cell(r.subject), fileCell(r.files), r.status, todo)
		}
		b.WriteString("\n")
	}
	for _, r := range rs {
		if r.status != Fail || r.reason == "" {
			continue
		}
		fmt.Fprintf(&b, "<details><summary>fail: %s (%s)</summary>\n\n%s\n\n</details>\n\n",
			text(r.subject), text(r.rule), fence(r.reason))
	}
	for _, e := range extra {
		fmt.Fprintf(&b, "<details><summary>fail: rule could not be loaded</summary>\n\n%s\n\n</details>\n\n", fence(e))
	}
	if red > 0 {
		b.WriteString(rng.Fix() + "\n")
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

// text makes untrusted text (a path, a rule name, a reason: all can quote a judged file) inert
// in markdown and HTML: markup characters become entities and a newline becomes a space.
func text(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "`", "&#96;", "|", "&#124;",
		"\\", "&#92;", "*", "&#42;", "_", "&#95;", "[", "&#91;", "]", "&#93;", "\r\n", " ", "\n", " ", "\r", " ").Replace(s)
}

// cell is text for a table cell.
func cell(s string) string { return text(s) }

// fence is a fenced code block around untrusted text, its fence longer than any backtick run
// the text holds, so the text cannot close it.
func fence(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	f := strings.Repeat("`", max(3, longest+1))
	return f + "text\n" + s + "\n" + f
}

// escapeData and escapeProp are GitHub's workflow-command escapes.
func escapeData(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func escapeProp(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}

// Guard wraps human-readable text in GitHub's ::stop-commands:: block so that no line of it is
// run as a workflow command (::add-mask::, ::set-env:: ...). Judge reasons can quote content an
// attacker controls, so everything that is not one of this package's own escaped lines goes
// through here. The token is fresh and crypto-random per call: text cannot know it to close
// the block early.
func Guard(text string) string {
	return GuardWith(newToken(), text)
}

// GuardWith is Guard with a given token (for tests).
func GuardWith(token, text string) string {
	text = strings.TrimSuffix(text, "\n")
	return "::stop-commands::" + token + "\n" + text + "\n::" + token + "::\n"
}

func newToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("sloprail: no randomness for the stop-commands token: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// FirstLines reads `git diff -U0` output and gives, for each file, the first changed line on
// the head side: where an annotation lands inline in "Files changed". A deletion-only hunk
// points at the line after it; a file with no hunk (a mode change, a binary) is absent.
func FirstLines(diff string) map[string]int {
	out := map[string]int{}
	file := ""
	for _, ln := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(ln, "+++ "):
			file = ""
			if p := strings.TrimPrefix(ln, "+++ "); p != "/dev/null" {
				file = strings.TrimPrefix(p, "b/")
			}
		case strings.HasPrefix(ln, "@@ ") && file != "":
			if _, done := out[file]; done {
				continue
			}
			plus := ln[strings.Index(ln, "+")+1:]
			plus = plus[:strings.IndexAny(plus+" ", ", ")]
			if n, err := strconv.Atoi(plus); err == nil {
				if n < 1 {
					n = 1
				}
				out[file] = n
			}
		}
	}
	return out
}
