package main

import (
	"database/sql"
	"log"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// startMetricsServer exposes Prometheus metrics on their own port.
//
// A separate port (default 9090) keeps /metrics off the application port, so the
// api-gateway, which proxies /api/inventory/* to the application port, can never
// route internet traffic to it. Prometheus scrapes this port inside the cluster.
//
// Besides Go runtime and process metrics (registered by default), it adds the
// database connection-pool stats from db.Stats(): connections open / in use / idle,
// and how often and how long requests waited for a free connection.
func startMetricsServer(db *sql.DB, port string) {
	prometheus.MustRegister(collectors.NewDBStatsCollector(db, "orders"))

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	go func() {
		log.Printf("Metrics listening on :%s", port)
		if err := http.ListenAndServe(":"+port, mux); err != nil {
			log.Printf("metrics server error: %v", err)
		}
	}()
}
