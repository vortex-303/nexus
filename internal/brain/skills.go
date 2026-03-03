package brain

import (
	"os"
	"path/filepath"
	"strings"
)

// Skill represents a loaded brain skill definition.
type Skill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Trigger     string   `json:"trigger"`     // "mention", "schedule", "keyword"
	Channels    []string `json:"channels"`    // empty = all channels
	Autonomy    string   `json:"autonomy"`    // "reactive", "proactive"
	Prompt      string   `json:"prompt"`      // The skill's instruction prompt
	FileName    string   `json:"file_name"`
}

// LoadSkills reads all SKILL.md files from the brain/skills/ directory.
func LoadSkills(brainDir string) []Skill {
	skillsDir := filepath.Join(brainDir, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}

	var skills []Skill
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(skillsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		skill := parseSkill(string(data))
		skill.FileName = entry.Name()
		if skill.Name != "" {
			skills = append(skills, skill)
		}
	}

	return skills
}

// parseSkill extracts skill metadata from YAML-like frontmatter and markdown body.
func parseSkill(content string) Skill {
	var skill Skill

	lines := strings.Split(content, "\n")
	inFrontmatter := false
	bodyStart := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if i == 0 && trimmed == "---" {
			inFrontmatter = true
			continue
		}
		if inFrontmatter && trimmed == "---" {
			inFrontmatter = false
			bodyStart = i + 1
			continue
		}

		if inFrontmatter {
			key, val := parseYAMLLine(trimmed)
			switch key {
			case "name":
				skill.Name = val
			case "description":
				skill.Description = val
			case "trigger":
				skill.Trigger = val
			case "channels":
				for _, ch := range strings.Split(val, ",") {
					ch = strings.TrimSpace(ch)
					if ch != "" {
						skill.Channels = append(skill.Channels, ch)
					}
				}
			case "autonomy":
				skill.Autonomy = val
			}
		}
	}

	// If no frontmatter, try to parse from heading
	if skill.Name == "" {
		for _, line := range lines {
			if strings.HasPrefix(line, "# ") {
				skill.Name = strings.TrimPrefix(line, "# ")
				break
			}
		}
	}

	// Body is everything after frontmatter
	if bodyStart > 0 && bodyStart < len(lines) {
		skill.Prompt = strings.TrimSpace(strings.Join(lines[bodyStart:], "\n"))
	} else {
		skill.Prompt = strings.TrimSpace(content)
	}

	if skill.Trigger == "" {
		skill.Trigger = "mention"
	}
	if skill.Autonomy == "" {
		skill.Autonomy = "reactive"
	}

	return skill
}

func parseYAMLLine(line string) (string, string) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", ""
	}
	key := strings.TrimSpace(line[:idx])
	val := strings.TrimSpace(line[idx+1:])
	// Remove quotes
	val = strings.Trim(val, "\"'")
	return key, val
}

