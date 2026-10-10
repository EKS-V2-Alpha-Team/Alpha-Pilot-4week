package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
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

	db.SetMaxOpenConns(15)
	db.SetMaxIdleConns(3)
	db.SetConnMaxLifetime(5 * time.Minute)
	waitForDB()
	migrate()

    startMetricsServer(getEnv("METRICS_PORT", "9090"))  
	mux := http.NewServeMux()
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/shipments", handleShipments)
	mux.HandleFunc("/shipments/", handleShipment)
	mux.HandleFunc("/track/", handleTrack)
	mux.HandleFunc("/webhook", handleCarrierWebhook)

	port := getEnv("PORT", "8085")
	server := &http.Server{
		Addr:         ":" + port,
		Handler:      instrument(mux), 
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("Shipping service listening on :%s", port)
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
		`CREATE TABLE IF NOT EXISTS shipments (
			id SERIAL PRIMARY KEY,
			order_id INTEGER NOT NULL,
			carrier VARCHAR(50) NOT NULL,
			tracking_number VARCHAR(100) UNIQUE,
			status VARCHAR(30) NOT NULL DEFAULT 'label_created',
			recipient_name VARCHAR(255),
			address_line1 TEXT,
			address_line2 TEXT,
			city VARCHAR(100),
			postcode VARCHAR(20),
			country VARCHAR(2) DEFAULT 'GB',
			weight_kg DECIMAL(8,2),
			estimated_delivery DATE,
			shipped_at TIMESTAMP,
			delivered_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_shipments_order ON shipments(order_id)`,
		// One shipment per order. This is what makes creating a shipment safe to repeat.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_shipments_order_unique ON shipments(order_id)`,
		`CREATE INDEX IF NOT EXISTS idx_shipments_tracking ON shipments(tracking_number)`,
		`CREATE TABLE IF NOT EXISTS tracking_events (
			id SERIAL PRIMARY KEY,
			shipment_id INTEGER NOT NULL REFERENCES shipments(id),
			status VARCHAR(30) NOT NULL,
			location VARCHAR(255),
			description TEXT,
			occurred_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tracking_shipment ON tracking_events(shipment_id)`,
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			log.Fatalf("Migration failed: %v", err)
		}
	}
	log.Println("Shipping service migrations complete")
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	if err := db.Ping(); err != nil {
		status = "unhealthy"
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status, "service": "shipping-service"})
}

