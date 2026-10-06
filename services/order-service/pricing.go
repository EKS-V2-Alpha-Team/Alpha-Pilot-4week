package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"time"
)

// Prices and stock come from inventory, never from the request (ALPHA-50).
// An order for a product inventory does not know is refused here, instead of being
// accepted and cancelled by the worker a few seconds later.
var (
	inventoryURL    = getEnv("INVENTORY_SERVICE_URL", "http://inventory-service:8082")
	inventoryClient = &http.Client{Timeout: 3 * time.Second}
)

// orderRejection is a refusal that should reach the caller with this status and message.
type orderRejection struct {
	status int
	msg    string
}

type catalogueEntry struct {
	Price     float64 `json:"price"`
	Available int     `json:"available"`
}

// priceItems checks every item against inventory and returns the items with the
// catalogue price filled in. The price sent in the request is ignored.
func priceItems(ctx context.Context, items []OrderItem) ([]OrderItem, *orderRejection) {
	// Quantity per product across all lines, so two lines for one product are
	// checked against stock together.
	wanted := map[string]int{}
	for _, item := range items {
		if item.ProductID == "" {
			return nil, &orderRejection{http.StatusBadRequest, "every item needs a product_id"}
		}
		if item.Quantity < 1 {
			return nil, &orderRejection{http.StatusBadRequest, fmt.Sprintf("quantity for %s must be at least 1", item.ProductID)}
		}
		wanted[item.ProductID] += item.Quantity
	}

	catalogue := map[string]catalogueEntry{}
	for id, qty := range wanted {
		entry, rej := lookupProduct(ctx, id)
		if rej != nil {
			return nil, rej
		}
		// Early answer only: the worker's reserve step is what actually holds the stock.
		if entry.Available < qty {
			return nil, &orderRejection{http.StatusConflict, fmt.Sprintf("not enough stock for %s (available: %d)", id, entry.Available)}
		}
		catalogue[id] = entry
	}

	priced := make([]OrderItem, len(items))
	for i, item := range items {
		priced[i] = OrderItem{ProductID: item.ProductID, Quantity: item.Quantity, Price: catalogue[item.ProductID].Price}
	}
	return priced, nil
}

func lookupProduct(ctx context.Context, id string) (catalogueEntry, *orderRejection) {
	unavailable := &orderRejection{http.StatusServiceUnavailable, "inventory is unavailable, try again shortly"}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, inventoryURL+"/products/"+url.PathEscape(id), nil)
	if err != nil {
		log.Printf("Inventory lookup for %q: build request: %v", id, err)
		return catalogueEntry{}, unavailable
	}
	resp, err := inventoryClient.Do(req)
	if err != nil {
		log.Printf("Inventory lookup for %q failed: %v", id, err)
		return catalogueEntry{}, unavailable
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var entry catalogueEntry
		if err := json.NewDecoder(resp.Body).Decode(&entry); err != nil {
			log.Printf("Inventory lookup for %q: bad response: %v", id, err)
			return catalogueEntry{}, unavailable
		}
		return entry, nil
	case http.StatusNotFound:
		return catalogueEntry{}, &orderRejection{http.StatusUnprocessableEntity, fmt.Sprintf("unknown product: %s", id)}
	default:
		log.Printf("Inventory lookup for %q: status %d", id, resp.StatusCode)
		return catalogueEntry{}, unavailable
	}
}

// roundMoney rounds to whole pence, so the total shown, stored, sent to payment and
// emailed is the same figure.
func roundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}
