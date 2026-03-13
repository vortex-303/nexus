package server

// Agent scheduling now lives in calendar events.
// Calendar events with agent_id are fired by checkDirectAgentEvents()
// in calendar_triggers.go, which runs every minute via the existing cron.
//
// The gocron v2 scheduler on Server is available for future use
// (e.g. more complex recurring patterns beyond what the 1-min cron handles).
