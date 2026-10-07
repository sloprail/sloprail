#!/usr/bin/env bash
# Mock judge: logs how many files its bucket hands it to judge (the <file> tags between the two
# headings of the rendered prompt), then passes.
awk '/^## The files to judge/{on=1} /^## Entities they cite/{on=0} on && /<file path=/{n++} END{print n+0}' >> "$JUDGE_LOG"
echo '{"pass": true, "reasoning": "mock"}'
