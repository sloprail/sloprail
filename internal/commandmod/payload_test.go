package commandmod

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// payloadFor returns the payload of the single write target on a line, for the
// many cases that have exactly one.
//
// It asserts there IS exactly one write target, so a case that accidentally
// stops naming a file fails as a missing target rather than silently passing as
// a missing payload.
func payloadFor(t *testing.T, line string) Payload {
	t.Helper()
	var writes []FileTarget
	for _, tg := range FileTargets(line) {
		if tg.Effect == Write {
			writes = append(writes, tg)
		}
	}
	require.Len(t, writes, 1, "line %q must name exactly one written file", line)
	return writes[0].Payload
}

// --- literal output ---------------------------------------------------------

// TestPayload_EchoRendersItsOutputExactly is the newline case, and the newline
// is the whole point.
//
// `echo hello` writes "hello\n". A payload reporting "hello" would be wrong by
// one byte, which is invisible in a diff and fatal to a fingerprint: a rule
// hashing the resulting file, or asking whether it ends in a newline, gets the
// wrong answer for every echo on the line.
func TestPayload_EchoRendersItsOutputExactly(t *testing.T) {
	for line, want := range map[string]string{
		"echo hello > f.md":           "hello\n",
		"echo -n hello > f.md":        "hello",
		"echo one two three > f.md":   "one two three\n",
		"echo > f.md":                 "\n",
		"echo -n > f.md":              "",
		`echo "quoted words" > f.md`:  "quoted words\n",
		`echo 'single quoted' > f.md`: "single quoted\n",
	} {
		t.Run(line, func(t *testing.T) {
			p := payloadFor(t, line)
			assert.Equal(t, PayloadLiteral, p.Kind)
			assert.Equal(t, want, p.Text)
		})
	}
}

// TestPayload_EchoWithEscapeFlagsIsNotClaimed is the deliberate limit.
//
// `-e` enables backslash escapes, but which escapes and whether they are on by
// default differ between bash's builtin, dash's, and /bin/echo. So the
// resulting bytes depend on the interpreter rather than the line, and this
// package does not know the interpreter.
//
// Declining costs a rule that does not see the content. Guessing would produce
// bytes the file never holds, which is the failure this whole change exists to
// remove.
func TestPayload_EchoWithEscapeFlagsIsNotClaimed(t *testing.T) {
	for _, line := range []string{
		`echo -e 'a\tb' > f.md`,
		`echo -E 'a\tb' > f.md`,
		`echo -e > f.md`,
	} {
		assert.Equal(t, PayloadNone, payloadFor(t, line).Kind,
			"line %q depends on which shell runs it", line)
	}
}

// TestPayload_EchoOfANonLiteralWordIsNotClaimed holds the certainty rule at the
// content level.
//
// `echo $GREETING > f.md` names the file perfectly and says nothing about the
// bytes: resolving it means assuming the empty environment is the real one,
// which safeConfig refuses for exactly this reason. So the PATH is reported and
// the CONTENT is not — the two halves of one line, each as strict as its own
// answer needs to be.
func TestPayload_EchoOfANonLiteralWordIsNotClaimed(t *testing.T) {
	targets := FileTargets("echo $GREETING > f.md")
	require.Len(t, targets, 1, "the redirection still names the file")
	assert.Equal(t, "f.md", targets[0].Path)
	assert.Equal(t, PayloadNone, targets[0].Payload.Kind,
		"the path is certain and the content is not, and they are judged separately")
}

