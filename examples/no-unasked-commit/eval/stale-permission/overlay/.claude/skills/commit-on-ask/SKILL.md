---
name: commit-on-ask
description: Use before running `git commit` or `git push` in this repo — a standing or earlier permission from this session does not authorize one now.
---

# Committing and pushing on ask, not on standing permission

This project's `.sloprail/` requires `git commit` and `git push` to cite the
user's own words with the command that runs them:

```bash
sr-session trajectory cite '<exact quote from the user>' && git commit -m "..."
sr-session trajectory cite '<exact quote from the user>' && git push
```

Run the `cite` part on its own first — `sr-session trajectory cite
'<quote>'` — to confirm it resolves before chaining it.

The citation must be the user's own words asking for **this** commit or
push, **right now**. An earlier message from this same session where the
user said something like "commit and push" does not authorize a commit or
push later in the conversation about something else — if the user's most
recent message does not itself ask for a commit or a push, do not run one:
ask first, or do the work the current message actually asked for and leave
git alone.
