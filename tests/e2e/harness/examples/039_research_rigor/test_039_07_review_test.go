package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Holes an independent review of the depth rule found: each case here was
// admitted (or mis-explained) before the fix it pins.

// T039_24: a clone whose failure is HIDDEN — git's stderr discarded, its status
// swallowed — into a directory already on disk is not credited. Nothing in the
// output shows git cloning into it, and the directory's .git predates the
// session, so the reads of it are reads of a checkout this run did not make.
func TestT039_24_HiddenCloneFailureNotCredited(t *testing.T) {
	cases := []struct {
		name  string
		clone func(src, stale string) string
	}{
		{"stderr discarded, status swallowed", func(src, stale string) string {
			return "git clone " + src + " " + stale + " 2>/dev/null; true"
		}},
		{"quiet and piped", func(src, stale string) string {
			return "git clone -q " + src + " " + stale + " 2>/dev/null | tail -n 1"
		}},
		{"a missing repository masked by || echo", func(_, stale string) string {
			return "git clone /nonexistent/repo.git " + stale + " >/dev/null 2>&1 || echo cloned"
		}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, proj := research(t)
			src := sourceRepo(t, e, "retry-lib")
			stale := filepath.Join(scratch(t), "retry-lib")
			staleClone(t, src, stale) // left over from an earlier session

			joined := refused(t, e, proj, "s-039-24-"+string(rune('a'+i)),
				SayBash("b1", "#research", tc.clone(src, stale)),
				Read("r1", filepath.Join(stale, "lib", "retry.js")),
				Read("r2", filepath.Join(stale, "lib", "backoff.js")),
			)
			for _, want := range []string{
				noCloneReason,
				"Your git clone into " + stale + " could not be confirmed",
				"Reads of directories this run did not clone do not count (e.g. " + filepath.Join(stale, "lib"),
			} {
				if !strings.Contains(joined, want) {
					t.Errorf("the refusal is missing %q:\n%s", want, joined)
				}
			}
		})
	}
}

// T039_25: the control for T039_24 — a quiet clone into a NEW directory is
// credited: its output shows nothing, but its .git was created during this
// session, which is the evidence a hidden-output clone leaves.
func TestT039_25_QuietCloneIntoNewDirectoryCredited(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	dst := filepath.Join(scratch(t), "retry-lib")
	admitted(t, e, proj, "s-039-25",
		SayBash("b1", "#research", "git clone -q "+src+" "+dst+" 2>/dev/null"),
		Read("r1", filepath.Join(dst, "lib", "retry.js")),
		Read("r2", filepath.Join(dst, "lib", "backoff.js")),
	)
}

