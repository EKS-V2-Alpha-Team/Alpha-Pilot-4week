package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// useFakeInventory points the order-service at a stand-in inventory for one test.
// Unknown ids answer 404, like the real service.
func useFakeInventory(t *testing.T, products map[string]catalogueEntry) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/products/")
		p, ok := products[id]
		if !ok {
			http.Error(w, `{"error":"product not found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(p)
	}))
	old := inventoryURL
	inventoryURL = srv.URL
	t.Cleanup(func() { srv.Close(); inventoryURL = old })
}

func TestPriceComesFromInventoryNotRequest(t *testing.T) {
	useFakeInventory(t, map[string]catalogueEntry{"kb-001": {Price: 25, Available: 10}})

	items, rej := priceItems(context.Background(), []OrderItem{{ProductID: "kb-001", Quantity: 2, Price: 0.01}})
	if rej != nil {
		t.Fatalf("unexpected rejection: %+v", rej)
	}
	if items[0].Price != 25 {
		t.Fatalf("price: got %v, want 25 (the request said 0.01)", items[0].Price)
	}
}

func TestUnknownProductIsRefused(t *testing.T) {
	useFakeInventory(t, map[string]catalogueEntry{})

	_, rej := priceItems(context.Background(), []OrderItem{{ProductID: "nope", Quantity: 1}})
	if rej == nil || rej.status != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %+v", rej)
	}
}

func TestNotEnoughStockIsRefused(t *testing.T) {
	useFakeInventory(t, map[string]catalogueEntry{"kb-001": {Price: 25, Available: 3}})

	_, rej := priceItems(context.Background(), []OrderItem{{ProductID: "kb-001", Quantity: 4}})
	if rej == nil || rej.status != http.StatusConflict {
		t.Fatalf("want 409, got %+v", rej)
	}
}

// Two lines for one product are checked against stock together: 2 + 2 > 3.
func TestRepeatedProductLinesAreCountedTogether(t *testing.T) {
	useFakeInventory(t, map[string]catalogueEntry{"kb-001": {Price: 25, Available: 3}})

	_, rej := priceItems(context.Background(), []OrderItem{
		{ProductID: "kb-001", Quantity: 2},
		{ProductID: "kb-001", Quantity: 2},
	})
	if rej == nil || rej.status != http.StatusConflict {
		t.Fatalf("want 409, got %+v", rej)
	}
}

func TestBadItemsAreRefusedBeforeAnyLookup(t *testing.T) {
	for name, item := range map[string]OrderItem{
		"no product id": {Quantity: 1},
		"zero quantity": {ProductID: "kb-001", Quantity: 0},
		"negative":      {ProductID: "kb-001", Quantity: -1},
	} {
		if _, rej := priceItems(context.Background(), []OrderItem{item}); rej == nil || rej.status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %+v", name, rej)
		}
	}
}

func TestInventoryDownGives503(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing is listening any more
	old := inventoryURL
	inventoryURL = url
	t.Cleanup(func() { inventoryURL = old })

	_, rej := priceItems(context.Background(), []OrderItem{{ProductID: "kb-001", Quantity: 1}})
	if rej == nil || rej.status != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %+v", rej)
	}
}

func TestInventoryServerErrorGives503(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	old := inventoryURL
	inventoryURL = srv.URL
	t.Cleanup(func() { inventoryURL = old })

	_, rej := priceItems(context.Background(), []OrderItem{{ProductID: "kb-001", Quantity: 1}})
	if rej == nil || rej.status != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %+v", rej)
	}
}

func TestRoundMoneyRemovesFloatNoise(t *testing.T) {
	// 3 x 79.99 added up as floats, the figure the live order showed.
	if got := roundMoney(239.96999999999997); got != 239.97 {
		t.Errorf("got %v, want 239.97", got)
	}
	if got := roundMoney(0.1 + 0.2); got != 0.3 {
		t.Errorf("got %v, want 0.3", got)
	}
}
