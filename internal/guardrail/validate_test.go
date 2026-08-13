package guardrail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// testModule declares the kinds these tests validate against, rather than
// importing filemod. The point of the registry is that the engine works from
// declarations rather than from anything compiled into it, and a test that
// borrowed a real module's kinds would stop proving that.
type testModule struct{}

func (testModule) Name() string { return "test" }

func (testModule) Kinds() []module.KindDecl {
	return []module.KindDecl{fileKind, commandKind}
}

func (testModule) Extract(module.Input) ([]event.Event, error) { return nil, nil }

func testRegistry(t *testing.T) *module.Registry {
	t.Helper()
	reg, err := module.NewRegistry(testModule{})
	require.NoError(t, err)
	return reg
}

// scriptDir returns a directory holding an executable hook, a non-executable
// one, and a subdirectory — the three things a command can turn out to be.
func scriptDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.sh"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plain.sh"), []byte("#!/bin/sh\n"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "adir"), 0o755))
	return dir
}

// okHook is a hook that nothing should object to.
func okHook() Hook { return Hook{Type: HookCommand, Command: "./ok.sh"} }

// declWith builds a declaration in dir binding one kind to one binding.
func declWith(dir, kind, matcher string, hooks ...Hook) Declaration {
	return Declaration{
		Name:  "test-guardrail",
		Dir:   dir,
		Hooks: map[string][]Binding{kind: {{Matcher: matcher, Hooks: hooks}}},
	}
}

// onlyProblem asserts a single problem of the given kind and returns it, so a
// test names the fault it means rather than a fragment of its wording.
func onlyProblem(t *testing.T, problems []Problem, kind error) Problem {
	t.Helper()
	require.Len(t, problems, 1)
	assert.ErrorIs(t, problems[0], kind)
	return problems[0]
}

// The acceptance case. Everything below rejects something; without this one a
// validator that refused every declaration would pass the whole file.
func TestValidate_AcceptsASoundDeclaration(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "PreFileCreate", `path startsWith "guarded/"`, okHook())

	assert.Empty(t, Validate(d, testRegistry(t)))
}

func TestValidate_AcceptsAbsentMatcher(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "PreFileCreate", "", okHook())

	assert.Empty(t, Validate(d, testRegistry(t)), "no matcher means every occurrence")
}

// A declaration binding nothing enforces nothing, which is a legitimate thing
// to have written down.
func TestValidate_AcceptsDeclarationBindingNothing(t *testing.T) {
	assert.Empty(t, Validate(Declaration{Name: "inert"}, testRegistry(t)))
}

// Turning a rule off is a declaration rather than a deletion, so that a rule
// can be parked without the reasoning behind it being thrown away. Reporting a
// parked rule at every session start would make parking one cost a warning
// forever, and the only way to silence it would be the deletion `enabled:
// false` exists to avoid.
func TestValidate_SkipsDisabledDeclaration(t *testing.T) {
	off := false
	d := declWith(scriptDir(t), "NoSuchKind", `pth startsWith "x"`, Hook{Type: "script"})
	d.Enabled = &off

	assert.Empty(t, Validate(d, testRegistry(t)), "a rule switched off need not be correct")
}

// ...and the same declaration switched back on is reported, so the silence
// above is the enabled flag doing its job rather than the checks going missing.
func TestValidate_ValidatesTheSameDeclarationWhenEnabled(t *testing.T) {
	on := true
	d := declWith(scriptDir(t), "NoSuchKind", `pth startsWith "x"`, Hook{Type: "script"})
	d.Enabled = &on

	assert.NotEmpty(t, Validate(d, testRegistry(t)))
}

// Absent means enabled: a declaration that says nothing about being switched
// off is switched on, and must still be checked.
func TestValidate_AbsentEnabledIsStillValidated(t *testing.T) {
	d := declWith(scriptDir(t), "NoSuchKind", "", okHook())
	require.Nil(t, d.Enabled)

	assert.NotEmpty(t, Validate(d, testRegistry(t)))
}

func TestValidate_UnknownEventKind(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "PreFileCreat", "", okHook())

	p := onlyProblem(t, Validate(d, testRegistry(t)), ErrUnknownEventKind)
	assert.Contains(t, p.Message(), "PreFileCreat")
	// The kinds that do exist, so the author can see the one they meant.
	assert.Contains(t, p.Message(), "PreFileCreate")
}

