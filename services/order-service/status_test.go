package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// putStatus sends PUT /status for order 1 and returns the response code.
func putStatus(t *testing.T, status string) int {
	t.Helper()
	body := fmt.Sprintf(`{"order_id":1,"new_status":%q}`, status)
	req := httptest.NewRequest(http.MethodPut, "/status", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handleUpdateStatus(rec, req)
	return rec.Code
}

func orderStatus(t *testing.T) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT status FROM orders WHERE id = 1`).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return s
}

// warmPool opens n connections so concurrent requests really run at the same time.
func warmPool(t *testing.T, n int) {
	t.Helper()
	db.SetMaxOpenConns(n)
	db.SetMaxIdleConns(n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := db.Exec(`SELECT pg_sleep(0.05)`); err != nil {
				t.Errorf("warm: %v", err)
			}
		}()
	}
	wg.Wait()
}

// A redelivered message asks for the status the order already has. That is a repeat,
// not a conflict: answer 200 and change nothing.
func TestSameStatusAgainIsSuccessAndChangesNothing(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })
	if rec := postOrder(t); rec.Code != http.StatusCreated {
		t.Fatalf("create order: %d", rec.Code)
	}
	if code := putStatus(t, "confirmed"); code != http.StatusOK {
		t.Fatalf("first confirm: got %d, want 200", code)
	}

	if code := putStatus(t, "confirmed"); code != http.StatusOK {
		t.Fatalf("repeat confirm: got %d, want 200", code)
	}

	if n := count(t, `SELECT count(*) FROM order_events WHERE order_id = 1 AND event_type = 'status_changed'`); n != 1 {
		t.Errorf("history rows: got %d, want 1", n)
	}
	if n := count(t, `SELECT count(*) FROM order_outbox WHERE event_type = 'order.status_changed'`); n != 1 {
		t.Errorf("status events: got %d, want 1", n)
	}
}

// Confirm and cancel arrive at the same time. Exactly one may win; the loser gets 409.
func TestConcurrentConfirmAndCancelOnlyOneWins(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })
	warmPool(t, 4)

	for run := 0; run < 30; run++ {
		if _, err := db.Exec(`TRUNCATE order_outbox, order_events, orders RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		if rec := postOrder(t); rec.Code != http.StatusCreated {
			t.Fatalf("create order: %d", rec.Code)
		}

		codes := make([]int, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i, status := range []string{"confirmed", "cancelled"} {
			wg.Add(1)
			go func(i int, status string) {
				defer wg.Done()
				<-start
				codes[i] = putStatus(t, status)
			}(i, status)
		}
		close(start)
		wg.Wait()

		wins := 0
		for _, c := range codes {
			if c == http.StatusOK {
				wins++
			}
		}
		if wins != 1 {
			t.Fatalf("run %d: confirm=%d cancel=%d, want exactly one 200 (final status %s)", run, codes[0], codes[1], orderStatus(t))
		}
		if n := count(t, `SELECT count(*) FROM order_outbox WHERE event_type = 'order.status_changed'`); n != 1 {
			t.Fatalf("run %d: status events: got %d, want 1", run, n)
		}
	}
}

