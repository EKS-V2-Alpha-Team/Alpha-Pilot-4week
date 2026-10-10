package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
)

func orderIDFrom(t *testing.T, body []byte) int {
	t.Helper()
	var out struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return out.ID
}

// The same key and the same request twice: one order, one outbox event. The second
// answer is 200 (not 201) and carries the first order's id.
func TestSameKeyTwiceCreatesOneOrder(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })

	first := postOrderWithKey(t, "k1", defaultOrderBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("first: got %d, want 201", first.Code)
	}
	second := postOrderWithKey(t, "k1", defaultOrderBody)
	if second.Code != http.StatusOK {
		t.Fatalf("retry: got %d, want 200", second.Code)
	}
	if second.Header().Get("Idempotent-Replayed") != "true" {
		t.Errorf("retry should say it was a replay")
	}
	if a, b := orderIDFrom(t, first.Body.Bytes()), orderIDFrom(t, second.Body.Bytes()); a != b {
		t.Errorf("retry returned order %d, want the original %d", b, a)
	}
	if n := count(t, `SELECT count(*) FROM orders`); n != 1 {
		t.Errorf("orders: got %d, want 1", n)
	}
	if n := count(t, `SELECT count(*) FROM order_outbox WHERE event_type = 'order.created'`); n != 1 {
		t.Errorf("order.created events: got %d, want 1", n)
	}
}

// Ten identical requests at the same moment (a double click, an impatient retry loop):
// exactly one order is made and everybody is told about that one order.
func TestConcurrentSameKeyCreatesOneOrder(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })
	warmPool(t, 12)

	for run := 0; run < 20; run++ {
		if _, err := db.Exec(`TRUNCATE order_outbox, order_events, orders RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		const n = 10
		codes := make([]int, n)
		ids := make([]int, n)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				rec := postOrderWithKey(t, "double-click", defaultOrderBody)
				codes[i] = rec.Code
				if rec.Code == http.StatusCreated || rec.Code == http.StatusOK {
					ids[i] = orderIDFrom(t, rec.Body.Bytes())
				}
			}(i)
		}
		close(start)
		wg.Wait()

		created := 0
		for i, c := range codes {
			switch c {
			case http.StatusCreated:
				created++
			case http.StatusOK:
			default:
				t.Fatalf("run %d: request %d got %d", run, i, c)
			}
			if ids[i] != ids[0] {
				t.Fatalf("run %d: requests got different orders: %v", run, ids)
			}
		}
		if created != 1 {
			t.Fatalf("run %d: %d requests got 201, want exactly 1 (codes %v)", run, created, codes)
		}
		if got := count(t, `SELECT count(*) FROM orders`); got != 1 {
			t.Fatalf("run %d: orders: got %d, want 1", run, got)
		}
		if got := count(t, `SELECT count(*) FROM order_outbox WHERE event_type = 'order.created'`); got != 1 {
			t.Fatalf("run %d: order.created events: got %d, want 1", run, got)
		}
	}
}

// Reusing a key for a different order is a client bug. It must not silently return the
// old order, and it must not create a new one.
func TestSameKeyWithDifferentRequestIsRejected(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })

	if rec := postOrderWithKey(t, "k1", defaultOrderBody); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d", rec.Code)
	}
	other := `{"items":[{"product_id":"p1","quantity":5}]}`
	if rec := postOrderWithKey(t, "k1", other); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("different request, same key: got %d, want 422", rec.Code)
	}
	if n := count(t, `SELECT count(*) FROM orders`); n != 1 {
		t.Errorf("orders: got %d, want 1", n)
	}
}

// A different key is a different order, even with an identical body.
func TestDifferentKeyIsANewOrder(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })

	a := postOrderWithKey(t, "k1", defaultOrderBody)
	b := postOrderWithKey(t, "k2", defaultOrderBody)
	if a.Code != http.StatusCreated || b.Code != http.StatusCreated {
		t.Fatalf("got %d and %d, want 201 and 201", a.Code, b.Code)
	}
	if n := count(t, `SELECT count(*) FROM orders`); n != 2 {
		t.Errorf("orders: got %d, want 2", n)
	}
}

func TestMissingOrOversizedKeyIsRejected(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })

	if rec := postOrderWithKey(t, "", defaultOrderBody); rec.Code != http.StatusBadRequest {
		t.Errorf("no key: got %d, want 400", rec.Code)
	}
	long := make([]byte, maxIdempotencyKeyLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if rec := postOrderWithKey(t, string(long), defaultOrderBody); rec.Code != http.StatusBadRequest {
		t.Errorf("256-character key: got %d, want 400", rec.Code)
	}
	if n := count(t, `SELECT count(*) FROM orders`); n != 0 {
		t.Errorf("orders: got %d, want 0", n)
	}
}

// The customer's retry arrives while inventory is down. Their order already exists, so
// they should get it back rather than a 503.
func TestReplayStillWorksWhenInventoryIsDown(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })

	first := postOrderWithKey(t, "k1", defaultOrderBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("first: %d", first.Code)
	}
	old := inventoryURL
	inventoryURL = "http://127.0.0.1:1" // nothing listens here
	t.Cleanup(func() { inventoryURL = old })

	retry := postOrderWithKey(t, "k1", defaultOrderBody)
	if retry.Code != http.StatusOK {
		t.Fatalf("retry with inventory down: got %d, want 200", retry.Code)
	}
	if n := count(t, `SELECT count(*) FROM orders`); n != 1 {
		t.Errorf("orders: got %d, want 1", n)
	}
}

// A request that fails after claiming the key (inventory down on a first attempt) must
// not leave the key behind, or the customer's real retry would be refused for 24 hours.
func TestFailedAttemptDoesNotUseUpTheKey(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })

	old := inventoryURL
	inventoryURL = "http://127.0.0.1:1"
	if rec := postOrderWithKey(t, "k1", defaultOrderBody); rec.Code == http.StatusCreated {
		t.Fatalf("expected a failure with inventory down, got 201")
	}
	inventoryURL = old

	if n := count(t, `SELECT count(*) FROM idempotency_keys`); n != 0 {
		t.Fatalf("a failed attempt left %d keys behind", n)
	}
	if rec := postOrderWithKey(t, "k1", defaultOrderBody); rec.Code != http.StatusCreated {
		t.Fatalf("retry after the failure: got %d, want 201", rec.Code)
	}
}

// Keys are per customer: another customer using the same string gets their own order.
func TestKeysAreScopedToTheCustomer(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })

	if rec := postOrderWithKey(t, "shared", defaultOrderBody); rec.Code != http.StatusCreated {
		t.Fatalf("alice: %d", rec.Code)
	}
	req := newOrderRequest("bob@test.com", "shared", defaultOrderBody)
	rec := serve(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bob with alice's key string: got %d, want 201", rec.Code)
	}
	if n := count(t, `SELECT count(*) FROM orders`); n != 2 {
		t.Errorf("orders: got %d, want 2", n)
	}
}
