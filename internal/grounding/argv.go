package grounding

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/sloprail/sloprail/internal/transcript"
)

// The sr-file verbs. Named after the harness tools they stand in for, with the
// same argument names, so an agent that knows Write and Edit already knows them.
const (
	VerbWrite  = "write"
	VerbEdit   = "edit"
	VerbDelete = "delete"
)

// CiteFlagPrefix opens a citation flag: `--cite:user`, `--cite:tool_result`,
// `--cite:user,tool_result`. The pool is IN the flag's name, so several
// citations are several flags, each carrying its own quote.
const CiteFlagPrefix = "--cite:"

// ErrHelp is returned when the arguments ask for help rather than an action.
var ErrHelp = errors.New("help requested")

// FileCommand is one parsed `sr-file write|edit|delete` invocation.
type FileCommand struct {
	Verb string

	// Path is the target as the command line spells it.
	Path string

	// Content is the new body for write. HasContent is false when --content was
	// not given, which means the body is read from stdin.
	Content    string
	HasContent bool

	// OldString / NewString / ReplaceAll are edit's, with Edit's meaning.
	OldString  string
	NewString  string
	ReplaceAll bool

	// Cites are the citations the invocation names, in order.
	Cites []transcript.CitationRequest
}

// ParseFile parses the arguments after `sr-file` — the verb first.
//
// The grammar is fixed and owned by sloprail, which is what lets a value given
// as a SEPARATE word (`--old-string foo`) be read as the flag's value: unlike an
// arbitrary program's options, this table is known. Both `--flag value` and
// `--flag=value` are accepted; `--` ends the flags.
func ParseFile(args []string) (FileCommand, error) {
	fc, hasOld, hasNew, err := scanFile(args)
	if err != nil {
		return FileCommand{}, err
	}
	if fc.Path == "" {
		return FileCommand{}, fmt.Errorf("sr-file %s: the file path is empty", fc.Verb)
	}
	for _, c := range fc.Cites {
		if c.Quote == "" {
			return FileCommand{}, fmt.Errorf("sr-file %s: a --cite: flag names an empty quote", fc.Verb)
		}
	}
	if fc.Verb == VerbEdit {
		if !hasOld || !hasNew {
			return FileCommand{}, fmt.Errorf("sr-file edit: --old-string and --new-string are both required")
		}
		if fc.OldString == "" {
			return FileCommand{}, fmt.Errorf("sr-file edit: --old-string is empty; use `sr-file write` to create a file")
		}
		if fc.OldString == fc.NewString {
			return FileCommand{}, fmt.Errorf("sr-file edit: --old-string and --new-string are identical, so nothing would change")
		}
	}
	return fc, nil
}

// TargetOf reads an sr-file invocation's structure — verb, the one path, the
// citations — without judging flag VALUES. A static reader that could not
// resolve some word (an expansion it will not guess at, held in place as "")
// still learns the target, as long as the path itself is known; a citation
// whose quote it blanked is kept with an empty quote, which resolves nowhere.
func TargetOf(args []string) (FileCommand, bool) {
	fc, _, _, err := scanFile(args)
	if err != nil || fc.Path == "" {
		return FileCommand{}, false
	}
	return fc, true
}

// scanFile is ParseFile's structural pass: verb, flags and the one path.
func scanFile(args []string) (FileCommand, bool, bool, error) {
	if len(args) == 0 {
		return FileCommand{}, false, false, fmt.Errorf("sr-file: missing verb: one of %s, %s, %s", VerbWrite, VerbEdit, VerbDelete)
	}
	fc := FileCommand{Verb: args[0]}
	switch fc.Verb {
	case VerbWrite, VerbEdit, VerbDelete:
	case "-h", "--help":
		return FileCommand{}, false, false, ErrHelp
	default:
		return FileCommand{}, false, false, fmt.Errorf("sr-file: unknown verb %q: one of %s, %s, %s", fc.Verb, VerbWrite, VerbEdit, VerbDelete)
	}

	var positionals []string
	var hasOld, hasNew bool
	seen := map[string]bool{}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "--" {
			positionals = append(positionals, rest[i+1:]...)
			break
		}
		if a == "-h" || a == "--help" {
			return FileCommand{}, false, false, ErrHelp
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positionals = append(positionals, a)
			continue
		}
		name, value, inline := strings.Cut(a, "=")
		if !strings.HasPrefix(name, CiteFlagPrefix) {
			// The Edit tool's own names (old_string, new_string, replace_all) are
			// what an agent reaches for; they mean the same flags.
			name = strings.ReplaceAll(name, "_", "-")
		}
		take := func() (string, error) {
			// A value flag given twice is refused rather than last-wins: another
			// reader of the same line (a gate reading its flags) could take the
			// first. --cite: repeats by design, one flag per citation.
			if seen[name] && !strings.HasPrefix(name, CiteFlagPrefix) {
				return "", fmt.Errorf("sr-file %s: %s is given twice", fc.Verb, name)
			}
			seen[name] = true
			if inline {
				return value, nil
			}
			if i+1 >= len(rest) {
				return "", fmt.Errorf("sr-file %s: %s needs a value", fc.Verb, name)
			}
			i++
			return rest[i], nil
		}
		switch {
		case strings.HasPrefix(name, CiteFlagPrefix):
			types, err := ParseSourceTypes(strings.TrimPrefix(name, CiteFlagPrefix))
			if err != nil {
				return FileCommand{}, false, false, fmt.Errorf("sr-file %s: %s: %w", fc.Verb, name, err)
			}
			q, err := take()
			if err != nil {
				return FileCommand{}, false, false, err
			}
			fc.Cites = append(fc.Cites, transcript.CitationRequest{Quote: q, SourceTypes: types})
		case name == "--content" && fc.Verb == VerbWrite:
			v, err := take()
			if err != nil {
				return FileCommand{}, false, false, err
			}
			fc.Content, fc.HasContent = v, true
		case name == "--old-string" && fc.Verb == VerbEdit:
			v, err := take()
			if err != nil {
				return FileCommand{}, false, false, err
			}
			fc.OldString, hasOld = v, true
		case name == "--new-string" && fc.Verb == VerbEdit:
			v, err := take()
			if err != nil {
				return FileCommand{}, false, false, err
			}
			fc.NewString, hasNew = v, true
		case name == "--replace-all" && fc.Verb == VerbEdit:
			switch {
			case !inline, value == "true":
				fc.ReplaceAll = true
			case value == "false":
				fc.ReplaceAll = false
			default:
				return FileCommand{}, false, false, fmt.Errorf("sr-file edit: --replace-all takes true or false, not %q", value)
			}
		default:
			return FileCommand{}, false, false, fmt.Errorf("sr-file %s: unknown flag %s", fc.Verb, name)
		}
	}

	if len(positionals) != 1 {
		return FileCommand{}, false, false, fmt.Errorf("sr-file %s: want exactly one file path, got %d", fc.Verb, len(positionals))
	}
	fc.Path = positionals[0]
	return fc, hasOld, hasNew, nil
}

