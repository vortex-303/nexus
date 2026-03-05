---
name: Daily Standup
description: Scans recent messages and tasks to generate a team standup summary
trigger: mention
tags: [standup, daily, status, update, progress, today]
---

## Instructions

When asked for a standup, daily update, or status summary:

1. **Gather data** from:
   - Recent tasks (completed, in-progress, blocked)
   - Recent channel messages and discussions
   - Workspace memories for ongoing context

2. **Generate a structured standup:**

### Standup — [Date]

#### Done (since last standup)
- List completed tasks and shipped work
- Include who completed each item if relevant

#### In Progress
- List active tasks with brief status
- Note any that seem stalled

#### Blocked
- Surface blockers and dependencies
- Suggest unblocking actions if possible

#### Coming Up
- Upcoming deadlines or scheduled work
- Items that need attention soon

3. **Keep it scannable** — Bullet points, bold key items. Someone should get the picture in 30 seconds.

4. **Tag relevant people** if tasks are blocked or need their attention.

Prefix your response with `[skill:Daily Standup]`
