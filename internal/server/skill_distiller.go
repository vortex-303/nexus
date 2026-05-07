package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/brain"
	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// distillerCooldown is the minimum gap between automatic skill-distiller
// runs. Manual triggers can pass ?force=1 to override. Keeps the LLM cost
// bounded — at ~$0.12 per Sonnet 4.6 run, weekly cadence = ~$0.50/month
// per workspace; daily would be ~$3.50.
const distillerCooldown = 7 * 24 * time.Hour

// distillerInputCap bounds how many recent items we feed the LLM. Keeps
// input tokens predictable. ~30 actions × ~500 tokens each ≈ 15K tokens.
const distillerActionCap = 30
const distillerTraceCap = 30

// SkillProposal is one distilled-skill draft saved to the proposals dir.
// It's the JSON shape we ask the LLM to return so we can parse + persist.
type SkillProposal struct {
	Name             string   `json:"name"`              // kebab-case, used as filename
	Description      string   `json:"description"`       // 1-line summary for the YAML frontmatter
	Engines          []string `json:"engines"`           // e.g. ["claude", "openrouter"]
	TriggerKeywords  []string `json:"trigger_keywords"`  // for OpenRouter keyword matching
	Body             string   `json:"body"`              // markdown body of the SKILL.md
	Rationale        string   `json:"rationale"`         // why we proposed this — shown in UI for review
	BasedOn          []string `json:"based_on"`          // 2-4 short citations from observed patterns
}

// distillerSystemPrompt is the LLM system prompt for the analysis pass.
// Instructs the model to output a strict JSON array of SkillProposal-shaped
// objects (or [] if no patterns warrant a new skill).
const distillerSystemPrompt = `You are Brain's skill-distiller — a meta-skill that observes Brain's recent activity in this workspace and proposes new behavioral skills (.md skill files) the workspace should adopt.

Your job: find recurring patterns, repeated user corrections, implicit conventions, or workflow shapes that should be codified as a reusable skill so future Brain sessions follow them automatically.

Skip patterns that are already covered by existing skills (you'll see the existing skill names in the input). Skip one-off events. Skip personal preferences from a single user (unless that user is the workspace owner correcting Brain's behavior workspace-wide).

Quality bar: a good proposal is specific, actionable, and based on at least 2-3 observed instances. Bad proposals: vague tone advice, generic "be helpful", or anything you couldn't trace to real evidence in the input.

Output: a strict JSON array of skill proposals. Empty array [] if no patterns warrant a new skill — that's a perfectly fine answer. Do NOT wrap in markdown fences. Each proposal must have:
- name: kebab-case (e.g. "spanish-client-output")
- description: one sentence describing what the skill does and when it fires
- engines: array, choose from "claude", "openrouter" — both unless one engine is clearly the only fit
- trigger_keywords: 3-8 keywords that should activate this skill on OpenRouter (where keyword-matching is the firing mechanism)
- body: the full markdown body of the SKILL.md, with frontmatter + headings + concrete instructions. ~200-600 words. Match the structure of the existing workspace skills shown in the input.
- rationale: 1-2 sentences explaining what pattern you observed and why a skill helps
- based_on: 2-4 short quotes / paraphrases from the input that motivated this proposal (so the user can verify your reasoning).

Be concise. Propose 0-3 skills max — quality over quantity.`

