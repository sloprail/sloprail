package transcript

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/harness"
)

// TestSurveyStableSessionID resolves the identity of every transcript under a
// real harness's projects directory and reports how each one fared. It is a
// measuring instrument, not a regression test: it reads the operator's own
// ~/.claude, so it runs only when asked —
//
//	SLOPRAIL_SURVEY_PROJECTS=~/.claude/projects go test ./internal/transcript -run TestSurvey -v
//
// It exists because every number in identity.go and sibling.go came from a
// sweep like this one, and the next change to the walk should be able to re-run
// it rather than re-invent it. Each transcript is resolved the way sr-session
// resolves it: a sub-agent's project directory is the one its session sits in.
func TestSurveyStableSessionID(t *testing.T) {
	root := os.Getenv("SLOPRAIL_SURVEY_PROJECTS")
	if root == "" {
		t.Skip("set SLOPRAIL_SURVEY_PROJECTS to a harness projects directory to survey it")
	}
	root = strings.Replace(root, "~", os.Getenv("HOME"), 1)

	counts := map[string]int{}
	examples := map[string][]string{}
	total := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") || !surveyIsTranscript(path) {
			return nil
		}
		projectDir := filepath.Dir(path)
		if sessionDir := SessionDirOfSubagent(path); sessionDir != "" {
			projectDir = filepath.Dir(sessionDir)
		} else if filepath.Dir(projectDir) != filepath.Clean(root) {
			return nil // neither a session's record nor a sub-agent's
		}
		total++
		mode := "ok"
		res, err := ResolveStableSessionID(projectDir, path)
		switch {
		case err != nil:
			mode = surveyMode(err)
		case res.Degraded != nil:
			mode = "degraded: " + surveyMode(res.Degraded)
		}
		counts[mode]++
		if mode != "ok" && len(examples[mode]) < 10 {
			examples[mode] = append(examples[mode], path)
		}
		return nil
	})

	modes := make([]string, 0, len(counts))
	for m := range counts {
		modes = append(modes, m)
	}
	sort.Strings(modes)
	t.Logf("%d transcripts under %s", total, root)
	for _, m := range modes {
		t.Logf("  %-40s %6d", m, counts[m])
		for _, ex := range examples[m] {
			t.Logf("      %s", ex)
		}
	}
}

func surveyMode(err error) string {
	switch {
	case errors.Is(err, ErrChainRunaway):
		if strings.Contains(err.Error(), "revisits") {
			return "chain revisits"
		}
		return "chain runaway"
	case errors.Is(err, ErrContinuationMissing):
		return "continuation missing"
	case errors.Is(err, ErrNoOriginRecord):
		return "no origin record"
	case errors.Is(err, fs.ErrNotExist):
		return "record missing"
	default:
		return "other: " + err.Error()
	}
}

// surveyIsTranscript leaves out the files a harness keeps beside its
// transcripts that are not session records at all — a workflow's journal and
// its agents' raw stream logs carry no record with a uuid, so they have no
// chain for an identity to be read from and no hook ever resolves one.
func surveyIsTranscript(path string) bool {
	return containsAnyRecordUUID(path)
}

func containsAnyRecordUUID(path string) bool {
	found := false
	_ = scanFile(path, func(rec harness.Record) bool {
		found = rec.UUID != ""
		return !found
	})
	return found
}
