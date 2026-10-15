// Package limit is an in-memory fixed-window limiter with all-or-nothing
// checks: a request is counted against every rule it matches, or against none
// when any of them refuses. Counters are lost on restart, which is acceptable
// for a demo.
package limit

import (
	"sync"
	"time"
)

// Rule limits one key: Limit requests per Window, or per local calendar day
// when Daily is set (the day rolls over at midnight in the limiter's location).
type Rule struct {
	Name   string // reported when this rule refuses, e.g. "visitor/day"
	Key    string
	Limit  int
	Window time.Duration
	Daily  bool
}

type counter struct {
	n     int
	reset time.Time
}

// Limiter is safe for concurrent use.
type Limiter struct {
	loc *time.Location
	now func() time.Time

	mu       sync.Mutex
	counters map[string]*counter
}

// New returns a Limiter whose daily rules roll over in loc. A nil now means time.Now.
func New(loc *time.Location, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{loc: loc, now: now, counters: make(map[string]*counter)}
}

func (l *Limiter) windowEnd(r Rule, now time.Time) time.Time {
	if r.Daily {
		y, m, d := now.In(l.loc).Date()
		return time.Date(y, m, d+1, 0, 0, 0, 0, l.loc)
	}
	return now.Add(r.Window)
}

// Take counts one request against every rule. If any rule is exhausted nothing
// is counted and Take returns false, how long until that rule frees up, and its name.
func (l *Limiter) Take(rules ...Rule) (ok bool, retryAfter time.Duration, name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	// First pass only reads: a refused request must create no counters, or a flood
	// of unique keys would grow the map for nothing.
	for _, r := range rules {
		if c := l.counters[key(r)]; c != nil && now.Before(c.reset) && c.n >= r.Limit {
			return false, c.reset.Sub(now), r.Name
		}
	}
	for _, r := range rules {
		k := key(r)
		c := l.counters[k]
		if c == nil || !now.Before(c.reset) {
			c = &counter{reset: l.windowEnd(r, now)}
			l.counters[k] = c
		}
		c.n++
	}
	return true, 0, ""
}

func key(r Rule) string { return r.Name + "\x00" + r.Key }

// Refund gives back one count per rule (a request that turned out to cost nothing).
// A counter never goes below zero.
func (l *Limiter) Refund(rules ...Rule) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range rules {
		if c := l.counters[key(r)]; c != nil && c.n > 0 {
			c.n--
		}
	}
}

// Sweep forgets counters whose window has passed.
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for k, c := range l.counters {
		if !now.Before(c.reset) {
			delete(l.counters, k)
		}
	}
}

// Size is the number of live counters (for tests and diagnostics).
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.counters)
}
