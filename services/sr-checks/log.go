package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkcache"
	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/gitrepo"
)

// logEntry is one stored verdict of a guard over a subject: one JSON line of `log --json`.
type logEntry struct {
	Rule    string   `json:"rule"`
	Subject string   `json:"subject"`
	Status  string   `json:"status"` // pass | fail
	Reasons []string `json:"reasons,omitempty"`
	// Key is the verdict's content-addressed cache key (checkcache.Key.ID).
	Key string `json:"key"`
	// JudgedAt is the stamp of the run that stored the verdict, RFC3339 UTC.
	JudgedAt string `json:"judgedAt"`
	// Base and Head are the commit range of the run that stored it; null when it recorded none.
	// The key itself is content-addressed and holds no commit.
	Base *string `json:"base"`
	Head *string `json:"head"`

	at time.Time
}

func newLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "log",
		// Hidden: an operator's read-back of the verdict history, not part of an agent's workflow
		// (run / verify / show), so it stays out of help, the generated CLI reference and the docs.
		Hidden: true,
		Short:  "Every stored file-guard verdict, oldest first, including fails a later pass superseded",
		Long: `Print every verdict stored on the sloprail/checks branch, oldest first.

Unlike show, which prints each subject's latest result over a range, log is the history: a fail that
a later run replaced with a pass is listed too. A verdict is a guard's pass or fail over one subject
and one cache key; a run that only replayed a stored verdict is not a new verdict and is not listed,
and neither are engine errors, skips and interrupted runs.

It reads the store exactly as verify does (fetches from origin, uses the local copy when that fails)
and only reads: it never writes, and never runs a script or a judge. It needs no range.

--json (alias --jsonl) prints one JSON object per line on stdout:

  {"rule":"file-guard/docs","subject":"changeset","status":"fail","reasons":["..."],
   "key":"<hash>","judgedAt":"2026-01-02T15:04:05.123456789Z","base":"<sha|null>","head":"<sha|null>"}

status is pass or fail; reasons is present for a fail only. judgedAt is the time the run that stored
the verdict was recorded. base and head are that run's commit range, null when it recorded none.

--rule limits the listing to one file-guard, by folder name or qualified name.
--failing keeps only the fails.
--since keeps verdicts judged at or after a time: a duration back from now (30m, 24h, 7d, 2w), an
RFC3339 time, or a date (2026-01-02, UTC).`,
		Args: cobra.NoArgs,
		RunE: runLog,
	}
	cmd.Flags().Bool("json", false, "Print one JSON object per verdict, one per line (JSONL)")
	cmd.Flags().Bool("jsonl", false, "Alias of --json")
	cmd.Flags().Bool("failing", false, "Only fails")
	cmd.Flags().String("rule", "", "Only this file-guard (folder name or qualified name)")
	cmd.Flags().String("since", "", "Only verdicts judged at or after this: a duration (24h, 7d), an RFC3339 time or a date")
	return cmd
}

func runLog(cmd *cobra.Command, _ []string) error {
	asJSON, _ := cmd.Flags().GetBool("json")
	if j, _ := cmd.Flags().GetBool("jsonl"); j {
		asJSON = true
	}
	failing, _ := cmd.Flags().GetBool("failing")
	rule, _ := cmd.Flags().GetString("rule")
	sinceArg, _ := cmd.Flags().GetString("since")
	var since time.Time
	if sinceArg != "" {
		t, err := parseSince(sinceArg, time.Now())
		if err != nil {
			return err
		}
		since = t
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("sloprail: working directory: %w", err)
	}
	root, err := gitrepo.Root(cwd)
	if err != nil || root == "" {
		return fmt.Errorf("sloprail: %s is not inside a git repository", cwd)
	}
	cache, err := checkrun.OpenCache(cmd.ErrOrStderr(), filepath.Clean(root), false) // read-only, like verify
	if err != nil {
		return err
	}
	runs, err := cache.Runs()
	if err != nil {
		return fmt.Errorf("sloprail: the check results could not be read: %w", err)
	}
	checkrun.WarnPending(cmd.ErrOrStderr(), cache)

	w := cmd.OutOrStdout()
	enc := json.NewEncoder(w)
	for _, e := range logEntries(runs, rule, failing, since) {
		if asJSON {
			if err := enc.Encode(e); err != nil {
				return err
			}
			continue
		}
		line := fmt.Sprintf("%s  %-4s  %s  %s", e.JudgedAt, e.Status, e.Rule, e.Subject)
		if len(e.Reasons) > 0 {
			line += "  " + strings.Join(strings.Fields(e.Reasons[0]), " ")
		}
		fmt.Fprintln(w, line)
	}
	return nil
}

