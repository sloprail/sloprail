#!/usr/bin/env python3
"""Hands the judge the two skills and the entities the bucket's invariants cite (as at head)."""
import json
import speclib as s

p = s.payload()
head = p["changeset"]["head"]
content = {f["path"]: f.get("newContent", "") for f in p["changeset"]["files"]}
members = p["subject"]["files"]
cited = sorted({e for m in members if s.is_invariant(m) for e in s.cited_entities(content.get(m, ""))} - set(members))
print(json.dumps({"additionalContext": {
    "invariant_rules": s.at_head(head, s.SKILLS[0]) or "",
    "entity_rules": s.at_head(head, s.SKILLS[1]) or "",
    "members": [{"path": m, "content": content.get(m, "")} for m in members],
    "cited": [{"path": e, "content": s.at_head(head, e) or "<missing>"} for e in cited],
}}))
