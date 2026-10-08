package main

// Idempotency-Key on POST /charge.
//
// The database already allows one completed charge per order (ADR 0002). The key adds
// what that rule can't: a retried charge gets back exactly the first answer, including a
// decline. Without it, a charge that was declined could be sent again and approved on the
// second try. The worker sends "charge-order-<id>", so every retry of one order's charge
// uses the same key. Calls without a key keep working as before.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
)

const maxIdempotencyKeyLen = 255

// chargeFingerprint identifies what was asked for, so a key reused for a different
// charge is refused instead of answered with the wrong result.
func chargeFingerprint(orderID int, customerID string, amount float64, currency, method string) string {
	s := fmt.Sprintf("%d|%s|%.2f|%s|%s", orderID, customerID, amount, currency, method)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// replayCharge answers from a stored result when this key was used before. It returns
// true when it has written a response, false when the key is new.
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