// guardKind is the kind of the one verdict a guard stores per subject (checkrun's own constant
// is unexported); it is also the only kind that carries a fingerprint, so the key is the cache key.
const guardKind = "guard"

// logEntries lists every stored verdict in runs, oldest first. A verdict is a guard-kind check
// with a fingerprint that finished as a pass or a fail and is not a replay of an earlier one.
func logEntries(runs []checkcache.Run, rule string, failing bool, since time.Time) []logEntry {
	out := []logEntry{}
	for _, r := range runs {
		if rule != "" && r.Rule != rule && !strings.HasSuffix(r.Rule, "/"+rule) {
			continue
		}
		at, stamp := runTime(r.RunAt)
		if !since.IsZero() && at.Before(since) {
			continue
		}
		for _, c := range r.Checks {
			if c.Kind != guardKind || c.Fingerprint == "" {
				continue
			}
			if c.Status != checkcache.StatusPass && c.Status != checkcache.StatusFail {
				continue
			}
			if replayed, _ := c.Metadata["replayed"].(bool); replayed {
				continue
			}
			if failing && c.Status != checkcache.StatusFail {
				continue
			}
			e := logEntry{Rule: r.Rule, Subject: c.Subject, Status: c.Status, Key: r.CheckKey(c).ID(),
				JudgedAt: stamp, Base: orNull(r.BaseRef), Head: orNull(r.HeadRef), at: at}
			if c.Status == checkcache.StatusFail {
				e.Reasons = failReasons(c)
			}
			out = append(out, e)
		}
	}
	// Runs come newest first; a stable reverse by time keeps ties in store order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out
}

// failReasons are the reasons of a stored fail: each failing step's own reason when the verdict
// kept its steps, else the guard's overall reasoning.
func failReasons(c checkcache.Check) []string {
	var reasons []string
	seen := map[string]bool{}
	for _, st := range storedSteps(c.Metadata) {
		if st.Status == checkcache.StatusFail && st.Reason != "" && !seen[st.Reason] {
			seen[st.Reason] = true
			reasons = append(reasons, st.Reason)
		}
	}
	if len(reasons) == 0 {
		if s, _ := c.Metadata["reasoning"].(string); s != "" {
			reasons = []string{s}
		}
	}
	if reasons == nil {
		reasons = []string{}
	}
	return reasons
}

func storedSteps(meta map[string]any) []struct{ Status, Reason string } {
	body, err := json.Marshal(meta["steps"])
	if err != nil {
		return nil
	}
	var rows []struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(body, &rows) != nil {
		return nil
	}
	out := make([]struct{ Status, Reason string }, len(rows))
	for i, r := range rows {
		out[i] = struct{ Status, Reason string }{r.Status, r.Reason}
	}
	return out
}

func orNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// runTime reads a run's fixed-width UTC stamp and returns it as RFC3339 (nanoseconds kept). A stamp
// that does not parse is passed through, ordered first.
func runTime(s string) (time.Time, string) {
	t, err := time.Parse("2006-01-02T15:04:05.000000000Z", s)
	if err != nil {
		if t, err = time.Parse(time.RFC3339Nano, s); err != nil {
			return time.Time{}, s
		}
	}
	return t, t.UTC().Format(time.RFC3339Nano)
}

var durationRe = regexp.MustCompile(`^(\d+)([dw])$`)

// parseSince reads --since: a duration back from now (Go's, plus d and w), an RFC3339 time or a
// UTC date.
func parseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if m := durationRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := 24 * time.Hour
		if m[2] == "w" {
			unit *= 7
		}
		return now.Add(-time.Duration(n) * unit), nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("sloprail: --since %q: want a duration (30m, 24h, 7d), an RFC3339 time or a date (2026-01-02)", s)
}
