package main

import (
	"errors"
	"fmt"
	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/harness/claudecode"
	"path/filepath"
	"sort"
	"strings"
)

// Harness is one agent CLI sr-agent knows how to run.
//
// It is a string rather than an enum because it is also what `--harness` takes
// from a caller, and a caller naming an unsupported one must get back the name
// it typed in the refusal. An enum would have had to map the unknown case to a
// zero value and lose it.
type Harness string

// ClaudeCode and Codex are the first harnesses this binary supported; Cursor follows
// (cursor_harness.go). What makes another cheap is that everything harness-shaped in
// this binary is reached through the registry below rather than written inline.
const (
	ClaudeCode Harness = "claude-code"
	Codex      Harness = "codex"
)

// Cursor is Cursor's CLI agent, `cursor-agent`.
const Cursor Harness = "cursor"

// SizeAlias is one of the harness-agnostic sizes a model set may name.
//
// Spelled `size-md` rather than bare `md`, and the prefix is load-bearing: a
// harness could ship a model literally called `md`, and without the prefix an
// entry could not be classified without knowing every harness's catalogue.
// With it, an entry says which kind it is on sight — which is what lets
// classification be a pure function of the string, with no catalogue lookup and
// no ambiguity for a reader either.
type SizeAlias string

// The six sizes. This is the whole set: an alias outside it is not a size that
// resolves differently, it is a concrete model name that happens to start with
// `size-`, and it is treated as one.
const (
	SizeXS  SizeAlias = "size-xs"
	SizeSM  SizeAlias = "size-sm"
	SizeMD  SizeAlias = "size-md"
	SizeLG  SizeAlias = "size-lg"
	SizeXL  SizeAlias = "size-xl"
	SizeXXL SizeAlias = "size-xxl"
)

// sizeAliases is every valid alias, in ascending order of size. The order is
// what `--help` and the diagnostics print; nothing resolves by index.
var sizeAliases = []SizeAlias{SizeXS, SizeSM, SizeMD, SizeLG, SizeXL, SizeXXL}

