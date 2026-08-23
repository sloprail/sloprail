package filemod

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- the three leaders -------------------------------------------------------

func TestScan_EveryCommentLeader(t *testing.T) {
	// The writer emits one of three leaders depending on the file's language.
	// A reader that handled two of them would drop every marker in the third's
	// languages — silently, since a file with no markers read is
	// indistinguishable from a file with none written.
	for leader, lang := range map[string]string{
		"//": "go, ts, java, rust, c",
		"#":  "python, ruby, sh, yaml",
		"--": "sql",
	} {
		t.Run(leader, func(t *testing.T) {
			got := Scan(leader + " sr:blueprint pkg.Thing\n")
			require.Len(t, got, 1, "leader %q is used by %s", leader, lang)
			assert.Equal(t, Marker{Kind: "blueprint", FQN: "pkg.Thing", Line: 1}, got[0])
		})
	}
}

func TestScan_LeaderIsIndented(t *testing.T) {
	// The writer inherits the target line's indentation, so a marker on a
	// nested declaration is indented. `^\s*` is what reads it back.
	got := Scan("func f() {\n\t\t// sr:blueprint pkg.Inner\n}\n")
	require.Len(t, got, 1)
	assert.Equal(t, 2, got[0].Line)
}

// --- absence -----------------------------------------------------------------

func TestScan_NoMarkersIsEmptyNotNil(t *testing.T) {
	// Observable, not cosmetic: `markers == nil` and `len(markers) == 0` are
	// different expressions in expr, and a rule about unmarked code is written
	// with one of them. A nil here would make that rule's meaning depend on
	// whether the scanner happened to append anything.
	got := Scan("package main\n\nfunc main() {}\n")
	assert.NotNil(t, got, "a file with no markers must scan to an EMPTY list, not nil")
	assert.Empty(t, got)

	// Prove the assertion can fail: a nil slice is Empty too, so Empty alone
	// would pass against the bug this test exists to catch.
	var nilSlice []Marker
	assert.Empty(t, nilSlice, "assert.Empty does NOT distinguish nil — NotNil above is the load-bearing half")
	assert.Nil(t, nilSlice)
}

