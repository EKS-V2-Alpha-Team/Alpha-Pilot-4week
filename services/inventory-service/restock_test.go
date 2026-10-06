package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
)

// A cancelled order that was already sold gets its stock back, exactly once.
func TestRestockReturnsSoldStockOnce(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 2))
	post(t, handleCommit, map[string]int{"order_id": 1}) // stock 5 -> 3

	rec := post(t, handleRestock, map[string]int{"order_id": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("restock: got %d, want 200: %s", rec.Code, rec.Body)
	}
	if stock := stockCount(t); stock != 5 {
		t.Fatalf("stock after restock = %d, want 5", stock)
	}

	// A redelivered cancel event asks again: nothing more comes back.
	rec = post(t, handleRestock, map[string]int{"order_id": 1})
	var resp struct {
		Restocked int `json:"restocked"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	if rec.Code != http.StatusOK || resp.Restocked != 0 {
		t.Errorf("repeat restock: got %d restocked=%d, want 200 restocked=0", rec.Code, resp.Restocked)
	}
	if stock := stockCount(t); stock != 5 {
		t.Errorf("stock after repeat = %d, want 5", stock)
	}
}

// Stock that was only held (never sold) is not touched by restock; release handles it.
func TestRestockLeavesHeldStockAlone(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 2))

	post(t, handleRestock, map[string]int{"order_id": 1})
	if stock, reserved := stockCount(t), reservedCount(t); stock != 5 || reserved != 2 {
		t.Errorf("stock=%d reserved=%d, want stock=5 reserved=2 (hold untouched)", stock, reserved)
	}
}

func TestConcurrentRestocksReturnStockOnce(t *testing.T) {
	setupTestDB(t)
	post(t, handleReserve, reserveBody(1, 2))
	post(t, handleCommit, map[string]int{"order_id": 1})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			post(t, handleRestock, map[string]int{"order_id": 1})
		}()
	}
	wg.Wait()
	if stock := stockCount(t); stock != 5 {
		t.Errorf("stock = %d, want 5 (returned once, not once per request)", stock)
	}
}
