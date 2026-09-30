# content-de-layering (file-guard)

Each fact belongs in exactly one file: an update should reference a person
file rather than duplicate it inline, and strategy reasoning shouldn't leak
into branding copy.

## Install

Make sure sloprail is installed — see the docs
[install page](https://sloprail.com/docs/getting-started/install/).

Then copy this example into your project:

```bash
git clone --depth 1 https://github.com/sloprail/sloprail /tmp/sloprail-clone && mkdir -p .sloprail && cp -R /tmp/sloprail-clone/examples/content-de-layering/.sloprail/. .sloprail/ && rm -rf /tmp/sloprail-clone
```

Then write a change that duplicates content that already lives elsewhere,
and confirm you see the refusal below.

## The rule

Right content in the right file: an update references a person file, it
doesn't duplicate it inline; no strategy reasoning leaks into branding;
one-fact-one-home. Purely a judge call — there is no mechanical signature of
"this is a duplicate", only a reading of whether a fact stated here actually
belongs to this file's subject.

## Why file-guard

The guard is about the file's own content being right, independent of
whether this write created it or merely modified it — a file that
accumulated a duplicated fact over several unrelated edits is exactly as
wrong as one that got it in a single write. There is no gate here: there is no
cheap, reliable way to predict before the write whether new prose duplicates a
fact that lives elsewhere, so this guard is after-only.

It judges **commits**: at Stop, uncommitted changes to a file the rule selects
refuse the turn with "commit these" (nothing is committed for the agent), and the
rule then runs once over the range from where it last passed to `HEAD`, handed to
the judge as one squashed diff (`{{ change }}`) and the files (`changeset.files`).
A refused range is never partly passed, so a fix is judged together with the
commit it fixes.

## Why judge-only, no script tier

Unlike the other file-guard examples in this set, this rule has no
deterministic first cut. "Is this fact duplicated, or does it natively
belong here" is not a byte-comparable property — there is no string or
line-count signature that separates a legitimate reference from an
illegitimate restatement. The whole check is the judge.

## The tension this names on purpose

In direct tension with no-unasked-deletion by design: that rule refuses
removing content nobody asked to remove; this one actively demands removing
a specific class of content — a fact that demonstrably still lives
elsewhere. The resolution lives in the judge's answer: a de-layering removal
is only a pass if it names the surviving home the fact still lives in. A
removal that cannot point at where the fact remains is not de-layering, it's
unasked deletion wearing this rule's justification.
