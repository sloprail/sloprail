package record

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
)

// ProjectSessions implements harness.SessionLister: every top-level <id>.jsonl of the
// project directory is a root conversation (a sub-agent's record sits under its
// session's own directory, never beside it).
func (Transcripts) ProjectSessions(configDir, dir string) []harness.SessionRecord {
	projDir := ProjectDir(configDir, dir)
	if projDir == "" {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(projDir, "*.jsonl"))
	var out []harness.SessionRecord
	for _, m := range matches {
		out = append(out, harness.SessionRecord{ID: strings.TrimSuffix(filepath.Base(m), ".jsonl"), Path: m, Root: true})
	}
	return out
}

// Companions implements harness.CompanionLocator: the session's own directory
// (<session>/, its tool results; the sub-agents under it are archived through
// SubagentFiles) and the temp directory Claude Code keeps its scratchpad and task
// outputs in.
func (Transcripts) Companions(transcriptPath string) []harness.Companion {
	if !strings.HasSuffix(transcriptPath, ".jsonl") {
		return nil
	}
	sessDir := strings.TrimSuffix(transcriptPath, ".jsonl")
	out := []harness.Companion{{
		Item: "session dir (tool-results)", Dir: "session-dir", Path: sessDir, Exclude: []string{"subagents"},
	}}
	tmp := harness.Companion{Item: "scratchpad and tasks", Dir: "tmp"}
	if root, how := findTempDir(filepath.Base(filepath.Dir(transcriptPath)), filepath.Base(sessDir), transcriptPath); root != "" {
		tmp.Path, tmp.How = root, how
	} else {
		tmp.Why = "no temp directory found for the session"
	}
	return append(out, tmp)
}

// findTempDir locates Claude Code's per-session temp directory,
// <tmp root>/claude-<uid>/<project dir>/<session>/ (it holds scratchpad/ and
// tasks/). The transcript is asked first, for it may name the exact path; then
// $CLAUDE_CODE_TMPDIR (Claude Code's own override), then the platform's temp
// dirs. Symlinked roots (macOS /tmp -> /private/tmp) are resolved.
func findTempDir(projName, id, transcriptPath string) (dir, how string) {
	re := regexp.MustCompile(`(/[^\s"'\\]*/claude-[0-9]+/[A-Za-z0-9-]+/` + regexp.QuoteMeta(id) + `)/`)
	if p := scanForTempDir(transcriptPath, re); p != "" {
		return p, "named in the transcript"
	}
	var bases []string
	if d := os.Getenv("CLAUDE_CODE_TMPDIR"); d != "" {
		bases = append(bases, d)
	}
	bases = append(bases, "/tmp", os.TempDir())
	for _, base := range bases {
		cand := filepath.Join(base, fmt.Sprintf("claude-%d", os.Getuid()), projName, id)
		if isDir(cand) {
			if r, err := filepath.EvalSymlinks(cand); err == nil {
				cand = r
			}
			return cand, "temp root " + base
		}
	}
	return "", ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// scanForTempDir streams the transcript (these run to hundreds of MB; reading
// one whole would double the process's memory) in chunks that overlap enough
// to keep a path split across a chunk boundary, and returns the first match
// that is a directory.
func scanForTempDir(path string, re *regexp.Regexp) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	const chunk, overlap = 4 << 20, 4096
	buf := make([]byte, 0, chunk+overlap)
	tmp := make([]byte, chunk)
	for {
		n, rerr := f.Read(tmp)
		buf = append(buf, tmp[:n]...)
		for _, m := range re.FindAllSubmatch(buf, -1) {
			if isDir(string(m[1])) {
				return string(m[1])
			}
		}
		if len(buf) > overlap {
			buf = append(buf[:0], buf[len(buf)-overlap:]...)
		}
		if rerr != nil {
			return ""
		}
	}
}
