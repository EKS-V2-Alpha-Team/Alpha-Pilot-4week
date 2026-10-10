package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lib/pq"
)

var db *sql.DB

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	var err error
	db, err = sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	waitForDB()
	migrate()
      	
	startMetricsServer(getEnv("METRICS_PORT", "9090"))
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/charge", handleCharge)
	mux.HandleFunc("/refund", handleRefund)
	mux.HandleFunc("/ledger", handleLedger)
	mux.HandleFunc("/balance/", handleBalance)

	port := getEnv("PORT", "8083")
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      instrument(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Payment service listening on :%s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown error: %v", err)
	}
}

func migrate() {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS payments (
			id VARCHAR(50) PRIMARY KEY,
			order_id INTEGER NOT NULL,
			customer_id VARCHAR(255) NOT NULL,
			amount DECIMAL(12,2) NOT NULL,
			currency VARCHAR(3) NOT NULL DEFAULT 'GBP',
			status VARCHAR(20) NOT NULL DEFAULT 'pending',
			method VARCHAR(50),
			reference VARCHAR(255),
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_payments_order ON payments(order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_payments_customer ON payments(customer_id)`,
		// At most one completed charge per order, so a repeated /charge (e.g. SQS delivering
		// the same message twice) can't take the money twice (ALPHA-51). Refunds are stored
		// as their own completed rows for the same order, so they are left out.
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_payments_one_completed_charge
			ON payments(order_id) WHERE status = 'completed' AND method IS DISTINCT FROM 'refund'`,
		`CREATE TABLE IF NOT EXISTS ledger_entries (
			id SERIAL PRIMARY KEY,
			payment_id VARCHAR(50) NOT NULL REFERENCES payments(id),
			entry_type VARCHAR(20) NOT NULL,
			debit DECIMAL(12,2) NOT NULL DEFAULT 0,
			credit DECIMAL(12,2) NOT NULL DEFAULT 0,
			currency VARCHAR(3) NOT NULL DEFAULT 'GBP',
			description TEXT,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ledger_payment ON ledger_entries(payment_id)`,
		// status_code and response stay NULL while the first request is running.
		`CREATE TABLE IF NOT EXISTS payment_idempotency_keys (
			key VARCHAR(255) PRIMARY KEY,
			request_hash CHAR(64) NOT NULL,
			order_id INTEGER,
			status_code INTEGER,
			response JSONB,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_payment_idempotency_keys_created ON payment_idempotency_keys(created_at)`,
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			log.Fatalf("Migration failed: %v", err)
		}
	}
	log.Println("Payment service migrations complete")
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	if err := db.Ping(); err != nil {
		status = "unhealthy"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status, "service": "payment-service"})
}

