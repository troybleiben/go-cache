package cache

import (
	"errors"
	"sync"
	"time"
)

// IndexFunc derives an index key from a value. Return ok=false to skip
// this index for the given value (e.g. an optional field is empty); the
// value is still stored, just not reachable through this index.
type IndexFunc[V any] func(V) (key any, ok bool)

// ErrUnknownIndex is returned when an index name is used that was not
// registered at construction time.
var ErrUnknownIndex = errors.New("cache: unknown index")

// MemoryMultiCache stores values that can be looked up through multiple
// independent indexes (for example: by ID and by Code). It is generic
// over the value type V; index keys flow through `any` because each
// index may use a different key type.
//
// Consistency guarantees:
//   - Set updates all indexes atomically. If a new value collides with
//     an existing entry on any index, the old entry's other index keys
//     are removed too — there are no stale entries.
//   - Delete removes the value from every index it is registered under.
//   - Each index maintains its own not-found set.
type MemoryMultiCache[V any] struct {
	mu sync.RWMutex

	// Canonical storage: each value has an internal id.
	items  map[uint64]multiEntry[V]
	nextID uint64

	// indexName -> indexKey -> canonical id
	indexes map[string]map[any]uint64

	// Index extractors are fixed at construction time.
	extractors map[string]IndexFunc[V]

	// Negative entries per index: indexName -> indexKey -> expiresAt
	notFound map[string]map[any]time.Time

	ttl    time.Duration
	negTTL time.Duration

	stopCh  chan struct{}
	stopped bool
}

type multiEntry[V any] struct {
	value     V
	expiresAt time.Time
	// Index keys this value currently occupies. Used to clean up sibling
	// index entries when this value is evicted, deleted, or replaced.
	indexKeys map[string]any
}

// NewMemoryMultiCache creates a new multi-index cache.
//   - ttl:        default TTL for positive entries (0 = no expiry)
//   - negTTL:     default TTL for negative entries (0 = no expiry)
//   - extractors: map of index name to key extractor; at least one required.
func NewMemoryMultiCache[V any](
	ttl, negTTL time.Duration,
	extractors map[string]IndexFunc[V],
) *MemoryMultiCache[V] {
	if len(extractors) == 0 {
		panic("cache.NewMemoryMultiCache: at least one index extractor required")
	}

	mc := &MemoryMultiCache[V]{
		items:      make(map[uint64]multiEntry[V]),
		indexes:    make(map[string]map[any]uint64, len(extractors)),
		extractors: make(map[string]IndexFunc[V], len(extractors)),
		notFound:   make(map[string]map[any]time.Time, len(extractors)),
		ttl:        ttl,
		negTTL:     negTTL,
		stopCh:     make(chan struct{}),
	}
	for name, fn := range extractors {
		mc.indexes[name] = make(map[any]uint64)
		mc.notFound[name] = make(map[any]time.Time)
		mc.extractors[name] = fn
	}

	if interval := pickInterval(ttl, negTTL); interval > 0 {
		go mc.janitor(interval)
	}
	return mc
}

// Set stores the value, registering it under every index whose extractor
// returns ok=true. Existing entries colliding on any index are fully
// removed (from all their indexes) before the new value is inserted.
func (mc *MemoryMultiCache[V]) Set(value V) {
	mc.SetWithTTL(value, mc.ttl)
}

// SetWithTTL is like Set but with a per-call TTL override. ttl=0 = no expiry.
func (mc *MemoryMultiCache[V]) SetWithTTL(value V, ttl time.Duration) {
	// Compute index keys outside the lock.
	keys := make(map[string]any, len(mc.extractors))
	for name, fn := range mc.extractors {
		if k, ok := fn(value); ok {
			keys[name] = k
		}
	}

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}

	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Evict any existing entries that collide on any of the new keys.
	// This prevents stale index entries when a value is updated and
	// one of its index keys has changed.
	collidingIDs := make(map[uint64]struct{})
	for name, key := range keys {
		if id, ok := mc.indexes[name][key]; ok {
			collidingIDs[id] = struct{}{}
		}
	}
	for id := range collidingIDs {
		mc.evictByIDLocked(id)
	}

	// Allocate a new id and insert.
	mc.nextID++
	id := mc.nextID
	mc.items[id] = multiEntry[V]{
		value:     value,
		expiresAt: exp,
		indexKeys: keys,
	}
	for name, key := range keys {
		mc.indexes[name][key] = id
		// Inserting clears any negative entry on the same index key.
		delete(mc.notFound[name], key)
	}
}

