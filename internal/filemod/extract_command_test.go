package filemod

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// bashPending is the payload for a shell tool carrying a command line.
//
// The tool is named Bash because that is what Claude Code emits, but nothing in
// the module reads it — the name is here so the fixture looks like the real
// thing, not because anything branches on it. TestExtractCommand_TheToolNameIsNot
// Consulted holds that.
func bashPending(command string) fakePending {
	args, _ := json.Marshal(map[string]string{"command": command})
	return fakePending{tool: "Bash", args: args}
}

// extractFor runs the pre phase over a command line and returns what came back.
func extractFor(t *testing.T, command string) ([]event.Event, error) {
	t.Helper()
	return New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: bashPending(command),
	})
}

// kindsByPath renders events as path→kind, so a case reads as the answer.
func kindsByPath(events []event.Event) map[string]string {
	got := map[string]string{}
	for _, e := range events {
		path, _ := e.Fields[FieldPath].(string)
		got[path] = e.Kind
	}
	return got
}

// TestExtractCommand_TheFourMeasuredCommands is the defect, end of the module.
//
// Measured before this existed: commandmod saw a program and this module saw
// NOTHING, so the Bash tool was a hole straight through every file rule. Each
// command here names an existing file, so each one now produces the Pre event a
// rule can refuse.
//
// `echo x > out.md` appears twice on purpose — once over an existing file and
// once over an absent one — because those are different answers and only one of
// them is an event. See TestExtractCommand_ACreationIsNotPredicted.
func TestExtractCommand_TheFourMeasuredCommands(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"notes.md", "a.md", "b.md", "out.md", "f.md"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("body\n"), 0o644))
	}
	at := func(name string) string { return filepath.Join(dir, name) }

	for _, tc := range []struct {
		command string
		want    map[string]string
	}{
		{
			// The headline case: a rule bound to PreFileDelete on notes.md must
			// fire on this.
			command: "rm -rf " + at("notes.md"),
			want:    map[string]string{at("notes.md"): KindPreDelete},
		},
		{
			// Both halves: a.md stops existing, b.md is replaced.
			command: "mv " + at("a.md") + " " + at("b.md"),
			want: map[string]string{
				at("a.md"): KindPreDelete,
				at("b.md"): KindPreUpdate,
			},
		},
		{
			command: "echo x > " + at("out.md"),
			want:    map[string]string{at("out.md"): KindPreUpdate},
		},
		{
			command: "sed -i '' s/a/b/ " + at("f.md"),
			want:    map[string]string{at("f.md"): KindPreUpdate},
		},
	} {
		events, err := extractFor(t, tc.command)
		require.NoError(t, err, "command %q", tc.command)
		assert.Equal(t, tc.want, kindsByPath(events), "command %q", tc.command)
	}
}

// TestExtractCommand_ACommandTouchingNothingProducesNoEvent is the reverse
// proof, and it is the one that keeps this from becoming a rule that fires on
// everything.
//
// Every command here NAMES an existing file and changes none of it. If any
// produced an event, a guardrail about a file would fire on the agent merely
// reading it — which would make the feature worse than its absence, because a
// rule that fires on `cat` is a rule its author turns off.
func TestExtractCommand_ACommandTouchingNothingProducesNoEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	require.NoError(t, os.WriteFile(path, []byte("body\n"), 0o644))

	for _, command := range []string{
		"cat " + path,
		"head -n 5 " + path,
		"wc -l " + path,
		"grep foo " + path,
		"sed s/a/b/ " + path, // no -i: reads the file, writes stdout
		"git status",
		"ls -la " + dir,
		"python script.py",
		"make build",
		"npm run build",
		"echo hello",
		"true",
	} {
		events, err := extractFor(t, command)
		require.NoError(t, err, "command %q", command)
		assert.Emptyf(t, events, "command %q changes no file and must produce no event", command)
	}
}

// TestExtractCommand_ACreationIsNotPredictedWhenItsBytesAreUnknowable is what
// remains of the old blanket rule, narrowed to the cases where it is actually
// true.
//
// The rule used to be: a command aimed at a path that does not exist produces
// nothing, because PreFileCreate requires `content` and "a command line does not
// say what bytes will result". The second half is false for a real subset of
// commands — `echo`, `touch`, a quoted heredoc, `cp` — and the blanket was using
// "we cannot know in general" to avoid the cases where we can. Those now predict
// their content and are pinned by the tests below.
//
// What is left here is the genuine article: a line whose redirection is real and
// whose bytes are not. The original argument applies unchanged to these, and it
// is the reason silence is still right rather than `content: ""` — an empty
// string would make `unknown-tool > new.md` indistinguishable from `touch
// new.md`, whose empty content is a FACT. That distinction is the whole point of
// the change, so preserving it here matters more than emitting one more event.
func TestExtractCommand_ACreationIsNotPredictedWhenItsBytesAreUnknowable(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "not-there-yet.md")

	for _, command := range []string{
		// A program this engine knows nothing about. The redirection is real,
		// the bytes are not.
		"some-unknown-tool > " + absent,
		"python script.py > " + absent,
		"make build > " + absent,
		// tee's bytes come from stdin, which the line does not carry.
		"tee " + absent,
		// An UNQUOTED heredoc interpolates against an environment this does
		// not have. The quoted form is knowable and is tested separately.
		"cat > " + absent + " <<EOF\nvalue is $HOME\nEOF\n",
		// echo -e enables escape handling that differs between shells, so the
		// resulting bytes depend on the interpreter rather than the line.
		"echo -e 'a\\tb' > " + absent,
		// A non-literal word: the content is resolved out of an assumed empty
		// environment, which safeConfig refuses to treat as real.
		"echo $GREETING > " + absent,
		// printf with a format this does not model exactly. %f carries a
		// default precision and a locale-dependent decimal point; a WIDTH
		// pads with whitespace this deliberately will not guess at.
		"printf '%f' 1.5 > " + absent,
		"printf '%5s' x > " + absent,
		// cp from a source that is not there: the command fails, so nothing is
		// created and nothing may be claimed.
		"cp " + filepath.Join(dir, "missing-source.md") + " " + absent,
		// A copy whose source is a DIRECTORY. `cp -r somedir dst` produces a
		// tree rather than a file, and no single content can describe it.
		"cp -r " + dir + " " + absent,
	} {
		events, err := extractFor(t, command)
		require.NoError(t, err, "command %q", command)
		assert.Emptyf(t, events,
			"command %q creates a file whose bytes are not derivable, so it must stay silent "+
				"rather than claim content:\"\"", command)
	}
}