func handleShipments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := db.Query(
			`SELECT id, order_id, carrier, tracking_number, status, recipient_name,
			        city, country, estimated_delivery, created_at
			 FROM shipments ORDER BY created_at DESC LIMIT 100`,
		)
		if err != nil {
			httpError(w, "query failed", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		type ShipmentSummary struct {
			ID                int    `json:"id"`
			OrderID           int    `json:"order_id"`
			Carrier           string `json:"carrier"`
			TrackingNumber    string `json:"tracking_number"`
			Status            string `json:"status"`
			RecipientName     string `json:"recipient_name"`
			City              string `json:"city"`
			Country           string `json:"country"`
			EstimatedDelivery string `json:"estimated_delivery,omitempty"`
			CreatedAt         string `json:"created_at"`
		}

		shipments := []ShipmentSummary{}
		for rows.Next() {
			var s ShipmentSummary
			var estDel, tracking sql.NullString
			rows.Scan(&s.ID, &s.OrderID, &s.Carrier, &tracking, &s.Status, &s.RecipientName,
				&s.City, &s.Country, &estDel, &s.CreatedAt)
			if tracking.Valid {
				s.TrackingNumber = tracking.String
			}
			if estDel.Valid {
				s.EstimatedDelivery = estDel.String
			}
			shipments = append(shipments, s)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(shipments)

	case http.MethodPost:
		createShipment(w, r)

	default:
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func createShipment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrderID       int     `json:"order_id"`
		Carrier       string  `json:"carrier"`
		RecipientName string  `json:"recipient_name"`
		AddressLine1  string  `json:"address_line1"`
		AddressLine2  string  `json:"address_line2"`
		City          string  `json:"city"`
		Postcode      string  `json:"postcode"`
		Country       string  `json:"country"`
		WeightKg      float64 `json:"weight_kg"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.OrderID == 0 || req.RecipientName == "" {
		httpError(w, "order_id and recipient_name required", http.StatusBadRequest)
		return
	}

	carrier := req.Carrier
	if carrier == "" {
		carrier = "royal_mail"
	}
	country := req.Country
	if country == "" {
		country = "GB"
	}

	trackingNumber := generateTrackingNumber(carrier)
	estimatedDelivery := time.Now().AddDate(0, 0, 3+rand.Intn(5))

	// Shipment and first tracking event go in together or not at all.
	tx, err := db.Begin()
	if err != nil {
		log.Printf("create shipment: begin: %v", err)
		httpError(w, "failed to create shipment", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// One shipment per order. A second request for the same order (a retried SQS
	// message) inserts nothing and gets the existing shipment back.
	var shipmentID int
	err = tx.QueryRow(
		`INSERT INTO shipments (order_id, carrier, tracking_number, recipient_name,
		 address_line1, address_line2, city, postcode, country, weight_kg, estimated_delivery)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (order_id) DO NOTHING RETURNING id`,
		req.OrderID, carrier, trackingNumber, req.RecipientName,
		req.AddressLine1, req.AddressLine2, req.City, req.Postcode, country,
		req.WeightKg, estimatedDelivery,
	).Scan(&shipmentID)

	if err == sql.ErrNoRows {
		tx.Rollback()
		var existingTracking sql.NullString
		var existingCarrier string
		var existingEstimate sql.NullTime
		err = db.QueryRow(
			`SELECT id, tracking_number, carrier, estimated_delivery FROM shipments WHERE order_id = $1`,
			req.OrderID,
		).Scan(&shipmentID, &existingTracking, &existingCarrier, &existingEstimate)
		if err != nil {
			log.Printf("create shipment: read existing for order %d: %v", req.OrderID, err)
			httpError(w, "failed to create shipment", http.StatusInternalServerError)
			return
		}
		resp := map[string]interface{}{
			"shipment_id":     shipmentID,
			"tracking_number": existingTracking.String,
			"carrier":         existingCarrier,
			"already_created": true,
		}
		if existingEstimate.Valid {
			resp["estimated_delivery"] = existingEstimate.Time.Format("2006-01-02")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}
	if err != nil {
		log.Printf("create shipment: insert: %v", err)
		httpError(w, "failed to create shipment", http.StatusInternalServerError)
		return
	}

	// Initial tracking event
	if _, err := tx.Exec(
		`INSERT INTO tracking_events (shipment_id, status, location, description, occurred_at)
		 VALUES ($1, 'label_created', $2, 'Shipping label created', NOW())`,
		shipmentID, req.City,
	); err != nil {
		log.Printf("create shipment: tracking event: %v", err)
		httpError(w, "failed to create shipment", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("create shipment: commit: %v", err)
		httpError(w, "failed to create shipment", http.StatusInternalServerError)
		return
	}

	publishEvent("shipment.created", map[string]interface{}{
		"shipment_id":     shipmentID,
		"order_id":        req.OrderID,
		"tracking_number": trackingNumber,
		"carrier":         carrier,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"shipment_id":        shipmentID,
		"tracking_number":    trackingNumber,
		"carrier":            carrier,
		"estimated_delivery": estimatedDelivery.Format("2006-01-02"),
	})
}

// shipmentRank orders the steps of a shipment's life. A shipment only moves forward.
var shipmentRank = map[string]int{"label_created": 0, "in_transit": 1, "delivered": 2}

var shipmentSteps = map[string]string{
	"in_transit": "Handed to the carrier",
	"delivered":  "Delivered",
	"cancelled":  "Shipment cancelled because the order was cancelled",
}

// handleShipmentByOrder moves an order's shipment to in_transit, delivered or cancelled.
// The worker calls it when the order's status changes (ALPHA-78). It is safe to repeat:
// asking for a step the shipment is already at or past changes nothing and still
// answers 200, so a retried or out-of-order event never fails.
func handleShipmentByOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orderID, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/shipments/by-order/"))
	if err != nil || orderID < 1 {
		httpError(w, "order id must be a number", http.StatusBadRequest)
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if _, ok := shipmentSteps[req.Status]; !ok {
		httpError(w, "status must be in_transit, delivered or cancelled", http.StatusBadRequest)
		return
	}
	moveShipmentByOrder(w, orderID, req.Status, "", "")
}

// moveShipmentByOrder applies one status change to an order's shipment, forward only.
// location and description are optional; the shipment's city and the standard wording
// for the step are used when they are empty. Both the worker's PUT and the carrier
// webhook come through here, so they follow the same rules.
func moveShipmentByOrder(w http.ResponseWriter, orderID int, status, location, description string) {
	if description == "" {
		description = shipmentSteps[status]
	}

	tx, err := db.Begin()
	if err != nil {
		httpError(w, "failed to update shipment", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Lock the row so two events for one order can't interleave.
	var shipmentID int
	var current string
	var trackingCol, city sql.NullString
	err = tx.QueryRow(
		`SELECT id, status, tracking_number, city FROM shipments WHERE order_id = $1 FOR UPDATE`,
		orderID,
	).Scan(&shipmentID, &current, &trackingCol, &city)
	if err == sql.ErrNoRows {
		httpError(w, "no shipment for this order", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("update shipment: read order %d: %v", orderID, err)
		httpError(w, "failed to update shipment", http.StatusInternalServerError)
		return
	}

	tracking := trackingCol.String
	respond := func(changed bool, status string) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"shipment_id": shipmentID, "tracking_number": tracking, "status": status, "changed": changed,
		})
	}

	// Cancelled and delivered are final. For the rest, only forward moves count.
	forward := false
	switch {
	case current == "cancelled" || current == "delivered":
	case status == "cancelled":
		forward = true
	default:
		forward = shipmentRank[status] > shipmentRank[current]
	}
	if !forward {
		log.Printf("Shipment for order %d is %s, ignoring request for %s", orderID, current, status)
		respond(false, current)
		return
	}

	if _, err := tx.Exec(
		`UPDATE shipments SET status = $1::text, updated_at = NOW(),
		        shipped_at = CASE WHEN $1::text IN ('in_transit', 'delivered') THEN COALESCE(shipped_at, NOW()) ELSE shipped_at END,
		        delivered_at = CASE WHEN $1::text = 'delivered' THEN NOW() ELSE delivered_at END
		 WHERE id = $2`,
		status, shipmentID,
	); err != nil {
		log.Printf("update shipment %d: %v", shipmentID, err)
		httpError(w, "failed to update shipment", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(
		`INSERT INTO tracking_events (shipment_id, status, location, description, occurred_at)
		 VALUES ($1, $2, $3, $4, NOW())`,
		shipmentID, status, orDefault(location, city.String), description,
	); err != nil {
		log.Printf("tracking event for shipment %d: %v", shipmentID, err)
		httpError(w, "failed to update shipment", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("update shipment %d: commit: %v", shipmentID, err)
		httpError(w, "failed to update shipment", http.StatusInternalServerError)
		return
	}

	publishEvent("shipment."+status, map[string]interface{}{
		"shipment_id":     shipmentID,
		"order_id":        orderID,
		"tracking_number": tracking,
		"status":          status,
	})
	respond(true, status)
}

func handleShipment(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/shipments/by-order/") {
		handleShipmentByOrder(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/shipments/")
	if id == "" {
		httpError(w, "shipment id required", http.StatusBadRequest)
		return
	}

	var s struct {
		ID                int     `json:"id"`
		OrderID           int     `json:"order_id"`
		Carrier           string  `json:"carrier"`
		TrackingNumber    string  `json:"tracking_number"`
		Status            string  `json:"status"`
		RecipientName     string  `json:"recipient_name"`
		City              string  `json:"city"`
		Country           string  `json:"country"`
		WeightKg          float64 `json:"weight_kg"`
		EstimatedDelivery string  `json:"estimated_delivery"`
		CreatedAt         string  `json:"created_at"`
	}

	err := db.QueryRow(
		`SELECT id, order_id, carrier, tracking_number, status, recipient_name,
		        city, country, weight_kg, estimated_delivery, created_at
		 FROM shipments WHERE id = $1`,
		id,
	).Scan(&s.ID, &s.OrderID, &s.Carrier, &s.TrackingNumber, &s.Status,
		&s.RecipientName, &s.City, &s.Country, &s.WeightKg, &s.EstimatedDelivery, &s.CreatedAt)

	if err != nil {
		httpError(w, "shipment not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

func handleTrack(w http.ResponseWriter, r *http.Request) {
	tracking := strings.TrimPrefix(r.URL.Path, "/track/")
	if tracking == "" {
		httpError(w, "tracking number required", http.StatusBadRequest)
		return
	}

	var shipmentID int
	var status string
	err := db.QueryRow(
		"SELECT id, status FROM shipments WHERE tracking_number = $1",
		tracking,
	).Scan(&shipmentID, &status)
	if err != nil {
		httpError(w, "tracking number not found", http.StatusNotFound)
		return
	}

	rows, err := db.Query(
		`SELECT status, location, description, occurred_at FROM tracking_events
		 WHERE shipment_id = $1 ORDER BY occurred_at DESC`,
		shipmentID,
	)
	if err != nil {
		httpError(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type Event struct {
		Status      string `json:"status"`
		Location    string `json:"location"`
		Description string `json:"description"`
		OccurredAt  string `json:"occurred_at"`
	}

	events := []Event{}
	for rows.Next() {
		var e Event
		rows.Scan(&e.Status, &e.Location, &e.Description, &e.OccurredAt)
		events = append(events, e)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tracking_number": tracking,
		"current_status":  status,
		"events":          events,
	})
}

func handleCarrierWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TrackingNumber string `json:"tracking_number"`
		Status         string `json:"status"`
		Location       string `json:"location"`
		Description    string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// The webhook is open to the internet, so it only accepts what a carrier can really
	// report. Cancelling is our own decision (the order was cancelled), never the carrier's.
	if req.TrackingNumber == "" {
		httpError(w, "tracking_number is required", http.StatusBadRequest)
		return
	}
	if req.Status != "in_transit" && req.Status != "delivered" {
		httpError(w, "status must be in_transit or delivered", http.StatusBadRequest)
		return
	}

	var orderID int
	err := db.QueryRow(
		"SELECT order_id FROM shipments WHERE tracking_number = $1",
		req.TrackingNumber,
	).Scan(&orderID)
	if err != nil {
		httpError(w, "tracking number not found", http.StatusNotFound)
		return
	}

	// Same forward-only, repeat-safe move the worker uses: a replayed or late "in_transit"
	// after "delivered" changes nothing, and a cancelled shipment stays cancelled.
	moveShipmentByOrder(w, orderID, req.Status, req.Location, req.Description)
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func generateTrackingNumber(carrier string) string {
	prefix := "RM"
	switch carrier {
	case "dpd":
		prefix = "DPD"
	case "hermes":
		prefix = "EVR"
	case "ups":
		prefix = "1Z"
	}
	return fmt.Sprintf("%s%d%d", prefix, time.Now().UnixNano(), rand.Intn(100000))
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
	log.Printf("Event -> SQS: %s", string(data))
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
