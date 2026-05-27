package server

import (
	"time"

	"github.com/nexus-chat/nexus/internal/db"
	"github.com/nexus-chat/nexus/internal/logger"
)

// onboardingNudgeDelay is the delay before each onboarding nudge fires.
// We let the user experience the workspace before pinging them — too eager
// feels spammy. If the API key was added during the delay, the nudge is
// skipped silently at fire-time (the goroutine re-checks state before
// dispatching the notification).
var (
	geminiNudgeDelay = 5 * time.Minute
	xaiNudgeDelay    = 10 * time.Minute
)

// scheduleOnboardingNudges schedules the per-key "you're missing this
// capability" inbox notifications that fire after workspace creation.
// Called from handleCreateWorkspace right after the immediate "Welcome
// to Nexus!" notification.
//
// Each nudge:
//   - Fires after a delay (via time.AfterFunc — best-effort; lost on
//     server restart, accepted tradeoff for solo-dev simplicity)
//   - Re-checks the API key state at fire time and skips if the user
//     already added the key
//   - Includes a concrete example of what they're missing
//   - Deep-links to Brain Settings → Services tab where they fix it
func (s *Server) scheduleOnboardingNudges(slug, userID string) {
	time.AfterFunc(geminiNudgeDelay, func() {
		s.sendGeminiNudgeIfNeeded(slug, userID)
	})
	time.AfterFunc(xaiNudgeDelay, func() {
		s.sendXAINudgeIfNeeded(slug, userID)
	})
}

func (s *Server) sendGeminiNudgeIfNeeded(slug, userID string) {
	if s.getBrainSetting(slug, "gemini_api_key") != "" {
		return // user already added the key — silent skip
	}
	wdb, err := s.ws.Open(slug)
	if err != nil {
		logger.WithCategory(logger.CatSystem).Warn().Str("slug", slug).Err(err).Msg("gemini nudge: workspace open failed")
		return
	}
	if !memberStillExists(wdb, userID) {
		return // user left between create and nudge — no notification
	}
	s.createNotification(wdb, slug, userID, "system",
		"Unlock image generation",
		"Brain can compose banners, logos, mockups, and product visuals — but only with a Gemini key. Try: \"@Brain make a 16:9 hero banner for our launch\" → get a Nano Banana 2 image inline. Without it, Brain can only describe what it would draw. Add your free Gemini key in 30 seconds.",
		"/w/"+slug+"#brain-settings/services",
		"", "Nexus", "")
	logger.WithCategory(logger.CatSystem).Info().Str("slug", slug).Str("user", userID).Msg("sent gemini onboarding nudge")
}

func (s *Server) sendXAINudgeIfNeeded(slug, userID string) {
	if s.getBrainSetting(slug, "xai_api_key") != "" {
		return // user already added the key — silent skip
	}
	wdb, err := s.ws.Open(slug)
	if err != nil {
		logger.WithCategory(logger.CatSystem).Warn().Str("slug", slug).Err(err).Msg("xai nudge: workspace open failed")
		return
	}
	if !memberStillExists(wdb, userID) {
		return
	}
	s.createNotification(wdb, slug, userID, "system",
		"Unlock real-time research",
		"Brain can answer with live X/Twitter signal and cited real-time sources — but only with an xAI Grok key. Try: \"@Brain what's the latest on AI agents this week?\" → cited current results. Without it, Brain falls back to slower public web search. Add your xAI key to give Brain real-time eyes.",
		"/w/"+slug+"#brain-settings/services",
		"", "Nexus", "")
	logger.WithCategory(logger.CatSystem).Info().Str("slug", slug).Str("user", userID).Msg("sent xai onboarding nudge")
}

// memberStillExists guards against firing nudges at users who left the
// workspace during the delay window.
func memberStillExists(wdb *db.WorkspaceDB, userID string) bool {
	var n int
	_ = wdb.DB.QueryRow("SELECT COUNT(*) FROM members WHERE id = ?", userID).Scan(&n)
	return n > 0
}
