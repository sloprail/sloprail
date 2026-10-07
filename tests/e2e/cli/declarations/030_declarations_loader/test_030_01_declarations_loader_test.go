package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree writes a set of relative files under dir, creating parents. Used to
// stand up a `.sloprail` tree a test then loads.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

// T030_01: a project whose declarations are all sound loads clean and exits 0.
//
// This is the whole-binary counterpart to the loader's unit tests — it proves the
// command wired the loader to the real module registry and reports success with
// the status a caller reads, not just that the package function returns no
// Invalid.
func TestT030_01_ValidDeclarationsLoadCleanAndExitZero(t *testing.T) {
	e := New(t)
	proj := t.TempDir()

	writeTree(t, proj, map[string]string{
		".sloprail/gate/require-skill/gate.yaml": `
on:
  - event: PreFileWrite
    match: event.path startsWith "memories/topics/"
require:
  - skill: document-topic
`,
		".sloprail/context/refactoring/context.yaml": `
on:
  - event: PreToolUse
enter: ./enter.sh
exit: ./exit.sh
`,
		".sloprail/file-guard/pinned/file-guard.yaml": `
match: any(markers, .kind == "invariant")
checks:
  - script: ./check.sh
`,
		".sloprail/file-guard/structure.yaml": `
allow:
  - glob: "memories/*.md"
`,
	})

	res := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if res.Code != 0 {
		t.Fatalf("expected exit 0 for sound declarations, got %d:\n%s", res.Code, res.Output)
	}
	// The report names each nature's loaded declarations. (A goal is NOT a loaded
	// nature — it is a project-level composite the engine does not read — so no
	// goal appears here.)
	for _, want := range []string{"require-skill", "refactoring", "pinned", "structure gate: present"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("expected the report to mention %q:\n%s", want, res.Output)
		}
	}
}

// T030_02: an invalid declaration is reported by name with its fault, and the
// command exits 1 — the status a CI step or hook reads. This is the load-time
// refusal the whole slice exists to produce, observed through the binary.
// sr:proves loading/one-broken-rule-disables-only-itself
func TestT030_02_InvalidDeclarationIsReportedAndExitsOne(t *testing.T) {
	e := New(t)
	proj := t.TempDir()

	writeTree(t, proj, map[string]string{
		// A gate on a Post event — too late to gate, refused at load.
		".sloprail/gate/late/gate.yaml": `
on:
  - event: PostFileCreate
checks:
  - script: ./s.sh
`,
	})

	res := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if res.Code != 1 {
		t.Fatalf("expected exit 1 for an invalid declaration, got %d:\n%s", res.Code, res.Output)
	}
	// The refusal names the gate and the offending kind.
	if !strings.Contains(res.Output, "gate/late") {
		t.Errorf("expected the report to name the invalid gate:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "PostFileCreate") {
		t.Errorf("expected the report to name the offending event kind:\n%s", res.Output)
	}
}

// T030_03: one bad declaration does not stop the others being reported as loaded.
// The engine loads every sound declaration and refuses only the broken one — a
// single typo must not disarm a whole project.
// sr:proves loading/one-broken-rule-disables-only-itself
func TestT030_03_OneBadDoesNotDisarmTheRest(t *testing.T) {
	e := New(t)
	proj := t.TempDir()

	writeTree(t, proj, map[string]string{
		".sloprail/gate/good/gate.yaml": `
on:
  - event: Stop
checks:
  - script: ./s.sh
`,
		".sloprail/gate/bad/gate.yaml": `
on:
  - event: PostFileCreate
checks:
  - script: ./s.sh
`,
	})

	res := e.CLIDirect(proj, "sr-file", "declarations", proj)
	// Exits 1 because one is invalid, but the good one is still reported loaded.
	if res.Code != 1 {
		t.Fatalf("expected exit 1, got %d:\n%s", res.Code, res.Output)
	}
	if !strings.Contains(res.Output, "gates (1): good") {
		t.Errorf("the sound gate must still load and be reported:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "gate/bad") {
		t.Errorf("the broken gate must be reported invalid:\n%s", res.Output)
	}
}

// T030_04: the command accepts either a project root or the `.sloprail` path
// itself, naming the same tree — so an author need not remember which the command
// wanted.
func TestT030_04_AcceptsProjectRootOrDotDir(t *testing.T) {
	e := New(t)
	proj := t.TempDir()
	writeTree(t, proj, map[string]string{
		".sloprail/gate/stop-check/gate.yaml": "on:\n  - event: Stop\nchecks:\n  - script: ./s.sh\n",
	})

	viaRoot := e.CLIDirect(proj, "sr-file", "declarations", proj)
	viaDotDir := e.CLIDirect(proj, "sr-file", "declarations", filepath.Join(proj, ".sloprail"))

	if viaRoot.Code != 0 || viaDotDir.Code != 0 {
		t.Fatalf("both spellings should exit 0: root=%d dotdir=%d", viaRoot.Code, viaDotDir.Code)
	}
	if !strings.Contains(viaRoot.Output, "stop-check") || !strings.Contains(viaDotDir.Output, "stop-check") {
		t.Errorf("both spellings should load the same tree:\n--- root ---\n%s\n--- dotdir ---\n%s", viaRoot.Output, viaDotDir.Output)
	}
}

// T030_05: every shipped new-format example loads clean through the binary. This
// is the reconciliation proof at the CLI level — pointing the real command at a
// real example's `.sloprail` exits 0, confirming the example grammar matches the
// spec-faithful scopes the loader enforces.
func TestT030_05_ShippedExamplesLoadCleanThroughTheBinary(t *testing.T) {
	e := New(t)
	repo := repoRootForExamples(t)

	// A representative spread across natures and both aliases: a gate with a
	// PreFileWrite alias (required-context-precondition), a context with a
	// PostFileWrite alias (eval-loop-maxing), a PostTagWrite context
	// (research-rigor), a structure gate (completeness-artifact-on-trigger), and a
	// gate+context+file-guard composite where a Stop gate reads a context's payload
	// (deterministic-refactoring-mode).
	for _, example := range []string{
		"required-context-precondition",
		"eval-loop-maxing",
		"research-rigor",
		"completeness-artifact-on-trigger",
		"interlinking",
		"marker-anchored-structure",
		"deterministic-refactoring-mode",
	} {
		example := example
		t.Run(example, func(t *testing.T) {
			dir := filepath.Join(repo, "examples", example)
			res := e.CLIDirect(dir, "sr-file", "declarations", dir)
			if res.Code != 0 {
				t.Fatalf("example %q must load clean through the binary, got exit %d:\n%s", example, res.Code, res.Output)
			}
		})
	}
}

// T030_06: every shipped PLUGIN's `.sloprail` tree loads clean through the binary
// under PLUGIN rules (`--plugin <name>`) — not project rules. The examples above
// live under examples/ and load as a PROJECT's own declarations; a use-case
// plugin ships its guardrails under marketplace/plugins/<name>/.sloprail and is
// discovered by a consumer as a plugin, so its declarations must load the way a
// plugin's do: a structure.yaml here is expected to declare `scope` (a project's
// own structure must NOT — see structure-gate.md), so loading it WITHOUT
// `--plugin` is refused by design and is not the check this test makes.
// sloprail-tasks and sloprail-content are the shipped plugins as of this
// writing, each with its own file-guards/gates and a scoped structure gate.
func TestT030_06_ShippedPluginsLoadCleanThroughTheBinary(t *testing.T) {
	e := New(t)
	repo := repoRootForExamples(t)

	for _, plugin := range []string{
		"sloprail-tasks",
		"sloprail-content",
	} {
		plugin := plugin
		t.Run(plugin, func(t *testing.T) {
			dir := filepath.Join(repo, "marketplace", "plugins", plugin)
			res := e.CLIDirect(dir, "sr-file", "declarations", "--plugin", plugin, dir)
			if res.Code != 0 {
				t.Fatalf("plugin %q must load clean through the binary (--plugin %s), got exit %d:\n%s", plugin, plugin, res.Code, res.Output)
			}
		})
	}
}

// repoRootForExamples finds the module root so the shipped examples are reachable
// from the test's working directory.
func repoRootForExamples(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod up the tree)")
		}
		dir = parent
	}
}

