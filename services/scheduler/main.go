package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
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

	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
	waitForDB()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startMetricsServer(getEnv("METRICS_PORT", "9090"))

	// Health check
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			status := "ok"
			if err := db.Ping(); err != nil {
				status = "unhealthy"
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": status, "service": "scheduler"})
		})
		port := getEnv("HEALTH_PORT", "8091")
		log.Printf("Scheduler health check on :%s", port)
		http.ListenAndServe(":"+port, mux)
	}()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Shutting down scheduler...")
		cancel()
	}()

	log.Println("Scheduler started")

	// Run scheduled jobs
	go runEvery(ctx, 1*time.Minute, "expire_reservations", expireReservations)
	go runEvery(ctx, 5*time.Minute, "abandoned_carts", detectAbandonedOrders)
	go runEvery(ctx, 15*time.Minute, "retry_failed_payments", retryFailedPayments)
	go runEvery(ctx, 1*time.Hour, "daily_digest", generateDigest)
	go runEvery(ctx, 30*time.Minute, "cleanup_old_events", cleanupOldEvents)

	<-ctx.Done()
	log.Println("Scheduler stopped")
}

func runEvery(ctx context.Context, interval time.Duration, name string, fn func()) {
	log.Printf("Scheduled job '%s' every %s", name, interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run immediately on start
	log.Printf("Running job: %s", name)
	runJob(name, fn) 

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			log.Printf("Running job: %s", name)
			start := time.Now()
			runJob(name, fn) 
			log.Printf("Job '%s' completed in %s", name, time.Since(start))
		}
	}
}

// expireReservations puts back stock that was held for an order that never went through.
//
// The reservation is claimed first (active -> expired) and the stock is only returned if
// that claim really changed a row, inside one transaction. The inventory-service's commit
// and release use the same claim-then-act pattern on the same rows, so if an order is
// committed or released at the very moment its hold times out, exactly one of them wins
// and the stock is never given back twice.
func expireReservations() {
	rows, err := db.Query(
		`SELECT id FROM reservations WHERE status = 'active' AND expires_at < NOW() ORDER BY id LIMIT 500`,
	)
	if err != nil {
		log.Printf("expire_reservations query error: %v", err)
		return
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			log.Printf("expire_reservations scan error: %v", err)
			continue
		}
		ids = append(ids, id)
	}
	rows.Close()

	count := 0
	for _, id := range ids {
		expired, err := expireReservation(id)
		if err != nil {
			log.Printf("expire_reservations: reservation %d: %v", id, err)
			continue
		}
		if expired {
			count++
		}
	}

	if count > 0 {
		log.Printf("Expired %d reservations", count)
	}
}

// expireReservation expires one reservation if it is still active and past its time, and
// returns its stock. It reports false when someone else got there first (committed,
// released, or already expired), in which case nothing is changed.
func expireReservation(id int) (bool, error) {
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var productID string
	var quantity int
	err = tx.QueryRow(
		`UPDATE reservations SET status = 'expired'
		 WHERE id = $1 AND status = 'active' AND expires_at < NOW()
		 RETURNING product_id, quantity`, id,
	).Scan(&productID, &quantity)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(
		`UPDATE products SET reserved = GREATEST(reserved - $1, 0), updated_at = NOW() WHERE id = $2`,
		quantity, productID,
	); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// An order that stays pending this long was never finished by the worker.
const abandonedAfter = 30 * time.Minute

// One run handles at most this many orders, so a backlog or a slow order-service
// cannot hold the job for long. The next run picks up the rest.
const abandonedBatch = 100

var (
	orderServiceURL = getEnv("ORDER_SERVICE_URL", "http://order-service:8081")
	orderClient     = &http.Client{Timeout: 10 * time.Second}
)

// detectAbandonedOrders cancels orders stuck in pending for too long.
func detectAbandonedOrders() {
	n := cancelAbandonedOrders(orderClient, orderServiceURL, abandonedAfter)
	if n > 0 {
		log.Printf("Cancelled %d abandoned orders", n)
	}
}

// cancelAbandonedOrders asks order-service to cancel each pending order that is older
// than age, and returns how many it cancelled. The scheduler never writes to the orders
// table itself: order-service owns the status rules, and the cancel event it queues is
// what makes the worker give the stock and the money back.
//
// The age is worked out inside the database (NOW() minus an interval), not from this
// process's clock. created_at is a TIMESTAMP without a time zone, written by the
// database's NOW(). Comparing it with a Go time.Time makes Postgres drop the zone and
// compare wall clocks, which is wrong by the offset whenever this process and the
// database are not in the same time zone (a laptop in UTC+3 against a UTC container).
func cancelAbandonedOrders(client *http.Client, baseURL string, age time.Duration) int {
	rows, err := db.Query(
		`SELECT id FROM orders
		 WHERE status = 'pending' AND created_at < NOW() - make_interval(secs => $1)
		 ORDER BY created_at
		 LIMIT $2`,
		age.Seconds(), abandonedBatch,
	)
	if err != nil {
		log.Printf("abandoned_carts query error: %v", err)
		return 0
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			log.Printf("abandoned_carts scan error: %v", err)
			continue
		}
		ids = append(ids, id)
	}
	rows.Close()

	cancelled := 0
	for _, id := range ids {
		changed, err := cancelOrder(client, baseURL, id)
		if err != nil {
			log.Printf("abandoned_carts: order #%d not cancelled: %v", id, err)
			if errors.Is(err, errOrderServiceDown) {
				// The rest would fail the same way. Try again on the next run.
				break
			}
			continue
		}
		if changed {
			log.Printf("Cancelled abandoned order #%d", id)
			cancelled++
		}
	}
	return cancelled
}

