package transcript

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadNormalisesTheFieldsKept checks the translation itself: what Claude
// Code spells, arriving under the canonical names, with nothing else carried
// through.
func TestReadNormalisesTheFieldsKept(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		`{"type":"user","uuid":"u1","parentUuid":null,"isSidechain":false,`+
			`"timestamp":"2026-08-13T09:00:00.000Z","message":{"role":"user","content":"go"},`+
			`"sessionId":"raw","version":"9.9.9","gitBranch":"main","requestId":"req_1"}`,
		`{"type":"assistant","uuid":"u2","parentUuid":"u1","isSidechain":true,`+
			`"timestamp":"2026-08-13T09:00:01.000Z","message":{"role":"assistant","content":"done"},`+
			`"toolUseResult":{"stdout":"ok"}}`,
	)

	entries, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Read returned %d entries, want 2", len(entries))
	}

	first := entries[0]
	if first.Type != EntryUser || first.UUID != "u1" || first.ParentUUID != "" {
		t.Fatalf("first entry = %+v, want a user entry u1 with no parent", first)
	}
	if first.Timestamp != "2026-08-13T09:00:00.000Z" || first.IsSidechain {
		t.Fatalf("first entry = %+v, want its timestamp kept and IsSidechain false", first)
	}
	if !strings.Contains(string(first.Message), `"content":"go"`) {
		t.Fatalf("first entry's message = %s, want the message carried through", first.Message)
	}

	second := entries[1]
	if second.Type != EntryAssistant || second.ParentUUID != "u1" || !second.IsSidechain {
		t.Fatalf("second entry = %+v, want an assistant entry parented on u1 and marked a sidechain", second)
	}
	if !strings.Contains(string(second.ToolUseResult), `"stdout":"ok"`) {
		t.Fatalf("second entry's tool result = %s, want what the tool returned", second.ToolUseResult)
	}
}

// TestEntryCarriesNothingBeyondTheShape is the field-discipline check: every
// field kept is one each new harness must be normalised into, so an entry
// serialises to those and nothing else. Round-tripping is how a field added
// without thinking gets noticed.
func TestEntryCarriesNothingBeyondTheShape(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session", root("u1"))
	entries, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	blob, err := json.Marshal(entries[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	allowed := map[string]bool{
		"type": true, "uuid": true, "parentUuid": true, "logicalParentUuid": true,
		"timestamp": true, "isSidechain": true, "message": true, "toolUseResult": true,
	}
	for k := range got {
		if !allowed[k] {
			t.Fatalf("an entry carries %q, which no rule asks about — every field kept is one each new harness must be normalised into", k)
		}
	}
}

// TestReadSkipsRecordsWithoutAUUID: Claude Code writes preamble and bookkeeping
// lines — custom-title, ai-title, mode, queue-operation, last-prompt — that
// describe the session rather than anything that happened in it, and carry no
// uuid at all. They cannot take part in a chain and hold nothing to ask about.
func TestReadSkipsRecordsWithoutAUUID(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		`{"type":"custom-title","customTitle":"x","sessionId":"s"}`,
		`{"type":"ai-title","aiTitle":"y","sessionId":"s"}`,
		`{"type":"mode","mode":"default","sessionId":"s"}`,
		`{"type":"queue-operation","sessionId":"s"}`,
		root("u1"),
	)

	entries, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 1 || entries[0].UUID != "u1" {
		t.Fatalf("Read returned %d entries (%+v), want only the one carrying a uuid", len(entries), entries)
	}
}

// TestReadKeepsAKindItDoesNotKnow: a harness writes kinds beyond the three a
// rule is usually written against — Claude Code emits `attachment`, among
// others. Renaming those to "something else" would lose which one it was.
func TestReadKeepsAKindItDoesNotKnow(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		`{"type":"attachment","uuid":"u1","parentUuid":null,"isSidechain":false,"timestamp":"t"}`,
	)

	entries, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if entries[0].Type != EntryType("attachment") {
		t.Fatalf("entry type = %q, want the harness's own name for it", entries[0].Type)
	}
}

// TestReadSkipsALineItCannotParse: the format is read by observation and
// promised by nobody, so one unrecognised line is the harness having changed
// something — a reason to keep reading the rest, not to refuse to answer.
func TestReadSkipsALineItCannotParse(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		root("u1"),
		"{ this is not json",
		record("u2", "u1"),
	)

	entries, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Read returned %d entries, want the two readable ones", len(entries))
	}
}

// TestReadHandlesARecordPastTheDefaultScanBuffer: one record carries a whole
// tool result, which for a file read or a long command runs far past a
// scanner's default 64KiB. A smaller bound would not fail loudly — it would
// silently stop reading mid-conversation.
func TestReadHandlesARecordPastTheDefaultScanBuffer(t *testing.T) {
	p := newProject(t)
	huge := strings.Repeat("x", 300*1024)
	path := p.write("a-session",
		root("u1"),
		`{"type":"user","uuid":"u2","parentUuid":"u1","isSidechain":false,"timestamp":"t",`+
			`"toolUseResult":{"stdout":"`+huge+`"}}`,
	)

	entries, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("Read returned %d entries, want 2 — a long tool result truncated the read", len(entries))
	}
}

// TestReadFailsWhenTheFileIsMissing: an absent record is a broken environment,
// not a session that did nothing.
func TestReadFailsWhenTheFileIsMissing(t *testing.T) {
	p := newProject(t)
	if _, err := Read(filepath.Join(p.dir, "never-written.jsonl")); err == nil {
		t.Fatal("reading a missing transcript must be an error")
	}
}
