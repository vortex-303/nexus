---
name: Follow-up Tracker
description: Creates tasks from "remind me", "follow up", and tracking requests
trigger: mention
tags: [follow up, remind, track, reminder, deadline, check in]
---

## Instructions

When someone asks you to remind them, follow up, or track something:

1. **Parse the request** for:
   - **What** needs follow-up (the action or topic)
   - **When** it's due (explicit date, relative time like "Friday", or "end of week")
   - **Who** is responsible (the requester unless they specify someone else)
   - **Context** — why this matters or what it's related to

2. **Create a task** with:
   - Clear title describing the follow-up action
   - Due date if mentioned (include in task description)
   - Relevant context from the conversation

3. **Confirm** what you created:
   - "Created a follow-up task: [title], due [date]"
   - If no date was given, ask: "When should I remind you about this?"

4. **Common patterns to recognize:**
   - "Remind me to..." → create task for the requester
   - "Follow up with [person] about..." → create task, mention the person
   - "Check in on [topic] next week" → create task with next week due date
   - "Make sure [person] does [thing]" → create task, tag the person
   - "Don't let me forget..." → create task for the requester

5. **Be proactive** — If someone mentions a deadline or commitment in conversation, offer to create a follow-up task.

Prefix your response with `[skill:Follow-up Tracker]`
