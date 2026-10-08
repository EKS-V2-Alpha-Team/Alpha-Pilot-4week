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

func refundByOrder(t *testing.T, orderID int) (int, map[string]interface{}) {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"order_id": orderID, "reason": "order cancelled"})
	rec := httptest.NewRecorder()
	handleRefund(rec, httptest.NewRequest(http.MethodPost, "/refund", bytes.NewReader(body)))
	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	return rec.Code, resp
}

func refundRows(t *testing.T, orderID int) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM payments WHERE order_id = $1 AND method = 'refund'`, orderID).Scan(&n); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	return n
}

func TestRefundByOrderRefundsTheCharge(t *testing.T) {
	setupTestDB(t)
	charge(t, 42, 100)

	code, resp := refundByOrder(t, 42)
	if code != http.StatusOK || resp["status"] != "completed" {
		t.Fatalf("refund: got %d %v, want 200 completed", code, resp)
	}
	if got := refundRows(t, 42); got != 1 {
		t.Errorf("refund rows = %d, want 1", got)
	}
	if got := countPayments(t, 42, "refunded"); got != 1 {
		t.Errorf("charge status refunded count = %d, want 1", got)
	}
}

// A redelivered cancel event refunds again. The money goes back once.
func TestRefundByOrderTwiceRefundsOnce(t *testing.T) {
	setupTestDB(t)
	charge(t, 42, 100)
	refundByOrder(t, 42)

	code, resp := refundByOrder(t, 42)
	if code != http.StatusOK || resp["status"] != "already_refunded" {
		t.Errorf("repeat refund: got %d %v, want 200 already_refunded", code, resp)
	}
	if got := refundRows(t, 42); got != 1 {
		t.Errorf("refund rows = %d, want 1", got)
	}
}

func TestConcurrentRefundsRefundOnce(t *testing.T) {
	setupTestDB(t)
	charge(t, 42, 100)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			refundByOrder(t, 42)
		}()
	}
	wg.Wait()
	if got := refundRows(t, 42); got != 1 {
		t.Errorf("refund rows = %d, want 1 (concurrent refunds must not each pay out)", got)
	}
}

// An order that was never charged (out of stock, declined) has nothing to refund.
func TestRefundOfUnchargedOrderIsNotFound(t *testing.T) {
	setupTestDB(t)
	if code, _ := refundByOrder(t, 99); code != http.StatusNotFound {
		t.Errorf("got %d, want 404", code)
	}
}

func TestRefundNeedsAPaymentOrOrder(t *testing.T) {
	setupTestDB(t)
	rec := httptest.NewRecorder()
	handleRefund(rec, httptest.NewRequest(http.MethodPost, "/refund", bytes.NewReader([]byte(`{}`))))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

// Item 20: the refund mirrors the charge, so debits and credits across the order match.
func TestLedgerBalancesAfterRefund(t *testing.T) {
	setupTestDB(t)
	charge(t, 42, 100)
	refundByOrder(t, 42)

	var debit, credit float64
	if err := db.QueryRow(`SELECT COALESCE(SUM(debit),0), COALESCE(SUM(credit),0) FROM ledger_entries`).Scan(&debit, &credit); err != nil {
		t.Fatalf("sum ledger: %v", err)
	}
	if debit != credit {
		t.Errorf("ledger out of balance: debit %.2f, credit %.2f", debit, credit)
	}
	var entries int
	db.QueryRow(`SELECT count(*) FROM ledger_entries`).Scan(&entries)
	if entries != 4 {
		t.Errorf("ledger entries = %d, want 4 (charge, revenue, refund, revenue_reversal)", entries)
	}
}

// By payment id the old rule holds: a second refund of the same payment is a 409.
func TestRefundByPaymentIDTwiceIsConflict(t *testing.T) {
	setupTestDB(t)
	_, paid := charge(t, 42, 100)
	body, _ := json.Marshal(map[string]interface{}{"payment_id": paid.PaymentID})
	do := func() int {
		rec := httptest.NewRecorder()
		handleRefund(rec, httptest.NewRequest(http.MethodPost, "/refund", bytes.NewReader(body)))
		return rec.Code
	}
	if first := do(); first != http.StatusOK {
		t.Fatalf("first refund: got %d, want 200", first)
	}
	if second := do(); second != http.StatusConflict {
		t.Errorf("second refund: got %d, want 409", second)
	}
	if got := refundRows(t, 42); got != 1 {
		t.Errorf("refund rows = %d, want 1", got)
	}
}

// A refund row is a payment too, but it can't be refunded itself.
func TestRefundCannotBeRefunded(t *testing.T) {
	setupTestDB(t)
	charge(t, 42, 100)
	_, resp := refundByOrder(t, 42)
	refundID, _ := resp["refund_id"].(string)
	body, _ := json.Marshal(map[string]interface{}{"payment_id": refundID})
	rec := httptest.NewRecorder()
	handleRefund(rec, httptest.NewRequest(http.MethodPost, "/refund", bytes.NewReader(body)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("refund of a refund: got %d, want 404", rec.Code)
	}
}
