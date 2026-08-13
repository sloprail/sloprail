#!/bin/sh
# Verifies that a file declaring moved code really carries the source's bytes.
#
# Reads one PreFileCreate event on stdin. The event carries the path being
# written and the exact content that would land, which is what makes this
# checkable with no state and no model: the claimed source is on disk, the
# claimed result is in hand, and the comparison between them is a diff.
#
# Refuses on anything it cannot verify. A header it cannot parse, a source file
# it cannot read, a line range outside the file — each is a refusal rather than
# a pass, because the alternative is a rule that waves through exactly the
# malformed cases an agent would produce when it had not really done the move.
set -u

PAYLOAD=$(cat)

# The event's own fields. Extracted with a JSON reader rather than by pattern:
# content is arbitrary source code, and pattern-matching it out of JSON is how
# a check ends up parsing a brace in someone's code as the end of the payload.
read_field() {
	printf '%s' "$PAYLOAD" | awk -v field="$1" '
		BEGIN { RS = "\0" }
		{ body = $0 }
		END {
			# The key, then optional whitespace, then the opening quote. Matched
			# rather than assumed adjacent: whether an encoder puts a space after
			# the colon is its own business, and a reader that depended on one
			# encoders formatting would work here and fail on a payload that is
			# equally valid JSON.
			key = "\"" field "\"[ \t\r\n]*:[ \t\r\n]*\""
			i = match(body, key)
			if (i == 0) { exit 1 }
			rest = substr(body, i + RLENGTH)
			out = ""
			j = 1
			while (j <= length(rest)) {
				c = substr(rest, j, 1)
				if (c == "\\") {
					n = substr(rest, j + 1, 1)
					if (n == "n") { out = out "\n" }
					else if (n == "t") { out = out "\t" }
					else if (n == "r") { out = out "\r" }
					else if (n == "\\") { out = out "\\" }
					else if (n == "\"") { out = out "\"" }
					else if (n == "/") { out = out "/" }
					else if (n == "u") {
						code = substr(rest, j + 2, 4)
						out = out sprintf("%c", strtonum("0x" code))
						j = j + 4
					}
					else { out = out n }
					j = j + 2
					continue
				}
				if (c == "\"") { break }
				out = out c
				j = j + 1
			}
			printf "%s", out
		}'
}

refuse() {
	echo "$1" >&2
	exit 1
}

TARGET=$(read_field path) || refuse "This write carries no path, so the move it declares cannot be checked. Write the file with an explicit path."
CONTENT=$(read_field content) || refuse "This write carries no content, so the move it declares cannot be checked."

# The project root. The hook runs with its own guardrail folder as the working
# directory, and the declared source path is relative to the project — so the
# three levels back out of .sloprail/guardrails/<name> are what connect them.
ROOT=$(cd "$PWD/../../.." && pwd) || refuse "Could not locate the project root from the guardrail folder."

WORK=$(mktemp -d) || refuse "Could not create a working directory to verify the move."
trap 'rm -rf "$WORK"' EXIT

# Content as it would land. printf rather than echo: the content is arbitrary
# and echo would interpret backslashes in it on some shells.
printf '%s' "$CONTENT" > "$WORK/incoming"

