package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterWindow(t *testing.T) {
	rl := &rateLimiter{limit: 3, window: 50 * time.Millisecond, buckets: make(map[string]*rateBucket)}

	for i := 0; i < 3; i++ {
		if !rl.allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if rl.allow("1.2.3.4") {
		t.Fatal("4th request in window should be denied")
	}
	if !rl.allow("5.6.7.8") {
		t.Fatal("different IP should have its own bucket")
	}

	time.Sleep(60 * time.Millisecond)
	if !rl.allow("1.2.3.4") {
		t.Fatal("request after window reset should be allowed")
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name   string
		fly    string
		xff    string
		remote string
		want   string
	}{
		{"fly header wins", "9.9.9.9", "8.8.8.8", "127.0.0.1:1234", "9.9.9.9"},
		{"xff first entry", "", "8.8.8.8, 10.0.0.1", "127.0.0.1:1234", "8.8.8.8"},
		{"xff single", "", "8.8.8.8", "127.0.0.1:1234", "8.8.8.8"},
		{"remote addr fallback", "", "", "203.0.113.7:5555", "203.0.113.7"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = c.remote
		if c.fly != "" {
			r.Header.Set("Fly-Client-IP", c.fly)
		}
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	handler := rateLimit(2, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	do := func(ip string) int {
		r := httptest.NewRequest("POST", "/api/workspaces", nil)
		r.Header.Set("Fly-Client-IP", ip)
		r.RemoteAddr = "172.16.0.1:9999"
		w := httptest.NewRecorder()
		handler(w, r)
		return w.Code
	}

	if code := do("1.1.1.1"); code != http.StatusOK {
		t.Fatalf("1st request: got %d", code)
	}
	if code := do("1.1.1.1"); code != http.StatusOK {
		t.Fatalf("2nd request: got %d", code)
	}
	if code := do("1.1.1.1"); code != http.StatusTooManyRequests {
		t.Fatalf("3rd request: got %d, want 429", code)
	}

	// Loopback is exempt regardless of count.
	r := httptest.NewRequest("POST", "/api/workspaces", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("loopback request %d: got %d, want 200", i+1, w.Code)
		}
	}
}
