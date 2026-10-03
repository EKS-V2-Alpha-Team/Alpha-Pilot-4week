package main

import (
	"log"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	workerEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worker_events_total",
		Help: "Event processing attempts by type and result.",
	}, []string{"event_type", "result"}) // result: success | parse_failed | handle_failed | delete_failed

	workerEventDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "worker_event_duration_seconds",
		Help:    "Time spent handling one event.",
		Buckets: prometheus.DefBuckets,
	}, []string{"event_type"})

	workerReceiveErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "worker_receive_errors_total",
		Help: "Failed attempts to receive messages from SQS.",
	})
)

func init() {
	prometheus.MustRegister(workerEvents, workerEventDuration, workerReceiveErrors)
}

// Event types come from message content, so cap them to known values.
func eventTypeLabel(t string) string {
	switch t {
	case "order.created":
		return t
	}
	return "other"
}

// Separate port (default 9090) keeps /metrics off the health port.
func startMetricsServer(port string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	go func() {
		if err := http.ListenAndServe(":"+port, mux); err != nil {
			log.Printf("metrics server stopped: %v", err)
		}
	}()
}