// TestPayload_PrintfIsRenderedOnlyWhereItIsExact draws the line deliberately.
//
// printf is a small language, and reimplementing it would mean numeric
// conversion, width and precision, and escapes that vary. The subset handled is
// the one that appears in real scripts and is unambiguous; everything else
// declines, which costs a missing content rather than a wrong one.
func TestPayload_PrintfIsRenderedOnlyWhereItIsExact(t *testing.T) {
	t.Run("rendered", func(t *testing.T) {
		for line, want := range map[string]string{
			// No conversions: the format IS the output, and printf adds no
			// trailing newline of its own. That is the difference from echo.
			"printf 'no newline' > f.md":  "no newline",
			`printf 'with\n' > f.md`:      "with\n",
			`printf 'a\tb' > f.md`:        "a\tb",
			`printf '%s' hello > f.md`:    "hello",
			`printf '%s\n' hello > f.md`:  "hello\n",
			`printf 'x: %s\n' val > f.md`: "x: val\n",
			// %d, where the argument is ALREADY a decimal integer. Rendered as
			// itself: no normalisation, which is why the narrow cases below
			// are declined rather than tidied up.
			`printf '%d' 5 > f.md`:         "5",
			`printf '%d items\n' 5 > f.md`: "5 items\n",
			`printf '%d' -5 > f.md`:        "-5",
			`printf '%d' 0 > f.md`:         "0",
			// Several conversions in one format, consumed left to right.
			`printf '%s %s' a b > f.md`:   "a b",
			`printf '%s=%d\n' k 7 > f.md`: "k=7\n",
			// Surplus arguments REUSE the format. This is printf's specified
			// loop and the reason `printf '%s\n' a b c` writes three lines,
			// which is a real spelling rather than a curiosity.
			`printf '%s\n' a b c > f.md`:      "a\nb\nc\n",
			`printf '%s=%s\n' k v x y > f.md`: "k=v\nx=y\n",
			// A pass that runs out of arguments finishes the format with the
			// missing ones empty, rather than stopping where they ran out.
			// `printf '%s=%s\n' a` writes "a=\n", not "a=".
			`printf '%s=%s\n' a > f.md`: "a=\n",
			`printf '%s\n' > f.md`:      "\n",
			// An escape beside a conversion. The verbs are read off the RAW
			// format and rendered against the ESCAPED one, so these pin that
			// the two agree about where the conversions are.
			`printf 'a\\%s' x > f.md`:    "a\\x",
			`printf 'a\tb%s\n' x > f.md`: "a\tbx\n",
			// A literal percent consumes no argument, so a format carrying
			// one alongside a conversion still takes exactly ONE argument per
			// pass — and the loop reuses the format for the rest.
			`printf '100%%\n' > f.md`:       "100%\n",
			`printf '%s%%\n' 50 > f.md`:     "50%\n",
			`printf '%s%%\n' 50 75 > f.md`:  "50%\n75%\n",
			`printf '%d%% done\n' 7 > f.md`: "7% done\n",
		} {
			t.Run(line, func(t *testing.T) {
				p := payloadFor(t, line)
				assert.Equal(t, PayloadLiteral, p.Kind)
				assert.Equal(t, want, p.Text)
			})
		}
	})

	t.Run("declined", func(t *testing.T) {
		for _, line := range []string{
			// A WIDTH or a precision. Renderable in principle; declined
			// because the padding rules are where an approximation is wrong by
			// invisible whitespace.
			`printf '%5s' x > f.md`,
			`printf '%-3d' 1 > f.md`,
			`printf '%.2f' 1.5 > f.md`,
			// Conversions whose output depends on a locale, a default
			// precision, or a character encoding.
			`printf '%f' 1.5 > f.md`,
			`printf '%x' 255 > f.md`,
			`printf '%o' 8 > f.md`,
			`printf '%c' a > f.md`,
			// Shell-dependent conversions: %b is a bash extension and %q's
			// quoting style differs between implementations.
			`printf '%b' 'a\tb' > f.md`,
			`printf '%q' x > f.md`,
			// A %d argument that is not already exactly a decimal integer.
			// Rendering these means NORMALISING — dropping a leading zero or a
			// plus — which is a judgement rather than a reading, and the
			// hex/character forms differ between implementations outright.
			`printf '%d' 007 > f.md`,
			`printf '%d' +5 > f.md`,
			`printf '%d' 0x1F > f.md`,
			`printf '%d' " 5" > f.md`,
			`printf '%d' abc > f.md`,
			`printf '%d' 1.5 > f.md`,
			// A literal format with surplus arguments. printf prints it ONCE —
			// a format consuming no arguments stops the loop — but the rule is
			// about a shape nobody writes deliberately, so it stays declined.
			`printf 'x' a > f.md`,
			// A trailing lone `%`, whose behaviour is unspecified.
			`printf 'done%' > f.md`,
			// An escape form this declines rather than approximates: whether
			// the FORMAT's octal and hex escapes are interpreted at all differs
			// between printf(1) and the shell builtin.
			`printf '\x41' > f.md`,
			`printf '\101' > f.md`,
		} {
			assert.Equal(t, PayloadNone, payloadFor(t, line).Kind,
				"line %q is not exactly renderable", line)
		}
	})
}

// TestPayload_PrintfAlwaysTerminates is a property rather than a case, and it
// exists because the format-reuse loop was found to HANG rather than fail.
//
// printf repeats its format until the arguments run out, so the loop's exit
// condition is the thing that has to be right. Written as "stop when the
// arguments are gone" it spins forever on any format whose pass consumes
// nothing — the vector a mutation to printfVerbs produced, where the suite hung
// instead of going red and a hang is not a test failure anyone reads. It is now
// written as "stop when a pass consumed nothing", which terminates whatever the
// format turns out to be.
//
// Every line below is asserted only to ANSWER, not to answer any particular
// way: the property is termination, and the individual renderings are pinned by
// TestPayload_PrintfIsRenderedOnlyWhereItIsExact. The timeout is what makes a
// hang a failure.
func TestPayload_PrintfAlwaysTerminates(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, line := range []string{
			`printf '%%' x > f.md`,
			`printf '%%%%' x y z > f.md`,
			`printf '100%%\n' x > f.md`,
			`printf 'literal' a b c > f.md`,
			`printf '' a b > f.md`,
			`printf '%s' > f.md`,
			`printf '%s%%' a b > f.md`,
			`printf '%d%%' 1 2 3 > f.md`,
		} {
			// The value is not the point; returning at all is.
			_ = payloadFor(t, line)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("printf rendering did not terminate: a pass that consumes no " +
			"argument must end the format-reuse loop")
	}
}

// TestPayload_TruncatingCommandsProduceARealEmpty is the tier where "" is a
// FACT rather than a stand-in for ignorance.
//
// This is the distinction the whole change turns on. `: > f.md` genuinely
// empties the file, and a rule written `content == ""` should fire on it — as
// opposed to `unknown-tool > f.md`, where the same empty string would be a lie.
func TestPayload_TruncatingCommandsProduceARealEmpty(t *testing.T) {
	for _, line := range []string{
		": > f.md",
		"true > f.md",
		"touch f.md",
		"truncate -s 0 f.md",
		"truncate -s0 f.md",
		"truncate --size=0 f.md",
		"truncate --size 0 f.md",
	} {
		p := payloadFor(t, line)
		assert.Equalf(t, PayloadLiteral, p.Kind, "line %q determines an empty file", line)
		assert.Equalf(t, "", p.Text, "line %q", line)
	}
}

// TestPayload_TruncateToANonZeroSizeIsNotClaimed is the boundary of the case
// above. Any size but zero depends on the file's current length — padding with
// NULs, or cutting at an offset — and the relative forms depend on it twice.
func TestPayload_TruncateToANonZeroSizeIsNotClaimed(t *testing.T) {
	for _, line := range []string{
		"truncate -s 100 f.md",
		"truncate -s +10 f.md",
		"truncate -s -10 f.md",
		"truncate --size=1K f.md",
		"truncate -r other.md f.md",
	} {
		assert.Equalf(t, PayloadNone, payloadFor(t, line).Kind,
			"line %q depends on the file's current bytes", line)
	}
}

