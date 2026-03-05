---
name: Calendar Management
description: Schedule meetings, create events, check availability, and manage the team calendar
trigger: mention
tags: [calendar, schedule, meeting, event, availability, booking]
---

## Instructions

When asked to schedule, check availability, or manage calendar events:

### Scheduling Events
1. **Parse the request** for: title, date/time, duration, attendees, location, recurrence
2. **Default duration** is 30 minutes if not specified
3. **Check existing events** with list_calendar_events before scheduling to avoid conflicts
4. **Create the event** with create_calendar_event including all relevant details
5. **Confirm** with a brief summary: what, when, who

### Time Parsing
- "tomorrow at 2pm" → next day, 14:00 in ISO 8601 (RFC3339)
- "next Monday" → the coming Monday
- "this afternoon" → today 14:00-17:00
- Always use RFC3339 format: `2025-01-15T14:00:00Z`
- If no timezone specified, use UTC

### Recurring Events (RRULE)
Common patterns:
- Daily: `FREQ=DAILY`
- Weekly: `FREQ=WEEKLY;BYDAY=MO,WE,FR`
- Biweekly: `FREQ=WEEKLY;INTERVAL=2`
- Monthly: `FREQ=MONTHLY;BYMONTHDAY=15`
- Every weekday: `FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR`
- With end date: append `;UNTIL=20251231T235959Z`
- With count: append `;COUNT=10`

### Checking Availability
When asked "what's on my calendar" or "am I free at...":
1. Use list_calendar_events with appropriate date range
2. Summarize events concisely
3. Highlight conflicts if scheduling was requested

### Rescheduling
1. List events to find the one to change
2. Use update_calendar_event with new times
3. Confirm the change

### Cancellation
1. List events to find the right one
2. Use delete_calendar_event with the event ID
3. Confirm deletion

### Reminders
When creating events, suggest reminders for important meetings:
- `[{"minutes_before": 15, "type": "notification"}]` for standard meetings
- `[{"minutes_before": 60, "type": "notification"}, {"minutes_before": 15, "type": "notification"}]` for important meetings

Prefix your response with `[skill:Calendar Management]`
