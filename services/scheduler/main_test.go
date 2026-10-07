package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"
)

// setupTestDB connects to TEST_DATABASE_URL and gives each test an empty orders table.
// The table is the same shape order-service creates, so the tests also run on a database
// that order-service's own tests already used. Use a throwaway database: it is truncated.
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
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS orders (
		id SERIAL PRIMARY KEY,
		customer_id VARCHAR(255) NOT NULL,
		status VARCHAR(20) NOT NULL DEFAULT 'pending',
		items JSONB NOT NULL,
		total DECIMAL(12,2) NOT NULL DEFAULT 0,
		currency VARCHAR(3) NOT NULL DEFAULT 'GBP',
		notes TEXT,
		created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW()
	)`); err != nil {
		t.Fatalf("create orders: %v", err)
	}
	if _, err := db.Exec(`TRUNCATE orders RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// insertOrder adds an order of the given status that is minutesOld minutes old.
func insertOrder(t *testing.T, status string, minutesOld int) int {
	t.Helper()
	var id int
	err := db.QueryRow(
		`INSERT INTO orders (customer_id, status, items, created_at)
		 VALUES ('sara@example.com', $1, '[]', NOW() - make_interval(mins => $2)) RETURNING id`,
		status, minutesOld,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	return id
}

func statusOf(t *testing.T, id int) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT status FROM orders WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return s
}

// statusCall is one PUT /status the fake order-service received.
type statusCall struct {
	OrderID        int    `json:"order_id"`
	NewStatus      string `json:"new_status"`
	ExpectedStatus string `json:"expected_status"`
}

// fakeOrderService records every PUT /status and answers with the code (and "changed")
// chosen per order. Orders without an entry get 200 with changed true.
type fakeOrderService struct {
	mu      sync.Mutex
	calls   []statusCall
	answers map[int]int // order id -> status code
	changed map[int]bool
}

func newFakeOrderService(t *testing.T) (*fakeOrderService, string) {
	t.Helper()
	f := &fakeOrderService{answers: map[int]int{}, changed: map[int]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/status" {
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var c statusCall
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			t.Errorf("bad body: %v", err)
		}
		f.mu.Lock()
		f.calls = append(f.calls, c)
		code, ok := f.answers[c.OrderID]
		changed, hasChanged := f.changed[c.OrderID]
		f.mu.Unlock()
		if !ok {
			code = http.StatusOK
		}
		if !hasChanged {
			changed = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]interface{}{"changed": changed})
	}))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

// The scheduler picks only old pending orders, and cancels them by asking order-service
// (with the status it saw), not by writing to the table. The fake does not change the
// table, so a status that stays "pending" proves the scheduler did not write it itself.
func TestAbandonedOrderIsCancelledThroughOrderService(t *testing.T) {
	setupTestDB(t)
	old := insertOrder(t, "pending", 31)
	insertOrder(t, "pending", 1)    // too new
	insertOrder(t, "confirmed", 45) // not pending
	f, url := newFakeOrderService(t)

	n := cancelAbandonedOrders(&http.Client{Timeout: 2 * time.Second}, url, abandonedAfter)

	if n != 1 {
		t.Errorf("cancelled: got %d, want 1", n)
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls: got %d, want 1 (%v)", len(f.calls), f.calls)
	}
	want := statusCall{OrderID: old, NewStatus: "cancelled", ExpectedStatus: "pending"}
	if f.calls[0] != want {
		t.Errorf("call: got %+v, want %+v", f.calls[0], want)
	}
	if s := statusOf(t, old); s != "pending" {
		t.Errorf("scheduler wrote to the orders table itself: status %s", s)
	}
}

// 409 means the order moved on (the worker confirmed it a moment ago). That order is left
// alone and the next one is still handled.
func TestConflictLeavesThatOrderAndCarriesOn(t *testing.T) {
	setupTestDB(t)
	first := insertOrder(t, "pending", 50)
	second := insertOrder(t, "pending", 40)
	f, url := newFakeOrderService(t)
	f.answers[first] = http.StatusConflict

	n := cancelAbandonedOrders(&http.Client{Timeout: 2 * time.Second}, url, abandonedAfter)

	if n != 1 {
		t.Errorf("cancelled: got %d, want 1", n)
	}
	if len(f.calls) != 2 || f.calls[0].OrderID != first || f.calls[1].OrderID != second {
		t.Errorf("calls: got %v, want %d then %d", f.calls, first, second)
	}
}

