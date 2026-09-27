package grounding

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/transcript"
)

// Resolve mode: the contract between sloprail's pre-tool hook and sr-file.
//
// Before a Bash call made ONLY of sr-file invocations runs, the hook runs the
// same line with EnvResolveDir naming a fresh directory. Each sr-file then
// changes nothing and appends one Resolved record to ResolvedFile(dir): the
// change it WOULD make, computed by the binary that will make it, after the
// real shell has done its quoting and expansion. The hook turns those records
// into the file events guardrails judge, so prediction and action cannot drift.
//
// The records travel through a file the hook names, never stdout: stdout is
// the command's own output, free to say anything, and a channel another writer
// shares is not one a guardrail may trust.
const (
	// EnvResolveDir, when set, puts sr-file in resolve mode and names the
	// directory it records into.
	EnvResolveDir = "SR_FILE_RESOLVE_DIR"

	// EnvTranscript names the trajectory citations resolve against. The hook
	// sets it; outside a hook sr-file falls back to the current session's.
	EnvTranscript = "SR_TRANSCRIPT"
)

// Resolved is one change an sr-file invocation would make.
type Resolved struct {
	Verb string `json:"verb"`

	// Path is the target, absolute.
	Path string `json:"path"`

	// Existed says whether the target was a file before this change, as the
	// overlay sees it after earlier invocations in the same line.
	Existed bool `json:"existed"`

	OldContent string `json:"oldContent"`
	NewContent string `json:"newContent"`

	Citations []transcript.Citation `json:"citations"`
}

// ResolvedFile is where resolve mode appends its records, one JSON per line.
func ResolvedFile(dir string) string { return filepath.Join(dir, "resolved.jsonl") }

// OverlayEntry is where resolve mode records the predicted content of abs, so
// a later invocation in the same line (`sr-file edit a ... && sr-file edit a
// ...`) reads the earlier one's result instead of disk. A name derived from the
// path maps any path to one flat file.
func OverlayEntry(dir, abs string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return filepath.Join(dir, "overlay", hex.EncodeToString(sum[:]))
}

// OverlayDeleted marks abs as deleted earlier in the line.
func OverlayDeleted(dir, abs string) string { return OverlayEntry(dir, abs) + ".deleted" }
