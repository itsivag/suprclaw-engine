---
name: feedback_no_coauthor
description: Do not add Co-Authored-By Claude line in git commits
type: feedback
---

Never include `Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>` (or any Claude co-author line) in git commit messages.

**Why:** User preference — they don't want Claude credited in commit history.

**How to apply:** When creating any git commit, omit the Co-Authored-By trailer entirely.