func TestScan_EmptyTextIsEmptyNotNil(t *testing.T) {
	got := Scan("")
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

// --- several, and order ------------------------------------------------------

func TestScan_SeveralMarkersInFileOrder(t *testing.T) {
	text := strings.Join([]string{
		"package main",              // 1
		"",                          // 2
		"// sr:blueprint pkg.Alpha", // 3
		"func Alpha() {}",           // 4
		"",                          // 5
		"// sr:docs pkg.Beta",       // 6
		"func Beta() {}",            // 7
		"// sr:blueprint pkg.Gamma", // 8
	}, "\n")

	got := Scan(text)
	assert.Equal(t, []Marker{
		{Kind: "blueprint", FQN: "pkg.Alpha", Line: 3},
		{Kind: "docs", FQN: "pkg.Beta", Line: 6},
		{Kind: "blueprint", FQN: "pkg.Gamma", Line: 8},
	}, got, "in the order the lines carry them — a rule may require one precede another")
}

func TestScan_EveryKindNotOnlyKnownOnes(t *testing.T) {
	// Kinds are not drawn from a fixed set, and the scanner does not filter by
	// what any guardrail happens to bind to. If it did, `len(markers) == 0`
	// would mean something different depending on which OTHER guardrails the
	// project declared.
	got := Scan("# sr:whatever-nobody-declared some.Name\n-- sr:x y\n")
	require.Len(t, got, 2)
	assert.Equal(t, "whatever-nobody-declared", got[0].Kind)
	assert.Equal(t, "x", got[1].Kind)
}

// --- the same fqn twice ------------------------------------------------------

func TestScan_SameFQNTwiceKeepsBoth(t *testing.T) {
	// This is why the field is a list of objects and not a mapping from name to
	// position. One file may carry the same fqn twice — a guard and the branch
	// it protects, both named for the invariant they share — and a mapping
	// keyed by name silently keeps one of them.
	text := strings.Join([]string{
		"// sr:blueprint pkg.Invariant", // 1
		"if !ok { return }",             // 2
		"// sr:blueprint pkg.Invariant", // 3
	}, "\n")

	got := Scan(text)
	require.Len(t, got, 2, "both kept — the input really does repeat one fqn")
	assert.Equal(t, got[0].FQN, got[1].FQN, "and it is the SAME fqn, not two similar ones")
	assert.Equal(t, 1, got[0].Line)
	assert.Equal(t, 3, got[1].Line, "the lines are what distinguishes them")
}

func TestScan_SameFQNDifferentKindsBothKept(t *testing.T) {
	got := Scan("// sr:blueprint pkg.Thing\n// sr:docs pkg.Thing\n")
	require.Len(t, got, 2)
	assert.Equal(t, "blueprint", got[0].Kind)
	assert.Equal(t, "docs", got[1].Kind)
}

// --- string literals and fenced blocks ---------------------------------------

func TestScan_MarkerInsideStringLiteralIsKept(t *testing.T) {
	// The pinned decision: a marker inside a string literal IS returned.
	// Excluding it means knowing where a literal starts and ends, which is a
	// per-language question this scanner refuses for the same reason Marker.Line
	// refuses to report an extent. Dropping is the worse failure of the two: a
	// missed marker is a rule that does not fire on code that IS marked, which
	// reads as the rule being satisfied.
	//
	// Note the line must be ONLY the marker after its leader for this to match
	// at all — `x := "// sr:k f"` does not, because the anchors reject it. What
	// does match is a marker sitting alone on a line inside a multi-line
	// literal, which is what a heredoc or a raw string block contains.
	text := "const doc = `\n// sr:blueprint pkg.Example\n`\n"
	got := Scan(text)
	require.Len(t, got, 1, "kept — see the doc comment on Scan")
	assert.Equal(t, 2, got[0].Line)
}

func TestScan_MarkerInFencedCodeBlockIsKept(t *testing.T) {
	text := strings.Join([]string{
		"# Docs",                    // 1
		"",                          // 2
		"```go",                     // 3
		"// sr:blueprint pkg.Shown", // 4
		"```",                       // 5
	}, "\n")

	got := Scan(text)
	require.Len(t, got, 1, "kept — the scanner does not track fence state")
	assert.Equal(t, Marker{Kind: "blueprint", FQN: "pkg.Shown", Line: 4}, got[0])
}

func TestScan_MarkerAfterCodeOnSameLineIsNotAMarker(t *testing.T) {
	// The other side of the decision. The pattern is anchored at both ends, so
	// a `sr:` with code before it is not read — the text before it decides what
	// it means and this scanner cannot see that text. This is also what keeps
	// `x := "// sr:k f"` out.
	for name, line := range map[string]string{
		"trailing comment": `x := 1 // sr:blueprint pkg.Thing`,
		"inside a literal": `x := "// sr:blueprint pkg.Thing"`,
		"after a brace":    `} // sr:blueprint pkg.Thing`,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, Scan(line+"\n"))
		})
	}
}

// --- line endings ------------------------------------------------------------

func TestScan_CRLF(t *testing.T) {
	// A CRLF file must scan identically to the same file with LF endings — the
	// property, not the mechanism. Asserted as an equality against the LF scan
	// so that it holds however the \r comes to be discarded; an earlier draft
	// asserted only "no \r in the fqn", which an implementation that dropped the
	// marker entirely would also have satisfied.
	const body = "package main\n// sr:blueprint pkg.Thing\nfunc f() {}\n"
	crlf := strings.ReplaceAll(body, "\n", "\r\n")

	got := Scan(crlf)
	require.Len(t, got, 1, "the marker must not be lost to the \\r")
	assert.Equal(t, Scan(body), got, "CRLF and LF must scan the same")
	assert.Equal(t, Marker{Kind: "blueprint", FQN: "pkg.Thing", Line: 2}, got[0])

	// And nothing anywhere in the result carries one.
	for _, m := range got {
		assert.NotContains(t, m.FQN, "\r")
		assert.NotContains(t, m.Kind, "\r")
	}
}

