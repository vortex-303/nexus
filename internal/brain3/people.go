package brain3

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/brain"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// memberSeed is a row from the members table, minus columns we don't write
// into the seed profile (account_id, color, etc.).
type memberSeed struct {
	ID          string
	DisplayName string
	Role        string
	JoinedAt    string
	Title       string
	Bio         string
	Goals       string
	ReportsTo   string // member ID; resolved to display_name in the rendered profile
}

// SeedMemberProfiles writes a starter `/people/<slug>.md` to the workspace's
// memory_store for every human member that doesn't already have one. Pulls
// title / bio / goals / reports_to from the existing `members` table so v3
// starts day-zero context-aware about who's in the workspace.
//
// Idempotent: if the file already exists at that path (Claude already wrote
// it via the file tools), it's skipped — never overwrite the agent's own
// contributions. Skips Brain itself and any agent-role members.
//
// Wired into pipeline.Run via a one-shot `mga_members_seeded` flag so it
// runs at most once per workspace. Future members added after first seed
// won't get auto-profiled — that's a follow-up (hook into member-add flow).
func SeedMemberProfiles(ctx context.Context, client *anthropic.Client, db *sql.DB, storeID string) error {
	if storeID == "" || db == nil || client == nil {
		return nil
	}

	rows, err := db.Query(`
		SELECT id, display_name, COALESCE(role,''), COALESCE(joined_at,''),
		       COALESCE(title,''), COALESCE(bio,''), COALESCE(goals,''),
		       COALESCE(reports_to,'')
		FROM members
		WHERE id != ? AND COALESCE(role,'') != 'agent'
	`, brain.BrainMemberID)
	if err != nil {
		return fmt.Errorf("query members: %w", err)
	}
	defer rows.Close()

	// Build display-name lookup so reports_to (a member id) resolves to a name.
	nameByID := map[string]string{}
	var members []memberSeed
	for rows.Next() {
		var m memberSeed
		if err := rows.Scan(&m.ID, &m.DisplayName, &m.Role, &m.JoinedAt, &m.Title, &m.Bio, &m.Goals, &m.ReportsTo); err != nil {
			continue
		}
		members = append(members, m)
		nameByID[m.ID] = m.DisplayName
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan members: %w", err)
	}

	for _, m := range members {
		slug := SenderSlug(m.DisplayName)
		if slug == "" {
			continue
		}
		path := PeoplePath(slug)

		// Skip if a profile already exists — never overwrite Claude's own
		// updates.
		if existing, _ := readMemoryAtPath(ctx, client, storeID, path); existing != "" {
			continue
		}

		content := renderSeedProfile(m, nameByID[m.ReportsTo])
		writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := client.Beta.MemoryStores.Memories.New(writeCtx, storeID, anthropic.BetaMemoryStoreMemoryNewParams{
			Path:    path,
			Content: param.NewOpt(content),
		})
		cancel()
		if err != nil {
			// Best-effort — don't fail the whole seed pass if one member
			// errors. The agent can write the missing profile itself later.
			continue
		}
	}
	return nil
}

// renderSeedProfile builds a Markdown profile from members-table data.
// Sections only appear when the source field is non-empty so we don't ship
// blank "## About" headers.
func renderSeedProfile(m memberSeed, reportsToName string) string {
	var b strings.Builder

	b.WriteString("---\n")
	b.WriteString("member_id: " + m.ID + "\n")
	b.WriteString("display_name: " + m.DisplayName + "\n")
	if m.Role != "" {
		b.WriteString("role: " + m.Role + "\n")
	}
	b.WriteString("seeded_at: " + time.Now().UTC().Format("2006-01-02") + "\n")
	b.WriteString("source: workspace-member-directory\n")
	b.WriteString("---\n\n")

	b.WriteString("# " + m.DisplayName + "\n\n")

	// Header strip with structured fields, only the ones we have data for.
	var header []string
	if m.Title != "" {
		header = append(header, "**Title:** "+m.Title)
	}
	if reportsToName != "" {
		header = append(header, "**Reports to:** "+reportsToName)
	}
	if m.JoinedAt != "" {
		header = append(header, "**Joined:** "+formatJoinedDate(m.JoinedAt))
	}
	if m.Role != "" && m.Role != "member" {
		header = append(header, "**Role:** "+m.Role)
	}
	if len(header) > 0 {
		b.WriteString(strings.Join(header, "  ·  ") + "\n\n")
	}

	if m.Bio != "" {
		b.WriteString("## About\n\n" + strings.TrimSpace(m.Bio) + "\n\n")
	}
	if m.Goals != "" {
		b.WriteString("## Goals\n\n" + strings.TrimSpace(m.Goals) + "\n\n")
	}

	b.WriteString("---\n\n")
	b.WriteString("*Seed profile from the workspace member directory. " +
		"Update this file as you learn more about " + m.DisplayName +
		" from conversations — preferences, working style, active focus.*\n")

	return b.String()
}

// formatJoinedDate trims an ISO-8601 timestamp to its date portion for
// readability in the seed profile. Returns the input unchanged on parse
// failure rather than dropping the field.
func formatJoinedDate(joinedAt string) string {
	t, err := time.Parse(time.RFC3339, joinedAt)
	if err != nil {
		return joinedAt
	}
	return t.Format("2006-01-02")
}
