package cache

import (
	"sync"
	"time"
)

// Status represents the result of a cache lookup.
type Status int

const (
	// StatusMiss: the cache has no information about the key. The caller
	// should query the underlying source (e.g. database).
	StatusMiss Status = iota
	// StatusHit: the key exists and a valid value is returned.
	StatusHit
	// StatusNotFound: a previous lookup confirmed the key does not exist
	// in the underlying source. Caller should not query again until the
	// negative entry expires.
	StatusNotFound
)

func (s Status) String() string {
	switch s {
	case StatusHit:
		return "hit"
	case StatusNotFound:
		return "not_found"
	default:
		return "miss"
	}
}

type entry[V any] struct {
	value     V
	expiresAt time.Time // zero means no expiry
}

// Memory is a generic, thread-safe in-memory cache that keeps positive
// (value) and negative (not-found) entries in separate tables.
type Memory[K comparable, V any] struct {
	mu       sync.RWMutex
	items    map[K]entry[V]
	notFound map[K]time.Time // value: expiresAt; zero means no expiry

	ttl    time.Duration // default TTL for positive entries (0 = no expiry)
	negTTL time.Duration // default TTL for negative entries (0 = no expiry)

	stopCh  chan struct{}
	stopped bool
}

// NewMemory creates a new cache with the given TTLs.
//   - ttl: lifetime of positive entries. 0 disables expiry.
//   - negTTL: lifetime of negative entries. 0 disables expiry.
//
// A background janitor goroutine runs if at least one TTL is > 0.
// Call Close() when done.
func NewMemory[K comparable, V any](ttl, negTTL time.Duration) *Memory[K, V] {
	c := &Memory[K, V]{
		items:    make(map[K]entry[V]),
		notFound: make(map[K]time.Time),
		ttl:      ttl,
		negTTL:   negTTL,
		stopCh:   make(chan struct{}),
	}
	if interval := pickInterval(ttl, negTTL); interval > 0 {
		go c.janitor(interval)
	}
	return c
}

func pickInterval(a, b time.Duration) time.Duration {
	switch {
	case a > 0 && b > 0:
		if a < b {
			return a
		}
		return b
	case a > 0:
		return a
	case b > 0:
		return b
	default:
		return 0
	}
}

// Set stores the value with the default TTL. Any existing not-found entry
// for the same key is removed.
func (c *Memory[K, V]) Set(key K, value V) {
	c.SetWithTTL(key, value, c.ttl)
}