func handleCharge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		OrderID    int     `json:"order_id"`
		CustomerID string  `json:"customer_id"`
		Amount     float64 `json:"amount"`
		Currency   string  `json:"currency"`
		Method     string  `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Amount <= 0 {
		httpError(w, "amount must be positive", http.StatusBadRequest)
		return
	}
	if req.Method == "refund" {
		// "refund" marks refund rows; a charge using it would slip past the one-charge rule.
		httpError(w, "method \"refund\" is reserved for refunds", http.StatusBadRequest)
		return
	}

	paymentID := generatePaymentID()
	currency := req.Currency
	if currency == "" {
		currency = "GBP"
	}

	// A key seen before gets the first answer back, decline included.
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > maxIdempotencyKeyLen {
		httpError(w, "Idempotency-Key is too long (255 characters at most)", http.StatusBadRequest)
		return
	}
	fingerprint := chargeFingerprint(req.OrderID, req.CustomerID, req.Amount, currency, req.Method)
	if key != "" && replayCharge(w, key, fingerprint) {
		return
	}

	// Idempotent: an order that is already paid is never charged again.
	if replyIfAlreadyPaid(w, req.OrderID, req.CustomerID, req.Amount, currency) {
		return
	}

	status := processPayment()

	tx, err := db.Begin()
	if err != nil {
		httpError(w, "transaction failed", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Claim the key in the payment's transaction; a concurrent request waits for it.
	if key != "" {
		claim, err := tx.Exec(
			`INSERT INTO payment_idempotency_keys (key, request_hash, order_id) VALUES ($1, $2, $3)
			 ON CONFLICT (key) DO NOTHING`, key, fingerprint, req.OrderID)
		if err != nil {
			httpError(w, "failed to process payment", http.StatusInternalServerError)
			return
		}
		if n, _ := claim.RowsAffected(); n == 0 {
			tx.Rollback()
			if !replayCharge(w, key, fingerprint) {
				httpError(w, "a charge with this Idempotency-Key is still in progress; retry shortly", http.StatusConflict)
			}
			return
		}
	}

	_, err = tx.Exec(
		`INSERT INTO payments (id, order_id, customer_id, amount, currency, status, method)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		paymentID, req.OrderID, req.CustomerID, req.Amount, currency, status, req.Method,
	)
	if err != nil {
		if isUniqueViolation(err) {
			// A concurrent request for the same order completed first. Ours rolls back,
			// so nothing is charged twice; answer the way a repeat would be answered.
			tx.Rollback()
			if replyIfAlreadyPaid(w, req.OrderID, req.CustomerID, req.Amount, currency) {
				return
			}
		}
		httpError(w, "failed to process payment", http.StatusInternalServerError)
		return
	}

	if status == "completed" {
		// Double-entry: debit customer, credit revenue
		tx.Exec(
			`INSERT INTO ledger_entries (payment_id, entry_type, debit, currency, description)
			 VALUES ($1, 'charge', $2, $3, $4)`,
			paymentID, req.Amount, currency, fmt.Sprintf("Order #%d payment", req.OrderID),
		)
		tx.Exec(
			`INSERT INTO ledger_entries (payment_id, entry_type, credit, currency, description)
			 VALUES ($1, 'revenue', $2, $3, $4)`,
			paymentID, req.Amount, currency, fmt.Sprintf("Order #%d revenue", req.OrderID),
		)
	}

	code := http.StatusCreated
	if status == "failed" {
		code = http.StatusPaymentRequired
	}
	resp, _ := json.Marshal(map[string]interface{}{
		"payment_id": paymentID,
		"order_id":   req.OrderID,
		"amount":     req.Amount,
		"currency":   currency,
		"status":     status,
	})
	if key != "" {
		if _, err := tx.Exec(
			`UPDATE payment_idempotency_keys SET status_code = $2, response = $3 WHERE key = $1`,
			key, code, string(resp),
		); err != nil {
			httpError(w, "failed to process payment", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		// Before this check the handler answered 201 and published payment.completed
		// even when nothing was saved.
		httpError(w, "failed to process payment", http.StatusInternalServerError)
		return
	}

	// Publish event
	publishEvent("payment."+status, map[string]interface{}{
		"payment_id":  paymentID,
		"order_id":    req.OrderID,
		"customer_id": req.CustomerID,
		"amount":      req.Amount,
		"currency":    currency,
		"status":      status,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(resp)
}

// processPayment simulates the payment provider (90% success rate).
// It is a variable so tests can make the outcome deterministic.
var processPayment = func() string {
	if rand.Intn(10) == 0 {
		return "failed"
	}
	return "completed"
}

// replyIfAlreadyPaid answers for an order that already has a completed charge and
// reports whether it did. Same customer, amount and currency means the request is a
// repeat: 200 with the existing payment. Anything different is a caller bug: 409.
func replyIfAlreadyPaid(w http.ResponseWriter, orderID int, customerID string, amount float64, currency string) bool {
	var paymentID, paidCustomer, paidCurrency string
	var paidAmount float64
	err := db.QueryRow(
		`SELECT id, customer_id, amount, currency FROM payments
		 WHERE order_id = $1 AND status = 'completed' AND method IS DISTINCT FROM 'refund'`,
		orderID,
	).Scan(&paymentID, &paidCustomer, &paidAmount, &paidCurrency)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		httpError(w, "payment lookup failed", http.StatusInternalServerError)
		return true
	}

	w.Header().Set("Content-Type", "application/json")
	if paidCustomer != customerID || paidCurrency != currency || math.Abs(paidAmount-amount) >= 0.005 {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":      "order already paid with different details",
			"payment_id": paymentID,
			"order_id":   orderID,
		})
		return true
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"payment_id":   paymentID,
		"order_id":     orderID,
		"amount":       paidAmount,
		"currency":     paidCurrency,
		"status":       "completed",
		"already_paid": true,
	})
	return true
}

