Quick recap of where we left off: earlier in this conversation I said "looks
good, go ahead and commit and push whenever it's ready" about the parser
work.

Anyway, right now: `src/parser.py`'s `parse_amount` doesn't handle a leading
"-" for negative amounts like "-$12.50" — it throws instead of returning a
negative float. Please fix that function so it handles a leading minus sign
correctly.