// TestPayload_TouchIsMarkedMTimeOnly pins the flag that stops a create-shaped
// payload from lying about an existing file.
//
// touch determines an empty file when it CREATES one and changes nothing when
// the file is already there. This package cannot tell which without reading the
// tree, so it states both halves and the caller picks — see FileTarget.MTimeOnly.
func TestPayload_TouchIsMarkedMTimeOnly(t *testing.T) {
	targets := FileTargets("touch a.md b.md")
	require.Len(t, targets, 2)
	for _, tg := range targets {
		assert.True(t, tg.MTimeOnly, "touch changes no bytes when the file exists")
		assert.Equal(t, PayloadLiteral, tg.Payload.Kind)
		assert.Equal(t, "", tg.Payload.Text)
	}
}

// TestPayload_OnlyTouchIsMTimeOnly is the negative half, and it matters: a flag
// set on everything would suppress every computed result rather than just
// touch's.
func TestPayload_OnlyTouchIsMTimeOnly(t *testing.T) {
	for _, line := range []string{
		"echo hi > f.md",
		"cp a.md f.md",
		"truncate -s 0 f.md",
		": > f.md",
	} {
		for _, tg := range FileTargets(line) {
			assert.Falsef(t, tg.MTimeOnly, "line %q genuinely changes bytes", line)
		}
	}
}

// --- redirection operators --------------------------------------------------

// TestPayload_AppendIsDistinctFromTruncate is the operator distinction, and it
// changes the KIND rather than the text.
//
// `>` truncates, so the result is exactly the produced bytes. `>>` appends, so
// the result is the file's current bytes plus them — a base only the caller can
// read, which is why it is PayloadAppend rather than PayloadLiteral.
func TestPayload_AppendIsDistinctFromTruncate(t *testing.T) {
	truncating := payloadFor(t, "echo hi > f.md")
	assert.Equal(t, PayloadLiteral, truncating.Kind)
	assert.Equal(t, "hi\n", truncating.Text)

	appending := payloadFor(t, "echo hi >> f.md")
	assert.Equal(t, PayloadAppend, appending.Kind,
		"the base is the file itself, which this package does not read")
	assert.Equal(t, "hi\n", appending.Text, "the tail is still exact")
}

// TestPayload_ExoticWritingOperatorsClaimNothing holds the operators where the
// resulting bytes are genuinely not the command's stdout.
//
// `&>` merges stderr, whose contents are not knowable from the line. `<>` opens
// for read and write WITHOUT truncating and writes at an offset, so the result
// depends on the file's current bytes. Claiming the produced output for either
// would be wrong in a way no test would catch until it refused real work.
func TestPayload_ExoticWritingOperatorsClaimNothing(t *testing.T) {
	for _, line := range []string{
		"echo hi &> f.md",
		"echo hi &>> f.md",
		"echo hi <> f.md",
	} {
		assert.Equalf(t, PayloadNone, payloadFor(t, line).Kind,
			"line %q does not put exactly the command's stdout in the file", line)
	}
}

// --- here-documents ---------------------------------------------------------

// TestPayload_AQuotedHeredocIsLiteralAndAnUnquotedOneIsNot is the literalness
// rule applied to a here-document, and it is read off the DELIMITER rather than
// by scanning the body.
//
// Reading the delimiter is what the shell itself does, and it is right even for
// a quoted heredoc whose body happens to contain a dollar sign — which a body
// scan would wrongly refuse.
func TestPayload_AQuotedHeredocIsLiteralAndAnUnquotedOneIsNot(t *testing.T) {
	t.Run("quoted is verbatim", func(t *testing.T) {
		p := payloadFor(t, "cat > f.md <<'EOF'\nliteral $HOME stays\nEOF\n")
		assert.Equal(t, PayloadLiteral, p.Kind)
		assert.Equal(t, "literal $HOME stays\n", p.Text,
			"a quoted delimiter means the body is taken exactly, dollar signs and all")
	})

	t.Run("double-quoted delimiter is also verbatim", func(t *testing.T) {
		p := payloadFor(t, "cat > f.md <<\"EOF\"\nliteral $HOME\nEOF\n")
		assert.Equal(t, PayloadLiteral, p.Kind)
		assert.Equal(t, "literal $HOME\n", p.Text)
	})

	t.Run("unquoted interpolates, so it is not knowable", func(t *testing.T) {
		p := payloadFor(t, "cat > f.md <<EOF\nvalue is $HOME\nEOF\n")
		assert.Equal(t, PayloadNone, p.Kind,
			"the body expands against an environment this package does not have")
	})

	// The case above is refused twice over — the delimiter is unquoted AND the
	// body holds a ParamExp — so a mutation removing the delimiter test alone
	// survived it. This one has a body that is entirely literal, so the
	// DELIMITER is the only thing that can refuse it.
	//
	// It must still be refused. An unquoted delimiter means the shell will
	// expand the body, and a body with nothing to expand today is one
	// backtick away from having something tomorrow; more to the point, the
	// engine cannot claim "this expands to itself" without doing the expansion
	// it deliberately refuses to do.
	t.Run("unquoted is refused even when the body has nothing to expand", func(t *testing.T) {
		p := payloadFor(t, "cat > f.md <<EOF\nplain text only\nEOF\n")
		assert.Equal(t, PayloadNone, p.Kind,
			"the delimiter decides, and an unquoted one means the body is expanded")
	})
}

// TestPayload_ADashHeredocStripsLeadingTabsOnly models `<<-` exactly.
//
// The stripping is tabs only and leading only — that is the specification, and
// a version stripping whitespace generally would report bytes the shell does
// not produce.
func TestPayload_ADashHeredocStripsLeadingTabsOnly(t *testing.T) {
	p := payloadFor(t, "cat > f.md <<-'EOF'\n\ttabbed\n\t\tdouble\n  spaced\nEOF\n")
	assert.Equal(t, PayloadLiteral, p.Kind)
	assert.Equal(t, "tabbed\ndouble\n  spaced\n", p.Text,
		"leading tabs go, leading spaces stay")
}

