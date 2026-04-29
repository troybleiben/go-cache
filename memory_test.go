package cache

import (
	"sync"
	"testing"
	"time"
)

func TestMemory_SetGet(t *testing.T) {
	c := NewMemory[string, int](0, 0)
	defer c.Close()

	c.Set("a", 1)
	v, ok := c.Get("a")
	if !ok || v != 1 {
		t.Fatalf("got (%v,%v), want (1,true)", v, ok)
	}
}

func TestMemory_LookupStates(t *testing.T) {
	c := NewMemory[string, int](0, 0)
	defer c.Close()

	if _, st := c.Lookup("missing"); st != StatusMiss {
		t.Fatalf("want Miss, got %v", st)
	}

	c.Set("hit", 42)
	if v, st := c.Lookup("hit"); st != StatusHit || v != 42 {
		t.Fatalf("want Hit/42, got %v/%v", st, v)
	}

	c.MarkNotFound("nf")
	if _, st := c.Lookup("nf"); st != StatusNotFound {
		t.Fatalf("want NotFound, got %v", st)
	}
}

func TestMemory_SetClearsNotFound(t *testing.T) {
	c := NewMemory[string, int](0, 0)
	defer c.Close()

	c.MarkNotFound("k")
	c.Set("k", 7)

	if v, st := c.Lookup("k"); st != StatusHit || v != 7 {
		t.Fatalf("want Hit/7 after Set, got %v/%v", st, v)
	}
	if c.NotFoundLen() != 0 {
		t.Fatalf("not-found should be cleared, got len=%d", c.NotFoundLen())
	}
}

func TestMemory_MarkNotFoundClearsItem(t *testing.T) {
	c := NewMemory[string, int](0, 0)
	defer c.Close()

	c.Set("k", 7)
	c.MarkNotFound("k")

	if _, st := c.Lookup("k"); st != StatusNotFound {
		t.Fatalf("want NotFound after MarkNotFound, got %v", st)
	}
	if c.Len() != 0 {
		t.Fatalf("item should be cleared, got len=%d", c.Len())
	}
}

func TestMemory_Expiry(t *testing.T) {
	c := NewMemory[string, int](20*time.Millisecond, 20*time.Millisecond)
	defer c.Close()

	c.Set("k", 1)
	c.MarkNotFound("nf")

	if _, st := c.Lookup("k"); st != StatusHit {
		t.Fatalf("want Hit before expiry, got %v", st)
	}

	time.Sleep(80 * time.Millisecond)

	if _, st := c.Lookup("k"); st != StatusMiss {
		t.Fatalf("want Miss after expiry, got %v", st)
	}
	if _, st := c.Lookup("nf"); st != StatusMiss {
		t.Fatalf("want Miss after negTTL expiry, got %v", st)
	}
}

func TestMemory_SetWithTTLOverride(t *testing.T) {
	c := NewMemory[string, int](time.Hour, 0)
	defer c.Close()

	c.SetWithTTL("k", 1, 20*time.Millisecond)
	time.Sleep(80 * time.Millisecond)

	if _, st := c.Lookup("k"); st != StatusMiss {
		t.Fatalf("want Miss after per-key ttl, got %v", st)
	}
}

func TestMemory_KeysValuesItems(t *testing.T) {
	c := NewMemory[string, int](0, 0)
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.MarkNotFound("c") // must not appear in positive listings

	if got := len(c.Keys()); got != 2 {
		t.Fatalf("Keys len: want 2, got %d", got)
	}
	if got := len(c.Values()); got != 2 {
		t.Fatalf("Values len: want 2, got %d", got)
	}
	items := c.Items()
	if items["a"] != 1 || items["b"] != 2 || len(items) != 2 {
		t.Fatalf("Items mismatch: %v", items)
	}

	nf := c.NotFoundKeys()
	if len(nf) != 1 || nf[0] != "c" {
		t.Fatalf("NotFoundKeys: %v", nf)
	}
}

func TestMemory_Delete(t *testing.T) {
	c := NewMemory[string, int](0, 0)
	defer c.Close()

	c.Set("a", 1)
	c.MarkNotFound("b")
	c.Delete("a")
	c.Delete("b")

	if _, st := c.Lookup("a"); st != StatusMiss {
		t.Fatalf("a should be Miss")
	}
	if _, st := c.Lookup("b"); st != StatusMiss {
		t.Fatalf("b should be Miss")
	}
}

func TestMemory_ClearAndClearNotFound(t *testing.T) {
	c := NewMemory[string, int](0, 0)
	defer c.Close()

	c.Set("a", 1)
	c.MarkNotFound("b")

	c.ClearNotFound()
	if c.Len() != 1 || c.NotFoundLen() != 0 {
		t.Fatalf("after ClearNotFound: items=%d nf=%d", c.Len(), c.NotFoundLen())
	}

	c.Clear()
	if c.Len() != 0 || c.NotFoundLen() != 0 {
		t.Fatalf("after Clear: items=%d nf=%d", c.Len(), c.NotFoundLen())
	}
}

func TestMemory_PointerValueSharing(t *testing.T) {
	type user struct{ Name string }
	c := NewMemory[string, *user](0, 0)
	defer c.Close()

	u := &user{Name: "A"}
	c.Set("k", u)

	got, _ := c.Get("k")
	got.Name = "B"

	if u.Name != "B" {
		t.Fatalf("pointer value should be shared, got %s", u.Name)
	}
}

func TestMemory_Concurrent(t *testing.T) {
	c := NewMemory[int, int](0, 0)
	defer c.Close()

	var wg sync.WaitGroup
	const workers = 16
	const ops = 1000

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				k := (id*ops + i) % 256
				switch i % 4 {
				case 0:
					c.Set(k, i)
				case 1:
					c.MarkNotFound(k)
				case 2:
					_, _ = c.Lookup(k)
				case 3:
					c.Delete(k)
				}
			}
		}(w)
	}
	wg.Wait()
	// Run with: go test -race
}

func TestMemory_CloseIdempotent(t *testing.T) {
	c := NewMemory[string, int](time.Millisecond, time.Millisecond)
	c.Close()
	c.Close() // must not panic
}
