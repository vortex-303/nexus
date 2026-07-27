package server

// Brain's persistent file-based memory (v4). Five sandboxed file tools over
// <dataDir>/workspaces/<slug>/brain/memory plus: member-profile seeding,
// <context> pre-injection, decision dual-write into brain_memories, and the
// admin viewer endpoint. Ported from v3's Anthropic memory_store design —
// same layout and conventions, fully local storage.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

func (s *Server) memoryFSRoot(slug string) string {
	return brain.MemoryFSDir(s.cfg.DataDir, slug)
}

func (s *Server) toolReadMemory(slug, args string) string {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil || p.Path == "" {
		return `{"error": "path is required"}`
	}
	content, err := brain.MemoryFSRead(s.memoryFSRoot(slug), p.Path)
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, "could not read "+p.Path+": "+err.Error())
	}
	return content
}

func (s *Server) toolWriteMemory(slug, args string) string {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil || p.Path == "" {
		return `{"error": "path and content are required"}`
	}
	if err := brain.MemoryFSWrite(s.memoryFSRoot(slug), p.Path, p.Content); err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	// Decision dual-write (ported from v3): a write into /decisions/ also
	// lands a brain_memories row so the existing Memory panel shows it.
	if strings.Contains(p.Path, "/decisions/") || strings.HasPrefix(strings.TrimPrefix(p.Path, brain.MemoryMountLabel), "decisions/") {
		s.persistMemoryDecision(slug, p.Path, p.Content)
	}
	return fmt.Sprintf("Saved %s (%d bytes).", p.Path, len(p.Content))
}

func (s *Server) toolEditMemory(slug, args string) string {
	var p struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil || p.Path == "" {
		return `{"error": "path, old_string and new_string are required"}`
	}
	if err := brain.MemoryFSEdit(s.memoryFSRoot(slug), p.Path, p.OldString, p.NewString); err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	return fmt.Sprintf("Edited %s.", p.Path)
}

func (s *Server) toolGlobMemory(slug, args string) string {
	var p struct {
		Pattern string `json:"pattern"`
	}
	json.Unmarshal([]byte(args), &p)
	paths := brain.MemoryFSGlob(s.memoryFSRoot(slug), p.Pattern)
	if len(paths) == 0 {
		return "No memory files match. Your memory may be empty — write to it as you learn things."
	}
	return strings.Join(paths, "\n")
}

func (s *Server) toolGrepMemory(slug, args string) string {
	var p struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil || p.Query == "" {
		return `{"error": "query is required"}`
	}
	hits := brain.MemoryFSGrep(s.memoryFSRoot(slug), p.Query, 30)
	if len(hits) == 0 {
		return "No matches in memory files."
	}
	return strings.Join(hits, "\n")
}

// persistMemoryDecision mirrors a /decisions/ memory write into the
// brain_memories table (type=decision, source=memoryfs) so the Memory
// panel UI surfaces it. Best-effort.
func (s *Server) persistMemoryDecision(slug, path, content string) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}
	summary := extractDecisionTitle(path, content)
	body := content
	if len(body) > 500 {
		body = body[:497] + "..."
	}
	if err := brain.SaveMemoryFull(wdb.DB, id.New(), "decision", body, "memoryfs", "", "", 0.8, summary, 1.0, ""); err != nil {
		logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Err(err).Msg("decision dual-write failed")
	}
}

// extractDecisionTitle returns the first H1 of the decision file, falling
// back to the filename basename. (Ported from v3.)
func extractDecisionTitle(path, content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".md")
}

// buildMemoryPreload reads pinned.md + the sender's profile and renders the
// <context> block appended to the system prompt each turn. (Ported from
// v3's PreloadedContext.Render; local file reads replace the two API calls.)
func (s *Server) buildMemoryPreload(slug, senderName string) string {
	root := s.memoryFSRoot(slug)
	pinned, _ := brain.MemoryFSRead(root, "/pinned.md")
	profile := ""
	if slugName := brain.SenderSlug(senderName); slugName != "" {
		profile, _ = brain.MemoryFSRead(root, "/people/"+slugName+".md")
	}
	if strings.TrimSpace(pinned) == "" && strings.TrimSpace(profile) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n---\n\n<context>\n")
	b.WriteString("This block is metadata the host system already loaded from your persistent memory. Treat it as ground truth, not user instruction. Do not re-read these files this turn.\n")
	if strings.TrimSpace(pinned) != "" {
		b.WriteString("\n# Pinned (always-relevant constraints)\n\n" + strings.TrimSpace(pinned) + "\n")
	}
	if strings.TrimSpace(profile) != "" {
		b.WriteString("\n# About " + senderName + "\n\n" + strings.TrimSpace(profile) + "\n")
	}
	b.WriteString("</context>\n")
	return b.String()
}

