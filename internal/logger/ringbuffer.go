package logger

import (
	"encoding/json"
	"sync"
	"time"
)

// LogEntry is a single queryable log record.
type LogEntry struct {
	Time     time.Time              `json:"time"`
	Level    string                 `json:"level"`
	Category string                 `json:"category"`
	Message  string                 `json:"message"`
	Fields   map[string]interface{} `json:"fields,omitempty"`
}

// RingBuffer is a thread-safe circular buffer of log entries.
type RingBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
	size    int
	pos     int
	count   int
}

// NewRingBuffer creates a ring buffer with the given capacity.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		entries: make([]LogEntry, size),
		size:    size,
	}
}

// Write implements io.Writer for zerolog multi-writer.
// It parses each JSON log line and stores it.
func (rb *RingBuffer) Write(p []byte) (n int, err error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(p, &raw); err != nil {
		return len(p), nil // skip malformed
	}

	entry := LogEntry{
		Time:   time.Now(),
		Fields: make(map[string]interface{}),
	}

	if lvl, ok := raw["level"].(string); ok {
		entry.Level = lvl
	}
	if cat, ok := raw["category"].(string); ok {
		entry.Category = cat
	}
	if msg, ok := raw["message"].(string); ok {
		entry.Message = msg
	}
	if t, ok := raw["time"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			entry.Time = parsed
		}
	}

	// Copy remaining fields
	for k, v := range raw {
		switch k {
		case "level", "category", "message", "time":
			continue
		default:
			entry.Fields[k] = v
		}
	}

	rb.mu.Lock()
	rb.entries[rb.pos] = entry
	rb.pos = (rb.pos + 1) % rb.size
	if rb.count < rb.size {
		rb.count++
	}
	rb.mu.Unlock()

	return len(p), nil
}

// QueryOpts specifies filters for log queries.
type QueryOpts struct {
	Category string
	Level    string
	Since    time.Time
	Until    time.Time
	Limit    int
	Offset   int
}

// Query returns filtered log entries, newest first.
func (rb *RingBuffer) Query(opts QueryOpts) []LogEntry {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if opts.Limit <= 0 {
		opts.Limit = 100
	}

	// Collect all entries in reverse chronological order
	var results []LogEntry
	skipped := 0

	for i := 0; i < rb.count; i++ {
		idx := (rb.pos - 1 - i + rb.size) % rb.size
		e := rb.entries[idx]

		// Apply filters
		if opts.Category != "" && e.Category != opts.Category {
			continue
		}
		if opts.Level != "" && e.Level != opts.Level {
			continue
		}
		if !opts.Since.IsZero() && e.Time.Before(opts.Since) {
			continue
		}
		if !opts.Until.IsZero() && e.Time.After(opts.Until) {
			continue
		}

		// Apply offset
		if skipped < opts.Offset {
			skipped++
			continue
		}

		results = append(results, e)
		if len(results) >= opts.Limit {
			break
		}
	}

	return results
}

// Count returns the total number of entries in the buffer.
func (rb *RingBuffer) Count() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count
}
