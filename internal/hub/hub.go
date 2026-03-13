package hub

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Envelope is the wire format for all WebSocket messages.
type Envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Conn represents a connected client.
type Conn struct {
	ID            string
	UserID        string
	DisplayName   string
	WorkspaceSlug string
	Role          string
	Send          chan []byte
	lastMessageAt atomic.Int64 // unix nanoseconds, atomic for race-free rate limiting
	closed        atomic.Bool  // set on Unregister to prevent send-on-closed-channel
	channels      map[string]bool // subscribed channel IDs
	mu            sync.Mutex
}

// SetLastMessageAt records the time of the last message (for rate limiting).
func (c *Conn) SetLastMessageAt(t time.Time) {
	c.lastMessageAt.Store(t.UnixNano())
}

// LastMessageAt returns the time of the last message.
func (c *Conn) LastMessageAt() time.Time {
	ns := c.lastMessageAt.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// trySend attempts to send a message, returning false if the connection is closed or buffer full.
func (c *Conn) trySend(msg []byte) bool {
	if c.closed.Load() {
		return false
	}
	select {
	case c.Send <- msg:
		return true
	default:
		return false
	}
}

// NewConn creates a properly initialized Conn.
func NewConn(id, userID, displayName, workspaceSlug, role string) *Conn {
	return &Conn{
		ID:            id,
		UserID:        userID,
		DisplayName:   displayName,
		WorkspaceSlug: workspaceSlug,
		Role:          role,
		Send:          make(chan []byte, 256),
		channels:      make(map[string]bool),
	}
}

func (c *Conn) Subscribe(channelID string) {
	c.mu.Lock()
	c.channels[channelID] = true
	c.mu.Unlock()
}

func (c *Conn) Unsubscribe(channelID string) {
	c.mu.Lock()
	delete(c.channels, channelID)
	c.mu.Unlock()
}

func (c *Conn) IsSubscribed(channelID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.channels[channelID]
}

func (c *Conn) SubscribedChannels() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.channels))
	for ch := range c.channels {
		out = append(out, ch)
	}
	return out
}

// Hub manages WebSocket connections for a workspace.
type Hub struct {
	slug    string
	conns   map[string]*Conn // conn ID → Conn
	mu      sync.RWMutex
}

func NewHub(slug string) *Hub {
	return &Hub{
		slug:  slug,
		conns: make(map[string]*Conn),
	}
}

func (h *Hub) Register(c *Conn) {
	h.mu.Lock()
	h.conns[c.ID] = c
	h.mu.Unlock()
	log.Printf("[hub:%s] %s (%s) connected", h.slug, c.DisplayName, c.ID)
}

func (h *Hub) Unregister(c *Conn) {
	c.closed.Store(true) // prevent further sends before removing from map
	h.mu.Lock()
	delete(h.conns, c.ID)
	h.mu.Unlock()
	close(c.Send)
	log.Printf("[hub:%s] %s (%s) disconnected", h.slug, c.DisplayName, c.ID)
}

// Broadcast sends a message to all connections subscribed to the channel, except the sender.
func (h *Hub) Broadcast(channelID string, msg []byte, exceptConnID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.conns {
		if c.ID == exceptConnID {
			continue
		}
		if c.IsSubscribed(channelID) {
			if !c.trySend(msg) {
				log.Printf("[hub:%s] dropped message for conn %s (buffer full %d/%d)", h.slug, c.ID, len(c.Send), cap(c.Send))
			}
		}
	}
}

// BroadcastAll sends to all connections in the workspace (for presence, etc).
func (h *Hub) BroadcastAll(msg []byte, exceptConnID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.conns {
		if c.ID == exceptConnID {
			continue
		}
		if !c.trySend(msg) {
			log.Printf("[hub:%s] dropped broadcast for conn %s (buffer full %d/%d)", h.slug, c.ID, len(c.Send), cap(c.Send))
		}
	}
}

// SendTo sends a message to a specific connection.
func (h *Hub) SendTo(connID string, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if c, ok := h.conns[connID]; ok {
		if !c.trySend(msg) {
			log.Printf("[hub:%s] dropped direct message for conn %s (buffer full)", h.slug, connID)
		}
	}
}

// SendToUser sends a message to all connections of a specific user.
func (h *Hub) SendToUser(userID string, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.conns {
		if c.UserID == userID {
			if !c.trySend(msg) {
				log.Printf("[hub:%s] dropped message for user %s conn %s (buffer full)", h.slug, userID, c.ID)
			}
		}
	}
}

// OnlineMembers returns user IDs and names of connected members.
func (h *Hub) OnlineMembers() []map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	seen := make(map[string]bool)
	var members []map[string]string
	for _, c := range h.conns {
		if seen[c.UserID] {
			continue
		}
		seen[c.UserID] = true
		members = append(members, map[string]string{
			"user_id":      c.UserID,
			"display_name": c.DisplayName,
			"role":         c.Role,
		})
	}
	return members
}

// SubscribeAll subscribes ALL connections in the hub to a channel.
func (h *Hub) SubscribeAll(channelID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.conns {
		c.Subscribe(channelID)
	}
}

// SubscribeUsersByID subscribes only connections belonging to specific user IDs.
func (h *Hub) SubscribeUsersByID(channelID string, userIDs []string) {
	allowed := make(map[string]bool, len(userIDs))
	for _, uid := range userIDs {
		allowed[uid] = true
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.conns {
		if allowed[c.UserID] {
			c.Subscribe(channelID)
		}
	}
}

// UnsubscribeUsersByID unsubscribes connections belonging to specific user IDs from a channel.
func (h *Hub) UnsubscribeUsersByID(channelID string, userIDs []string) {
	remove := make(map[string]bool, len(userIDs))
	for _, uid := range userIDs {
		remove[uid] = true
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.conns {
		if remove[c.UserID] {
			c.Unsubscribe(channelID)
		}
	}
}

// BroadcastLowPriority sends a message but drops it for connections whose buffer is > 50% full.
func (h *Hub) BroadcastLowPriority(channelID string, msg []byte, exceptConnID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.conns {
		if c.ID == exceptConnID {
			continue
		}
		if c.IsSubscribed(channelID) {
			if len(c.Send)*2 > cap(c.Send) {
				continue // silently drop low-priority when buffer half full
			}
			c.trySend(msg)
		}
	}
}

// BroadcastAllLowPriority sends to all connections but drops when buffer > 50% full.
func (h *Hub) BroadcastAllLowPriority(msg []byte, exceptConnID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.conns {
		if c.ID == exceptConnID {
			continue
		}
		if len(c.Send)*2 > cap(c.Send) {
			continue
		}
		c.trySend(msg)
	}
}

func (h *Hub) ConnCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// Manager manages hubs across all workspaces.
type Manager struct {
	hubs map[string]*Hub
	mu   sync.Mutex
}

func NewManager() *Manager {
	return &Manager{hubs: make(map[string]*Hub)}
}

// Get returns (or creates) the hub for a workspace.
func (m *Manager) Get(slug string) *Hub {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.hubs[slug]; ok {
		return h
	}
	h := NewHub(slug)
	m.hubs[slug] = h
	return h
}

// ActiveSlugs returns slugs of all workspaces with active connections.
func (m *Manager) ActiveSlugs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var slugs []string
	for slug, h := range m.hubs {
		if h.ConnCount() > 0 {
			slugs = append(slugs, slug)
		}
	}
	return slugs
}