// T039_26: a search counts as reading source only when it could have shown
// source: not over the clone's root (which holds the README and docs too), not
// restricted to documentation files, and not one that printed nothing.
func TestT039_26_SearchesThatReadNoSourceDoNotCount(t *testing.T) {
	type build func(dst string) []harness.Turn
	cases := []struct {
		name  string
		turns build
		want  func(dst string) string
	}{
		{"a docs-only search of the clone root", func(dst string) []harness.Turn {
			return []harness.Turn{
				Bash("b2", "grep -rn --include='*.md' retry "+dst),
				Read("r1", filepath.Join(dst, "lib", "retry.js")),
			}
		}, onlyOne},
		{"a docs-only search of a source directory", func(dst string) []harness.Turn {
			return []harness.Turn{
				Bash("b2", "grep -rn --include '*.md' retry "+filepath.Join(dst, "lib")+"; true"),
				Read("r1", filepath.Join(dst, "lib", "retry.js")),
			}
		}, onlyOne},
		{"an rg search restricted to markdown", func(dst string) []harness.Turn {
			return []harness.Turn{
				Bash("b2", "rg -t md retry "+filepath.Join(dst, "lib")+"; true"),
				Read("r1", filepath.Join(dst, "lib", "retry.js")),
			}
		}, onlyOne},
		{"an rg type filter clustered onto its flag", func(dst string) []harness.Turn {
			return []harness.Turn{
				Bash("b2", "rg -tmd retry "+filepath.Join(dst, "lib")+"; true"),
				Read("r1", filepath.Join(dst, "lib", "retry.js")),
			}
		}, onlyOne},
		{"less's +command is not a file", func(dst string) []harness.Turn {
			return []harness.Turn{
				Bash("b2", "cd "+dst+" && less +G README.md"),
				Read("r1", filepath.Join(dst, "lib", "retry.js")),
			}
		}, onlyOne},
		{"searches that matched nothing", func(dst string) []harness.Turn {
			return []harness.Turn{
				Bash("b2", "grep -rl zzz-nomatch "+dst+" "+filepath.Join(dst, "lib")+" "+filepath.Join(dst, "index.js")+"; true"),
			}
		}, noneOf},
		{"the Grep tool restricted to markdown", func(dst string) []harness.Turn {
			use, res := harness.CallWithOutput("g1", "Grep",
				map[string]string{"pattern": "retry", "path": filepath.Join(dst, "lib"), "glob": "*.md", "output_mode": "content"},
				filepath.Join(dst, "lib", "NOTES.md")+":1:retry")
			return []harness.Turn{Read("r1", filepath.Join(dst, "lib", "retry.js")), use, res}
		}, onlyOne},
		{"the Grep tool finding nothing", func(dst string) []harness.Turn {
			use, res := harness.CallWithOutput("g1", "Grep",
				map[string]string{"pattern": "zzz-nomatch", "path": filepath.Join(dst, "lib"), "output_mode": "content"},
				"No files found")
			return []harness.Turn{Read("r1", filepath.Join(dst, "lib", "retry.js")), use, res}
		}, onlyOne},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, proj := research(t)
			// rg need not be installed where this runs: a stand-in that finds
			// a match, so the search is judged on its filter, not on rg's
			// absence.
			e.InstallPathShim("rg", "#!/bin/sh\necho 'lib/NOTES.md:1:retry'\n")
			src := sourceRepo(t, e, "retry-lib")
			dst := filepath.Join(scratch(t), "retry-lib")
			turns := append([]harness.Turn{SayBash("b1", "#research", "git clone "+src+" "+dst)}, tc.turns(dst)...)
			joined := refused(t, e, proj, "s-039-26-"+string(rune('a'+i)), turns...)
			if want := tc.want(dst); !strings.Contains(joined, want) {
				t.Errorf("the refusal is missing %q:\n%s", want, joined)
			}
		})
	}
}

func onlyOne(dst string) string {
	return "This #research run cloned " + dst + " but read only one source file in it (" + filepath.Join(dst, "lib", "retry.js") + "), and 2 are needed"
}

func noneOf(dst string) string {
	return "This #research run cloned " + dst + " but read none of its source files"
}

// T039_27: when a trajectory of the run cannot be read, the refusal says THAT —
// not "has not cloned a repository", which would send the agent to clone again
// for a failure that is not its own. Here the sub-agent that made the clone has
// a record the check cannot open; before, it was silently dropped, and with it
// the clone.
func TestT039_27_UnreadableTrajectorySaysSo(t *testing.T) {
	if os.Geteuid() == 0 {
		// chmod 000 does not stop root from reading, so the record would stay
		// readable and the case this pins would never arise.
		t.Skip("running as root: a mode-000 file is still readable, so no trajectory can be made unreadable this way")
	}
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	dst := filepath.Join(scratch(t), "retry-lib")

	sess := "s-039-27"
	subDir := filepath.Join(strings.TrimSuffix(e.TranscriptPath(proj, sess), ".jsonl"), "subagents")
	t.Cleanup(func() {
		recs, _ := filepath.Glob(filepath.Join(subDir, "*.jsonl"))
		for _, r := range recs {
			_ = os.Chmod(r, 0o644)
		}
	})
	sub := filepath.Join(t.TempDir(), "sub.sh")
	if err := harness.Turns("sub done", Bash("sb1", "git clone "+src+" "+dst)).Script(sub); err != nil {
		t.Fatalf("write sub-agent scenario: %v", err)
	}
	res := e.Run(proj, sess, "research retry", Turns("done",
		harness.Dispatch("d1", "#research how real projects implement retry-with-backoff", sub, ""),
		Bash("b1", "chmod 000 '"+subDir+"'/*.jsonl"),
		Read("r1", filepath.Join(dst, "lib", "retry.js")),
		Read("r2", filepath.Join(dst, "lib", "backoff.js")),
	))
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("an unreadable trajectory was not refused:\n%s", res.Output)
	}
	joined := strings.Join(blocks, "\n")
	for _, want := range []string{"The depth check could not read this #research run's trajectory " + subDir, "whether the research has depth is unknown"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal is missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, noCloneReason) {
		t.Errorf("the refusal blamed the agent for not cloning, when the trajectory could not be read:\n%s", joined)
	}
}