// Two copies of the same message confirm at the same time. Both succeed (the second is a
// repeat) but the order changes, and emits its event, only once.
func TestConcurrentSameConfirmBothSucceedOnce(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })
	warmPool(t, 4)

	for run := 0; run < 30; run++ {
		if _, err := db.Exec(`TRUNCATE order_outbox, order_events, orders RESTART IDENTITY CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		if rec := postOrder(t); rec.Code != http.StatusCreated {
			t.Fatalf("create order: %d", rec.Code)
		}

		codes := make([]int, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				codes[i] = putStatus(t, "confirmed")
			}(i)
		}
		close(start)
		wg.Wait()

		if codes[0] != http.StatusOK || codes[1] != http.StatusOK {
			t.Fatalf("run %d: got %v, want both 200", run, codes)
		}
		if n := count(t, `SELECT count(*) FROM order_outbox WHERE event_type = 'order.status_changed'`); n != 1 {
			t.Fatalf("run %d: status events: got %d, want 1", run, n)
		}
	}
}

// A real conflict still fails: a cancelled order can't be confirmed.
func TestConfirmAfterCancelIsStillRejected(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })
	if rec := postOrder(t); rec.Code != http.StatusCreated {
		t.Fatalf("create order: %d", rec.Code)
	}
	if code := putStatus(t, "cancelled"); code != http.StatusOK {
		t.Fatalf("cancel: got %d", code)
	}
	if code := putStatus(t, "confirmed"); code != http.StatusConflict {
		t.Fatalf("confirm after cancel: got %d, want 409", code)
	}
	if s := orderStatus(t); s != "cancelled" {
		t.Errorf("status: got %s, want cancelled", s)
	}
}

// putStatusExpecting sends PUT /status for order 1 with an expected current status.
func putStatusExpecting(t *testing.T, status, expected string) int {
	t.Helper()
	body := fmt.Sprintf(`{"order_id":1,"new_status":%q,"expected_status":%q}`, status, expected)
	req := httptest.NewRequest(http.MethodPut, "/status", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handleUpdateStatus(rec, req)
	return rec.Code
}

// The scheduler saw the order as pending and asks to cancel it. If the worker confirmed
// it in the meantime, the cancel must be refused: confirmed to cancelled is a valid move
// on its own, so only the expected status stops it.
func TestExpectedStatusStopsAChangeToAnOrderThatMovedOn(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })
	if rec := postOrder(t); rec.Code != http.StatusCreated {
		t.Fatalf("create order: %d", rec.Code)
	}
	if code := putStatus(t, "confirmed"); code != http.StatusOK {
		t.Fatalf("confirm: got %d, want 200", code)
	}

	if code := putStatusExpecting(t, "cancelled", "pending"); code != http.StatusConflict {
		t.Fatalf("cancel expecting pending: got %d, want 409", code)
	}

	if s := orderStatus(t); s != "confirmed" {
		t.Errorf("status: got %s, want confirmed", s)
	}
	if n := count(t, `SELECT count(*) FROM order_outbox WHERE event_type = 'order.status_changed' AND payload->>'new_status' = 'cancelled'`); n != 0 {
		t.Errorf("cancel events: got %d, want 0", n)
	}
}

// When the order is still in the expected status the change goes through as normal.
func TestExpectedStatusMatchingIsApplied(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })
	if rec := postOrder(t); rec.Code != http.StatusCreated {
		t.Fatalf("create order: %d", rec.Code)
	}

	if code := putStatusExpecting(t, "cancelled", "pending"); code != http.StatusOK {
		t.Fatalf("cancel expecting pending: got %d, want 200", code)
	}

	if s := orderStatus(t); s != "cancelled" {
		t.Errorf("status: got %s, want cancelled", s)
	}
}

// An order that is already cancelled stays a repeat (200, nothing written), whatever
// the caller expected. The scheduler may retry after a lost reply.
func TestExpectedStatusOnAlreadyCancelledOrderIsARepeat(t *testing.T) {
	setupTestDB(t)
	t.Cleanup(func() { db.Close() })
	if rec := postOrder(t); rec.Code != http.StatusCreated {
		t.Fatalf("create order: %d", rec.Code)
	}
	if code := putStatus(t, "cancelled"); code != http.StatusOK {
		t.Fatalf("cancel: got %d, want 200", code)
	}

	if code := putStatusExpecting(t, "cancelled", "pending"); code != http.StatusOK {
		t.Fatalf("repeat cancel: got %d, want 200", code)
	}

	if n := count(t, `SELECT count(*) FROM order_outbox WHERE event_type = 'order.status_changed'`); n != 1 {
		t.Errorf("status events: got %d, want 1", n)
	}
}
