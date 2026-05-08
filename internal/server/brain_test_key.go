package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Brain key validation — explicit "Test" path so users can verify a pasted
// API key works BEFORE saving it, and a "Disable" path so they can clear a
// stale/broken key from Brain Settings without having to type a placeholder.
//
// Tests a *candidate* key (passed in the request body), not whatever is
// currently saved in brain_settings. That way the user can paste a new key
// in the input and click Test before committing.

type testKeyReq struct {
	Provider string `json:"provider"` // "claude" | "openrouter" | "gemini"
	Key      string `json:"key"`
}

type testKeyResp struct {
	OK      bool   `json:"ok"`
	Status  int    `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	// Account is a small piece of metadata the upstream provider returns —
	// limit shown to the admin (e.g. OpenRouter credits, Anthropic
	// organization name) so they can confirm "yes this is the key I meant".
	Account string `json:"account,omitempty"`
}

func (s *Server) handleBrainTestKey(w http.ResponseWriter, r *http.Request) {
	var req testKeyReq
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		writeJSON(w, http.StatusOK, testKeyResp{OK: false, Message: "key is empty"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	switch strings.ToLower(req.Provider) {
	case "claude", "anthropic":
		writeJSON(w, http.StatusOK, testAnthropicKey(ctx, req.Key))
	case "openrouter":
		writeJSON(w, http.StatusOK, testOpenRouterKey(ctx, req.Key))
	case "gemini", "google":
		writeJSON(w, http.StatusOK, testGeminiKey(ctx, req.Key))
	default:
		writeError(w, http.StatusBadRequest, "unknown provider")
	}
}

// testAnthropicKey hits POST /v1/messages with a 1-token request — cheap,
// authoritative, and mirrors how Brain v3 actually uses the key. Returns
// the model echoed back so the admin sees confirmation the key worked.
func testAnthropicKey(ctx context.Context, key string) testKeyResp {
	body := map[string]any{
		"model":      "claude-haiku-4-5",
		"max_tokens": 1,
		"messages": []map[string]any{
			{"role": "user", "content": "ping"},
		},
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return testKeyResp{OK: false, Message: err.Error()}
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return testKeyResp{OK: false, Message: "couldn't reach Anthropic: " + err.Error()}
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == 200 {
		return testKeyResp{OK: true, Status: resp.StatusCode, Account: "Anthropic"}
	}
	return testKeyResp{
		OK:      false,
		Status:  resp.StatusCode,
		Message: shortAPIError(out, resp.StatusCode),
	}
}

// testOpenRouterKey uses /api/v1/auth/key — purpose-built validation
// endpoint, returns rate limit + credits balance, no token spend.
func testOpenRouterKey(ctx context.Context, key string) testKeyResp {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://openrouter.ai/api/v1/auth/key", nil)
	if err != nil {
		return testKeyResp{OK: false, Message: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return testKeyResp{OK: false, Message: "couldn't reach OpenRouter: " + err.Error()}
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 {
		return testKeyResp{OK: false, Status: resp.StatusCode, Message: shortAPIError(out, resp.StatusCode)}
	}
	var parsed struct {
		Data struct {
			Label  string  `json:"label"`
			Limit  *float64 `json:"limit"`
			Usage  float64 `json:"usage"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &parsed)
	acct := parsed.Data.Label
	if parsed.Data.Limit != nil {
		acct = fmt.Sprintf("%s · %.2f / %.2f credits used", parsed.Data.Label, parsed.Data.Usage, *parsed.Data.Limit)
	} else if acct == "" {
		acct = "OpenRouter"
	}
	return testKeyResp{OK: true, Status: 200, Account: acct}
}

// testGeminiKey lists the available models — the cheapest authenticated
// call Google AI exposes; no generation, no token cost.
func testGeminiKey(ctx context.Context, key string) testKeyResp {
	url := "https://generativelanguage.googleapis.com/v1beta/models?key=" + key
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return testKeyResp{OK: false, Message: err.Error()}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return testKeyResp{OK: false, Message: "couldn't reach Google AI: " + err.Error()}
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == 200 {
		return testKeyResp{OK: true, Status: 200, Account: "Google AI"}
	}
	return testKeyResp{OK: false, Status: resp.StatusCode, Message: shortAPIError(out, resp.StatusCode)}
}

// shortAPIError extracts a one-line user-facing message from the upstream
// error body, falling back to a status-coded fallback. Avoids leaking the
// full error JSON into the UI.
func shortAPIError(body []byte, status int) string {
	if len(body) > 0 {
		var parsed struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error.Message != "" {
			return parsed.Error.Message
		}
	}
	switch status {
	case 401:
		return "401 Unauthorized — key invalid or revoked"
	case 402:
		return "402 — out of credits"
	case 403:
		return "403 Forbidden — key lacks access to this resource"
	case 404:
		return "404 Not Found — endpoint not available on this account"
	case 429:
		return "429 — rate limited"
	}
	return fmt.Sprintf("HTTP %d", status)
}