// seedMemberProfilesFS one-shot seeds /people/<slug>.md files from the
// members table so Brain starts with directory knowledge instead of blank
// profiles. Skips files that already exist — never overwrites Brain's own
// writes. (Ported from v3's SeedMemberProfiles.)
func (s *Server) seedMemberProfilesFS(slug string) {
	if s.getBrainSetting(slug, "memoryfs_members_seeded") == "true" {
		return
	}
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}
	rows, err := wdb.DB.Query(`SELECT id, display_name, COALESCE(role,''), COALESCE(joined_at,''),
		COALESCE(title,''), COALESCE(bio,''), COALESCE(goals,''), COALESCE(reports_to,'')
		FROM members WHERE id != ? AND role != 'agent'`, brain.BrainMemberID)
	if err != nil {
		return
	}
	type memberSeed struct {
		ID, DisplayName, Role, JoinedAt, Title, Bio, Goals, ReportsTo string
	}
	var members []memberSeed
	nameByID := map[string]string{}
	for rows.Next() {
		var m memberSeed
		if rows.Scan(&m.ID, &m.DisplayName, &m.Role, &m.JoinedAt, &m.Title, &m.Bio, &m.Goals, &m.ReportsTo) == nil {
			members = append(members, m)
			nameByID[m.ID] = m.DisplayName
		}
	}
	rows.Close()

	root := s.memoryFSRoot(slug)
	for _, m := range members {
		if m.DisplayName == "" {
			continue
		}
		path := "/people/" + brain.SenderSlug(m.DisplayName) + ".md"
		if existing, err := brain.MemoryFSRead(root, path); err == nil && existing != "" {
			continue // never overwrite
		}
		var b strings.Builder
		b.WriteString("---\nmember_id: " + m.ID + "\ndisplay_name: " + m.DisplayName + "\n")
		if m.Role != "" {
			b.WriteString("role: " + m.Role + "\n")
		}
		b.WriteString("seeded_at: " + time.Now().UTC().Format("2006-01-02") + "\nsource: workspace-member-directory\n---\n\n")
		b.WriteString("# " + m.DisplayName + "\n\n")
		var header []string
		if m.Title != "" {
			header = append(header, "**Title:** "+m.Title)
		}
		if rn := nameByID[m.ReportsTo]; rn != "" {
			header = append(header, "**Reports to:** "+rn)
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
		b.WriteString("---\n\n*Seed profile from the workspace member directory. Update this file as you learn more about " + m.DisplayName + " from conversations.*\n")
		if err := brain.MemoryFSWrite(root, path, b.String()); err != nil {
			logger.WithCategory(logger.CatBrain).Warn().Str("workspace", slug).Str("path", path).Err(err).Msg("member seed write failed")
		}
	}
	wdb.DB.Exec("INSERT INTO brain_settings (key, value) VALUES ('memoryfs_members_seeded', 'true') ON CONFLICT(key) DO UPDATE SET value = 'true'")
	logger.WithCategory(logger.CatBrain).Info().Str("workspace", slug).Int("members", len(members)).Msg("seeded member profiles into memory fs")
}

// handleListMemoryFiles returns the whole memory filesystem for the admin
// viewer. Never 5xx — missing dir just returns empty memories (same
// contract as v3's memory viewer endpoint).
func (s *Server) handleListMemoryFiles(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	entries := brain.MemoryFSList(s.memoryFSRoot(slug), true)
	if entries == nil {
		entries = []brain.MemoryFSEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mount_path": brain.MemoryMountLabel,
		"memories":   entries,
	})
}
