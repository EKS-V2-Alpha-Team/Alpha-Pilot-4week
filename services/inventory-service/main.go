package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lib/pq"
)

var db *sql.DB

type Product struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	SKU       string  `json:"sku"`
	Price     float64 `json:"price"`
	Stock     int     `json:"stock"`
	Reserved  int     `json:"reserved"`
	Available int     `json:"available"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

type Reservation struct {
	ID        int    `json:"id"`
	OrderID   int    `json:"order_id"`
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
	CreatedAt string `json:"created_at"`
}

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
	if getEnv("SEED_DEMO_PRODUCTS", "") == "true" {
		seedDemoProducts()
	}
	startMetricsServer(db, getEnv("METRICS_PORT", "9090"))

	mux := http.NewServeMux()
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/products", handleProducts)
	mux.HandleFunc("/products/", handleProduct)
	mux.HandleFunc("/reserve", handleReserve)
	mux.HandleFunc("/release", handleRelease)
	mux.HandleFunc("/commit", handleCommit)
	mux.HandleFunc("/restock", handleRestock)
	mux.HandleFunc("/low-stock", handleLowStock)

	port := getEnv("PORT", "8082")
	server := &http.Server{
		Addr:         ":" + port,
		Handler: instrument(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Inventory service listening on :%s", port)
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
		`CREATE TABLE IF NOT EXISTS products (
			id VARCHAR(50) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			sku VARCHAR(100) UNIQUE NOT NULL,
			price DECIMAL(10,2) NOT NULL,
			stock INTEGER NOT NULL DEFAULT 0,
			reserved INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_products_sku ON products(sku)`,
		`CREATE TABLE IF NOT EXISTS reservations (
			id SERIAL PRIMARY KEY,
			order_id INTEGER NOT NULL,
			product_id VARCHAR(50) NOT NULL REFERENCES products(id),
			quantity INTEGER NOT NULL,
			status VARCHAR(20) NOT NULL DEFAULT 'active',
			expires_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reservations_order ON reservations(order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_reservations_status ON reservations(status)`,
		// One active reservation per order and product, so a repeated /reserve for the same
		// order (e.g. SQS delivering order.created twice) can't hold stock twice (ALPHA-51).
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_reservations_active_order_product
			ON reservations(order_id, product_id) WHERE status = 'active'`,
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			log.Fatalf("Migration failed: %v", err)
		}
	}
	log.Println("Inventory service migrations complete")
}

// seedDemoProducts adds a small demo catalogue when SEED_DEMO_PRODUCTS=true (dev and
// local compose only; the variable is not set anywhere else). A fresh database has no
// products, and orders for unknown products are refused. Existing products, and any
// stock changes made since, are left alone, so it is safe on every start and with
// several replicas starting together.
func seedDemoProducts() {
	demo := []struct {
		id, name, sku string
		price         float64
		stock         int
	}{
		{"kb-001", "Mechanical Keyboard", "SKU-KB-001", 79.99, 100},
		{"ms-001", "Wireless Mouse", "SKU-MS-001", 29.99, 100},
		{"mon-001", "27 inch Monitor", "SKU-MON-001", 199.00, 50},
		{"hub-001", "USB-C Hub", "SKU-HUB-001", 39.50, 200},
	}
	for _, p := range demo {
		if _, err := db.Exec(
			`INSERT INTO products (id, name, sku, price, stock) VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT DO NOTHING`,
			p.id, p.name, p.sku, p.price, p.stock,
		); err != nil {
			log.Fatalf("Seeding demo product %s failed: %v", p.id, err)
		}
	}
	log.Println("Demo products ensured")
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	if err := db.Ping(); err != nil {
		status = "unhealthy"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status, "service": "inventory-service"})
}

