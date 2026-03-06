package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/id"
)

// Model represents a normalized OpenRouter model.
type Model struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Provider       string       `json:"provider"`
	ContextLength  int          `json:"context_length"`
	Pricing        modelPricing `json:"pricing"`
	SupportsTools  bool         `json:"supports_tools"`
	SupportsVision bool         `json:"supports_vision"`
	IsNew          bool         `json:"is_new"`
	IsFree         bool         `json:"is_free"`
}

type modelPricing struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
}

// In-memory cache for OpenRouter models
var (
	modelCache     []Model
	modelCacheTime time.Time
	modelCacheMu   sync.Mutex
)

// handleBrowseModels proxies OpenRouter /api/v1/models with 1-hour cache.
func (s *Server) handleBrowseModels(w http.ResponseWriter, r *http.Request) {
	modelCacheMu.Lock()
	if modelCache != nil && time.Since(modelCacheTime) < time.Hour {
		cached := modelCache
		modelCacheMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"models": cached})
		return
	}
	modelCacheMu.Unlock()

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://openrouter.ai/api/v1/models")
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch models")
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to read response")
		return
	}

	var result struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
			Pricing       struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
			Architecture struct {
				Modality string `json:"modality"`
			} `json:"architecture"`
			SupportedParameters []string `json:"supported_parameters"`
			CreatedAt           int64    `json:"created_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		writeError(w, http.StatusBadGateway, "failed to parse models")
		return
	}

	now := time.Now().Unix()
	models := make([]Model, 0, len(result.Data))
	for _, m := range result.Data {
		parts := splitModelID(m.ID)
		isFree := m.Pricing.Prompt == "0" || m.Pricing.Prompt == ""
		isNew := (now - m.CreatedAt) < 30*24*3600 // new if < 30 days

		// Vision: modality contains "image" on input side (e.g. "text+image->text")
		supportsVision := strings.Contains(m.Architecture.Modality, "image")

		// Tools: check supported_parameters for "tools"
		supportsTools := false
		for _, p := range m.SupportedParameters {
			if p == "tools" {
				supportsTools = true
				break
			}
		}

		models = append(models, Model{
			ID:             m.ID,
			Name:           m.Name,
			Provider:       parts[0],
			ContextLength:  m.ContextLength,
			Pricing:        modelPricing{Prompt: m.Pricing.Prompt, Completion: m.Pricing.Completion},
			SupportsTools:  supportsTools,
			SupportsVision: supportsVision,
			IsNew:          isNew,
			IsFree:         isFree,
		})
	}

	modelCacheMu.Lock()
	modelCache = models
	modelCacheTime = time.Now()
	modelCacheMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func splitModelID(id string) [2]string {
	for i, c := range id {
		if c == '/' {
			return [2]string{id[:i], id[i+1:]}
		}
	}
	return [2]string{"", id}
}

// handleGetPinnedModels returns the list of admin-pinned models for workspace use.
func (s *Server) handleGetPinnedModels(w http.ResponseWriter, r *http.Request) {
	rows, err := s.global.DB.Query("SELECT id, display_name, provider, COALESCE(context_length, 0), supports_tools FROM platform_models ORDER BY pinned_at DESC")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query models")
		return
	}
	defer rows.Close()

	type pinnedModel struct {
		ID            string `json:"id"`
		DisplayName   string `json:"display_name"`
		Provider      string `json:"provider"`
		ContextLength int    `json:"context_length"`
		SupportsTools bool   `json:"supports_tools"`
	}

	var models []pinnedModel
	for rows.Next() {
		var m pinnedModel
		if err := rows.Scan(&m.ID, &m.DisplayName, &m.Provider, &m.ContextLength, &m.SupportsTools); err != nil {
			continue
		}
		models = append(models, m)
	}
	if models == nil {
		models = []pinnedModel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// handleAdminGetModels returns pinned models (superadmin view).
func (s *Server) handleAdminGetModels(w http.ResponseWriter, r *http.Request) {
	s.handleGetPinnedModels(w, r)
}

// handleAdminSetModels sets the pinned model list.
func (s *Server) handleAdminSetModels(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)

	var req struct {
		Models []struct {
			ID            string `json:"id"`
			DisplayName   string `json:"display_name"`
			Provider      string `json:"provider"`
			ContextLength int    `json:"context_length"`
			SupportsTools bool   `json:"supports_tools"`
		} `json:"models"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tx, err := s.global.DB.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "transaction error")
		return
	}

	tx.Exec("DELETE FROM platform_models")
	for _, m := range req.Models {
		tx.Exec(
			"INSERT INTO platform_models (id, display_name, provider, context_length, supports_tools, pinned_by) VALUES (?, ?, ?, ?, ?, ?)",
			m.ID, m.DisplayName, m.Provider, m.ContextLength, m.SupportsTools, claims.AccountID,
		)
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save models")
		return
	}

	s.logAdminAction(claims, "models.update", "platform", "", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Workspace-scoped models ---

type workspaceModel struct {
	ID                string `json:"id"`
	DisplayName       string `json:"display_name"`
	Provider          string `json:"provider"`
	ContextLength     int    `json:"context_length"`
	SupportsTools     bool   `json:"supports_tools"`
	PricingPrompt     string `json:"pricing_prompt"`
	PricingCompletion string `json:"pricing_completion"`
	AddedBy           string `json:"added_by"`
	AddedAt           string `json:"added_at"`
}

// handleGetWorkspaceModels returns models saved to this workspace.
func (s *Server) handleGetWorkspaceModels(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	rows, err := wdb.DB.Query("SELECT id, display_name, provider, context_length, supports_tools, pricing_prompt, pricing_completion, added_by, added_at FROM workspace_models ORDER BY added_at DESC")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"models": []any{}})
		return
	}
	defer rows.Close()

	var models []workspaceModel
	for rows.Next() {
		var m workspaceModel
		if err := rows.Scan(&m.ID, &m.DisplayName, &m.Provider, &m.ContextLength, &m.SupportsTools, &m.PricingPrompt, &m.PricingCompletion, &m.AddedBy, &m.AddedAt); err != nil {
			continue
		}
		models = append(models, m)
	}
	if models == nil {
		models = []workspaceModel{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// handleAddWorkspaceModel adds a model to the workspace's saved list.
func (s *Server) handleAddWorkspaceModel(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req struct {
		ID                string `json:"id"`
		DisplayName       string `json:"display_name"`
		Provider          string `json:"provider"`
		ContextLength     int    `json:"context_length"`
		SupportsTools     bool   `json:"supports_tools"`
		PricingPrompt     string `json:"pricing_prompt"`
		PricingCompletion string `json:"pricing_completion"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	if req.DisplayName == "" {
		req.DisplayName = req.ID
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = wdb.DB.Exec(
		`INSERT INTO workspace_models (id, display_name, provider, context_length, supports_tools, pricing_prompt, pricing_completion, added_by, added_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET display_name = ?, provider = ?, context_length = ?, supports_tools = ?, pricing_prompt = ?, pricing_completion = ?`,
		req.ID, req.DisplayName, req.Provider, req.ContextLength, req.SupportsTools, req.PricingPrompt, req.PricingCompletion, claims.UserID, now,
		req.DisplayName, req.Provider, req.ContextLength, req.SupportsTools, req.PricingPrompt, req.PricingCompletion,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add model")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok", "id": req.ID})
}

// handleRemoveWorkspaceModel removes a model from the workspace's saved list.
func (s *Server) handleRemoveWorkspaceModel(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	modelID := r.PathValue("modelID")
	claims := auth.GetClaims(r)
	if claims == nil || claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	res, err := wdb.DB.Exec("DELETE FROM workspace_models WHERE id = ?", modelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove model")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "model not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleCheckModelAvailability checks if the configured model is available.
func (s *Server) handleCheckModelAvailability(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims := auth.GetClaims(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	_, model := s.getBrainSettings(slug)
	fallback := FreeAutoModelID

	available := true
	if model == FreeAutoModelID {
		// Virtual model is always "available"
	} else {
		modelCacheMu.Lock()
		if modelCache != nil {
			found := false
			for _, m := range modelCache {
				if m.ID == model {
					found = true
					break
				}
			}
			if !found {
				available = false
			}
		}
		modelCacheMu.Unlock()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"model":           model,
		"model_available": available,
		"fallback_model":  fallback,
	})
}

// --- Free Auto Model ---

// FreeAutoModelID is the virtual model ID that resolves to a curated free model chain.
const FreeAutoModelID = "nexus/free-auto"

// FreeModel represents a curated free model entry.
type FreeModel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Priority int    `json:"priority"`
}

// DefaultFreeModels is the built-in priority list of free models with tool support.
var DefaultFreeModels = []FreeModel{
	{ID: "deepseek/deepseek-chat-v3-0324:free", Name: "DeepSeek V3", Priority: 0},
	{ID: "qwen/qwen3-235b-a22b-thinking-2507:free", Name: "Qwen3 235B", Priority: 1},
	{ID: "meta-llama/llama-3.3-70b-instruct:free", Name: "Llama 3.3 70B", Priority: 2},
	{ID: "google/gemma-3-27b-it:free", Name: "Gemma 3 27B", Priority: 3},
	{ID: "mistralai/mistral-small-3.1-24b-instruct:free", Name: "Mistral Small 3.1", Priority: 4},
	{ID: "openai/gpt-oss-120b:free", Name: "GPT-OSS 120B", Priority: 5},
	{ID: "nvidia/nemotron-3-nano-30b-a3b:free", Name: "Nemotron 30B", Priority: 6},
	{ID: "qwen/qwen3-coder-480b-a35b-instruct:free", Name: "Qwen3 Coder 480B", Priority: 7},
	{ID: "openrouter/free", Name: "OpenRouter Free", Priority: 8},
}

// getFreeModels returns the admin-customized free model list, or defaults.
func (s *Server) getFreeModels() []FreeModel {
	rows, err := s.global.DB.Query("SELECT model_id, display_name, priority FROM free_models ORDER BY priority ASC")
	if err != nil {
		return DefaultFreeModels
	}
	defer rows.Close()

	var models []FreeModel
	for rows.Next() {
		var m FreeModel
		if err := rows.Scan(&m.ID, &m.Name, &m.Priority); err != nil {
			continue
		}
		models = append(models, m)
	}
	if len(models) == 0 {
		return DefaultFreeModels
	}
	return models
}

// handleGetFreeModels returns the curated free model list.
func (s *Server) handleGetFreeModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"models": s.getFreeModels()})
}

// handleAdminSetFreeModels replaces the free model list (superadmin only).
func (s *Server) handleAdminSetFreeModels(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r)

	var req struct {
		Models []FreeModel `json:"models"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tx, err := s.global.DB.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "transaction error")
		return
	}

	tx.Exec("DELETE FROM free_models")
	for i, m := range req.Models {
		tx.Exec(
			"INSERT INTO free_models (model_id, display_name, priority) VALUES (?, ?, ?)",
			m.ID, m.Name, i,
		)
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save free models")
		return
	}

	s.logAdminAction(claims, "free_models.update", "platform", "", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Ensure id import is used (for future use)
var _ = id.New
