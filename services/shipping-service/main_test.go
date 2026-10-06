package main

// Integration tests for creating and moving shipments (ALPHA-78). They need a real,
// DISPOSABLE Postgres:
//
//	TEST_DATABASE_URL=postgres://app:localdev@localhost:5432/shipping_test?sslmode=disable go test ./...
//
// WARNING: every test TRUNCATES shipments and tracking_events in that database.
// Without TEST_DATABASE_URL the tests are skipped.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	db.SetMaxOpenConns(20)
	migrate()
	if _, err := db.Exec(`TRUNCATE tracking_events, shipments RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func createFor(t *testing.T, orderID int) (int, map[string]interface{}) {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"order_id": orderID, "recipient_name": "sara@example.com",
		"address_line1": "PLACEHOLDER", "city": "TBC", "postcode": "TBC",
	})
	rec := httptest.NewRecorder()
	createShipment(rec, httptest.NewRequest(http.MethodPost, "/shipments", bytes.NewReader(body)))
	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	return rec.Code, resp
}

func moveTo(t *testing.T, orderID int, status string) (int, map[string]interface{}) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"status": status})
	rec := httptest.NewRecorder()
	handleShipment(rec, httptest.NewRequest(http.MethodPut, "/shipments/by-order/"+strconv.Itoa(orderID), bytes.NewReader(body)))
	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	return rec.Code, resp
}

func shipmentCount(t *testing.T, orderID int) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM shipments WHERE order_id = $1`, orderID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func statusOf(t *testing.T, orderID int) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT status FROM shipments WHERE order_id = $1`, orderID).Scan(&s); err != nil {
		t.Fatalf("status: %v", err)
	}
	return s
}

func eventCount(t *testing.T, orderID int, status string) int {
	t.Helper()
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM tracking_events e JOIN shipments s ON s.id = e.shipment_id
		 WHERE s.order_id = $1 AND e.status = $2`, orderID, status).Scan(&n)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	return n
}

func TestCreateShipmentOnce(t *testing.T) {
	setupTestDB(t)
	code, first := createFor(t, 42)
	if code != http.StatusCreated || first["tracking_number"] == "" {
		t.Fatalf("first create: got %d %v, want 201 with a tracking number", code, first)
	}
	code, second := createFor(t, 42)
	if code != http.StatusOK || second["already_created"] != true {
		t.Fatalf("second create: got %d %v, want 200 already_created", code, second)
	}
	if first["tracking_number"] != second["tracking_number"] {
		t.Errorf("repeat returned a different tracking number: %v vs %v", first["tracking_number"], second["tracking_number"])
	}
	if n := shipmentCount(t, 42); n != 1 {
		t.Errorf("%d shipments for order 42, want 1", n)
	}
	if n := eventCount(t, 42, "label_created"); n != 1 {
		t.Errorf("%d label_created events, want 1", n)
	}
}

func TestConcurrentCreatesMakeOneShipment(t *testing.T) {
	setupTestDB(t)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			createFor(t, 7)
		}()
	}
	wg.Wait()
	if n := shipmentCount(t, 7); n != 1 {
		t.Errorf("%d shipments for order 7, want 1", n)
	}
	if n := eventCount(t, 7, "label_created"); n != 1 {
		t.Errorf("%d label_created events, want 1", n)
	}
}

func TestCreateNeedsOrderAndRecipient(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{"order_id": 5})
	rec := httptest.NewRecorder()
	createShipment(rec, httptest.NewRequest(http.MethodPost, "/shipments", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestShipmentMovesForwardAndStampsDates(t *testing.T) {
	setupTestDB(t)
	createFor(t, 42)

	code, resp := moveTo(t, 42, "in_transit")
	if code != http.StatusOK || resp["changed"] != true || statusOf(t, 42) != "in_transit" {
		t.Fatalf("in_transit: got %d %v (status %s)", code, resp, statusOf(t, 42))
	}
	code, resp = moveTo(t, 42, "delivered")
	if code != http.StatusOK || resp["changed"] != true || statusOf(t, 42) != "delivered" {
		t.Fatalf("delivered: got %d %v (status %s)", code, resp, statusOf(t, 42))
	}

	var shipped, delivered sql.NullTime
	db.QueryRow(`SELECT shipped_at, delivered_at FROM shipments WHERE order_id = 42`).Scan(&shipped, &delivered)
	if !shipped.Valid || !delivered.Valid {
		t.Errorf("shipped_at valid=%v delivered_at valid=%v, want both set", shipped.Valid, delivered.Valid)
	}
	if resp["tracking_number"] == "" {
		t.Error("response has no tracking number (the shipped email needs it)")
	}
}

func TestRepeatedStatusRequestChangesNothing(t *testing.T) {
	setupTestDB(t)
	createFor(t, 42)
	moveTo(t, 42, "in_transit")
	code, resp := moveTo(t, 42, "in_transit")
	if code != http.StatusOK || resp["changed"] != false {
		t.Fatalf("repeat: got %d %v, want 200 changed=false", code, resp)
	}
	if n := eventCount(t, 42, "in_transit"); n != 1 {
		t.Errorf("%d in_transit events, want 1", n)
	}
}

// An old "shipped" event arriving after "delivered" must not drag the shipment back.
func TestShipmentNeverMovesBackwards(t *testing.T) {
	setupTestDB(t)
	createFor(t, 42)
	moveTo(t, 42, "delivered")
	code, resp := moveTo(t, 42, "in_transit")
	if code != http.StatusOK || resp["changed"] != false || statusOf(t, 42) != "delivered" {
		t.Fatalf("got %d %v (status %s), want 200 changed=false and still delivered", code, resp, statusOf(t, 42))
	}
}

func TestCancelledShipmentStaysCancelled(t *testing.T) {
	setupTestDB(t)
	createFor(t, 42)
	if code, resp := moveTo(t, 42, "cancelled"); code != http.StatusOK || resp["changed"] != true {
		t.Fatalf("cancel: got %d %v", code, resp)
	}
	code, resp := moveTo(t, 42, "in_transit")
	if code != http.StatusOK || resp["changed"] != false || statusOf(t, 42) != "cancelled" {
		t.Fatalf("got %d %v (status %s), want 200 changed=false and still cancelled", code, resp, statusOf(t, 42))
	}
}

func TestDeliveredShipmentCannotBeCancelled(t *testing.T) {
	setupTestDB(t)
	createFor(t, 42)
	moveTo(t, 42, "delivered")
	code, resp := moveTo(t, 42, "cancelled")
	if code != http.StatusOK || resp["changed"] != false || statusOf(t, 42) != "delivered" {
		t.Fatalf("got %d %v (status %s), want 200 changed=false and still delivered", code, resp, statusOf(t, 42))
	}
}

func TestOrderWithoutShipmentIs404(t *testing.T) {
	setupTestDB(t)
	if code, _ := moveTo(t, 999, "in_transit"); code != http.StatusNotFound {
		t.Errorf("got %d, want 404", code)
	}
}

func TestUnknownStatusIsRejected(t *testing.T) {
	setupTestDB(t)
	createFor(t, 42)
	if code, _ := moveTo(t, 42, "teleported"); code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", code)
	}
	if got := statusOf(t, 42); got != "label_created" {
		t.Errorf("status %s, want label_created", got)
	}
}

func TestConcurrentMovesRecordOneEvent(t *testing.T) {
	setupTestDB(t)
	createFor(t, 42)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			moveTo(t, 42, "in_transit")
		}()
	}
	wg.Wait()
	if n := eventCount(t, 42, "in_transit"); n != 1 {
		t.Errorf("%d in_transit events, want 1", n)
	}
}
