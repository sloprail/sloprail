package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/fingerprint"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// `seen`: whether a Post event re-sends something an earlier Stop was already
// shown.
//
// The engine delivers generously on purpose. A cycle stays open until a Stop
// passes, so a refused reply's text — and its tags — come back with the retry;
// and the file events are the difference against the session's baseline, so a
// changed file is delivered on every Stop until it is committed. Re-sending is
// right (a rule that needs an obligation to survive a refusal must still see
// it), but a rule that judges only what happened since the previous Stop — a
// tag the corrected reply declares, a file written this turn — cannot tell the
// two apart from the event alone. Before this, such a rule had to re-read the
// transcript or fingerprint the files itself.
//
// So each Post event says it: `seen: true` on a tag found only in text an
// earlier Stop read (tagmod, from the split cycleAgentMessages makes), and on a
// file whose content is what an earlier Stop was handed (markSeenFiles). Seen is
// relative to the previous JUDGED Stop — one that ran the dispatch — whatever
// its verdict. A Stop let through un-judged at stop_hook_block_cap, and a cycle
// that was interrupted before any Stop, record nothing, so what they covered is
// delivered unseen again: re-judged rather than silently skipped.

// absentFingerprint stands for a file a Post event reports deleted. A real
// fingerprint never takes this form, so a file deleted, then restored with its
// old content, reads as a change either way round.
const absentFingerprint = "absent"

// markSeenFiles sets `seen` on each Post file event whose content matches what
// the previous judged Stop was handed for that path, and returns this Stop's
// snapshot (path to fingerprint) for recordStopSeen.
//
// A file that cannot be read has no identity to compare: it is left unseen and
// kept out of the snapshot, so it is delivered as new again next time — the
// re-judge direction, never the skip one. A snapshot that cannot be read makes
// every file unseen, which is the behaviour before this existed.
func markSeenFiles(cmd *cobra.Command, store sessionstate.Store, events []event.Event, root string) map[string]string {
	previous := map[string]string{}
	if raw, ok, err := store.Meta(sessionstate.MetaStopSeenFiles); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: previous Stop's files not read, so none are marked seen:", err)
	} else if ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &previous); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: previous Stop's files unreadable, so none are marked seen:", err)
			previous = map[string]string{}
		}
	}

	snapshot := map[string]string{}
	for _, e := range events {
		f, err := filemod.FromEvent(e)
		if err != nil {
			continue
		}
		var fp string
		switch e.Kind {
		case filemod.KindPostCreate, filemod.KindPostUpdate:
			fp, err = fingerprint.OfFile(resolve(root, f.Path))
			if err != nil {
				continue
			}
		case filemod.KindPostDelete:
			fp = absentFingerprint
		default:
			continue
		}
		snapshot[f.Path] = fp
		if before, ok := previous[f.Path]; ok && before == fp {
			e.Fields[filemod.FieldSeen] = true
		}
	}
	return snapshot
}

// recordStopSeen stores what this judged Stop was shown: the files' snapshot and
// how far the record was read for tags. Failures are reported and swallowed —
// the cost is the next Stop marking nothing seen, i.e. re-judging, never
// skipping.
func recordStopSeen(cmd *cobra.Command, store sessionstate.Store, files map[string]string, recordEnd string) {
	raw, err := json.Marshal(files)
	if err == nil {
		err = store.SetMeta(sessionstate.MetaStopSeenFiles, string(raw))
	}
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: this Stop's files not recorded as seen:", err)
	}
	if err := store.SetMeta(sessionstate.MetaStopSeenRecord, recordEnd); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: this Stop's read position not recorded as seen:", err)
	}
}

// resolve turns an event's path into one the filesystem will accept.
//
// Paths on file events are the project's own — relative to where the session
// runs. Joining is what makes the fingerprint read the file the hook would
// read, rather than whatever sits at the same relative path from the process's
// working directory. An absolute path is already an answer and is left alone.
func resolve(cwd, path string) string {
	if filepath.IsAbs(path) || cwd == "" {
		return path
	}
	return filepath.Join(cwd, path)
}
