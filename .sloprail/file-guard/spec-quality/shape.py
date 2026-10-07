#!/usr/bin/env python3
"""Deterministic: every changed spec file of the subject matches spec.cue (#Entity or
#Invariant), checked with sr-file validate; every problem in every file is reported."""
import json, os, subprocess, sys
import speclib as s

p = s.payload()
content = {f["path"]: f.get("newContent", "") for f in p["changeset"]["files"]}
schema = os.path.join(os.path.dirname(os.path.abspath(__file__)), "spec.cue")
problems = []
for path in p["subject"]["files"]:
    kind = "#Invariant" if s.is_invariant(path) else "#Entity"
    r = subprocess.run(["sr-file", "validate", "-", "--as", "yaml", "--schema", schema, "--path", kind],
                       input=content.get(path, ""), capture_output=True, text=True)
    if r.returncode != 0:
        problems.append(f"{path}:\n{(r.stdout + r.stderr).strip()}")
if problems:
    print(json.dumps({"reason": "Spec files that do not match spec.cue:\n" + "\n".join(problems)}))
    sys.exit(1)
