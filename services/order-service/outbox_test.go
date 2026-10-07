package main

// Integration tests for the transactional outbox. They need a real, DISPOSABLE Postgres:
//
//	TEST_DATABASE_URL=postgres://app:localdev@localhost:5432/orders_test?sslmode=disable go test ./...
//
// WARNING: every test TRUNCATES orders, order_events, order_outbox and idempotency_keys in that database.
// Never point TEST_DATABASE_URL at a database whose data you want to keep.
// Without TEST_DATABASE_URL the tests are skipped, so pipelines without Postgres stay green.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakePublisher records what would have been sent to SQS, or fails on demand.
type fakePublisher struct {
	mu     sync.Mutex
	bodies []string
	err    error
}

func (f *fakePublisher) Publish(_ context.Context, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.bodies = append(f.bodies, body)
	return nil
}

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
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	migrate()
	// Orders are priced by inventory, so these tests use a stand-in that knows "p1".
	useFakeInventory(t, map[string]catalogueEntry{"p1": {Price: 50, Available: 1000}})
	if _, err := db.Exec(`TRUNCATE order_outbox, order_events, orders RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func count(t *testing.T, query string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

const defaultOrderBody = `{"items":[{"product_id":"p1","quantity":2,"price":50}]}`

var keyCounter int64

// postOrder places the default order with a fresh Idempotency-Key, so every call is a new order.
func postOrder(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	return postOrderWithKey(t, fmt.Sprintf("test-key-%d", atomic.AddInt64(&keyCounter, 1)), defaultOrderBody)
}

// postOrderWithKey sends POST / as alice with the given key (an empty key sends no header).
func postOrderWithKey(t *testing.T, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serve(newOrderRequest("alice@test.com", key, body))
}

func newOrderRequest(customer, key, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("X-User-Email", customer)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return req
}

func serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handleOrders(rec, req)
	return rec
}

// 1. The happy path: one request saves the order, its history row and exactly one outbox event.
func TestCreateOrderSavesOrderAndEventTogether(t *testing.T) {
	setupTestDB(t)

	rec := postOrder(t)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body)
	}

	if n := count(t, `SELECT COUNT(*) FROM orders`); n != 1 {
		t.Fatalf("want 1 order, got %d", n)
	}
	if n := count(t, `SELECT COUNT(*) FROM order_events WHERE event_type = 'order_created'`); n != 1 {
		t.Fatalf("want 1 order_events row, got %d", n)
	}
	var eventType string
	var orderID int
	var sentAt sql.NullTime
	err := db.QueryRow(`SELECT event_type, (payload->>'order_id')::int, sent_at FROM order_outbox`).
		Scan(&eventType, &orderID, &sentAt)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if eventType != "order.created" || orderID != 1 || sentAt.Valid {
		t.Fatalf("unexpected outbox row: type=%s order_id=%d sent=%v", eventType, orderID, sentAt.Valid)
	}
}

// 2. Atomicity: if the event cannot be saved, the order must not be saved either.
func TestCreateOrderSavesNothingWhenOutboxWriteFails(t *testing.T) {
	setupTestDB(t)

	// Simulate an outbox failure by hiding the table for the duration of the request.
	if _, err := db.Exec(`ALTER TABLE order_outbox RENAME TO order_outbox_hidden`); err != nil {
		t.Fatalf("hide outbox: %v", err)
	}
	defer db.Exec(`ALTER TABLE order_outbox_hidden RENAME TO order_outbox`)

	rec := postOrder(t)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
	if n := count(t, `SELECT COUNT(*) FROM orders`); n != 0 {
		t.Fatalf("order was saved without its event: %d order(s)", n)
	}
	if n := count(t, `SELECT COUNT(*) FROM order_events`); n != 0 {
		t.Fatalf("order_events row saved without its order: %d", n)
	}
}

// 3. Status changes queue order.status_changed in the same transaction.
func TestStatusChangeQueuesEvent(t *testing.T) {
	setupTestDB(t)
	postOrder(t)

	req := httptest.NewRequest(http.MethodPut, "/status",
		bytes.NewBufferString(`{"order_id":1,"new_status":"confirmed"}`))
	rec := httptest.NewRecorder()
	handleUpdateStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body)
	}
	n := count(t, `SELECT COUNT(*) FROM order_outbox
	                WHERE event_type = 'order.status_changed' AND payload->>'new_status' = 'confirmed'`)
	if n != 1 {
		t.Fatalf("want 1 status_changed event, got %d", n)
	}
}

// 4. The relay sends pending events once, in the envelope the worker expects, and marks them sent.
func TestRelaySendsPendingEventsAndMarksThemSent(t *testing.T) {
	setupTestDB(t)
	postOrder(t)
	postOrder(t)

	pub := &fakePublisher{}
	sent, err := relayOutboxOnce(context.Background(), pub)
	if err != nil || sent != 2 {
		t.Fatalf("want 2 sent, got %d (err %v)", sent, err)
	}

	var env struct {
		ID        string                 `json:"id"`
		Type      string                 `json:"type"`
		Payload   map[string]interface{} `json:"payload"`
		Timestamp string                 `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(pub.bodies[0]), &env); err != nil {
		t.Fatalf("envelope is not JSON: %v", err)
	}
	if env.ID != "order-service-1" || env.Type != "order.created" ||
		env.Payload["order_id"] != float64(1) || env.Timestamp == "" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	if n := count(t, `SELECT COUNT(*) FROM order_outbox WHERE sent_at IS NULL`); n != 0 {
		t.Fatalf("want all rows marked sent, %d still pending", n)
	}

	// A second pass must not send anything again.
	sent, err = relayOutboxOnce(context.Background(), pub)
	if err != nil || sent != 0 || len(pub.bodies) != 2 {
		t.Fatalf("second pass re-sent events: sent=%d total=%d err=%v", sent, len(pub.bodies), err)
	}
}