func TestScan_CRLFOnEveryLeaderAndAtEOF(t *testing.T) {
	// The trailing \s* is what absorbs the \r, so it must do so for each leader
	// and on a final line with no newline after it.
	for _, leader := range []string{"//", "#", "--"} {
		got := Scan(leader + " sr:k f\r\n")
		require.Lenf(t, got, 1, "leader %q with CRLF", leader)
		assert.Equal(t, "f", got[0].FQN)

		// Last line, CR present, no trailing newline at all.
		got = Scan("x\r\n" + leader + " sr:k f\r")
		require.Lenf(t, got, 1, "leader %q, CR at EOF", leader)
		assert.Equal(t, "f", got[0].FQN)
		assert.Equal(t, 2, got[0].Line)
	}
}

func TestScan_CRLFDoesNotShiftLineNumbers(t *testing.T) {
	// A scanner that treated \r as its own line separator would report line 4
	// here. Constructed so the wrong answer is a DIFFERENT number, not the same
	// one by luck.
	got := Scan("a\r\nb\r\n// sr:k f\r\n")
	require.Len(t, got, 1)
	assert.Equal(t, 3, got[0].Line)
}

func TestScan_LastLineNoTrailingNewline(t *testing.T) {
	got := Scan("package main\n// sr:blueprint pkg.Last")
	require.Len(t, got, 1, "a marker on the final line of a file that does not end in a newline")
	assert.Equal(t, 2, got[0].Line)
}

func TestScan_TrailingNewlineDoesNotAddAMarker(t *testing.T) {
	withNL := Scan("// sr:k f\n")
	withoutNL := Scan("// sr:k f")
	assert.Equal(t, withoutNL, withNL, "the empty string a final newline produces matches nothing")
	assert.Len(t, withNL, 1)
}

// --- malformed ---------------------------------------------------------------

func TestScan_MalformedSRLinesAreNotMarkers(t *testing.T) {
	// Lines that contain `sr:` and look marker-ish but do not satisfy the
	// reader. Each is rejected for a named reason; none of them should produce a
	// half-filled Marker.
	for name, line := range map[string]string{
		"no fqn":                  `// sr:blueprint`,
		"no fqn, trailing space":  `// sr:blueprint   `,
		"fqn has a space":         `// sr:blueprint pkg.Thing and more`,
		"no kind":                 `// sr: pkg.Thing`,
		"no space after kind":     `// sr:blueprintpkg.Thing`, // reads as one token: kind with no fqn
		"unsupported leader ;":    `; sr:blueprint pkg.Thing`,
		"unsupported leader %":    `% sr:blueprint pkg.Thing`,
		"unsupported leader <!--": `<!-- sr:blueprint pkg.Thing -->`,
		"no leader":               `sr:blueprint pkg.Thing`,
		"wrong namespace":         `// srx:blueprint pkg.Thing`,
		"uppercase namespace":     `// SR:blueprint pkg.Thing`,
		"single slash":            `/ sr:blueprint pkg.Thing`,
		"single dash":             `- sr:blueprint pkg.Thing`,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, Scan(line+"\n"), "%q must not read as a marker", line)
		})
	}
}

func TestScan_MalformedLineDoesNotBreakTheRest(t *testing.T) {
	// A rejected line must not consume the good marker after it, and must not
	// shift its line number.
	text := "// sr:blueprint\n// sr:blueprint pkg.Good\n"
	got := Scan(text)
	require.Len(t, got, 1)
	assert.Equal(t, Marker{Kind: "blueprint", FQN: "pkg.Good", Line: 2}, got[0])
}

func TestScan_ExtraWhitespaceIsTolerated(t *testing.T) {
	// The writer emits exactly one space, but the reader's `\s*`/`\s+` accept
	// more — a marker a person reformatted must still read.
	got := Scan("  //   sr:blueprint    pkg.Thing   \n")
	require.Len(t, got, 1)
	assert.Equal(t, Marker{Kind: "blueprint", FQN: "pkg.Thing", Line: 1}, got[0])
}

func TestScan_LeaderWithNoSpaceBeforeSR(t *testing.T) {
	// `\s*` after the leader means zero spaces is fine.
	got := Scan("//sr:blueprint pkg.Thing\n")
	require.Len(t, got, 1)
	assert.Equal(t, "pkg.Thing", got[0].FQN)
}

// --- the quoted fqn ----------------------------------------------------------

