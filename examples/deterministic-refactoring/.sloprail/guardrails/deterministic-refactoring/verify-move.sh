#!/bin/sh
# Verifies that a file declaring moved code really carries the source's bytes.
#
# Reads one PreFileCreate event on stdin. The event carries the path being
# written, the exact content that would land, and the `sr:` markers the engine
# found in that content — which is what makes this checkable with no state and
# no model: the claimed origin is a commit, the claimed result is in hand, and
# the comparison between them is a diff.
#
# The origin is read from a MARKER, not parsed out of the text. `markers` is an
# engine field: the scanner finds every `sr:<kind> <fqn>` line, and the matcher
# in GUARDRAIL.md selects on `any(markers, .kind == "moved-from")` — an
# expression the engine type-checks when the guardrail loads, so a typo inside
# the predicate is refused there rather than silently never firing. What this
# script keeps is the part markers do not do: reading the source out of git and
# comparing bytes.
#
# Refuses on anything it cannot verify. A marker it cannot parse, a commit it
# cannot reach, a line range outside the file at that commit — each is a refusal
# rather than a pass, because the alternative is a rule that waves through
# exactly the malformed cases an agent would produce when it had not really done
# the move.
set -u

PAYLOAD=$(cat)

refuse() {
	echo "$1" >&2
	exit 1
}

# The event's own fields, read with jq. The content is arbitrary source code and
# pattern-matching it out of JSON is how a check ends up parsing a brace in
# someone's code as the end of the payload.
command -v jq > /dev/null 2>&1 ||
	refuse "deterministic-refactoring: jq is not installed, so this rule cannot read the event it was handed. Refusing: a check that could not run has not passed."

TARGET=$(printf '%s' "$PAYLOAD" | jq -r '.event.fields.path // empty' 2> /dev/null)
[ -n "$TARGET" ] || refuse "This write carries no path, so the move it declares cannot be checked."

CONTENT=$(printf '%s' "$PAYLOAD" | jq -r '.event.fields.content // empty' 2> /dev/null)
[ -n "$CONTENT" ] || refuse "This write carries no content, so the move it declares cannot be checked."