// handleDistillSkills runs skill-distiller — analyzes recent workspace
// activity and writes 0-3 skill proposals to data/workspaces/<slug>/brain/
// skills/_proposals/. Manual trigger; cooldown-bounded.
//
// POST /api/workspaces/{slug}/brain/distill-skills?force=1
func (s *Server) handleDistillSkills(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	force := r.URL.Query().Get("force") == "1"
	if !force {
		lastRun := s.getBrainSetting(slug, "last_distill_run")
		if t, err := time.Parse(time.RFC3339, lastRun); err == nil && time.Since(t) < distillerCooldown {
			next := t.Add(distillerCooldown)
			writeJSON(w, http.StatusOK, map[string]any{
				"skipped":    true,
				"reason":     "cooldown",
				"last_run":   lastRun,
				"next_after": next.Format(time.RFC3339),
				"message":    fmt.Sprintf("Last analysis was %s ago. Add ?force=1 to override.", time.Since(t).Round(time.Hour)),
			})
			return
		}
	}

	go s.runSkillDistill(slug)
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

// handleListSkillProposals returns the current proposals (unreviewed
// skill drafts) for the Extensions tab UI.
//
// GET /api/workspaces/{slug}/brain/skill-proposals
func (s *Server) handleListSkillProposals(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if claims := auth.GetClaims(r); claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	dir := s.proposalsDir(slug)
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"proposals": []any{}, "last_run": s.getBrainSetting(slug, "last_distill_run")})
		return
	}

	type proposalView struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Engines     []string `json:"engines"`
		Rationale   string   `json:"rationale"`
		BasedOn     []string `json:"based_on"`
		Body        string   `json:"body"`
		FilePath    string   `json:"file_path"`
		CreatedAt   string   `json:"created_at"`
	}
	var out []proposalView
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		p := parseProposalFile(string(data))
		var createdAt string
		if info, err := e.Info(); err == nil {
			createdAt = info.ModTime().UTC().Format(time.RFC3339)
		}
		out = append(out, proposalView{
			Name:        p.Name,
			Description: p.Description,
			Engines:     p.Engines,
			Rationale:   p.Rationale,
			BasedOn:     p.BasedOn,
			Body:        p.Body,
			FilePath:    e.Name(),
			CreatedAt:   createdAt,
		})
	}
	if out == nil {
		out = []proposalView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"proposals": out,
		"last_run":  s.getBrainSetting(slug, "last_distill_run"),
	})
}