var errOrderServiceDown = errors.New("order-service not available")

// cancelOrder asks order-service to move one order from pending to cancelled.
// expected_status makes it a compare-and-set: if the worker confirmed the order in the
// meantime, order-service answers 409 and nothing changes. It reports whether the order
// was changed now (false when it was already cancelled).
func cancelOrder(client *http.Client, baseURL string, orderID int) (bool, error) {
	body, err := json.Marshal(map[string]interface{}{
		"order_id":        orderID,
		"new_status":      "cancelled",
		"expected_status": "pending",
	})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequest(http.MethodPut, baseURL+"/status", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: %v", errOrderServiceDown, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		var out struct {
			Changed bool `json:"changed"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		return out.Changed, nil
	case resp.StatusCode >= 500:
		return false, fmt.Errorf("%w: status %d", errOrderServiceDown, resp.StatusCode)
	default:
		// 409: the order moved on (confirmed, or cancelled by someone else). 404: gone.
		// Neither is ours to force.
		return false, fmt.Errorf("order-service answered %d", resp.StatusCode)
	}
}

// retryFailedPayments attempts to re-process failed payments
func retryFailedPayments() {
	// The hour is counted by the database, not passed in from this process's clock.
	var count int
	db.QueryRow(
		`SELECT COUNT(*) FROM payments
		 WHERE status = 'failed' AND created_at > NOW() - make_interval(secs => $1)`,
		time.Hour.Seconds(),
	).Scan(&count)

	if count > 0 {
		log.Printf("Found %d failed payments eligible for retry", count)
		// In production: re-queue payment attempts via SQS
		// POST to payment-service/charge for each
	}
}

// generateDigest creates summary stats
func generateDigest() {
	var totalOrders, pendingOrders, completedOrders int
	var totalRevenue float64

	// "Today" is the database's own day (CURRENT_DATE), the same clock that stamped the rows.
	db.QueryRow("SELECT COUNT(*) FROM orders WHERE created_at >= CURRENT_DATE").Scan(&totalOrders)
	db.QueryRow("SELECT COUNT(*) FROM orders WHERE status = 'pending'").Scan(&pendingOrders)
	db.QueryRow("SELECT COUNT(*) FROM orders WHERE status = 'delivered' AND updated_at >= CURRENT_DATE").Scan(&completedOrders)
	db.QueryRow("SELECT COALESCE(SUM(amount), 0) FROM payments WHERE status = 'completed' AND created_at >= CURRENT_DATE").Scan(&totalRevenue)

	log.Printf("Daily digest - Orders today: %d, Pending: %d, Completed: %d, Revenue: %.2f",
		totalOrders, pendingOrders, completedOrders, totalRevenue)

	// In production: send digest via notification-service
}

// cleanupOldEvents removes old tracking/audit data
func cleanupOldEvents() {
	pruneOldRows(db, retentionRules)
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
