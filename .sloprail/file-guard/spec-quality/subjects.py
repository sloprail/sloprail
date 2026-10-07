#!/usr/bin/env python3
"""One subject per bucket of changed spec files (see speclib.buckets). Fingerprint: the two
skills and every entity a member cites that is not itself a member, as committed at head."""
import hashlib, json
import speclib as s

p = s.payload()
head = p["changeset"]["head"]
files = {f["path"]: f.get("newContent", "") for f in p["changeset"]["files"] if f.get("status") != "D"}
out = []
for members in s.buckets(files):
    outside = sorted({e for m in members if s.is_invariant(m) for e in s.cited_entities(files[m])} - set(members))
    h = hashlib.sha256()
    for path in s.SKILLS + outside:
        h.update(path.encode() + b"\0" + (s.at_head(head, path) or "<absent>").encode() + b"\0")
    out.append({"id": "bucket-" + s.hash16(members), "files": members, "fingerprint": h.hexdigest()[:32]})
print(json.dumps(out))