// MarkNotFound records a not-found entry on the given index using the
// default negative TTL. If a positive entry exists at that index key,
// it is removed (along with its other index entries).
func (mc *MemoryMultiCache[V]) MarkNotFound(indexName string, key any) error {
	return mc.MarkNotFoundWithTTL(indexName, key, mc.negTTL)
}

// MarkNotFoundWithTTL is like MarkNotFound with a per-call TTL override.
func (mc *MemoryMultiCache[V]) MarkNotFoundWithTTL(indexName string, key any, ttl time.Duration) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	idx, ok := mc.indexes[indexName]
	if !ok {
		return ErrUnknownIndex
	}

	// Remove any positive entry sitting on this index key.
	if id, ok := idx[key]; ok {
		mc.evictByIDLocked(id)
	}

	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	mc.notFound[indexName][key] = exp
	return nil
}

// Lookup returns the cached state for (indexName, key).
func (mc *MemoryMultiCache[V]) Lookup(indexName string, key any) (V, Status, error) {
	var zero V

	mc.mu.RLock()
	idx, ok := mc.indexes[indexName]
	if !ok {
		mc.mu.RUnlock()
		return zero, StatusMiss, ErrUnknownIndex
	}
	id, hasItem := idx[key]
	var e multiEntry[V]
	if hasItem {
		e = mc.items[id]
	}
	exp, hasNF := mc.notFound[indexName][key]
	mc.mu.RUnlock()

	now := time.Now()

	if hasItem {
		if e.expiresAt.IsZero() || now.Before(e.expiresAt) {
			return e.value, StatusHit, nil
		}
		// Lazy cleanup of expired item — drop the whole entry across all indexes.
		mc.mu.Lock()
		if cur, ok := mc.items[id]; ok && cur.expiresAt.Equal(e.expiresAt) {
			mc.evictByIDLocked(id)
		}
		mc.mu.Unlock()
	}

	if hasNF {
		if exp.IsZero() || now.Before(exp) {
			return zero, StatusNotFound, nil
		}
		mc.mu.Lock()
		if cur, ok := mc.notFound[indexName][key]; ok && cur.Equal(exp) {
			delete(mc.notFound[indexName], key)
		}
		mc.mu.Unlock()
	}

	return zero, StatusMiss, nil
}

// Get is the two-value convenience form of Lookup. Returns (zero, false)
// for both Miss and NotFound. Returns (zero, false) and silently ignores
// unknown index names — use Lookup if you need to detect that.
func (mc *MemoryMultiCache[V]) Get(indexName string, key any) (V, bool) {
	v, st, err := mc.Lookup(indexName, key)
	if err != nil {
		var zero V
		return zero, false
	}
	return v, st == StatusHit
}

// Delete removes the value reachable via (indexName, key) from every
// index it occupies. Returns true if a value was removed.
func (mc *MemoryMultiCache[V]) Delete(indexName string, key any) (bool, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	idx, ok := mc.indexes[indexName]
	if !ok {
		return false, ErrUnknownIndex
	}
	id, ok := idx[key]
	if !ok {
		return false, nil
	}
	mc.evictByIDLocked(id)
	return true, nil
}

// DeleteValue removes the value from every index it occupies, by
// recomputing its index keys with the registered extractors. Useful
// when the caller has the value object directly.
func (mc *MemoryMultiCache[V]) DeleteValue(value V) bool {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	for name, fn := range mc.extractors {
		k, ok := fn(value)
		if !ok {
			continue
		}
		if id, ok := mc.indexes[name][k]; ok {
			mc.evictByIDLocked(id)
			return true
		}
	}
	return false
}