func TestScan_QuotedFQNKeepsTheWholePhrase(t *testing.T) {
	// The reason the quoted form exists. A marker's name is not always a token:
	// `sr:asked` records a phrase a person actually said, which a later check
	// grounds against the trajectory. A bare `(\S+)` truncates it at the first
	// space; the quoted form must carry it whole, with the surrounding quotes
	// stripped and every inner space preserved.
	got := Scan(`# sr:asked "keep the original transcript_path, just add the new section"` + "\n")
	require.Len(t, got, 1)
	assert.Equal(t, Marker{
		Kind: "asked",
		FQN:  "keep the original transcript_path, just add the new section",
		Line: 1,
	}, got[0], "the whole quoted phrase is the fqn, quotes removed, spaces kept")
}

func TestScan_QuotedFQNVariations(t *testing.T) {
	// The quoted inner text is taken verbatim between the quotes — spaces,
	// punctuation, and any character that is not itself a `"`.
	for name, tc := range map[string]struct{ line, fqn string }{
		"one word quoted":         {`// sr:k "word"`, "word"},
		"several words":           {`// sr:k "two words here"`, "two words here"},
		"punctuation":             {`// sr:asked "delete lines 3-7, keep the header!"`, "delete lines 3-7, keep the header!"},
		"colons and slashes":      {`// sr:k "see src/a/b.go: the top half"`, "see src/a/b.go: the top half"},
		"a sentence with a url":   {`// sr:k "per https://x.dev/a?b=c, drop the retry"`, "per https://x.dev/a?b=c, drop the retry"},
		"unicode inside":          {`// sr:k "залиш оригінальний текст"`, "залиш оригінальний текст"},
		"leading/trailing spaces": {`// sr:k "  padded  "`, "  padded  "},
		"a single quote inside":   {`// sr:k "don't rewrite it"`, "don't rewrite it"},
		"tab character inside":    {"// sr:k \"a\tb\"", "a\tb"},
	} {
		t.Run(name, func(t *testing.T) {
			got := Scan(tc.line + "\n")
			require.Len(t, got, 1)
			assert.Equal(t, tc.fqn, got[0].FQN)
		})
	}
}

func TestScan_QuotedFQNOnEveryLeader(t *testing.T) {
	// The quoted form is not special to `#`. It rides on all three leaders, so a
	// quoted name written in a `//` or `--` comment reads back the same.
	for _, leader := range []string{"//", "#", "--"} {
		got := Scan(leader + ` sr:asked "keep it as is"` + "\n")
		require.Lenf(t, got, 1, "leader %q with a quoted fqn", leader)
		assert.Equal(t, "asked", got[0].Kind)
		assert.Equal(t, "keep it as is", got[0].FQN)
	}
}

func TestScan_QuotedFQNCoexistsWithBare(t *testing.T) {
	// Both forms in one file, read in line order. The bare form is unchanged by
	// the quoted form's addition.
	text := strings.Join([]string{
		`// sr:blueprint pkg.Thing`,          // 1 — bare
		`# sr:asked "just append a section"`, // 2 — quoted
		`-- sr:docs schema.users`,            // 3 — bare
	}, "\n")

	got := Scan(text)
	assert.Equal(t, []Marker{
		{Kind: "blueprint", FQN: "pkg.Thing", Line: 1},
		{Kind: "asked", FQN: "just append a section", Line: 2},
		{Kind: "docs", FQN: "schema.users", Line: 3},
	}, got)
}

func TestScan_QuotedExtraWhitespaceIsTolerated(t *testing.T) {
	// As with the bare form, a person may have reformatted the line. The spaces
	// AROUND the quoted string are `\s*`/`\s+`; the spaces INSIDE it are the
	// fqn and are kept exactly.
	got := Scan(`   #    sr:asked    "the inner   spacing   is kept"   ` + "\n")
	require.Len(t, got, 1)
	assert.Equal(t, "asked", got[0].Kind)
	assert.Equal(t, "the inner   spacing   is kept", got[0].FQN)
}

func TestScan_QuotedFQNWithTrailingCR(t *testing.T) {
	// CRLF: the trailing `\r` after the closing quote is absorbed by `\s*$`,
	// exactly as it is for a bare fqn. The quote must still close before it.
	got := Scan("# sr:asked \"keep the header\"\r\n")
	require.Len(t, got, 1)
	assert.Equal(t, "keep the header", got[0].FQN)
	assert.NotContains(t, got[0].FQN, "\r")
}

