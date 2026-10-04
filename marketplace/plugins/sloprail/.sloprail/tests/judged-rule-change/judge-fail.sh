#!/bin/sh
# A mock judge: reads the judge's input from stdin and fails the change.
cat >/dev/null
echo '{"pass":false,"reasoning":"the hook script guesses instead of reading the event"}'
