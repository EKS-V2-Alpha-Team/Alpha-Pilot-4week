package main

import "testing"

// Seeding runs on every start of every replica, so it must be safe to repeat and must
// not undo stock changes made since the first run.
func TestSeedDemoProductsIsSafeToRepeat(t *testing.T) {
	setupTestDB(t)

	seedDemoProducts()
	if _, err := db.Exec(`UPDATE products SET stock = 7 WHERE id = 'kb-001'`); err != nil {
		t.Fatalf("change stock: %v", err)
	}
	seedDemoProducts()

	var products, stock int
	if err := db.QueryRow(`SELECT COUNT(*) FROM products WHERE id <> 'p1'`).Scan(&products); err != nil {
		t.Fatalf("count: %v", err)
	}
	if products != 4 {
		t.Fatalf("want 4 demo products after seeding twice, got %d", products)
	}
	if err := db.QueryRow(`SELECT stock FROM products WHERE id = 'kb-001'`).Scan(&stock); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	if stock != 7 {
		t.Fatalf("second seed reset stock: got %d, want 7", stock)
	}
}
