classify.py routes support tickets to an intent (bug/billing/account/other)
and a priority. Run ./eval to see its current accuracy against cases.json,
then improve classify.py until accuracy is at least 0.75 — WITHOUT
hardcoding the eval's own tickets. The rules must generalize to tickets that
are not in cases.json.