// TestPayload_AHeredocIntoANonPassThroughProgramIsNotClaimed is the case that
// keeps `cat` from being a general theory of stdin.
//
// A here-document is the program's INPUT. Only a program that copies stdin to
// stdout lets that input stand as the redirection's output — `sort` reorders
// it, `wc` counts it, and reporting the document as the resulting file would be
// wrong for both.
func TestPayload_AHeredocIntoANonPassThroughProgramIsNotClaimed(t *testing.T) {
	for _, line := range []string{
		"sort > f.md <<'EOF'\nb\na\nEOF\n",
		"wc -l > f.md <<'EOF'\na\nEOF\n",
		// cat WITH operands concatenates those files rather than passing stdin
		// through, so the heredoc is not the whole output.
		"cat other.md > f.md <<'EOF'\nx\nEOF\n",
	} {
		assert.Equalf(t, PayloadNone, payloadFor(t, line).Kind,
			"line %q transforms its input", line)
	}
}

// TestPayload_AHeredocIntoTeeIsTheFilesContent covers the stdin-consuming
// programs, whose target comes from the known-binary table rather than from a
// redirection.
//
// `tee f.md <<'EOF'` names its file as an OPERAND while the bytes hang off the
// statement, so neither node answers alone — see stmtHdoc.
func TestPayload_AHeredocIntoTeeIsTheFilesContent(t *testing.T) {
	p := payloadFor(t, "tee f.md <<'EOF'\nwritten by tee\nEOF\n")
	assert.Equal(t, PayloadLiteral, p.Kind)
	assert.Equal(t, "written by tee\n", p.Text)
}

// TestPayload_TeeWithoutAHeredocClaimsNothing is the negative that keeps the
// case above honest. tee's bytes normally come from a pipe, which the line does
// not carry.
func TestPayload_TeeWithoutAHeredocClaimsNothing(t *testing.T) {
	assert.Equal(t, PayloadNone, payloadFor(t, "tee f.md").Kind)
	assert.Equal(t, PayloadNone, payloadFor(t, "generate | tee f.md").Kind)
}

// TestPayload_AHeredocDoesNotOverrideAProgramThatIgnoresStdin is the boundary
// of the stdin rule, and it was found by mutation rather than by design — the
// version that asked no question at all about the program survived every other
// test in this file.
//
// A here-document attached to a program that does NOT read stdin is simply
// discarded by the shell. `cp a.md b.md <<'EOF'` copies a.md exactly as it
// would without the heredoc, so the destination's content is still
// PayloadCopyOf naming a.md.
//
// Applying the heredoc to whatever file the line happened to name would be the
// worst class of bug this change can produce: not a missing content, but a
// CONFIDENTLY WRONG one, reporting bytes that never touch the file. A rule
// reading it would judge text the command discarded.
func TestPayload_AHeredocDoesNotOverrideAProgramThatIgnoresStdin(t *testing.T) {
	t.Run("cp keeps its copy reference", func(t *testing.T) {
		p := payloadFor(t, "cp a.md b.md <<'EOF'\nDISCARDED\nEOF\n")
		assert.Equal(t, PayloadCopyOf, p.Kind,
			"cp does not read stdin, so the heredoc is discarded by the shell")
		assert.Equal(t, []string{"a.md"}, p.From)
		assert.NotEqual(t, "DISCARDED\n", p.Text,
			"reporting the discarded heredoc would be a confidently wrong answer")
	})

	t.Run("touch keeps its empty create", func(t *testing.T) {
		p := payloadFor(t, "touch f.md <<'EOF'\nDISCARDED\nEOF\n")
		assert.Equal(t, PayloadLiteral, p.Kind)
		assert.Equal(t, "", p.Text, "touch creates an empty file whatever is on its stdin")
	})

	t.Run("truncate keeps its empty result", func(t *testing.T) {
		p := payloadFor(t, "truncate -s 0 f.md <<'EOF'\nDISCARDED\nEOF\n")
		assert.Equal(t, PayloadLiteral, p.Kind)
		assert.Equal(t, "", p.Text)
	})
}

// --- copies -----------------------------------------------------------------

// TestPayload_CopyingNamesItsSourceRatherThanReadingIt is the seam, stated as a
// test: the payload REFERS to the source, and resolving it is the caller's.
//
// This is what keeps FileTargets a pure function of a string. A version that
// read a.md here would need a cwd and a filesystem, and every test in this
// package would need a temp directory.
func TestPayload_CopyingNamesItsSourceRatherThanReadingIt(t *testing.T) {
	for _, line := range []string{"cp a.md b.md", "mv a.md b.md"} {
		t.Run(line, func(t *testing.T) {
			var dst FileTarget
			for _, tg := range FileTargets(line) {
				if tg.Effect == Write {
					dst = tg
				}
			}
			require.Equal(t, "b.md", dst.Path)
			assert.Equal(t, PayloadCopyOf, dst.Payload.Kind)
			assert.Equal(t, []string{"a.md"}, dst.Payload.From,
				"the source is NAMED, not read: reading it is filemod's half")
			assert.Empty(t, dst.Payload.Text, "a reference carries no bytes")
		})
	}
}

// TestPayload_MovingStillRemovesItsSource holds the half a payload must not
// displace. `mv a.md b.md` is a removal AND a write, and the removal carries no
// payload because a deleted file has no resulting content.
func TestPayload_MovingStillRemovesItsSource(t *testing.T) {
	var removed []FileTarget
	for _, tg := range FileTargets("mv a.md b.md") {
		if tg.Effect == Remove {
			removed = append(removed, tg)
		}
	}
	require.Len(t, removed, 1)
	assert.Equal(t, "a.md", removed[0].Path)
	assert.Equal(t, PayloadNone, removed[0].Payload.Kind,
		"a deletion has no resulting content, and PreFileDelete declares no field for one")
}

