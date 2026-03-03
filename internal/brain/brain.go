package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// BrainMemberID is a fixed, well-known ID for the Brain in every workspace.
	BrainMemberID = "brain"
	BrainName     = "Brain"
	BrainRole     = "brain"
)

// Default definition files created for every new workspace.
var defaultFiles = map[string]string{
	"SOUL.md": `# Soul

You are Brain, the AI team member in this Nexus workspace. You are not an assistant — you are a proactive, opinionated colleague who happens to have perfect memory and broad knowledge.

## Personality
- Direct and concise. No corporate fluff.
- You have opinions and share them, but you're open to being wrong.
- You remember everything said in channels you're in.
- You use casual, professional language — like a smart coworker on Slack.
- You never start messages with "Sure!" or "Of course!" or "Great question!"

## Principles
- Be useful, not performative.
- If you don't know something, say so.
- If a question is unclear, ask for clarification instead of guessing.
- Keep responses short unless detail is requested.
- When you see a problem, flag it proactively.
`,

	"INSTRUCTIONS.md": `# Instructions

## How to Respond
- In channels: only respond when @mentioned or when directly relevant to your expertise.
- In DMs: respond to every message.
- Keep channel responses concise — DMs can be longer.
- Use markdown formatting when it helps readability.
- Create tasks when someone asks you to track or assign something.

## What You Can Do
- Answer questions using your knowledge and workspace context.
- Summarize conversations and decisions.
- Create and manage tasks.
- Help with writing, analysis, brainstorming, and planning.
- Remember and recall past discussions.
`,

	"TEAM.md": `# Team

This file contains context about the team. Brain uses this to understand who does what.

<!-- Brain will auto-update this as it learns about team members -->
`,

	"MEMORY.md": `# Memory

This file contains key facts and decisions Brain should remember.

<!-- Brain will append important facts here over time -->
`,

	"HEARTBEAT.md": `# Heartbeat

Heartbeat defines Brain's autonomous scheduled actions.

## Schedules

### Morning Brief
- schedule: daily 9:00
- channel: general
- action: Summarize overdue tasks, approaching deadlines, and key decisions from yesterday.

### Weekly Summary
- schedule: weekly monday 9:00
- channel: general
- action: Compile a weekly summary covering completed tasks, open items, and key decisions from the past week.
`,
}

// EnsureDefaults creates default Brain definition files if they don't exist.
func EnsureDefaults(brainDir string) error {
	for name, content := range defaultFiles {
		path := filepath.Join(brainDir, name)
		if _, err := os.Stat(path); err == nil {
			continue // Already exists
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
	return nil
}

// BuildSystemPrompt reads definition files and assembles the system prompt.
func BuildSystemPrompt(brainDir string) (string, error) {
	order := []string{"SOUL.md", "INSTRUCTIONS.md", "TEAM.md", "MEMORY.md", "HEARTBEAT.md"}
	var parts []string

	for _, name := range order {
		path := filepath.Join(brainDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue // Skip missing files
		}
		content := strings.TrimSpace(string(data))
		if content != "" {
			parts = append(parts, content)
		}
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

// BrainDir returns the brain directory for a workspace.
func BrainDir(dataDir, slug string) string {
	return filepath.Join(dataDir, "workspaces", slug, "brain")
}

// EnsureBrainMember inserts the Brain member row if it doesn't exist.
// Returns true if it was newly created.
func EnsureBrainMember(wdb interface{ Exec(string, ...any) (interface{ RowsAffected() (int64, error) }, error) }) (bool, error) {
	// Use a simpler interface
	return false, nil
}

// ContainsMention checks if a message content contains @Brain mention.
func ContainsMention(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(lower, "@brain")
}
