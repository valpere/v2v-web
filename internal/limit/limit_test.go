package limit

import (
	"testing"
	"time"

	_ "time/tzdata" // the daily-reset test must never be skipped for want of a zoneinfo database
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newLimiter(t *testing.T) (*Limiter, *clock) {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 10, 15, 12, 0, 0, 0, loc)}
	return New(loc, c.now), c
}

func TestAllowsUpToTheLimitThenRefusesWithRetryAfter(t *testing.T) {
	l, c := newLimiter(t)
	r := Rule{Name: "visitor/min", Key: "v1", Limit: 3, Window: time.Minute}
	for i := 0; i < 3; i++ {
		if ok, _, _ := l.Take(r); !ok {
			t.Fatalf("request %d refused", i+1)
		}
	}
	c.t = c.t.Add(20 * time.Second)
	ok, retry, name := l.Take(r)
	if ok || name != "visitor/min" || retry < 39*time.Second || retry > 41*time.Second {
		t.Fatalf("ok=%v retry=%v name=%q", ok, retry, name)
	}
	c.t = c.t.Add(41 * time.Second) // the window has passed
	if ok, _, _ := l.Take(r); !ok {
		t.Fatal("a new window must allow again")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l, _ := newLimiter(t)
	a := Rule{Name: "n", Key: "a", Limit: 1, Window: time.Hour}
	b := Rule{Name: "n", Key: "b", Limit: 1, Window: time.Hour}
	l.Take(a)
	if ok, _, _ := l.Take(b); !ok {
		t.Fatal("another key shares nothing")
	}
}

func TestARefusedRequestConsumesNothing(t *testing.T) {
	l, _ := newLimiter(t)
	tight := Rule{Name: "tight", Key: "k", Limit: 1, Window: time.Hour}
	loose := Rule{Name: "loose", Key: "k", Limit: 5, Window: time.Hour}
	l.Take(tight, loose) // 1 used of each
	for i := 0; i < 10; i++ {
		l.Take(tight, loose) // refused by tight: must not eat loose
	}
	only := Rule{Name: "loose", Key: "k", Limit: 5, Window: time.Hour}
	used := 0
	for ok, _, _ := l.Take(only); ok; ok, _, _ = l.Take(only) {
		used++
	}
	if used != 4 {
		t.Fatalf("loose had %d left, want 4", used)
	}
}

func TestDailyRuleResetsAtLocalMidnight(t *testing.T) {
	l, c := newLimiter(t) // 12:00 Kyiv
	r := Rule{Name: "global/day", Key: "all", Limit: 2, Daily: true}
	l.Take(r)
	l.Take(r)
	if ok, _, _ := l.Take(r); ok {
		t.Fatal("third request of the day")
	}
	c.t = c.t.Add(11*time.Hour + 59*time.Minute) // 23:59
	if ok, retry, _ := l.Take(r); ok || retry > 2*time.Minute {
		t.Fatalf("still the same day: ok=%v retry=%v", ok, retry)
	}
	c.t = c.t.Add(2 * time.Minute) // 00:01 next day
	if ok, _, _ := l.Take(r); !ok {
		t.Fatal("a new day must allow again")
	}
}

func TestExpiredCountersAreForgotten(t *testing.T) {
	l, c := newLimiter(t)
	for i := 0; i < 1000; i++ {
		l.Take(Rule{Name: "n", Key: string(rune('a'+i%26)) + time.Duration(i).String(), Limit: 1, Window: time.Minute})
	}
	c.t = c.t.Add(time.Hour)
	l.Take(Rule{Name: "n", Key: "fresh", Limit: 1, Window: time.Minute})
	l.Sweep()
	if n := l.Size(); n != 1 {
		t.Fatalf("%d counters left, want 1", n)
	}
}

func TestRefusedRequestsCreateNoCounters(t *testing.T) {
	// a flood of unique keys must not grow memory when the request is refused anyway
	l, _ := newLimiter(t)
	tight := Rule{Name: "ip/min", Key: "1.1.1.1", Limit: 1, Window: time.Minute}
	l.Take(tight)
	before := l.Size()
	for i := 0; i < 500; i++ {
		l.Take(Rule{Name: "visitor/day", Key: time.Duration(i).String(), Limit: 5, Daily: true}, tight)
	}
	if l.Size() != before {
		t.Fatalf("counters grew from %d to %d on refused requests", before, l.Size())
	}
}

func TestRefundGivesBackTheCount(t *testing.T) {
	l, _ := newLimiter(t)
	r := Rule{Name: "n", Key: "k", Limit: 1, Window: time.Hour}
	if ok, _, _ := l.Take(r); !ok {
		t.Fatal("first")
	}
	l.Refund(r)
	if ok, _, _ := l.Take(r); !ok {
		t.Fatal("a refunded request frees its slot")
	}
	l.Refund(r)
	l.Refund(r) // refunding more than was taken must not go negative
	l.Take(r)
	if ok, _, _ := l.Take(r); ok {
		t.Fatal("a counter never goes below zero")
	}
}
