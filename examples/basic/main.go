package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/troybleiben/go-cache"
)

type User struct {
	ID   string
	Name string
}

var errUserNotFound = errors.New("user not found")

// fakeDB simulates a slow database. We use it to demonstrate how the
// negative cache prevents repeated lookups for known-missing keys.
func fakeDB(id string) (*User, error) {
	time.Sleep(50 * time.Millisecond) // pretend this is slow
	if id == "u1" {
		return &User{ID: "u1", Name: "Alice"}, nil
	}
	return nil, errUserNotFound
}

func main() {
	users := cache.NewMemory[string, *User](
		10*time.Minute, // positive TTL
		1*time.Minute,  // negative TTL: re-check missing keys every minute
	)
	defer users.Close()

	get := func(id string) (*User, error) {
		switch v, st := users.Lookup(id); st {
		case cache.StatusHit:
			return v, nil
		case cache.StatusNotFound:
			return nil, errUserNotFound
		}
		// Miss — go to DB.
		u, err := fakeDB(id)
		if errors.Is(err, errUserNotFound) {
			users.MarkNotFound(id)
			return nil, err
		}
		if err != nil {
			return nil, err
		}
		users.Set(id, u)
		return u, nil
	}

	for _, id := range []string{"u1", "u1", "ghost", "ghost", "ghost"} {
		start := time.Now()
		u, err := get(id)
		fmt.Printf("get(%q) -> %v, err=%v, took=%v\n", id, u, err, time.Since(start))
	}
}