# The origin markers, one fqn per line. Read off the event's `markers` field
# rather than scanned out of the text again: the engine already did that scan,
# and a second reader here would be a second grammar to keep in step with the
# first — the exact drift the markers abstraction exists to remove.
ORIGINS=$(printf '%s' "$PAYLOAD" | jq -r '
	[ .event.fields.markers // [] | .[] | select(.kind == "moved-from") | .fqn ] | .[]
' 2> /dev/null)

COUNT=$(printf '%s' "$ORIGINS" | grep -c . || true)
[ "$COUNT" -eq 1 ] ||
	refuse "A moved file must carry exactly one sr:moved-from marker naming its origin, and this one carries $COUNT. Write it with: sr-mark apply moved-from --<path>@<sha>:<start>-<end>=$TARGET:1"

ORIGIN=$(printf '%s' "$ORIGINS" | sed -n '1p')

# The renames, in the order the file carries them. Same source as the origin:
# a rename is a marker too, so both halves of the declaration come off the
# event and neither is parsed out of the text.
RENAMES=$(printf '%s' "$PAYLOAD" | jq -r '
	[ .event.fields.markers // [] | .[] | select(.kind == "moved-rename") | .fqn ] | .[]
' 2> /dev/null)

# The marker lines themselves are not part of what is compared. Which lines
# those are is on the event — `.line`, 1-based — so they are deleted by number
# rather than by re-matching the marker grammar here.
DROP=$(printf '%s' "$PAYLOAD" | jq -r '
	[ .event.fields.markers // []
	  | .[]
	  | select(.kind == "moved-from" or .kind == "moved-rename")
	  | .line
	] | sort | reverse | .[]
' 2> /dev/null)

# The project root. The hook runs with its own guardrail folder as the working
# directory, and the origin path is relative to the project — so the three
# levels back out of .sloprail/guardrails/<name> are what connect them.
ROOT=$(cd "$PWD/../../.." && pwd) || refuse "Could not locate the project root from the guardrail folder."

WORK=$(mktemp -d) || refuse "Could not create a working directory to verify the move."
trap 'rm -rf "$WORK"' EXIT

# Content as it would land. printf rather than echo: the content is arbitrary
# and echo would interpret backslashes in it on some shells.
printf '%s' "$CONTENT" > "$WORK/incoming"

cp "$WORK/incoming" "$WORK/body"
for n in $DROP; do
	sed "${n}d" "$WORK/body" > "$WORK/body.next" || refuse "Could not remove the marker lines from the incoming content."
	mv "$WORK/body.next" "$WORK/body"
done

# ---------------------------------------------------------------------------
# The origin: <path>@<sha>:<start>-<end>, all of it in the marker's fqn.
#
# The fqn is one URL-safe token, which is exactly enough to carry all four
# parts, and it is the only field that can carry them: `.line` is where the
# marker SITS, not an extent, and the engine will not answer where a function
# ends. Splitting on the LAST colon keeps a path containing one from taking the
# range with it; splitting on the LAST `@` before that does the same for the
# commit.
# ---------------------------------------------------------------------------
SPEC=${ORIGIN##*:}
LOCATOR=${ORIGIN%:*}
case "$SPEC" in
[0-9]*-[0-9]*) ;;
*) refuse "The sr:moved-from marker must end in <start>-<end> with both numbers present, as in: <path>@<sha>:12-40. Got: $ORIGIN" ;;
esac
START=${SPEC%%-*}
END=${SPEC##*-}

case "$LOCATOR" in
*@*) ;;
*) refuse "The sr:moved-from marker must pin the commit the code was taken from, as <path>@<sha>:<start>-<end>. A range alone names a file as it is NOW, so if the source moved on after the copy this check would compare against the wrong bytes and say nothing. Got: $ORIGIN" ;;
esac
SHA=${LOCATOR##*@}
SRC=${LOCATOR%@*}

[ -n "$SHA" ] || refuse "The sr:moved-from marker names an empty commit. Pin the sha the code was taken from: <path>@<sha>:<start>-<end>."

case "$SRC" in
"" | /* | *..*) refuse "The sr:moved-from marker must name a source path inside the project, relative to its root. Got: $SRC" ;;
esac

# ---------------------------------------------------------------------------
# The source, read out of git at the pinned commit rather than off the working
# tree.
#
# This is the whole reason the sha is there. A range alone describes a file as
# it is now: if the source was edited after the copy was taken, the working tree
# no longer holds the bytes that were moved, and a comparison against it fails a
# correct move — or, worse, passes a wrong one whose origin has drifted into
# agreement. `git show <sha>:<path>` compares against what was actually there.
#
# An unreachable commit REFUSES. It is the same posture as everything else here:
# the claim is about a specific commit, and a check that could not read that
# commit has established nothing. Falling back to the working tree would be the
# silent-pass this rule exists to prevent — the fallback would be taken exactly
# when the pin mattered most, which is when the sha names something this
# checkout does not have.
# ---------------------------------------------------------------------------
git -C "$ROOT" rev-parse --git-dir > /dev/null 2>&1 ||
	refuse "The sr:moved-from marker pins commit $SHA, but this project is not a git repository, so there is nothing to read that commit out of. Refusing: the origin claim cannot be checked."

git -C "$ROOT" cat-file -e "${SHA}^{commit}" 2> /dev/null ||
	refuse "The sr:moved-from marker names commit $SHA, but this repository has no such commit. Pin the commit the code was actually taken from — a sha this checkout cannot reach is a claim that cannot be checked, so it is refused rather than assumed."

if ! git -C "$ROOT" show "${SHA}:${SRC}" > "$WORK/src" 2> "$WORK/giterr"; then
	refuse "The sr:moved-from marker names $SRC at commit $SHA, but that commit has no such file. Name the file the code was actually taken from, as it was at that commit."
fi

TOTAL=$(awk 'END { print NR }' "$WORK/src")
if [ "$START" -lt 1 ] || [ "$END" -lt "$START" ] || [ "$END" -gt "$TOTAL" ]; then
	refuse "The sr:moved-from marker claims lines ${START}-${END} of $SRC at commit $SHA, but the file had $TOTAL lines at that commit. Declare the range the code was actually taken from."
fi

sed -n "${START},${END}p" "$WORK/src" > "$WORK/expect"

# Declared renames, applied in the order written. Each is validated as an
# identifier pair first: gsub takes its pattern as a regular expression, so a
# name containing metacharacters would match more than it appears to, and
# accepting one would mean claiming a verification that had not been performed.
#
# A rename marker's fqn is `<old>=<new>`. `=` is URL-safe, so the pair survives
# the fqn's own character rule intact.
printf '%s\n' "$RENAMES" > "$WORK/renames"

while IFS= read -r line; do
	[ -n "$line" ] || continue
	case "$line" in
	*=*) ;;
	*) refuse "An sr:moved-rename marker must read <old>=<new>. Got: $line" ;;
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
	echo "The code you are writing into $TARGET is not the code at ${SRC}:${START}-${END} as of commit ${SHA}."
	echo
	echo "A move must carry the source's bytes. What follows is the difference between"
	echo "the declared source (with any declared renames applied) and what you wrote;"
	echo "'-' lines are in the source and missing from your file, '+' lines are yours"
	echo "and are not in the source:"
	echo
	sed -n '3,40p' "$WORK/diff"
	echo
	echo "Copy the declared lines out of $SRC at ${SHA} unmodified — 'git show ${SHA}:${SRC}'"
	echo "is what this check read. If the code genuinely needs to change, land the move"
	echo "first and change it afterwards, where the diff shows it."
} >&2
exit 1
