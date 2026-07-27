package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter enforces a fixed-window per-IP request cap on unauthenticated
// endpoints. Windows are kept in memory; entries are dropped once expired.
type rateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*rateBucket
}

type rateBucket struct {
	count int
	reset time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{limit: limit, window: window, buckets: make(map[string]*rateBucket)}
	go rl.cleanupLoop()
	return rl
}

func (rl *rateLimiter) cleanupLoop() {
	for range time.Tick(time.Minute) {
		rl.mu.Lock()
		now := time.Now()
		for ip, b := range rl.buckets {
			if now.After(b.reset) {
				delete(rl.buckets, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok || now.After(b.reset) {
		rl.buckets[ip] = &rateBucket{count: 1, reset: now.Add(rl.window)}
		return true
	}
	if b.count >= rl.limit {
		return false
	}
	b.count++
	return true
}

// clientIP resolves the real client address. Fly's proxy sets Fly-Client-IP;
// X-Forwarded-For covers other reverse proxies; RemoteAddr is the fallback.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("Fly-Client-IP"); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isLoopback(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsLoopback()
}

// rateLimit wraps a handler with a per-IP fixed-window limit. Loopback
// requests (local dev, health probes) are exempt; behind Fly the proxy's
// RemoteAddr is never loopback and Fly-Client-IP takes precedence.
func rateLimit(limit int, window time.Duration, next http.HandlerFunc) http.HandlerFunc {
	rl := newRateLimiter(limit, window)
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !isLoopback(ip) && !rl.allow(ip) {
			writeError(w, http.StatusTooManyRequests, "too many requests, try again later")
			return
		}
		next(w, r)
	}
}
