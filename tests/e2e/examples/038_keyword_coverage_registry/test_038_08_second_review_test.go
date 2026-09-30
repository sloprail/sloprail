package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The second review of #84. Each test is a reviewer's probe, reproduced first on
// the branch it reviewed.

// emptyingWrite rewrites the scanner behind every rule's back: python is not a
// program the engine parses, so no Pre event carries it.
func emptyingWrite(content string) string {
	return `python3 -c "open('scanners/mine/scanner.yaml','w').write('` + strings.ReplaceAll(content, "\n", `\n`) + `')"`
}

// T038_34: a scanner cannot be retired without the user's words. Emptied by a
// write the engine cannot parse, the scanner's delete compared against the file
// alone dropped nothing, needed no citation, skipped the judge — and the last
// check retired it, so Stop passed with no search ever run.
func TestT038_34_AnEmptiedScannerIsNotRetiredUncited(t *testing.T) {
	e, proj := researchProject(t)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	const sess = "s-038-34"
	res := e.Run(proj, sess, "research guardrails", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
		Bash("b1", emptyingWrite("active: true\n")),
		Bash("b2", "rm -rf scanners/mine"),
	).ThenCommit("write the files"))
	if !res.Saw(deleteHint) {
		t.Errorf("the delete of the emptied scanner was not refused for its owed keywords:\n%s", res.Output)
	}
	if got := e.GuardrailState(proj, sess, "scanner-keywords-hold", "retired:"); len(got) != 0 {
		t.Errorf("the scanner was retired with no citation: %v", got)
	}
	if joined := stopRefusals(e, proj, sess); !strings.Contains(joined, coverageRefusal) {
		t.Errorf("Stop passed with the scanner never searched:\n%s", joined)
	}
}

// T038_35: a narrowing the engine could not see does not shrink the registry.
// At Stop the settled file of a scanner declared this session is a
// PostFileCreate — a create drops nothing — and the context set the entry to
// exactly the file's keywords, so a search for the one keyword left passed.
func TestT038_35_AnUnseenNarrowingDoesNotShrinkTheRegistry(t *testing.T) {
	e, proj := researchProject(t)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	const sess = "s-038-35"
	e.Run(proj, sess, "research guardrails", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
		Bash("b1", emptyingWrite("active: true\nkeywords:\n  - guardrail\n")),
		Bash("b2", stubbed(`gh search repos guardrail`)),
	).ThenCommit("write the files"))
	joined := stopRefusals(e, proj, sess)
	if !strings.Contains(joined, `scanners/mine: "guardrail" "llm" "agent"`) {
		t.Errorf("Stop did not hold the search to llm and agent, which the user never dropped:\n%s", joined)
	}
	if !strings.Contains(joined, "drops the declared keyword(s) agent,llm") {
		t.Errorf("the narrowing itself was not refused at Stop for want of the user's words:\n%s", joined)
	}
}

