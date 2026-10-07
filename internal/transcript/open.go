package transcript

import (
	"io"
	"os"
	"time"

	"github.com/sloprail/sloprail/internal/harness"
)

// openRecord opens the record at path as the engine reads it: the harness's file, or,
// for a harness that keeps something beside it (harness.RecordOpener: Cursor's tool
// results), the merged stream. Every reader of a record goes through here, so physical
// line numbers (what a citation's `<path>:<line>` names) mean the same everywhere.
func openRecord(path string) (io.ReadCloser, error) {
	if o, ok := harness.Current().Transcripts().(harness.RecordOpener); ok {
		return o.OpenRecord(path)
	}
	return os.Open(path)
}

// recordVersion is the size and modification time that identify the record's current
// content: the file's own, or the harness's (harness.RecordOpener.RecordVersion).
func recordVersion(path string) (int64, time.Time, error) {
	if o, ok := harness.Current().Transcripts().(harness.RecordOpener); ok {
		return o.RecordVersion(path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, time.Time{}, err
	}
	return fi.Size(), fi.ModTime(), nil
}