func TestScan_EmptyQuotedFQNIsAMarkerWithEmptyFQN(t *testing.T) {
	// `""` is degenerate but read, not rejected — the same report-not-reject
	// stance every other odd fqn gets. A check that receives an empty quote
	// grounds it to nothing and refuses on its own terms; a marker silently
	// dropped here would read as unmarked code instead. This case is also why
	// Scan disambiguates by submatch index: the string form cannot tell an
	// empty quoted match from a bare branch that did not participate.
	got := Scan(`# sr:asked ""` + "\n")
	require.Len(t, got, 1, "an empty quoted fqn is still a marker")
	assert.Equal(t, "asked", got[0].Kind)
	assert.Equal(t, "", got[0].FQN, "the fqn is the empty string, not the two quote characters")
}

func TestScan_MalformedQuotesAreNotMarkers(t *testing.T) {
	// A line whose fqn STARTS with `"` is committed to the quoted form: it must
	// close and be followed only by whitespace. None of these do, so each is a
	// malformed line the anchors reject — not a bare fqn that silently begins
	// with a quote character, and not a half-filled Marker.
	for name, line := range map[string]string{
		"unterminated":                 `// sr:asked "keep the header`,
		"unterminated, one word":       `// sr:k "word`,
		"opens, space, never closes":   `// sr:asked "keep this and that`,
		"junk after the close":         `// sr:asked "keep it" and more`,
		"a bare token after close":     `// sr:asked "keep it" trailing`,
		"escaped quote is not nesting": `// sr:k "he said \"hi\""`, // no escaping: the \" closes early, the rest trails
		"only an opening quote":        `// sr:k "`,
		"three quotes":                 `// sr:k """`, // opens, closes empty, a stray " trails
	} {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, Scan(line+"\n"), "%q must not read as a marker", line)
		})
	}
}

func TestScan_QuoteNotAtStartOfBareFQNStaysInIt(t *testing.T) {
	// The quoted form is entered only when the fqn's FIRST character is `"`. A
	// `"` anywhere else is an ordinary character, so a bare token that happens
	// to contain one is unchanged from the original reader — it is not suddenly
	// interpreted as a broken quoted fqn.
	for name, tc := range map[string]struct{ line, fqn string }{
		"quote in the middle": {`// sr:k a"b`, `a"b`},
		"quote at the end":    {`// sr:k ab"`, `ab"`},
	} {
		t.Run(name, func(t *testing.T) {
			got := Scan(tc.line + "\n")
			require.Len(t, got, 1)
			assert.Equal(t, tc.fqn, got[0].FQN)
		})
	}
}

func TestScan_QuotedFQNMayContainTheCommentLeaders(t *testing.T) {
	// The inner text is arbitrary (bar a `"`), so a quoted phrase that itself
	// contains `//`, `#`, or `--` is kept whole — the leaders are only special
	// at the START of the line, not inside a quoted fqn.
	got := Scan(`// sr:asked "keep the // and the -- and the # in place"` + "\n")
	require.Len(t, got, 1)
	assert.Equal(t, "keep the // and the -- and the # in place", got[0].FQN)
}

// --- frontmatter -------------------------------------------------------------

func TestScan_MarkerInMarkdownFrontmatter(t *testing.T) {
	// The concrete case from examples/no-unasked-deletion. A markdown file
	// carries the marker as a YAML comment between the `---` fences. A YAML
	// comment parses to nothing (comment-only frontmatter is a valid, EMPTY
	// document), so the marker MUST be read out of the raw text — which the
	// per-line `#` scan does without needing to know what frontmatter is.
	text := strings.Join([]string{
		`---`, // 1
		`# sr:asked "keep the original transcript_path, just add the new section"`, // 2
		`---`,        // 3
		`# Some doc`, // 4
	}, "\n")

	got := Scan(text)
	require.Len(t, got, 1, "the frontmatter marker is found between the --- fences")
	assert.Equal(t, Marker{
		Kind: "asked",
		FQN:  "keep the original transcript_path, just add the new section",
		Line: 2,
	}, got[0])
}

