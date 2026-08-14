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