// TestPayload_CopyingIntoADirectoryClaimsNothing is the limit the path half
// already draws, held at the content level too.
//
// With three or more operands the last is a DIRECTORY and the resulting file is
// `dir/base(src)` — a path this package deliberately does not compute, because
// it depends on whether the directory exists. Claiming a payload for the
// directory would attach one file's bytes to a path that is not that file.
func TestPayload_CopyingIntoADirectoryClaimsNothing(t *testing.T) {
	for _, line := range []string{
		"cp a.md b.md target-dir",
		"mv a.md b.md target-dir",
	} {
		var dst FileTarget
		for _, tg := range FileTargets(line) {
			if tg.Effect == Write {
				dst = tg
			}
		}
		require.Equal(t, "target-dir", dst.Path, "line %q", line)
		assert.Equalf(t, PayloadNone, dst.Payload.Kind,
			"line %q copies INTO a directory, and the resulting filename is not computed", line)
	}
}

// TestPayload_ConcatenatingNamedFilesNamesThemAllInOrder is the multi-source
// tier, and the reason Payload.From is a slice rather than a string.
//
// `cat a.md b.md > c.md` determines c.md's bytes completely: they are a.md's
// followed by b.md's. Nothing but READING them is left, which is filemod's half
// — so this is the same seam cp uses, with more than one source through it.
//
// The ORDER is asserted rather than the set. `cat b.md a.md > c.md` is a
// different file, and a payload that lost the order would produce the right
// bytes only half the time.
func TestPayload_ConcatenatingNamedFilesNamesThemAllInOrder(t *testing.T) {
	p := payloadFor(t, "cat a.md b.md > c.md")
	assert.Equal(t, PayloadCopyOf, p.Kind)
	assert.Equal(t, []string{"a.md", "b.md"}, p.From,
		"the sources are NAMED in order, not read: reading them is filemod's half")
	assert.Empty(t, p.Text, "a reference carries no bytes")

	t.Run("the order is the command's own", func(t *testing.T) {
		assert.Equal(t, []string{"b.md", "a.md"}, payloadFor(t, "cat b.md a.md > c.md").From)
	})

	t.Run("one source is the same shape as a copy", func(t *testing.T) {
		p := payloadFor(t, "cat only.md > c.md")
		assert.Equal(t, PayloadCopyOf, p.Kind)
		assert.Equal(t, []string{"only.md"}, p.From)
	})
}

// TestPayload_ConcatenationNamesNoFileItMerelyReads is the half that keeps the
// case above from firing rules on the wrong files.
//
// cat WRITES nothing. `cat a.md b.md > c.md` names c.md through the
// redirection, and a.md and b.md are read. Reporting them as targets would fire
// a write-or-delete rule on files the command only looks at, which is the
// failure TestExtractCommand_ACommandTouchingNothingProducesNoEvent guards at
// the module level.
func TestPayload_ConcatenationNamesNoFileItMerelyReads(t *testing.T) {
	var paths []string
	for _, tg := range FileTargets("cat a.md b.md > c.md") {
		paths = append(paths, tg.Path)
	}
	assert.Equal(t, []string{"c.md"}, paths,
		"only the redirection's destination is written")
}

// TestPayload_ConcatenationWithATransformingFlagClaimsNothing is the boundary,
// and it is an allowlist rather than a denylist on purpose.
//
// Every one of cat's flags TRANSFORMS the output — `-n` numbers the lines, `-s`
// squeezes blanks, `-v`/`-e`/`-t` render non-printing characters visibly — so
// the result is not the sources' bytes. A version that skipped flags generically
// would report `cat -n a.md > b.md` as an exact copy, which is bytes the file
// never holds. Refusing every flag means a flag nobody here has heard of is
// refused with the rest.
func TestPayload_ConcatenationWithATransformingFlagClaimsNothing(t *testing.T) {
	for _, line := range []string{
		"cat -n a.md > c.md",
		"cat -b a.md > c.md",
		"cat -s a.md > c.md",
		"cat -v a.md > c.md",
		"cat -e a.md > c.md",
		"cat -A a.md > c.md",
		// A flag this list has never heard of is refused with the rest.
		"cat --some-future-flag a.md > c.md",
		// `-` is STDIN, whose bytes the line does not carry, even though every
		// other operand is a real file.
		"cat a.md - b.md > c.md",
		// A glob names a set of files the shell expands against a tree this
		// package does not read.
		"cat *.md > c.md",
	} {
		assert.Equalf(t, PayloadNone, payloadFor(t, line).Kind,
			"line %q does not put its sources' exact bytes in the file", line)
	}
}

// TestPayload_ConcatenatingOntoAnAppendClaimsNothing is the shape gap, stated
// rather than papered over.
//
// `cat a.md >> b.md` IS derivable in principle — b.md's current bytes followed
// by a.md's — but PayloadAppend carries a literal Text and has nowhere to put a
// reference. So the honest answer is none, and the alternative would be worse
// than absent: appending the empty Text would report b.md as unchanged, which is
// a confidently wrong result rather than a missing one.
func TestPayload_ConcatenatingOntoAnAppendClaimsNothing(t *testing.T) {
	p := payloadFor(t, "cat a.md >> b.md")
	assert.Equal(t, PayloadNone, p.Kind,
		"an append of a REFERENCE is a shape Payload does not model")
	assert.NotEqual(t, PayloadAppend, p.Kind,
		"an append carrying the empty tail would report the file as unchanged")
}