// A build with no modules is the engine's fault, not the declaration's, and
// should read that way rather than as an empty list of alternatives.
func TestValidate_RegistryWithNoKinds(t *testing.T) {
	empty, err := module.NewRegistry()
	require.NoError(t, err)

	p := onlyProblem(t, Validate(declWith(scriptDir(t), "PreFileCreate", "", okHook()), empty), ErrUnknownEventKind)
	assert.Contains(t, p.Message(), "no events at all")
}

// A matcher cannot be checked against a kind that does not exist. Reporting it
// too would bury the one fault that explains both.
func TestValidate_UnknownKindDoesNotAlsoBlameItsMatcher(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "NoSuchKind", `pth startsWith "x"`, okHook())

	onlyProblem(t, Validate(d, testRegistry(t)), ErrUnknownEventKind)
}

func TestValidate_MatcherNamingAFieldTheKindLacks(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "PreFileCreate", `pth startsWith "guarded/"`, okHook())

	p := onlyProblem(t, Validate(d, testRegistry(t)), ErrBadMatcher)
	assert.Contains(t, p.Message(), "pth")
	// What the kind does carry, which is what turns the report into a fix.
	assert.Contains(t, p.Message(), "path")
	assert.NotContains(t, p.Message(), "\n", "expr's underline is unreadable mid-sentence")
}

func TestValidate_HookTypeNotUnderstood(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "PreFileCreate", "", Hook{Type: "script", Command: "./ok.sh"})

	p := onlyProblem(t, Validate(d, testRegistry(t)), ErrBadHookType)
	assert.Contains(t, p.Message(), "script")
	assert.Contains(t, p.Message(), HookCommand)
}

func TestValidate_HookTypeMissing(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "PreFileCreate", "", Hook{Command: "./ok.sh"})

	p := onlyProblem(t, Validate(d, testRegistry(t)), ErrBadHookType)
	assert.Contains(t, p.Message(), "no type")
}

func TestValidate_HookCommandMissing(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "PreFileCreate", "", Hook{Type: HookCommand})

	p := onlyProblem(t, Validate(d, testRegistry(t)), ErrBadHookCommand)
	assert.Contains(t, p.Message(), "no command")
}

func TestValidate_HookCommandDoesNotExist(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "PreFileCreate", "", Hook{Type: HookCommand, Command: "./missing.sh"})

	p := onlyProblem(t, Validate(d, testRegistry(t)), ErrBadHookCommand)
	assert.Contains(t, p.Message(), "missing.sh")
	assert.Contains(t, p.Message(), "no such file")
}

func TestValidate_HookCommandNotExecutable(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "PreFileCreate", "", Hook{Type: HookCommand, Command: "./plain.sh"})

	p := onlyProblem(t, Validate(d, testRegistry(t)), ErrBadHookCommand)
	assert.Contains(t, p.Message(), "not executable")
}

func TestValidate_HookCommandIsADirectory(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "PreFileCreate", "", Hook{Type: HookCommand, Command: "./adir"})

	p := onlyProblem(t, Validate(d, testRegistry(t)), ErrBadHookCommand)
	assert.Contains(t, p.Message(), "is a directory")
}

// A command line with arguments is still resolved by its first word.
func TestValidate_HookCommandWithArguments(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "PreFileCreate", "", Hook{Type: HookCommand, Command: "./ok.sh --strict"})

	assert.Empty(t, Validate(d, testRegistry(t)))
}

// A command line the engine runs through `sh -c` may name something PATH
// resolves, or be a pipeline, or expand a variable. None of those are ours to
// have an opinion on, and refusing one would break a working guardrail.
func TestValidate_LeavesNonPathCommandsAlone(t *testing.T) {
	dir := scriptDir(t)
	for _, command := range []string{
		"python3 check.py",
		"grep -q TODO",
		"cat | ./ok.sh",
		`"$GUARDRAIL_DIR"/check.sh`,
		"jq -e .ok",
	} {
		d := declWith(dir, "PreFileCreate", "", Hook{Type: HookCommand, Command: command})
		assert.Emptyf(t, Validate(d, testRegistry(t)), "command %q should not be judged", command)
	}
}

func TestValidate_BindingWithNoHooks(t *testing.T) {
	dir := scriptDir(t)
	d := declWith(dir, "PreFileCreate", `path startsWith "x"`)

	p := onlyProblem(t, Validate(d, testRegistry(t)), ErrNoHooks)
	assert.Contains(t, p.Message(), "no hooks")
}

