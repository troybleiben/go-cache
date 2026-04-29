# go-cache

[![CI](https://github.com/troybleiben/go-cache/actions/workflows/ci.yml/badge.svg)](https://github.com/troybleiben/go-cache/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/troybleiben/go-cache.svg)](https://pkg.go.dev/github.com/troybleiben/go-cache)
[![Go Report Card](https://goreportcard.com/badge/github.com/troybleiben/go-cache)](https://goreportcard.com/report/github.com/troybleiben/go-cache)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Generic, thread-safe in-memory caches for Go with first-class support for
**negative caching** (remembering what's *not* in your source) and
**multi-index lookups** (one value, many keys).

```go
go get github.com/troybleiben/go-cache
```

Requires Go 1.21+.

## Why

Most cache libraries answer "is the value here?" with yes or no. That collapses
two very different states:

- **Miss** — we have never asked the source about this key.
- **Not found** — we asked, the source had nothing, and asking again right now
  is wasted work.

`go-cache` exposes both, so you can skip database round-trips for keys you
already know don't exist. It also handles the common case where one record has
multiple unique identifiers (an internal ID *and* an external code, for
example) without forcing you to maintain parallel caches by hand.

## Features

- **Generic.** `Memory[K, V]` and `MemoryMultiCache[V]` work with any types.
- **Negative caching.** `MarkNotFound` records confirmed-missing keys with a
  separate TTL. Re-checks happen at your chosen cadence, not on every request.
- **Three-state lookup.** `StatusHit` / `StatusNotFound` / `StatusMiss`.
- **Multi-index.** Look up the same value by ID, code, email, whatever.
  Updates and deletes stay consistent across all indexes automatically.
- **Per-key TTL overrides.** `SetWithTTL` / `MarkNotFoundWithTTL` for the rare
  entry that needs different timing than the default.
- **Background janitor.** Periodic eviction of expired entries, plus lazy
  cleanup on read.
- **Thread-safe.** All operations are safe for concurrent use; the test suite
  runs under `-race`.
- **Zero dependencies.** Standard library only.

## Quick start

### Single-key cache

```go
package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/troybleiben/go-cache"
)

type User struct{ ID, Name string }

var errNotFound = errors.New("not found")

func main() {
	users := cache.NewMemory[string, *User](
		10*time.Minute, // positive TTL
		1*time.Minute,  // negative TTL
	)
	defer users.Close()

	get := func(id string) (*User, error) {
		switch v, st := users.Lookup(id); st {
		case cache.StatusHit:
			return v, nil
		case cache.StatusNotFound:
			return nil, errNotFound
		}
		u, err := loadFromDB(id) // your DB call
		if errors.Is(err, errNotFound) {
			users.MarkNotFound(id)
			return nil, err
		}
		if err != nil {
			return nil, err
		}
		users.Set(id, u)
		return u, nil
	}

	u, err := get("u1")
	fmt.Println(u, err)
}
```

### Multi-index cache

When the same record has multiple unique identifiers — for example a UUID and
a human-readable code — register one extractor per index:

```go
type Product struct {
ID   string
SKU  string
Name string
}

products := cache.NewMemoryMultiCache[*Product](
10*time.Minute,
1*time.Minute,
map[string]cache.IndexFunc[*Product]{
"id":  func(p *Product) (any, bool) { return p.ID, p.ID != "" },
"sku": func(p *Product) (any, bool) { return p.SKU, p.SKU != "" },
},
)
defer products.Close()

products.Set(&Product{ID: "p1", SKU: "SKU-001", Name: "Widget"})

p, _      := products.Get("id", "p1")
p, _       = products.Get("sku", "SKU-001")

// Update p1 with a new SKU. The old SKU entry is removed automatically.
products.Set(&Product{ID: "p1", SKU: "SKU-001-V2", Name: "Widget v2"})

_, ok := products.Get("sku", "SKU-001") // ok == false
```

## API overview

### `Memory[K, V]`

| Method | Purpose |
|---|---|
| `NewMemory[K, V](ttl, negTTL)` | Construct a new cache. |
| `Set(k, v)` / `SetWithTTL(k, v, ttl)` | Store a value. |
| `MarkNotFound(k)` / `MarkNotFoundWithTTL(k, ttl)` | Record a confirmed-missing key. |
| `Lookup(k) (V, Status)` | Three-state lookup. |
| `Get(k) (V, bool)` | Two-state convenience form. |
| `Delete(k)` | Remove from both positive and negative tables. |
| `Keys()` / `Values()` / `Items()` | Snapshot of valid positive entries. |
| `NotFoundKeys()` | Snapshot of valid negative entries. |
| `Len()` / `NotFoundLen()` | Counts. |
| `Clear()` / `ClearNotFound()` | Bulk removal. |
| `Close()` | Stop the janitor. |

### `MemoryMultiCache[V]`

| Method | Purpose |
|---|---|
| `NewMemoryMultiCache[V](ttl, negTTL, extractors)` | Construct with one or more named index extractors. |
| `Set(v)` / `SetWithTTL(v, ttl)` | Store under every index whose extractor returns `ok=true`. |
| `MarkNotFound(idx, key)` / `MarkNotFoundWithTTL(...)` | Negative entry on a specific index. |
| `Lookup(idx, key) (V, Status, error)` | Three-state lookup; `ErrUnknownIndex` if `idx` isn't registered. |
| `Get(idx, key) (V, bool)` | Convenience form. |
| `Delete(idx, key)` | Remove the value from every index it occupies. |
| `DeleteValue(v)` | Same, but you supply the value directly. |
| `Values()` / `Keys(idx)` | Snapshots. |
| `IndexNames()` | List registered indexes. |
| `Len()` / `NotFoundLen(idx)` | Counts. |
| `Clear()` / `ClearNotFound(idx)` | Bulk removal; pass `""` to clear negatives across all indexes. |
| `Close()` | Stop the janitor. |

## Behavior notes

- **TTL of 0 means no expiry.** If both TTLs are 0, no janitor goroutine runs;
  cleanup happens lazily on read.
- **Setting clears matching not-found.** A `Set` removes any negative entry on
  the same key, so freshly-inserted source data becomes visible immediately.
- **`MarkNotFound` removes a positive entry.** Mutually exclusive states.
- **Multi-index updates are atomic.** Replacing a value with one that has a
  different key on some index drops the old key — no stale reads.
- **Negative entries are per-index** in `MemoryMultiCache`. Marking a SKU as
  not-found does not affect lookups by ID.
- **Pointer values are shared, not copied.** Mutating a value retrieved from
  the cache mutates the cached object. The cache mutex protects only its own
  maps.

## Recommended TTLs

A good default is **negative TTL shorter than positive TTL**: stale "not found"
results are usually worse than stale values, because newly-inserted source rows
should become visible quickly. `10m` positive, `1m` negative is a reasonable
starting point; tune based on how often your source gains new rows.

## Examples

Runnable examples live in [`examples/`](examples/):

- [`examples/basic`](examples/basic) — single-key cache with negative caching
- [`examples/multiindex`](examples/multiindex) — multi-index cache

```bash
go run ./examples/basic
go run ./examples/multiindex
```

## Development

This project ships with a `Makefile` that wraps the common workflows. Run
`make help` to see all targets.

### Common tasks

```bash
make check           # tidy + fmt + vet + lint + test (run before every PR)
make test            # tests with -race
make cover           # tests with coverage; prints total
make cover-html      # open coverage.html in your browser
make bench           # run benchmarks (Benchmark* funcs)
make examples        # build and run all examples
make install-tools   # install golangci-lint
```

### Releasing

The `tag-*` targets compute the next semver tag from the latest one and push
it. The working tree must be clean. Use `DRY_RUN=1` to preview without
creating the tag.

```bash
make current-version             # show the latest tag (e.g. v0.1.0)
make tag-patch                   # v0.1.0 -> v0.1.1   (bug fixes)
make tag-minor                   # v0.1.3 -> v0.2.0   (new features, backward compatible)
make tag-major                   # v0.4.2 -> v1.0.0   (breaking changes)
make tag VERSION=v0.5.0-rc.1     # explicit version (e.g. pre-releases)
make tag-minor DRY_RUN=1         # preview without tagging
```

The Makefile validates the resulting version against semver, refuses to
overwrite existing tags, and warns if you tag from a non-default branch.
After tagging, [pkg.go.dev](https://pkg.go.dev/github.com/troybleiben/go-cache)
picks up the new version within a few minutes.

Don't forget to update [`CHANGELOG.md`](CHANGELOG.md) before tagging.

## Versioning

This project follows [Semantic Versioning](https://semver.org/). Until `v1.0.0`
the API may change between minor versions; we will note breaking changes in
[CHANGELOG.md](CHANGELOG.md).

## License

[MIT](LICENSE)