// Len returns the number of valid (non-expired) values stored.
func (mc *MemoryMultiCache[V]) Len() int {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	now := time.Now()
	n := 0
	for _, e := range mc.items {
		if e.expiresAt.IsZero() || now.Before(e.expiresAt) {
			n++
		}
	}
	return n
}

// NotFoundLen returns the number of valid negative entries on the given index.
func (mc *MemoryMultiCache[V]) NotFoundLen(indexName string) (int, error) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	nf, ok := mc.notFound[indexName]
	if !ok {
		return 0, ErrUnknownIndex
	}
	now := time.Now()
	n := 0
	for _, exp := range nf {
		if exp.IsZero() || now.Before(exp) {
			n++
		}
	}
	return n, nil
}

// Values returns a snapshot of all valid values.
func (mc *MemoryMultiCache[V]) Values() []V {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	now := time.Now()
	out := make([]V, 0, len(mc.items))
	for _, e := range mc.items {
		if e.expiresAt.IsZero() || now.Before(e.expiresAt) {
			out = append(out, e.value)
		}
	}
	return out
}

// Keys returns the keys present on the given index, for valid entries only.
func (mc *MemoryMultiCache[V]) Keys(indexName string) ([]any, error) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	idx, ok := mc.indexes[indexName]
	if !ok {
		return nil, ErrUnknownIndex
	}
	now := time.Now()
	out := make([]any, 0, len(idx))
	for k, id := range idx {
		e := mc.items[id]
		if e.expiresAt.IsZero() || now.Before(e.expiresAt) {
			out = append(out, k)
		}
	}
	return out, nil
}

// IndexNames returns the registered index names.
func (mc *MemoryMultiCache[V]) IndexNames() []string {
	out := make([]string, 0, len(mc.extractors))
	for name := range mc.extractors {
		out = append(out, name)
	}
	return out
}

// Clear removes everything (positive and negative across all indexes).
func (mc *MemoryMultiCache[V]) Clear() {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.items = make(map[uint64]multiEntry[V])
	for name := range mc.indexes {
		mc.indexes[name] = make(map[any]uint64)
		mc.notFound[name] = make(map[any]time.Time)
	}
}

// ClearNotFound removes negative entries on the given index. Pass an
// empty string to clear negatives across all indexes.
func (mc *MemoryMultiCache[V]) ClearNotFound(indexName string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if indexName == "" {
		for name := range mc.notFound {
			mc.notFound[name] = make(map[any]time.Time)
		}
		return nil
	}
	if _, ok := mc.notFound[indexName]; !ok {
		return ErrUnknownIndex
	}
	mc.notFound[indexName] = make(map[any]time.Time)
	return nil
}

// Close stops the janitor goroutine. Safe to call multiple times.
func (mc *MemoryMultiCache[V]) Close() {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if !mc.stopped {
		close(mc.stopCh)
		mc.stopped = true
	}
}

// evictByIDLocked removes the value with the given canonical id from the
// items map and from every index it occupies. Caller must hold mc.mu.
func (mc *MemoryMultiCache[V]) evictByIDLocked(id uint64) {
	e, ok := mc.items[id]
	if !ok {
		return
	}
	for name, key := range e.indexKeys {
		if idx, ok := mc.indexes[name]; ok {
			// Only remove if this id still owns the key (defensive).
			if curID, ok := idx[key]; ok && curID == id {
				delete(idx, key)
			}
		}
	}
	delete(mc.items, id)
}

func (mc *MemoryMultiCache[V]) janitor(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			mc.evictExpired()
		case <-mc.stopCh:
			return
		}
	}
}

func (mc *MemoryMultiCache[V]) evictExpired() {
	now := time.Now()
	mc.mu.Lock()
	defer mc.mu.Unlock()

	for id, e := range mc.items {
		if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
			mc.evictByIDLocked(id)
		}
	}
	for _, nf := range mc.notFound {
		for k, exp := range nf {
			if !exp.IsZero() && now.After(exp) {
				delete(nf, k)
			}
		}
	}
}