func TestScan_FrontmatterFenceLinesAreNotMarkers(t *testing.T) {
	// The `---` fences themselves must not read as markers. `---` is not a
	// supported leader (`--` is, but only when followed by `\s* sr:`), and a
	// bare `---` has no `sr:` at all.
	got := Scan("---\n# not a marker, just a yaml comment\n---\n")
	assert.Empty(t, got, "neither the fences nor an ordinary comment is a marker")
}

func TestScan_FrontmatterWithRealFieldsAndAMarker(t *testing.T) {
	// The marker rides alongside real YAML fields too, not only in comment-only
	// frontmatter. The scan does not parse the YAML — it reads every line — so a
	// `# sr:` comment among actual keys is found, and the keys are ignored
	// because they carry no `sr:` leader-comment.
	text := strings.Join([]string{
		`---`,                              // 1
		`title: My Doc`,                    // 2
		`# sr:asked "only touch the body"`, // 3
		`tags: [a, b]`,                     // 4
		`---`,                              // 5
		``,                                 // 6
		`# Heading`,                        // 7 — a markdown H1, NOT a marker (no sr:)
	}, "\n")

	got := Scan(text)
	require.Len(t, got, 1, "only the sr: comment is a marker; the fields and the H1 are not")
	assert.Equal(t, Marker{Kind: "asked", FQN: "only touch the body", Line: 3}, got[0])
}

func TestScan_MarkdownHeadingIsNotAMarker(t *testing.T) {
	// A markdown `#` heading shares the `#` leader but is not `\s* sr:` after
	// it, so it is correctly ignored. Worth pinning because markdown is exactly
	// where the `#`-leader marker lives, and an over-eager reader that matched
	// any `#` line would swallow every heading in the file.
	for name, line := range map[string]string{
		"h1":               `# Introduction`,
		"h2":               `## Details`,
		"h1 mentioning sr": `# About sr: markers`, // the word "sr:" mid-heading, not a leader-comment
		"setext-ish":       `# sr is a tool`,      // "sr" without the colon
	} {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, Scan(line+"\n"), "%q is a heading, not a marker", line)
		})
	}
}

// --- the wire form -----------------------------------------------------------

func TestMarkerFields_LineIsAGoInt(t *testing.T) {
	// The declaration says module.TypeInt, which matcherenv maps to expr's
	// types.Int — TypeOf(0), a Go `int`. Any other width here would type-check
	// against something the vm is not holding, and `.line > 10` would fail at
	// run time after passing at load.
	fields := markerFields([]Marker{{Kind: "k", FQN: "f", Line: 7}})
	require.Len(t, fields, 1)

	entry, ok := fields[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "k", entry[KeyMarkerKind])
	assert.Equal(t, "f", entry[KeyMarkerFQN])

	line, ok := entry[KeyMarkerLine].(int)
	require.True(t, ok, "line must be a Go int, got %T", entry[KeyMarkerLine])
	assert.Equal(t, 7, line)
}

func TestMarkerFields_EmptyIsNonNil(t *testing.T) {
	assert.NotNil(t, markerFields(nil))
	assert.Empty(t, markerFields(nil))
}

// --- what the reader does at the edges, pinned rather than assumed -----------

func TestScan_FQNIsWhateverTheLineHoldsBetweenTheSpaces(t *testing.T) {
	// The spec constrains an fqn to "what a URL admits". The READER does not
	// enforce that, and should not: it reports what the file says, and a
	// scanner that dropped a marker whose fqn it disapproved of would be a
	// marker silently missing — which reads as unmarked code.
	for name, tc := range map[string]struct{ line, fqn string }{
		"dotted name": {"// sr:k pkg.Thing", "pkg.Thing"},
		"a url":       {"// sr:k https://x.dev/a?b=c", "https://x.dev/a?b=c"},
		"a path":      {"// sr:k src/a/b.go", "src/a/b.go"},
		"colons":      {"// sr:k a:b:c", "a:b:c"},
		"unicode":     {"// sr:k пакет.Річ", "пакет.Річ"},
	} {
		t.Run(name, func(t *testing.T) {
			got := Scan(tc.line + "\n")
			require.Len(t, got, 1)
			assert.Equal(t, tc.fqn, got[0].FQN)
		})
	}
}