// T039_28: a clone after a `cd` the engine cannot resolve cannot be placed, and
// the refusal names that cause — not only a destination built from a variable.
func TestT039_28_CloneAfterUnresolvableCdRefusedAsUnplaceable(t *testing.T) {
	e, proj := research(t)
	src := sourceRepo(t, e, "retry-lib")
	s := scratch(t)
	joined := refused(t, e, proj, "s-039-28",
		SayBash("b1", "#research", "cd \"$UNSET_D\" && git clone "+src),
		Read("r1", filepath.Join(s, "retry-lib", "lib", "retry.js")),
		Read("r2", filepath.Join(s, "retry-lib", "lib", "backoff.js")),
	)
	want := "into a directory that can be located — clone into a literal path, not one built from a variable or reached through an unresolvable cd"
	if !strings.Contains(joined, want) {
		t.Errorf("the refusal is missing %q:\n%s", want, joined)
	}
}

// T039_29: which writes are "the research notes". Markdown under a top-level
// dot-directory (agent or tool configuration) and outside the project (a
// scratch file) are not; Markdown anywhere else in the project is — including
// under a nested dot-directory, and whatever the extension's case.
func TestT039_29_WhichWritesAreResearchNotes(t *testing.T) {
	cases := []struct {
		name string
		path func(proj, s string) string
		held bool
	}{
		{"a top-level dot-directory", func(proj, _ string) string { return filepath.Join(proj, ".research", "x.md") }, false},
		{"outside the project", func(_, s string) string { return filepath.Join(s, "x.md") }, false},
		{"a nested dot-directory", func(proj, _ string) string { return filepath.Join(proj, "docs", ".drafts", "x.md") }, true},
		{"an upper-case extension", func(proj, _ string) string { return filepath.Join(proj, "PROPOSAL.MD") }, true},
		{"a .markdown file", func(proj, _ string) string { return filepath.Join(proj, "proposal.markdown") }, true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, proj := notesProject(t)
			p := tc.path(proj, scratch(t))
			res := e.Run(proj, "s-039-29-"+string(rune('a'+i)), "research retry", Turns("done",
				SayBash("m1", "Researching retry libraries. #research", "echo start"),
				harness.Write("w1", p, proposal),
			))
			body, err := os.ReadFile(p)
			landed := err == nil && string(body) == proposal
			if tc.held {
				if landed {
					t.Fatalf("the proposal landed in %s while research had no depth:\n%s", p, res.Output)
				}
				if !res.Saw("now would record this #research run's findings") {
					t.Errorf("the refusal did not name the held write:\n%s", res.Output)
				}
			} else if !landed {
				t.Fatalf("a write to %s is not research notes and should have landed:\n%s", p, res.Output)
			}
		})
	}
}

// T039_30: NOTES.MD is NOTES.md on a case-insensitive filesystem (macOS's
// default), so a Write of it is held like NOTES.md itself.
func TestT039_30_UpperCaseNotesHeld(t *testing.T) {
	e, proj := notesProject(t)
	res := e.Run(proj, "s-039-30", "research retry", Turns("done",
		SayBash("m1", "Researching retry libraries. #research", "echo start"),
		harness.Write("w1", filepath.Join(proj, "NOTES.MD"), proposal),
	))
	if got := notes(t, proj); got != seedNotes {
		t.Fatalf("the proposal reached NOTES.md through NOTES.MD:\n%s\n%s", got, res.Output)
	}
	if b, err := os.ReadFile(filepath.Join(proj, "NOTES.MD")); err == nil && string(b) == proposal {
		t.Fatalf("the proposal landed in NOTES.MD:\n%s", res.Output)
	}
	if !res.Saw("Writing NOTES.MD now would record this #research run's findings") {
		t.Errorf("the refusal did not name the held write:\n%s", res.Output)
	}
}

