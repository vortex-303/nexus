package brain

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"time"
)

// HeartbeatSchedule represents a parsed schedule from HEARTBEAT.md.
type HeartbeatSchedule struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"` // "daily 9:00", "weekly monday 9:00"
	Channel  string `json:"channel"`
	Action   string `json:"action"`
}

// ParseHeartbeat reads HEARTBEAT.md and extracts schedule definitions.
func ParseHeartbeat(brainDir string) []HeartbeatSchedule {
	path := brainDir + "/HEARTBEAT.md"
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var schedules []HeartbeatSchedule
	var current *HeartbeatSchedule

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		// Heading starts a new schedule
		if strings.HasPrefix(line, "### ") {
			if current != nil && current.Action != "" {
				schedules = append(schedules, *current)
			}
			current = &HeartbeatSchedule{Name: strings.TrimPrefix(line, "### ")}
			continue
		}

		if current == nil {
			continue
		}

		// Parse key-value fields
		if strings.HasPrefix(line, "- schedule:") {
			current.Schedule = strings.TrimSpace(strings.TrimPrefix(line, "- schedule:"))
		} else if strings.HasPrefix(line, "- channel:") {
			current.Channel = strings.TrimSpace(strings.TrimPrefix(line, "- channel:"))
		} else if strings.HasPrefix(line, "- action:") {
			current.Action = strings.TrimSpace(strings.TrimPrefix(line, "- action:"))
		}
	}

	// Don't forget the last one
	if current != nil && current.Action != "" {
		schedules = append(schedules, *current)
	}

	return schedules
}

var timeRe = regexp.MustCompile(`(\d{1,2}):(\d{2})`)

// ShouldRun checks if a schedule should run at the given time.
func (s HeartbeatSchedule) ShouldRun(now time.Time) bool {
	parts := strings.Fields(strings.ToLower(s.Schedule))
	if len(parts) == 0 {
		return false
	}

	// Parse time from schedule
	m := timeRe.FindStringSubmatch(s.Schedule)
	if m == nil {
		return false
	}

	hour := parseInt(m[1])
	minute := parseInt(m[2])

	if now.Hour() != hour || now.Minute() != minute {
		return false
	}

	switch parts[0] {
	case "daily":
		return true
	case "weekly":
		if len(parts) < 2 {
			return false
		}
		days := map[string]time.Weekday{
			"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
			"wednesday": time.Wednesday, "thursday": time.Thursday,
			"friday": time.Friday, "saturday": time.Saturday,
		}
		day, ok := days[parts[1]]
		return ok && now.Weekday() == day
	case "hourly":
		return minute == now.Minute()
	}

	return false
}

func parseInt(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
