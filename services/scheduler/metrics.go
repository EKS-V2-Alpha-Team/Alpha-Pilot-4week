package main

import (
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	schedulerJobRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "scheduler_job_runs_total",
		Help: "Completed runs per scheduled job.",
	}, []string{"task"})

	// Jobs can take much longer than web requests, so the buckets go up to 5 minutes.
	schedulerJobDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "scheduler_job_duration_seconds",
		Help:    "How long each scheduled job run took.",
		Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 300},
	}, []string{"task"})

	schedulerJobLastCompleted = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "scheduler_job_last_completed_timestamp_seconds",
		Help: "Unix time each job last finished a run.",
	}, []string{"task"})
)

func init() {
	prometheus.MustRegister(schedulerJobRuns, schedulerJobDuration, schedulerJobLastCompleted)
}

// runJob runs one job and records it. Jobs don't return errors,
// so this records that a run finished, not whether it succeeded.
func runJob(name string, fn func()) {
	start := time.Now()
	fn()
	schedulerJobDuration.WithLabelValues(name).Observe(time.Since(start).Seconds())
	schedulerJobRuns.WithLabelValues(name).Inc()
	schedulerJobLastCompleted.WithLabelValues(name).SetToCurrentTime()
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