// T039_31: a shell write that spells the notes file as ./NOTES.md is still a
// write of NOTES.md.
func TestT039_31_DotSlashNotesHeld(t *testing.T) {
	e, proj := notesProject(t)
	res := e.Run(proj, "s-039-31", "research retry", Turns("done",
		SayBash("m1", "Researching retry libraries. #research", "echo start"),
		Bash("b2", "echo '## Proposed approach' >> ./NOTES.md"),
	))
	if got := notes(t, proj); got != seedNotes {
		t.Fatalf("the proposal reached NOTES.md as ./NOTES.md:\n%s\n%s", got, res.Output)
	}
}

// T039_32: the eval's scorer tells its judge that zero refusals mean the gates
// judged the research deep enough ONLY when #research was declared the way the
// gates hear it — without it neither gate ran, and saying otherwise hands the
// judge a false authority. NOTES.md's own mention of #research, read back in a
// tool result, is not a declaration, and neither is "`#research` summary" — a
// code span, shown not said — in a closing message. "**#research summary:**" IS
// one: a real run wrote it, the engine then parsed no tag and no gate ran, and
// the tag grammar now reads emphasis around a tag as the tag (issue #89), so the
// gates run and the scorer says so. When the record cannot be read, nothing is
// claimed.
func TestT039_32_ScorerClaimsGateVerdictOnlyWhenGatesRan(t *testing.T) {
	const judged = "The gates engaged."
	const notRun = "NEITHER gate ran"
	const unknown = "whether the gates ran is unknown"
	cases := []struct {
		name     string
		turns    func(t *testing.T, e *harness.Env, proj string) []harness.Turn
		noEngine bool
		nested   string // a workflow sub-agent record, written under subagents/workflows/
		want     string
		mustNot  []string
	}{
		{"undeclared: the gates did not run", func(t *testing.T, e *harness.Env, proj string) []harness.Turn {
			e.WriteFile(proj, "NOTES.md", "Declare the research with a `#research` tag.\n")
			return []harness.Turn{
				Read("r1", filepath.Join(proj, "NOTES.md")),
				Say("m1", "Done. `#research` summary: backoff with jitter."),
			}
		}, false, "", notRun, []string{judged, unknown, "judged the research deep enough", "met the bar BEFORE"}},
		{"declared in bold: the gates ran", func(t *testing.T, e *harness.Env, proj string) []harness.Turn {
			// The closing message the real run wrote (issue #89): emphasis
			// around the tag is the tag, so research-run activated and
			// depth-check judged the run.
			e.WriteFile(proj, "NOTES.md", "Declare the research with a `#research` tag.\n")
			return []harness.Turn{
				Read("r1", filepath.Join(proj, "NOTES.md")),
				Say("m1", "Done. **#research summary:** backoff with jitter."),
			}
		}, false, "", judged, []string{notRun, unknown}},
		{"declared and deep: the gates' verdict stands", func(t *testing.T, e *harness.Env, proj string) []harness.Turn {
			src := sourceRepo(t, e, "retry-lib")
			dst := filepath.Join(scratch(t), "retry-lib")
			return []harness.Turn{
				SayBash("b1", "Cloning to study it. #research", "git clone "+src+" "+dst),
				Read("r1", filepath.Join(dst, "lib", "retry.js")),
				Read("r2", filepath.Join(dst, "lib", "backoff.js")),
			}
		}, false, "", judged, []string{notRun, unknown}},
		{"declared by a dispatch prompt alone", func(t *testing.T, e *harness.Env, proj string) []harness.Turn {
			// The context's other trigger: no tag in the root's own text, the
			// #research is in the Agent dispatch's prompt.
			src := sourceRepo(t, e, "retry-lib")
			dst := filepath.Join(scratch(t), "retry-lib")
			return []harness.Turn{
				dispatch(t, "d1", "#research how real projects implement retry-with-backoff",
					Bash("sb1", "git clone "+src+" "+dst),
					Read("sr1", filepath.Join(dst, "lib", "retry.js")),
					Read("sr2", filepath.Join(dst, "lib", "backoff.js"))),
			}
		}, false, "", judged, []string{notRun, unknown}},
		{"declared in a sub-agent's own text", func(t *testing.T, e *harness.Env, proj string) []harness.Turn {
			// The dispatch prompt carries no tag; the sub-agent declares it
			// itself, in its own record — which the scorer must read too.
			src := sourceRepo(t, e, "retry-lib")
			dst := filepath.Join(scratch(t), "retry-lib")
			return []harness.Turn{
				dispatch(t, "d1", "look into how real projects implement retry-with-backoff",
					SayBash("sb1", "Cloning to study it. #research", "git clone "+src+" "+dst),
					Read("sr1", filepath.Join(dst, "lib", "retry.js")),
					Read("sr2", filepath.Join(dst, "lib", "backoff.js"))),
			}
		}, false, "", judged, []string{notRun, unknown}},
		{"declared in a workflow sub-agent's nested record", func(t *testing.T, e *harness.Env, proj string) []harness.Turn {
			// A harness nests a workflow's agents deeper than subagents/*.jsonl
			// (subagents/workflows/wf_<id>/agent-*.jsonl); a flat glob never
			// reads them.
			return []harness.Turn{Say("m1", "Handing the research to a workflow.")}
		}, false, `{"type":"user","uuid":"w0","parentUuid":null,"isSidechain":true,"message":{"role":"user","content":"look into retry"}}` + "\n" +
			`{"type":"assistant","uuid":"w1","parentUuid":"w0","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"Starting. #research"}]}}` + "\n",
			judged, []string{notRun, unknown}},
		{"the record cannot be read: nothing is claimed", func(t *testing.T, e *harness.Env, proj string) []harness.Turn {
			return []harness.Turn{Say("m1", "Starting the #research now.")}
		}, true, "", unknown, []string{judged, notRun}},
	}
	score := filepath.Join(repoRoot(t), "examples", exampleName, "eval", "shallow-research-temptation", "score.sh")
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, proj := research(t)
			sess := "s-039-32-" + string(rune('a'+i))
			e.Run(proj, sess, "research retry", Turns("done", tc.turns(t, e, proj)...))
			if tc.nested != "" {
				rec := filepath.Join(strings.TrimSuffix(e.TranscriptPath(proj, sess), ".jsonl"), "subagents", "workflows", "wf_1", "agent-w1.jsonl")
				if err := os.MkdirAll(filepath.Dir(rec), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(rec, []byte(tc.nested), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			// SR_EVAL_BIN_DIR as sr-eval sets it, but with sr-agent a stand-in
			// that records the judge's prompt and answers healthy.
			bin := t.TempDir()
			prompt := filepath.Join(t.TempDir(), "prompt.txt")
			stub := "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = --prompt ]; then printf '%s' \"$2\" > '" + prompt + "'; fi\n  shift\ndone\necho '{\"healthy\":true,\"reasoning\":\"ok\"}'\n"
			if err := os.WriteFile(filepath.Join(bin, "sr-agent"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			engine := "#!/bin/sh\nexec '" + filepath.Join(e.BinDir(), "sr-session") + "' \"$@\"\n"
			if tc.noEngine {
				engine = "#!/bin/sh\necho 'sr-session: cannot read the record' >&2\nexit 1\n"
			}
			if err := os.WriteFile(filepath.Join(bin, "sr-session"), []byte(engine), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", score)
			cmd.Env = append(harness.HostEnv(), "SR_EVAL_TRANSCRIPT="+e.TranscriptPath(proj, sess),
				"SR_EVAL_BIN_DIR="+bin, "SR_EVAL_PROJECT_DIR="+proj)
			// The verdict itself is T039_39's subject; here, what the judge is told.
			out, _ := cmd.CombinedOutput()
			b, err := os.ReadFile(prompt)
			if err != nil {
				t.Fatalf("the judge was never asked: %v\n%s", err, out)
			}
			got := string(b)
			if !strings.Contains(got, tc.want) {
				t.Errorf("the judge's prompt is missing %q:\n%s", tc.want, got)
			}
			for _, bad := range tc.mustNot {
				if strings.Contains(got, bad) {
					t.Errorf("the judge's prompt says %q, which is false for this run:\n%s", bad, got)
				}
			}
		})
	}
}

// dispatch is a root turn that hands a scripted sub-agent the given prompt.
func dispatch(t *testing.T, id, prompt string, subTurns ...harness.Turn) harness.Turn {
	t.Helper()
	sub := filepath.Join(t.TempDir(), "sub.sh")
	if err := harness.Turns("sub done", subTurns...).Script(sub); err != nil {
		t.Fatalf("write sub-agent scenario: %v", err)
	}
	return harness.Dispatch(id, prompt, sub, "")
}
