package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// judgeSessionSep is what the judges' project directories add to the project's own: a judge
// runs in <project>/.sloprail/<nature>/<rule>, which the harness encodes as
// <project dir>--sloprail-<nature>-<rule>.
const judgeSessionSep = "--sloprail-"

// sessionUsage is what one session's record says it cost, as the harness wrote it.
type sessionUsage struct {
	Role       string          `json:"role"`           // "agent" or "judge"
	Rule       string          `json:"rule,omitempty"` // a judge's rule, as its directory names it
	File       string          `json:"file"`           // the transcript, under the run directory
	CostUSD    float64         `json:"cost_usd"`
	APISeconds float64         `json:"api_seconds"`
	Models     json.RawMessage `json:"model_usage,omitempty"`
}

// runUsage is usage.json: every session of the run and the totals per role.
type runUsage struct {
	Note     string             `json:"note"`
	TotalUSD map[string]float64 `json:"total_cost_usd"`
	Sessions []sessionUsage     `json:"sessions"`
}

// judgeFiles is the transcripts of the judges the run's checks started (kept under
// judges/<rule>/), found beside the agent's own project directory. A judge is a session of
// its own, so `sr-eval archive` of the project does not hold it.
func judgeFiles(transcriptPath string) map[string][]byte {
	files := map[string][]byte{}
	projDir := filepath.Dir(transcriptPath)
	dirs, _ := filepath.Glob(projDir + judgeSessionSep + "*")
	for _, d := range dirs {
		rule := strings.TrimPrefix(filepath.Base(d), filepath.Base(projDir)+judgeSessionSep)
		records, _ := filepath.Glob(filepath.Join(d, "*.jsonl"))
		for _, r := range records {
			if body, err := os.ReadFile(r); err == nil {
				files[filepath.Join("judges", rule, filepath.Base(r))] = body
			}
		}
	}
	return files
}

// costOf reads the last cost record a session's transcript holds (Claude Code's "cost-state"
// line). A transcript without one costs nothing here, and says so by its zero.
func costOf(body []byte) (usd, apiSeconds float64, models json.RawMessage) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<28)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"type":"cost-state"`)) {
			continue
		}
		var rec struct {
			TotalCostUSD     float64         `json:"totalCostUSD"`
			TotalAPIDuration float64         `json:"totalAPIDuration"`
			ModelUsage       json.RawMessage `json:"modelUsage"`
		}
		if json.Unmarshal(line, &rec) == nil {
			usd, apiSeconds, models = rec.TotalCostUSD, rec.TotalAPIDuration/1000, rec.ModelUsage
		}
	}
	return usd, apiSeconds, models
}

// usageFile is usage.json for the agent's transcript and the judges' ones.
func usageFile(agentTranscript []byte, judges map[string][]byte) []byte {
	u := runUsage{
		Note:     "as each session's own record states it (list prices); a judge verdict served from the cache started no session",
		TotalUSD: map[string]float64{},
	}
	add := func(role, rule, file string, body []byte) {
		usd, secs, models := costOf(body)
		u.Sessions = append(u.Sessions, sessionUsage{Role: role, Rule: rule, File: file, CostUSD: usd, APISeconds: secs, Models: models})
		u.TotalUSD[role] += usd
		u.TotalUSD["all"] += usd
	}
	add("agent", "", "transcript.jsonl", agentTranscript)
	names := make([]string, 0, len(judges))
	for name := range judges {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		add("judge", filepath.Base(filepath.Dir(name)), name, judges[name])
	}
	body, _ := json.MarshalIndent(u, "", "  ")
	return append(body, '\n')
}

// repoBundle is the project's whole repository as one git bundle (every branch, the check
// results' one included): the end state the run left, restorable with `git clone repo.bundle`.
func repoBundle(ctx context.Context, project string) ([]byte, error) {
	tmp, err := os.CreateTemp("", "sr-eval-repo-*.bundle")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if out, err := exec.CommandContext(ctx, "git", "-C", project, "bundle", "create", "--quiet", tmp.Name(), "--all").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git bundle: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return os.ReadFile(tmp.Name())
}