func handleProducts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := db.Query(
			"SELECT id, name, sku, price, stock, reserved, created_at, updated_at FROM products ORDER BY name LIMIT 100",
		)
		if err != nil {
			httpError(w, "query failed", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		products := []Product{}
		for rows.Next() {
			var p Product
			rows.Scan(&p.ID, &p.Name, &p.SKU, &p.Price, &p.Stock, &p.Reserved, &p.CreatedAt, &p.UpdatedAt)
			p.Available = p.Stock - p.Reserved
			products = append(products, p)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(products)

	case http.MethodPost:
		var p struct {
			ID    string  `json:"id"`
			Name  string  `json:"name"`
			SKU   string  `json:"sku"`
			Price float64 `json:"price"`
			Stock int     `json:"stock"`
		}
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			httpError(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if msg := validateNewProduct(p.ID, p.Name, p.SKU, p.Price, p.Stock); msg != "" {
			httpError(w, msg, http.StatusBadRequest)
			return
		}

		_, err := db.Exec(
			"INSERT INTO products (id, name, sku, price, stock) VALUES ($1, $2, $3, $4, $5)",
			p.ID, p.Name, p.SKU, p.Price, p.Stock,
		)
		if err != nil {
			// The database error names tables and constraints, so it stays in the log.
			log.Printf("Create product %q failed: %v", p.ID, err)
			httpError(w, "product could not be created (the id or sku may already exist)", http.StatusConflict)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": p.ID, "status": "created"})

	default:
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// validateNewProduct returns a message for the first problem with a new product, or "".
// Limits match the columns: id VARCHAR(50), name VARCHAR(255), sku VARCHAR(100),
// price DECIMAL(10,2).
func validateNewProduct(id, name, sku string, price float64, stock int) string {
	switch {
	case strings.TrimSpace(id) == "" || len(id) > 50:
		return "id is required (max 50 characters)"
	case strings.TrimSpace(name) == "" || len(name) > 255:
		return "name is required (max 255 characters)"
	case strings.TrimSpace(sku) == "" || len(sku) > 100:
		return "sku is required (max 100 characters)"
	case price <= 0 || price >= 100000000:
		return "price must be greater than 0"
	case stock < 0:
		return "stock cannot be negative"
	}
	return ""
}

func handleProduct(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/products/")
	if id == "" {
		httpError(w, "product id required", http.StatusBadRequest)
		return
	}

	var p Product
	err := db.QueryRow(
		"SELECT id, name, sku, price, stock, reserved, created_at, updated_at FROM products WHERE id = $1",
		id,
	).Scan(&p.ID, &p.Name, &p.SKU, &p.Price, &p.Stock, &p.Reserved, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		httpError(w, "product not found", http.StatusNotFound)
		return
	}
	p.Available = p.Stock - p.Reserved

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

func handleReserve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		OrderID int `json:"order_id"`
		Items   []struct {
			ProductID string `json:"product_id"`
			Quantity  int    `json:"quantity"`
		} `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Idempotent: an order that already holds (or has bought) its stock gets success,
	// not a second hold.
	if existing, ok, err := existingReservationExpiry(req.OrderID); err != nil {
		httpError(w, "reservation lookup failed", http.StatusInternalServerError)
		return
	} else if ok {
		writeReservation(w, http.StatusOK, req.OrderID, existing)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		httpError(w, "transaction failed", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	expiresAt := time.Now().Add(15 * time.Minute)

	for _, item := range req.Items {
		// Check available stock with row lock
		var stock, reserved int
		err := tx.QueryRow(
			"SELECT stock, reserved FROM products WHERE id = $1 FOR UPDATE",
			item.ProductID,
		).Scan(&stock, &reserved)
		if err != nil {
			httpError(w, fmt.Sprintf("product %s not found", item.ProductID), http.StatusNotFound)
			return
		}

		available := stock - reserved
		if available < item.Quantity {
			httpError(w, fmt.Sprintf("insufficient stock for %s: available %d, requested %d",
				item.ProductID, available, item.Quantity), http.StatusConflict)
			return
		}

		// Reserve stock
		if _, err := tx.Exec("UPDATE products SET reserved = reserved + $1, updated_at = NOW() WHERE id = $2",
			item.Quantity, item.ProductID); err != nil {
			httpError(w, "reservation update failed", http.StatusInternalServerError)
			return
		}

		if _, err := tx.Exec(
			"INSERT INTO reservations (order_id, product_id, quantity, status, expires_at) VALUES ($1, $2, $3, 'active', $4)",
			req.OrderID, item.ProductID, item.Quantity, expiresAt,
		); err != nil {
			if isUniqueViolation(err) {
				// A concurrent request for the same order reserved first. Undo ours
				// (including the reserved increment) and report theirs.
				tx.Rollback()
				if existing, ok, err := existingReservationExpiry(req.OrderID); err == nil && ok {
					writeReservation(w, http.StatusOK, req.OrderID, existing)
					return
				}
			}
			httpError(w, "reservation insert failed", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		httpError(w, "reservation failed", http.StatusInternalServerError)
		return
	}

	writeReservation(w, http.StatusCreated, req.OrderID, expiresAt)
}

// existingReservationExpiry returns the earliest expiry of the order's active or
// committed reservations, and ok=false when the order holds no stock. Committed counts
// so a repeated order.created after the sale doesn't hold the stock a second time.
func existingReservationExpiry(orderID int) (time.Time, bool, error) {
	var expiresAt sql.NullTime
	err := db.QueryRow(
		"SELECT MIN(expires_at) FROM reservations WHERE order_id = $1 AND status IN ('active', 'committed')",
		orderID,
	).Scan(&expiresAt)
	return expiresAt.Time, expiresAt.Valid, err
}

func writeReservation(w http.ResponseWriter, code int, orderID int, expiresAt time.Time) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"order_id":   orderID,
		"status":     "reserved",
		"expires_at": expiresAt.Format(time.RFC3339),
	})
}

// isUniqueViolation reports whether err is Postgres error 23505 (unique_violation).
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

func handleRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		OrderID int `json:"order_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		httpError(w, "transaction failed", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Claim the active reservations and read what they held in one statement. A concurrent
	// release waits on these row locks, then finds nothing left to claim, so `reserved` is
	// decremented exactly once (ALPHA-51; the old SELECT-then-UPDATE let both subtract).
	rows, err := tx.Query(
		"UPDATE reservations SET status = 'released' WHERE order_id = $1 AND status = 'active' RETURNING product_id, quantity",
		req.OrderID,
	)
	if err != nil {
		httpError(w, "query failed", http.StatusInternalServerError)
		return
	}

	type item struct {
		productID string
		quantity  int
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.productID, &it.quantity); err != nil {
			rows.Close()
			httpError(w, "scan failed", http.StatusInternalServerError)
			return
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		httpError(w, "iteration failed", http.StatusInternalServerError)
		return
	}

	for _, it := range items {
		if _, err := tx.Exec("UPDATE products SET reserved = reserved - $1, updated_at = NOW() WHERE id = $2",
			it.quantity, it.productID); err != nil {
			httpError(w, "release update failed", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		httpError(w, "commit failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"order_id": req.OrderID,
		"released": len(items),
	})
}

// handleCommit turns an order's held stock into a sale: stock and reserved both drop by
// the quantity and the reservation becomes 'committed', which the scheduler never
// expires. Same claim-then-act pattern as release, so a repeat commits nothing.
func handleCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		OrderID int `json:"order_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		httpError(w, "transaction failed", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	rows, err := tx.Query(
		"UPDATE reservations SET status = 'committed' WHERE order_id = $1 AND status = 'active' RETURNING product_id, quantity",
		req.OrderID,
	)
	if err != nil {
		httpError(w, "commit failed", http.StatusInternalServerError)
		return
	}
	type item struct {
		productID string
		quantity  int
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.productID, &it.quantity); err != nil {
			rows.Close()
			httpError(w, "scan failed", http.StatusInternalServerError)
			return
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		httpError(w, "iteration failed", http.StatusInternalServerError)
		return
	}

	for _, it := range items {
		if _, err := tx.Exec(
			"UPDATE products SET stock = stock - $1, reserved = reserved - $1, updated_at = NOW() WHERE id = $2",
			it.quantity, it.productID,
		); err != nil {
			httpError(w, "commit update failed", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		httpError(w, "commit failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"order_id":  req.OrderID,
		"committed": len(items),
	})
}

// handleRestock puts a cancelled order's sold stock back on the shelf: the reservation
// goes from 'committed' to 'returned' and stock goes up by the quantity. Same
// claim-then-act pattern as commit, so a repeat returns nothing twice.
func handleRestock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		OrderID int `json:"order_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		httpError(w, "transaction failed", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	rows, err := tx.Query(
		"UPDATE reservations SET status = 'returned' WHERE order_id = $1 AND status = 'committed' RETURNING product_id, quantity",
		req.OrderID,
	)
	if err != nil {
		httpError(w, "restock failed", http.StatusInternalServerError)
		return
	}
	type item struct {
		productID string
		quantity  int
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.productID, &it.quantity); err != nil {
			rows.Close()
			httpError(w, "scan failed", http.StatusInternalServerError)
			return
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		httpError(w, "iteration failed", http.StatusInternalServerError)
		return
	}

	for _, it := range items {
		if _, err := tx.Exec(
			"UPDATE products SET stock = stock + $1, updated_at = NOW() WHERE id = $2",
			it.quantity, it.productID,
		); err != nil {
			httpError(w, "restock update failed", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		httpError(w, "restock failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"order_id":  req.OrderID,
		"restocked": len(items),
	})
}

func handleLowStock(w http.ResponseWriter, r *http.Request) {
	threshold := 10
	rows, err := db.Query(
		"SELECT id, name, sku, stock, reserved FROM products WHERE (stock - reserved) < $1 ORDER BY (stock - reserved) ASC",
		threshold,
	)
	if err != nil {
		httpError(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type LowStockItem struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		SKU       string `json:"sku"`
		Stock     int    `json:"stock"`
		Reserved  int    `json:"reserved"`
		Available int    `json:"available"`
	}

	items := []LowStockItem{}
	for rows.Next() {
		var i LowStockItem
		rows.Scan(&i.ID, &i.Name, &i.SKU, &i.Stock, &i.Reserved)
		i.Available = i.Stock - i.Reserved
		items = append(items, i)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
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
