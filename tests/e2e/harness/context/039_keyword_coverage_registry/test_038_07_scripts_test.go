package e2e

import (
	"encoding/json"
	"fmt"
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Two of the example's scripts are proven by running them directly: what each
// decides depends on something a scripted session cannot arrange — a sibling
// script that cannot run, and a real eval scorer's input.

func exampleFile(t *testing.T, rel string) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "examples", exampleName, rel)
}

func copyFile(t *testing.T, src, dst string, mode os.FileMode) {
	t.Helper()
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", dst, err)
	}
	if err := os.WriteFile(dst, body, mode); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// T038_32: the judge's prepare skips the model only on drops-keywords.sh's
// DECIDED "drops nothing" (exit 1). A predicate that could not run at all —
// not executable (126), missing (127) — decided nothing; reading it as "drops
// nothing" skipped the judge, and any resolvable quote of the user's then
// admitted the drop. The engine's own `when` contract is the same: anything but
// exit 1 applies.
func TestT038_32_ThePrepareSkipsOnlyOnADecidedNoDrop(t *testing.T) {
	prepare := exampleFile(t, ".sloprail/file-guard/scanner-keywords-hold/only-when-dropped.sh")
	payload := `{"event":{"kind":"PostFileDelete","path":"scanners/mine/scanner.yaml","oldContent":"active: true\nkeywords:\n  - agent\n"}}`

	run := func(t *testing.T, dir string) string {
		t.Helper()
		cmd := exec.Command("bash", prepare)
		cmd.Env = append(harness.HostEnv(), "SR_GUARDRAIL_DIR="+dir)
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("the prepare itself failed: %v\n%s", err, out)
		}
		return string(out)
	}

	for _, tc := range []struct {
		name string
		lay  func(t *testing.T, dir string)
		want string
	}{
		{"not executable", func(t *testing.T, dir string) {
			copyFile(t, exampleFile(t, ".sloprail/file-guard/scanner-keywords-hold/drops-keywords.sh"), filepath.Join(dir, "drops-keywords.sh"), 0o644)
		}, `"additionalContext"`},
		{"missing", func(t *testing.T, dir string) {}, `"additionalContext"`},
		{"crashes", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "drops-keywords.sh"), []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, `"additionalContext"`},
		{"decided: drops nothing", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "drops-keywords.sh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, `"skip": true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.lay(t, dir)
			if got := run(t, dir); !strings.Contains(got, tc.want) {
				t.Errorf("prepare printed %q, want it to carry %s", got, tc.want)
			}
		})
	}
}

// T038_33: the eval scorer tells its judge a scanner WAS covered only when it
// checked that itself. It used to say so whenever verify-scanner-coverage never
// refused — which is also what happens when that gate never ran at all (the
// context inactive, the registry in a sub-agent's own session) — and told the
// judge to trust it over its own reading.
func TestT038_33_TheScorerClaimsOnlyTheCoverageItChecked(t *testing.T) {
	// The build under test's sr-session: the scorer reads the gh calls off its
	// `trajectory normalize`, the same parse the gates use.
	build := New(t).BinDir()
	covering := `gh search issues "auth token" leak logging`
	heredoc := "cat > SCAN-NOTES.md <<'EOF'\nWe ran " + covering + " and found little.\nEOF"
	for _, tc := range []struct {
		name     string
		calls    []string // Bash commands the agent ran, in order
		refused  map[int]bool
		wantFact string
		banFact  string
		broken   bool // sr-session's normalize fails
	}{
		{"a declared scanner and no gh call", nil, nil, "found NO single gh command", "WAS covered", false},
		{"a declared scanner and a covering gh call", []string{covering}, nil, "WAS covered", "found NO single gh command", false},
		// A heredoc that merely MENTIONS the covering search is not one.
		{"a covering search only mentioned in a heredoc", []string{heredoc}, nil, "found NO single gh command", "WAS covered", false},
		// A covering search a hook refused never ran.
		{"a covering search that was refused", []string{covering}, map[int]bool{0: true}, "found NO single gh command", "WAS covered", false},
		// The scorer could not read the gh calls at all: coverage is unknown, not
		// absent.
		{"normalize fails", []string{covering}, nil, "not established by script", "found NO single gh command", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			proj := filepath.Join(work, "proj")
			bin := filepath.Join(work, "bin")
			prompt := filepath.Join(work, "prompt.txt")

			scanner := "active: true\nkeywords:\n  - auth token\n  - leak\n  - logging\n"
			if err := os.MkdirAll(filepath.Join(proj, "scanners", "token-leaks"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(proj, "scanners", "token-leaks", "scanner.yaml"), []byte(scanner), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(proj, "SCAN-NOTES.md"), []byte("# notes\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			// The judge the scorer asks, stubbed: record the prompt, answer healthy.
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			stub := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + prompt + "\necho '{\"healthy\": true, \"reasoning\": \"stub\"}'\n"
			if err := os.WriteFile(filepath.Join(bin, "sr-agent"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.broken {
				if err := os.WriteFile(filepath.Join(bin, "sr-session"), []byte("#!/bin/sh\necho 'database is locked' >&2\nexit 1\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(filepath.Join(build, "sr-session"), filepath.Join(bin, "sr-session")); err != nil {
				t.Fatal(err)
			}

			var lines []string
			entry := func(m map[string]any) {
				m["uuid"] = fmt.Sprintf("e%d", len(lines))
				if len(lines) > 0 {
					m["parentUuid"] = fmt.Sprintf("e%d", len(lines)-1)
				}
				m["sessionId"] = "s-038-33"
				b, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				lines = append(lines, string(b))
			}
			entry(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "check GitHub for prior art on auth tokens leaking into logs"}})
			entry(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "w1", "name": "Write", "input": map[string]any{"file_path": "scanners/token-leaks/scanner.yaml", "content": "x"}}}}})
			for i, c := range tc.calls {
				id := fmt.Sprintf("b%d", i)
				entry(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "content": []any{
					map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": c}}}}})
				result := "ok"
				if tc.refused[i] {
					result = "PreToolUse:Bash hook error: No scanner is declared in this session"
				}
				// A hook block is recorded as an error result, as the harness does.
				entry(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "tool_result", "tool_use_id": id, "content": result, "is_error": tc.refused[i]}}}})
			}
			transcript := filepath.Join(work, "session.jsonl")
			if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("sh", exampleFile(t, "eval/security-scan/score.sh"))
			cmd.Env = append(harness.HostEnv(),
				"SR_EVAL_TRANSCRIPT="+transcript,
				"SR_EVAL_BIN_DIR="+bin,
				"SR_EVAL_PROJECT_DIR="+proj,
				"SR_EVAL_VERDICT_OUT="+filepath.Join(work, "verdict.json"),
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("the scorer failed: %v\n%s", err, out)
			}
			asked, err := os.ReadFile(prompt)
			if err != nil {
				t.Fatalf("the scorer never asked its judge: %v\n%s", err, out)
			}
			if !strings.Contains(string(asked), tc.wantFact) {
				t.Errorf("the judge was not told %q:\n%s", tc.wantFact, asked)
			}
			if strings.Contains(string(asked), tc.banFact) {
				t.Errorf("the judge was told %q, which the scorer did not establish:\n%s", tc.banFact, asked)
			}
		})
	}
}