// DefaultSkills are bundled skill definitions created for every new workspace.
var DefaultSkills = map[string]string{

	// --- Rhythm: Keep the Team Moving ---

	"daily-standup.md": `---
name: Daily Standup
description: Async standup — collect updates, compile summary, flag blockers
trigger: schedule
channels: general
autonomy: proactive
---

# Daily Standup

Post a standup prompt each morning asking team members to share:
1. What they accomplished yesterday
2. What they're working on today
3. Any blockers or things they need help with

After collecting responses (or after 2 hours), compile a summary organized by person.
Highlight any blockers that need attention. Tag people who might be able to help with blockers.
If someone hasn't posted by noon, send a gentle reminder.
`,

	"meeting-notes.md": `---
name: Meeting Notes
description: Capture discussion points, decisions, and action items from conversations
trigger: mention
channels:
autonomy: reactive
---

# Meeting Notes

When asked to take meeting notes (e.g. "@Brain take notes"), monitor the conversation and capture:
- Key discussion points and context
- Decisions made (who decided, reasoning)
- Action items (with assignees and deadlines if mentioned)
- Open questions that need follow-up

When the meeting wraps up (someone says "that's it", "thanks everyone", etc.):
1. Create a document with the formatted notes
2. Create tasks for each action item
3. Post a brief summary in the channel
`,

	"decision-logger.md": `---
name: Decision Logger
description: Detect team consensus, confirm decisions, log as memories and tasks
trigger: mention
channels:
autonomy: reactive
---

# Decision Logger

When a team decision is reached in conversation:
1. Confirm the decision with the team — quote the decision clearly and ask "Should I log this?"
2. Once confirmed, log it as a "decision" memory with full context
3. Create follow-up tasks if there are action items
4. Summarize who was involved and the reasoning

Watch for phrases like "let's go with", "we decided", "agreed", "the plan is", "final answer".
`,

	"new-hire-buddy.md": `---
name: New Hire Buddy
description: Welcome new members, provide workspace overview, schedule check-ins
trigger: mention
channels: general
autonomy: proactive
---

# New Hire Buddy

When a new member joins the workspace:
1. Send a welcome message in #general introducing them
2. Provide an overview of active channels and what each is used for
3. Share any key team norms or conventions from memory
4. List current team members and their roles
5. Suggest channels they should join based on their role

If asked, provide a summary of recent decisions, active projects, and ongoing work.
`,

	// --- Client & Sales ---

	"client-onboarding.md": `---
name: Client Onboarding
description: Automate new client setup — channel, checklist, welcome message
trigger: mention
channels:
autonomy: reactive
---

# Client Onboarding

When asked to onboard a new client (e.g. "@Brain onboard client Acme Corp"):
1. Create a dedicated channel for the client (e.g. #client-acme)
2. Create an onboarding checklist as tasks:
   - Send welcome email
   - Schedule kickoff call
   - Share access credentials
   - Send brand guidelines
   - Set up recurring check-in
3. Post a welcome message in the new channel with client details
4. Notify the team in #general about the new client
`,

	"deal-tracker.md": `---
name: Deal Tracker
description: Monitor sales conversations, track deal stages, weekly pipeline reports
trigger: mention
channels: sales
autonomy: reactive
---

# Deal Tracker

Track deals mentioned in #sales conversations:
- When someone mentions a new opportunity, create a task with deal details
- Track deal stages: prospect → qualified → proposal → negotiation → closed
- When deal updates are mentioned, update the relevant task status
- Weekly: compile a pipeline summary showing all active deals by stage

When asked "@Brain pipeline" or "@Brain deals", show current pipeline status.
`,

	// --- Operations ---

	"support-triage.md": `---
name: Support Triage
description: Categorize support requests, assign priority, route to team members
trigger: mention
channels: support
autonomy: reactive
---

# Support Triage

When a support request comes in:
1. Categorize it: bug, feature request, question, billing, account
2. Assign priority based on urgency keywords and customer context
3. Create a task with the categorization and priority
4. Suggest which team member should handle it based on expertise
5. If it matches a known issue from memory, link to the previous resolution

When asked "@Brain support summary", show open tickets by category and priority.
`,

	"campaign-manager.md": `---
name: Campaign Manager
description: Track marketing campaigns — tasks, assets, deadlines, daily briefs
trigger: mention
channels: marketing
autonomy: reactive
---

# Campaign Manager

Help manage marketing campaigns:
- When a new campaign is announced, create a task checklist with standard milestones
- Track asset creation (copy, design, video) as subtasks
- Monitor deadlines and send reminders 2 days before due dates
- Daily brief: what's due today, what's overdue, what's coming up this week

When asked "@Brain campaign status", show all active campaigns with progress.
`,

	"content-calendar.md": `---
name: Content Calendar
description: Track content schedule, send reminders, compile weekly content plan
trigger: mention
channels: marketing
autonomy: reactive
---

# Content Calendar

Manage the team's content schedule:
- Track planned content pieces (blog posts, social media, newsletters)
- Send reminders when content is due for review or publication
- Weekly: compile the upcoming content plan
- Flag gaps in the schedule

When asked "@Brain content plan", show this week's and next week's scheduled content.
`,

	"retro-facilitator.md": `---
name: Retro Facilitator
description: Run async retrospectives — collect feedback, identify patterns, action items
trigger: mention
channels: general
autonomy: reactive
---

# Retro Facilitator

When asked to run a retrospective:
1. Post three prompts for the team:
   - What went well this week/sprint?
   - What could be improved?
   - What should we try next?
2. Collect responses over a set period (default: 4 hours)
3. Compile and group feedback by theme
4. Identify recurring patterns from past retros (check memory)
5. Create action items as tasks for the top improvements
6. Save key insights as memories for future reference
`,
}

// EnsureDefaultSkills creates bundled skill files if the skills directory is empty.
func EnsureDefaultSkills(brainDir string) error {
	skillsDir := filepath.Join(brainDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return err
	}

	for name, content := range DefaultSkills {
		path := filepath.Join(skillsDir, name)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
	}

	return nil
}

// AgentSkillsDir returns the path to an agent's skills directory.
func AgentSkillsDir(dataDir, slug, agentID string) string {
	return filepath.Join(dataDir, "workspaces", slug, "brain", "agents", agentID, "skills")
}

// EnsureAgentSkillsDir creates the agent skills directory if needed.
func EnsureAgentSkillsDir(dataDir, slug, agentID string) error {
	return os.MkdirAll(AgentSkillsDir(dataDir, slug, agentID), 0755)
}

// LoadAgentSkills reads skill files from an agent's skills directory.
func LoadAgentSkills(dataDir, slug, agentID string) []Skill {
	skillsDir := AgentSkillsDir(dataDir, slug, agentID)
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}

	var skills []Skill
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(skillsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		skill := parseSkill(string(data))
		skill.FileName = entry.Name()
		if skill.Name != "" {
			skills = append(skills, skill)
		}
	}

	return skills
}

// BuildAgentSkillContext formats agent skills with full body for inclusion in system prompt.
func BuildAgentSkillContext(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, "## Available Skills\n")
	for _, s := range skills {
		header := "### " + s.Name
		if s.Description != "" {
			header += "\n" + s.Description
		}
		parts = append(parts, header)
		if s.Prompt != "" {
			parts = append(parts, s.Prompt)
		}
		parts = append(parts, "")
	}
	return strings.Join(parts, "\n")
}

// BuildSkillContext formats loaded skills into context for the system prompt.
func BuildSkillContext(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, "## Available Skills")
	for _, s := range skills {
		line := "- **" + s.Name + "**: " + s.Description
		if s.Trigger != "" {
			line += " (trigger: " + s.Trigger + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