func TestScan_SeparatorIsGoRegexpWhitespaceAndNothingElse(t *testing.T) {
	// `\S+` is RE2's, and Go's `\s` is [\t\n\f\r ] — it does NOT include the
	// vertical tab, and certainly not a NUL or a non-breaking space. So a
	// character that LOOKS like a separator can land inside the fqn instead of
	// ending it. This is not a claim that it cannot happen; it is the record of
	// what actually does, matching the writer's own pattern byte for byte.
	t.Run("vertical tab stays inside the fqn", func(t *testing.T) {
		got := Scan("// sr:k f\vg\n")
		require.Len(t, got, 1)
		assert.Equal(t, "f\vg", got[0].FQN, "\\v is not \\s in Go's regexp")
	})
	t.Run("nul stays inside the fqn", func(t *testing.T) {
		got := Scan("// sr:k f\x00g\n")
		require.Len(t, got, 1)
		assert.Equal(t, "f\x00g", got[0].FQN)
	})
	t.Run("non-breaking space stays inside the fqn", func(t *testing.T) {
		// The first draft of this test claimed this line would be REJECTED. It
		// is not: U+00A0 is not \s to RE2, so it never ends the fqn, and the line
		// reads as ONE marker whose fqn contains it. Recorded in the form that
		// caught the wrong claim, since that is the one worth a test.
		got := Scan("// sr:k f\u00a0g\n")
		require.Len(t, got, 1)
		assert.Equal(t, "f\u00a0g", got[0].FQN)
	})
	t.Run("an ordinary space DOES end it, leaving a second token", func(t *testing.T) {
		// Which is why this line is rejected outright rather than truncated to
		// the first token — the `$` anchor is what refuses it.
		assert.Empty(t, Scan("// sr:k f g\n"))
	})
}

func TestScan_LeadingBOMDefeatsTheLeader(t *testing.T) {
	// A UTF-8 BOM is not \s, so it sits between ^ and the leader and the anchor
	// fails. A marker on the first line of a BOM-prefixed file is NOT read.
	// Recorded because it is a real way to lose a marker, and because the
	// writer has the same blind spot — the two agree, which is what matters.
	assert.Empty(t, Scan("\ufeff// sr:k f\n"))
	assert.Len(t, Scan("\ufeff\n// sr:k f\n"), 1, "only the BOM's own line is affected")
}

func TestScan_LoneCRIsNotALineSeparator(t *testing.T) {
	// Old-Mac line endings are not supported: only \n splits. A marker after a
	// lone \r is part of the preceding line and does not read.
	assert.Empty(t, Scan("a\r// sr:k f\r"))
}

func TestScan_MidLineCRIsNotStripped(t *testing.T) {
	// Only a TRAILING \r is removed, so a \r in the middle of a line is a
	// character like any other — and since it IS \s, it ends the fqn and leaves
	// a second token, which the anchor rejects.
	assert.Empty(t, Scan("// sr:k f\rg\n"))
}

func TestScan_LeaderMustBeTheFirstThingOnTheLine(t *testing.T) {
	for name, line := range map[string]string{
		"comment inside a comment": "// // sr:k f",
		"hash then slashes":        "# // sr:k f",
		"three dashes":             "--- sr:k f",
		"four slashes":             "//// sr:k f",
	} {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, Scan(line+"\n"),
				"%q — the leader is followed by something that is not \\s* sr:", line)
		})
	}
}

func TestScan_LineNumbersSurviveABlankFile(t *testing.T) {
	got := Scan("\n\n\n// sr:k f\n")
	require.Len(t, got, 1)
	assert.Equal(t, 4, got[0].Line)
}

func TestScan_ManyMarkersLineNumbersAreNotOffByOne(t *testing.T) {
	// Built so an off-by-one is visible at every position rather than only at
	// the first: the fqn encodes the line the marker is on.
	var text string
	want := []Marker{}
	for i := 1; i <= 50; i++ {
		if i%3 == 0 {
			text += "// sr:k line" + itoa(i) + "\n"
			want = append(want, Marker{Kind: "k", FQN: "line" + itoa(i), Line: i})
			continue
		}
		text += "filler\n"
	}
	got := Scan(text)
	require.Len(t, got, len(want))
	assert.Equal(t, want, got)
	for _, m := range got {
		assert.Equal(t, "line"+itoa(m.Line), m.FQN, "the fqn names the line it sits on")
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
