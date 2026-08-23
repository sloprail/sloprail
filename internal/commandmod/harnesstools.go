package commandmod

// The harness tool names this package and filemod trust to name a file write
// or run a shell command, and the argument for gating on them despite
// extractPending's own case against it.
//
// # This is NOT the same list as knownBins, and the two are not interchangeable
//
// knownbin.go allowlists POSIX utilities — rm, cp, mv, dd — found INSIDE a
// shell command line, and its own doc comment argues at length for why that
// is safe: those names are specified by POSIX, unrenameable by any vendor
// without breaking every shell script in existence, so the drift risk that
// makes a HARNESS tool name dangerous to allowlist does not transfer to them.
//
// The names below are the opposite case: HarnessWriteTools and
// HarnessCommandTools name harness TOOLS — Write, Edit, Bash — which are one
// vendor's product decision, changed on that vendor's own schedule with no
// notice owed to anyone. Claude Code renamed Task to Agent in v2.1.63 with no
// warning; the identical thing could happen to Write tomorrow. This file
// allowlists that vocabulary anyway, so the argument for doing so has to be
// made on its own rather than borrowed from knownBins.
//
// # Why the allowlist, given the drift risk is real and not denied
//
// extractPending used to dispatch on argument SHAPE precisely to avoid this
// exposure — a call carrying `content` or `old_string`/`new_string` is a
// write whatever the tool is named, so a rename cost nothing. That protected
// against a write going unnoticed. It also meant a call carrying `file_path`
// and NO write-shaped key — Read's shape — could not be told apart from a
// notebook edit whose bytes are real but underivable, and a preventive
// file-guard bound to PreFileCreate/PreFileUpdate refused the READ itself.
// See pendingArgs.statesAWrite (filemod/pendingshape.go) for that fix's own
// account, and extractPending's doc comment for the measured repro.
//
// This project's choice, made explicitly rather than inferred: the harness
// tool name IS now the authoritative signal, maintained by hand, in exchange
// for a guarantee shape inference cannot give — a tool not on this list
// produces NO file/command event, full stop, rather than a best-effort
// guess from its arguments. A write tool renamed and not added here goes
// silent until the list is updated. That is the accepted cost, taken on
// knowingly: this engine is operated by the people who choose which harness
// it watches, and keeping a short, named list current on a rename is judged
// the smaller and more honest cost against inferring "is this a write" from
// argument shape for every call, forever, with the Read-refusal class of
// defect it can produce.
//
// # What "sole gate" means here
//
// There is no shape fallback for an unrecognised tool. A tool not in
// HarnessWriteTools produces no PreFileCreate/PreFileUpdate however its
// arguments are shaped, and a tool not in HarnessCommandTools is never
// parsed as a command line. A fast-path-plus-fallback design — trust a
// listed tool immediately, but still infer from shape for an unlisted one —
// was considered and rejected before this file was written: it would have
// meant the list only ever adds precision and never enforces the discipline
// of keeping it current, since an unlisted tool would still get SOME
// coverage from shape alone. This is deliberately more exposed to a silent
// rename than that alternative would have been, by choice: a sole gate makes
// a missed rename visible the first time an author notices a guardrail did
// not fire, which is the pressure that keeps the list honest.
//
// # Maintaining this list
//
// Add a tool here the same day its guardrail-relevant behaviour ships or
// changes — a new write-capable tool, or a rename of an existing one. This
// is now this project's own obligation. There is no shape-based backstop to
// catch what the list misses.

// HarnessWriteTools names every tool whose arguments state a file write,
// under the shape pendingArgs already models: content, old_string/new_string,
// edits[], or a notebook edit's new_source.
//
// Read is deliberately absent — see statesAWrite and extractPending's doc
// comment for the defect its absence fixes.
var HarnessWriteTools = map[string]bool{
	"Write":        true,
	"Edit":         true,
	"MultiEdit":    true,
	"NotebookEdit": true,
}

// HarnessCommandTools names every tool whose arguments are a shell command
// line — the shape both commandmod.Extract and filemod.extractCommand read
// out of a `command` key.
var HarnessCommandTools = map[string]bool{
	"Bash": true,
}