// TestExtractCommand_ACreationIsPredictedWhenItsBytesAreDerivable is the
// widened half: the engine already parses these lines completely, so where the
// line determines the resulting bytes it says so.
//
// Each case is a create — the path does not exist — carrying the exact content
// the command would leave, trailing newline included. The newline is not a
// detail: `echo hi` writes "hi\n" and `printf '%s' hi` writes "hi", and a rule
// fingerprinting the file gets a different answer for each.
func TestExtractCommand_ACreationIsPredictedWhenItsBytesAreDerivable(t *testing.T) {
	for name, tc := range map[string]struct {
		command string
		want    string
	}{
		"echo appends a newline": {
			command: "echo hello > %s",
			want:    "hello\n",
		},
		"echo -n does not": {
			command: "echo -n hello > %s",
			want:    "hello",
		},
		"echo joins its words with single spaces": {
			command: "echo one two three > %s",
			want:    "one two three\n",
		},
		"echo of nothing is just the newline": {
			command: "echo > %s",
			want:    "\n",
		},
		"a quoted heredoc is verbatim": {
			command: "cat > %s <<'EOF'\nliteral $HOME stays\nEOF\n",
			want:    "literal $HOME stays\n",
		},
		"printf with no conversions adds no newline": {
			command: "printf 'no newline' > %s",
			want:    "no newline",
		},
		"printf %s substitutes its argument": {
			command: "printf '%%s\\n' hello > %s",
			want:    "hello\n",
		},
		"touch creates an empty file, and that empty is REAL": {
			command: "touch %s",
			want:    "",
		},
		"a truncating redirection of nothing empties it": {
			command: ": > %s",
			want:    "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "created.md")

			events, err := extractFor(t, fmt.Sprintf(tc.command, path))
			require.NoError(t, err)
			require.Len(t, events, 1, "a derivable creation is predicted")
			require.Equal(t, KindPreCreate, events[0].Kind)
			assert.Equal(t, tc.want, events[0].Fields[FieldNewContent],
				"the exact bytes, trailing newline and all")
		})
	}
}

// TestExtractCommand_CopyingAFileCarriesTheSourcesCurrentBytes is the
// PayloadCopyOf tier, and the one that needs BOTH halves of the seam.
//
// commandmod says "the result is whatever a.md holds" without reading anything;
// filemod reads it. Neither package could answer alone, which is why the
// payload carries a reference rather than a string.
func TestExtractCommand_CopyingAFileCarriesTheSourcesCurrentBytes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.md")
	require.NoError(t, os.WriteFile(src, []byte("# Source body\n"), 0o644))

	for name, command := range map[string]string{
		"cp": "cp " + src + " %s",
		"mv": "mv " + src + " %s",
	} {
		t.Run(name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "destination.md")

			events, err := extractFor(t, fmt.Sprintf(command, dst))
			require.NoError(t, err)

			var create event.Event
			var found bool
			for _, e := range events {
				if e.Fields[FieldPath] == dst {
					create, found = e, true
				}
			}
			require.True(t, found, "the destination must be reported")
			assert.Equal(t, KindPreCreate, create.Kind)
			assert.Equal(t, "# Source body\n", create.Fields[FieldNewContent],
				"the destination's bytes are the source's current bytes")
		})
	}
}

// TestExtractCommand_AbsentContentIsNotEmptyContent is the reference case the
// whole tier exists to protect, asserted on the FIELD rather than on the event.
//
// `touch new.md` and `some-unknown-tool > new.md` both create a file. The first
// genuinely leaves it EMPTY, and `content: ""` is the true answer. The second's
// bytes are not derivable, and the same `""` would be a lie indistinguishable
// from the first.
//
// The two are distinguished by whether the field is THERE at all — which is why
// the assertion below reads the map's second return value rather than comparing
// to "". A test written `assert.Equal(t, "", content)` would pass for both, and
// pass for exactly the bug this exists to prevent.
//
// This is not academic. The owner's live frontmatter-transcript-path guardrail
// branches on `has("content")` for precisely this reason, so collapsing the two
// would change what that rule does to real work.
func TestExtractCommand_AbsentContentIsNotEmptyContent(t *testing.T) {
	contentField := func(t *testing.T, command string) (any, bool, int) {
		t.Helper()
		events, err := extractFor(t, command)
		require.NoError(t, err)
		if len(events) != 1 {
			return nil, false, len(events)
		}
		v, present := events[0].Fields[FieldNewContent]
		return v, present, 1
	}

	t.Run("a real empty carries the field", func(t *testing.T) {
		for _, command := range []string{
			"touch %s",
			": > %s",
			"truncate -s 0 %s",
			"printf '' > %s",
			"echo -n > %s",
		} {
			path := filepath.Join(t.TempDir(), "new.md")
			v, present, n := contentField(t, fmt.Sprintf(command, path))
			require.Equalf(t, 1, n, "command %q creates a file", command)
			require.Truef(t, present,
				"command %q leaves a genuinely empty file, so content must be PRESENT", command)
			assert.Equalf(t, "", v, "command %q", command)
		}
	})

	t.Run("an underivable create omits the field entirely", func(t *testing.T) {
		for _, command := range []string{
			"some-unknown-tool > %s",
			"curl https://example.com > %s",
			"python script.py > %s",
			// The tiers this task decided AGAINST, held to the same bar: each
			// must omit the field rather than render an empty one.
			"printf '%%5s' x > %s",
			"cat -n other.md > %s",
			"dd if=other.md of=%s count=1",
		} {
			path := filepath.Join(t.TempDir(), "new.md")
			events, err := extractFor(t, fmt.Sprintf(command, path))
			require.NoErrorf(t, err, "command %q", command)
			for _, e := range events {
				_, present := e.Fields[FieldNewContent]
				assert.Falsef(t, present,
					"command %q cannot derive its bytes, so content must be ABSENT — "+
						"an empty one would be indistinguishable from touch", command)
			}
		}
	})
}

