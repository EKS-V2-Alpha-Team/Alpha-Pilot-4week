package main

// Integration tests for reserve/release idempotency (ALPHA-51). They need a real,
// DISPOSABLE Postgres:
//
//	TEST_DATABASE_URL=postgres://app:localdev@localhost:5432/inventory_test?sslmode=disable go test ./...
//
// WARNING: every test TRUNCATES products and reservations in that database.
// Without TEST_DATABASE_URL the tests are skipped.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database integration test")
	}
	var err error
	db, err = sql.Open("postgres", url)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() }) // each test opens its own pool; close it or connections pile up
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	warmPool(t, 20)
	migrate()
	if _, err := db.Exec(`TRUNCATE reservations, products RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO products (id, name, sku, price, stock) VALUES ('p1', 'Widget', 'W-1', 10, 5)`); err != nil {
		t.Fatalf("seed product: %v", err)
	}
}

// warmPool opens n connections up front. Without it the first goroutine finishes on the
// only open connection while the others are still connecting, and the race never happens.
func warmPool(t *testing.T, n int) {
	t.Helper()
	db.SetMaxIdleConns(n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db.Exec(`SELECT pg_sleep(0.05)`)
		}()
	}
	wg.Wait()
}

func post(t *testing.T, h http.HandlerFunc, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b)))
	return rec
}

func reserveBody(orderID, qty int) map[string]interface{} {
	return map[string]interface{}{
		"order_id": orderID,
		"items":    []map[string]interface{}{{"product_id": "p1", "quantity": qty}},
	}
}

func reservedCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT reserved FROM products WHERE id = 'p1'`).Scan(&n); err != nil {
		t.Fatalf("read reserved: %v", err)
	}
	return n
}

func activeReservations(t *testing.T, orderID int) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM reservations WHERE order_id = $1 AND status = 'active'`, orderID).Scan(&n); err != nil {
		t.Fatalf("count reservations: %v", err)
	}
	return n
}

func TestReserveTwiceHoldsStockOnce(t *testing.T) {
	setupTestDB(t)
	if rec := post(t, handleReserve, reserveBody(1, 2)); rec.Code != http.StatusCreated {
		t.Fatalf("first reserve: got %d, want 201: %s", rec.Code, rec.Body)
	}
	if rec := post(t, handleReserve, reserveBody(1, 2)); rec.Code != http.StatusOK {
		t.Fatalf("repeat reserve: got %d, want 200: %s", rec.Code, rec.Body)
	}
	if got := reservedCount(t); got != 2 {
		t.Errorf("reserved = %d, want 2 (held once)", got)
	}
	if got := activeReservations(t, 1); got != 1 {
		t.Errorf("active reservations = %d, want 1", got)
	}
}

func TestConcurrentReservesForSameOrderHoldStockOnce(t *testing.T) {
	setupTestDB(t)
	var wg sync.WaitGroup
	codes := make([]int, 10)
	start := make(chan struct{})
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = post(t, handleReserve, reserveBody(1, 1)).Code
		}(i)
	}
	close(start)
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusCreated && c != http.StatusOK {
			t.Errorf("request %d: got %d, want 201 or 200", i, c)
		}
	}
	if got := reservedCount(t); got != 1 {
		t.Errorf("reserved = %d, want 1 (held once)", got)
	}
}

func TestReleaseTwiceDecrementsOnce(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 3))
	post(t, handleRelease, map[string]int{"order_id": 1})
	rec := post(t, handleRelease, map[string]int{"order_id": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat release: got %d, want 200", rec.Code)
	}
	var resp struct {
		Released int `json:"released"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Released != 0 {
		t.Errorf("repeat release released %d reservations, want 0", resp.Released)
	}
	if got := reservedCount(t); got != 0 {
		t.Errorf("reserved = %d, want 0", got)
	}
}

func TestConcurrentReleasesDecrementOnce(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 3))
	// Release the same order from 20 goroutines at once (a start gate lines them up),
	// like SQS delivering the same cancellation to several workers.
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			post(t, handleRelease, map[string]int{"order_id": 1})
		}()
	}
	close(start)
	wg.Wait()
	if got := reservedCount(t); got != 0 {
		t.Errorf("reserved = %d, want 0 (old code went negative)", got)
	}
}

func TestReserveAgainAfterReleaseWorks(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 2))
	post(t, handleRelease, map[string]int{"order_id": 1})
	if rec := post(t, handleReserve, reserveBody(1, 2)); rec.Code != http.StatusCreated {
		t.Fatalf("reserve after release: got %d, want 201: %s", rec.Code, rec.Body)
	}
	if got := reservedCount(t); got != 2 {
		t.Errorf("reserved = %d, want 2", got)
	}
}