// T038_36: padding a scanner's folder past the byte budget of a recursive
// removal does not hide the removal. The whole folder used to predict nothing
// past 8 MiB; now every file is predicted, the padding and what sorts after it
// unread — and the guard, unable to read what the scanner held, asks for the
// user's words.
func TestT038_36_APaddedFolderStillPredictsTheDelete(t *testing.T) {
	e, proj := researchProjectWithScanner(t)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	pad := filepath.Join(proj, "scanners", "mine", "pad.bin")
	if err := os.WriteFile(pad, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(pad, 9<<20); err != nil {
		t.Fatal(err)
	}
	res := e.Run(proj, "s-038-36", "tidy up", Turns("done",
		Bash("b1", "rm -rf scanners/mine"),
	).ThenCommit("write the files"))
	if !res.Refused() {
		t.Errorf("rm -rf of a padded scanner folder was not refused:\n%s", res.Output)
	}
	if got := readScanner(t, proj); got != activeScanner {
		t.Errorf("the refused delete reached the file:\n%s", got)
	}
}

// T038_37: a REFUSED gh search is not coverage. The coverage gate read every
// gh tool_use, whether it ran or not, so a covering search refused before any
// scanner existed "covered" the scanner declared after it.
func TestT038_37_ARefusedSearchIsNotCoverage(t *testing.T) {
	e, proj := researchProject(t)
	const sess = "s-038-37"
	res := e.Run(proj, sess, "research guardrails", Turns("done",
		Bash("b1", stubbed(`gh search repos guardrail llm agent`)),
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
	).ThenCommit("write the files"))
	if !res.Saw(searchRefusal) {
		t.Fatalf("precondition: the search before any scanner should be refused:\n%s", res.Output)
	}
	if joined := stopRefusals(e, proj, sess); !strings.Contains(joined, coverageRefusal) {
		t.Errorf("a search that never ran counted as covering the scanner:\n%s", joined)
	}
}

// T038_38: a scanner the refusal says to "write again unchanged" registers when
// written again unchanged. A flow list, `active: yes`, and `keywords :` never
// registered, so following that hint was a loop; and a file that truly cannot
// register is named with its real cause.
func TestT038_38_FollowingTheHintRegistersTheScanner(t *testing.T) {
	for _, body := range []string{
		"active: true\nkeywords: [guardrail, llm, agent]\n",
		"active: yes\nkeywords:\n  - guardrail\n  - llm\n  - agent\n",
		"active: True\nkeywords :\n  - guardrail\n  - llm\n  - agent\n",
	} {
		t.Run(body, func(t *testing.T) {
			e, proj := researchProject(t)
			e.WriteFile(proj, "scanners/mine/scanner.yaml", body)
			e.CommitAll(proj, "scanner")
			res := e.Run(proj, "s-038-38", "research guardrails", Turns("done",
				Bash("b1", stubbed(`gh search repos guardrail llm agent`)),
			).ThenCommit("write the files"))
			if !res.Saw("scanners/mine/scanner.yaml is in the right place but was not registered") {
				t.Fatalf("precondition: the refusal should say to write the scanner again:\n%s", res.Output)
			}
			res = e.Run(proj, "s-038-38b", "research guardrails", Turns("done",
				Write("w1", "scanners/mine/scanner.yaml", body),
				Bash("b1", stubbed(`gh search repos guardrail llm agent`)),
			).ThenCommit("write the files"))
			if res.Refused() || !res.Saw("stub-gh search repos") {
				t.Errorf("writing the scanner again, as the hint says, did not let the search run:\n%s", res.Output)
			}
		})
	}

	for body, cause := range map[string]string{
		"active: false\nkeywords:\n  - guardrail\n": "is switched off",
		"active: true\nkeywords: {guardrail: 1}\n":  "declares no keyword this project can read",
	} {
		t.Run(cause, func(t *testing.T) {
			e, proj := researchProject(t)
			e.WriteFile(proj, "scanners/mine/scanner.yaml", body)
			e.CommitAll(proj, "scanner")
			res := e.Run(proj, "s-038-38c", "research guardrails", Turns("done",
				Bash("b1", stubbed(`gh search repos guardrail`)),
			).ThenCommit("write the files"))
			if !res.Saw("scanners/mine/scanner.yaml "+cause) || res.Saw("unchanged is fine") {
				t.Errorf("the refusal does not name why the scanner cannot register (%s):\n%s", cause, res.Output)
			}
		})
	}
}

// T038_39: `git rm -r` of a declared scanner's folder is a delete like `rm -rf`
// — the engine did not model git rm, so it removed the scanner unseen.
func TestT038_39_GitRmOfTheScannerFolderRefused(t *testing.T) {
	e, proj := keywordsProject(t)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	res := e.Run(proj, "s-038-39", "research guardrails", Turns("done",
		Bash("b1", "git rm -r -q scanners/mine"),
	).ThenCommit("write the files"))
	if !res.Refused() || !res.Saw(deleteHint) {
		t.Fatalf("git rm -r of the scanner's folder was not refused with the hint:\n%s", res.Output)
	}
	if got := readScanner(t, proj); got != activeScanner {
		t.Errorf("the refused delete reached the file:\n%s", got)
	}
}

// T038_40: a declaration whose stamp cannot be recorded does not enter. The
// stamp is what an admitted delete or drop is tied to; entering without it
// activated the context on a registry that did not hold the declaration.
func TestT038_40_AnUnrecordedDeclarationDoesNotEnter(t *testing.T) {
	e, proj := researchProject(t)
	e.WrapBinary("sr-session", `if [ "$1" = "state" ] && [ "$2" = "set" ]; then case "$3" in stamp:*) echo "state set: disk full" >&2; exit 1;; esac; fi`)
	const sess = "s-038-40"
	e.Run(proj, sess, "declare a scanner", Turns("done",
		Write("w1", "scanners/mine/scanner.yaml", activeScanner),
	).ThenCommit("write the files"))
	if active, _ := e.ContextState(proj, sess, "scanner-declared"); active {
		t.Errorf("the context entered although the declaration's stamp could not be recorded")
	}
}

// T038_41: the one keyword parser, run directly on the shapes YAML allows a
// scanner's list to take — every rule reads a scanner through it.
func TestT038_41_TheKeywordParserReadsEveryListShape(t *testing.T) {
	lib := exampleFile(t, ".sloprail/context/scanner-declared/scanner-lib.sh")
	for _, tc := range []struct {
		name, body, want string
	}{
		{"block list", "keywords:\n  - guardrail\n  - llm\n", "guardrail|llm"},
		{"column-0 comment", "keywords:\n  - guardrail\n# more\n  - agent\n", "guardrail|agent"},
		{"column-0 items", "keywords:\n- guardrail\n- agent\nactive: true\n", "guardrail|agent"},
		{"quotes and inline comment", "keywords:\n  - \"auth token\"\n  - 'leak'\n  - agent   # the agent\n", "auth token|leak|agent"},
		{"doubled single quote", "keywords:\n  - 'it''s'\n", "it's"},
		{"plain continuation", "keywords:\n  - auth\n    token\n  - leak\n", "auth token|leak"},
		{"folded block scalar", "keywords:\n  - >\n    auth\n    token\n  - leak\n", "auth token|leak"},
		{"literal block scalar", "keywords:\n  - |-\n    leak\n", "leak"},
		{"flow list", "keywords: [guardrail, \"auth token\", 'a, b']\n", "guardrail|auth token|a, b"},
		{"flow list over lines", "keywords: [guardrail,\n  llm]\nactive: true\n", "guardrail|llm"},
		{"space before the colon", "keywords :\n  - guardrail\n", "guardrail"},
		{"CRLF", "keywords:\r\n  - guardrail\r\n", "guardrail"},
		{"a mapping is not a list", "keywords: {guardrail: 1}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", `. "$1"; scanner_keywords "$2" | paste -sd '|' -`, "_", lib, tc.body)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("parser failed: %v", err)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Errorf("scanner_keywords(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
	for body, want := range map[string]string{
		"active: true\n": "true", "active: yes\n": "true", "active: True # on\n": "true",
		"active : \"on\"\n": "true", "active: false\n": "false", "keywords: []\n": "",
	} {
		out, err := exec.Command("bash", "-c", `. "$1"; scanner_active "$2"`, "_", lib, body).Output()
		if err != nil {
			t.Fatalf("scanner_active failed: %v", err)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("scanner_active(%q) = %q, want %q", body, got, want)
		}
	}
}

// T038_42: the last check records a retirement only on a resolved citation of
// the user's words — it does not infer "asked for" from having been reached,
// since a delete that drops nothing needs no citation to reach it. Run directly,
// against a stub registry that records every write.
func TestT038_42_NothingIsRecordedWithoutTheUsersWords(t *testing.T) {
	script := exampleFile(t, ".sloprail/gate/scanner-keywords-hold/record-admitted.sh")
	for _, tc := range []struct {
		name      string
		citations string
		want      bool
	}{
		{"no citation", `[]`, false},
		{"a citation of a tool result, not the user", `[{"quote":"q","sourceTypes":["tool_result"],"path":"t","line":3}]`, false},
		{"the user's words", `[{"quote":"remove it","sourceTypes":["user"],"path":"t","line":1}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "writes")
			stub := "#!/bin/sh\n" +
				`if [ "$1 $2" = "state list" ]; then echo '{"key":"stamp:scanners/mine","value":"S1"}'; exit 0; fi` + "\n" +
				`if [ "$1 $2" = "state set" ]; then echo "$3=$4" >> ` + log + "; exit 0; fi\nexit 1\n"
			if err := os.WriteFile(filepath.Join(dir, "sr-session"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", script)
			cmd.Env = append(os.Environ(),
				"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"SR_GUARDRAIL_DIR="+filepath.Dir(script))
			cmd.Stdin = strings.NewReader(`{"event":{"kind":"PreFileDelete","path":"scanners/mine/scanner.yaml","oldContent":"","oldContentKnown":true,"citations":` + tc.citations + `}}`)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the check refused: %v\n%s", err, out)
			}
			b, _ := os.ReadFile(log)
			if got := strings.Contains(string(b), "retired:scanners/mine=S1"); got != tc.want {
				t.Errorf("retired recorded = %v, want %v (writes: %q)", got, tc.want, b)
			}
		})
	}
}

// T038_43: a drop the user asked for narrows what is owed. The registry itself
// only ever grows, so without the admitted narrowing the dropped keyword stayed
// owed and a search covering what the scanner still declares was refused.
func TestT038_43_ACitedDropNarrowsTheObligation(t *testing.T) {
	e, proj := researchProjectWithScanner(t)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the user asked to drop agent"}`)
	const sess = "s-038-43"
	const ask = "drop the agent keyword from the scanner"
	res := e.Run(proj, sess, ask, Turns("done",
		srWriteScanner("b1", narrowedScanner, ask),
		Bash("b2", stubbed(`gh search repos guardrail llm`)),
	).ThenCommit("write the files"))
	if got := readScanner(t, proj); strings.Contains(got, "agent") {
		t.Fatalf("precondition: the cited drop should have landed:\n%s\n%s", got, res.Output)
	}
	if joined := stopRefusals(e, proj, sess); strings.Contains(joined, coverageRefusal) {
		t.Errorf("the keyword the user asked to drop is still owed a search:\n%s", joined)
	}
}

// T038_44: a line where gh is only TEXT beside a command that runs code is
// refused before a scanner exists — once code runs, text and command cannot be
// told apart — and the refusal says how to get past that honestly: run the
// code-running command in a separate call.
func TestT038_44_MixedLinesSayToRunTheCodeSeparately(t *testing.T) {
	for _, command := range []string{
		`git commit --allow-empty -m "fix gh auth" && ./gradlew test`,
		`python3 -c "print(1)" && echo "gh ok"`,
		`gh pr create --title "Update gh workflow" --body b && ./scripts/check.sh`,
		`git log | awk '{print $1}'; echo "gh done"`,
	} {
		t.Run(command, func(t *testing.T) {
			e, proj := researchProject(t)
			res := e.Run(proj, "s-038-44", "tidy up", Turns("done", Bash("b1", stubbed(command))))
			if !res.Refused() || !res.Saw("run the code-running command in a separate call") {
				t.Errorf("a mixed line was not refused with the remedy:\n%s", res.Output)
			}
		})
	}
}