// TestExtractCommand_ConcatenatingNamedFilesCarriesTheirBytesInOrder is the
// multi-source half of the copy tier, and it needs both sides of the seam for
// several files at once.
//
// `cat a.md b.md > c.md` determines c.md completely: a.md's bytes followed by
// b.md's. commandmod names them in order without reading either, and this side
// reads them.
func TestExtractCommand_ConcatenatingNamedFilesCarriesTheirBytesInOrder(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	b := filepath.Join(dir, "b.md")
	require.NoError(t, os.WriteFile(a, []byte("# First\n"), 0o644))
	require.NoError(t, os.WriteFile(b, []byte("# Second\n"), 0o644))

	t.Run("into a new file", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "joined.md")
		events, err := extractFor(t, "cat "+a+" "+b+" > "+out)
		require.NoError(t, err)
		require.Len(t, events, 1)
		require.Equal(t, KindPreCreate, events[0].Kind)
		assert.Equal(t, "# First\n# Second\n", events[0].Fields[FieldNewContent],
			"the sources' bytes, concatenated in the order the line names them")
	})

	// The ORDER, at the module level. A payload that lost it would produce the
	// right bytes only half the time, and both halves read as plausible.
	t.Run("the reverse order is a different file", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "joined.md")
		events, err := extractFor(t, "cat "+b+" "+a+" > "+out)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, "# Second\n# First\n", events[0].Fields[FieldNewContent])
	})

	t.Run("over an existing file it is a known result", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "joined.md")
		require.NoError(t, os.WriteFile(out, []byte("replaced\n"), 0o644))
		events, err := extractFor(t, "cat "+a+" "+b+" > "+out)
		require.NoError(t, err)
		require.Len(t, events, 1)
		require.Equal(t, KindPreUpdate, events[0].Kind)
		assert.Equal(t, true, events[0].Fields[FieldResultKnown])
		assert.Equal(t, "# First\n# Second\n", events[0].Fields[FieldNewContent])
	})
}

// TestExtractCommand_AConcatenationWithAnUnreadableSourceClaimsNothing is the
// failure direction, and the whole-or-nothing rule is the point.
//
// `cat a.md missing.md > c.md` writes a.md's bytes and then FAILS, so c.md's
// final contents are not what a partial concatenation would report. Reporting
// the readable PREFIX would be a confidently wrong answer — bytes that look
// like a complete file and are not.
func TestExtractCommand_AConcatenationWithAnUnreadableSourceClaimsNothing(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	require.NoError(t, os.WriteFile(a, []byte("# First\n"), 0o644))
	out := filepath.Join(dir, "joined.md")

	for name, command := range map[string]string{
		"a missing source after a readable one":  "cat " + a + " " + filepath.Join(dir, "gone.md") + " > " + out,
		"a missing source before a readable one": "cat " + filepath.Join(dir, "gone.md") + " " + a + " > " + out,
		"a source that is a directory":           "cat " + a + " " + dir + " > " + out,
	} {
		t.Run(name, func(t *testing.T) {
			events, err := extractFor(t, command)
			require.NoError(t, err)
			assert.Empty(t, events,
				"the concatenation fails, so no content may be claimed — not even the readable part")
		})
	}
}

// TestExtractCommand_CopyingIntoADirectoryNamesTheResultingFiles resolves the
// ambiguity commandmod deliberately leaves open.
//
// `cp a.md b.md target/` produces target/a.md and target/b.md, not a file
// called target. Which it is depends on whether target is a directory, which is
// a tree question — so commandmod states both readings and this side stats and
// picks. Each resulting file carries its OWN source's bytes: a payload spread
// across both would attach one file's contents to the other's path.
func TestExtractCommand_CopyingIntoADirectoryNamesTheResultingFiles(t *testing.T) {
	for _, bin := range []string{"cp", "mv", "install"} {
		t.Run(bin, func(t *testing.T) {
			dir := t.TempDir()
			a := filepath.Join(dir, "a.md")
			b := filepath.Join(dir, "b.md")
			require.NoError(t, os.WriteFile(a, []byte("# A\n"), 0o644))
			require.NoError(t, os.WriteFile(b, []byte("# B\n"), 0o644))
			target := filepath.Join(dir, "target")
			require.NoError(t, os.Mkdir(target, 0o755))

			events, err := extractFor(t, bin+" "+a+" "+b+" "+target)
			require.NoError(t, err)

			got := map[string]string{}
			for _, e := range events {
				if e.Kind != KindPreCreate {
					continue
				}
				path, _ := e.Fields[FieldPath].(string)
				content, _ := e.Fields[FieldNewContent].(string)
				got[path] = content
			}
			assert.Equal(t, map[string]string{
				filepath.Join(target, "a.md"): "# A\n",
				filepath.Join(target, "b.md"): "# B\n",
			}, got, "each resulting file carries its OWN source's bytes")

			// The DIRECTORY itself is not reported. It is not a file, and a
			// rule about target/ is not the rule anyone wrote.
			for _, e := range events {
				assert.NotEqual(t, target, e.Fields[FieldPath],
					"the directory is not one of the files that change")
			}
		})
	}
}

// TestExtractCommand_ACopySourceWithNoBasenameNamesNoFileInside is the crafted
// case, and it was found by mutation: removing the basename guard left every
// other test green.
//
// `cp somedir/. target/` and `cp somedir/.. target/` are shapes no ordinary
// copy has, but a command line can carry them. filepath.Base returns "." and
// ".." for them, and joining either onto the directory produces the DIRECTORY
// ITSELF or a path OUTSIDE it — so a target would be predicted for a path the
// copy does not create, one of which is above the destination entirely.
//
// The event that must not appear is the one naming the directory or its parent.
// Skipping the source is the honest answer: the line's real effect is a
// recursive copy whose resulting files are not derivable anyway.
func TestExtractCommand_ACopySourceWithNoBasenameNamesNoFileInside(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(sub, 0o755))
	target := filepath.Join(dir, "target")
	require.NoError(t, os.Mkdir(target, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(target, "sub"), []byte("decoy\n"), 0o644))

	for _, src := range []string{sub + "/.", sub + "/.."} {
		events, err := extractFor(t, "cp -r "+src+" "+target)
		require.NoErrorf(t, err, "source %q", src)
		for _, e := range events {
			assert.NotEqualf(t, target, e.Fields[FieldPath],
				"source %q must not resolve to the destination directory itself", src)
			assert.NotEqualf(t, dir, e.Fields[FieldPath],
				"source %q must not resolve to a path ABOVE the destination", src)
		}
	}
}

