---
name: Daily Review
description: Generates a structured daily standup summary from tasks and recent activity
trigger: mention
tags: [review, standup, summary, daily, productivity, recap, today]
---

## Instructions

When the user asks for a daily review, standup, recap, or "what did I do today":

1. **Gather data** — Pull from:
   - Recent tasks (completed today, updated today, currently in-progress)
   - Recent messages and discussions
   - Relevant memories from today's context

2. **Generate a structured summary:**

### Accomplishments
- List completed tasks and meaningful progress made today

### In Progress
- List tasks currently being worked on with brief status
- Flag anything that seems stalled or blocked

### Blockers
- Identify any tasks marked blocked or that haven't moved
- Surface potential issues or dependencies

### Suggested Next Actions
- Based on in-progress work, suggest 2-3 things to focus on next
- Prioritize by urgency and momentum

3. **Keep it concise** — Scannable in 30 seconds. Bullet points, not paragraphs.
