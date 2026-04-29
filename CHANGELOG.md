# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-04-29

### Added
- `Memory[K, V]` — generic, thread-safe in-memory cache with separate
  positive and negative entry tables and per-key TTL support.
- `MemoryMultiCache[V]` — value cache lookable through multiple
  independent indexes, with automatic cross-index consistency on
  `Set`, `Delete`, and expiry.
- Three-state `Lookup` API distinguishing `StatusHit`, `StatusNotFound`,
  and `StatusMiss`, plus the classic two-value `Get` shortcut.
- Per-cache and per-call TTL overrides for both positive and negative
  entries.
- Background janitor goroutine for periodic eviction.

[Unreleased]: https://github.com/troybleiben/go-cache/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/troybleiben/go-cache/releases/tag/v0.1.0