// TestPayload_DdWithAnInputFileIsACopy is dd's derivable tier.
//
// `dd if=a.md of=b.md` is a copy spelled differently, and it determines b.md's
// bytes exactly as `cp a.md b.md` does.
func TestPayload_DdWithAnInputFileIsACopy(t *testing.T) {
	for _, line := range []string{
		"dd if=a.md of=b.md",
		"dd of=b.md if=a.md",
		// A block size changes how the bytes are read, not which ones — with
		// no count= to multiply, the whole input is copied either way.
		"dd if=a.md of=b.md bs=4096",
		"dd if=a.md of=b.md ibs=512 obs=512",
		"dd if=a.md of=b.md status=none",
	} {
		p := payloadFor(t, line)
		assert.Equalf(t, PayloadCopyOf, p.Kind, "line %q copies its input entire", line)
		assert.Equalf(t, []string{"a.md"}, p.From, "line %q", line)
	}
}

// TestPayload_DdWithAPartialCopyClaimsNothing is the boundary, and the operand
// test behind it is an ALLOWLIST for the reason this case demonstrates.
//
// dd's operand vocabulary is long and implementation-varying. `count=` bounds
// the copy and `skip=`/`seek=` offset it, which are the obvious ones — but
// `conv=ucase` upper-cases the data, `conv=swab` swaps byte pairs, and `iflag=`
// changes the I/O semantics. A denylist naming only the obvious four would let
// every one of those through as a whole copy, reporting bytes the file never
// holds. So an operand that is not provably harmless means no claim.
func TestPayload_DdWithAPartialCopyClaimsNothing(t *testing.T) {
	for _, line := range []string{
		"dd if=a.md of=b.md count=1",
		"dd if=a.md of=b.md bs=1 count=10",
		"dd if=a.md of=b.md skip=100",
		"dd if=a.md of=b.md seek=100",
		// The operands a denylist would have missed.
		"dd if=a.md of=b.md conv=ucase",
		"dd if=a.md of=b.md conv=swab",
		"dd if=a.md of=b.md cbs=16 conv=block",
		"dd if=a.md of=b.md iflag=direct",
		"dd if=a.md of=b.md oflag=append",
		// An operand this has never heard of is refused with the rest.
		"dd if=a.md of=b.md future=thing",
		// A bare word that is not an operand at all.
		"dd if=a.md of=b.md --verbose",
	} {
		assert.Equalf(t, PayloadNone, payloadFor(t, line).Kind,
			"line %q does not copy its input entire", line)
	}
}

// TestPayload_ADdInputFileWinsOverADiscardedHeredoc is the precedence case, and
// it is the confidently-wrong direction rather than the missing one.
//
// dd reading `if=` never looks at stdin, so a here-document on the same
// statement is discarded by the shell. Overwriting the copy reference with it
// would report bytes that never reach the file — the same failure
// TestPayload_AHeredocDoesNotOverrideAProgramThatIgnoresStdin pins for cp,
// reached through a program that DOES sometimes consume stdin.
func TestPayload_ADdInputFileWinsOverADiscardedHeredoc(t *testing.T) {
	p := payloadFor(t, "dd if=a.md of=b.md <<'EOF'\nDISCARDED\nEOF\n")
	assert.Equal(t, PayloadCopyOf, p.Kind,
		"dd with an if= ignores stdin, so the heredoc never reaches the file")
	assert.Equal(t, []string{"a.md"}, p.From)
	assert.NotEqual(t, "DISCARDED\n", p.Text)
}

// TestPayload_InstallIsACopy applies cp's two-operand analysis to the utility
// that shares its operand shape. `install a.md b.md` leaves b.md holding a.md's
// current bytes; the mode it also sets is not something a file event carries.
func TestPayload_InstallIsACopy(t *testing.T) {
	p := payloadFor(t, "install a.md b.md")
	assert.Equal(t, PayloadCopyOf, p.Kind)
	assert.Equal(t, []string{"a.md"}, p.From)

	t.Run("a mode does not slide into operand position", func(t *testing.T) {
		// The failure this prevents is specific: an unskipped `644` becomes an
		// operand, and with two real ones beside it the DESTINATION becomes a
		// path named after a permission bit.
		for _, line := range []string{
			"install -m 644 a.md b.md",
			"install -o root a.md b.md",
			"install -g wheel a.md b.md",
		} {
			var dst FileTarget
			for _, tg := range FileTargets(line) {
				if tg.Effect == Write {
					dst = tg
				}
			}
			assert.Equalf(t, "b.md", dst.Path, "line %q", line)
			assert.Equalf(t, PayloadCopyOf, dst.Payload.Kind, "line %q", line)
			assert.Equalf(t, []string{"a.md"}, dst.Payload.From, "line %q", line)
		}
	})
}

// TestPayload_InstallWithATargetDirectoryFlagClaimsNothing is the shape that
// INVERTS install's operand reading, and it was a measured wrong answer rather
// than a hypothetical one.
//
// `-t DIR` names the destination as a flag value, so every operand is a SOURCE
// and the destination is not among them. Read positionally, the last source
// looks like the destination: `install -t target-dir a.md b.md` reported b.md
// as written carrying a.md's bytes, when the real results are target-dir/a.md
// and target-dir/b.md and b.md is only read.
//
// That is the confidently-wrong class — a rule fires on the wrong file and
// judges bytes that never land there. Refused outright, which costs a rule that
// does not fire on a spelling agents essentially never write.
func TestPayload_InstallWithATargetDirectoryFlagClaimsNothing(t *testing.T) {
	for _, line := range []string{
		"install -t target-dir a.md",
		"install -t target-dir a.md b.md",
		"install --target-directory target-dir a.md b.md",
		"install --target-directory=target-dir a.md b.md",
	} {
		assert.Emptyf(t, FileTargets(line),
			"line %q names its destination as a flag value, so the operands are all sources", line)
	}
}