// 5. SQS down: the event is kept, the failure is recorded, and it is delivered after recovery.
func TestRelayKeepsEventWhenQueueIsDown(t *testing.T) {
	setupTestDB(t)
	postOrder(t)

	pub := &fakePublisher{err: errors.New("queue unreachable")}
	sent, err := relayOutboxOnce(context.Background(), pub)
	if err == nil || sent != 0 {
		t.Fatalf("want an error and 0 sent, got sent=%d err=%v", sent, err)
	}
	var attempts int
	var lastError sql.NullString
	var sentAt sql.NullTime
	if err := db.QueryRow(`SELECT attempts, last_error, sent_at FROM order_outbox WHERE id = 1`).
		Scan(&attempts, &lastError, &sentAt); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if attempts != 1 || !lastError.Valid || sentAt.Valid {
		t.Fatalf("want attempts=1, last_error set, unsent; got %d %v %v", attempts, lastError, sentAt.Valid)
	}

	pub.err = nil // queue is back
	sent, err = relayOutboxOnce(context.Background(), pub)
	if err != nil || sent != 1 {
		t.Fatalf("want delivery after recovery, got sent=%d err=%v", sent, err)
	}
}

// blockingPublisher pauses inside Publish until released, to hold a relay mid-batch.
type blockingPublisher struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingPublisher) Publish(ctx context.Context, _ string) error {
	b.entered <- struct{}{}
	<-b.release
	return nil
}

// 6. Two relays (two order-service replicas on EKS) never pick up the same event at once.
func TestTwoRelaysDoNotSendTheSameEvent(t *testing.T) {
	setupTestDB(t)
	postOrder(t)

	slow := &blockingPublisher{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan int)
	go func() {
		n, _ := relayOutboxOnce(context.Background(), slow)
		done <- n
	}()

	select {
	case <-slow.entered: // replica A holds the row and is "sending"
	case <-time.After(5 * time.Second):
		t.Fatal("replica A never started sending")
	}

	// Replica B must skip the locked row immediately, not wait for replica A.
	other := &fakePublisher{}
	type result struct {
		n   int
		err error
	}
	bDone := make(chan result, 1)
	go func() {
		n, err := relayOutboxOnce(context.Background(), other)
		bDone <- result{n, err}
	}()
	select {
	case r := <-bDone:
		if r.err != nil || r.n != 0 || len(other.bodies) != 0 {
			close(slow.release)
			t.Fatalf("replica B sent an event replica A was already sending: n=%d err=%v", r.n, r.err)
		}
	case <-time.After(3 * time.Second):
		close(slow.release)
		t.Fatal("replica B waited for replica A's lock instead of skipping the row")
	}

	close(slow.release)
	if n := <-done; n != 1 {
		t.Fatalf("replica A should have sent 1, sent %d", n)
	}
}