// TestExtractCommand_ASymlinkToADirectoryIsNotExpanded is the boundary of the
// directory question, and it was found by mutation: switching isDirectory from
// Lstat to Stat left every other test green.
//
// `cp a.md link-to-dir` DOES follow the link on a real system, so the honest
// answer here is narrower than the truth — and deliberately so. Expanding it
// means resolving a path the command line did not name, which is a guess about
// the tree rather than a reading of it. The cost is one unclaimed result on a
// spelling nobody writes; the alternative is this module deciding where a link
// points, which is the class of judgement it refuses everywhere else.
//
// What must NOT happen either way is a confidently wrong answer. The link
// itself is what lookAt sees, so the event is about the link — never about a
// file inside whatever it points at.
func TestExtractCommand_ASymlinkToADirectoryIsNotExpanded(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	require.NoError(t, os.WriteFile(a, []byte("# A\n"), 0o644))
	real := filepath.Join(dir, "realdir")
	require.NoError(t, os.Mkdir(real, 0o755))
	link := filepath.Join(dir, "linkdir")
	require.NoError(t, os.Symlink(real, link))

	events, err := extractFor(t, "cp "+a+" "+link)
	require.NoError(t, err)

	for _, e := range events {
		assert.NotEqual(t, filepath.Join(link, "a.md"), e.Fields[FieldPath],
			"the link is not resolved, so no path inside it is predicted")
		assert.NotEqual(t, filepath.Join(real, "a.md"), e.Fields[FieldPath],
			"and certainly not a path inside what it points at")
	}
}

// TestExtractCommand_ATwoOperandCopyOntoADirectoryIsAlsoExpanded is the case
// that makes the ambiguity real rather than theoretical.
//
// `cp a.md target/` has TWO operands, the shape commandmod reads as a copy onto
// a path — and if target is a directory the result is target/a.md instead. Only
// the stat separates them, which is why both readings are stated.
func TestExtractCommand_ATwoOperandCopyOntoADirectoryIsAlsoExpanded(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	require.NoError(t, os.WriteFile(a, []byte("# A\n"), 0o644))
	target := filepath.Join(dir, "target")
	require.NoError(t, os.Mkdir(target, 0o755))

	events, err := extractFor(t, "cp "+a+" "+target)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreCreate, events[0].Kind)
	assert.Equal(t, filepath.Join(target, "a.md"), events[0].Fields[FieldPath],
		"the destination is INSIDE the directory, not the directory")
	assert.Equal(t, "# A\n", events[0].Fields[FieldNewContent])
}

// TestExtractCommand_ACopyOntoAFileIsNotExpanded is the other side of the same
// stat. With a regular file at the destination the two-operand reading is the
// right one, and the payload commandmod already attached stands.
func TestExtractCommand_ACopyOntoAFileIsNotExpanded(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	dst := filepath.Join(dir, "dest.md")
	require.NoError(t, os.WriteFile(a, []byte("# A\n"), 0o644))
	require.NoError(t, os.WriteFile(dst, []byte("replaced\n"), 0o644))

	events, err := extractFor(t, "cp "+a+" "+dst)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreUpdate, events[0].Kind)
	assert.Equal(t, dst, events[0].Fields[FieldPath])
	assert.Equal(t, true, events[0].Fields[FieldResultKnown])
	assert.Equal(t, "# A\n", events[0].Fields[FieldNewContent])
}

// TestExtractCommand_CopyingIntoADirectoryOverExistingFilesIsAnUpdate holds
// that an expanded target is classified by exactly the same code every other
// target is — the reason the expansion happens BEFORE the loop rather than
// inside it.
func TestExtractCommand_CopyingIntoADirectoryOverExistingFilesIsAnUpdate(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	require.NoError(t, os.WriteFile(a, []byte("# New\n"), 0o644))
	target := filepath.Join(dir, "target")
	require.NoError(t, os.Mkdir(target, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(target, "a.md"), []byte("# Old\n"), 0o644))

	events, err := extractFor(t, "cp "+a+" "+target)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreUpdate, events[0].Kind,
		"a file already inside the directory is replaced, not created")
	assert.Equal(t, true, events[0].Fields[FieldResultKnown])
	assert.Equal(t, "# New\n", events[0].Fields[FieldNewContent])
}

// TestExtractCommand_MovingIntoADirectoryStillRemovesItsSources is the half a
// destination expansion must not displace. `mv a.md target/` is a removal AND a
// create, and the removal is what a delete rule is about.
func TestExtractCommand_MovingIntoADirectoryStillRemovesItsSources(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.md")
	require.NoError(t, os.WriteFile(a, []byte("# A\n"), 0o644))
	target := filepath.Join(dir, "target")
	require.NoError(t, os.Mkdir(target, 0o755))

	events, err := extractFor(t, "mv "+a+" "+target)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		a:                             KindPreDelete,
		filepath.Join(target, "a.md"): KindPreCreate,
	}, kindsByPath(events))
}

// TestExtractCommand_DdWithAnInputFileCarriesItsBytes is dd's derivable tier at
// the module level: a copy spelled differently resolves the same way cp does.
func TestExtractCommand_DdWithAnInputFileCarriesItsBytes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.md")
	require.NoError(t, os.WriteFile(src, []byte("# Source\n"), 0o644))
	dst := filepath.Join(t.TempDir(), "out.md")

	events, err := extractFor(t, "dd if="+src+" of="+dst)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreCreate, events[0].Kind)
	assert.Equal(t, "# Source\n", events[0].Fields[FieldNewContent])
}

// TestExtractCommand_DdWithAPartialCopyIsNotDerivable is the boundary. A
// `count=` bounds the copy and a `conv=` transforms it, so the output is not
// the input's bytes and no content may be claimed.
func TestExtractCommand_DdWithAPartialCopyIsNotDerivable(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.md")
	require.NoError(t, os.WriteFile(src, []byte("# Source\n"), 0o644))

	for _, operand := range []string{"count=1", "skip=1", "seek=1", "conv=ucase", "iflag=direct"} {
		t.Run(operand, func(t *testing.T) {
			// An existing destination, so the event is an UPDATE and survives
			// to state resultKnown — a create would simply be withheld, which
			// would pass for the wrong reason.
			dst := filepath.Join(t.TempDir(), "out.md")
			require.NoError(t, os.WriteFile(dst, []byte("before\n"), 0o644))

			events, err := extractFor(t, "dd if="+src+" of="+dst+" "+operand)
			require.NoError(t, err)
			require.Len(t, events, 1)
			require.Equal(t, KindPreUpdate, events[0].Kind)
			assert.Equal(t, false, events[0].Fields[FieldResultKnown],
				operand+" makes the copy partial, so the result is not derivable")
		})
	}
}

