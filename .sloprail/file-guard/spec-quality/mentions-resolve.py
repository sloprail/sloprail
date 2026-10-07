#!/usr/bin/env python3
"""Deterministic half: every invariant has a predicate and at least one typed mention, and
every mention resolves to an entity file (and a field it declares) as committed at head."""
import json, re, sys
import speclib as s

p = s.payload()
head = p["changeset"]["head"]
content = {f["path"]: f.get("newContent", "") for f in p["changeset"]["files"]}
problems = []
for path in p["subject"]["files"]:
    text = content.get(path, "")
    if not s.is_invariant(path):
        continue
    if not re.search(r"(?m)^predicate:", text):
        problems.append(f"{path}: no predicate")
    ms = s.mentions(text)
    if not ms:
        problems.append(f"{path}: the predicate names no {{@ent:...}} or {{@fld:...}}")
    for kind, dom, ent, fld in ms:
        epath = s.entity_path(dom, ent)
        etext = s.at_head(head, epath)
        if etext is None:
            problems.append(f"{path}: {{@{kind}:{dom}:{ent}...}} has no {epath}")
        elif kind == "fld" and not re.search(r"(?m)^\s*-\s*name:\s*" + re.escape(fld or "") + r"\s*$", etext):
            problems.append(f"{path}: {epath} declares no field {fld}")
if problems:
    print(json.dumps({"reason": "Spec mentions that do not resolve:\n" + "\n".join(problems)}))
    sys.exit(1)
