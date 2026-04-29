// Package cache provides generic, thread-safe in-memory caches with first-class
// support for negative (not-found) entries and per-key TTLs.
//
// Two cache types are exposed:
//
//   - Memory[K, V] is a single-key cache that maps a comparable key K to a
//     value V. It keeps positive (value) and negative (not-found) entries in
//     separate tables, so callers can distinguish "we never asked" from
//     "we asked and the source had nothing".
//
//   - MemoryMultiCache[V] stores a single value V that can be looked up via
//     multiple independent indexes (for example, by ID and by Code). Indexes
//     are kept consistent automatically: updating a value rewrites all index
//     entries it touches, deleting via one index removes the value from every
//     index it occupies, and each index has its own negative table.
//
// Both caches run an optional background janitor that removes expired entries.
// Call Close when done to stop it.
//
// # Lookup states
//
// Lookups return one of three states:
//
//	StatusHit       value is cached and valid; use it
//	StatusNotFound  source was checked previously and had no such key
//	StatusMiss      cache has no information; query the source
//
// The classic two-value Get method collapses Miss and NotFound to (zero, false).
// Use Lookup if you need to tell them apart, e.g. to skip a database round-trip.
//
// # Value semantics
//
// V is stored as-is. Pointer types are shared; struct values are copied at Set
// and at Get time. Concurrent mutation of pointer values is the caller's
// responsibility — the cache's mutex protects only its internal maps.
package cache