// TestExtractCommand_LinkingDoesNotClaimTheTargetsBytes is the ln verdict at the
// module level, and it is the confidently-wrong direction being refused.
//
// A symlink's bytes are its target PATH. Reporting a.md's contents for
// `ln -s a.md b.md` would hand a rule text that is nowhere in the file.
func TestExtractCommand_LinkingDoesNotClaimTheTargetsBytes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.md")
	require.NoError(t, os.WriteFile(src, []byte("# Target body\n"), 0o644))
	link := filepath.Join(dir, "b.md")

	events, err := extractFor(t, "ln -s "+src+" "+link)
	require.NoError(t, err)
	assert.Empty(t, events,
		"the link's bytes are a path, not the target's content, so the create stays silent")
}

// TestExtractCommand_PrintfWidenedFormatsAreRenderedExactly is the widened
// printf tier, end to end. %d, several conversions, and the format-reuse loop
// are all exactly specified, so they are rendered rather than declined.
func TestExtractCommand_PrintfWidenedFormatsAreRenderedExactly(t *testing.T) {
	for name, tc := range map[string]struct {
		command string
		want    string
	}{
		"%d renders an integer as itself": {
			command: "printf '%%d items\\n' 5 > %s",
			want:    "5 items\n",
		},
		"several conversions consume left to right": {
			command: "printf '%%s=%%d\\n' k 7 > %s",
			want:    "k=7\n",
		},
		"surplus arguments reuse the format": {
			command: "printf '%%s\\n' a b c > %s",
			want:    "a\nb\nc\n",
		},
		"an exhausted pass finishes with empties": {
			command: "printf '%%s=%%s\\n' a > %s",
			want:    "a=\n",
		},
		"a literal percent consumes no argument": {
			command: "printf '100%%%%\\n' > %s",
			want:    "100%\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "created.md")
			events, err := extractFor(t, fmt.Sprintf(tc.command, path))
			require.NoError(t, err)
			require.Len(t, events, 1)
			require.Equal(t, KindPreCreate, events[0].Kind)
			assert.Equal(t, tc.want, events[0].Fields[FieldNewContent])
		})
	}
}

// TestExtractCommand_SedInPlaceIsAnUpdateWithNoDerivableResult is the shape this
// task deliberately left underivable, pinned in BOTH directions at once.
//
// The PATH half fires: `sed -i` names its file and the event happens, so a rule
// bound to PreFileUpdate on it is asked. The CONTENT half does not:
// `resultKnown` is false, which is the field that lets "not derivable" be SAID
// rather than silently rendered as an empty result.
//
// The reasoning, argued rather than assumed. Deriving the result means
// implementing sed's expression language — addresses, ranges, hold space,
// alternate delimiters, the `s` flags — against a file this package does not
// read, and being byte-exact or being worse than useless. The narrow subset that
// is genuinely safe (`s/literal/literal/`, no address, no flags, no regex
// metacharacter anywhere) is a small fraction of real usage, and the cost of
// misjudging the boundary is a `result` that is confidently WRONG rather than
// absent. A rule cannot tell a wrong answer from a right one, so an absent one
// is strictly better.
func TestExtractCommand_SedInPlaceIsAnUpdateWithNoDerivableResult(t *testing.T) {
	for _, command := range []string{
		"sed -i 's/a/b/' %s",
		"sed -i '' 's/a/b/' %s",
		"sed -i.bak 's/a/b/' %s",
		"perl -i -pe 's/a/b/' %s",
	} {
		path := filepath.Join(t.TempDir(), "existing.md")
		require.NoError(t, os.WriteFile(path, []byte("a line\n"), 0o644))

		events, err := extractFor(t, fmt.Sprintf(command, path))
		require.NoError(t, err, "command %q", command)
		require.Lenf(t, events, 1, "command %q still names its file", command)
		require.Equal(t, KindPreUpdate, events[0].Kind)
		assert.Equalf(t, false, events[0].Fields[FieldResultKnown],
			"command %q states a transformation, not an outcome", command)
	}
}

// TestExtractCommand_CopyingFromAnUnreadableSourceClaimsNothing is the failure
// direction of the copy tier, and it was found by mutation: removing the
// source's presence check left every other copy test green.
//
// The danger is specific. `cp missing.md dst.md` FAILS, so dst.md is never
// created — but os.ReadFile on a missing path returns ("", err), and a version
// that ignored the error would report a create carrying `content: ""`. That is
// the exact wrong this whole change removes, reintroduced at the other end of
// the pipeline: an empty string standing in for "not known", indistinguishable
// from `touch dst.md`.
func TestExtractCommand_CopyingFromAnUnreadableSourceClaimsNothing(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "destination.md")

	t.Run("a source that does not exist", func(t *testing.T) {
		events, err := extractFor(t, "cp "+filepath.Join(dir, "missing.md")+" "+dst)
		require.NoError(t, err)
		assert.Empty(t, events,
			"the copy fails, so nothing is created and no content may be claimed")
	})

	t.Run("a source that is a directory", func(t *testing.T) {
		events, err := extractFor(t, "cp "+dir+" "+dst)
		require.NoError(t, err)
		assert.Empty(t, events, "a directory's bytes are not a file's content")
	})
}

// TestExtractCommand_AppendingCarriesTheWholeResultingFile is the append tier.
//
// `echo x >> log.md` is an UPDATE, not a create, and its result is the file's
// current bytes plus the new text. That needs the disk (the base) and the line
// (the tail), so it is the clearest case for the split.
func TestExtractCommand_AppendingCarriesTheWholeResultingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.md")
	require.NoError(t, os.WriteFile(path, []byte("first line\n"), 0o644))

	events, err := extractFor(t, "echo second line >> "+path)
	require.NoError(t, err)
	require.Len(t, events, 1)

	e := events[0]
	require.Equal(t, KindPreUpdate, e.Kind, "appending to an existing file is an update")
	assert.Equal(t, true, e.Fields[FieldResultKnown])
	assert.Equal(t, "first line\nsecond line\n", e.Fields[FieldNewContent],
		"the whole resulting file: what is there now, plus what is being added")
}

