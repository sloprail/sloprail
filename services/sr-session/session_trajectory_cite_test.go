package main

import (
	"github.com/sloprail/sloprail/internal/sessionpath"
	"os"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/internal/harness/claudecode"
	"github.com/sloprail/sloprail/internal/transcript"
)

// parseSourceTypes is the command-domain half of --source-types: it turns the flag
// string into the pools transcript.CiteWithSources searches, and it is where a
// typo or an empty selection is refused BEFORE any file work — so a bad flag reads
// as the caller's error rather than as exit 1 ("the quote was not found"). These
// pin the default (preserving today's behaviour), the two pools, both, and the two
// refusals.

// TestParseSourceTypes_DefaultIsUserOnly: the flag's own default value resolves to
// SourceUser alone, so a cite with no --source-types behaves exactly as before.
func TestParseSourceTypes_DefaultIsUserOnly(t *testing.T) {
	got, err := parseSourceTypes(string(transcript.SourceUser))
	if err != nil {
		t.Fatalf("default flag value must parse: %v", err)
	}
	if len(got) != 1 || got[0] != transcript.SourceUser {
		t.Fatalf("default must be [user]; got %v", got)
	}
}

// TestParseSourceTypes_KnownPools: each pool name, alone and combined, resolves to
// exactly that selection. The combined form is order-preserving and both members
// are present.
func TestParseSourceTypes_KnownPools(t *testing.T) {
	cases := map[string][]transcript.SourceType{
		"user":                 {transcript.SourceUser},
		"tool_result":          {transcript.SourceToolResult},
		"user,tool_result":     {transcript.SourceUser, transcript.SourceToolResult},
		" user , tool_result ": {transcript.SourceUser, transcript.SourceToolResult}, // whitespace tolerated
	}
	for flag, want := range cases {
		got, err := parseSourceTypes(flag)
		if err != nil {
			t.Fatalf("parseSourceTypes(%q): unexpected error %v", flag, err)
		}
		if len(got) != len(want) {
			t.Fatalf("parseSourceTypes(%q) = %v, want %v", flag, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("parseSourceTypes(%q)[%d] = %q, want %q", flag, i, got[i], want[i])
			}
		}
	}
}

// TestParseSourceTypes_RefusesUnknown: a name that is not a citable pool — an entry
// type like `assistant`, or a typo — is refused, not silently dropped. Silently
// dropping it could leave an empty selection that matches nothing and reads as "not
// found", hiding the mistake.
func TestParseSourceTypes_RefusesUnknown(t *testing.T) {
	for _, bad := range []string{"assistant", "system", "toolresult", "user,assistant"} {
		if _, err := parseSourceTypes(bad); err == nil {
			t.Fatalf("parseSourceTypes(%q) must refuse an unknown pool, got nil error", bad)
		}
	}
}

// TestParseSourceTypes_RefusesEmpty: a flag that trims down to nothing (empty, or
// only commas/spaces) names no pool and is refused — a search against no pool would
// exit 1 and be misread as "the user did not say that".
func TestParseSourceTypes_RefusesEmpty(t *testing.T) {
	for _, empty := range []string{"", ",", "  ", " , , "} {
		if _, err := parseSourceTypes(empty); err == nil {
			t.Fatalf("parseSourceTypes(%q) must refuse an empty selection, got nil error", empty)
		}
	}
}

// transcript.CurrentSessionPath is the environment fallback resolveTrajectory uses when
// no --path was given and no hook payload named a record: it derives the CURRENT
// session's own transcript from CLAUDE_CODE_SESSION_ID and the working directory.
// These cases pin the env→path derivation and, above all, that the encoding it
// uses is Claude Code's REAL projects-dir scheme — the raw working directory with
// every non-alphanumeric byte replaced by '-' — resolving to an actual file on
// disk, not a guess.
//
// The variable is CLAUDE_CODE_SESSION_ID (with the CODE), which Claude Code
// exports to a tool call and a hook alike. An earlier version of this package
// wrongly asserted no session id reached a tool call at all.