// harnessSpec is everything that differs between one harness and the next.
//
// Every harness-specific fact lives in one of these values rather than in a
// switch somewhere, so supporting Codex is writing another value and not
// finding every place a harness is named.
type harnessSpec struct {
	// name is the harness as `--harness` spells it.
	name Harness

	// binary is the executable to run.
	binary string

	// detect reports whether an environment names this harness. It is given a
	// lookup rather than reading os.Getenv itself, so detection is testable
	// without mutating the process environment — which matters under -race,
	// where a test setting a variable races every other test reading one.
	detect func(getenv func(string) string) bool

	// sizes maps every alias to a model this harness offers. Every alias must
	// be present: an alias that did not resolve would break the property the
	// whole design rests on, and aliasesComplete pins that at startup.
	sizes map[SizeAlias]string

	// offers reports whether this harness has a model by that exact name. This
	// is what makes a concrete entry harness-specific — the same string is a
	// match under one harness and a skip under another.
	offers func(model string) bool

	// argsFlag is the `--<harness>-args` flag carrying this harness's own
	// settings. Named here so that passing one harness's flag while another is
	// running can be reported precisely rather than generically.
	argsFlag string

	// baseArgs are harness-specific arguments sr-agent ALWAYS passes this harness,
	// ahead of any caller-supplied ones — the settings a run needs whatever the
	// caller asked, so a caller (a judge check, most of all) does not have to know
	// them. For Claude Code this is the isolation `--settings` that keeps the
	// launched agent from re-triggering the very hooks/plugins/mcp servers that
	// launched it; a rule firing on RULE.md whose judge is itself an agent would
	// otherwise recurse. nil when the harness needs none.
	baseArgs []string

	// stdinPromptAbove is the prompt size, in bytes, above which the prompt goes
	// to the harness on its STDIN instead of as a command-line argument. An argv
	// is bounded (the OS's ARG_MAX covers argv AND environment together, about
	// 1 MB on macOS, and Linux caps one argument at 128 KiB): a ~550 KB rendered
	// judge prompt failed with "Argument list too long". `claude -p` reads its
	// prompt from stdin when none is given as an argument. 0 means the harness is
	// never handed one that way. Below the bound the prompt stays positional, as
	// every harness takes it.
	stdinPromptAbove int

	// execArgs are the arguments that put the binary in one-shot mode, before
	// the model flag: `-p` for Claude Code, the `exec` subcommand for Codex. nil
	// means ["-p"], which every spec literal that predates Codex relied on.
	execArgs []string

	// modelFlag is the flag naming the model; "" means `--model`.
	modelFlag string

	// stdinPromptArgs are appended when the prompt is fed on stdin: Codex needs
	// the explicit `-` placeholder, Claude Code reads stdin when given no prompt.
	stdinPromptArgs []string

	// grant returns the arguments that give the agent exactly the file access a
	// run needs: each added directory in its mode — writable or readonly — (the
	// caller's `--add-dir[:<mode>]`s, and the --verify answer file's folder, which
	// is just one more writable dir), and the caller's own requested tools (a
	// judge's `allowed_tools`), merged into the same grant so they do not arrive
	// as competing flags. nil when the harness has no permission model to speak to.
	//
	// --stop-script puts the agent's output file outside the working tree on
	// purpose, and a harness that sandboxes writes will refuse it. This is the
	// seam for saying so per-harness rather than assuming every harness has
	// Claude Code's permission model.
	grant func(g accessGrant) []string

	// checkGrant, when set, refuses an access grant this harness cannot express as
	// tightly as asked (before anything runs). nil accepts every grant.
	checkGrant func(g accessGrant) error

	// tools, when set, translates the allowed/denied tools (the canonical vocabulary,
	// internal/harness/toolrules.go) into this harness's run settings, instead of the
	// rules riding through verbatim in grant. nil means verbatim (Claude Code).
	tools harness.ToolPolicy

	// grantEnv is grant for a harness whose permissions are not command-line flags
	// but a configuration the process reads: it returns the environment that points
	// the harness at one made for this run, any arguments that belong with it, and a cleanup that removes it. Used by
	// Cursor (see cursor_grant.go). nil when the harness has none.
	grantEnv func(g accessGrant) (env, args []string, cleanup func(), err error)
}

