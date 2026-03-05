package server

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/nexus-chat/nexus/internal/auth"
	"github.com/nexus-chat/nexus/internal/id"
	"github.com/nexus-chat/nexus/internal/logger"
)

// Telegram Bot API types (minimal subset)

type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int64        `json:"message_id"`
	From      telegramUser `json:"from"`
	Chat      telegramChat `json:"chat"`
	Text      string       `json:"text"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// handleTelegramUpdate receives Telegram webhook updates.
// POST /api/telegram/{slug}/update?secret={secret}
func (s *Server) handleTelegramUpdate(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	secret := r.URL.Query().Get("secret")

	// Validate secret
	expectedSecret := s.getBrainSetting(slug, "telegram_webhook_secret")
	if expectedSecret == "" || secret != expectedSecret {
		http.NotFound(w, r)
		return
	}

	var update telegramUpdate
	if err := readJSON(r, &update); err != nil {
		writeError(w, http.StatusBadRequest, "invalid update")
		return
	}

	// Ignore non-message updates
	if update.Message == nil || update.Message.Text == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	msg := update.Message

	wdb, err := s.ws.Open(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace error")
		return
	}

	// Look up or create channel for this Telegram chat
	chatIDStr := strconv.FormatInt(msg.Chat.ID, 10)
	var channelID string
	_ = wdb.DB.QueryRow(
		"SELECT channel_id FROM channel_integrations WHERE source_type = 'telegram' AND source_key = ?",
		chatIDStr,
	).Scan(&channelID)

	if channelID == "" {
		channelID = id.New()
		chName := fmt.Sprintf("telegram: %s", msg.From.FirstName)
		if msg.Chat.Type == "group" || msg.Chat.Type == "supergroup" {
			chName = fmt.Sprintf("telegram: group %s", chatIDStr)
		}
		_, err = wdb.DB.Exec(
			"INSERT INTO channels (id, name, type, created_by) VALUES (?, ?, 'public', 'system')",
			channelID, chName,
		)
		if err != nil {
			logger.WithCategory(logger.CatSystem).Error().Err(err).Str("workspace", slug).Msg("telegram: failed to create channel")
			writeError(w, http.StatusInternalServerError, "channel creation failed")
			return
		}
		_, _ = wdb.DB.Exec(
			"INSERT OR IGNORE INTO channel_integrations (id, channel_id, source_type, source_key, label) VALUES (?, ?, 'telegram', ?, ?)",
			id.New(), channelID, chatIDStr, chName,
		)
	}

	// Sender info
	senderID := fmt.Sprintf("telegram:%d", msg.From.ID)
	senderName := msg.From.FirstName
	if msg.From.LastName != "" {
		senderName += " " + msg.From.LastName
	}

	// Get autonomy
	autonomy := s.getBrainSetting(slug, "telegram_autonomy")
	if autonomy == "" {
		autonomy = "autonomous"
	}

	// Build reply function
	botToken := s.getBrainSetting(slug, "telegram_bot_token")
	chatID := msg.Chat.ID
	replyFn := func(response string) {
		if botToken != "" {
			sendTelegramMessage(botToken, chatID, response)
		}
	}

	// Respond 200 immediately (Telegram expects fast response)
	w.WriteHeader(http.StatusOK)

	// Process asynchronously
	go s.ingestExternalMessage(slug, channelID, senderID, senderName, msg.Text, "telegram", autonomy, replyFn)
}

// sendTelegramMessage sends a message via the Telegram Bot API.
func sendTelegramMessage(botToken string, chatID int64, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		logger.WithCategory(logger.CatSystem).Error().Err(err).Msg("telegram: failed to send message")
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		logger.WithCategory(logger.CatSystem).Error().Int("status", resp.StatusCode).Msg("telegram: API error")
		return fmt.Errorf("telegram API error: %d", resp.StatusCode)
	}
	return nil
}

// registerTelegramWebhook calls the Telegram setWebhook API to register our endpoint.
func (s *Server) registerTelegramWebhook(slug, botToken, secret string) {
	domain := s.cfg.Domain
	if domain == "" {
		logger.WithCategory(logger.CatSystem).Warn().Str("workspace", slug).Msg("telegram: no domain configured, cannot register webhook")
		return
	}

	webhookURL := fmt.Sprintf("https://%s/api/telegram/%s/update?secret=%s", domain, slug, secret)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", botToken)

	payload := map[string]string{"url": webhookURL}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(apiURL, "application/json", bytes.NewReader(body))
	if err != nil {
		logger.WithCategory(logger.CatSystem).Error().Err(err).Str("workspace", slug).Msg("telegram: failed to register webhook")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		logger.WithCategory(logger.CatSystem).Info().Str("workspace", slug).Str("url", webhookURL).Msg("telegram: webhook registered")
	} else {
		logger.WithCategory(logger.CatSystem).Error().Str("workspace", slug).Int("status", resp.StatusCode).Msg("telegram: webhook registration failed")
	}
}

// handleListTelegramChats returns linked Telegram chats for a workspace.
// GET /api/workspaces/{slug}/brain/telegram/chats
func (s *Server) handleListTelegramChats(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
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

	rows, err := wdb.DB.Query(
		"SELECT id, channel_id, source_key, label, created_at FROM channel_integrations WHERE source_type = 'telegram' ORDER BY created_at DESC",
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	var chats []map[string]string
	for rows.Next() {
		var cid, chID, sourceKey, label, createdAt string
		if rows.Scan(&cid, &chID, &sourceKey, &label, &createdAt) == nil {
			chats = append(chats, map[string]string{
				"id":         cid,
				"channel_id": chID,
				"chat_id":    sourceKey,
				"label":      label,
				"created_at": createdAt,
			})
		}
	}

	if chats == nil {
		chats = []map[string]string{}
	}
	writeJSON(w, http.StatusOK, chats)
}

// handleDeleteTelegramChat unlinks a Telegram chat.
// DELETE /api/workspaces/{slug}/brain/telegram/chats/{id}
func (s *Server) handleDeleteTelegramChat(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	integrationID := r.PathValue("id")
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

	result, _ := wdb.DB.Exec("DELETE FROM channel_integrations WHERE id = ? AND source_type = 'telegram'", integrationID)
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeError(w, http.StatusNotFound, "chat not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// onTelegramBotTokenSaved is called when the telegram_bot_token setting is saved.
// It auto-generates a webhook secret and registers the webhook with Telegram.
func (s *Server) onTelegramBotTokenSaved(slug, botToken string) {
	if botToken == "" {
		return
	}

	// Generate webhook secret
	secretBytes := make([]byte, 32)
	rand.Read(secretBytes)
	secret := hex.EncodeToString(secretBytes)

	wdb, err := s.ws.Open(slug)
	if err != nil {
		return
	}

	_, _ = wdb.DB.Exec(
		"INSERT INTO brain_settings (key, value) VALUES ('telegram_webhook_secret', ?) ON CONFLICT(key) DO UPDATE SET value = ?",
		secret, secret,
	)

	// Register webhook with Telegram API
	go s.registerTelegramWebhook(slug, botToken, secret)
}
