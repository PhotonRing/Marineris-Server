package auth

import (
	"sync"
	"time"
)

// Throttle tracks authentication failures per key (client IP, username)
// and locks a key out with exponential backoff once it fails too often.
type Throttle struct {
	mu      sync.Mutex
	entries map[string]*throttleEntry
	now     func() time.Time

	// FreeAttempts failures are allowed within Window before locking.
	FreeAttempts int
	Window       time.Duration
	BaseLock     time.Duration
	MaxLock      time.Duration
}

type throttleEntry struct {
	failures    int
	first       time.Time
	lockedUntil time.Time
}

// NewThrottle returns a throttle allowing 5 failures per 15 minutes, then
// locking for 1 minute, doubling up to 1 hour.
func NewThrottle() *Throttle {
	return &Throttle{
		entries:      map[string]*throttleEntry{},
		now:          time.Now,
		FreeAttempts: 5,
		Window:       15 * time.Minute,
		BaseLock:     time.Minute,
		MaxLock:      time.Hour,
	}
}

// SetClock overrides the time source, for tests.
func (t *Throttle) SetClock(now func() time.Time) { t.now = now }

// Allowed reports whether every key may attempt to authenticate, and if not
// how long the caller must wait.
func (t *Throttle) Allowed(keys ...string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	var wait time.Duration
	for _, k := range keys {
		if e, ok := t.entries[k]; ok && now.Before(e.lockedUntil) {
			if d := e.lockedUntil.Sub(now); d > wait {
				wait = d
			}
		}
	}
	return wait == 0, wait
}

// Fail records a failure for every key.
func (t *Throttle) Fail(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for _, k := range keys {
		e, ok := t.entries[k]
		if !ok || now.Sub(e.first) > t.Window && now.After(e.lockedUntil) {
			e = &throttleEntry{first: now}
			t.entries[k] = e
		}
		e.failures++
		if over := e.failures - t.FreeAttempts; over > 0 {
			lock := t.BaseLock
			for i := 1; i < over && lock < t.MaxLock; i++ {
				lock *= 2
			}
			if lock > t.MaxLock {
				lock = t.MaxLock
			}
			e.lockedUntil = now.Add(lock)
		}
	}
	if len(t.entries) > 100_000 {
		t.gcLocked(now)
	}
}

// Succeed clears the failure history of every key.
func (t *Throttle) Succeed(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		delete(t.entries, k)
	}
}

// GC drops entries that are neither locked nor inside their window.
func (t *Throttle) GC() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gcLocked(t.now())
}

func (t *Throttle) gcLocked(now time.Time) {
	for k, e := range t.entries {
		if now.After(e.lockedUntil) && now.Sub(e.first) > t.Window {
			delete(t.entries, k)
		}
	}
}