// claudeCodeSpec is Claude Code.
//
// The alias targets are the FAMILY aliases claude's own `--help` documents as
// "an alias for the latest model" — `haiku`, `sonnet`, `opus`, `fable` — not
// pinned versions like `claude-sonnet-4-5`. A pinned version is a promise this
// binary cannot keep: `claude-3-5-haiku-latest` is already retired and refused,
// and `claude-sonnet-4-7` never existed at all, so a table of pins would decay
// into a table of refusals with each release. A family alias is the harness's
// own answer to "the latest of this size", kept current by the harness rather
// than by this file. All four were confirmed to run.
//
// There are six sizes and four families, so two sizes double up. Claude Code
// offers nothing below Haiku, so `size-xs` and `size-sm` are both Haiku; the
// top two are Opus and Fable, which is where the largest models are. Doubling
// up is honest — the alias asks for a size and gets the closest thing this
// harness has — and it keeps every alias resolving, which is the property that
// makes an alias always end the search.
var claudeCodeSpec = harnessSpec{
	name:   ClaudeCode,
	binary: "claude",

	stdinPromptAbove: 64 << 10,
	detect: func(getenv func(string) string) bool {
		// CLAUDECODE is what Claude Code sets on every session; the entrypoint
		// variable is checked too so that a session which sets only one of them
		// is still recognised. Both were confirmed present in a live session.
		return getenv("CLAUDECODE") != "" || getenv("CLAUDE_CODE_ENTRYPOINT") != ""
	},
	sizes: map[SizeAlias]string{
		SizeXS:  "haiku",
		SizeSM:  "haiku",
		SizeMD:  "sonnet",
		SizeLG:  "opus",
		SizeXL:  "opus",
		SizeXXL: "fable",
	},
	// A concrete name is offered when it looks like one of this harness's own.
	//
	// This is a PREFIX test, not a catalogue, and the difference is deliberate.
	// A hardcoded list of exact names would be wrong the day a new model ships:
	// a set naming a real, runnable model would be refused because this binary
	// had not heard of it, and the author would be told their model does not
	// exist when it does. The harness itself is the authority on its catalogue
	// and it already answers — it exits non-zero with a message naming the
	// model. So this decides only what a name is ABOUT, and lets the harness
	// decide whether it exists.
	//
	// The cost is that `claude-sonnet-4-7`, which does not exist, is treated as
	// a match here and refused by claude rather than skipped. That is the right
	// side to err on: skipping it silently would run some other model in its
	// place, which is the accountability failure the refusal rule exists to
	// prevent.
	offers: func(model string) bool {
		return strings.HasPrefix(model, "claude-") ||
			isClaudeFamilyAlias(model)
	},
	argsFlag: "--claude-args",

	// The isolation settings sr-agent ALWAYS gives Claude Code
	// (claudecode.IsolationSettings): the launched agent carries none of the caller's
	// session wiring. No hooks fire inside it, no plugins load, no MCP servers
	// connect. They are what the hand-rolled judge scripts passed before the
	// judge-check migration, lifted here so a judge check gets the isolation for
	// free. Without it a judge that is itself an agent (a rule-quality judge firing
	// on RULE.md) would re-trigger the guard on its own child's writes and recurse.
	//
	// `disableAllHooks:true` is what stops the hooks; the empty `hooks:{}` and
	// `enabledPlugins:{}` do NOT. Measured 2026-10-01 (claude 2.1.285, haiku,
	// `claude -p`):
	//
	//   - A project whose .claude/settings.json had Stop and UserPromptSubmit hooks
	//     appending to a marker file: with the empty-object settings both markers
	//     were written. --settings is merged over the project and user settings
	//     and an empty object overrides nothing, so a judge session ran the
	//     project's (and any enabled plugin's) Stop hooks, nesting a judge inside a
	//     judge. With "disableAllHooks":true no marker was written.
	//   - The same held for a plugin's hooks (--plugin-dir with a hooks.json Stop
	//     hook): fired under the old settings, silent with disableAllHooks.
	//   - A caller's LATER --settings replaces this one rather than merging with
	//     it: `--settings '{"hooks":{},"disableAllHooks":true}' --settings '{}'`
	//     still fired the project's Stop hook. That is the escape hatch sr-eval
	//     uses (`settings: "{}"`) to run its agent-under-test WITH hooks.
	//
	// `--permission-mode default` pins the permission mode, which the launched
	// agent would otherwise inherit from the user's own settings. Measured on
	// 2026-09-27 (claude 2.1.282, haiku, a fake HOME whose settings.json set
	// `defaultMode: bypassPermissions`): a judge granted NO tools wrote a file
	// outside every granted directory; with this flag the same Write was
	// refused. (The readonly project's deny held either way; under
	// `acceptEdits` nothing outside was writable.) A caller that wants another
	// mode says so in --claude-args, which comes later and wins.
	baseArgs: []string{"--settings", claudecode.IsolationSettings, "--permission-mode", "default"},

	// The file access a run gets. Every line of this was MEASURED against the real
	// CLI (claude 2.1.282, haiku, `claude -p` in a clean environment, 2026-09-27)
	// rather than read off the docs — and the measurement overturned what this
	// comment used to say.
	//
	// What was measured. The agent ran with its working directory set to a rule's
	// folder inside a scratch project (as a judge does) and was asked, tool by
	// tool, to Read/Grep/Glob a project file, to Write, Edit and Bash-write into
	// the project, to Write outside both the project and the answer directory, and
	// to write its answer file. `permission_denials` in the JSON result and the
	// files left on disk were the evidence.
	//
	//   - The OLD grant (`--add-dir <answer dir> --allowed-tools "Write …"`) did
	//     NOT confine writes. An unscoped `Write` allow wrote new files into the
	//     project, into the rule's own folder, AND outside every granted
	//     directory; --add-dir confined nothing. With `Read` granted, reads
	//     anywhere succeeded; with `Write WebFetch` (doc-conformance's judge) the
	//     Read of the project's SPEC.md was denied, and so were the Bash grep/find
	//     the agent fell back to — the blind judges seen in real eval runs.
	//
	//   - `Edit(//<dir>/**)` DOES grant writing the answer file: Write created and
	//     overwrote it, and a Bash `printf … > <answer>` redirection was allowed
	//     too (claude checks redirection targets against Edit rules). Everything
	//     else was denied: Write/Edit into the project, Write outside it, and
	//     Bash `printf >`, `touch` and `rm` anywhere else. (What the old comment
	//     reported — that an Edit rule does not cover creating the file — was the
	//     Write tool's own "read the file before writing it" refusal of the
	//     pre-created empty answer, not a permission denial: the agent Reads the
	//     file and the Write then succeeds.) `Write(<path>)` is still NOT matched:
	//     claude matches only Edit rules on file writes, and the answer write was
	//     denied under it.
	//
	//   - A rule's path is matched as the agent SPELLS it, not as the filesystem
	//     resolves it: an allow for /private/var/…/answer did not cover a Write to
	//     /var/…/answer (macOS's /var is a symlink). So every path rule is emitted
	//     for both spellings — see pathRules.
	//
	//   - --add-dir makes a directory a working directory, and inside a working
	//     directory Read, Grep and Glob need no grant at all. So a readonly dir
	//     is readable by a judge whatever its allowed_tools — which is the point:
	//     a judge of a file-guard must be able to open the spec its marker pins.
	//
	//   - `--disallowed-tools Edit(//<readonly dir>/**)` keeps a readonly dir
	//     unwritable even against a caller who ALSO granted Write, Edit or Bash:
	//     a deny rule beats every allow. Measured with `Write` granted: Write and
	//     Edit into the project and Write into the rule's folder inside it were
	//     denied; and with `Bash` granted: `printf >`, `touch` and `rm` there
	//     were denied.
	//
	// So, per `--add-dir[:<mode>]` (sr-agent's own flag, mirroring claude's):
	// every dir goes into one `--add-dir`; a WRITABLE dir (`--add-dir <path>`,
	// and the --verify answer folder, which is just one of them) gets an Edit
	// allow scoped to it — the NARROWEST grant that writes there, no unscoped
	// Write, which was measured to write anywhere; a READONLY dir
	// (`--add-dir:readonly <path>`) gets an Edit deny.
	//
	// Confirmed end to end through this binary on 2026-09-27 (claude 2.1.282,
	// haiku; `sr-agent --verify … --add-dir:readonly <project> --add-dir
	// <scratch>`, started in a rule's folder inside the project):
	//
	//   - readonly project: Read of its SPEC.md and a grep of it succeeded;
	//     Write, Edit and NotebookEdit into it, Write into the rule's own folder
	//     and Bash `printf > <project file>` were all refused ("File is in a
	//     directory that is denied by your permission settings") — also with
	//     `--allowed-tools "Write Edit NotebookEdit Bash"`, where the deny beat
	//     every one of those allows;
	//   - writable scratch dir: Write created a file, Edit changed one, and a Bash
	//     `printf > <scratch file>` redirection was allowed;
	//   - the answer file: the agent's FIRST Write created it and the verifier
	//     accepted it;
	//   - nesting: a writable dir inside the readonly project stayed writable
	//     (Write, Edit, Bash redirection all succeeded there) while the rest of
	//     the project still refused every write — denied by default, as nothing
	//     allows it; and a readonly dir inside a writable one refused a Write
	//     while its writable parent accepted one.
	//
	// The one write that ever got through outside a writable dir was a
	// caller-granted unscoped Write to a file OUTSIDE every added dir, which is
	// the caveat below.
	//
	// What this does NOT confine: a caller who names `Write`/`Edit` in its own
	// tools gets them unscoped, and those write anywhere OUTSIDE the readonly
	// dirs; a caller who names `Bash` gets a shell. The Edit deny catches the
	// file-writing commands claude recognises (redirection, touch, rm were
	// measured) but a granted shell can run any program, and no permission rule
	// sandboxes what that program does. So the read-only promise is made for the file tools;
	// granting Bash to a judge is granting it a shell, and the rule author owns
	// that choice.
	//
	// The caller's tools are passed as further values of the SAME
	// `--allowed-tools` flag (the flag is variadic: separate values each grant,
	// measured), never as a second flag, and each rule is its own argument so a
	// path containing a space stays one rule (measured with a project named
	// "my proj"). The `--` BuildInvocation puts before the prompt is what stops
	// these variadic flags swallowing it.
	grant: func(g accessGrant) []string {
		var args, dirs, allow, deny []string
		for _, d := range g.Dirs {
			dirs = append(dirs, d.Path)
			switch d.Mode {
			case dirWritable:
				allow = append(allow, pathRules("Edit", d.Path)...)
			case dirReadonly:
				// ALWAYS denied. There used to be an exception for a readonly dir
				// holding a writable one (the answer folder, when $TMPDIR sits
				// inside the project), and a review measured what it cost: with
				// TMPDIR=<project>/.tmp the deny was dropped, and a judge granted
				// Write wrote <project>/pwned-nested.txt. claude has no way to say
				// "deny this dir except that sub-dir", so the exception cannot be
				// expressed safely. Instead, nothing writable may sit inside a
				// readonly dir: resolveAddDirs refuses the caller's own
				// `--add-dir X/sub --add-dir:readonly X`, and runVerified places the
				// answer folder outside every readonly dir (answerRoots).
				// Re-measured after the fix (2026-09-27, claude 2.1.282, haiku,
				// through sr-agent): with TMPDIR=<project>/.tmp and Write granted,
				// Write into the project and into <project>/.tmp were refused and
				// the answer landed in the user cache dir; the same with a project
				// settings.json of `defaultMode: acceptEdits` and no tools.
				deny = append(deny, pathRules("Edit", d.Path)...)
			}
		}
		allow = append(allow, g.Tools...)
		if len(dirs) > 0 {
			args = append(append(args, "--add-dir"), dirs...)
		}
		if len(allow) > 0 {
			args = append(append(args, "--allowed-tools"), allow...)
		}
		// The caller's own denies join the readonly-dir denies in ONE
		// --disallowed-tools group, each rule its own argument.
		//
		// No DEFAULT deny set is added for a command family the caller grants
		// (e.g. curl's writing flags whenever Bash(curl:*) is allowed), on
		// measurement rather than taste. On 2026-09-27 (claude 2.1.282, haiku),
		// with Bash(curl:*) granted, a 20-pattern deny set (`Bash(curl * -o *)`,
		// `--output`, `-O`, `--remote-name*`, `--output-dir`, `-D`, `-c`,
		// `--trace*`, `-K`, `-d @*`, `--data*@*`, `-T`, `--upload-file`,
		// `-F *@*`, `--json @*`, `-X`) refused every form it names and still let
		// `curl -sL <url> | grep …` run, but these got through:
		//   - combined short flags: `curl -sLo <file> <url>` wrote the file;
		//   - curl's long tail: `--etag-save`, `--stderr`, `--hsts`,
		//     `--dump-header`, `--cookie-jar` each wrote a file;
		//   - `-H @<file>` sent a file's contents as headers.
		// And under Bash(sed:*), `sed -n 'w <file>'` wrote a file. A pattern list
		// cannot enumerate a tool's option grammar, so an engine default would
		// promise a confinement it does not deliver. What a rule grants, and
		// takes back, stays in the rule, where its author can see the trade.
		// The shape that measured SOUND is a rule's own: pin the whole command
		// (`Bash(curl -sL https://code.claude.com/docs/*)`) and deny any extra
		// word after it (`Bash(curl -sL https://code.claude.com/docs/* *)`,
		// claude's `*` crossing spaces). Every appended flag, file or URL was
		// refused, and the one-argument fetch piped into grep still ran.
		deny = append(deny, g.DenyTools...)
		if len(deny) > 0 {
			args = append(append(args, "--disallowed-tools"), deny...)
		}
		return args
	},
}