// The property the task asks for: every problem, not the first. Four faults
// across two kinds, and an author who fixes them should need one reload.
func TestValidate_ReportsEveryProblem(t *testing.T) {
	dir := scriptDir(t)
	d := Declaration{
		Name: "many-faults",
		Dir:  dir,
		Hooks: map[string][]Binding{
			"PreFileCreate": {
				{Matcher: `pth startsWith "a/"`, Hooks: []Hook{okHook()}},
				{Matcher: `path startsWith "b/"`, Hooks: []Hook{{Type: "script", Command: "./ok.sh"}}},
			},
			"NoSuchKind": {{Hooks: []Hook{okHook()}}},
			"PreCommand": {{Matcher: `raw startsWith "npm"`, Hooks: []Hook{{Type: HookCommand, Command: "./missing.sh"}}}},
		},
	}

	problems := Validate(d, testRegistry(t))
	assert.Len(t, problems, 4)

	// One of each kind, named rather than matched on wording.
	for _, kind := range []error{ErrBadMatcher, ErrBadHookType, ErrUnknownEventKind, ErrBadHookCommand} {
		assert.Truef(t, hasKind(problems, kind), "expected a %v among the problems", kind)
	}
}

func hasKind(problems []Problem, kind error) bool {
	for _, p := range problems {
		if p.Is(kind) {
			return true
		}
	}
	return false
}

// Two runs over one declaration must report in the same order, or a diff of
// two reports is noise. Map iteration would not.
func TestValidate_OrderIsStable(t *testing.T) {
	dir := scriptDir(t)
	d := Declaration{
		Name: "many-faults",
		Dir:  dir,
		Hooks: map[string][]Binding{
			"PreFileCreate": {{Matcher: `aaa == "x"`, Hooks: []Hook{okHook()}}},
			"PreCommand":    {{Matcher: `bbb == "x"`, Hooks: []Hook{okHook()}}},
			"NoSuchKind":    {{Hooks: []Hook{okHook()}}},
		},
	}

	first := Validate(d, testRegistry(t))
	for i := 0; i < 8; i++ {
		assert.Equal(t, first, Validate(d, testRegistry(t)))
	}
}

// Each problem says which binding it is about. With several bindings under one
// kind, a report that did not would leave the author to guess.
func TestValidate_NamesTheBindingAtFault(t *testing.T) {
	dir := scriptDir(t)
	d := Declaration{
		Name: "two-bindings",
		Dir:  dir,
		Hooks: map[string][]Binding{
			"PreFileCreate": {
				{Matcher: `path startsWith "a/"`, Hooks: []Hook{okHook()}},
				{Matcher: `pth startsWith "b/"`, Hooks: []Hook{okHook()}},
			},
		},
	}

	p := onlyProblem(t, Validate(d, testRegistry(t)), ErrBadMatcher)
	assert.Equal(t, 1, p.Binding, "the second binding is the faulty one")
	assert.Equal(t, "PreFileCreate", p.Event)
	assert.Contains(t, p.Message(), "binding 1")
}

// A hook problem locates the hook as well as the binding.
func TestValidate_NamesTheHookAtFault(t *testing.T) {
	dir := scriptDir(t)
	d := Declaration{
		Name: "two-hooks",
		Dir:  dir,
		Hooks: map[string][]Binding{
			"PreFileCreate": {{Hooks: []Hook{okHook(), {Type: "script", Command: "./ok.sh"}}}},
		},
	}

	p := onlyProblem(t, Validate(d, testRegistry(t)), ErrBadHookType)
	assert.Equal(t, 1, p.Hook)
	assert.Contains(t, p.Message(), "hook 1")
}

// Kinds are what a caller branches on, so they must be distinguishable rather
// than all collapsing to one sentinel.
func TestProblem_KindsAreDistinct(t *testing.T) {
	dir := scriptDir(t)
	matcher := Validate(declWith(dir, "PreFileCreate", `pth == "x"`, okHook()), testRegistry(t))
	hookType := Validate(declWith(dir, "PreFileCreate", "", Hook{Type: "script", Command: "./ok.sh"}), testRegistry(t))

	require.Len(t, matcher, 1)
	require.Len(t, hookType, 1)
	assert.ErrorIs(t, matcher[0], ErrBadMatcher)
	assert.NotErrorIs(t, matcher[0], ErrBadHookType)
	assert.ErrorIs(t, hookType[0], ErrBadHookType)
	assert.NotErrorIs(t, hookType[0], ErrBadMatcher)
}

func TestMessages_RendersOneLinePerProblem(t *testing.T) {
	dir := scriptDir(t)
	problems := Validate(declWith(dir, "PreFileCreate", `pth == "x"`, okHook()), testRegistry(t))

	lines := Messages(problems)
	require.Len(t, lines, 1)
	assert.Equal(t, problems[0].Message(), lines[0])
	assert.NotContains(t, strings.Join(lines, ""), "\n")
}
