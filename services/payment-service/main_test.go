package main

// Integration tests for charge idempotency (ALPHA-51). They need a real, DISPOSABLE Postgres:
//
//	TEST_DATABASE_URL=postgres://app:localdev@localhost:5432/payment_test?sslmode=disable go test ./...
//
// WARNING: every test TRUNCATES payments and ledger_entries in that database.
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
	if _, err := db.Exec(`TRUNCATE ledger_entries, payments RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	// Make the simulated provider deterministic; tests that need a failure override it.
	processPayment = func() string { return "completed" }
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

type chargeResp struct {
	PaymentID   string `json:"payment_id"`
	Status      string `json:"status"`
	AlreadyPaid bool   `json:"already_paid"`
}

func charge(t *testing.T, orderID int, amount float64) (int, chargeResp) {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"order_id": orderID, "customer_id": "c1", "amount": amount, "method": "card",
	})
	rec := httptest.NewRecorder()
	handleCharge(rec, httptest.NewRequest(http.MethodPost, "/charge", bytes.NewReader(body)))
	var resp chargeResp
	json.NewDecoder(rec.Body).Decode(&resp)
	return rec.Code, resp
}

func countPayments(t *testing.T, orderID int, status string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM payments WHERE order_id = $1 AND status = $2 AND method IS DISTINCT FROM 'refund'`,
		orderID, status,
	).Scan(&n); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	return n
}

func TestChargeTwiceChargesOnce(t *testing.T) {
	setupTestDB(t)
	code1, first := charge(t, 42, 100)
	if code1 != http.StatusCreated {
		t.Fatalf("first charge: got %d, want 201", code1)
	}
	code2, repeat := charge(t, 42, 100)
	if code2 != http.StatusOK || !repeat.AlreadyPaid {
		t.Fatalf("repeat charge: got %d already_paid=%v, want 200 already_paid=true", code2, repeat.AlreadyPaid)
	}
	if repeat.PaymentID != first.PaymentID {
		t.Errorf("repeat returned payment %s, want the original %s", repeat.PaymentID, first.PaymentID)
	}
	if got := countPayments(t, 42, "completed"); got != 1 {
		t.Errorf("completed charges = %d, want 1", got)
	}
}

func TestChargeWithDifferentAmountForPaidOrderIsConflict(t *testing.T) {
	setupTestDB(t)
	_, first := charge(t, 42, 100)
	code, resp := charge(t, 42, 5)
	if code != http.StatusConflict {
		t.Fatalf("different amount: got %d, want 409", code)
	}
	if resp.PaymentID != first.PaymentID {
		t.Errorf("409 carried payment %s, want the original %s", resp.PaymentID, first.PaymentID)
	}
	if got := countPayments(t, 42, "completed"); got != 1 {
		t.Errorf("completed charges = %d, want 1", got)
	}
}

func TestConcurrentChargesForSameOrderChargeOnce(t *testing.T) {
	setupTestDB(t)
	start := make(chan struct{})
	codes := make([]int, 20)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i], _ = charge(t, 42, 100)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusCreated && c != http.StatusOK {
			t.Errorf("request %d: got %d, want 201 or 200", i, c)
		}
	}
	if got := countPayments(t, 42, "completed"); got != 1 {
		t.Errorf("completed charges = %d, want 1", got)
	}
}

func TestChargeCanBeRetriedAfterAFailure(t *testing.T) {
	setupTestDB(t)
	processPayment = func() string { return "failed" }
	if code, _ := charge(t, 42, 100); code != http.StatusPaymentRequired {
		t.Fatalf("failed charge: got %d, want 402", code)
	}
	processPayment = func() string { return "completed" }
	if code, _ := charge(t, 42, 100); code != http.StatusCreated {
		t.Fatalf("retry after failure: got %d, want 201", code)
	}
	if got := countPayments(t, 42, "completed"); got != 1 {
		t.Errorf("completed charges = %d, want 1", got)
	}
}

func TestRefundStillWorksWithOneChargeRule(t *testing.T) {
	setupTestDB(t)
	_, paid := charge(t, 42, 100)
	body, _ := json.Marshal(map[string]interface{}{"payment_id": paid.PaymentID, "reason": "test"})
	rec := httptest.NewRecorder()
	handleRefund(rec, httptest.NewRequest(http.MethodPost, "/refund", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("refund: got %d, want 200: %s", rec.Code, rec.Body)
	}
	var refunds int
	db.QueryRow(`SELECT count(*) FROM payments WHERE order_id = 42 AND method = 'refund'`).Scan(&refunds)
	if refunds != 1 {
		t.Errorf("refund rows = %d, want 1", refunds)
	}
}

func TestChargeWithRefundMethodIsRejected(t *testing.T) {
	setupTestDB(t)
	body, _ := json.Marshal(map[string]interface{}{
		"order_id": 42, "customer_id": "c1", "amount": 100, "method": "refund",
	})
	rec := httptest.NewRecorder()
	handleCharge(rec, httptest.NewRequest(http.MethodPost, "/charge", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("method refund: got %d, want 400", rec.Code)
	}
}
