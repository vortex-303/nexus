package server

import (
	"net/http"
	"sync"
	"time"
)

// NetworkEntry records a single outbound HTTP request.
type NetworkEntry struct {
	Timestamp   string `json:"timestamp"`
	Method      string `json:"method"`
	Host        string `json:"host"`
	Path        string `json:"path"`
	StatusCode  int    `json:"status_code"`
	DurationMs  int64  `json:"duration_ms"`
	Purpose     string `json:"purpose"`
}

// NetworkLog is a thread-safe ring buffer of outbound connection records.
type NetworkLog struct {
	mu      sync.Mutex
	entries []NetworkEntry
	maxSize int
}

// NewNetworkLog creates a ring buffer with the given capacity.
func NewNetworkLog(size int) *NetworkLog {
	return &NetworkLog{
		entries: make([]NetworkEntry, 0, size),
		maxSize: size,
	}
}

// Add records a new outbound connection.
func (nl *NetworkLog) Add(e NetworkEntry) {
	nl.mu.Lock()
	defer nl.mu.Unlock()
	if len(nl.entries) >= nl.maxSize {
		nl.entries = nl.entries[1:]
	}
	nl.entries = append(nl.entries, e)
}

// Entries returns a copy of all recorded entries.
func (nl *NetworkLog) Entries() []NetworkEntry {
	nl.mu.Lock()
	defer nl.mu.Unlock()
	out := make([]NetworkEntry, len(nl.entries))
	copy(out, nl.entries)
	return out
}

// Stats returns aggregated connection stats by host.
func (nl *NetworkLog) Stats() []HostStats {
	nl.mu.Lock()
	defer nl.mu.Unlock()

	hostMap := make(map[string]*HostStats)
	for _, e := range nl.entries {
		hs, ok := hostMap[e.Host]
		if !ok {
			hs = &HostStats{Host: e.Host, Purpose: e.Purpose}
			hostMap[e.Host] = hs
		}
		hs.Count++
		hs.LastSeen = e.Timestamp
	}

	var out []HostStats
	for _, hs := range hostMap {
		out = append(out, *hs)
	}
	return out
}

// HostStats summarizes connections to a single host.
type HostStats struct {
	Host     string `json:"host"`
	Purpose  string `json:"purpose"`
	Count    int    `json:"count"`
	LastSeen string `json:"last_seen"`
}

// LoggingTransport wraps an http.RoundTripper and logs outbound requests.
type LoggingTransport struct {
	Inner   http.RoundTripper
	Log     *NetworkLog
	Purpose string // e.g. "LLM", "Webhook", "Telegram"
}

func (lt *LoggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := lt.Inner.RoundTrip(req)

	status := 0
	if resp != nil {
		status = resp.StatusCode
	}

	lt.Log.Add(NetworkEntry{
		Timestamp:  start.UTC().Format(time.RFC3339),
		Method:     req.Method,
		Host:       req.URL.Host,
		Path:       req.URL.Path,
		StatusCode: status,
		DurationMs: time.Since(start).Milliseconds(),
		Purpose:    lt.Purpose,
	})

	return resp, err
}

// handleNetworkLog returns the network connection log.
// GET /api/workspaces/{slug}/network-log
func (s *Server) handleNetworkLog(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode == "stats" {
		writeJSON(w, http.StatusOK, map[string]any{
			"hosts":   s.netLog.Stats(),
			"total":   len(s.netLog.Entries()),
			"message": "These are ALL outbound connections this Nexus instance has made. Nothing else leaves your server.",
		})
		return
	}

	entries := s.netLog.Entries()
	if entries == nil {
		entries = []NetworkEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"message": "Every outbound HTTP request from this Nexus instance. No hidden connections.",
	})
}