// handleApproveSkillProposal moves a proposal from _proposals/ to skills/.
// On Claude engine, the next agent drift will pick it up via the existing
// skill upload pipeline; on OpenRouter the keyword-match picks it up.
//
// POST /api/workspaces/{slug}/brain/skill-proposals/{name}/approve
func (s *Server) handleApproveSkillProposal(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	name := r.PathValue("name")
	if claims := auth.GetClaims(r); claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	src := filepath.Join(s.proposalsDir(slug), name)
	if !strings.HasSuffix(src, ".md") {
		src += ".md"
	}
	if _, err := os.Stat(src); err != nil {
		writeError(w, http.StatusNotFound, "proposal not found")
		return
	}
	dst := filepath.Join(brain.BrainDir(s.cfg.DataDir, slug), "skills", filepath.Base(src))
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to ensure skills dir")
		return
	}
	if err := os.Rename(src, dst); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to move proposal")
		return
	}
	// For Claude: clear tools hash so next turn re-uploads skills (existing
	// drift mechanism we use for .md edits).
	if s.resolveEngine(slug) == "claude" {
		if wdb, err := s.ws.Open(slug); err == nil {
			_, _ = wdb.DB.Exec("DELETE FROM brain_settings WHERE key = 'mga_provisioned_tools_hash'")
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved", "path": dst})
}

// handleRejectSkillProposal deletes a proposal file. Brain's distiller
// won't propose this exact name again unless explicitly retried.
//
// DELETE /api/workspaces/{slug}/brain/skill-proposals/{name}
func (s *Server) handleRejectSkillProposal(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	name := r.PathValue("name")
	if claims := auth.GetClaims(r); claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	path := filepath.Join(s.proposalsDir(slug), name)
	if !strings.HasSuffix(path, ".md") {
		path += ".md"
	}
	if err := os.Remove(path); err != nil {
		writeError(w, http.StatusNotFound, "proposal not found or already removed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// proposalsDir is data/workspaces/<slug>/brain/skills/_proposals/.
func (s *Server) proposalsDir(slug string) string {
	return filepath.Join(brain.BrainDir(s.cfg.DataDir, slug), "skills", "_proposals")
}

// runSkillDistill is the main analysis pass. Runs in a goroutine.
func (s *Server) runSkillDistill(slug string) {
	log := logger.WithCategory(logger.CatBrain)
	log.Info().Str("workspace", slug).Msg("skill-distiller: starting")

	wdb, err := s.ws.Open(slug)
	if err != nil {
		log.Error().Err(err).Str("workspace", slug).Msg("skill-distiller: failed to open workspace")
		return
	}

	input := s.gatherDistillerInput(slug, wdb)
	if len(input.Actions) < 5 {
		log.Info().Str("workspace", slug).Int("actions", len(input.Actions)).Msg("skill-distiller: not enough activity to analyze, skipping")
		s.persistDistillRun(slug, 0, "insufficient_activity")
		return
	}

	prompt := formatDistillerPrompt(input)
	result, usage, err := s.memoryComplete(slug, distillerSystemPrompt, []brain.Message{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		log.Error().Err(err).Str("workspace", slug).Msg("skill-distiller: LLM error")
		s.persistDistillRun(slug, 0, "llm_error")
		return
	}
	s.trackUsage(slug, usage, "", "skill_distill", "", "")

	proposals := parseProposals(result)
	if len(proposals) == 0 {
		log.Info().Str("workspace", slug).Msg("skill-distiller: no patterns warranted a new skill")
		s.persistDistillRun(slug, 0, "no_patterns")
		return
	}

	dir := s.proposalsDir(slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Error().Err(err).Str("workspace", slug).Msg("skill-distiller: failed to make proposals dir")
		return
	}
	written := 0
	for _, p := range proposals {
		if p.Name == "" || p.Body == "" {
			continue
		}
		filename := sanitizeSkillName(p.Name) + ".md"
		path := filepath.Join(dir, filename)
		// Don't overwrite an existing proposal with the same name — the user
		// hasn't reviewed it yet. Skip silently.
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(formatProposalFile(p)), 0644); err != nil {
			log.Warn().Err(err).Str("workspace", slug).Str("name", p.Name).Msg("skill-distiller: failed to write proposal")
			continue
		}
		written++
	}
	log.Info().Str("workspace", slug).Int("written", written).Msg("skill-distiller: proposals saved")
	s.persistDistillRun(slug, written, "ok")
	brain.LogAction(wdb.DB, id.New(), "skill_distill", "", "Skill distiller analysis", fmt.Sprintf("%d proposals written", written), "", nil)
}

// persistDistillRun records the run timestamp + status in brain_settings.
func (s *Server) persistDistillRun(slug string, written int, status string) {
	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = wdb.DB.Exec(`INSERT INTO brain_settings (key, value) VALUES ('last_distill_run', ?)
		ON CONFLICT(key) DO UPDATE SET value = ?`, now, now)
	_, _ = wdb.DB.Exec(`INSERT INTO brain_settings (key, value) VALUES ('last_distill_count', ?)
		ON CONFLICT(key) DO UPDATE SET value = ?`, fmt.Sprintf("%d", written), fmt.Sprintf("%d", written))
	_, _ = wdb.DB.Exec(`INSERT INTO brain_settings (key, value) VALUES ('last_distill_status', ?)
		ON CONFLICT(key) DO UPDATE SET value = ?`, status, status)
}

// distillerInput is everything the LLM sees in its analysis pass.
type distillerInput struct {
	WorkspaceName     string
	Actions           []actionRow // recent brain_action_log
	ExistingSkills    []string    // names of already-active skills (so we don't re-propose)
	ExistingProposals []string    // names of unreviewed proposals (so we don't duplicate them either)
	Soul              string      // SOUL.md
	Instructions      string      // INSTRUCTIONS.md
}

type actionRow struct {
	When         string
	ActionType   string
	TriggerText  string
	ResponseText string
	ToolsUsed    string
}

// gatherDistillerInput pulls recent activity + existing skills + .md files.
func (s *Server) gatherDistillerInput(slug string, wdb *db.WorkspaceDB) distillerInput {
	inp := distillerInput{}
	_ = s.global.DB.QueryRow("SELECT name FROM workspaces WHERE slug = ?", slug).Scan(&inp.WorkspaceName)

	rows, err := wdb.DB.Query(`SELECT created_at, action_type, trigger_text, response_text, tools_used
		FROM brain_action_log ORDER BY created_at DESC LIMIT ?`, distillerActionCap)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var a actionRow
			if rows.Scan(&a.When, &a.ActionType, &a.TriggerText, &a.ResponseText, &a.ToolsUsed) == nil {
				inp.Actions = append(inp.Actions, a)
			}
		}
	}

	brainDir := brain.BrainDir(s.cfg.DataDir, slug)
	skillsDir := filepath.Join(brainDir, "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			inp.ExistingSkills = append(inp.ExistingSkills, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	if entries, err := os.ReadDir(s.proposalsDir(slug)); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			inp.ExistingProposals = append(inp.ExistingProposals, strings.TrimSuffix(e.Name(), ".md"))
		}
	}

	if d, err := os.ReadFile(filepath.Join(brainDir, "SOUL.md")); err == nil {
		inp.Soul = string(d)
	}
	if d, err := os.ReadFile(filepath.Join(brainDir, "INSTRUCTIONS.md")); err == nil {
		inp.Instructions = string(d)
	}
	return inp
}

// formatDistillerPrompt builds the user message for the analysis call.
func formatDistillerPrompt(in distillerInput) string {
	var b strings.Builder
	now := time.Now().UTC()
	fmt.Fprintf(&b, "Today is %s.\n\n", now.Format("Monday, January 2, 2006"))
	fmt.Fprintf(&b, "## Workspace: %s\n\n", in.WorkspaceName)

	if len(in.ExistingSkills) > 0 {
		fmt.Fprintf(&b, "## Existing skills (do not re-propose these)\n%s\n\n", strings.Join(in.ExistingSkills, ", "))
	}
	if len(in.ExistingProposals) > 0 {
		fmt.Fprintf(&b, "## Existing proposals (already pending review, do not re-propose)\n%s\n\n", strings.Join(in.ExistingProposals, ", "))
	}

	if in.Soul != "" {
		fmt.Fprintf(&b, "## SOUL.md (workspace voice)\n%s\n\n", strings.TrimSpace(in.Soul))
	}
	if in.Instructions != "" {
		fmt.Fprintf(&b, "## INSTRUCTIONS.md (existing rules)\n%s\n\n", strings.TrimSpace(in.Instructions))
	}

	fmt.Fprintf(&b, "## Recent Brain activity (last %d actions, newest first)\n\n", len(in.Actions))
	for i, a := range in.Actions {
		fmt.Fprintf(&b, "### %d. [%s] %s\n", i+1, a.ActionType, a.When)
		if a.TriggerText != "" {
			tt := truncateTo(a.TriggerText, 600)
			fmt.Fprintf(&b, "**Trigger:** %s\n", tt)
		}
		if a.ResponseText != "" {
			rt := truncateTo(a.ResponseText, 600)
			fmt.Fprintf(&b, "**Brain replied:** %s\n", rt)
		}
		if a.ToolsUsed != "" && a.ToolsUsed != "[]" {
			fmt.Fprintf(&b, "**Tools used:** %s\n", a.ToolsUsed)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n---\n\nNow analyze. Look for: recurring patterns, repeated user corrections, implicit conventions, workflow shapes worth codifying. Skip patterns covered by existing skills. Output a JSON array (max 3 proposals, 0 is fine if nothing fits).")
	return b.String()
}

func truncateTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + " […]"
}

// parseProposals tolerates JSON wrapped in markdown fences (LLMs sometimes
// add them despite instructions). Returns empty slice on parse failure
// — distiller logs no_patterns and the cooldown applies.
func parseProposals(raw string) []SkillProposal {
	s := strings.TrimSpace(raw)
	// Strip optional markdown code fences
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		if strings.HasSuffix(s, "```") {
			s = s[:len(s)-3]
		}
		s = strings.TrimSpace(s)
	}
	// Also tolerate a leading "json" tag or descriptive text before the array
	if i := strings.Index(s, "["); i > 0 {
		s = s[i:]
	}
	if i := strings.LastIndex(s, "]"); i >= 0 && i < len(s)-1 {
		s = s[:i+1]
	}
	var out []SkillProposal
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// formatProposalFile composes the .md file for a saved proposal. The body
// from the LLM is already markdown; we wrap it with our own metadata at
// the top so the UI can render rationale + based_on without re-parsing.
func formatProposalFile(p SkillProposal) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", p.Name)
	fmt.Fprintf(&b, "description: %s\n", strings.ReplaceAll(p.Description, "\n", " "))
	fmt.Fprintf(&b, "engines: [%s]\n", strings.Join(p.Engines, ", "))
	if len(p.TriggerKeywords) > 0 {
		fmt.Fprintf(&b, "trigger_keywords: [%s]\n", strings.Join(p.TriggerKeywords, ", "))
	}
	b.WriteString("---\n\n")
	if p.Rationale != "" {
		fmt.Fprintf(&b, "<!-- distiller-rationale: %s -->\n", strings.ReplaceAll(p.Rationale, "\n", " "))
	}
	for _, ev := range p.BasedOn {
		fmt.Fprintf(&b, "<!-- distiller-based-on: %s -->\n", strings.ReplaceAll(ev, "\n", " "))
	}
	if len(p.BasedOn) > 0 || p.Rationale != "" {
		b.WriteString("\n")
	}
	b.WriteString(strings.TrimSpace(p.Body))
	b.WriteString("\n")
	return b.String()
}

// parseProposalFile re-reads a .md proposal file written by formatProposalFile.
// Pulls the metadata out of the YAML frontmatter + comment markers.
func parseProposalFile(content string) SkillProposal {
	var p SkillProposal
	rest := content
	if strings.HasPrefix(rest, "---\n") {
		end := strings.Index(rest[4:], "---")
		if end > 0 {
			fm := rest[4 : 4+end]
			rest = strings.TrimSpace(rest[4+end+3:])
			for _, line := range strings.Split(fm, "\n") {
				line = strings.TrimSpace(line)
				if v, ok := strings.CutPrefix(line, "name:"); ok {
					p.Name = strings.TrimSpace(v)
				} else if v, ok := strings.CutPrefix(line, "description:"); ok {
					p.Description = strings.TrimSpace(v)
				} else if v, ok := strings.CutPrefix(line, "engines:"); ok {
					p.Engines = parseListField(v)
				} else if v, ok := strings.CutPrefix(line, "trigger_keywords:"); ok {
					p.TriggerKeywords = parseListField(v)
				}
			}
		}
	}
	// Pull rationale + based_on out of HTML comments
	for _, line := range strings.Split(rest, "\n") {
		t := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(t, "<!-- distiller-rationale:"); ok {
			v = strings.TrimSuffix(v, "-->")
			p.Rationale = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(t, "<!-- distiller-based-on:"); ok {
			v = strings.TrimSuffix(v, "-->")
			p.BasedOn = append(p.BasedOn, strings.TrimSpace(v))
		}
	}
	// Body is the content after frontmatter, minus the comment lines we already extracted
	var bodyLines []string
	for _, line := range strings.Split(rest, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "<!-- distiller-") {
			continue
		}
		bodyLines = append(bodyLines, line)
	}
	p.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
	return p
}

func parseListField(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func sanitizeSkillName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '_', r == '-':
			b.WriteRune('-')
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	out = strings.Trim(out, "-")
	if out == "" {
		out = "unnamed-skill"
	}
	return out
}
