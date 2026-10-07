package transcript

import (
	"bytes"
	"io"
)

// A quote is looked for in a record's text only after its raw bytes say the
// quote could be there. citeNeedle is the part of a quote that a record's raw
// JSON must contain verbatim whenever the quote is in the record's text: if the
// needle is absent from the bytes, no entry of the record can match, and nothing
// has to be parsed to know it.
//
// A needle is a run of characters JSON never writes differently from the text it
// encodes — printable ASCII bar `"` and `\` (escaped), and `<`, `>`, `&`, `/`
// (some encoders escape them) — and no whitespace, which containsWords folds. The
// longest such run is the most selective. Every text is searched as one decoded
// string, so a run never spans two of them. Too short a run selects nothing:
// there is no needle, and the caller searches everything.
//
// The needle only rules records and entries OUT. What stays in is still decided
// by the exact match, so a needle can cost a missed shortcut, never a wrong
// answer.
func citeNeedle(quote string) []byte {
	var best, run []byte
	flush := func() {
		if len(run) > len(best) {
			best = append(best[:0], run...)
		}
		run = run[:0]
	}
	for i := 0; i < len(quote); i++ {
		c := quote[i]
		if c < 0x21 || c > 0x7e || bytes.IndexByte([]byte(`"\<>&/`), c) >= 0 {
			flush()
			continue
		}
		run = append(run, c)
	}
	flush()
	if len(best) < minNeedle {
		return nil
	}
	return best
}

const minNeedle = 4

// mayContain reports whether raw JSON could hold a quote with this needle. No
// needle means every record might.
func mayContain(raw []byte, needle []byte) bool {
	return needle == nil || bytes.Contains(raw, needle)
}

// fileMayContain is mayContain over a file, streamed so a record is never held
// whole. A file that cannot be read might contain it: the real search reports
// why.
func fileMayContain(path string, needle []byte) bool {
	if needle == nil {
		return true
	}
	f, err := openRecord(path)
	if err != nil {
		return true
	}
	defer f.Close()
	const chunk = 1 << 20
	buf := make([]byte, chunk+len(needle))
	keep := 0
	for {
		n, err := f.Read(buf[keep:])
		if n > 0 {
			window := buf[:keep+n]
			if bytes.Contains(window, needle) {
				return true
			}
			keep = min(len(needle)-1, len(window))
			copy(buf, window[len(window)-keep:])
		}
		if err == io.EOF {
			return false
		}
		if err != nil {
			return true
		}
	}
}