// TestExtractCommand_AppendingToAnAbsentFileCreatesItWithJustTheNewText holds
// the other side of the append: with no file to append to, `>>` creates one
// holding only the new bytes.
func TestExtractCommand_AppendingToAnAbsentFileCreatesItWithJustTheNewText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new-log.md")

	events, err := extractFor(t, "echo first >> "+path)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreCreate, events[0].Kind)
	assert.Equal(t, "first\n", events[0].Fields[FieldNewContent])
}

// TestExtractCommand_TruncatingAnExistingFileIsAKnownEmptyResult pins the
// update side of the truly-empty cases. These are the commands for which an
// empty result is a FACT rather than a stand-in for ignorance, which is exactly
// the distinction resultKnown exists to carry.
func TestExtractCommand_TruncatingAnExistingFileIsAKnownEmptyResult(t *testing.T) {
	for name, command := range map[string]string{
		"a bare truncating redirection": ": > %s",
		"truncate -s 0":                 "truncate -s 0 %s",
		"truncate -s0":                  "truncate -s0 %s",
		"truncate --size=0":             "truncate --size=0 %s",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "existing.md")
			require.NoError(t, os.WriteFile(path, []byte("had content\n"), 0o644))

			events, err := extractFor(t, fmt.Sprintf(command, path))
			require.NoError(t, err)
			require.Len(t, events, 1)
			require.Equal(t, KindPreUpdate, events[0].Kind)
			assert.Equal(t, true, events[0].Fields[FieldResultKnown],
				"emptying a file is a known outcome, not an unknown one")
			assert.Equal(t, "", events[0].Fields[FieldNewContent])
		})
	}
}

// TestExtractCommand_ATruncateToANonZeroSizeIsNotDerivable is the boundary of
// the case above. Any size but zero depends on the file's current bytes and
// length — padding with NULs, or cutting at an offset — so it is declined.
func TestExtractCommand_ATruncateToANonZeroSizeIsNotDerivable(t *testing.T) {
	for _, size := range []string{"100", "+10", "-10"} {
		path := filepath.Join(t.TempDir(), "existing.md")
		require.NoError(t, os.WriteFile(path, []byte("had content\n"), 0o644))

		events, err := extractFor(t, "truncate -s "+size+" "+path)
		require.NoError(t, err)
		require.Len(t, events, 1, "size %q", size)
		assert.Equal(t, false, events[0].Fields[FieldResultKnown],
			"size %q depends on the file's current bytes", size)
	}
}

// TestExtractCommand_TouchingAnExistingFileDoesNotClaimToEmptyIt is the case
// that would be a silent disaster if the payload were applied blindly.
//
// commandmod reports `touch` as a literal empty payload, because that is what
// the line determines FOR A CREATE. Against a file that already exists, touch
// changes only the mtime and leaves every byte alone — so claiming a result of
// "" would tell a rule the file was being emptied, and a guardrail refusing
// empty files would fire on `touch existing.md`.
//
// The split is what prevents it: commandmod says what the line determines, and
// filemod decides what the TREE makes of it. This test is that decision.
func TestExtractCommand_TouchingAnExistingFileDoesNotClaimToEmptyIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.md")
	require.NoError(t, os.WriteFile(path, []byte("untouched body\n"), 0o644))

	events, err := extractFor(t, "touch "+path)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreUpdate, events[0].Kind)
	assert.NotEqual(t, "", events[0].Fields[FieldNewContent],
		"touch does not empty a file, and must never be reported as though it does")
	assert.Equal(t, "untouched body\n", events[0].Fields[FieldNewContent],
		"the bytes are unchanged, which is itself a known result")
}

// TestExtractCommand_RemovingWhatIsNotThereIsNotADeletion pins the other
// absent-path direction.
//
// `rm gone.md` deletes no file. Announcing a PreFileDelete would fire a rule on
// a file that was never at risk, and a refusal would block a command that would
// have changed nothing.
func TestExtractCommand_RemovingWhatIsNotThereIsNotADeletion(t *testing.T) {
	events, err := extractFor(t, "rm "+filepath.Join(t.TempDir(), "gone.md"))
	require.NoError(t, err)
	assert.Empty(t, events)
}

// TestExtractCommand_ADirectoryIsNotAFile mirrors the tool-write path's own
// answer for the same condition.
//
// Something is at the path and it is not a file, so no file event can honestly
// be about it — the same judgement presentNotAFile encodes for a write aimed at
// a directory. `rm -rf somedir` produces nothing rather than a PreFileDelete
// naming a directory.
func TestExtractCommand_ADirectoryIsNotAFile(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "adir")
	require.NoError(t, os.Mkdir(sub, 0o755))

	for _, command := range []string{"rm -rf " + sub, "mkdir " + sub, "echo x > " + sub} {
		events, err := extractFor(t, command)
		require.NoError(t, err, "command %q", command)
		assert.Emptyf(t, events, "command %q names a directory, and no file event can be about one", command)
	}
}

// TestExtractCommand_OnlyARecognisedCommandToolIsRead pins the CORRECTED,
// current contract: dispatch to the command shape is gated on
// commandmod.HarnessCommandTools first, by tool name, with no shape
// fallback. This test used to assert the opposite (name never consulted);
// see commandmod/harnesstools.go for the argument behind the reversal.
func TestExtractCommand_OnlyARecognisedCommandToolIsRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")

	args, _ := json.Marshal(map[string]string{"command": "rm " + path})

	t.Run("Bash is on the list", func(t *testing.T) {
		require.NoError(t, os.WriteFile(path, []byte("body\n"), 0o644))
		events, err := New().Extract(module.Input{
			module.InputPhase:   module.PhasePre,
			module.InputPayload: fakePending{tool: "Bash", args: args},
		})
		require.NoError(t, err)
		require.Len(t, events, 1, "Bash is on HarnessCommandTools and must be read")
		assert.Equal(t, KindPreDelete, events[0].Kind)
	})

	t.Run("an unrecognised tool is not, whatever it carries", func(t *testing.T) {
		require.NoError(t, os.WriteFile(path, []byte("body\n"), 0o644))
		for _, tool := range []string{"Shell", "RunCommand", "", "SomeFutureShellTool"} {
			events, err := New().Extract(module.Input{
				module.InputPhase:   module.PhasePre,
				module.InputPayload: fakePending{tool: tool, args: args},
			})
			require.NoError(t, err, "tool %q", tool)
			assert.Emptyf(t, events, "tool %q is not on HarnessCommandTools, so its command line is not read", tool)
		}
	})
}