// T030_07: `preventive:` was removed from file-guards. A file-guard still carrying
// it — true or false — is refused at load, by name, and the message says how to
// split it: a PreFileWrite gate for the prevention plus a plain file-guard for the
// result. The sound gate beside it still loads. The command exits 1, the status a
// CI step or a session start reads.
// sr:proves loading/retired-file-guard-keys-are-refused
func TestT030_07_PreventiveFileGuardIsRefusedWithTheSplit(t *testing.T) {
	e := New(t)
	for _, value := range []string{"true", "false"} {
		value := value
		t.Run("preventive: "+value, func(t *testing.T) {
			proj := t.TempDir()
			writeTree(t, proj, map[string]string{
				".sloprail/file-guard/old-way/file-guard.yaml": "match: \"memories/**\"\npreventive: " + value + "\nchecks:\n  - script: ./check.sh\n",
				".sloprail/gate/new-way/gate.yaml":             "on:\n  - event: PreFileWrite\n    match: event.path startsWith \"memories/\"\nchecks:\n  - script: ./check.sh\n",
			})

			res := e.CLIDirect(proj, "sr-file", "declarations", proj)
			if res.Code != 1 {
				t.Fatalf("a file-guard carrying `preventive:` must be refused (exit 1), got %d:\n%s", res.Code, res.Output)
			}
			for _, want := range []string{"file-guard/old-way", "preventive", "PreFileWrite", "gate", "plain file-guard"} {
				if !strings.Contains(res.Output, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, res.Output)
				}
			}
			if !strings.Contains(res.Output, "gates (1): new-way") {
				t.Errorf("the sound gate beside it must still load:\n%s", res.Output)
			}
		})
	}
}

// T030_08: the match forms the authoring docs teach for an unknown write result
// load clean on a PreFileWrite gate: `event.resultKnown` and `event.newContent`
// are declared on both kinds the alias expands to.
func TestT030_08_ResultKnownMatchExamplesLoad(t *testing.T) {
	e := New(t)
	proj := t.TempDir()
	writeTree(t, proj, map[string]string{
		".sloprail/gate/strip/gate.yaml":   "on:\n  - event: PreFileWrite\n    match: 'event.resultKnown and not (event.newContent contains \"---\")'\nchecks:\n  - script: ./check.sh\n",
		".sloprail/gate/unknown/gate.yaml": "on:\n  - event: PreFileWrite\n    match: 'event.path startsWith \"spec/\" and not event.resultKnown'\nchecks:\n  - script: ./cannot-verify.sh\n",
	})
	res := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if res.Code != 0 {
		t.Fatalf("the documented resultKnown matches must load clean, got exit %d:\n%s", res.Code, res.Output)
	}
}
