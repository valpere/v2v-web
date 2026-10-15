package server

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/valpere/v2v-web/internal/limit"
)

// Limits caps what one browser, one address and the whole site may spend. A
// public page calls a paid LLM: the engine's own per-chat limits do not bound a
// visitor who opens new sessions, and its daily total is shared with the Telegram bot.
type Limits struct {
	VisitorPerMin, VisitorPerDay              int // messages per visitor (cookie)
	IPPerMin, IPPerDay                        int // messages per client address
	SessionsVisitorPerHour, SessionsIPPerHour int // new conversations
	GlobalPerDay                              int // messages for the whole site, per local day
}

// DefaultLimits is the agreed starting table (a demo conversation is 10–20 messages).
func DefaultLimits() Limits {
	return Limits{
		VisitorPerMin: 6, VisitorPerDay: 60,
		IPPerMin: 12, IPPerDay: 120,
		SessionsVisitorPerHour: 10, SessionsIPPerHour: 20,
		GlobalPerDay: 150,
	}
}

// clientIP is the address the request came from. Behind a trusted reverse proxy
// the rightmost X-Forwarded-For entry is the client as the proxy saw it; anything
// to its left was supplied by the client and is ignored.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// withLimits refuses a counted request with 429 before it reaches the engine.
// Only POST /v1/sessions and POST /v1/sessions/{id}/messages are counted.
func withLimits(l Limits, lim *limit.Limiter, trustProxy bool, next http.Handler) http.Handler {
	var calls atomic.Uint64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rules := rulesFor(l, r, clientIP(r, trustProxy))
		if len(rules) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if calls.Add(1)%256 == 0 {
			lim.Sweep()
		}
		ok, retry, name := lim.Take(rules...)
		if ok {
			next.ServeHTTP(w, r)
			return
		}
		secs := int(math.Ceil(retry.Seconds()))
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"` + refusal(name) + `"}`))
	})
}

func rulesFor(l Limits, r *http.Request, ip string) []limit.Rule {
	if r.Method != http.MethodPost {
		return nil
	}
	vid := r.Header.Get("X-Visitor-Id") // set by withVisitor; empty when no engine key (dev)
	var rules []limit.Rule
	add := func(name, key string, n int, window time.Duration, daily bool) {
		if key != "" && n > 0 {
			rules = append(rules, limit.Rule{Name: name, Key: key, Limit: n, Window: window, Daily: daily})
		}
	}
	switch {
	case r.URL.Path == "/v1/sessions":
		add("sessions/visitor/hour", vid, l.SessionsVisitorPerHour, time.Hour, false)
		add("sessions/ip/hour", ip, l.SessionsIPPerHour, time.Hour, false)
	case strings.HasPrefix(r.URL.Path, "/v1/sessions/") && strings.HasSuffix(r.URL.Path, "/messages"):
		add("visitor/min", vid, l.VisitorPerMin, time.Minute, false)
		add("visitor/day", vid, l.VisitorPerDay, 0, true)
		add("ip/min", ip, l.IPPerMin, time.Minute, false)
		add("ip/day", ip, l.IPPerDay, 0, true)
		add("global/day", "all", l.GlobalPerDay, 0, true)
	}
	return rules
}

// refusal is the text the browser shows (the client prints the "error" field).
func refusal(rule string) string {
	switch {
	case rule == "global/day":
		return "Ліміт демо на сьогодні вичерпано — спробуйте завтра."
	case strings.HasPrefix(rule, "sessions/"):
		return "Забагато нових розмов — зачекайте трохи."
	case strings.HasSuffix(rule, "/day"):
		return "Денний ліміт повідомлень вичерпано — спробуйте завтра."
	default:
		return "Забагато повідомлень — зачекайте хвилину."
	}
}
