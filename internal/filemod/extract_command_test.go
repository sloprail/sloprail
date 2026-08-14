package filemod

import (
	"encoding/json"
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

// TestExtractCommand_ACreationIsNotPredicted is the spec decision, held as a
// test.
//
// A command aimed at a path that does not exist yet is a CREATION, and
// PreFileCreate requires `content` — the file cannot be read off disk, so the
// event carries what would be written. A command line does not say what bytes
// will result.
//
// Sending `content: ""` would make this indistinguishable from a tool writing a
// genuinely empty file, and `content == ""` is exactly the rule an author writes
// to catch that. So nothing is emitted, and the creation is reported after the
// fact as PostFileCreate off the tree diff.
//
// This is a real limit and the test states it as one rather than as a
// preference. If a later change gives PreFileCreate a way to say "content
// unknown", this is the test that should change.
func TestExtractCommand_ACreationIsNotPredicted(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "not-there-yet.md")

	for _, command := range []string{
		"echo x > " + absent,
		"echo x >> " + absent,
		"touch " + absent,
		"tee " + absent,
		"cp /etc/hosts " + absent,
	} {
		events, err := extractFor(t, command)
		require.NoError(t, err, "command %q", command)
		assert.Emptyf(t, events, "command %q creates a file, and a creation cannot be predicted with content", command)
	}
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