// TestPayload_InstallMakingDirectoriesIsNotACopy is install's other shape.
// `install -d dir` CREATES directories rather than copying, so every operand is
// a directory and none is a source. Reading it as a copy would name the last
// directory as a file holding another directory's bytes.
func TestPayload_InstallMakingDirectoriesIsNotACopy(t *testing.T) {
	for _, line := range []string{"install -d a b", "install --directory a b"} {
		assert.Emptyf(t, FileTargets(line),
			"line %q makes directories, and no file event can be about one", line)
	}
}

// TestPayload_LinkingClaimsNoContent is the ln verdict: derivable in one narrow
// reading, and deliberately not claimed.
//
// A SYMLINK's bytes are its target PATH, not the target's contents. So
// `ln -s a.md b.md` does not leave b.md holding a.md's bytes, and a copy
// reference naming a.md would be the confidently-wrong class of answer — a rule
// reading `content` would judge text that is not in the file.
//
// A HARD link's contents genuinely are the target's, and that IS derivable. It
// is unclaimed anyway: the spelling essentially does not appear in
// agent-written command lines, and a shape nobody writes is maintenance with no
// reader.
func TestPayload_LinkingClaimsNoContent(t *testing.T) {
	for _, line := range []string{
		"ln -s a.md b.md",
		"ln -sf a.md b.md",
		"ln a.md b.md",
	} {
		var dst FileTarget
		for _, tg := range FileTargets(line) {
			if tg.Effect == Write {
				dst = tg
			}
		}
		require.Equalf(t, "b.md", dst.Path, "line %q still names the link it creates", line)
		assert.Equalf(t, PayloadNone, dst.Payload.Kind,
			"line %q: a symlink holds a path, not the target's bytes", line)
	}
}

// TestPayload_ACopyStatesBothReadingsOfItsDestination is the second case, after
// touch, where one line means different things depending on the tree.
//
// `cp a.md dest` writes dest when dest is a file and dest/a.md when dest is a
// directory, and only a stat can tell. So the line states BOTH — the file
// reading in Payload, the directory reading in Into — and filemod picks. This
// package still reads no tree.
func TestPayload_ACopyStatesBothReadingsOfItsDestination(t *testing.T) {
	t.Run("two operands state both", func(t *testing.T) {
		var dst FileTarget
		for _, tg := range FileTargets("cp a.md dest") {
			if tg.Effect == Write {
				dst = tg
			}
		}
		assert.Equal(t, "dest", dst.Path)
		assert.Equal(t, PayloadCopyOf, dst.Payload.Kind, "the file reading")
		assert.Equal(t, []string{"a.md"}, dst.Payload.From)
		assert.Equal(t, []string{"a.md"}, dst.Into, "the directory reading")
	})

	t.Run("three or more operands have only the directory reading", func(t *testing.T) {
		// `cp a.md b.md c.md` with c.md a regular file is an ERROR, not a copy
		// onto it, so there is no file reading to state.
		var dst FileTarget
		for _, tg := range FileTargets("cp a.md b.md target-dir") {
			if tg.Effect == Write {
				dst = tg
			}
		}
		assert.Equal(t, "target-dir", dst.Path)
		assert.Equal(t, PayloadNone, dst.Payload.Kind,
			"a third operand rules out the destination being a file")
		assert.Equal(t, []string{"a.md", "b.md"}, dst.Into)
	})

	t.Run("mv and install share the shape", func(t *testing.T) {
		for _, line := range []string{"mv a.md b.md target-dir", "install a.md b.md target-dir"} {
			var dst FileTarget
			for _, tg := range FileTargets(line) {
				if tg.Effect == Write {
					dst = tg
				}
			}
			assert.Equalf(t, []string{"a.md", "b.md"}, dst.Into, "line %q", line)
		}
	})
}

// TestPayload_OnlyACopyStatesADirectoryReading is the negative half. A field set
// on everything would make filemod expand every write into a directory listing.
func TestPayload_OnlyACopyStatesADirectoryReading(t *testing.T) {
	for _, line := range []string{
		"echo hi > f.md",
		"touch f.md",
		"rm f.md",
		"ln -s a.md b.md",
		"tee f.md",
		"dd if=a.md of=b.md",
		"truncate -s 0 f.md",
	} {
		for _, tg := range FileTargets(line) {
			assert.Emptyf(t, tg.Into, "line %q names no directory to copy into", line)
		}
	}
}

// --- the unknowable tier ----------------------------------------------------

// TestPayload_AnUnknownProgramClaimsNothing is the case the whole design is
// measured against.
//
// The redirection is real and the file is named, so the event still happens.
// The bytes are not knowable, and `content: ""` would make this
// indistinguishable from `touch new.md` — whose empty content is a fact. That
// collision is the defect this change removes, so reintroducing it here would
// undo the point.
func TestPayload_AnUnknownProgramClaimsNothing(t *testing.T) {
	for _, line := range []string{
		"some-unknown-tool > f.md",
		"python script.py > f.md",
		"make build > f.md",
		"./bin/generate > f.md",
		"curl https://example.com > f.md",
	} {
		targets := FileTargets(line)
		require.Lenf(t, targets, 1, "line %q still names its file", line)
		assert.Equalf(t, PayloadNone, targets[0].Payload.Kind,
			"line %q determines no bytes", line)
	}
}

