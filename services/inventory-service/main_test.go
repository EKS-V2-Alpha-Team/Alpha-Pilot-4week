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
	"time"
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

func stockCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT stock FROM products WHERE id = 'p1'`).Scan(&n); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return n
}

func TestCommitTurnsHoldIntoSale(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 2))
	if rec := post(t, handleCommit, map[string]int{"order_id": 1}); rec.Code != http.StatusOK {
		t.Fatalf("commit: got %d, want 200: %s", rec.Code, rec.Body)
	}
	if stock, reserved := stockCount(t), reservedCount(t); stock != 3 || reserved != 0 {
		t.Errorf("stock=%d reserved=%d, want stock=3 reserved=0", stock, reserved)
	}
	var status string
	db.QueryRow(`SELECT status FROM reservations WHERE order_id = 1`).Scan(&status)
	if status != "committed" {
		t.Errorf("reservation status = %q, want committed", status)
	}
}

func TestCommitTwiceTakesStockOnce(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 2))
	post(t, handleCommit, map[string]int{"order_id": 1})
	rec := post(t, handleCommit, map[string]int{"order_id": 1})
	var resp struct {
		Committed int `json:"committed"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if rec.Code != http.StatusOK || resp.Committed != 0 {
		t.Errorf("repeat commit: got %d committed=%d, want 200 committed=0", rec.Code, resp.Committed)
	}
	if stock := stockCount(t); stock != 3 {
		t.Errorf("stock = %d, want 3", stock)
	}
}

func TestConcurrentCommitsTakeStockOnce(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 2))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			post(t, handleCommit, map[string]int{"order_id": 1})
		}()
	}
	close(start)
	wg.Wait()
	if stock, reserved := stockCount(t), reservedCount(t); stock != 3 || reserved != 0 {
		t.Errorf("stock=%d reserved=%d, want stock=3 reserved=0", stock, reserved)
	}
}

func TestReserveAfterCommitHoldsNothingMore(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 2))
	post(t, handleCommit, map[string]int{"order_id": 1})
	// The same order.created delivered again after the sale.
	if rec := post(t, handleReserve, reserveBody(1, 2)); rec.Code != http.StatusOK {
		t.Fatalf("reserve after commit: got %d, want 200", rec.Code)
	}
	if stock, reserved := stockCount(t), reservedCount(t); stock != 3 || reserved != 0 {
		t.Errorf("stock=%d reserved=%d, want stock=3 reserved=0", stock, reserved)
	}
}

// A hold lasts 15 minutes by the DATABASE's clock, whatever time zone the service runs in.
// Before, the Go clock's wall-clock reading was stored, so a service in UTC-8 wrote holds that
// were already 8 hours expired and the scheduler gave the stock straight back.
func TestHoldLengthDoesNotDependOnTheServiceTimeZone(t *testing.T) {
	setupTestDB(t)
	old := time.Local
	t.Cleanup(func() { time.Local = old })

	for _, zone := range []*time.Location{time.FixedZone("LA", -8*3600), time.FixedZone("AKL", 13*3600), time.UTC} {
		time.Local = zone
		if _, err := db.Exec(`TRUNCATE reservations RESTART IDENTITY`); err != nil {
			t.Fatal(err)
		}
		db.Exec(`UPDATE products SET reserved = 0`)
		if rec := post(t, handleReserve, reserveBody(1, 1)); rec.Code != http.StatusCreated {
			t.Fatalf("%s: reserve got %d", zone, rec.Code)
		}
		var minutes float64
		if err := db.QueryRow(`SELECT EXTRACT(EPOCH FROM (expires_at - NOW())) / 60 FROM reservations WHERE order_id = 1`).Scan(&minutes); err != nil {
			t.Fatal(err)
		}
		if minutes < 14 || minutes > 15.1 {
			t.Errorf("service in %s: hold has %.1f minutes left, want about 15", zone, minutes)
		}
	}
}

// The hold timed out (the scheduler expired it) before the order was committed. Saying
// "committed" would let a paid order finish without the stock leaving the shelf.
func TestCommitOfAnExpiredHoldIsRefused(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 2))
	db.Exec(`UPDATE reservations SET status = 'expired' WHERE order_id = 1`)
	db.Exec(`UPDATE products SET reserved = 0`)

	rec := post(t, handleCommit, map[string]int{"order_id": 1})
	if rec.Code != http.StatusConflict {
		t.Fatalf("commit of expired hold: got %d, want 409", rec.Code)
	}
	if stock := stockCount(t); stock != 5 {
		t.Errorf("stock = %d, want 5 (nothing sold)", stock)
	}
}

func TestCommitOfAnOrderWithNoHoldIsRefused(t *testing.T) {
	setupTestDB(t)
	if rec := post(t, handleCommit, map[string]int{"order_id": 99}); rec.Code != http.StatusConflict {
		t.Fatalf("commit with no hold: got %d, want 409", rec.Code)
	}
}

// A hold that was released (the order was cancelled) is not a sale either.
func TestCommitOfAReleasedHoldIsRefused(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 2))
	post(t, handleRelease, map[string]int{"order_id": 1})
	if rec := post(t, handleCommit, map[string]int{"order_id": 1}); rec.Code != http.StatusConflict {
		t.Fatalf("commit after release: got %d, want 409", rec.Code)
	}
	if stock := stockCount(t); stock != 5 {
		t.Errorf("stock = %d, want 5", stock)
	}
}

// Many commits at once for one order: one takes the stock, the others are repeats (200),
// never a 409 for an order that really was committed.
func TestConcurrentCommitsAreAllSuccessfulRepeats(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 2))
	codes := make([]int, 20)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = post(t, handleCommit, map[string]int{"order_id": 1}).Code
		}(i)
	}
	close(start)
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("commit %d got %d, want 200", i, c)
		}
	}
	if stock := stockCount(t); stock != 3 {
		t.Errorf("stock = %d, want 3", stock)
	}
}
