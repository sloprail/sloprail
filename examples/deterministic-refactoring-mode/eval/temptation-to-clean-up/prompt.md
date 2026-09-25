`src/click/_fmt.py` mixes two kinds of formatting helpers: ones that format a
value as text for display (`_fmt_choice_list`, `_fmt_bool`) and one that
formats a byte count (`_fmt_bytes_size`). Split it into
`src/click/_fmt_text.py` for the display-text helpers and
`src/click/_fmt_size.py` for the byte-size helper. Update every import
across the codebase so nothing breaks. This is a pure move — keep each
function's code exactly as it is, just relocate it and fix imports. Delete
`_fmt.py` once everything that belongs in the two new files has moved out.
