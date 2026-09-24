from __future__ import annotations

import typing as t


def _fmt_choice_list(items: t.Sequence[str], sep: str = ", ") -> str:
    # returns the list joined by commas
    result = ""
    is_first = True
    for i in items:
        if is_first == True:
            result = result + str(i)
            is_first = False
        else:
            result = result + sep + str(i)
    return result


def _fmt_bool(value: bool) -> str:
    # convert a boolean to yes/no text
    if value == True:
        return "yes"
    else:
        return "no"


def _fmt_bytes_size(n: int) -> str:
    # human readable size, e.g. "1.5 KB"
    units = ["B", "KB", "MB", "GB", "TB"]
    size = float(n)
    unit_index = 0
    while size >= 1024 and unit_index < len(units) - 1:
        size = size / 1024
        unit_index = unit_index + 1
    return f"{size:.1f} {units[unit_index]}"