// cursorSpec is Cursor (`cursor-agent -p`).
//
// Every claim here was measured against the real cursor-agent 2026.10.01 on
// 2026-10-07 (headless, a scratch directory), beyond the harness-mocks recordings
// (cursor-mock/snapshots/runs/*/run.yaml); docs: https://cursor.com/docs/cli/headless,
// https://cursor.com/docs/cli/reference/parameters,
// https://cursor.com/docs/cli/reference/permissions.
//
//   - Invocation: `cursor-agent -p --trust --model <m> [-- <prompt>]`. `--trust` is the
//     headless workspace-trust flag. `--force` is deliberately NOT passed: without it
//     a headless run rejects shell commands (also recorded: runs/noninteractive-no-force),
//     while file writes still apply, which is what lets --verify's answer file be written.
//   - Prompt: with no prompt argument cursor-agent -p reads it from STDIN (a 600 KB
//     prompt on stdin answered; the same as an argument failed to start), and `--`
//     before a positional prompt is accepted. So stdinPromptAbove is the same bound as
//     claude's.
//   - Permissions: no flag, but a private CURSOR_CONFIG_DIR with a cli-config.json is
//     honoured headless; see cursor_grant.go for what that does and does not express.
//   - Hooks are NOT disabled: no flag or setting turns off project, user or plugin
//     hooks (the user's are read from the real home, which also holds the login). A
//     judge launched here carries SLOPRAIL_LAUNCHED_BY, which the engine's own hook
//     answers by not gating; any other hook the project has fires.
//   - Models: `cursor-agent --list-models`. The aliases map to rungs of one vendor
//     family where there is one: Gemini Flash for the small sizes, Claude Sonnet 5.5
//     for the middle, Claude Opus 5.5 above. Claude's Fable (the top rung under
//     claude-code) is not used: Cursor lists it "NO ZDR" (no zero data retention),
//     which a judge reading a user's project should not opt into silently.
var cursorSpec = harnessSpec{
	name:   Cursor,
	binary: "cursor-agent",

	// CURSOR_AGENT=1 is set in the environment of every shell command the agent runs
	// (recorded: runs/subprocess-session-env), which is where a judge check
	// launched by a hook or tool call would look; CURSOR_INVOKED_AS is the same
	// session's second marker.
	detect: func(getenv func(string) string) bool {
		return getenv("CURSOR_AGENT") != "" || getenv("CURSOR_INVOKED_AS") != ""
	},
	sizes: map[SizeAlias]string{
		SizeXS:  "gemini-3.8-flash-low",
		SizeSM:  "gemini-3.8-flash-high",
		SizeMD:  "claude-sonnet-5-5-medium",
		SizeLG:  "claude-opus-5-5-medium",
		SizeXL:  "claude-opus-5-5-high",
		SizeXXL: "claude-opus-5-5-max",
	},
	// A concrete name is Cursor's when it looks like one of the catalogue's families.
	// A prefix test, not the catalogue, for the reason claudeCodeSpec gives: the CLI
	// is the authority on what exists.
	offers: func(model string) bool {
		if model == "auto" {
			return true
		}
		for _, prefix := range []string{"cursor-", "composer-", "gpt-", "claude-", "gemini-", "grok-", "muse-"} {
			if strings.HasPrefix(model, prefix) {
				return true
			}
		}
		return false
	},
	argsFlag:         "--cursor-args",
	baseArgs:         []string{"--trust"},
	stdinPromptAbove: 64 << 10,
	grantEnv:         cursorGrantEnv,
}

