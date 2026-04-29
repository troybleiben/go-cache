package main

import (
	"fmt"
	"time"

	"github.com/troybleiben/go-cache"
)

type Product struct {
	ID    string // primary key
	SKU   string // alternate unique key
	Name  string
	Price float64
}

func main() {
	products := cache.NewMemoryMultiCache[*Product](
		10*time.Minute,
		1*time.Minute,
		map[string]cache.IndexFunc[*Product]{
			"id":  func(p *Product) (any, bool) { return p.ID, p.ID != "" },
			"sku": func(p *Product) (any, bool) { return p.SKU, p.SKU != "" },
		},
	)
	defer products.Close()

	products.Set(&Product{ID: "p1", SKU: "SKU-001", Name: "Widget", Price: 9.99})
	products.Set(&Product{ID: "p2", SKU: "SKU-002", Name: "Gadget", Price: 19.99})

	if p, ok := products.Get("id", "p1"); ok {
		fmt.Printf("by id  -> %+v\n", p)
	}
	if p, ok := products.Get("sku", "SKU-002"); ok {
		fmt.Printf("by sku -> %+v\n", p)
	}

	// Update p1 with a new SKU. The old SKU is automatically removed.
	products.Set(&Product{ID: "p1", SKU: "SKU-001-V2", Name: "Widget v2", Price: 12.99})

	if _, ok := products.Get("sku", "SKU-001"); !ok {
		fmt.Println("old SKU is gone after update — good")
	}
	if p, ok := products.Get("sku", "SKU-001-V2"); ok {
		fmt.Printf("new SKU resolves -> %+v\n", p)
	}

	// Negative caching per index.
	_ = products.MarkNotFound("sku", "SKU-DOES-NOT-EXIST")
	if _, st, _ := products.Lookup("sku", "SKU-DOES-NOT-EXIST"); st == cache.StatusNotFound {
		fmt.Println("SKU-DOES-NOT-EXIST -> NotFound (DB will be skipped)")
	}
}
