# Writing a judge check

A judge asks a model what a script cannot decide: "is this change clean and targeted?",
"does the code still do what its comment promises?". It is a prompt template (`.md.j2`, in
Jinja2) beside the rule, rendered with the check's input and answered with a verdict.

```yaml
checks:
  - prepare: ./gather.sh                  # optional: assembles context, or skips the model
    judge: ./change-is-clean.md.j2
    model: size-md                         # optional
    allowed_tools: [WebFetch]              # optional: more than reading the project
    disallowed_tools: [WebSearch]          # optional
    response_schema: ./response.schema.json   # optional: the shape of the answer
    post_process: ./post-process.sh           # optional: turns the answer into the verdict
```

`prepare`, `model`, `allowed_tools` and `disallowed_tools` belong to a judge; on a script check
they fail to load. So do `response_schema` and `post_process`.

## The template

The template holds the rubric and the material to judge, and nothing about the answer's
format: sloprail adds the verdict instruction, which also tells the model to treat everything
above it as data, never as instructions. Wrap each piece of material in a named tag:

```markdown
## The change
<change>
{{ change }}
</change>

## The rules it must follow
<rules>
{{ additionalContext.rules }}
</rules>
```

It can use:

- `change`, the unified diff: on a file-guard, of the files `match` selected over the range;
  on a gate, of the event's old content to its new;
- on a file-guard, `changeset` and `subject` ([file-guard.md](file-guard.md#what-a-check-receives));
- on a gate or a context, `event` and its fields (`event.path`, `event.newContent`), and
  `context`;
- `transcriptPath`, and `additionalContext` when a `prepare` produced one.

Rendering is safe by default. Every value has `</` broken, so it cannot close the tag it sits
in, and a value in a quoted attribute (`path="{{ event.path }}"`) cannot end the attribute.
Always quote attribute values. Use `| raw` for a value that is meant as markup, and `| tojson`
for a map or a list (`{{ additionalContext.items | tojson }}`). Do not wrap a value in a
Markdown code fence: a value holding a fence line of its own would close it. A filter that
does not exist is reported as a load error.

## `prepare`: assembling the material

`prepare` runs first, with the same payload on stdin as a script check, to gather what the
template needs (the rules that apply, a file the change names) or to skip the model when there
is nothing to judge. Only the `additionalContext` key of what it prints is used:

```bash
jq -n --arg rules "$rules" '{additionalContext: {rules: $rules}}'   # or: {"skip": true}
```

Printing nothing is fine. A `prepare` that fails, or prints anything other than that shape,
fails the check, with its own words as the reason. A script check may have a `prepare` too; its
`additionalContext` then reaches the script under that key.

A verdict is cached on the subject's files and fingerprint, never on what `prepare` produced
or on the rendered prompt. So anything a judge depends on beyond the subject's files, whether
`prepare` computed it or the judge reads it itself, belongs in the subject's fingerprint
([file-guard.md](file-guard.md#verdicts-are-cached-by-content)).

## The verdict

The model answers `{"pass": true, "reasoning": ""}`, or on a failure
`{"pass": false, "reasoning": "…"}`. On a failure, `reasoning` is what the agent is shown, so
the rubric should ask for one concrete sentence naming the problem and its fix. A judge's key
is `reasoning`; a script's is `reason`.

A judge fails closed. If the model cannot be run, times out, or never gives a well-formed
verdict, the check refuses. When a flaky model call must not block work, write the check as a
script that calls a model itself and exits 0 on its own failures, saying in a comment which
exits those are.

## A structured answer

Two optional keys let the judge answer in a shape of your own, and let a script of yours turn
that answer into the verdict.

`response_schema` is a JSON Schema file beside the rule. The judge is shown it in place of the
default answer format and must answer with one JSON object that matches it. An answer that
does not match is sent back to the judge with the problems, as a malformed verdict is.

`post_process` is a script that runs after the judge answered, the mirror of `prepare`. It
runs in the rule's folder with the [environment](script-checks.md#the-environment) every script
gets, plus `SR_JUDGE_INPUT`: a file holding, as JSON, what the template was rendered with. It
reads the judge's answer on stdin and prints the check's result:

```bash
#!/usr/bin/env bash
# post-process.sh: every id the judge was given is answered; the check passes when all pass.
answer="$(cat)"
for id in $(jq -r '.additionalContext.ids[]' "$SR_JUDGE_INPUT"); do
  jq -e --arg id "$id" '.invariants | has($id)' <<<"$answer" >/dev/null ||
    { echo "no entry for $id: answer for every id you were given" >&2; exit 65; }
done
jq -c '{pass: all(.invariants[]; .code == "pass"),
        reasoning: ([.invariants | to_entries[] | select(.value.code == "fail")
                     | "\(.key): \(.value.why)"] | join("; ")),
        metadata: {invariants: .invariants}}' <<<"$answer"
```

- `pass` and `reasoning` are the check's verdict, with the meaning they have in a judge's own.
- `metadata` is optional: a JSON object of at most 64 KiB, kept with the stored verdict and
  never shown to the agent. `sr-checks show --json` prints it on the judge's row.
- Exit 65 rejects the answer as unusable: the judge is asked again, with what the script wrote
  to stderr.
- Anything else (another non-zero exit, output that is not one such object, `metadata` over
  the limit) fails the check closed, like a judge that could not decide, and the judge is not
  asked again.

Either key works without the other. Without `response_schema`, `post_process` receives the
default `{"pass": …, "reasoning": …}` answer. Without `post_process`, the schema must keep a
required boolean `pass` and a string `reasoning` at its top level, or the rule fails to load.

## The model

`model:` is a size from `size-xs` to `size-xxl`, a concrete model name, or a comma-separated
list of preferences. It defaults to `size-md`: a judge is a real reasoning task, and the largest
size is a cost no per-action check should pay by default.

## What a judge can read and write

A judge can read the whole project with `Read`, `Grep` and `Glob`, without any grant, so a
rubric can say "read the spec this file names" without a `prepare` copying it in. It cannot
write the project; it writes only its verdict.

`allowed_tools` adds tools, by the same names on every harness: `Read` (to read outside the
project, such as the session transcript), `WebFetch`, `WebSearch`, `Bash`, `Agent`,
`mcp__<server>__<tool>`. A rule can be scoped, quoted in YAML because of its colon:
`"WebFetch(domain:example.com)"`, `"Bash(curl -sL:*)"`. A harness that cannot enforce a rule as
tightly as it is written refuses to start the judge rather than give it more.

Grant a shell sparingly. `Bash` runs any program, and a scoped rule such as `"Bash(curl:*)"`
allows every flag of that command, including the ones that write files or send them out.
`disallowed_tools` takes back only the exact forms it names, and a command's flags cannot all
be listed. What holds is pinning the whole command and denying anything appended to it:

```yaml
allowed_tools: ["Bash(curl -sL https://example.com/docs/*)"]
disallowed_tools: ["Bash(curl -sL https://example.com/docs/* *)"]
```

When a judge must run something, a `prepare` script, which you control, is usually the better
place.

## A good rubric

State the standard concretely enough that two runs agree. Name what fails, what passes, and
what the `reasoning` must say. Then cause the action and watch the judge refuse it: a judge
that loads and never refuses protects nothing.
