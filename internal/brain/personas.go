package brain

import (
	"database/sql"
	"fmt"
	"strings"
)

// Persona is a workspace-scoped Brain persona — a polymorphic operating mode
// the user invokes either explicitly (via `/persona-slug` slash command,
// which rewrites the message head to `[persona:<slug>] ...`) or implicitly
// (via keyword match on the user's content, for backward compat).
//
// When invoked explicitly, the persona's Body becomes Brain's operating
// directive (clean swap — no polymorphic meta-rules). When matched by
// keyword, the body is inlined into the existing skill-context slot
// alongside the polymorphic framing (today's behavior).
//
// Same row drives both rails:
//   - v1/v2 (OpenRouter) reads BodyOpenRouter (terse) — token-efficient
//   - v3 (Claude)        reads Body            (canonical) — richer prose
type Persona struct {
	Slug            string   `json:"slug"`
	DisplayName     string   `json:"display_name"`
	Description     string   `json:"description"`
	Body            string   `json:"body"`             // canonical body (Claude)
	BodyOpenRouter  string   `json:"body_openrouter"`  // terse variant (OpenRouter)
	Model           string   `json:"model"`            // "" = workspace default
	Skills          []string `json:"skills"`           // bundled skill slugs ("" = all)
	Autonomy        string   `json:"autonomy"`         // reactive | proactive
	TriggerMode     string   `json:"trigger_mode"`     // slash | keyword | both | llm
	Keywords        []string `json:"keywords"`         // for keyword trigger
	AvatarURL       string   `json:"avatar_url"`
	BuiltinLocked   bool     `json:"builtin_locked"`
	Enabled         bool     `json:"enabled"`
	CreatedBy       string   `json:"created_by"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

// LoadPersonas returns every persona in the workspace, ordered:
// built-ins first (by display_name), then custom (by created_at desc).
func LoadPersonas(db *sql.DB) ([]Persona, error) {
	rows, err := db.Query(`
		SELECT slug, display_name, description, body, body_openrouter, model,
		       skills, autonomy, trigger_mode, keywords, avatar_url,
		       builtin_locked, enabled, created_by, created_at, updated_at
		  FROM personas
		 ORDER BY builtin_locked DESC, display_name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Persona
	for rows.Next() {
		p, err := scanPersona(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// LoadPersonaBySlug returns a single persona by slug, or sql.ErrNoRows.
func LoadPersonaBySlug(db *sql.DB, slug string) (Persona, error) {
	row := db.QueryRow(`
		SELECT slug, display_name, description, body, body_openrouter, model,
		       skills, autonomy, trigger_mode, keywords, avatar_url,
		       builtin_locked, enabled, created_by, created_at, updated_at
		  FROM personas WHERE slug = ?
	`, slug)
	return scanPersona(row)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPersona(r rowScanner) (Persona, error) {
	var p Persona
	var skillsCSV, keywordsCSV string
	var builtinLocked, enabled int
	if err := r.Scan(
		&p.Slug, &p.DisplayName, &p.Description, &p.Body, &p.BodyOpenRouter,
		&p.Model, &skillsCSV, &p.Autonomy, &p.TriggerMode, &keywordsCSV,
		&p.AvatarURL, &builtinLocked, &enabled, &p.CreatedBy,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return Persona{}, err
	}
	p.BuiltinLocked = builtinLocked != 0
	p.Enabled = enabled != 0
	p.Skills = splitCSV(skillsCSV)
	p.Keywords = splitCSV(keywordsCSV)
	return p, nil
}

func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinCSV(parts []string) string {
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			clean = append(clean, p)
		}
	}
	return strings.Join(clean, ",")
}

// SeedBuiltinPersonas inserts the canonical built-in personas (Creative
// Director, Researcher) into the workspace's personas table. Idempotent:
// uses INSERT OR IGNORE keyed on slug, so existing rows (including user
// edits) are preserved. Called on workspace open from the same path that
// runs SeedPersonaSkills.
func SeedBuiltinPersonas(db *sql.DB) error {
	for _, p := range PersonaSkills {
		_, err := db.Exec(`
			INSERT OR IGNORE INTO personas (
				slug, display_name, description, body, body_openrouter,
				model, skills, autonomy, trigger_mode, keywords, avatar_url,
				builtin_locked, enabled, created_by
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 1, 'system')
		`,
			p.Name,
			builtinDisplayName(p.Name),
			p.Description,
			p.Body,
			p.OpenRouterBody,
			"",                     // model: workspace default
			"",                     // skills: bundle empty = all
			"reactive",             // autonomy
			"both",                 // trigger_mode: keyword OR slash both work
			joinCSV(p.Keywords),    // keywords for backward-compat path
			"",                     // avatar_url
		)
		if err != nil {
			return fmt.Errorf("seed persona %s: %w", p.Name, err)
		}
	}
	return nil
}

// builtinDisplayName maps the canonical slug to its UI display name.
func builtinDisplayName(slug string) string {
	switch slug {
	case "creative-director":
		return "Creative Director"
	case "researcher":
		return "Researcher"
	default:
		// Fallback: title-case the slug ("foo-bar" → "Foo Bar")
		parts := strings.Split(slug, "-")
		for i, p := range parts {
			if p == "" {
				continue
			}
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
		return strings.Join(parts, " ")
	}
}

// ParsePersonaPrefix peels the leading `[persona:<slug>] ` token off a
// message, returning the slug and the remaining content. Returns an empty
// slug + the original content when no prefix is present.
//
// The prefix is what `/persona-slug` slash commands rewrite the input to
// before sending, and what handleBrainMention reads to decide whether to
// switch into clean-swap mode.
func ParsePersonaPrefix(content string) (slug, rest string) {
	c := strings.TrimLeft(content, " \t")
	const prefix = "[persona:"
	if !strings.HasPrefix(c, prefix) {
		return "", content
	}
	end := strings.IndexByte(c[len(prefix):], ']')
	if end < 0 {
		return "", content
	}
	slug = strings.TrimSpace(c[len(prefix) : len(prefix)+end])
	rest = strings.TrimLeft(c[len(prefix)+end+1:], " \t")
	if slug == "" {
		return "", content
	}
	return slug, rest
}

// BuildPersonaOperatingDirective produces the system-prompt overlay used by
// the clean-swap path. Lands instead of (not on top of) the polymorphic
// persona-rules section. Picks the variant best suited to the engine:
// canonical Body for Claude (v3); terse BodyOpenRouter for OpenRouter (v1/v2)
// when present.
func (p Persona) BuildPersonaOperatingDirective(engine string) string {
	body := p.Body
	if engine == "openrouter" || engine == "v1" || engine == "v2" {
		if p.BodyOpenRouter != "" {
			body = p.BodyOpenRouter
		}
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Active Persona: ")
	b.WriteString(p.DisplayName)
	b.WriteString("\n\n")
	b.WriteString("You are operating as ")
	b.WriteString(p.DisplayName)
	b.WriteString(". The directive below sets your output contract for this turn — follow it exactly. Lead your reply with `[persona:")
	b.WriteString(p.Slug)
	b.WriteString("]` on its own line so the team sees which persona is active.\n\n")
	b.WriteString(body)
	return b.String()
}