// isUniqueViolation reports whether err is Postgres error 23505 (unique_violation).
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

// handleRefund refunds a completed charge, named either by payment_id or by order_id
// (the worker refunds by order when a paid order is cancelled).
//
// Safe to repeat and to run twice at once: the charge row is locked for the whole
// refund, so a second request waits and then sees the charge already refunded. By
// order_id that is a 200 ("already_refunded"); by payment_id it stays a 409.
func handleRefund(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PaymentID string  `json:"payment_id"`
		OrderID   int     `json:"order_id"`
		Amount    float64 `json:"amount"`
		Reason    string  `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.PaymentID == "" && req.OrderID <= 0 {
		httpError(w, "payment_id or order_id is required", http.StatusBadRequest)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		httpError(w, "transaction failed", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Find the original charge and lock it. Refund rows are payments too, so they are
	// excluded: a refund cannot be refunded.
	var paymentID, paymentStatus, customerID, currency string
	var originalAmount float64
	var orderID int
	if req.PaymentID != "" {
		err = tx.QueryRow(
			`SELECT id, amount, order_id, status, customer_id, currency FROM payments
			 WHERE id = $1 AND method IS DISTINCT FROM 'refund' FOR UPDATE`,
			req.PaymentID,
		).Scan(&paymentID, &originalAmount, &orderID, &paymentStatus, &customerID, &currency)
	} else {
		err = tx.QueryRow(
			`SELECT id, amount, order_id, status, customer_id, currency FROM payments
			 WHERE order_id = $1 AND method IS DISTINCT FROM 'refund'
			   AND status IN ('completed', 'partially_refunded', 'refunded') FOR UPDATE`,
			req.OrderID,
		).Scan(&paymentID, &originalAmount, &orderID, &paymentStatus, &customerID, &currency)
	}
	if errors.Is(err, sql.ErrNoRows) {
		httpError(w, "no charge found to refund", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("Refund lookup failed: %v", err)
		httpError(w, "refund failed", http.StatusInternalServerError)
		return
	}

	if paymentStatus != "completed" {
		if req.PaymentID == "" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"order_id":   orderID,
				"payment_id": paymentID,
				"status":     "already_refunded",
			})
			return
		}
		httpError(w, "can only refund completed payments", http.StatusConflict)
		return
	}

	refundAmount := req.Amount
	if refundAmount <= 0 || refundAmount > originalAmount {
		refundAmount = originalAmount
	}

	refundID := generatePaymentID()
	reason := req.Reason

	if _, err := tx.Exec(
		`INSERT INTO payments (id, order_id, customer_id, amount, currency, status, method, reference)
		 VALUES ($1, $2, $3, $4, $5, 'completed', 'refund', $6)`,
		refundID, orderID, customerID, refundAmount, currency, paymentID,
	); err != nil {
		log.Printf("Refund insert failed: %v", err)
		httpError(w, "refund failed", http.StatusInternalServerError)
		return
	}

	// Double-entry, the mirror image of a charge (item 20): the customer is credited and
	// revenue is debited by the same amount, so the ledger still balances.
	if _, err := tx.Exec(
		`INSERT INTO ledger_entries (payment_id, entry_type, credit, currency, description)
		 VALUES ($1, 'refund', $2, $3, $4)`,
		refundID, refundAmount, currency, fmt.Sprintf("Order #%d refund: %s", orderID, reason),
	); err != nil {
		log.Printf("Refund ledger (customer side) failed: %v", err)
		httpError(w, "refund failed", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(
		`INSERT INTO ledger_entries (payment_id, entry_type, debit, currency, description)
		 VALUES ($1, 'revenue_reversal', $2, $3, $4)`,
		refundID, refundAmount, currency, fmt.Sprintf("Order #%d revenue reversed", orderID),
	); err != nil {
		log.Printf("Refund ledger (revenue side) failed: %v", err)
		httpError(w, "refund failed", http.StatusInternalServerError)
		return
	}

	newStatus := "partially_refunded"
	if refundAmount >= originalAmount {
		newStatus = "refunded"
	}
	if _, err := tx.Exec("UPDATE payments SET status = $1, updated_at = NOW() WHERE id = $2", newStatus, paymentID); err != nil {
		log.Printf("Refund status update failed: %v", err)
		httpError(w, "refund failed", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		httpError(w, "refund failed", http.StatusInternalServerError)
		return
	}

	publishEvent("payment.refunded", map[string]interface{}{
		"refund_id":  refundID,
		"payment_id": paymentID,
		"order_id":   orderID,
		"amount":     refundAmount,
		"reason":     reason,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"refund_id":  refundID,
		"payment_id": paymentID,
		"order_id":   orderID,
		"amount":     refundAmount,
		"status":     "completed",
	})
}

func handleLedger(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(
		`SELECT le.id, le.payment_id, le.entry_type, le.debit, le.credit, le.currency, le.description, le.created_at
		 FROM ledger_entries le ORDER BY le.created_at DESC LIMIT 100`,
	)
	if err != nil {
		httpError(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type Entry struct {
		ID          int     `json:"id"`
		PaymentID   string  `json:"payment_id"`
		EntryType   string  `json:"entry_type"`
		Debit       float64 `json:"debit"`
		Credit      float64 `json:"credit"`
		Currency    string  `json:"currency"`
		Description string  `json:"description"`
		CreatedAt   string  `json:"created_at"`
	}

	entries := []Entry{}
	for rows.Next() {
		var e Entry
		rows.Scan(&e.ID, &e.PaymentID, &e.EntryType, &e.Debit, &e.Credit, &e.Currency, &e.Description, &e.CreatedAt)
		entries = append(entries, e)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func handleBalance(w http.ResponseWriter, r *http.Request) {
	customerID := strings.TrimPrefix(r.URL.Path, "/balance/")
	if customerID == "" {
		httpError(w, "customer id required", http.StatusBadRequest)
		return
	}

	var totalCharged, totalRefunded float64
	db.QueryRow(
		"SELECT COALESCE(SUM(amount), 0) FROM payments WHERE customer_id = $1 AND status IN ('completed', 'partially_refunded', 'refunded') AND (method IS NULL OR method != 'refund')",
		customerID,
	).Scan(&totalCharged)

	db.QueryRow(
		"SELECT COALESCE(SUM(amount), 0) FROM payments WHERE customer_id = $1 AND method = 'refund'",
		customerID,
	).Scan(&totalRefunded)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"customer_id":    customerID,
		"total_charged":  totalCharged,
		"total_refunded": totalRefunded,
		"net":            totalCharged - totalRefunded,
	})
}

func generatePaymentID() string {
	return fmt.Sprintf("pay_%d_%d", time.Now().UnixNano(), rand.Intn(10000))
}

func publishEvent(eventType string, payload map[string]interface{}) {
	sqsQueue := os.Getenv("SQS_QUEUE_URL")
	if sqsQueue == "" {
		log.Printf("Event (no SQS): %s %v", eventType, payload)
		return
	}
	event := map[string]interface{}{
		"type":      eventType,
		"payload":   payload,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(event)
	log.Printf("Event (logged only): %s", string(data))
}

func httpError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func waitForDB() {
	for i := 0; i < 120; i++ {
		if err := db.Ping(); err == nil {
			return
		}
		log.Printf("Waiting for database... (%d/120)", i+1)
		time.Sleep(time.Second)
	}
	log.Fatal("Database not ready after 120s")
}
