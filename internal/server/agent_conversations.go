package server

import (
	"sync"
	"time"
)

// ConversationKey uniquely identifies a conversation session for an agent.
type ConversationKey struct {
	Slug      string
	ChannelID string
	AgentID   string
}

// ConversationSession tracks an agent's active conversation following.
type ConversationSession struct {
	Key          ConversationKey
	LastActivity time.Time
	MessageCount int
	MaxMessages  int
	TTL          time.Duration
}

// cooldownEntry tracks when an agent last responded in a channel.
type cooldownEntry struct {
	LastResponseAt time.Time
}

// ConversationTracker manages active conversation-following sessions and cooldowns.
type ConversationTracker struct {
	mu        sync.RWMutex
	sessions  map[ConversationKey]*ConversationSession
	cooldowns map[ConversationKey]*cooldownEntry
	done      chan struct{}
}

// NewConversationTracker creates a tracker and starts the background reaper.
func NewConversationTracker() *ConversationTracker {
	ct := &ConversationTracker{
		sessions:  make(map[ConversationKey]*ConversationSession),
		cooldowns: make(map[ConversationKey]*cooldownEntry),
		done:      make(chan struct{}),
	}
	go ct.reapLoop()
	return ct
}

// StartFollowing begins tracking a conversation for an agent.
func (ct *ConversationTracker) StartFollowing(key ConversationKey, ttlMinutes, maxMessages int) {
	if ttlMinutes <= 0 {
		return
	}
	if maxMessages <= 0 {
		maxMessages = 20
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.sessions[key] = &ConversationSession{
		Key:          key,
		LastActivity: time.Now(),
		MessageCount: 0,
		MaxMessages:  maxMessages,
		TTL:          time.Duration(ttlMinutes) * time.Minute,
	}
}

// IsFollowing returns true if the agent is actively following a conversation.
func (ct *ConversationTracker) IsFollowing(key ConversationKey) bool {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	sess, ok := ct.sessions[key]
	if !ok {
		return false
	}
	return !ct.isExpired(sess)
}

// RecordMessage records a message in a conversation session.
// Returns true if the agent should respond (session is active and not exceeded).
func (ct *ConversationTracker) RecordMessage(key ConversationKey) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	sess, ok := ct.sessions[key]
	if !ok {
		return false
	}
	if ct.isExpired(sess) {
		delete(ct.sessions, key)
		return false
	}
	sess.MessageCount++
	sess.LastActivity = time.Now()
	if sess.MessageCount > sess.MaxMessages {
		delete(ct.sessions, key)
		return false
	}
	return true
}

// CheckCooldown returns true if enough time has passed since the agent's last response.
func (ct *ConversationTracker) CheckCooldown(key ConversationKey, cooldownSeconds int) bool {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	entry, ok := ct.cooldowns[key]
	if !ok {
		return true // no previous response, OK to respond
	}
	return time.Since(entry.LastResponseAt) >= time.Duration(cooldownSeconds)*time.Second
}

// RecordResponse records that an agent responded now (for cooldown tracking).
func (ct *ConversationTracker) RecordResponse(key ConversationKey) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.cooldowns[key] = &cooldownEntry{LastResponseAt: time.Now()}
}

// StopFollowing ends a conversation session.
func (ct *ConversationTracker) StopFollowing(key ConversationKey) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	delete(ct.sessions, key)
}

// Stop shuts down the reaper loop.
func (ct *ConversationTracker) Stop() {
	close(ct.done)
}

func (ct *ConversationTracker) isExpired(sess *ConversationSession) bool {
	return time.Since(sess.LastActivity) > sess.TTL
}

func (ct *ConversationTracker) reapLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ct.mu.Lock()
			for key, sess := range ct.sessions {
				if ct.isExpired(sess) {
					delete(ct.sessions, key)
				}
			}
			ct.mu.Unlock()
		case <-ct.done:
			return
		}
	}
}