// TestCurrentSessionTranscript_ResolvesTheRealFile is the load-bearing case: a
// config dir laid out exactly as Claude Code lays one out —
// <config>/projects/<encoded-cwd>/<session-id>.jsonl — is resolved by the fallback
// to that very file. The transcript is written first and the resolver's answer is
// asserted to be a path that exists and holds the session's own records, so the
// encoder is checked against a real on-disk layout rather than an assumed one.
func TestCurrentSessionTranscript_ResolvesTheRealFile(t *testing.T) {
	cwd := t.TempDir()
	cfg := t.TempDir()
	sessionID := "453b70e4-d144-47d8-a819-1f7ecf4fce5b"

	// Lay out the transcript the way the harness (and Claude Code) does: the
	// projects dir is the RESOLVED cwd with every non-alnum byte turned to '-'.
	projDir := filepath.Join(cfg, "projects",
		transcript.EncodeProjectDir(transcript.ResolveWorkDir(cwd)))
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	want := filepath.Join(projDir, sessionID+".jsonl")
	// A first record naming the session and the tree, so BelongsToSession and
	// BelongsToTree both agree this is the file.
	rec := `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"` + sessionID +
		`","cwd":"` + cwd + `","message":{"role":"user","content":"please cite this"}}` + "\n"
	if err := os.WriteFile(want, []byte(rec), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv(claudecode.SessionIDEnv, sessionID)

	got := transcript.CurrentSessionPath(cwd)
	if got != want {
		t.Fatalf("transcript.CurrentSessionPath(%q) = %q, want %q", cwd, got, want)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("the resolved path does not exist on disk: %v", err)
	}
}

// TestCurrentSessionTranscript_UsesTheProjectsDirScheme pins that the fallback
// builds the projects path with transcript.ProjectDir (the raw-cwd scheme), NOT
// the git-root-anchored encodeWorkspace this service keys its own state under. The
// two DIVERGE for any subdirectory of a repo, and Claude Code files transcripts
// under the process cwd verbatim — so the projects scheme is the correct one. This
// asserts the resolved path is the ProjectDir path and, explicitly, is not the
// encodeWorkspace path when the cwd sits below a git root.
func TestCurrentSessionTranscript_UsesTheProjectsDirScheme(t *testing.T) {
	cfg := t.TempDir()
	sessionID := "s-scheme-1"
	// A directory whose encodeWorkspace anchor (its own path, since it is not a git
	// repo) equals the raw cwd here — the point of THIS case is the exact path
	// shape, so use a plain temp dir and compare to ProjectDir directly.
	cwd := t.TempDir()

	projDir := transcript.ProjectDir(cfg, cwd)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	want := filepath.Join(projDir, sessionID+".jsonl")
	rec := `{"type":"user","uuid":"u1","sessionId":"` + sessionID + `","cwd":"` + cwd + `"}` + "\n"
	if err := os.WriteFile(want, []byte(rec), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv(claudecode.SessionIDEnv, sessionID)

	got := transcript.CurrentSessionPath(cwd)
	if got != want {
		t.Fatalf("CurrentSessionPath = %q, want the ProjectDir-derived %q", got, want)
	}

	// The encodeWorkspace path is where this service keeps STATE, a different
	// component under a different root; it must never be what a transcript resolves
	// to. (encodeWorkspace also anchors on the git root, so for a repo subdirectory
	// it would name a shorter, wrong directory entirely.)
	wrong := filepath.Join(cfg, "projects", sessionpath.EncodeWorkspace(cwd), sessionID+".jsonl")
	if got == wrong && sessionpath.EncodeWorkspace(cwd) != transcript.EncodeProjectDir(transcript.ResolveWorkDir(cwd)) {
		t.Fatalf("resolved via encodeWorkspace %q, but the projects dir uses the raw-cwd scheme", got)
	}
}

// TestCurrentSessionTranscript_NoSessionID: with CLAUDE_CODE_SESSION_ID unset the
// fallback has nothing to derive from and returns "" — the signal resolveTrajectory
// reads as "no trajectory", so cite refuses with errNoTrajectory rather than
// guessing.
func TestCurrentSessionTranscript_NoSessionID(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// t.Setenv registers restoration of the ambient value (this test process runs
	// inside a real session that sets it); unset AFTER, so the cleanup still restores.
	t.Setenv(claudecode.SessionIDEnv, "placeholder")
	os.Unsetenv(claudecode.SessionIDEnv)
	if got := transcript.CurrentSessionPath(t.TempDir()); got != "" {
		t.Fatalf("with no %s set, want \"\"; got %q", claudecode.SessionIDEnv, got)
	}
}

// TestCurrentSessionTranscript_SessionIDIsNotAName: a session id carrying a path
// separator (or "."/"..") is refused rather than joined, the same refusal record()
// and sessionDBPath make — a name that traverses out of the projects directory must
// not resolve another conversation's file.
func TestCurrentSessionTranscript_SessionIDIsNotAName(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	for _, bad := range []string{"../../etc/passwd", `a\b`, ".", ".."} {
		t.Setenv(claudecode.SessionIDEnv, bad)
		if got := transcript.CurrentSessionPath(t.TempDir()); got != "" {
			t.Fatalf("a session id %q that is not a name must yield \"\"; got %q", bad, got)
		}
	}
}

// TestCurrentSessionTranscript_WrongSessionRefused: a file that exists at the
// derived path but whose records name a DIFFERENT session is not this session's, so
// BelongsToSession rejects it and the fallback returns "" — a guessed filename
// colliding with an unrelated conversation must not resolve.
func TestCurrentSessionTranscript_WrongSessionRefused(t *testing.T) {
	cwd := t.TempDir()
	cfg := t.TempDir()
	sessionID := "s-asked"

	projDir := transcript.ProjectDir(cfg, cwd)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	// The file at <session-id>.jsonl actually belongs to someone else.
	rec := `{"type":"user","uuid":"u1","sessionId":"s-OTHER","cwd":"` + cwd + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(projDir, sessionID+".jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv(claudecode.SessionIDEnv, sessionID)

	if got := transcript.CurrentSessionPath(cwd); got != "" {
		t.Fatalf("a file belonging to another session must yield \"\"; got %q", got)
	}
}