// 200 with changed false means someone already cancelled it: not counted as ours.
func TestAlreadyCancelledIsNotCounted(t *testing.T) {
	setupTestDB(t)
	id := insertOrder(t, "pending", 40)
	f, url := newFakeOrderService(t)
	f.changed[id] = false

	if n := cancelAbandonedOrders(&http.Client{Timeout: 2 * time.Second}, url, abandonedAfter); n != 0 {
		t.Errorf("cancelled: got %d, want 0", n)
	}
}

// If order-service is failing, the run stops after the first failure instead of waiting
// on every order, and nothing is written. The next run tries again.
func TestOrderServiceErrorStopsTheRun(t *testing.T) {
	setupTestDB(t)
	first := insertOrder(t, "pending", 50)
	insertOrder(t, "pending", 40)
	f, url := newFakeOrderService(t)
	f.answers[first] = http.StatusServiceUnavailable

	n := cancelAbandonedOrders(&http.Client{Timeout: 2 * time.Second}, url, abandonedAfter)

	if n != 0 {
		t.Errorf("cancelled: got %d, want 0", n)
	}
	if len(f.calls) != 1 {
		t.Errorf("calls: got %d, want 1 (stop after the first failure)", len(f.calls))
	}
}

// order-service not reachable at all behaves the same: no panic, nothing cancelled, and
// the order stays pending for the next run.
func TestOrderServiceUnreachableCancelsNothing(t *testing.T) {
	setupTestDB(t)
	id := insertOrder(t, "pending", 40)
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	n := cancelAbandonedOrders(&http.Client{Timeout: 2 * time.Second}, deadURL, abandonedAfter)

	if n != 0 {
		t.Errorf("cancelled: got %d, want 0", n)
	}
	if s := statusOf(t, id); s != "pending" {
		t.Errorf("status: got %s, want pending", s)
	}
}

// One run handles at most abandonedBatch orders; the rest wait for the next run.
func TestOneRunIsCappedAtTheBatchSize(t *testing.T) {
	setupTestDB(t)
	for i := 0; i < abandonedBatch+5; i++ {
		insertOrder(t, "pending", 40)
	}
	f, url := newFakeOrderService(t)

	n := cancelAbandonedOrders(&http.Client{Timeout: 5 * time.Second}, url, abandonedAfter)

	if n != abandonedBatch || len(f.calls) != abandonedBatch {
		t.Errorf("cancelled %d with %d calls, want %d of each", n, len(f.calls), abandonedBatch)
	}
}

// The age is judged by the database's own clock, so it must not matter which time zone
// the database session or this process uses. Found on a laptop in UTC+3 against a UTC
// Postgres: a 1 minute old order was cancelled because the Go time was compared with the
// TIMESTAMP column as a bare wall clock. Each zone below differs from this process's by
// hours, whatever it is.
func TestAgeDoesNotDependOnTheTimeZone(t *testing.T) {
	setupTestDB(t)
	base := os.Getenv("TEST_DATABASE_URL")
	for _, zone := range []string{"Pacific/Auckland", "America/Los_Angeles", "UTC"} {
		u, err := url.Parse(base)
		if err != nil {
			t.Fatalf("parse TEST_DATABASE_URL: %v", err)
		}
		q := u.Query()
		q.Set("timezone", zone)
		u.RawQuery = q.Encode()
		db.Close()
		db, err = sql.Open("postgres", u.String())
		if err != nil {
			t.Fatalf("open db in %s: %v", zone, err)
		}
		if _, err := db.Exec(`TRUNCATE orders RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		old := insertOrder(t, "pending", 31)
		insertOrder(t, "pending", 1) // too new
		f, srvURL := newFakeOrderService(t)

		n := cancelAbandonedOrders(&http.Client{Timeout: 2 * time.Second}, srvURL, abandonedAfter)

		if n != 1 || len(f.calls) != 1 || f.calls[0].OrderID != old {
			t.Errorf("database in %s: cancelled %d with calls %v, want only order %d", zone, n, f.calls, old)
		}
	}
}
