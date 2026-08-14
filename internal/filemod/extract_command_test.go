package filemod

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
		// printf with a format this does not model exactly.
		"printf '%d items' 5 > " + absent,
		// cp from a source that is not there: the command fails, so nothing is
		// created and nothing may be claimed.
		"cp " + filepath.Join(dir, "missing-source.md") + " " + absent,
		// A copy INTO a directory, where the resulting filename is
		// `dir/base(src)` — a path this deliberately does not compute.
		"cp /etc/hosts " + dir,
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
			assert.Equal(t, tc.want, events[0].Fields[FieldContent],
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
			assert.Equal(t, "# Source body\n", create.Fields[FieldContent],
				"the destination's bytes are the source's current bytes")
		})
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
	assert.Equal(t, "first line\nsecond line\n", e.Fields[FieldResult],
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
	assert.Equal(t, "first\n", events[0].Fields[FieldContent])
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
			assert.Equal(t, "", events[0].Fields[FieldResult])
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
	assert.NotEqual(t, "", events[0].Fields[FieldResult],
		"touch does not empty a file, and must never be reported as though it does")
	assert.Equal(t, "untouched body\n", events[0].Fields[FieldResult],
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

// TestExtractCommand_TheToolNameIsNotConsulted holds the rule extractPending
// argues at length: dispatch is on the SHAPE of the arguments and never on the
// name.
//
// A harness that renames its shell tool, or ships a second one, must not
// silently stop being watched — which is the exact failure that broke every
// module asking for `Task` after it became `Agent`.
func TestExtractCommand_TheToolNameIsNotConsulted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	require.NoError(t, os.WriteFile(path, []byte("body\n"), 0o644))

	for _, tool := range []string{"Bash", "Shell", "RunCommand", "", "SomeFutureName"} {
		args, _ := json.Marshal(map[string]string{"command": "rm " + path})
		events, err := New().Extract(module.Input{
			module.InputPhase:   module.PhasePre,
			module.InputPayload: fakePending{tool: tool, args: args},
		})
		require.NoError(t, err, "tool %q", tool)
		require.Lenf(t, events, 1, "tool %q carries a command line and must be read", tool)
		assert.Equal(t, KindPreDelete, events[0].Kind)
	}
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

// TestExtractCommand_AnUpdateCarriesTheMarkersOnDisk pins that a command-derived
// update is the same event a tool-derived one is.
//
// PreFileUpdate declares `markers`, and an event missing a declared field makes
// a matcher reading it ERROR — which refuses the action and blames the author's
// rule for the engine's gap. So the command path must fill it exactly as the
// tool path does.
func TestExtractCommand_AnUpdateCarriesTheMarkersOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.md")
	require.NoError(t, os.WriteFile(path, []byte("# sr:doc thing.one\nbody\n"), 0o644))

	events, err := extractFor(t, "sed -i '' s/a/b/ "+path)
	require.NoError(t, err)
	require.Len(t, events, 1)

	require.Equal(t, KindPreUpdate, events[0].Kind)
	markers, ok := events[0].Fields[FieldMarkers].([]any)
	require.Truef(t, ok, "markers must be present and a list, got %T", events[0].Fields[FieldMarkers])
	require.Len(t, markers, 1)
	entry, ok := markers[0].(map[string]any)
	require.True(t, ok, "a marker is carried as a map at the boundary")
	assert.Equal(t, "thing.one", entry[KeyMarkerFQN])
}

// TestExtractCommand_ADeletionCarriesNoMarkers is the complement, and it is a
// declaration question rather than a value one.
//
// PreFileDelete declares `path` alone, so an event carrying `markers` would
// carry a field nothing declares — a field no matcher can be checked against,
// because CompileMatcherFor validates against the declaration and refuses the
// name.
func TestExtractCommand_ADeletionCarriesNoMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	require.NoError(t, os.WriteFile(path, []byte("# sr:doc thing.one\n"), 0o644))

	events, err := extractFor(t, "rm "+path)
	require.NoError(t, err)
	require.Len(t, events, 1)

	assert.NotContains(t, events[0].Fields, FieldMarkers, "a delete has no text to read markers from")
	assert.NotContains(t, events[0].Fields, FieldContent, "a delete has no content")
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