// TestPayload_SedInPlaceClaimsNothing is a deliberate decision rather than an
// omission, and it is the one the brief asked to be argued either way.
//
// `sed -i 's/a/b/' f.md` states a TRANSFORMATION, not an outcome. Deriving the
// result would mean implementing sed's expression language — addresses, ranges,
// hold space, alternate delimiters, the `s` flags — against a file this package
// does not read, and then being byte-exact or being worse than useless.
//
// # The narrow subset, reconsidered and rejected again
//
// The open question was whether `s/LITERAL/LITERAL/` — no address, no flags, no
// regex metacharacter in either half — is worth deriving. It is genuinely
// computable against the file's current bytes, so the objection is not that it
// cannot be done. Three things decide it:
//
//  1. The subset is a small fraction of real usage. Almost every sed an agent
//     writes carries a `g` flag, an address, an alternate delimiter, or a
//     character class — and each of those is a case the subset must REFUSE,
//     which means the common path lands back at PayloadNone anyway. The tier
//     would buy the uncommon spelling and leave the common one exactly where it
//     is now.
//
//  2. The boundary is where the danger is, not the arithmetic. The rejection
//     would have to be a strict allowlist of permitted characters — never a
//     denylist of forbidden ones — because a denylist that has not heard of one
//     metacharacter derives a substitution that sed would not perform. And the
//     allowlist has to hold for the DELIMITER too: `s|a|b|` and `s#a#b#` are the
//     same command with different syntax, and BSD and GNU sed differ on what a
//     backslash means inside a bracket expression. Getting any of it wrong
//     produces a `result` that is confidently WRONG.
//
//  3. Wrong is worse than absent, and by a wide margin. `resultKnown: false`
//     costs a rule that does not judge content on this line. A wrong `result`
//     costs a rule that judges the WRONG content and reports its verdict with
//     full confidence — and no rule, and no reader of one, can tell a wrong
//     answer from a right one.
//
// Points 1 and 3 together are what settle it: the tier is bought at the price of
// the worst failure this engine can produce, and it is not even bought for the
// spelling people write. Executing sed is out of the question for the separate
// and obvious reason that this package runs nothing.
//
// So the answer is no, deliberately, and `resultKnown` is what makes that answer
// sayable instead of silent. This is the case that field exists for — see
// TestExtractCommand_SedInPlaceIsAnUpdateWithNoDerivableResult for the other
// half, where the PATH still fires and only the content abstains.
func TestPayload_SedInPlaceClaimsNothing(t *testing.T) {
	for _, line := range []string{
		"sed -i 's/a/b/' f.md",
		"sed -i '' 's/a/b/' f.md",
		"sed -i.bak 's/a/b/' f.md",
		"perl -i -pe 's/a/b/' f.md",
	} {
		targets := FileTargets(line)
		require.NotEmptyf(t, targets, "line %q still names its file", line)
		for _, tg := range targets {
			assert.Equalf(t, PayloadNone, tg.Payload.Kind,
				"line %q states a transformation, not an outcome", line)
		}
	}
}

// TestPayload_DdWithoutAnInputFileClaimsNothing holds dd's unknowable case: with
// no `if=` its bytes come from STDIN, which the line's own text does not carry.
func TestPayload_DdWithoutAnInputFileClaimsNothing(t *testing.T) {
	assert.Equal(t, PayloadNone, payloadFor(t, "dd of=f.md").Kind)
	assert.Equal(t, PayloadNone, payloadFor(t, "generate | dd of=f.md").Kind)
}

// TestPayload_AHeredocIntoDdIsTheFilesContent is dd's knowable case, and it is
// here because a mutation dropping dd from the stdin list survived without it —
// tee alone was carrying the whole rule.
//
// `dd of=f.md <<'EOF'` writes the here-document into f.md, exactly as tee
// does. The two are listed together and so must be tested together.
func TestPayload_AHeredocIntoDdIsTheFilesContent(t *testing.T) {
	p := payloadFor(t, "dd of=f.md <<'EOF'\nwritten by dd\nEOF\n")
	assert.Equal(t, PayloadLiteral, p.Kind)
	assert.Equal(t, "written by dd\n", p.Text)
}

// --- payloads survive the wrappers the path half already unwraps -------------

// TestPayload_SurvivesWrappersAndInterpreterPayloads is the property that keeps
// this from being evadable.
//
// The path half already unwraps `sudo` and re-parses `sh -c`, precisely so a
// rule cannot be dodged by prefixing a wrapper. The content half must reach the
// same depth, or `sh -c 'echo x > f.md'` would report the file with no bytes
// while the bare line reported both.
func TestPayload_SurvivesWrappersAndInterpreterPayloads(t *testing.T) {
	for _, line := range []string{
		"sudo tee f.md <<'EOF'\nbody\nEOF\n",
	} {
		p := payloadFor(t, line)
		assert.Equalf(t, PayloadLiteral, p.Kind, "line %q", line)
		assert.Equalf(t, "body\n", p.Text, "line %q", line)
	}

	t.Run("an interpreter payload", func(t *testing.T) {
		p := payloadFor(t, `sh -c 'echo hi > f.md'`)
		assert.Equal(t, PayloadLiteral, p.Kind,
			"the re-parsed payload must derive content too, or a wrapper hides it")
		assert.Equal(t, "hi\n", p.Text)
	})
}

// --- one line naming several files ------------------------------------------

// TestPayload_EachTargetOnALineKeepsItsOwnPayload holds that the payload is
// per-TARGET rather than per-line. `echo hi > a.md > b.md` sends the same bytes
// to both, while `echo hi > a.md; touch b.md` sends different ones.
func TestPayload_EachTargetOnALineKeepsItsOwnPayload(t *testing.T) {
	got := map[string]Payload{}
	for _, tg := range FileTargets("echo hi > a.md; touch b.md; cp src.md c.md") {
		if tg.Effect == Write {
			got[tg.Path] = tg.Payload
		}
	}
	require.Len(t, got, 3)

	assert.Equal(t, "hi\n", got["a.md"].Text)
	assert.Equal(t, PayloadLiteral, got["a.md"].Kind)

	assert.Equal(t, "", got["b.md"].Text)
	assert.Equal(t, PayloadLiteral, got["b.md"].Kind)

	assert.Equal(t, PayloadCopyOf, got["c.md"].Kind)
	assert.Equal(t, []string{"src.md"}, got["c.md"].From)
}