// SetWithTTL stores the value with the given TTL. ttl=0 means no expiry.
func (c *Memory[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	c.mu.Lock()
	c.items[key] = entry[V]{value: value, expiresAt: exp}
	delete(c.notFound, key)
	c.mu.Unlock()
}

// MarkNotFound records the key as confirmed-missing using the default
// negative TTL. Any positive entry for the same key is removed.
func (c *Memory[K, V]) MarkNotFound(key K) {
	c.MarkNotFoundWithTTL(key, c.negTTL)
}

// MarkNotFoundWithTTL records a not-found entry with the given TTL.
// ttl=0 means no expiry.
func (c *Memory[K, V]) MarkNotFoundWithTTL(key K, ttl time.Duration) {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	c.mu.Lock()
	c.notFound[key] = exp
	delete(c.items, key)
	c.mu.Unlock()
}

// Lookup returns the cached state for the key. Three outcomes:
//   - StatusHit: value is returned.
//   - StatusNotFound: key is known to be missing in the source.
//   - StatusMiss: nothing is known; query the source.
//
// For non-Hit outcomes, value is the zero value.
func (c *Memory[K, V]) Lookup(key K) (V, Status) {
	c.mu.RLock()
	e, hasItem := c.items[key]
	exp, hasNF := c.notFound[key]
	c.mu.RUnlock()

	var zero V
	now := time.Now()

	if hasItem {
		if e.expiresAt.IsZero() || now.Before(e.expiresAt) {
			return e.value, StatusHit
		}
		// Lazy cleanup of expired item.
		c.mu.Lock()
		if cur, ok := c.items[key]; ok && cur.expiresAt.Equal(e.expiresAt) {
			delete(c.items, key)
		}
		c.mu.Unlock()
	}

	if hasNF {
		if exp.IsZero() || now.Before(exp) {
			return zero, StatusNotFound
		}
		// Lazy cleanup of expired not-found.
		c.mu.Lock()
		if cur, ok := c.notFound[key]; ok && cur.Equal(exp) {
			delete(c.notFound, key)
		}
		c.mu.Unlock()
	}

	return zero, StatusMiss
}

// Get is a classic two-value cache API. It returns (value, true) only on
// StatusHit; both Miss and NotFound collapse to (zero, false). Use Lookup
// when you need to distinguish those two cases.
func (c *Memory[K, V]) Get(key K) (V, bool) {
	v, st := c.Lookup(key)
	return v, st == StatusHit
}

// Delete removes the key from both positive and negative tables.
func (c *Memory[K, V]) Delete(key K) {
	c.mu.Lock()
	delete(c.items, key)
	delete(c.notFound, key)
	c.mu.Unlock()
}

// Len returns the number of valid (non-expired) positive entries.
func (c *Memory[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := time.Now()
	n := 0
	for _, e := range c.items {
		if e.expiresAt.IsZero() || now.Before(e.expiresAt) {
			n++
		}
	}
	return n
}

// NotFoundLen returns the number of valid negative entries.
func (c *Memory[K, V]) NotFoundLen() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := time.Now()
	n := 0
	for _, exp := range c.notFound {
		if exp.IsZero() || now.Before(exp) {
			n++
		}
	}
	return n
}

// Keys returns the keys of all valid positive entries.
func (c *Memory[K, V]) Keys() []K {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := time.Now()
	out := make([]K, 0, len(c.items))
	for k, e := range c.items {
		if e.expiresAt.IsZero() || now.Before(e.expiresAt) {
			out = append(out, k)
		}
	}
	return out
}

// Values returns the values of all valid positive entries.
func (c *Memory[K, V]) Values() []V {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := time.Now()
	out := make([]V, 0, len(c.items))
	for _, e := range c.items {
		if e.expiresAt.IsZero() || now.Before(e.expiresAt) {
			out = append(out, e.value)
		}
	}
	return out
}

// Items returns a snapshot map of all valid positive entries.
func (c *Memory[K, V]) Items() map[K]V {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := time.Now()
	out := make(map[K]V, len(c.items))
	for k, e := range c.items {
		if e.expiresAt.IsZero() || now.Before(e.expiresAt) {
			out[k] = e.value
		}
	}
	return out
}

// NotFoundKeys returns the keys of all valid negative entries.
func (c *Memory[K, V]) NotFoundKeys() []K {
	c.mu.RLock()
	defer c.mu.RUnlock()
	now := time.Now()
	out := make([]K, 0, len(c.notFound))
	for k, exp := range c.notFound {
		if exp.IsZero() || now.Before(exp) {
			out = append(out, k)
		}
	}
	return out
}

// Clear removes all entries (positive and negative).
func (c *Memory[K, V]) Clear() {
	c.mu.Lock()
	c.items = make(map[K]entry[V])
	c.notFound = make(map[K]time.Time)
	c.mu.Unlock()
}

// ClearNotFound removes only the negative entries. Useful after a bulk
// insert into the underlying source, when negative entries should be
// re-evaluated.
func (c *Memory[K, V]) ClearNotFound() {
	c.mu.Lock()
	c.notFound = make(map[K]time.Time)
	c.mu.Unlock()
}

// Close stops the janitor goroutine. Safe to call multiple times.
func (c *Memory[K, V]) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.stopped {
		close(c.stopCh)
		c.stopped = true
	}
}

func (c *Memory[K, V]) janitor(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			c.evictExpired()
		case <-c.stopCh:
			return
		}
	}
}

func (c *Memory[K, V]) evictExpired() {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.items {
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			delete(c.items, k)
		}
	}
	for k, exp := range c.notFound {
		if !exp.IsZero() && now.After(exp) {
			delete(c.notFound, k)
		}
	}
}
