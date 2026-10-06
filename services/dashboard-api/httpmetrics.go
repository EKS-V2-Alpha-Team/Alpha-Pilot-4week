package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests by method, route and status code.",
	}, []string{"method", "route", "status_code"})

	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency by method and route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
)

func init() { prometheus.MustRegister(httpRequests, httpDuration) }

// Probe endpoints: not customer traffic, so not counted.
var skipRoutes = map[string]bool{"/livez": true, "/healthz": true, "/dashboard/healthz": true}

// Clients can send any method string; cap it to known values to protect cardinality.
func normaliseMethod(m string) string {
	switch m {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return m
	}
	return "OTHER"
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Lets http.ResponseController reach the real writer (flushing, e.g. in the gateway's proxy).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func instrument(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, route := mux.Handler(r) // matched pattern, e.g. "/products/" — never the raw path
		if skipRoutes[route] {
			mux.ServeHTTP(w, r)
			return
		}
		if route == "" {
			route = "unmatched"
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		mux.ServeHTTP(rec, r)

		method := normaliseMethod(r.Method)
		httpRequests.WithLabelValues(method, route, strconv.Itoa(rec.status)).Inc()
		httpDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
	})
}
