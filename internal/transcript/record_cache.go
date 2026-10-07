package transcript

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// A citation run resolves many quotes over the same few records, and every
// resolution used to read and parse the whole record again — plus the two
// maps classifying it (otherToolUses, citableResults). One main session is tens
// of thousands of lines, so the cost was quotes × records × lines, with nothing
// reused between quotes.
//
// loadRecord parses a record once and hands every later caller the same result,
// for as long as the file is unchanged. The result is shared and READ-ONLY:
// callers copy an entry before altering it (ownWords does), never the slice.

// recordCacheBudget bounds the bytes of record files kept parsed. A record
// larger than the whole budget is parsed per call, as before.
const recordCacheBudget = 256 << 20

// parsedRecord is one record, read once.
type parsedRecord struct {
	entries []LinedEntry

	othersOnce sync.Once
	others     map[string]bool

	recCallsOnce sync.Once
	recCalls     map[string]recordCall

	callsOnce sync.Once
	calls     map[string]assistantContentBlock
}

// toolUsesOtherThanAsk is otherToolUses of the record, computed on first use.
func (r *parsedRecord) toolUsesOtherThanAsk() map[string]bool {
	r.othersOnce.Do(func() { r.others = otherToolUses(r.entries) })
	return r.others
}

// toolCalls is every tool_use block of the record by id, computed on first use.
func (r *parsedRecord) toolCalls() map[string]assistantContentBlock {
	r.callsOnce.Do(func() {
		r.calls = map[string]assistantContentBlock{}
		for _, e := range r.entries {
			if e.Type != EntryAssistant || len(e.Message) == 0 {
				continue
			}
			var msg assistantContent
			var blocks []assistantContentBlock
			if json.Unmarshal(e.Message, &msg) != nil || json.Unmarshal(msg.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				if b.Type == "tool_use" && b.ID != "" {
					r.calls[b.ID] = b
				}
			}
		}
	})
	return r.calls
}

// recordCalls is recordCalls of the record's entries, computed on first use.
func (r *parsedRecord) recordCalls() map[string]recordCall {
	r.recCallsOnce.Do(func() { r.recCalls = recordCalls(r.entries) })
	return r.recCalls
}

// citable is citableFor of the record at path.
func (r *parsedRecord) citable(path string) map[string]bool {
	return citableFor(path, r.entries)
}

type recordCell struct {
	once sync.Once
	size int64
	mod  time.Time
	rec  *parsedRecord
	err  error
}

var recordCache = struct {
	sync.Mutex
	cells map[string]*recordCell
	order []string // insertion order, oldest first
	bytes int64
}{cells: map[string]*recordCell{}}

// loadRecord is ReadLines of the record at path, parsed once while the file is
// unchanged (same size and modification time). Concurrent callers of one path
// share a single parse.
func loadRecord(path string) (*parsedRecord, error) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() > recordCacheBudget {
		return parseRecord(path)
	}

	recordCache.Lock()
	cell := recordCache.cells[path]
	if cell == nil || cell.size != fi.Size() || !cell.mod.Equal(fi.ModTime()) {
		if cell != nil {
			dropLocked(path, cell.size)
		}
		cell = &recordCell{size: fi.Size(), mod: fi.ModTime()}
		recordCache.cells[path] = cell
		recordCache.order = append(recordCache.order, path)
		recordCache.bytes += cell.size
		for recordCache.bytes > recordCacheBudget && len(recordCache.order) > 1 {
			oldest := recordCache.order[0]
			dropLocked(oldest, recordCache.cells[oldest].size)
		}
	}
	recordCache.Unlock()

	// The stat above was taken before the read, so a file that grew in between
	// is cached under the older stat and re-parsed on the next call: never
	// stale content under a newer stat.
	cell.once.Do(func() { cell.rec, cell.err = parseRecord(path) })
	return cell.rec, cell.err
}

// dropLocked forgets path's cell. recordCache must be locked.
func dropLocked(path string, size int64) {
	delete(recordCache.cells, path)
	recordCache.bytes -= size
	for i, p := range recordCache.order {
		if p == path {
			recordCache.order = append(recordCache.order[:i], recordCache.order[i+1:]...)
			break
		}
	}
}

func parseRecord(path string) (*parsedRecord, error) {
	entries, err := ReadLines(path)
	if err != nil {
		return nil, err
	}
	return &parsedRecord{entries: entries}, nil
}