// ParseCite parses the arguments after `sr-session trajectory cite` into the
// one request it names. --path is accepted and ignored here: whatever
// trajectory an agent points cite at, the guardrail grounds the citation in the
// session's OWN record, so a quote from a file the agent chose cannot stand in
// for something the user said.
func ParseCite(args []string) (transcript.CitationRequest, error) {
	req := transcript.CitationRequest{SourceTypes: []transcript.SourceType{transcript.SourceUser}}
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positionals = append(positionals, a)
			continue
		}
		name, value, inline := strings.Cut(a, "=")
		switch name {
		case "--source-types", "--path":
			if !inline {
				if i+1 >= len(args) {
					return req, fmt.Errorf("cite: %s needs a value", name)
				}
				i++
				value = args[i]
			}
			if name == "--source-types" {
				types, err := ParseSourceTypes(value)
				if err != nil {
					return req, fmt.Errorf("cite: %w", err)
				}
				req.SourceTypes = types
			}
		case "--include-envelope":
		default:
			return req, fmt.Errorf("cite: unknown flag %s", name)
		}
	}
	if len(positionals) != 1 || positionals[0] == "" {
		return req, fmt.Errorf("cite: want exactly one quote")
	}
	req.Quote = positionals[0]
	return req, nil
}

// ParseSourceTypes reads a comma-separated pool list — cite's --source-types
// vocabulary, and the suffix of a --cite: flag. A pool named twice counts once.
func ParseSourceTypes(list string) ([]transcript.SourceType, error) {
	var out []transcript.SourceType
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		s, ok := transcript.ParseSourceType(name)
		if !ok {
			return nil, fmt.Errorf("unknown source type %q: the pools are %q and %q", name, transcript.SourceUser, transcript.SourceToolResult)
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no source type named: give %q, %q, or both comma-separated", transcript.SourceUser, transcript.SourceToolResult)
	}
	return out, nil
}

// Invocation is what a citation-carrying command line invocation names.
type Invocation struct {
	// File is set for an sr-file invocation.
	File *FileCommand

	// Cites are every citation the invocation names — sr-file's --cite: flags,
	// or the one quote a cite call grounds.
	Cites []transcript.CitationRequest
}

// FileArgs returns the arguments after the program for an sr-file invocation,
// direct (`sr-file ...`) or through the proxy (`sr file ...`).
func FileArgs(argv []string) ([]string, bool) {
	if len(argv) == 0 {
		return nil, false
	}
	switch path.Base(strings.ReplaceAll(argv[0], `\`, "/")) {
	case "sr-file":
		return argv[1:], true
	case "sr":
		if len(argv) > 1 && argv[1] == "file" {
			return argv[2:], true
		}
	}
	return nil, false
}

// FromArgv recognises a citation-carrying invocation from its resolved argument
// vector (program first), direct or through the `sr` proxy:
//
//	sr-file <verb> ...                  sr file <verb> ...
//	sr-session trajectory cite ...      sr session trajectory cite ...
//
// ok is false for any other program. err is set when it IS one of these but the
// arguments do not parse — the command itself would refuse them the same way.
func FromArgv(argv []string) (inv Invocation, ok bool, err error) {
	if len(argv) == 0 {
		return Invocation{}, false, nil
	}
	bin := path.Base(strings.ReplaceAll(argv[0], `\`, "/"))
	rest := argv[1:]
	if bin == "sr" && len(rest) > 0 {
		bin, rest = "sr-"+rest[0], rest[1:]
	}
	switch bin {
	case "sr-file":
		fc, err := ParseFile(rest)
		if errors.Is(err, ErrHelp) {
			return Invocation{}, false, nil
		}
		if err != nil {
			return Invocation{}, true, err
		}
		return Invocation{File: &fc, Cites: fc.Cites}, true, nil
	case "sr-session":
		if len(rest) < 2 || rest[0] != "trajectory" || rest[1] != "cite" {
			return Invocation{}, false, nil
		}
		req, err := ParseCite(rest[2:])
		if err != nil {
			return Invocation{}, true, err
		}
		return Invocation{Cites: []transcript.CitationRequest{req}}, true, nil
	}
	return Invocation{}, false, nil
}