# The header block: the leading run of sloprail: comment lines. Counted rather
# than assumed, because the number of rename lines varies and the body starts
# after all of them.
HEADER_LINES=$(awk '
	/^(\/\/|#)[ \t]*sloprail:(moved-from|rename)[ \t]/ { n = NR; next }
	{ exit }
	END { print n + 0 }' "$WORK/incoming")

if [ "$HEADER_LINES" -eq 0 ]; then
	refuse "This file mentions sloprail:moved-from but not as a header line at the top. Put the provenance header on the first line, in the form: // sloprail:moved-from <path>:<start>-<end>"
fi

sed -n "1,${HEADER_LINES}p" "$WORK/incoming" > "$WORK/header"
sed "1,${HEADER_LINES}d" "$WORK/incoming" > "$WORK/body"

# The directive's value: everything after the keyword, with surrounding space
# trimmed. Done in awk rather than sed because the two sed dialects disagree
# about alternation, and a header line is matched by one of two comment markers.
directive() {
	awk -v want="$1" '
		{
			line = $0
			sub(/^[ \t]*/, "", line)
			if (substr(line, 1, 2) == "//") { line = substr(line, 3) }
			else if (substr(line, 1, 1) == "#") { line = substr(line, 2) }
			else { next }
			sub(/^[ \t]*/, "", line)
			key = "sloprail:" want
			if (substr(line, 1, length(key)) != key) { next }
			rest = substr(line, length(key) + 1)
			if (rest !~ /^[ \t]/) { next }
			sub(/^[ \t]+/, "", rest)
			sub(/[ \t]+$/, "", rest)
			print rest
		}' "$WORK/header"
}

ORIGIN=$(directive moved-from | sed -n '1p')
[ -n "$ORIGIN" ] && [ "$(directive moved-from | grep -c .)" -eq 1 ] ||
	refuse "The provenance header must name exactly one origin, in the form: // sloprail:moved-from <path>:<start>-<end>"

# path:start-end. Split from the right, so a source path containing a colon
# does not take the range with it.
SPEC=${ORIGIN##*:}
SRC=${ORIGIN%:*}
case "$SPEC" in
[0-9]*-[0-9]*) ;;
*) refuse "The provenance header's line range must read <start>-<end> with both numbers present, as in: // sloprail:moved-from ${SRC}:12-40" ;;
esac
START=${SPEC%%-*}
END=${SPEC##*-}

case "$SRC" in
"" | /* | *..*) refuse "The provenance header must name a source path inside the project, relative to its root. Got: $SRC" ;;
esac

[ -f "$ROOT/$SRC" ] || refuse "The provenance header names $SRC as the source, but there is no such file in the project. Name the file the code was actually taken from, relative to the project root."

TOTAL=$(awk 'END { print NR }' "$ROOT/$SRC")
if [ "$START" -lt 1 ] || [ "$END" -lt "$START" ] || [ "$END" -gt "$TOTAL" ]; then
	refuse "The provenance header claims lines ${START}-${END} of $SRC, but that file has $TOTAL lines. Declare the range the code was actually taken from."
fi

sed -n "${START},${END}p" "$ROOT/$SRC" > "$WORK/expect"

# Declared renames, applied in the order written. Each is validated as an
# identifier first: gsub takes its pattern as a regular expression, so a name
# containing metacharacters would match more than it appears to, and accepting
# one would mean claiming a verification that had not been performed.
directive rename > "$WORK/renames"

while IFS= read -r line; do
	[ -n "$line" ] || continue
	case "$line" in
	*=*) ;;
	*) refuse "A rename must read <old>=<new>. Got: $line" ;;
	esac
	OLD=${line%%=*}
	NEW=${line#*=}
	for name in "$OLD" "$NEW"; do
		printf '%s' "$name" | grep -Eq '^[A-Za-z_][A-Za-z0-9_]*$' ||
			refuse "A declared rename may only name an identifier ([A-Za-z_][A-Za-z0-9_]*), because the substitution is a pattern replacement and anything else would match more than it names. Got: $name"
	done
	awk -v f="$OLD" -v t="$NEW" '{ gsub(f, t); print }' "$WORK/expect" > "$WORK/expect.next"
	mv "$WORK/expect.next" "$WORK/expect"
done < "$WORK/renames"

# The comparison. sed's output always ends in a newline, so the incoming body is
# compared on the same footing: a body whose final line is unterminated differs
# from the source only in a way the writer cannot express, and holding it
# against them would refuse correct moves for a reason they could not fix.
if [ -s "$WORK/body" ] && [ "$(tail -c 1 "$WORK/body" | wc -l)" -eq 0 ]; then
	printf '\n' >> "$WORK/body"
fi

if diff -u "$WORK/expect" "$WORK/body" > "$WORK/diff"; then
	exit 0
fi

{
	echo "The code you are writing into $TARGET is not the code at ${SRC}:${START}-${END}."
	echo
	echo "A move must carry the source's bytes. What follows is the difference between"
	echo "the declared source (with any declared renames applied) and what you wrote;"
	echo "'-' lines are in the source and missing from your file, '+' lines are yours"
	echo "and are not in the source:"
	echo
	sed -n '3,40p' "$WORK/diff"
	echo
	echo "Copy the declared lines out of $SRC unmodified. If the code genuinely needs to"
	echo "change, land the move first and change it afterwards, where the diff shows it."
} >&2
exit 1
