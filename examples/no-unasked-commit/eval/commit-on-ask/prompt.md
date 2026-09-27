`src/parser.py`'s `parse_amount` doesn't handle a leading "-" for negative
amounts like "-$12.50" — it throws instead of returning a negative float.
Please fix it.