// TestExtractCommand_ArgumentsWithNeitherShapeProduceNothing pins that adding
// the command shape did not make this module answer for payloads it has no
// business reading.
func TestExtractCommand_ArgumentsWithNeitherShapeProduceNothing(t *testing.T) {
	for name, args := range map[string]string{
		"grep":           `{"pattern":"foo","path":"/a"}`,
		"empty command":  `{"command":""}`,
		"not json":       `not json at all`,
		"empty object":   `{}`,
		"command is num": `{"command":42}`,
	} {
		events, err := New().Extract(module.Input{
			module.InputPhase:   module.PhasePre,
			module.InputPayload: fakePending{tool: "Whatever", args: json.RawMessage(args)},
		})
		require.NoError(t, err, "case %q", name)
		assert.Emptyf(t, events, "case %q names no file and carries no command", name)
	}
}

// TestExtractCommand_AWrapperDoesNotHideTheDeletion pins that the evasion
// commandmod's unwrapping already prevents for invocations is prevented for
// files too. A rule evadable by typing `sudo` is a suggestion.
func TestExtractCommand_AWrapperDoesNotHideTheDeletion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")

	for _, command := range []string{
		"sudo rm " + path,
		"env FOO=1 rm " + path,
		"timeout 5 rm " + path,
		"echo hi && rm " + path,
		"(rm " + path + ")",
	} {
		require.NoError(t, os.WriteFile(path, []byte("body\n"), 0o644))
		events, err := extractFor(t, command)
		require.NoError(t, err, "command %q", command)
		require.Lenf(t, events, 1, "command %q must still name the file", command)
		assert.Equal(t, KindPreDelete, events[0].Kind, "command %q", command)
	}
}

// TestExtractCommand_OneFileIsOneEvent pins the deduplication.
//
// A rule should be asked once about a file. Asking twice runs a judging hook
// twice over one decision, and a judge hook is a model call rather than a
// function — so a repeated path is a cost as well as a wrong.
func TestExtractCommand_OneFileIsOneEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	require.NoError(t, os.WriteFile(path, []byte("body\n"), 0o644))

	events, err := extractFor(t, "rm "+path+" "+path)
	require.NoError(t, err)
	assert.Len(t, events, 1, "one file named twice is one event")
}

// TestExtractCommand_OneFileIsOneEventAcrossSPELLINGS is the same contract held
// to the case the test above cannot see.
//
// The dedup was keyed on the RAW path while its sibling in extractObserved keyed
// on the canonical one — the half-fix shape. Two spellings of one file therefore
// survived as two targets and collapsed into two IDENTICAL events, which is
// exactly what the loop's own comment forbids: "a rule should be asked once
// about a file, and asking twice would run a judging hook twice over one
// decision". A judging hook is a model call, and an author reads one file
// refused twice for one reason.
//
// The test above used the same spelling twice, which the raw key already caught,
// so the contract read as pinned while three other spellings of it were not.
func TestExtractCommand_OneFileIsOneEventAcrossSpellings(t *testing.T) {
	cases := []struct {
		name string
		// %s is replaced by the workspace root, so a case can mix an absolute
		// spelling with a relative one for the same file.
		command string
	}{
		{"bare and dot-slash", "printf x > notes.md; printf y > ./notes.md"},
		{"bare and absolute", "printf x > notes.md; printf y > %s/notes.md"},
		{"dot-slash and absolute", "printf x > ./notes.md; printf y > %s/notes.md"},
		{"touch, bare and dot-slash", "touch notes.md; touch ./notes.md"},
		{"redundant separators", "touch notes.md; touch .//notes.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			command := tc.command
			if strings.Contains(command, "%s") {
				command = fmt.Sprintf(command, dir)
			}

			events, err := extractForIn(t, command, dir)
			require.NoError(t, err)
			require.Len(t, events, 1,
				"two spellings of one file are one event, not two: %v", kindsByPath(events))
			assert.Equal(t, "notes.md", events[0].Fields[FieldPath],
				"and it is reported under the workspace-relative spelling")
		})
	}
}

// TestExtractCommand_AnUpdateCarriesTheMarkersOnDisk pins that a command-derived
// update is the same event a tool-derived one is.
//
// PreFileUpdate declares `oldMarkers`, and an event missing a declared field
// makes a matcher reading it ERROR — which refuses the action and blames the
// author's rule for the engine's gap. So the command path must fill it exactly
// as the tool path does. `sed -i` states a transformation, not an outcome, so
// newMarkers is empty; oldMarkers describe the bytes on disk being replaced.
func TestExtractCommand_AnUpdateCarriesTheMarkersOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.md")
	require.NoError(t, os.WriteFile(path, []byte("# sr:doc thing.one\nbody\n"), 0o644))

	events, err := extractFor(t, "sed -i '' s/a/b/ "+path)
	require.NoError(t, err)
	require.Len(t, events, 1)

	require.Equal(t, KindPreUpdate, events[0].Kind)
	markers, ok := events[0].Fields[FieldOldMarkers].([]any)
	require.Truef(t, ok, "oldMarkers must be present and a list, got %T", events[0].Fields[FieldOldMarkers])
	require.Len(t, markers, 1)
	entry, ok := markers[0].(map[string]any)
	require.True(t, ok, "a marker is carried as a map at the boundary")
	assert.Equal(t, "thing.one", entry[KeyMarkerFQN])
}

