package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/checkcache"
	"github.com/sloprail/sloprail/internal/gitrepo"
)

func guardRun(id, at, rule, status, fp, base, head string, meta map[string]any) checkcache.Run {
	return checkcache.Run{ID: id, RunAt: at, Rule: rule, RuleHash: "h", BaseRef: base, HeadRef: head, Complete: true,
		Checks: []checkcache.Check{{Subject: "changeset", Kind: "guard", Status: status, Fingerprint: fp, Metadata: meta}}}
}

func TestLogEntries_KeepsASupersededFailAndOrdersOldestFirst(t *testing.T) {
	fail := guardRun("r1", "2026-01-02T10:00:00.000000000Z", "file-guard/docs", "fail", "fp1", "b1", "h1",
		map[string]any{"reasoning": "overall", "steps": []map[string]any{
			{"subject": "changeset", "kind": "judge", "status": "pass"},
			{"subject": "changeset", "kind": "script", "status": "fail", "reason": "the step reason"}}})
	pass := guardRun("r2", "2026-01-02T11:00:00.000000000Z", "file-guard/docs", "pass", "fp2", "", "", nil)
	// the store lists newest first
	got := logEntries([]checkcache.Run{pass, fail}, "", false, time.Time{})
	require.Len(t, got, 2)
	assert.Equal(t, "fail", got[0].Status)
	assert.Equal(t, []string{"the step reason"}, got[0].Reasons)
	assert.Equal(t, "2026-01-02T10:00:00Z", got[0].JudgedAt)
	require.NotNil(t, got[0].Base)
	assert.Equal(t, "b1", *got[0].Base)
	assert.Equal(t, "pass", got[1].Status)
	assert.Nil(t, got[1].Reasons)
	assert.Nil(t, got[1].Base, "a run that recorded no range has null base/head")
	assert.Equal(t, checkcache.Key{Rule: "file-guard/docs", Kind: "guard", Subject: "changeset", Fingerprint: "fp1"}.ID(), got[0].Key)
}

func TestLogEntries_FailFallsBackToOverallReasoning(t *testing.T) {
	r := guardRun("r1", "2026-01-02T10:00:00.000000000Z", "file-guard/x", "fail", "fp", "b", "h", map[string]any{"reasoning": "overall"})
	got := logEntries([]checkcache.Run{r}, "", false, time.Time{})
	require.Len(t, got, 1)
	assert.Equal(t, []string{"overall"}, got[0].Reasons)
}

func TestLogEntries_OnlyRealVerdicts(t *testing.T) {
	replay := guardRun("r1", "2026-01-02T10:00:00.000000000Z", "file-guard/x", "fail", "fp", "b", "h", map[string]any{"replayed": true})
	unstored := guardRun("r2", "2026-01-02T10:00:00.000000000Z", "file-guard/x", "fail", "", "b", "h", nil)
	unstored.Checks[0].Kind = "guard:unstored"
	errored := guardRun("r3", "2026-01-02T10:00:00.000000000Z", "file-guard/x", "error", "fp", "b", "h", nil)
	step := guardRun("r4", "2026-01-02T10:00:00.000000000Z", "file-guard/x", "fail", "", "b", "h", nil)
	step.Checks[0].Kind = "check[0]:script:./a.sh"
	assert.Empty(t, logEntries([]checkcache.Run{replay, unstored, errored, step}, "", false, time.Time{}))
}

func TestLogEntries_RuleFailingAndSinceFilters(t *testing.T) {
	a := guardRun("r1", "2026-01-02T10:00:00.000000000Z", "file-guard/docs", "fail", "f1", "b", "h", nil)
	b := guardRun("r2", "2026-01-03T10:00:00.000000000Z", "file-guard/docs", "pass", "f2", "b", "h", nil)
	c := guardRun("r3", "2026-01-03T11:00:00.000000000Z", "acme/file-guard/size", "fail", "f3", "b", "h", nil)
	all := []checkcache.Run{c, b, a}

	assert.Len(t, logEntries(all, "docs", false, time.Time{}), 2, "folder name")
	assert.Len(t, logEntries(all, "file-guard/docs", false, time.Time{}), 2, "qualified name")
	assert.Len(t, logEntries(all, "size", false, time.Time{}), 1, "a plugin's rule by folder name")
	assert.Len(t, logEntries(all, "", true, time.Time{}), 2, "--failing")
	since, err := parseSince("2026-01-03", time.Now())
	require.NoError(t, err)
	assert.Len(t, logEntries(all, "", false, since), 2, "--since")
}

func TestLogEntries_JSONShape(t *testing.T) {
	r := guardRun("r1", "2026-01-02T10:00:00.000000000Z", "file-guard/docs", "pass", "fp", "", "", nil)
	got := logEntries([]checkcache.Run{r}, "", false, time.Time{})
	var buf bytes.Buffer
	require.NoError(t, json.NewEncoder(&buf).Encode(got[0]))
	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	assert.ElementsMatch(t, []string{"rule", "subject", "status", "key", "judgedAt", "base", "head"}, keys(m), "no reasons on a pass; base/head are present as null")
	assert.Nil(t, m["base"])
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"24h":                  now.Add(-24 * time.Hour),
		"7d":                   now.Add(-7 * 24 * time.Hour),
		"2w":                   now.Add(-14 * 24 * time.Hour),
		"2026-01-02":           time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		"2026-01-02T03:04:05Z": time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	} {
		got, err := parseSince(in, now)
		require.NoError(t, err, in)
		assert.True(t, got.Equal(want), "%s: %s != %s", in, got, want)
	}
	_, err := parseSince("yesterday", now)
	require.Error(t, err)
}

func TestRangeCommits_BaseExclusiveHeadInclusive(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		out, err := c.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "base")
	base := run("rev-parse", "HEAD")
	run("commit", "-q", "--allow-empty", "-m", "one")
	one := run("rev-parse", "HEAD")
	run("commit", "-q", "--allow-empty", "-m", "two")
	two := run("rev-parse", "HEAD")

	got, err := rangeCommits(dir, []string{base + ".." + two})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{one: true, two: true}, got)

	got, err = rangeCommits(dir, []string{base + ".." + one, gitrepo.EmptyTree + ".." + base})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{one: true, base: true}, got)

	for _, bad := range []string{"nodots", "..two", base + "..", base + "..nosuchrev"} {
		_, err = rangeCommits(dir, []string{bad})
		assert.Error(t, err, bad)
	}
}