// isClaudeFamilyAlias reports whether a name is one of claude's own bare family
// aliases. They carry no `claude-` prefix, so the prefix test alone would miss
// them and a set naming `sonnet` outright would be skipped under the very
// harness that offers it.
func isClaudeFamilyAlias(model string) bool {
	switch model {
	case "haiku", "sonnet", "opus", "fable":
		return true
	}
	return false
}

// accessGrant is the file access one run needs, harness-neutral: the harness's
// grant turns it into that harness's own flags.
type accessGrant struct {
	// Dirs are the directories the agent is given, each in its mode: the
	// caller's `--add-dir[:<mode>]`s and, under --verify, the answer file's
	// folder as one more writable dir.
	Dirs []dirGrant

	// Tools are the caller's own requested tools (`--allowed-tools`), passed
	// through in the harness's own spelling.
	Tools []string

	// DenyTools are the caller's own denied tool rules (`--disallowed-tools`),
	// passed through in the harness's own spelling beside the grant's own
	// readonly-dir denies.
	DenyTools []string
}

// dirGrant is one directory the agent is given, and how.
type dirGrant struct {
	Path string
	Mode dirMode
}

// dirMode is how an added directory may be used.
type dirMode int

const (
	// dirWritable is `--add-dir <path>`: readable and writable, as claude's own
	// --add-dir.
	dirWritable dirMode = iota

	// dirReadonly is `--add-dir:readonly <path>`: readable, never writable.
	dirReadonly
)

