package cache

import (
	"sync"
	"testing"
	"time"
)

type user struct {
	ID   string
	Code string
	Name string
}

func userExtractors() map[string]IndexFunc[*user] {
	return map[string]IndexFunc[*user]{
		"id":   func(u *user) (any, bool) { return u.ID, u.ID != "" },
		"code": func(u *user) (any, bool) { return u.Code, u.Code != "" },
	}
}

func TestMulti_SetLookupBothIndexes(t *testing.T) {
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	u := &user{ID: "u1", Code: "C-1", Name: "Alice"}
	mc.Set(u)

	v, st, err := mc.Lookup("id", "u1")
	if err != nil || st != StatusHit || v != u {
		t.Fatalf("by id: got (%v,%v,%v)", v, st, err)
	}
	v, st, err = mc.Lookup("code", "C-1")
	if err != nil || st != StatusHit || v != u {
		t.Fatalf("by code: got (%v,%v,%v)", v, st, err)
	}
}

func TestMulti_GetConvenience(t *testing.T) {
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	mc.Set(&user{ID: "u1", Code: "C-1"})

	if v, ok := mc.Get("id", "u1"); !ok || v.Code != "C-1" {
		t.Fatalf("Get hit failed: %+v ok=%v", v, ok)
	}
	if _, ok := mc.Get("id", "missing"); ok {
		t.Fatalf("Get on missing should be false")
	}
	if _, ok := mc.Get("nonexistent", "x"); ok {
		t.Fatalf("Get with unknown index should be false")
	}
}

func TestMulti_UpdateChangesIndexKey(t *testing.T) {
	// Replacing a value with one that has a different index key on
	// some index must remove the old index entry there.
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	mc.Set(&user{ID: "u1", Code: "C-1", Name: "old"})
	mc.Set(&user{ID: "u1", Code: "C-2", Name: "new"})

	// Old code must no longer resolve.
	if _, st, _ := mc.Lookup("code", "C-1"); st != StatusMiss {
		t.Fatalf("old code should be Miss, got %v", st)
	}
	// New code resolves.
	if v, st, _ := mc.Lookup("code", "C-2"); st != StatusHit || v.Name != "new" {
		t.Fatalf("new code should be Hit/new, got %v/%v", st, v)
	}
	// ID still resolves to the new value.
	if v, st, _ := mc.Lookup("id", "u1"); st != StatusHit || v.Name != "new" {
		t.Fatalf("id should resolve to new, got %v/%v", st, v)
	}
	// And we have exactly one item.
	if mc.Len() != 1 {
		t.Fatalf("expected 1 item, got %d", mc.Len())
	}
}

func TestMulti_OptionalIndex(t *testing.T) {
	// A value with an empty optional field is stored but not registered
	// under the index it skipped.
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	mc.Set(&user{ID: "u1", Code: "", Name: "Alice"})

	if _, st, _ := mc.Lookup("id", "u1"); st != StatusHit {
		t.Fatalf("id lookup should hit")
	}
	if _, st, _ := mc.Lookup("code", ""); st == StatusHit {
		t.Fatalf("empty code must not be reachable")
	}
	if mc.Len() != 1 {
		t.Fatalf("expected 1 item, got %d", mc.Len())
	}
}

func TestMulti_MarkNotFoundPerIndex(t *testing.T) {
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	if err := mc.MarkNotFound("code", "C-X"); err != nil {
		t.Fatal(err)
	}
	if _, st, _ := mc.Lookup("code", "C-X"); st != StatusNotFound {
		t.Fatalf("want NotFound on code, got %v", st)
	}
	// Different index must not be affected.
	if _, st, _ := mc.Lookup("id", "C-X"); st != StatusMiss {
		t.Fatalf("want Miss on id, got %v", st)
	}
}

func TestMulti_SetClearsNotFoundOnTouchedIndexes(t *testing.T) {
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	_ = mc.MarkNotFound("id", "u1")
	_ = mc.MarkNotFound("code", "C-1")

	mc.Set(&user{ID: "u1", Code: "C-1"})

	for _, idx := range []string{"id", "code"} {
		n, _ := mc.NotFoundLen(idx)
		if n != 0 {
			t.Fatalf("not-found on %s should be cleared, got %d", idx, n)
		}
	}
}

func TestMulti_MarkNotFoundEvictsExistingItem(t *testing.T) {
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	mc.Set(&user{ID: "u1", Code: "C-1"})
	if err := mc.MarkNotFound("id", "u1"); err != nil {
		t.Fatal(err)
	}

	// Both indexes should be gone for that value.
	if _, st, _ := mc.Lookup("id", "u1"); st != StatusNotFound {
		t.Fatalf("id should be NotFound, got %v", st)
	}
	if _, st, _ := mc.Lookup("code", "C-1"); st != StatusMiss {
		t.Fatalf("code should be Miss after sibling eviction, got %v", st)
	}
}

