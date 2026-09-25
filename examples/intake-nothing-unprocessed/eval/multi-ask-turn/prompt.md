Two things: percent_change in calc.py throws a ZeroDivisionError when old
is 0 — fix that so it returns something sane instead of crashing. Separately,
can you also look into whether we should upgrade the requests dependency in
requirements.txt? It's pinned pretty old.
