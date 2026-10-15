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
	cs := make([]*counter, len(rules))
	for i, r := range rules {
		k := r.Name + "\x00" + r.Key
		c := l.counters[k]
		if c == nil || !now.Before(c.reset) {
			c = &counter{reset: l.windowEnd(r, now)}
			l.counters[k] = c
		}
		if c.n >= r.Limit {
			return false, c.reset.Sub(now), r.Name
		}
		cs[i] = c
	}
	for _, c := range cs {
		c.n++
	}
	return true, 0, ""
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
