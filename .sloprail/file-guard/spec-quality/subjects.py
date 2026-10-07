#!/usr/bin/env python3
"""One subject per bucket of changed spec files (see speclib.buckets). Fingerprint: this rule's
own files (the verdict cache does not cover the rule's definition), the two skills, and every
entity a member cites that is not itself a member, as committed at head."""
import glob, hashlib, json, os
import speclib as s

p = s.payload()
head = p["changeset"]["head"]
files = {f["path"]: f.get("newContent", "") for f in p["changeset"]["files"] if f.get("status") != "D"}
out = []
for members in s.buckets(files):
    outside = sorted({e for m in members if s.is_invariant(m) for e in s.cited_entities(files[m])} - set(members))
    h = hashlib.sha256()
    here = os.path.dirname(os.path.abspath(__file__))
    for own in sorted(f for f in glob.glob(os.path.join(here, "*")) if os.path.isfile(f)):
        h.update(os.path.basename(own).encode() + b"\0" + open(own, "rb").read() + b"\0")
    for path in s.SKILLS + outside:
        h.update(path.encode() + b"\0" + (s.at_head(head, path) or "<absent>").encode() + b"\0")
    out.append({"id": "bucket-" + s.hash16(members), "files": members, "fingerprint": h.hexdigest()[:32]})
print(json.dumps(out))
