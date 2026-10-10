package main

// Idempotency keys for POST /orders.
//
// A client that times out cannot tell whether its order was saved, so it retries, and
// without a key a retry is a second order. The client sends an Idempotency-Key header
// (one fresh value per "place this order" action, reused on every retry of it). The
// first request with a key creates the order; any later request with the same key from
// the same customer gets that same order back instead of a new one.
//
// The key is claimed inside the same transaction that saves the order. The unique
// (customer_id, key) index is what makes two simultaneous requests safe: the second
// INSERT waits for the first transaction to finish, then finds the key taken.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
)

const maxIdempotencyKeyLen = 255

// requestFingerprint is a hash of what the customer asked for (products, quantities,
// currency, notes). The client's own price is left out because it is ignored anyway.
// It lets us tell "the same request again" from "a different request reusing the key".
func requestFingerprint(items []OrderItem, currency, notes string) string {
	type line struct {
		ProductID string `json:"p"`
		Quantity  int    `json:"q"`
	}
	canonical := struct {
		Items    []line `json:"items"`
		Currency string `json:"currency"`
		Notes    string `json:"notes"`
	}{Currency: currency, Notes: notes}
	for _, it := range items {
		canonical.Items = append(canonical.Items, line{it.ProductID, it.Quantity})
	}
	b, _ := json.Marshal(canonical)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// replayIfSeen answers the request itself when this customer already used this key.
// It returns true when a response has been written (replay, mismatch or error), and
// false when the key is new and the caller should carry on creating the order.
func replayIfSeen(w http.ResponseWriter, customerID, key, fingerprint string) bool {
	var storedFingerprint string
	var orderID int
	err := db.QueryRow(
		`SELECT request_hash, order_id FROM idempotency_keys WHERE customer_id = $1 AND key = $2`,
		customerID, key,
	).Scan(&storedFingerprint, &orderID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		log.Printf("Idempotency lookup error: %v", err)
		httpError(w, "failed to create order", http.StatusInternalServerError)
		return true
	}

	if storedFingerprint != fingerprint {
		httpError(w, "Idempotency-Key was already used for a different order; use a new key", http.StatusUnprocessableEntity)
		return true
	}

	var status string
	var total float64
	if err := db.QueryRow(`SELECT status, total FROM orders WHERE id = $1`, orderID).Scan(&status, &total); err != nil {
		log.Printf("Idempotency replay error: order %d: %v", orderID, err)
		httpError(w, "failed to create order", http.StatusInternalServerError)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Idempotent-Replayed", "true")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{"id": orderID, "status": status, "total": total})
	return true
}
