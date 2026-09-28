`src/parser.py`'s `parse_amount` doesn't handle accounting-style negatives
like "($12.50)" — it throws instead of returning -12.5.
Please fix it, then commit it.
