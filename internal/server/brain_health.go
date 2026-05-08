package server

import (
	"net/http"
	"time"
)

// Brain Health is the Console subsection that answers "is the workspace's
// AI surface working right now?". Pulls from existing tables — no new
// schema. Read-only, admin-gated, single fetch on tab open.
//
// Three parts:
//   1. Engine — active engine + whether it has a usable key + (for v3) the
//      latest provisioning state cached in brain_settings.
//   2. MCP — count of total + enabled MCP servers attached. Detailed
//      reachability status lives on the dedicated MCP page.
//   3. Errors — number of v3 traces that failed in the last 24h. v2 doesn't
//      land in brain_traces, so this is engine-aware.

type brainHealthEngine struct {
	Active            string `json:"active"`              // claude | openrouter
	KeyConfigured     bool   `json:"key_configured"`
	Detail            string `json:"detail"`              // human-friendly status line
	ProvisionedAgent  string `json:"provisioned_agent,omitempty"`
	ProvisionedModel  string `json:"provisioned_model,omitempty"`
	SkillsUnavailable bool   `json:"skills_unavailable,omitempty"`
}

type brainHealthMCP struct {
	Total   int `json:"total"`
	Enabled int `json:"enabled"`
}

type brainHealthErrors struct {
	V3Errors24h int    `json:"v3_errors_24h"`
	LastError   string `json:"last_error,omitempty"`
	LastErrorAt string `json:"last_error_at,omitempty"`
}

type brainHealthResp struct {
	Engine brainHealthEngine `json:"engine"`
	MCP    brainHealthMCP    `json:"mcp"`
	Errors brainHealthErrors `json:"errors"`
}

func (s *Server) handleBrainHealth(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	resp := brainHealthResp{
		Engine: brainHealthEngine{Active: s.resolveEngine(slug)},
	}

	// Engine details — what's wired and what isn't.
	switch resp.Engine.Active {
	case "claude":
		key := s.getBrainSetting(slug, "anthropic_api_key")
		resp.Engine.KeyConfigured = key != ""
		resp.Engine.ProvisionedAgent = s.getBrainSetting(slug, "mga_agent_id")
		resp.Engine.ProvisionedModel = s.getBrainSetting(slug, "mga_provisioned_model")
		resp.Engine.SkillsUnavailable = s.getBrainSetting(slug, "mga_skills_unavailable") == "true"
		switch {
		case !resp.Engine.KeyConfigured:
			resp.Engine.Detail = "Anthropic API key missing — admin can paste one in Settings → Engine."
		case resp.Engine.ProvisionedAgent == "":
			resp.Engine.Detail = "Key set; agent will provision on the next @Brain message."
		case resp.Engine.SkillsUnavailable:
			resp.Engine.Detail = "Agent provisioned, but Skills API unavailable on this Anthropic account — running degraded (no custom skills attached)."
		default:
			resp.Engine.Detail = "Healthy — agent provisioned, Skills API reachable."
		}
	case "openrouter":
		key := s.getBrainSetting(slug, "api_key")
		resp.Engine.KeyConfigured = key != ""
		resp.Engine.ProvisionedModel = s.getBrainSetting(slug, "model")
		if !resp.Engine.KeyConfigured {
			resp.Engine.Detail = "OpenRouter key missing — admin can paste one in Settings → Engine."
		} else {
			resp.Engine.Detail = "Key set; using model " + resp.Engine.ProvisionedModel + "."
		}
	default:
		resp.Engine.Detail = "Local engine — no upstream provider."
	}

	// MCP — count rows. Fail-soft if the table doesn't exist on a brand-
	// new workspace that hasn't migrated yet.
	if rows, err := wdb.DB.Query("SELECT COUNT(*), COALESCE(SUM(enabled), 0) FROM mcp_servers"); err == nil {
		if rows.Next() {
			rows.Scan(&resp.MCP.Total, &resp.MCP.Enabled)
		}
		rows.Close()
	}

	// Recent v3 errors — last 24h. brain_traces.success is 0/1.
	since := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	wdb.DB.QueryRow(`SELECT COUNT(*) FROM brain_traces WHERE success = 0 AND created_at >= ?`, since).
		Scan(&resp.Errors.V3Errors24h)

	if resp.Errors.V3Errors24h > 0 {
		// Surface the most recent failure so the admin has a starting
		// point — full traces live in the Brain Observatory.
		var snippet, ts string
		_ = wdb.DB.QueryRow(`SELECT trigger_text, created_at FROM brain_traces
			WHERE success = 0 AND created_at >= ? ORDER BY created_at DESC LIMIT 1`, since).
			Scan(&snippet, &ts)
		if len(snippet) > 120 {
			snippet = snippet[:120] + "…"
		}
		resp.Errors.LastError = snippet
		resp.Errors.LastErrorAt = ts
	}

	writeJSON(w, http.StatusOK, resp)
}
