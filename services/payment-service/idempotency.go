package main

// Idempotency-Key handling for POST /charge: a retry gets the first answer back.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
)

const maxIdempotencyKeyLen = 255

// chargeFingerprint identifies the charge a key was first used for.
func chargeFingerprint(orderID int, customerID string, amount float64, currency, method string) string {
	s := fmt.Sprintf("%d|%s|%.2f|%s|%s", orderID, customerID, amount, currency, method)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// replayCharge writes the stored answer for a known key and reports whether it did.
func replayCharge(w http.ResponseWriter, key, fingerprint string) bool {
	var hash string
	var code sql.NullInt64
	var body []byte
	err := db.QueryRow(
		`SELECT request_hash, status_code, response FROM payment_idempotency_keys WHERE key = $1`, key,
	).Scan(&hash, &code, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		httpError(w, "idempotency lookup failed", http.StatusInternalServerError)
		return true
	}
	if hash != fingerprint {
		httpError(w, "Idempotency-Key was already used for a different charge", http.StatusUnprocessableEntity)
		return true
	}
	if !code.Valid {
		httpError(w, "a charge with this Idempotency-Key is still in progress; retry shortly", http.StatusConflict)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Idempotent-Replayed", "true")
	w.WriteHeader(int(code.Int64))
	w.Write(body)
	return true
}