// TestExtractCommand_ADeletionCarriesNoResult is the complement, and it is a
// declaration question rather than a value one.
//
// PreFileDelete declares `path`, `oldContent` and `oldMarkers` — the bytes about
// to be lost and their markers, but nothing about a result. An event carrying
// `newContent` or `newMarkers` would carry a field nothing declares, a field no
// matcher can be checked against, because CompileMatcherFor validates against the
// declaration and refuses the name.
func TestExtractCommand_ADeletionCarriesNoResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	require.NoError(t, os.WriteFile(path, []byte("# sr:doc thing.one\n"), 0o644))

	events, err := extractFor(t, "rm "+path)
	require.NoError(t, err)
	require.Len(t, events, 1)

	assert.NotContains(t, events[0].Fields, FieldNewMarkers, "a delete leaves nothing to scan for a result's markers")
	assert.NotContains(t, events[0].Fields, FieldNewContent, "a delete leaves no result")
	// It DOES carry the bytes about to be lost, read off the file on disk.
	assert.Equal(t, "# sr:doc thing.one\n", events[0].Fields[FieldOldContent],
		"oldContent is the file the command is about to remove")
	require.Contains(t, events[0].Fields, FieldOldMarkers)
	oldMarkers, ok := events[0].Fields[FieldOldMarkers].([]any)
	require.True(t, ok)
	require.Len(t, oldMarkers, 1, "the markers of the bytes about to be lost")
	assert.Equal(t, "thing.one", oldMarkers[0].(map[string]any)[KeyMarkerFQN])
	assert.Equal(t, path, events[0].Fields[FieldPath])
}

// TestExtractCommand_TheOtherPathsSurviveOneBadOne pins the module contract the
// package doc states: events go out ALONGSIDE an error, because one path the
// machine could not answer for is not a reason to withhold the classification
// of the others.
//
// The unreadable path is produced with a directory that cannot be traversed, so
// the stat fails for a reason that is not "not there" — which is what `unknown`
// means, and what must be reported rather than folded into absent. Folding it
// would announce a deletion of a file nobody could see.
func TestExtractCommand_TheOtherPathsSurviveOneBadOne(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root traverses a 0000 directory, so the stat cannot be made to fail this way")
	}
	dir := t.TempDir()

	good := filepath.Join(dir, "notes.md")
	require.NoError(t, os.WriteFile(good, []byte("body\n"), 0o644))

	locked := filepath.Join(dir, "locked")
	require.NoError(t, os.Mkdir(locked, 0o755))
	hidden := filepath.Join(locked, "inside.md")
	require.NoError(t, os.WriteFile(hidden, []byte("body\n"), 0o644))
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	events, err := extractFor(t, "rm "+good+" "+hidden)

	require.Error(t, err, "a stat that could not answer must be reported, never swallowed")
	require.Len(t, events, 1, "the readable path is still classified")
	assert.Equal(t, good, events[0].Fields[FieldPath])
	assert.Equal(t, KindPreDelete, events[0].Kind)
}

// TestExtractCommand_ThePostPhaseIgnoresACommand pins that a command line
// reaches the prediction and nothing else.
//
// What a command turned out to change is established by diffing the tree, which
// sees the file gone whatever removed it. Reading the command line again in the
// post phase would be inferring from a string where an observation is available,
// and the two could disagree.
func TestExtractCommand_ThePostPhaseIgnoresACommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	require.NoError(t, os.WriteFile(path, []byte("body\n"), 0o644))

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePost,
		module.InputPayload: bashPending("rm " + path),
	})
	require.NoError(t, err)
	assert.Empty(t, events, "the post phase observes the tree; it does not re-read command lines")
}

// bashPendingIn is a shell payload that knows which workspace it is running in.
//
// bashPending leaves Root empty, which is the "no workspace named" case — and
// under that case reportable leaves every path exactly as the command spelled
// it, which is why every test above still asserts absolute paths. A real session
// always names a root, and that is the case these two cover.
func bashPendingIn(command, root string) fakePending {
	p := bashPending(command)
	p.root = root
	return p
}

func extractForIn(t *testing.T, command, root string) ([]event.Event, error) {
	t.Helper()
	return New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: bashPendingIn(command, root),
	})
}

// TestExtractCommand_AnAbsolutePathInsideTheWorkspaceIsReportedRelative is the
// hole this pins, and it was a hole rather than an inconsistency.
//
// A matcher is a prefix test over the reported path, and the only spelling a
// rule's author can write is the workspace-relative one — they do not know where
// the repository will be checked out. The tool-write path has reported that
// spelling since Root existed. The COMMAND path did not: it reported whatever
// the command line said.
//
// So one write had two answers. `printf x > memories/a.md` was admitted by
// `path startsWith "memories/"` and refused; `printf x > <root>/memories/a.md`
// was reported absolute, admitted by nothing, and permitted. Measured end to end
// against a real project's guardrails before the fix — the same write, refused
// or permitted according to how one argument was spelled, which is a bypass for
// every Pre-kind rule narrowed on a folder.
//
// Both kinds are asserted because they are two separate constructions in the
// source and a fix to one leaves the other wrong.
func TestExtractCommand_AnAbsolutePathInsideTheWorkspaceIsReportedRelative(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "memories", "notes.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(existing), 0o755))
	require.NoError(t, os.WriteFile(existing, []byte("body\n"), 0o644))

	t.Run("a create", func(t *testing.T) {
		events, err := extractForIn(t, "printf x > "+filepath.Join(dir, "memories", "new.md"), dir)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, KindPreCreate, events[0].Kind)
		assert.Equal(t, "memories/new.md", events[0].Fields[FieldPath],
			"an absolute path inside the workspace must reach a matcher as the project's own spelling")
	})

	t.Run("an update", func(t *testing.T) {
		events, err := extractForIn(t, "printf x >> "+existing, dir)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, KindPreUpdate, events[0].Kind)
		assert.Equal(t, "memories/notes.md", events[0].Fields[FieldPath])
	})

	t.Run("a delete", func(t *testing.T) {
		events, err := extractForIn(t, "rm "+existing, dir)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, KindPreDelete, events[0].Kind)
		assert.Equal(t, "memories/notes.md", events[0].Fields[FieldPath])
	})
}

// TestExtractCommand_APathOutsideTheWorkspaceKeepsItsAbsoluteSpelling is the
// other half, and it is what stops the fix above from becoming its own hole.
//
// filepath.Rel would happily answer "../../etc/passwd" for a path outside the
// tree, and a matcher is a prefix test: a rule written for a folder in the
// project must never be handed a spelling that could climb into one. Leaving it
// absolute means no project-relative matcher admits it, which is the honest
// answer — the write is outside the rule's subject.
func TestExtractCommand_APathOutsideTheWorkspaceKeepsItsAbsoluteSpelling(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	require.NoError(t, os.WriteFile(outside, []byte("body\n"), 0o644))

	events, err := extractForIn(t, "rm "+outside, root)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, outside, events[0].Fields[FieldPath],
		"a path outside the workspace must not be given a relative spelling a project matcher could admit")
}
