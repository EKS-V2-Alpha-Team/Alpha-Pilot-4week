package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func chargeWithKey(t *testing.T, key string, orderID int, amount float64) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"order_id": orderID, "customer_id": "c1", "amount": amount, "method": "card",
	})
	req := httptest.NewRequest(http.MethodPost, "/charge", bytes.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	handleCharge(rec, req)
	return rec
}

// The bug the key closes: a declined charge sent again could be approved on the retry.
func TestDeclinedChargeStaysDeclinedOnRetry(t *testing.T) {
	setupTestDB(t)
	processPayment = func() string { return "failed" }
	first := chargeWithKey(t, "charge-order-7", 7, 20)
	if first.Code != http.StatusPaymentRequired {
		t.Fatalf("first: got %d, want 402", first.Code)
	}
	processPayment = func() string { return "completed" } // the provider would now approve
	again := chargeWithKey(t, "charge-order-7", 7, 20)
	if again.Code != http.StatusPaymentRequired || again.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry: got %d replayed=%q, want the stored 402", again.Code, again.Header().Get("Idempotent-Replayed"))
	}
	if !sameJSON(t, first, again) {
		t.Fatalf("retry body differs:\n%s\n%s", first.Body, again.Body)
	}
	if n := countPayments(t, 7, "completed"); n != 0 {
		t.Fatalf("%d completed payments after a replayed decline, want 0", n)
	}
}

func TestSuccessfulChargeIsReplayed(t *testing.T) {
	setupTestDB(t)
	first := chargeWithKey(t, "charge-order-8", 8, 20)
	again := chargeWithKey(t, "charge-order-8", 8, 20)
	if first.Code != http.StatusCreated || again.Code != http.StatusCreated || !sameJSON(t, first, again) {
		t.Fatalf("got %d then %d, want the same 201 twice", first.Code, again.Code)
	}
	if n := countPayments(t, 8, "completed"); n != 1 {
		t.Fatalf("%d completed payments, want 1", n)
	}
}

func TestKeyReusedForAnotherChargeIsRefused(t *testing.T) {
	setupTestDB(t)
	chargeWithKey(t, "k1", 9, 20)
	if rec := chargeWithKey(t, "k1", 9, 25); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("same key, other amount: got %d, want 422", rec.Code)
	}
}

func TestConcurrentChargesWithOneKeyChargeOnce(t *testing.T) {
	setupTestDB(t)
	const n = 10
	codes := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = chargeWithKey(t, "charge-order-10", 10, 20).Code
		}(i)
	}
	close(start)
	wg.Wait()
	for i, c := range codes {
		// 201, 200 or 409 are all fine; a second charge is not.
		if c != http.StatusCreated && c != http.StatusOK && c != http.StatusConflict {
			t.Errorf("request %d: got %d, want 201, 200 or 409", i, c)
		}
	}
	if got := countPayments(t, 10, "completed"); got != 1 {
		t.Fatalf("%d completed payments, want 1", got)
	}
	var rows int
	db.QueryRow(`SELECT count(*) FROM payments WHERE order_id = 10`).Scan(&rows)
	if rows != 1 {
		t.Fatalf("%d payment rows, want 1", rows)
	}
}

func TestChargeWithoutAKeyStillWorks(t *testing.T) {
	setupTestDB(t)
	if rec := chargeWithKey(t, "", 11, 20); rec.Code != http.StatusCreated {
		t.Fatalf("no key: got %d, want 201", rec.Code)
	}
}

// sameJSON compares two responses as JSON, ignoring key order.
func sameJSON(t *testing.T, a, b *httptest.ResponseRecorder) bool {
	t.Helper()
	var x, y map[string]interface{}
	if json.Unmarshal(a.Body.Bytes(), &x) != nil || json.Unmarshal(b.Body.Bytes(), &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