// pathRules is one Claude Code permission rule per spelling of dir: `Tool(//abs/**)`
// (a leading `//` is claude's spelling of an absolute path) for dir as given and,
// when it differs, for dir with its symlinks resolved.
//
// Both, because claude matches a rule against the path as the agent SPELLS it:
// measured, an allow for /private/var/…/sr-agent-output…/** did not cover a Write
// to /var/…/sr-agent-output…/answer — the same file, via macOS's /var symlink —
// and the prompt names the unresolved one while a tool may report the resolved
// one. A rule that covered only one spelling would be a deny the agent could walk
// around by naming the other.
func pathRules(tool, dir string) []string {
	forms := []string{filepath.Clean(dir)}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != forms[0] {
		forms = append(forms, resolved)
	}
	rules := make([]string, 0, len(forms))
	for _, form := range forms {
		rules = append(rules, tool+"(/"+form+"/**)")
	}
	return rules
}

// within reports whether path is dir or lies under it, comparing symlink-resolved
// forms where they resolve so /var and /private/var agree.
func within(path, dir string) bool {
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	rel, err := filepath.Rel(resolve(dir), resolve(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func (s harnessSpec) execArgsOrDefault() []string {
	if s.execArgs != nil {
		return s.execArgs
	}
	return []string{"-p"}
}

func (s harnessSpec) modelFlagOrDefault() string {
	if s.modelFlag != "" {
		return s.modelFlag
	}
	return "--model"
}

// harnesses is the registry. Adding a harness is adding an entry here. The order
// is the order environment detection tries them. SLOPRAIL_HARNESS, when set, is read
// first (DetectHarness); the specs' own markers decide otherwise, and Codex's yields to
// Claude Code's when an environment holds both.
var harnesses = []harnessSpec{codexSpec, claudeCodeSpec, cursorSpec}

// ErrNoHarness is returned when the environment names no harness this binary
// knows.
var ErrNoHarness = errors.New("no supported harness detected")

// ErrUnknownHarness is returned when `--harness` names one this binary does not
// support.
var ErrUnknownHarness = errors.New("unsupported harness")

// lookupSpec finds the registry entry for a named harness.
func lookupSpec(name Harness) (harnessSpec, bool) {
	for _, spec := range harnesses {
		if spec.name == name {
			return spec, true
		}
	}
	return harnessSpec{}, false
}

// supportedNames is every harness this binary supports, for diagnostics.
func supportedNames() []string {
	names := make([]string, 0, len(harnesses))
	for _, spec := range harnesses {
		names = append(names, string(spec.name))
	}
	sort.Strings(names)
	return names
}

// DetectHarness works out which harness is running, or refuses.
//
// An environment naming nothing known is an ERROR, not a default. The default
// would be Claude Code, and being wrong about it means running a different
// agent than the rule asked for while reporting success — a verdict attributed
// to a model that never saw the question. A refusal naming what was looked for
// is worth more than a guess that cannot be audited.
//
// The environment is read through a lookup so a test can supply one directly.
func DetectHarness(getenv func(string) string) (harnessSpec, error) {
	// An explicit SLOPRAIL_HARNESS names the session's harness: a judge runs on the
	// harness that triggered it.
	if name := getenv("SLOPRAIL_HARNESS"); name != "" {
		// The plugin hooks export the internal/harness registry name, which spells
		// Claude Code "claudecode"; --harness spells it "claude-code".
		if name == "claudecode" {
			name = string(ClaudeCode)
		}
		if spec, ok := lookupSpec(Harness(name)); ok {
			return spec, nil
		}
		return harnessSpec{}, fmt.Errorf("%w: SLOPRAIL_HARNESS=%q. Supported: %s",
			ErrUnknownHarness, name, strings.Join(supportedNames(), ", "))
	}
	for _, spec := range harnesses {
		if spec.detect(getenv) {
			return spec, nil
		}
	}
	return harnessSpec{}, fmt.Errorf(
		"%w: nothing in the environment names one of %s. "+
			"Pass --harness to name it explicitly, which is what a hook running outside any harness must do",
		ErrNoHarness, strings.Join(supportedNames(), ", "))
}

// ResolveHarness picks the harness to run: the one `--harness` names, or the
// one the environment names when it names none.
//
// An override that names an unsupported harness is refused rather than falling
// through to detection. A caller who asked for Codex and silently got Claude
// Code is in exactly the position the refusal rule exists to prevent, and it
// would be worse here than in detection because the caller was explicit.
func ResolveHarness(override string, getenv func(string) string) (harnessSpec, error) {
	if override == "" {
		return DetectHarness(getenv)
	}
	spec, ok := lookupSpec(Harness(override))
	if !ok {
		return harnessSpec{}, fmt.Errorf(
			"%w: %q. Supported: %s",
			ErrUnknownHarness, override, strings.Join(supportedNames(), ", "))
	}
	return spec, nil
}

// aliasesComplete checks that every harness maps every alias.
//
// An alias ALWAYS resolving is the property the model-set design rests on: it
// is what makes an alias end the search and what makes anything after one
// unreachable. A harness missing one entry would break that quietly — a set
// naming that alias would fall through to the entries after it, which the
// author wrote believing they were unreachable. Checked here so a registry
// entry with a hole fails immediately rather than at the first set that names
// the missing size.
func aliasesComplete() error {
	for _, spec := range harnesses {
		for _, alias := range sizeAliases {
			if spec.sizes[alias] == "" {
				return fmt.Errorf(
					"harness %s maps no model for %s: every harness must map every size alias, "+
						"because an alias that did not resolve would make the entries after it reachable",
					spec.name, alias)
			}
		}
	}
	return nil
}