func TestMulti_DeleteRemovesAcrossIndexes(t *testing.T) {
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	mc.Set(&user{ID: "u1", Code: "C-1"})

	removed, err := mc.Delete("id", "u1")
	if err != nil || !removed {
		t.Fatalf("Delete: removed=%v err=%v", removed, err)
	}
	if _, st, _ := mc.Lookup("code", "C-1"); st != StatusMiss {
		t.Fatalf("code should be Miss after Delete via id, got %v", st)
	}
}

func TestMulti_DeleteValue(t *testing.T) {
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	u := &user{ID: "u1", Code: "C-1"}
	mc.Set(u)

	if !mc.DeleteValue(u) {
		t.Fatalf("DeleteValue should report removal")
	}
	if mc.Len() != 0 {
		t.Fatalf("expected 0 items, got %d", mc.Len())
	}
}

func TestMulti_UnknownIndex(t *testing.T) {
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	if _, _, err := mc.Lookup("nope", "x"); err != ErrUnknownIndex {
		t.Fatalf("Lookup: want ErrUnknownIndex, got %v", err)
	}
	if err := mc.MarkNotFound("nope", "x"); err != ErrUnknownIndex {
		t.Fatalf("MarkNotFound: want ErrUnknownIndex, got %v", err)
	}
	if _, err := mc.Delete("nope", "x"); err != ErrUnknownIndex {
		t.Fatalf("Delete: want ErrUnknownIndex, got %v", err)
	}
}

func TestMulti_Expiry(t *testing.T) {
	mc := NewMemoryMultiCache[*user](20*time.Millisecond, 20*time.Millisecond, userExtractors())
	defer mc.Close()

	mc.Set(&user{ID: "u1", Code: "C-1"})
	_ = mc.MarkNotFound("id", "u2")

	time.Sleep(80 * time.Millisecond)

	if _, st, _ := mc.Lookup("id", "u1"); st != StatusMiss {
		t.Fatalf("u1 id should expire, got %v", st)
	}
	if _, st, _ := mc.Lookup("code", "C-1"); st != StatusMiss {
		t.Fatalf("u1 code should expire (sibling), got %v", st)
	}
	if _, st, _ := mc.Lookup("id", "u2"); st != StatusMiss {
		t.Fatalf("u2 negative should expire, got %v", st)
	}
}

func TestMulti_ClearNotFound(t *testing.T) {
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	_ = mc.MarkNotFound("id", "x")
	_ = mc.MarkNotFound("code", "y")

	if err := mc.ClearNotFound("id"); err != nil {
		t.Fatal(err)
	}
	if n, _ := mc.NotFoundLen("id"); n != 0 {
		t.Fatalf("id nf should be 0, got %d", n)
	}
	if n, _ := mc.NotFoundLen("code"); n != 1 {
		t.Fatalf("code nf should remain 1, got %d", n)
	}

	if err := mc.ClearNotFound(""); err != nil {
		t.Fatal(err)
	}
	if n, _ := mc.NotFoundLen("code"); n != 0 {
		t.Fatalf("code nf should be 0 after wildcard clear, got %d", n)
	}
}

func TestMulti_KeysAndValues(t *testing.T) {
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	mc.Set(&user{ID: "u1", Code: "C-1"})
	mc.Set(&user{ID: "u2", Code: "C-2"})

	idKeys, err := mc.Keys("id")
	if err != nil || len(idKeys) != 2 {
		t.Fatalf("id keys: %v err=%v", idKeys, err)
	}
	if vals := mc.Values(); len(vals) != 2 {
		t.Fatalf("values len: want 2, got %d", len(vals))
	}
}

func TestMulti_NoExtractorsPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic with empty extractors")
		}
	}()
	_ = NewMemoryMultiCache[*user](0, 0, nil)
}

func TestMulti_Concurrent(t *testing.T) {
	mc := NewMemoryMultiCache[*user](0, 0, userExtractors())
	defer mc.Close()

	var wg sync.WaitGroup
	const workers = 16
	const ops = 500

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				n := (id*ops + i) % 64
				u := &user{
					ID:   itoa(n),
					Code: "C-" + itoa(n),
				}
				switch i % 5 {
				case 0:
					mc.Set(u)
				case 1:
					_, _, _ = mc.Lookup("id", u.ID)
				case 2:
					_, _, _ = mc.Lookup("code", u.Code)
				case 3:
					_, _ = mc.Delete("id", u.ID)
				case 4:
					_ = mc.MarkNotFound("code", u.Code)
				}
			}
		}(w)
	}
	wg.Wait()
	// Run with: go test -race
}

// itoa avoids strconv import noise for the tiny test usage.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
